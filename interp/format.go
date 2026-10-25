package interp

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"an/syntax"
	"an/types"
)

const lssOp = syntax.Lss

// formatPrint formats a value as the verb %v of fmt.Print does: values
// with an Error or String method print the result of the method.
func (m *machine) formatPrint(b *strings.Builder, v value, t types.Type) {
	if x, ok := v.(iface); ok {
		if x.t == nil {
			b.WriteString("<nil>")
			return
		}
		v, t = x.v, x.t
	}
	if ms := m.prog.Methods(t); ms != nil {
		for _, name := range []string{"Error", "String"} {
			if f := ms.Methods[name]; f != nil && f.Sig.Params.Len() == 0 && f.Sig.Results.Len() == 1 {
				fmt.Fprint(b, m.call(m.info(f), []value{share(v)}, nil))
				return
			}
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch v := v.(type) {
		case bool:
			b.WriteString(strconv.FormatBool(v))
		case int64:
			b.WriteString(strconv.FormatInt(v, 10))
		case uint64:
			b.WriteString(strconv.FormatUint(v, 10))
		case float64:
			bits := 64
			if u.Kind() == types.F32 {
				bits = 32
			}
			b.WriteString(formatFloat(v, bits))
		case string:
			b.WriteString(v)
		}
	case *types.Struct:
		a := v.(*aggregate)
		b.WriteString("{")
		for i, f := range u.Fields {
			if i > 0 {
				b.WriteString(" ")
			}
			m.formatPrint(b, a.elems[i], f.Type())
		}
		b.WriteString("}")
	case *types.Array:
		a := v.(*aggregate)
		b.WriteString("[")
		for i, e := range a.elems {
			if i > 0 {
				b.WriteString(" ")
			}
			m.formatPrint(b, e, u.Elem)
		}
		b.WriteString("]")
	case *types.Slice:
		s := v.(sliceVal)
		b.WriteString("[")
		for i := range s.len {
			if i > 0 {
				b.WriteString(" ")
			}
			m.formatPrint(b, s.buf.elems[s.off+i], u.Elem)
		}
		b.WriteString("]")
	case *types.Map:
		mp, _ := v.(*mapObj)
		b.WriteString("map[")
		if mp != nil {
			for n, i := range sortedKeys(mp, u) {
				if n > 0 {
					b.WriteString(" ")
				}
				m.formatPrint(b, mp.keys[i], u.Key)
				b.WriteString(":")
				m.formatPrint(b, mp.vals[i], u.Elem)
			}
		}
		b.WriteString("]")
	case *types.Enum:
		a := v.(*aggregate)
		variant := u.Variants[a.variant]
		b.WriteString(variant.Name())
		if variant.HasData() {
			b.WriteString("{")
			for i, f := range variant.Fields() {
				if i > 0 {
					b.WriteString(" ")
				}
				m.formatPrint(b, a.elems[i], f.Type())
			}
			b.WriteString("}")
		}
	case *types.Signature:
		if v.(*closure) == nil {
			b.WriteString("<nil>")
		} else {
			b.WriteString("0x1")
		}
	default:
		fmt.Fprintf(b, "%v", v)
	}
}

// formatFloat formats like %v: the shortest representation, with an
// exponent if it is less than -4 or at least 6.
func formatFloat(f float64, bits int) string {
	if math.IsInf(f, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, bits)
}

// formatRuntime formats a value as the built-in print does, which
// follows the run-time printing of Go.
func (m *machine) formatRuntime(b *strings.Builder, v value, t types.Type) {
	switch v := v.(type) {
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case uint64:
		b.WriteString(strconv.FormatUint(v, 10))
	case float64:
		b.WriteString(runtimeFloat(v))
	case string:
		b.WriteString(v)
	case iface:
		if v.t == nil {
			b.WriteString("(0x0,0x0)")
			return
		}
		b.WriteString("(0x1,0x1)")
	case sliceVal:
		fmt.Fprintf(b, "[%d/%d]%s", v.len, v.cap, pointer(v.buf != nil))
	case *mapObj:
		b.WriteString(pointer(v != nil))
	case *closure:
		b.WriteString(pointer(v != nil))
	case nil: // a nil channel
		b.WriteString(pointer(false))
	default:
		b.WriteString("0x1")
	}
}

// runtimeFloat formats a float as the Go run-time print does: +d.dddddde+dd.
func runtimeFloat(v float64) string {
	switch {
	case v != v:
		return "NaN"
	case v+v == v && v > 0:
		return "+Inf"
	case v+v == v && v < 0:
		return "-Inf"
	}
	const n = 7
	var buf [n + 7]byte
	buf[0] = '+'
	e := 0
	if v == 0 {
		if 1/v < 0 {
			buf[0] = '-'
		}
	} else {
		if v < 0 {
			v = -v
			buf[0] = '-'
		}
		for v >= 10 {
			e++
			v /= 10
		}
		for v < 1 {
			e--
			v *= 10
		}
		h := 5.0
		for i := 0; i < n; i++ {
			h /= 10
		}
		v += h
		if v >= 10 {
			e++
			v /= 10
		}
	}
	for i := 0; i < n; i++ {
		s := int(v)
		buf[i+2] = byte(s + '0')
		v -= float64(s)
		v *= 10
	}
	buf[1] = buf[2]
	buf[2] = '.'
	buf[n+2] = 'e'
	buf[n+3] = '+'
	if e < 0 {
		e = -e
		buf[n+3] = '-'
	}
	buf[n+4] = byte(e/100 + '0')
	buf[n+5] = byte(e/10)%10 + '0'
	buf[n+6] = byte(e%10) + '0'
	return string(buf[:])
}

// pointer stands for a pointer in run-time printing: the interpreter has
// no addresses, so every non-nil pointer prints the same.
func pointer(nonNil bool) string {
	if nonNil {
		return "0x1"
	}
	return "0x0"
}
