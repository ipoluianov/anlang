package codegen

import (
	"go/constant"
	"math/big"
	"strconv"

	"an/ir"
	"an/types"
)

// Constant data (comp.gen.values).

// constLoc returns the location of a constant.
func (g *gen) constLoc(c *ir.Const) *loc {
	t := c.Typ
	if c.Computed != nil {
		return at(mref{kind: mStatic, sym: g.constData(c)}, t)
	}
	if c.Val == nil {
		return &loc{t: t, zero: true}
	}
	switch c.Val.Kind() {
	case constant.Bool:
		return boolLocT(constant.BoolVal(c.Val), t)
	case constant.String:
		s := constant.StringVal(c.Val)
		if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&types.IsTerminated != 0 {
			term := "\x00"
			if b.Kind() == types.Dstring {
				term = "$"
			}
			return &loc{t: t, parts: []operand{{kind: opSym, sym: g.stringData(s + term), width: g.ptr * 8}}}
		}
		l := g.stringLoc(s)
		l.t = t
		return l
	case constant.Int:
		return &loc{t: t, parts: g.constParts(intBits(c.Val), g.sizeof(t))}
	}
	g.unsupported(15, "floating-point constants")
	return &loc{t: t, zero: true}
}

func boolLocT(b bool, t types.Type) *loc {
	l := boolLoc(b)
	l.t = t
	return l
}

// intBits returns the two's complement bits of an integer constant.
func intBits(v constant.Value) uint64 {
	if u, ok := constant.Uint64Val(v); ok {
		return u
	}
	if s, ok := constant.Int64Val(v); ok {
		return uint64(s)
	}
	b, _ := new(big.Int).SetString(v.ExactString(), 10)
	return b.Uint64()
}

// stringLoc returns the location of a string constant: the address of
// its bytes in the constant data and its length.
func (g *gen) stringLoc(s string) *loc {
	parts := []operand{{kind: opSym, sym: g.stringData(s), width: g.ptr * 8}}
	parts = append(parts, g.constParts(uint64(len(s)), g.intBits/8)...)
	return &loc{t: types.Typ[types.String], parts: parts}
}

// stringData returns the label of the bytes of a string constant.
func (g *gen) stringData(s string) string {
	if l, ok := g.strs[s]; ok {
		return l
	}
	l := "S" + strconv.Itoa(len(g.strs))
	g.strs[s] = l
	g.data = append(g.data, &dataItem{label: l, bytes: []byte(s), align: 1})
	return l
}

// constData returns the label of the data of a computed constant.
func (g *gen) constData(c *ir.Const) string {
	if l, ok := g.consts[c]; ok {
		return l
	}
	l := "D" + strconv.Itoa(len(g.data))
	g.consts[c] = l
	d := &dataItem{label: l, bytes: make([]byte, g.sizeof(c.Typ)), align: g.alignof(c.Typ)}
	g.data = append(g.data, d)
	g.serialize(d, 0, c.Computed, c.Typ)
	return l
}

// serialize writes the computed value v of type t at offset off of d.
func (g *gen) serialize(d *dataItem, off int, v any, t types.Type) {
	putInt := func(x uint64, size int) {
		for k := 0; k < size; k++ {
			d.bytes[off+k] = byte(x >> uint(8*k))
		}
	}
	if _, _, _, ok := g.intInfo(t); ok {
		if c, ok := v.(constant.Value); ok && c != nil {
			putInt(intBits(constant.ToInt(c)), g.sizeof(t))
		}
		return
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		c, _ := v.(constant.Value)
		switch {
		case c == nil:
		case u.Info()&types.IsBoolean != 0:
			if constant.BoolVal(c) {
				d.bytes[off] = 1
			}
		case u.Info()&types.IsString != 0:
			s := constant.StringVal(c)
			d.relocs = append(d.relocs, reloc{off: off, sym: g.stringData(s)})
			off += g.ptr
			putInt(uint64(len(s)), g.intBits/8)
		default:
			g.unsupported(15, "floating-point constants")
		}
	case *types.Struct:
		comp, _ := v.(*types.Composite)
		if comp == nil {
			return
		}
		for k, f := range u.Fields {
			g.serialize(d, off+g.fieldOffset(u, k), comp.Elems[k], f.Type())
		}
	case *types.Array:
		comp, _ := v.(*types.Composite)
		if comp == nil {
			return
		}
		es := g.sizeof(u.Elem)
		for k, e := range comp.Elems {
			g.serialize(d, off+k*es, e, u.Elem)
		}
	case *types.Slice:
		comp, _ := v.(*types.Composite)
		if comp == nil || len(comp.Elems) == 0 {
			return
		}
		es := g.sizeof(u.Elem)
		l := "D" + strconv.Itoa(len(g.data))
		e := &dataItem{label: l, bytes: make([]byte, es*len(comp.Elems)), align: g.alignof(u.Elem)}
		g.data = append(g.data, e)
		for k, x := range comp.Elems {
			g.serialize(e, k*es, x, u.Elem)
		}
		d.relocs = append(d.relocs, reloc{off: off, sym: l})
		off += g.ptr
		putInt(uint64(len(comp.Elems)), g.intBits/8)
		off += g.intBits / 8
		putInt(uint64(len(comp.Elems)), g.intBits/8)
	case *types.Enum:
		comp, _ := v.(*types.Composite)
		if comp == nil {
			return
		}
		putInt(uint64(comp.Variant), g.tagSize(u))
		for k, f := range u.Variants[comp.Variant].Fields() {
			g.serialize(d, off+g.variantFieldOffset(u, comp.Variant, k), comp.Elems[k], f.Type())
		}
	case *types.Interface:
		iv, _ := v.(*types.IfaceValue)
		if iv == nil {
			return
		}
		putInt(uint64(g.typeNum(iv.Type)), g.word)
		if g.sizeof(iv.Type) <= 8 {
			g.serialize(d, off+g.word, iv.Value, iv.Type)
			return
		}
		l := "D" + strconv.Itoa(len(g.data))
		e := &dataItem{label: l, bytes: make([]byte, g.sizeof(iv.Type)), align: g.alignof(iv.Type)}
		g.data = append(g.data, e)
		g.serialize(e, 0, iv.Value, iv.Type)
		d.relocs = append(d.relocs, reloc{off: off + g.word, sym: l})
	default:
		g.unsupported(25, "constants of type "+t.String())
	}
}
