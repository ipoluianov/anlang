package codegen

import (
	"go/constant"
	"math"

	"an/ir"
	"an/syntax"
	"an/types"
)

// Floating-point values (target.floats.soft): the code generator calls
// the functions of the runtime package, which compute on the bits of the
// values, for every operation. A floating-point value has the layout of
// the unsigned integer of its width.

// floatBits returns the width of a floating-point type.
func floatBits(t types.Type) (int, bool) {
	b, ok := t.Underlying().(*types.Basic)
	if !ok || b.Info()&types.IsFloat == 0 {
		return 0, false
	}
	if b.Kind() == types.F32 {
		return 32, true
	}
	return 64, true
}

// floatConstBits returns the bits of a floating-point constant of the
// given width.
func floatConstBits(v constant.Value, bits int) uint64 {
	if bits == 32 {
		f, _ := constant.Float32Val(constant.ToFloat(v))
		return uint64(math.Float32bits(f))
	}
	f, _ := constant.Float64Val(constant.ToFloat(v))
	return math.Float64bits(f)
}

// floatRuntime calls the runtime function name plus the width of the
// operands, such as AddF64, and gives its result type t.
func (fn *fnGen) floatRuntime(v ir.Value, name string, bits int, t types.Type, args ...*loc) *loc {
	r := fn.callRuntime(name+"F"+itoa(bits), args)
	if r == nil {
		return &loc{t: t, zero: true}
	}
	r.t = t
	if v != nil {
		fn.vals[v] = r
	}
	return r
}

// floatBinOp generates an arithmetic or comparison operator on floats.
func (fn *fnGen) floatBinOp(i *ir.BinOp, x, y *loc, bits int) {
	t, b := i.X.Type(), types.Typ[types.Bool]
	switch i.Op {
	case syntax.Add:
		fn.floatRuntime(i, "Add", bits, t, x, y)
	case syntax.Sub:
		fn.floatRuntime(i, "Sub", bits, t, x, y)
	case syntax.Mul:
		fn.floatRuntime(i, "Mul", bits, t, x, y)
	case syntax.Quo:
		fn.floatRuntime(i, "Div", bits, t, x, y)
	case syntax.Eql:
		fn.floatRuntime(i, "Eq", bits, b, x, y)
	case syntax.Neq:
		eq := fn.floatRuntime(nil, "Eq", bits, b, x, y)
		fn.arith(i, "xor", 8, eq, boolLoc(true), "")
	case syntax.Lss:
		fn.floatRuntime(i, "Lt", bits, b, x, y)
	case syntax.Leq:
		fn.floatRuntime(i, "Le", bits, b, x, y)
	case syntax.Gtr:
		fn.floatRuntime(i, "Lt", bits, b, y, x)
	case syntax.Geq:
		fn.floatRuntime(i, "Le", bits, b, y, x)
	default:
		fn.errorf("codegen: operator %s on %s", i.Op, t)
	}
}

// floatConvert generates a conversion from or to a floating-point type.
func (fn *fnGen) floatConvert(i *ir.Convert, x *loc) {
	g := fn.g
	from, to := i.X.Type(), i.Type()
	fbits, fromFloat := floatBits(from)
	tbits, toFloat := floatBits(to)
	switch {
	case fromFloat && toFloat && fbits == tbits:
		fn.vals[i] = &loc{t: to, parts: x.parts, zero: x.zero, at: x.at, addr: x.addr}
	case fromFloat && toFloat && tbits == 64:
		fn.floatRuntime(i, "F64From", 32, to, x)
	case fromFloat && toFloat:
		fn.floatRuntime(i, "F32From", 64, to, x)
	case toFloat:
		_, signed, _, _ := g.intInfo(from)
		wide, name := types.Typ[types.U64], "FromU64"
		if signed {
			wide, name = types.Typ[types.I64], "FromI64"
		}
		x = fn.extend(&tempValue{wide}, x, signed)
		r := fn.callRuntime("F"+itoa(tbits)+name, []*loc{x})
		if r != nil {
			r.t = to
			fn.vals[i] = r
		}
	default:
		// The value truncated toward zero, which panics if it does not fit
		// (type.conv.float-int).
		bits, signed, _, _ := g.intInfo(to)
		name := "U64FromF"
		if signed {
			name = "I64FromF"
		}
		res := fn.callRuntime(name+itoa(fbits), []*loc{x, fn.intLoc(bits)})
		if res == nil {
			return
		}
		tt := res.t.(*types.Tuple)
		ok := g.label()
		fn.branchIf(fn.scalar(fn.sub(types.Typ[types.Bool], res, g.tupleOffset(tt, 1))), ok)
		fn.callRuntime("PanicOverflow", nil)
		fn.defLabel(ok)
		fn.vals[i] = fn.sub(to, res, 0) // the low bytes
	}
}

// floatNeg generates unary - of a float.
func (fn *fnGen) floatNeg(i *ir.UnOp, x *loc, bits int) {
	fn.floatRuntime(i, "Neg", bits, i.Type(), x)
}

// floatAsF64 returns a float as an f64, for printing.
func (fn *fnGen) floatAsF64(l *loc) *loc {
	if bits, _ := floatBits(l.t); bits == 32 {
		return fn.floatRuntime(nil, "F64From", 32, types.Typ[types.F64], l)
	}
	return l
}

// printFloat prints a float as fmt.Print does, or as the built-in print
// does.
func (fn *fnGen) printFloat(fd *loc, l *loc, fmtMode bool) {
	bits, _ := floatBits(l.t)
	x := fn.floatAsF64(l)
	if fmtMode {
		fn.callRuntime("PrintFloat", []*loc{fd, x, fn.intLoc(bits)})
		return
	}
	fn.callRuntime("PrintF64", []*loc{fd, x})
}
