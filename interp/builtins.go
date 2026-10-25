package interp

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"an/ir"
	"an/types"
)

func (m *machine) builtin(b *ir.Builtin, c *ir.Call, args []value) value {
	switch b.Bname {
	case "len":
		return int64(m.length(args[0]))
	case "cap":
		switch x := args[0].(type) {
		case sliceVal:
			return int64(x.cap)
		case *aggregate:
			return int64(len(x.elems))
		}
		return int64(0)
	case "append":
		s := args[0].(sliceVal)
		return m.appendValues(s, args[1:], sliceElem(b.Typ))
	case "appendslice":
		s, t := args[0].(sliceVal), args[1].(sliceVal)
		vals := make([]value, t.len)
		for i := range t.len {
			vals[i] = share(t.buf.elems[t.off+i])
		}
		return m.appendValues(s, vals, sliceElem(b.Typ))
	case "appendstring":
		s, str := args[0].(sliceVal), args[1].(string)
		vals := make([]value, len(str))
		for i := 0; i < len(str); i++ {
			vals[i] = uint64(str[i])
		}
		return m.appendValues(s, vals, sliceElem(b.Typ))
	case "copy":
		dst := args[0].(addr)
		d := dst.load().(sliceVal)
		n := 0
		switch src := args[1].(type) {
		case sliceVal:
			n = min(d.len, src.len)
			if n > 0 {
				d = uniqueSlice(dst, d)
				tmp := make([]value, n)
				for i := range n {
					tmp[i] = share(src.buf.elems[src.off+i])
				}
				copy(d.buf.elems[d.off:], tmp)
			}
		case string:
			n = min(d.len, len(src))
			if n > 0 {
				d = uniqueSlice(dst, d)
				for i := range n {
					d.buf.elems[d.off+i] = uint64(src[i])
				}
			}
		}
		return int64(n)
	case "delete":
		if mp, _ := args[0].(addr).load().(*mapObj); mp != nil {
			uniqueMap(args[0].(addr)).delete(args[1])
		}
		return nil
	case "clear":
		a := args[0].(addr)
		switch x := a.load().(type) {
		case *mapObj:
			if x != nil {
				a.store((*mapObj)(nil))
			}
		case sliceVal:
			if x.len > 0 {
				x = uniqueSlice(a, x)
				et := c.Args[0].Type().(*ir.Pointer).Elem.Underlying().(*types.Slice).Elem
				for i := range x.len {
					x.buf.elems[x.off+i] = m.zero(et)
				}
			}
		}
		return nil
	case "min", "max":
		r := args[0]
		for _, a := range args[1:] {
			if b.Bname == "min" && m.binop(lssOp, b.Typ, a, r).(bool) || b.Bname == "max" && m.binop(lssOp, b.Typ, r, a).(bool) {
				r = a
			}
			// NaN is the result if any operand is NaN, as in Go.
			if f, ok := a.(float64); ok && f != f {
				r = a
			}
		}
		return r
	case "print", "println":
		var sb strings.Builder
		for i, a := range args {
			if i > 0 && b.Bname == "println" {
				sb.WriteString(" ")
			}
			m.formatRuntime(&sb, a, c.Args[i].Type())
		}
		if b.Bname == "println" {
			sb.WriteString("\n")
		}
		fmt.Fprint(m.opts.Stderr, sb.String())
		return nil
	case "fmt.Print", "fmt.Println":
		fmt.Fprint(m.opts.Stdout, m.sprint(b.Bname == "fmt.Println", c, args))
		return nil
	case "fmt.Sprint", "fmt.Sprintln":
		return m.sprint(b.Bname == "fmt.Sprintln", c, args)
	case "mapkeys":
		mp, _ := args[0].(*mapObj)
		var keys []value
		if mp != nil {
			keys = append(keys, mp.keys...)
		}
		return sliceVal{buf: newBuffer(keys), len: len(keys), cap: len(keys)}
	case "decoderune":
		s := args[0].(string)
		i := int(args[1].(int64))
		r, w := utf8.DecodeRuneInString(s[i:])
		return []value{int64(r), int64(w)}
	case "Option.Get":
		o := args[0].(*aggregate)
		if o.variant == 1 {
			return []value{o.elems[0], true}
		}
		tu := b.Typ.(*types.Tuple)
		return []value{m.zero(tu.At(0).Type()), false}
	}
	panic("interp: built-in " + b.Bname)
}

// appendValues appends vals to s. The buffer of s is reused if s ends at
// the end of the used part of an unshared buffer; otherwise the elements
// are copied, so no other slice observes the new elements
// (mem.value.copy).
func (m *machine) appendValues(s sliceVal, vals []value, et types.Type) value {
	if len(vals) == 0 {
		return s
	}
	n := s.len + len(vals)
	if s.buf != nil && s.buf.refs <= 1 && s.off+s.len == s.buf.used && n <= s.cap {
		copy(s.buf.elems[s.off+s.len:], vals)
		s.buf.used += len(vals)
		s.len = n
		return s
	}
	c := max(n, 2*s.cap, 4)
	elems := make([]value, c)
	for i := range s.len {
		elems[i] = share(s.buf.elems[s.off+i])
	}
	copy(elems[s.len:], vals)
	for i := n; i < c; i++ {
		elems[i] = m.zero(et)
	}
	return sliceVal{buf: &buffer{elems: elems, used: n, refs: 1}, len: n, cap: c}
}

// sprint formats args as fmt.Sprint does, or as fmt.Sprintln if ln is
// set.
func (m *machine) sprint(ln bool, c *ir.Call, args []value) string {
	var sb strings.Builder
	for i, a := range args {
		t := c.Args[i].Type()
		if i > 0 && (ln || !isStringArg(a, t) && !isStringArg(args[i-1], c.Args[i-1].Type())) {
			sb.WriteString(" ")
		}
		m.formatPrint(&sb, a, t)
	}
	if ln {
		sb.WriteString("\n")
	}
	return sb.String()
}

func isStringArg(v value, t types.Type) bool {
	if x, ok := v.(iface); ok {
		if x.t == nil {
			return false
		}
		v, t = x.v, x.t
	}
	_, ok := v.(string)
	return ok
}

func sliceElem(t types.Type) types.Type { return t.Underlying().(*types.Slice).Elem }
