package codegen

import (
	"strings"

	"an/ir"
	"an/syntax"
	"an/types"
)

// runtimeName returns the name of the runtime function of an operation
// without a rule: the operation name in camel case and the width
// (target.arch.select).
func runtimeName(op string, width int) string {
	var b strings.Builder
	for _, p := range strings.Split(op, ".") {
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String() + itoa(width)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// arith emits arithmetic operation op of width bits with result r, or
// calls the runtime function of the operation if the architecture has no
// rule for it and it is wider than a word.
func (fn *fnGen) arith(v ir.Value, op string, bits int, x, y *loc, trap string) *loc {
	g := fn.g
	if !g.hasRule(op, bits, 0) && bits > g.word*8 {
		var args []*loc
		args = append(args, x)
		if y != nil {
			args = append(args, y)
		}
		res := fn.callRuntime(runtimeName(op, bits), args)
		if res == nil {
			return &loc{t: v.Type(), zero: true}
		}
		res.t = v.Type()
		fn.vals[v] = res
		return res
	}
	r := fn.result(v)
	ops := map[string]operand{"r": slotOperand(*r.at, bits/8), "x": fn.scalar(x)}
	if y != nil {
		ops["y"] = fn.scalar(y)
	}
	var labels map[string]string
	if trap != "" {
		labels = map[string]string{"trap": fn.trap(trap)}
	}
	fn.op(op, bits, 0, ops, labels)
	return r
}

// cmp emits comparison op of two values of width bits into a new bool.
func (fn *fnGen) cmp(v ir.Value, op string, bits int, x, y *loc) *loc {
	r := fn.result(v)
	fn.op(op, bits, 0, map[string]operand{"r": slotOperand(*r.at, 1), "x": fn.scalar(x), "y": fn.scalar(y)}, nil)
	return r
}

func boolLoc(b bool) *loc {
	v := uint64(0)
	if b {
		v = 1
	}
	return &loc{t: types.Typ[types.Bool], parts: []operand{imm(v, 8)}}
}

func (fn *fnGen) binOp(i *ir.BinOp) {
	g := fn.g
	t := i.X.Type()
	x, y := fn.loc(i.X), fn.loc(i.Y)
	if isString(t) {
		fn.stringOp(i, x, y)
		return
	}
	if isBool(t) {
		switch i.Op {
		case syntax.Eql:
			fn.cmp(i, "eq", 8, x, y)
		case syntax.Neq:
			fn.cmp(i, "ne", 8, x, y)
		case syntax.LAnd:
			fn.arith(i, "and", 8, x, y, "")
		case syntax.LOr:
			fn.arith(i, "or", 8, x, y, "")
		default:
			fn.errorf("codegen: operator %s on bool", i.Op)
		}
		return
	}
	bits, signed, wrapping, ok := g.intInfo(t)
	if !ok {
		if fb, isFloat := floatBits(t); isFloat {
			fn.floatBinOp(i, x, y, fb)
			return
		}
		if i.Op == syntax.Eql || i.Op == syntax.Neq {
			fn.equality(i, x, y, t)
			return
		}
		fn.errorf("codegen: operator %s on %s", i.Op, t)
		return
	}
	sfx := ".u"
	if signed {
		sfx = ".s"
	}
	checked := func(op string) {
		if wrapping || i.Proven {
			// A proven operation does not overflow (contract.check.runtime).
			fn.arith(i, op, bits, x, y, "")
		} else {
			fn.arith(i, op+sfx, bits, x, y, "PanicOverflow")
		}
	}
	switch i.Op {
	case syntax.Add:
		checked("add")
	case syntax.Sub:
		checked("sub")
	case syntax.Mul:
		checked("mul")
	case syntax.And:
		fn.arith(i, "and", bits, x, y, "")
	case syntax.Or:
		fn.arith(i, "or", bits, x, y, "")
	case syntax.Xor:
		fn.arith(i, "xor", bits, x, y, "")
	case syntax.AndNot:
		ny := fn.result(nil2(i))
		fn.op("not", bits, 0, map[string]operand{"r": slotOperand(*ny.at, bits/8), "x": fn.scalar(y)}, nil)
		ny.t = t
		fn.arith(i, "and", bits, x, ny, "")
	case syntax.Quo, syntax.Rem:
		fn.divide(i, x, y, bits, signed, wrapping)
	case syntax.Shl, syntax.Shr:
		fn.shift(i, x, y, bits, signed)
	case syntax.Eql:
		fn.cmp(i, "eq", bits, x, y)
	case syntax.Neq:
		fn.cmp(i, "ne", bits, x, y)
	case syntax.Lss:
		fn.cmp(i, "lt"+sfx, bits, x, y)
	case syntax.Leq:
		fn.cmp(i, "le"+sfx, bits, x, y)
	case syntax.Gtr:
		fn.cmp(i, "lt"+sfx, bits, y, x)
	case syntax.Geq:
		fn.cmp(i, "le"+sfx, bits, y, x)
	default:
		fn.errorf("codegen: operator %s", i.Op)
	}
}

// tempValue is an IR value for the slot of an intermediate result.
type tempValue struct{ t types.Type }

func (v *tempValue) Type() types.Type { return v.t }
func (v *tempValue) Name() string     { return "tmp" }

func nil2(v ir.Value) ir.Value { return &tempValue{v.Type()} }

// branchIf continues at label l if the bool operand c is true.
func (fn *fnGen) branchIf(c operand, l string) {
	fn.op("branch", 0, 0, map[string]operand{"x": c}, map[string]string{"L": l})
}

// test emits comparison op of x and y of width bits into a temporary
// bool and returns it.
func (fn *fnGen) test(op string, bits int, x, y operand) operand {
	t := fn.temp(1)
	fn.op(op, bits, 0, map[string]operand{"r": t, "x": x, "y": y}, nil)
	return t
}

// divide generates / and % (expr.int.div, expr.wrap.div).
func (fn *fnGen) divide(i *ir.BinOp, x, y *loc, bits int, signed, wrapping bool) {
	g := fn.g
	op := "div"
	if i.Op == syntax.Rem {
		op = "rem"
	}
	if signed {
		op += ".s"
	} else {
		op += ".u"
	}
	cy, yconst := fn.constInt(y)
	if !i.Proven && (!yconst || cy == 0) {
		fn.branchIf(fn.test("eq", bits, fn.scalar(y), imm(0, bits)), fn.trap("PanicDivide"))
	}
	minusOne := signed && (!yconst || cy == -1)
	if !minusOne || i.Proven && !wrapping {
		fn.arith(i, op, bits, x, y, "")
		return
	}
	min := uint64(1) << uint(bits-1)
	if !wrapping {
		// The minimum divided by -1 overflows.
		skip := g.label()
		fn.branchIf(fn.test("ne", bits, fn.scalar(y), imm(^uint64(0), bits)), skip)
		fn.branchIf(fn.test("eq", bits, fn.scalar(x), imm(min, bits)), fn.trap("PanicOverflow"))
		fn.defLabel(skip)
		fn.releaseTemps()
		fn.arith(i, op, bits, x, y, "")
		return
	}
	// x / -1 is -x and x % -1 is 0, without dividing (expr.wrap.div).
	r := fn.result(i)
	div, done := g.label(), g.label()
	fn.branchIf(fn.test("ne", bits, fn.scalar(y), imm(^uint64(0), bits)), div)
	if op == "div.s" {
		fn.op("neg", bits, 0, map[string]operand{"r": slotOperand(*r.at, bits/8), "x": fn.scalar(x)}, nil)
	} else {
		fn.store(*r.at, imm(0, bits), bits/8)
	}
	fn.op("jump", 0, 0, nil, map[string]string{"L": done})
	fn.defLabel(div)
	q := fn.arith(&tempValue{i.Type()}, op, bits, x, y, "")
	fn.copyTo(*r.at, q, bits/8)
	fn.defLabel(done)
	fn.vals[i] = r
}

// shift generates << and >>: the count is tested for a negative value
// and limited to a word.
func (fn *fnGen) shift(i *ir.BinOp, x, y *loc, bits int, signed bool) {
	g := fn.g
	ybits, ysigned, _, _ := g.intInfo(i.Y.Type())
	_, yconst := fn.constInt(y)
	if ysigned && !yconst && !i.Proven {
		fn.branchIf(fn.test("lt.s", ybits, fn.scalar(y), imm(0, ybits)), fn.trap("PanicShift"))
	}
	w := g.word * 8
	var count operand
	switch {
	case yconst:
		c, _ := fn.constInt(y)
		if c > int64(1)<<uint(w)-1 {
			c = int64(1)<<uint(w) - 1
		}
		count = imm(uint64(c), w)
	case ybits == w:
		count = fn.scalar(y)
	case ybits < w:
		count = fn.temp(g.word)
		fn.op("ext.u", w, ybits, map[string]operand{"r": count, "x": fn.scalar(y)}, nil)
	default:
		count = fn.temp(g.word)
		fn.writeWord(slotRef(count), fn.part(y, 0), g.word)
		sat, done := g.label(), g.label()
		for k := 1; k < ybits/w; k++ {
			fn.branchIf(fn.test("ne", w, fn.part(y, k), imm(0, w)), sat)
		}
		fn.op("jump", 0, 0, nil, map[string]string{"L": done})
		fn.defLabel(sat)
		fn.writeWord(slotRef(count), imm(^uint64(0), w), g.word)
		fn.defLabel(done)
	}
	op := "shl"
	if i.Op == syntax.Shr {
		op = "shr.u"
		if signed {
			op = "shr.s"
		}
	}
	cl := &loc{t: types.Typ[types.Uint], parts: []operand{count}}
	if count.isMem() {
		cl = at(slotRef(count), types.Typ[types.U16])
		if g.word != 2 {
			cl.t = types.Typ[types.Uint]
		}
	}
	if c, ok := fn.constInt(y); ok && bits > w && c >= int64(w) && c < int64(bits) {
		fn.wordShift(i, op, bits, x, int(c))
		return
	}
	if !g.hasRule(op, bits, 0) && bits > g.word*8 {
		fn.arith(i, op, bits, x, cl, "")
		return
	}
	r := fn.result(i)
	fn.op(op, bits, 0, map[string]operand{"r": slotOperand(*r.at, bits/8), "x": fn.scalar(x), "y": count}, nil)
}

// wordShift shifts a value wider than a word by a constant count of at
// least a word: whole words are moved, and the rest of the count is an
// operation.
func (fn *fnGen) wordShift(i *ir.BinOp, op string, bits int, x *loc, c int) {
	g := fn.g
	w := g.word * 8
	n, k, rest := bits/w, c/w, c%w
	r := fn.result(i)
	var fill operand = imm(0, w)
	if op == "shr.s" {
		fill = fn.temp(g.word)
		fn.op("shr.s", w, 0, map[string]operand{"r": fill, "x": fn.part(x, n-1), "y": imm(uint64(w-1), w)}, nil)
	}
	for j := 0; j < n; j++ {
		src := fill
		switch {
		case op == "shl" && j >= k:
			src = fn.part(x, j-k)
		case op == "shl":
			src = imm(0, w)
		case j+k < n:
			src = fn.part(x, j+k)
		}
		fn.writeWord(r.at.plus(j*g.word), src, g.word)
	}
	if rest > 0 {
		dst := slotOperand(*r.at, bits/8)
		fn.op(op, bits, 0, map[string]operand{"r": dst, "x": dst, "y": imm(uint64(rest), w)}, nil)
	}
}

func (fn *fnGen) unOp(i *ir.UnOp) {
	g := fn.g
	t := i.X.Type()
	x := fn.loc(i.X)
	if isBool(t) && i.Op == syntax.Not {
		fn.arith(i, "xor", 8, x, boolLoc(true), "")
		return
	}
	bits, signed, wrapping, ok := g.intInfo(t)
	if !ok {
		if i.Op == syntax.Arrow {
			fn.unsupported(30, "channels")
			return
		}
		if fb, isFloat := floatBits(t); isFloat && i.Op == syntax.Sub {
			fn.floatNeg(i, x, fb)
			return
		}
		if _, isFloat := floatBits(t); isFloat && i.Op == syntax.Add {
			fn.vals[i] = x
			return
		}
		fn.errorf("codegen: unary operator %s on %s", i.Op, t)
		return
	}
	switch i.Op {
	case syntax.Add:
		fn.vals[i] = x
	case syntax.Xor:
		fn.arith(i, "not", bits, x, nil, "")
	case syntax.Sub:
		switch {
		case wrapping || i.Proven:
			fn.arith(i, "neg", bits, x, nil, "")
		case signed:
			fn.arith(i, "neg.s", bits, x, nil, "PanicOverflow")
		default:
			fn.arith(i, "sub.u", bits, &loc{t: t, zero: true}, x, "PanicOverflow")
		}
	default:
		fn.errorf("codegen: unary operator %s", i.Op)
	}
}

// convert generates conversions between integer types and between types
// with the same representation.
func (fn *fnGen) convert(i *ir.Convert) {
	g := fn.g
	from, to := i.X.Type(), i.Type()
	x := fn.loc(i.X)
	_, fsigned, _, fint := g.intInfo(from)
	_, _, _, tint := g.intInfo(to)
	switch {
	case fint && tint:
		fn.vals[i] = fn.extend(i, x, fsigned)
	case types.Identical(from.Underlying(), to.Underlying()), types.IsInterface(from) && types.IsInterface(to):
		fn.vals[i] = &loc{t: to, parts: x.parts, zero: x.zero, at: x.at, addr: x.addr}
	default:
		_, fromFloat := floatBits(from)
		_, toFloat := floatBits(to)
		if fromFloat || toFloat {
			fn.floatConvert(i, x)
			return
		}
		if at, ok := to.Underlying().(*types.Array); ok {
			if _, ok := from.Underlying().(*types.Slice); ok {
				fn.sliceToArray(i, x, at)
				return
			}
		}
		if _, ok := to.Underlying().(*types.Slice); ok && isString(from) && x.parts != nil {
			// A constant string: the slice shares its bytes, which the
			// core profile cannot change (eff.source.alloc-ops).
			n := x.parts[1:]
			fn.vals[i] = &loc{t: to, parts: append(append([]operand{x.parts[0]}, n...), n...)}
			return
		}
		fn.unsupported(25, "conversion from "+from.String()+" to "+to.String())
	}
}

// sliceToArray converts a slice to an array of its length; another
// length panics.
func (fn *fnGen) sliceToArray(i *ir.Convert, x *loc, a *types.Array) {
	g := fn.g
	n := fn.sub(types.Typ[types.Int], x, g.ptr)
	ok := g.label()
	fn.branchIf(fn.test("eq", g.intBits, fn.scalar(n), imm(uint64(a.Len), g.intBits)), ok)
	fn.callRuntime("PanicConvert", []*loc{n, fn.intLoc(int(a.Len))})
	fn.defLabel(ok)
	r := fn.result(i)
	ptr := operand{kind: opSlot, off: fn.slot(g.ptr), width: g.ptr * 8}
	fn.writeWord(slotRef(ptr), fn.part(x, 0), g.ptr)
	fn.copyMem(*r.at, mref{kind: mPtr, ptr: ptr}, g.sizeof(a))
}

// stringOp generates comparisons of strings.
func (fn *fnGen) stringOp(i *ir.BinOp, x, y *loc) {
	switch i.Op {
	case syntax.Eql, syntax.Neq:
		eq := fn.callRuntime("EqualString", []*loc{x, y})
		if i.Op == syntax.Eql {
			eq.t = i.Type()
			fn.vals[i] = eq
			return
		}
		fn.arith(i, "xor", 8, eq, boolLoc(true), "")
	case syntax.Lss, syntax.Leq, syntax.Gtr, syntax.Geq:
		c := fn.callRuntime("CompareString", []*loc{x, y})
		zero := fn.intLoc(0)
		switch i.Op {
		case syntax.Lss:
			fn.cmp(i, "lt.s", fn.g.intBits, c, zero)
		case syntax.Leq:
			fn.cmp(i, "le.s", fn.g.intBits, c, zero)
		case syntax.Gtr:
			fn.cmp(i, "lt.s", fn.g.intBits, zero, c)
		case syntax.Geq:
			fn.cmp(i, "le.s", fn.g.intBits, zero, c)
		}
	default:
		fn.unsupported(25, "string concatenation")
	}
}

// slice generates a slice expression (expr.bounds.check).
func (fn *fnGen) slice(i *ir.Slice) {
	g := fn.g
	ib := g.intBits / 8
	var ptr operand
	var n, c *loc
	isSlice, isStr := false, false
	es := 1
	xt := i.X.Type()
	switch u := xt.Underlying().(type) {
	case *ir.Pointer:
		a := u.Elem.Underlying().(*types.Array)
		es = g.sizeof(a.Elem)
		src := fn.place(i.X)
		if src.kind != mStatic {
			// A snapshot of the array (comp.gen.values).
			size := g.sizeof(a)
			snap := mref{kind: mFrame, off: fn.slot(size)}
			fn.copyMem(snap, src, size)
			src = snap
			if containsSlice(i.Type()) {
				fn.escape = true
			}
		}
		ptr = fn.addrOf(src)
		n = fn.intLoc(int(a.Len))
		c = n
	case *types.Slice:
		es = g.sizeof(u.Elem)
		x := fn.loc(i.X)
		ptr = fn.part(x, 0)
		n = fn.sub(types.Typ[types.Int], x, g.ptr)
		c = fn.sub(types.Typ[types.Int], x, g.ptr+ib)
		isSlice = true
	default:
		x := fn.loc(i.X)
		ptr = fn.part(x, 0)
		n = fn.sub(types.Typ[types.Int], x, g.ptr)
		c = n
		isStr = true
	}
	lo, hi, mx := fn.intLoc(0), n, c
	if i.Low != nil {
		lo = fn.loc(i.Low)
	}
	if i.High != nil {
		hi = fn.loc(i.High)
	}
	if i.Max != nil {
		mx = fn.loc(i.Max)
	}
	clo, kl := fn.constInt(lo)
	chi, kh := fn.constInt(hi)
	cmx, km := fn.constInt(mx)
	cc, kc := fn.constInt(c)
	if !i.Proven && !(kl && kh && km && kc && 0 <= clo && clo <= chi && chi <= cmx && cmx <= cc) {
		fn.callRuntime("CheckSlice", []*loc{lo, hi, mx, c, boolLoc(i.Max != nil), boolLoc(isSlice)})
	}
	r := fn.result(i)
	// The address of the first element.
	if v, ok := fn.constInt(lo); ok && v == 0 {
		fn.writeWord(*r.at, ptr, g.ptr)
	} else {
		fn.op("add", g.ptr*8, 0, map[string]operand{"r": slotOperand(*r.at, g.ptr), "x": ptr, "y": fn.scaled(lo, es)}, nil)
	}
	fn.op("sub", g.intBits, 0, map[string]operand{"r": slotOperand(r.at.plus(g.ptr), ib), "x": fn.scalar(hi), "y": fn.scalar(lo)}, nil)
	if !isStr {
		fn.op("sub", g.intBits, 0, map[string]operand{"r": slotOperand(r.at.plus(g.ptr+ib), ib), "x": fn.scalar(mx), "y": fn.scalar(lo)}, nil)
	}
}

// containsSlice reports whether values of type t contain a slice.
func containsSlice(t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return true
	case *types.Struct:
		for _, f := range u.Fields {
			if containsSlice(f.Type()) {
				return true
			}
		}
	case *types.Array:
		return containsSlice(u.Elem)
	case *types.Tuple:
		for _, v := range u.Vars {
			if containsSlice(v.Type()) {
				return true
			}
		}
	case *types.Enum:
		for _, v := range u.Variants {
			for _, f := range v.Fields() {
				if containsSlice(f.Type()) {
					return true
				}
			}
		}
	}
	return false
}
