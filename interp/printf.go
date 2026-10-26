package interp

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"an/types"
)

// spec is a parsed formatting directive of fmt.Printf.
type spec struct {
	flags      string
	wid, prec  int
	hasWid     bool
	hasPrec    bool
	verb       rune
	plus, hash bool
}

// directive returns the directive as a Go format string, for formatting
// a primitive value with Go's package fmt, which defines the formats
// (std.fmt.print).
func (s spec) directive(verb rune) string {
	var b strings.Builder
	b.WriteString("%" + s.flags)
	if s.hasWid {
		b.WriteString(strconv.Itoa(s.wid))
	}
	if s.hasPrec {
		b.WriteString("." + strconv.Itoa(s.prec))
	}
	b.WriteRune(verb)
	return b.String()
}

// sprintf formats args, the arguments of fmt.Printf or fmt.Sprintf after
// the format string, as package fmt of Go does. The checker has verified
// the directives against the static types of the arguments
// (ct.reject.precondition).
func (m *machine) sprintf(format string, args []value) string {
	var b strings.Builder
	argNum := 0
	next := func() (value, bool) {
		if argNum >= len(args) {
			return nil, false
		}
		argNum++
		return args[argNum-1], true
	}
	intArg := func() (int, bool) {
		a, ok := next()
		if !ok {
			return 0, false
		}
		if x, ok := a.(iface); ok {
			a = x.v
		}
		switch v := a.(type) {
		case int64:
			return int(v), true
		case uint64:
			return int(v), true
		}
		return 0, false
	}
	for i := 0; i < len(format); {
		if format[i] != '%' {
			j := strings.IndexByte(format[i:], '%')
			if j < 0 {
				j = len(format) - i
			}
			b.WriteString(format[i : i+j])
			i += j
			continue
		}
		i++
		var s spec
		start := i
		for i < len(format) && strings.IndexByte("+-# 0", format[i]) >= 0 {
			i++
		}
		s.flags = format[start:i]
		s.plus = strings.Contains(s.flags, "+")
		s.hash = strings.Contains(s.flags, "#")
		if i < len(format) && format[i] == '*' {
			i++
			s.wid, s.hasWid = intArg()
			if s.wid < 0 {
				s.wid, s.flags = -s.wid, s.flags+"-"
			}
		} else {
			for i < len(format) && '0' <= format[i] && format[i] <= '9' {
				s.wid = s.wid*10 + int(format[i]-'0')
				s.hasWid = true
				i++
			}
		}
		if i < len(format) && format[i] == '.' {
			i++
			s.hasPrec = true
			if i < len(format) && format[i] == '*' {
				i++
				s.prec, _ = intArg()
				if s.prec < 0 {
					s.hasPrec = false
				}
			} else {
				for i < len(format) && '0' <= format[i] && format[i] <= '9' {
					s.prec = s.prec*10 + int(format[i]-'0')
					i++
				}
			}
		}
		if i >= len(format) {
			b.WriteString("%!(NOVERB)")
			break
		}
		verb, size := utf8.DecodeRuneInString(format[i:])
		i += size
		s.verb = verb
		if verb == '%' {
			b.WriteByte('%')
			continue
		}
		a, ok := next()
		if !ok {
			b.WriteString("%!" + string(verb) + "(MISSING)")
			continue
		}
		var t types.Type
		if x, ok := a.(iface); ok {
			a, t = x.v, x.t
		}
		m.formatArg(&b, s, a, t)
	}
	if argNum < len(args) {
		b.WriteString("%!(EXTRA ")
		for j, a := range args[argNum:] {
			if j > 0 {
				b.WriteString(", ")
			}
			var t types.Type
			if x, ok := a.(iface); ok {
				a, t = x.v, x.t
			}
			b.WriteString(m.typeName(t) + "=")
			m.formatArg(&b, spec{verb: 'v'}, a, t)
		}
		b.WriteString(")")
	}
	return b.String()
}

// typeName writes a type as %T does: named types of the program qualified
// by their package name.
func (m *machine) typeName(t types.Type) string {
	if t == nil {
		return "<nil>"
	}
	if n, ok := t.(*types.Named); ok && n.Obj.Pkg() != nil {
		return n.Obj.Pkg().Name + "." + n.String()
	}
	return t.String()
}

// formatArg formats value v of dynamic type t under directive s.
func (m *machine) formatArg(b *strings.Builder, s spec, v value, t types.Type) {
	if t == nil {
		if s.verb == 'v' || s.verb == 'T' {
			b.WriteString(fmt.Sprintf(s.directive('v'), "<nil>"))
		} else {
			b.WriteString("%!" + string(s.verb) + "(<nil>)")
		}
		return
	}
	if s.verb == 'T' {
		b.WriteString(fmt.Sprintf(s.directive('s'), m.typeName(t)))
		return
	}
	// Error and String methods (std.fmt.print).
	if strings.ContainsRune("vsqxX", s.verb) && !(s.hash && s.verb == 'v') {
		if ms := m.prog.Methods(t); ms != nil {
			for _, name := range []string{"Error", "String"} {
				if f := ms.Methods[name]; f != nil && f.Sig.Params.Len() == 0 && f.Sig.Results.Len() == 1 {
					str := m.call(m.info(f), []value{share(v)}, nil).(string)
					verb := s.verb
					if verb == 'v' {
						verb = 's'
					}
					b.WriteString(fmt.Sprintf(s.directive(verb), str))
					return
				}
			}
		}
	}
	if x, ok := v.(iface); ok {
		m.formatArg(b, s, x.v, x.t)
		return
	}
	bad := func() {
		b.WriteString("%!" + string(s.verb) + "(" + m.typeName(t) + "=")
		m.formatArg(b, spec{verb: 'v'}, v, t)
		b.WriteString(")")
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		var x any
		switch v := v.(type) {
		case bool:
			x = v
		case int64:
			x = v
		case uint64:
			x = v
		case float64:
			x = v
			if u.Kind() == types.F32 {
				x = float32(v)
			}
		case string:
			x = v
		}
		if !basicVerbOK(s.verb, u) {
			bad()
			return
		}
		b.WriteString(fmt.Sprintf(s.directive(s.verb), x))
	case *types.Slice:
		sv := v.(sliceVal)
		if eb, ok := u.Elem.Underlying().(*types.Basic); ok && eb.Kind() == types.U8 && strings.ContainsRune("sqxX", s.verb) {
			bs := make([]byte, sv.len)
			for i := range sv.len {
				bs[i] = byte(sv.buf.elems[sv.off+i].(uint64))
			}
			b.WriteString(fmt.Sprintf(s.directive(s.verb), bs))
			return
		}
		b.WriteString("[")
		for i := range sv.len {
			if i > 0 {
				b.WriteString(" ")
			}
			m.formatArg(b, s, sv.buf.elems[sv.off+i], u.Elem)
		}
		b.WriteString("]")
	case *types.Array:
		a := v.(*aggregate)
		b.WriteString("[")
		for i, e := range a.elems {
			if i > 0 {
				b.WriteString(" ")
			}
			m.formatArg(b, s, e, u.Elem)
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
				m.formatArg(b, s, mp.keys[i], u.Key)
				b.WriteString(":")
				m.formatArg(b, s, mp.vals[i], u.Elem)
			}
		}
		b.WriteString("]")
	case *types.Struct:
		a := v.(*aggregate)
		b.WriteString("{")
		for i, f := range u.Fields {
			if i > 0 {
				b.WriteString(" ")
			}
			if s.plus && s.verb == 'v' {
				b.WriteString(f.Name() + ":")
			}
			m.formatArg(b, s, a.elems[i], f.Type())
		}
		b.WriteString("}")
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
				m.formatArg(b, s, a.elems[i], f.Type())
			}
			b.WriteString("}")
		}
	case *types.Signature:
		if v.(*closure) == nil {
			b.WriteString(fmt.Sprintf(s.directive('v'), "<nil>"))
		} else {
			b.WriteString(fmt.Sprintf(s.directive('v'), "0x1"))
		}
	default:
		bad()
	}
}

// basicVerbOK reports whether verb applies to a value of basic type b.
func basicVerbOK(verb rune, b *types.Basic) bool {
	if verb == 'v' {
		return true
	}
	switch {
	case b.Info()&types.IsBoolean != 0:
		return verb == 't'
	case b.Info()&types.IsInteger != 0:
		return strings.ContainsRune("bcdoOqxXU", verb)
	case b.Info()&types.IsFloat != 0:
		return strings.ContainsRune("beEfFgGxX", verb)
	case b.Info()&(types.IsString|types.IsTerminated) != 0:
		return strings.ContainsRune("sqxX", verb)
	}
	return false
}
