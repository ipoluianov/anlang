package codegen

import (
	"fmt"

	"an/ir"
	"an/types"
)

// The layout of values in memory (comp.gen.layout).

// intInfo returns the width in bits of an integer type and whether it is
// signed and wrapping; ok is false for other types.
func (g *gen) intInfo(t types.Type) (bits int, signed, wrapping, ok bool) {
	b, isBasic := t.Underlying().(*types.Basic)
	if !isBasic {
		return 0, false, false, false
	}
	switch b.Kind() {
	case types.I8, types.U8, types.WrapU8, types.WrapI8:
		bits = 8
	case types.I16, types.U16, types.WrapU16, types.WrapI16:
		bits = 16
	case types.I32, types.U32, types.WrapU32, types.WrapI32:
		bits = 32
	case types.I64, types.U64, types.WrapU64, types.WrapI64:
		bits = 64
	case types.Int, types.Uint:
		bits = g.intBits
	case types.Usize:
		bits = g.ptr * 8
	default:
		return 0, false, false, false
	}
	info := b.Info()
	return bits, info&types.IsUnsigned == 0, info&types.IsWrapping != 0, true
}

// isString reports whether t is a string type.
func isString(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

// isBool reports whether t is a boolean type.
func isBool(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsBoolean != 0
}

// sizeof returns the size of a value of type t in bytes.
func (g *gen) sizeof(t types.Type) int {
	s, _ := g.layout(t)
	return s
}

// alignof returns the alignment of a value of type t in bytes.
func (g *gen) alignof(t types.Type) int {
	_, a := g.layout(t)
	return a
}

// layout returns the size and the alignment of type t.
func (g *gen) layout(t types.Type) (size, align int) {
	if l, ok := g.layouts[t]; ok {
		return l[0], l[1]
	}
	size, align = g.computeLayout(t)
	if size%align != 0 {
		size += align - size%align
	}
	g.layouts[t] = [2]int{size, align}
	return size, align
}

func (g *gen) scalarAlign(size int) int {
	return max(1, min(size, g.align))
}

func (g *gen) computeLayout(t types.Type) (int, int) {
	if p, ok := t.(*ir.Pointer); ok {
		_ = p
		return g.ptr, g.scalarAlign(g.ptr)
	}
	if bits, _, _, ok := g.intInfo(t); ok {
		return bits / 8, g.scalarAlign(bits / 8)
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return 1, 1
		case u.Info()&types.IsTerminated != 0:
			// cstring, dstring: the address of the bytes, which end with
			// the terminator.
			return g.ptr, g.scalarAlign(g.ptr)
		case u.Info()&types.IsString != 0:
			return g.ptr + g.intBits/8, g.scalarAlign(g.ptr)
		case u.Kind() == types.UntypedNil:
			return g.ptr, g.scalarAlign(g.ptr)
		case u.Info()&types.IsFloat != 0:
			g.unsupported(15, "floating-point types")
			return 8, g.scalarAlign(8)
		}
		g.fail("codegen: no layout for type %s", t)
		return 1, 1
	case *types.Struct:
		off, align := 0, 1
		for _, f := range u.Fields {
			s, a := g.layout(f.Type())
			off = roundUp(off, a) + s
			align = max(align, a)
		}
		return off, align
	case *types.Tuple:
		off, align := 0, 1
		for _, v := range u.Vars {
			s, a := g.layout(v.Type())
			off = roundUp(off, a) + s
			align = max(align, a)
		}
		return off, align
	case *types.Array:
		s, a := g.layout(u.Elem)
		return s * int(u.Len), a
	case *types.Slice:
		return g.ptr + 2*g.intBits/8, g.scalarAlign(g.ptr)
	case *types.Signature:
		return g.ptr, g.scalarAlign(g.ptr)
	case *types.Interface:
		return g.word + 8, g.scalarAlign(g.word)
	case *types.Enum:
		tag := g.tagSize(u)
		size, align := tag, g.scalarAlign(tag)
		for _, v := range u.Variants {
			off, a := g.payloadOffset(u), align
			for _, f := range v.Fields() {
				fs, fa := g.layout(f.Type())
				off = roundUp(off, fa) + fs
				a = max(a, fa)
			}
			size, align = max(size, off), max(align, a)
		}
		return size, align
	}
	g.fail("codegen: no layout for type %s", t)
	return 1, 1
}

// tagSize returns the size of the variant number of an enum.
func (g *gen) tagSize(e *types.Enum) int {
	if len(e.Variants) <= 256 {
		return 1
	}
	return 2
}

// payloadOffset returns the offset of the fields of the variants of an
// enum: after the variant number, aligned for every field.
func (g *gen) payloadOffset(e *types.Enum) int {
	a := 1
	for _, v := range e.Variants {
		for _, f := range v.Fields() {
			a = max(a, g.alignof(f.Type()))
		}
	}
	return roundUp(g.tagSize(e), a)
}

// fieldOffset returns the offset of field i of a struct.
func (g *gen) fieldOffset(s *types.Struct, i int) int {
	off := 0
	for j, f := range s.Fields {
		size, a := g.layout(f.Type())
		off = roundUp(off, a)
		if j == i {
			return off
		}
		off += size
	}
	panic("codegen: no field")
}

// tupleOffset returns the offset of element i of a tuple.
func (g *gen) tupleOffset(t *types.Tuple, i int) int {
	off := 0
	for j, v := range t.Vars {
		size, a := g.layout(v.Type())
		off = roundUp(off, a)
		if j == i {
			return off
		}
		off += size
	}
	panic("codegen: no tuple element")
}

// variantFieldOffset returns the offset of field i of variant v of an
// enum.
func (g *gen) variantFieldOffset(e *types.Enum, v, i int) int {
	off := g.payloadOffset(e)
	for j, f := range e.Variants[v].Fields() {
		size, a := g.layout(f.Type())
		off = roundUp(off, a)
		if j == i {
			return off
		}
		off += size
	}
	panic("codegen: no variant field")
}

func roundUp(n, a int) int {
	if a <= 1 {
		return n
	}
	return (n + a - 1) / a * a
}

func (g *gen) fail(format string, args ...any) {
	g.errs = append(g.errs, fmt.Errorf(format, args...))
}

// unsupported reports a construct that the code generator does not
// translate yet (comp.gen.coverage).
func (g *gen) unsupported(step int, what string) {
	msg := fmt.Sprintf("%s: not supported yet by the code generator (spec/24-roadmap.md, step %d)", what, step)
	for _, e := range g.errs {
		if e.Error() == msg {
			return
		}
	}
	g.errs = append(g.errs, fmt.Errorf("%s", msg))
}
