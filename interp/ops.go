package interp

import (
	"fmt"
	"math"
	"math/bits"
	"unicode/utf8"

	"an/syntax"
	"an/types"
)

// intSize returns the size in bits and signedness of integer type t.
func (m *machine) intSize(t types.Type) (int, bool) {
	b := t.Underlying().(*types.Basic)
	signed := b.Info()&types.IsUnsigned == 0
	switch b.Kind() {
	case types.I8, types.U8:
		return 8, signed
	case types.I16, types.U16, types.I16le, types.I16be, types.U16le, types.U16be:
		return 16, signed
	case types.I32, types.U32, types.I32le, types.I32be, types.U32le, types.U32be:
		return 32, signed
	case types.I64, types.U64, types.I64le, types.I64be, types.U64le, types.U64be:
		return 64, signed
	}
	return m.intBits, signed // int, uint, usize
}

func overflow() *anPanic { return runtimeError("integer overflow") }

// signedResult checks that the mathematical result r of an operation on
// a signed type of size n fits (expr.int.checked, expr.int.overflow).
func fitsSigned(r int64, n int) bool {
	if n == 64 {
		return true
	}
	min, max := int64(-1)<<(n-1), int64(1)<<(n-1)-1
	return r >= min && r <= max
}

func fitsUnsigned(r uint64, n int) bool {
	return n == 64 || r <= uint64(1)<<n-1
}

func truncSigned(r int64, n int) int64 {
	if n == 64 {
		return r
	}
	shift := 64 - n
	return r << shift >> shift
}

func truncUnsigned(r uint64, n int) uint64 {
	if n == 64 {
		return r
	}
	return r & (uint64(1)<<n - 1)
}

func (m *machine) binop(op syntax.Kind, t types.Type, x, y value) value {
	switch op {
	case syntax.Eql:
		return m.equal(x, y, t)
	case syntax.Neq:
		return !m.equal(x, y, t)
	}
	switch x := x.(type) {
	case int64:
		n, _ := m.intSize(t)
		if op == syntax.Shl || op == syntax.Shr {
			return shiftSigned(op, x, shiftCount(y), n)
		}
		return m.signedOp(op, x, y.(int64), n)
	case uint64:
		n, _ := m.intSize(t)
		if op == syntax.Shl || op == syntax.Shr {
			s := shiftCount(y)
			if s >= 64 {
				return uint64(0)
			}
			if op == syntax.Shl {
				return truncUnsigned(x<<s, n)
			}
			return x >> s
		}
		return m.unsignedOp(op, x, y.(uint64), n)
	case float64:
		y := y.(float64)
		f32 := t.Underlying().(*types.Basic).Kind() == types.F32
		round := func(r float64) float64 {
			if f32 {
				return float64(float32(r))
			}
			return r
		}
		switch op {
		case syntax.Add:
			return round(x + y)
		case syntax.Sub:
			return round(x - y)
		case syntax.Mul:
			return round(x * y)
		case syntax.Quo:
			return round(x / y)
		case syntax.Lss:
			return x < y
		case syntax.Leq:
			return x <= y
		case syntax.Gtr:
			return x > y
		case syntax.Geq:
			return x >= y
		}
	case string:
		y := y.(string)
		switch op {
		case syntax.Add:
			return x + y
		case syntax.Lss:
			return x < y
		case syntax.Leq:
			return x <= y
		case syntax.Gtr:
			return x > y
		case syntax.Geq:
			return x >= y
		}
	case bool:
		switch op {
		case syntax.LAnd:
			return x && y.(bool)
		case syntax.LOr:
			return x || y.(bool)
		}
	}
	panic(fmt.Sprintf("interp: operator %s on %T", op, x))
}

func shiftCount(y value) uint64 {
	switch y := y.(type) {
	case int64:
		if y < 0 {
			panic(runtimeError("negative shift amount"))
		}
		return uint64(y)
	case uint64:
		return y
	}
	panic("interp: shift count")
}

func shiftSigned(op syntax.Kind, x int64, s uint64, n int) int64 {
	if op == syntax.Shl {
		if s >= 64 {
			return 0
		}
		// Bits shifted out are discarded (expr.int.shift).
		return truncSigned(x<<s, n)
	}
	if s >= 64 {
		if x < 0 {
			return -1
		}
		return 0
	}
	return x >> s
}

func (m *machine) signedOp(op syntax.Kind, x, y int64, n int) value {
	switch op {
	case syntax.Add:
		r := x + y
		if (r > x) != (y > 0) || !fitsSigned(r, n) {
			panic(overflow())
		}
		return r
	case syntax.Sub:
		r := x - y
		if (r < x) != (y > 0) || !fitsSigned(r, n) {
			panic(overflow())
		}
		return r
	case syntax.Mul:
		if x == 0 || y == 0 {
			return int64(0)
		}
		r := x * y
		if r/y != x || (x == -1 && y == math.MinInt64) || (y == -1 && x == math.MinInt64) || !fitsSigned(r, n) {
			panic(overflow())
		}
		return r
	case syntax.Quo, syntax.Rem:
		if y == 0 {
			panic(runtimeError("integer divide by zero"))
		}
		if y == -1 && x == truncSigned(int64(-1)<<(n-1), n) {
			if op == syntax.Rem {
				return int64(0)
			}
			panic(overflow()) // expr.int.div
		}
		if op == syntax.Quo {
			return x / y
		}
		return x % y
	case syntax.And:
		return x & y
	case syntax.Or:
		return x | y
	case syntax.Xor:
		return x ^ y
	case syntax.AndNot:
		return x &^ y
	case syntax.Lss:
		return x < y
	case syntax.Leq:
		return x <= y
	case syntax.Gtr:
		return x > y
	case syntax.Geq:
		return x >= y
	}
	panic(fmt.Sprintf("interp: operator %s on integers", op))
}

func (m *machine) unsignedOp(op syntax.Kind, x, y uint64, n int) value {
	switch op {
	case syntax.Add:
		r, carry := bits.Add64(x, y, 0)
		if carry != 0 || !fitsUnsigned(r, n) {
			panic(overflow())
		}
		return r
	case syntax.Sub:
		if y > x {
			panic(overflow())
		}
		return x - y
	case syntax.Mul:
		hi, r := bits.Mul64(x, y)
		if hi != 0 || !fitsUnsigned(r, n) {
			panic(overflow())
		}
		return r
	case syntax.Quo:
		if y == 0 {
			panic(runtimeError("integer divide by zero"))
		}
		return x / y
	case syntax.Rem:
		if y == 0 {
			panic(runtimeError("integer divide by zero"))
		}
		return x % y
	case syntax.And:
		return x & y
	case syntax.Or:
		return x | y
	case syntax.Xor:
		return x ^ y
	case syntax.AndNot:
		return x &^ y
	case syntax.Lss:
		return x < y
	case syntax.Leq:
		return x <= y
	case syntax.Gtr:
		return x > y
	case syntax.Geq:
		return x >= y
	}
	panic(fmt.Sprintf("interp: operator %s on integers", op))
}

func (m *machine) unop(op syntax.Kind, t types.Type, x value) value {
	switch x := x.(type) {
	case int64:
		n, _ := m.intSize(t)
		switch op {
		case syntax.Sub:
			if x == truncSigned(int64(-1)<<(n-1), n) {
				panic(overflow())
			}
			return -x
		case syntax.Xor:
			return ^x
		case syntax.Add:
			return x
		}
	case uint64:
		n, _ := m.intSize(t)
		switch op {
		case syntax.Sub:
			if x != 0 {
				panic(overflow())
			}
			return x
		case syntax.Xor:
			return truncUnsigned(^x, n)
		case syntax.Add:
			return x
		}
	case float64:
		switch op {
		case syntax.Sub:
			return -x
		case syntax.Add:
			return x
		}
	case bool:
		if op == syntax.Not {
			return !x
		}
	}
	panic(fmt.Sprintf("interp: unary %s on %T", op, x))
}

// equal compares two values of type t (expr.cmp.go).
func (m *machine) equal(x, y value, t types.Type) bool {
	switch x := x.(type) {
	case nil:
		return y == nil
	case bool, int64, uint64, float64, string:
		return x == y
	case iface:
		y := y.(iface)
		if x.t == nil || y.t == nil {
			return x.t == nil && y.t == nil
		}
		if !types.Identical(x.t, y.t) {
			return false
		}
		if !types.Comparable(x.t) {
			panic(runtimeError("comparing uncomparable type " + x.t.String()))
		}
		return m.equal(x.v, y.v, x.t)
	case *aggregate:
		y := y.(*aggregate)
		if x.variant != y.variant || len(x.elems) != len(y.elems) {
			return false
		}
		for i := range x.elems {
			if !m.equal(x.elems[i], y.elems[i], elemTypeOf(t, x.variant, i)) {
				return false
			}
		}
		return true
	case sliceVal:
		// A slice compares only with nil: nil means empty
		// (expr.cmp.nil-empty).
		return x.len == 0 && y.(sliceVal).len == 0
	case *mapObj:
		return x.len() == 0 && y.(*mapObj).len() == 0
	case *closure:
		return x == nil && y.(*closure) == nil
	}
	panic(fmt.Sprintf("interp: comparison of %T", x))
}

// elemTypeOf returns the type of element i of an aggregate of type t.
func elemTypeOf(t types.Type, variant, i int) types.Type {
	switch u := t.Underlying().(type) {
	case *types.Struct:
		return u.Fields[i].Type()
	case *types.Array:
		return u.Elem
	case *types.Enum:
		return u.Variants[variant].Fields()[i].Type()
	}
	return types.Typ[types.Invalid]
}

// convert converts x from type from to type to.
func (m *machine) convert(x value, from, to types.Type) value {
	tb, toBasic := to.Underlying().(*types.Basic)
	if !toBasic {
		if ta, ok := to.Underlying().(*types.Array); ok {
			// A slice to an array: panics if the slice is too short.
			s := x.(sliceVal)
			if int64(s.len) < ta.Len {
				panic(runtimeError(fmt.Sprintf("cannot convert slice with length %d to array or pointer to array with length %d", s.len, ta.Len)))
			}
			a := &aggregate{elems: make([]value, ta.Len), refs: 1}
			for i := range a.elems {
				a.elems[i] = share(s.buf.elems[s.off+i])
			}
			return a
		}
		if ts, ok := to.Underlying().(*types.Slice); ok {
			if str, ok := x.(string); ok {
				return stringToSlice(str, ts)
			}
		}
		return x // the same underlying type
	}
	switch {
	case tb.Info()&(types.IsString|types.IsTerminated) != 0:
		switch x := x.(type) {
		case string:
			return x
		case int64:
			if x < 0 || x > utf8.MaxRune {
				return "�"
			}
			return string(rune(x))
		case uint64:
			if x > utf8.MaxRune {
				return "�"
			}
			return string(rune(x))
		case sliceVal:
			return sliceToString(x)
		}
	case tb.Info()&types.IsFloat != 0:
		var f float64
		switch x := x.(type) {
		case int64:
			f = float64(x)
		case uint64:
			f = float64(x)
		case float64:
			f = x
		}
		if tb.Kind() == types.F32 {
			f = float64(float32(f))
		}
		return f
	case tb.Info()&types.IsInteger != 0:
		n, signed := m.intSize(to)
		switch x := x.(type) {
		case int64:
			if signed {
				return truncSigned(x, n)
			}
			return truncUnsigned(uint64(x), n)
		case uint64:
			if signed {
				return truncSigned(int64(x), n)
			}
			return truncUnsigned(x, n)
		case float64:
			// Panics when the truncated value is not representable
			// (type.conv.float-int).
			t := math.Trunc(x)
			if math.IsNaN(t) {
				panic(overflow())
			}
			if signed {
				lo, hi := -math.Ldexp(1, n-1), math.Ldexp(1, n-1)
				if t < lo || t >= hi {
					panic(overflow())
				}
				return int64(t)
			}
			if t < 0 || t >= math.Ldexp(1, n) {
				panic(overflow())
			}
			return uint64(t)
		}
	case tb.Info()&types.IsBoolean != 0:
		return x
	}
	panic(fmt.Sprintf("interp: conversion of %T to %s", x, to))
}

func stringToSlice(s string, t *types.Slice) value {
	var elems []value
	if b, ok := t.Elem.Underlying().(*types.Basic); ok && (b.Kind() == types.I32) {
		for _, r := range s {
			elems = append(elems, int64(r))
		}
	} else {
		for i := 0; i < len(s); i++ {
			elems = append(elems, uint64(s[i]))
		}
	}
	return sliceVal{buf: newBuffer(elems), len: len(elems), cap: len(elems)}
}

func sliceToString(s sliceVal) string {
	var b []byte
	for i := range s.len {
		switch e := s.buf.elems[s.off+i].(type) {
		case uint64:
			b = append(b, byte(e))
		case int64:
			b = utf8.AppendRune(b, rune(e))
		}
	}
	return string(b)
}
