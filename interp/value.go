// Package interp is the IR interpreter (spec/22-compiler.md §22.4): it
// runs a program in IR form. It is the reference against which the
// backends are checked (conf.backend.ir).
package interp

import (
	"fmt"
	"go/constant"
	"math"
	"sort"
	"strings"

	"an/ir"
	"an/types"
)

// Runtime values:
//
//	bool                    bool
//	signed integers         int64, normalized to the size of the type
//	unsigned integers       uint64, normalized to the size of the type
//	f32, f64                float64 (f32 rounded to binary32)
//	string types            string
//	struct, array, enum     *aggregate
//	slice                   sliceVal
//	map                     *mapObj (nil for an empty map)
//	interface               iface
//	function                *closure (nil for nil)
//	address                 addr
//	tuple                   []value
type value = any

// aggregate holds the fields of a struct, the elements of an array, or
// the variant and fields of an enum value. refs counts the locations
// that share it: a write through an address copies a shared aggregate
// first (copy-on-write, spec/22-compiler.md §22.6).
type aggregate struct {
	elems   []value
	variant int
	refs    int
}

// buffer is the storage of slices: len(elems) is the capacity. Elements
// from used on are not visible to any slice yet, so append may write them
// in place.
type buffer struct {
	elems []value
	used  int
	refs  int
}

func newBuffer(elems []value) *buffer {
	return &buffer{elems: elems, used: len(elems), refs: 1}
}

type sliceVal struct {
	buf           *buffer
	off, len, cap int
}

type mapObj struct {
	keys  []value
	vals  []value
	index map[string]int
	refs  int
}

type iface struct {
	t types.Type // nil for a nil interface value
	v value
}

type closure struct {
	fn  *funcInfo
	env []value
}

// share records one more location sharing v (a logical copy,
// mem.value.copy).
func share(v value) value {
	switch v := v.(type) {
	case *aggregate:
		v.refs++
	case sliceVal:
		if v.buf != nil {
			v.buf.refs++
		}
	case *mapObj:
		if v != nil {
			v.refs++
		}
	case iface:
		share(v.v)
	}
	return v
}

func (a *aggregate) clone() *aggregate {
	n := &aggregate{elems: make([]value, len(a.elems)), variant: a.variant, refs: 1}
	for i, e := range a.elems {
		n.elems[i] = share(e)
	}
	return n
}

// addr is the address of a storage location.
type addr interface {
	load() value
	store(value)
}

// cell is a variable.
type cell struct {
	v value
}

func (c *cell) load() value   { return c.v }
func (c *cell) store(v value) { c.v = v }

// fieldRef is the address of a field of the struct at base.
type fieldRef struct {
	base addr
	i    int
}

func (r *fieldRef) load() value { return r.base.load().(*aggregate).elems[r.i] }
func (r *fieldRef) store(v value) {
	uniqueAggregate(r.base).elems[r.i] = v
}

// uniqueAggregate returns the aggregate at base, copied first if it is
// shared, so that it can be modified. The values that contain it are
// made unique first: a part of a shared value is shared too.
func uniqueAggregate(base addr) *aggregate {
	unshareParent(base)
	a := base.load().(*aggregate)
	if a.refs > 1 {
		a.refs--
		a = a.clone()
		base.store(a)
	}
	return a
}

// unshareParent makes the value that contains the location r unique,
// if r is a part of a value.
func unshareParent(r addr) {
	switch r := r.(type) {
	case *fieldRef:
		uniqueAggregate(r.base)
	case *elemRef:
		switch x := r.base.load().(type) {
		case *aggregate:
			uniqueAggregate(r.base)
		case sliceVal:
			uniqueSlice(r.base, x)
		}
	}
}

// elemRef is the address of element i of the array or slice at base.
type elemRef struct {
	base addr
	i    int
}

func (r *elemRef) load() value {
	switch x := r.base.load().(type) {
	case *aggregate:
		return x.elems[r.i]
	case sliceVal:
		return x.buf.elems[x.off+r.i]
	}
	panic("interp: bad element address")
}

func (r *elemRef) store(v value) {
	switch x := r.base.load().(type) {
	case *aggregate:
		uniqueAggregate(r.base).elems[r.i] = v
	case sliceVal:
		x = uniqueSlice(r.base, x)
		x.buf.elems[x.off+r.i] = v
		if _, ok := r.base.(*subRef); ok {
			r.base.store(x) // write back to the operand of the subslice
		}
	}
}

// subRef is the address of the elements lo to hi-1 of the slice or array
// at base (ir.SubsliceAddr).
type subRef struct {
	base   addr
	lo, hi int
}

// load returns a copy of the elements.
func (r *subRef) load() value {
	n := r.hi - r.lo
	elems := make([]value, n)
	switch x := r.base.load().(type) {
	case *aggregate:
		for i := range elems {
			elems[i] = share(x.elems[r.lo+i])
		}
	case sliceVal:
		for i := range elems {
			elems[i] = share(x.buf.elems[x.off+r.lo+i])
		}
	}
	return sliceVal{buf: newBuffer(elems), len: n, cap: n}
}

// store writes the elements of v back to base.
func (r *subRef) store(v value) {
	s := v.(sliceVal)
	if s.len != r.hi-r.lo {
		panic(runtimeError(fmt.Sprintf("length of an inout subslice changed from %d to %d", r.hi-r.lo, s.len)))
	}
	var dst []value
	switch x := r.base.load().(type) {
	case *aggregate:
		dst = uniqueAggregate(r.base).elems[r.lo:r.hi]
	case sliceVal:
		x = uniqueSlice(r.base, x)
		dst = x.buf.elems[x.off+r.lo : x.off+r.hi]
	}
	for i := range dst {
		dst[i] = share(s.buf.elems[s.off+i])
	}
}

// uniqueSlice returns slice s stored at base with a buffer that is not
// shared, copying the buffer first if needed.
func uniqueSlice(base addr, s sliceVal) sliceVal {
	if _, ok := base.(*subRef); !ok {
		unshareParent(base)
		s = base.load().(sliceVal)
	}
	if s.buf.refs > 1 {
		s.buf.refs--
		nb := &buffer{elems: make([]value, s.cap), used: s.len, refs: 1}
		for i := range s.cap {
			nb.elems[i] = share(s.buf.elems[s.off+i])
		}
		s = sliceVal{buf: nb, off: 0, len: s.len, cap: s.cap}
		base.store(s)
	}
	return s
}

// uniqueMap returns the map at base, copied first if it is shared, or a
// new map if it is empty.
func uniqueMap(base addr) *mapObj {
	m, _ := base.load().(*mapObj)
	switch {
	case m == nil:
		m = &mapObj{index: map[string]int{}, refs: 1}
		base.store(m)
	case m.refs > 1:
		m.refs--
		n := &mapObj{index: map[string]int{}, refs: 1}
		for i, k := range m.keys {
			n.index[keyString(k)] = len(n.keys)
			n.keys = append(n.keys, k)
			n.vals = append(n.vals, share(m.vals[i]))
		}
		m = n
		base.store(m)
	}
	return m
}

func (m *mapObj) lookup(k value) (value, bool) {
	if m == nil {
		return nil, false
	}
	i, ok := m.index[keyString(k)]
	if !ok {
		return nil, false
	}
	return m.vals[i], true
}

func (m *mapObj) set(k, v value) {
	ks := keyString(k)
	if i, ok := m.index[ks]; ok {
		m.vals[i] = v
		return
	}
	m.index[ks] = len(m.keys)
	m.keys = append(m.keys, k)
	m.vals = append(m.vals, v)
}

func (m *mapObj) delete(k value) {
	ks := keyString(k)
	i, ok := m.index[ks]
	if !ok {
		return
	}
	delete(m.index, ks)
	m.keys = append(m.keys[:i:i], m.keys[i+1:]...)
	m.vals = append(m.vals[:i:i], m.vals[i+1:]...)
	for j := i; j < len(m.keys); j++ {
		m.index[keyString(m.keys[j])] = j
	}
}

func (m *mapObj) len() int {
	if m == nil {
		return 0
	}
	return len(m.keys)
}

// keyString encodes a comparable value as a map key.
func keyString(v value) string {
	var b strings.Builder
	writeKey(&b, v)
	return b.String()
}

func writeKey(b *strings.Builder, v value) {
	switch v := v.(type) {
	case nil:
		b.WriteString("n")
	case bool:
		fmt.Fprintf(b, "b%t", v)
	case int64:
		fmt.Fprintf(b, "i%d", v)
	case uint64:
		fmt.Fprintf(b, "u%d", v)
	case float64:
		if v == 0 {
			v = 0 // -0 and +0 are equal keys
		}
		fmt.Fprintf(b, "f%x", math.Float64bits(v))
	case string:
		fmt.Fprintf(b, "s%d:%s", len(v), v)
	case *aggregate:
		fmt.Fprintf(b, "a%d{", v.variant)
		for _, e := range v.elems {
			writeKey(b, e)
			b.WriteString(",")
		}
		b.WriteString("}")
	case iface:
		if v.t == nil {
			b.WriteString("I")
			return
		}
		if !types.Comparable(v.t) {
			panic(runtimeError("hash of unhashable type " + v.t.String()))
		}
		fmt.Fprintf(b, "I%s|", v.t)
		writeKey(b, v.v)
	default:
		panic(runtimeError(fmt.Sprintf("hash of unhashable value %T", v)))
	}
}

// zero returns the zero value of type t.
func (m *machine) zero(t types.Type) value {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return false
		case u.Info()&types.IsUnsigned != 0:
			return uint64(0)
		case u.Info()&types.IsInteger != 0:
			return int64(0)
		case u.Info()&types.IsFloat != 0:
			return float64(0)
		case u.Info()&(types.IsString|types.IsTerminated) != 0:
			return ""
		}
	case *types.Struct:
		m.alloc(len(u.Fields))
		a := &aggregate{elems: make([]value, len(u.Fields)), refs: 1}
		for i, f := range u.Fields {
			a.elems[i] = m.zero(f.Type())
		}
		return a
	case *types.Array:
		m.alloc(int(u.Len))
		a := &aggregate{elems: make([]value, u.Len), refs: 1}
		for i := range a.elems {
			a.elems[i] = m.zero(u.Elem)
		}
		return a
	case *types.Slice:
		return sliceVal{}
	case *types.Map:
		return (*mapObj)(nil)
	case *types.Interface:
		return iface{}
	case *types.Signature:
		return (*closure)(nil)
	case *types.Enum:
		// The first variant, without data (type.enum.zero).
		return &aggregate{variant: 0, refs: 1}
	}
	return nil
}

func sortedKeys(m *mapObj, t *types.Map) []int {
	idx := make([]int, m.len())
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return less(m.keys[idx[i]], m.keys[idx[j]]) })
	return idx
}

// less orders map keys for printing, as package fmt does.
func less(x, y value) bool {
	switch x := x.(type) {
	case int64:
		return x < y.(int64)
	case uint64:
		return x < y.(uint64)
	case float64:
		return x < y.(float64)
	case string:
		return x < y.(string)
	case bool:
		return !x && y.(bool)
	}
	return keyString(x) < keyString(y)
}

// pin makes v shared by every use, so that a write through an address
// copies it first: the value of a constant is never modified
// (ct.const.immutable).
func pin(v value) {
	switch v := v.(type) {
	case *aggregate:
		v.refs = 1 << 40
	case sliceVal:
		if v.buf != nil {
			v.buf.refs = 1 << 40
		}
	case *mapObj:
		if v != nil {
			v.refs = 1 << 40
		}
	case iface:
		pin(v.v)
	}
}

// computedValue returns the run-time value of the value cv of a computed
// constant of type t.
func (m *machine) computedValue(cv any, t types.Type) value {
	switch cv := cv.(type) {
	case nil:
		return m.zero(t)
	case constant.Value:
		return m.constValue(&ir.Const{Typ: t, Val: cv})
	case *types.IfaceValue:
		return iface{t: cv.Type, v: m.computedValue(cv.Value, cv.Type)}
	case *types.MapValue:
		mt := t.Underlying().(*types.Map)
		mp := &mapObj{index: map[string]int{}, refs: 1}
		for i, k := range cv.Keys {
			mp.set(m.computedValue(k, mt.Key), m.computedValue(cv.Vals[i], mt.Elem))
		}
		return mp
	case *types.Composite:
		elems := make([]value, len(cv.Elems))
		switch u := t.Underlying().(type) {
		case *types.Struct:
			for i, e := range cv.Elems {
				elems[i] = m.computedValue(e, u.Fields[i].Type())
			}
		case *types.Array:
			for i, e := range cv.Elems {
				elems[i] = m.computedValue(e, u.Elem)
			}
		case *types.Slice:
			for i, e := range cv.Elems {
				elems[i] = m.computedValue(e, u.Elem)
			}
			return sliceVal{buf: newBuffer(elems), len: len(elems), cap: len(elems)}
		case *types.Enum:
			fields := u.Variants[cv.Variant].Fields()
			for i, e := range cv.Elems {
				elems[i] = m.computedValue(e, fields[i].Type())
			}
		}
		return &aggregate{elems: elems, variant: cv.Variant, refs: 1}
	}
	panic(fmt.Sprintf("interp: computed constant %T", cv))
}

// constOf returns the value v of type t as the value of a computed
// constant (ct.const.types).
func (m *machine) constOf(v value, t types.Type) (any, error) {
	switch v := v.(type) {
	case bool:
		return constant.MakeBool(v), nil
	case int64:
		return constant.MakeInt64(v), nil
	case uint64:
		return constant.MakeUint64(v), nil
	case float64:
		return constant.MakeFloat64(v), nil
	case string:
		return constant.MakeString(v), nil
	case iface:
		if v.t == nil {
			return nil, nil
		}
		if u := v.t.Underlying(); isFuncOrChan(u) {
			return nil, fmt.Errorf("the dynamic value has type %s", v.t)
		}
		x, err := m.constOf(v.v, v.t)
		if err != nil {
			return nil, err
		}
		return &types.IfaceValue{Type: v.t, Value: x}, nil
	case *mapObj:
		if v == nil {
			return nil, nil
		}
		mt := t.Underlying().(*types.Map)
		mv := &types.MapValue{}
		for i, k := range v.keys {
			kc, err := m.constOf(k, mt.Key)
			if err != nil {
				return nil, err
			}
			vc, err := m.constOf(v.vals[i], mt.Elem)
			if err != nil {
				return nil, err
			}
			mv.Keys, mv.Vals = append(mv.Keys, kc), append(mv.Vals, vc)
		}
		return mv, nil
	case sliceVal:
		if v.buf == nil {
			return nil, nil
		}
		et := t.Underlying().(*types.Slice).Elem
		c := &types.Composite{}
		for i := range v.len {
			x, err := m.constOf(v.buf.elems[v.off+i], et)
			if err != nil {
				return nil, err
			}
			c.Elems = append(c.Elems, x)
		}
		return c, nil
	case *aggregate:
		c := &types.Composite{Variant: v.variant}
		for i, e := range v.elems {
			x, err := m.constOf(e, elemTypeOf(t, v.variant, i))
			if err != nil {
				return nil, err
			}
			c.Elems = append(c.Elems, x)
		}
		return c, nil
	}
	return nil, fmt.Errorf("a value of type %s cannot be a constant", t)
}

func isFuncOrChan(t types.Type) bool {
	switch t.(type) {
	case *types.Signature, *types.Chan:
		return true
	}
	return false
}
