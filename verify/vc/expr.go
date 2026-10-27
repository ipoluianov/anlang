package vc

import (
	"fmt"
	"go/constant"
	"math/big"
	"strings"

	"an/syntax"
	"an/types"
	"an/verify"
)

var (
	zeroInt = big.NewInt(0)
	oneInt  = big.NewInt(1)
)

func cint(k int64) verify.Term { return verify.Const(k) }

func isConstTerm(t verify.Term) (*big.Int, bool) {
	if len(t.Coefs) != 0 {
		return nil, false
	}
	if t.Const == nil {
		return new(big.Int), true
	}
	return t.Const, true
}

// formula returns the formula of a boolean value, or a fresh one.
func (f *fn) formula(st *state, x val) verify.Formula {
	if x.kind == vBool {
		return x.f
	}
	return f.fresh(st, types.Typ[types.Bool], "cond").f
}

// cond evaluates a boolean expression.
func (f *fn) cond(st *state, e syntax.Expr) verify.Formula { return f.formula(st, f.eval(st, e)) }

// eval evaluates expression e: it proves the run-time checks of e and
// returns its value.
func (f *fn) eval(st *state, e syntax.Expr) val {
	tv := f.info.Types[e]
	if tv.Value != nil {
		switch {
		case isInt(tv.Type) || tv.Value.Kind() == constant.Int:
			if k, ok := f.constant(e); ok {
				return val{kind: vInt, t: verify.ConstBig(k)}
			}
		case tv.Value.Kind() == constant.Bool:
			if constant.BoolVal(tv.Value) {
				return val{kind: vBool, f: verify.Formula{Kind: verify.True}}
			}
			return val{kind: vBool, f: verify.Formula{Kind: verify.False}}
		case tv.Value.Kind() == constant.String:
			return val{kind: vSeq, t: cint(int64(len(constant.StringVal(tv.Value))))}
		}
	}
	switch e := e.(type) {
	case *syntax.ParenExpr:
		return f.eval(st, e.X)
	case *syntax.Name:
		if v, ok := f.info.Uses[e].(*types.Var); ok {
			return f.read(st, v)
		}
		if v, ok := f.info.Uses[e].(*types.Variant); ok && !v.HasData() && enumOf(tv.Type) != nil {
			return f.variant(tv.Type, v.Index(), nil)
		}
		return val{}
	case *syntax.SelectorExpr:
		if id, ok := unparen(e.X).(*syntax.Name); ok {
			if _, ok := f.info.Uses[id].(*types.PkgName); ok {
				if v, ok := f.info.Uses[e.Sel].(*types.Var); ok {
					return f.read(st, v)
				}
				if v, ok := f.info.Uses[e.Sel].(*types.Variant); ok && !v.HasData() && enumOf(tv.Type) != nil {
					return f.variant(tv.Type, v.Index(), nil)
				}
				return val{}
			}
		}
		if sel := f.info.Selections[e]; sel != nil && sel.Kind == types.FieldVal {
			if y, ok := field(f.eval(st, e.X), sel.Index); ok && y.kind != vNone {
				return y
			}
			return f.received(st, tv.Type, types.ExprString(e))
		}
		f.eval(st, e.X)
		return f.fresh(st, tv.Type, types.ExprString(e))
	case *syntax.IndexExpr:
		return f.index(st, e)
	case *syntax.SliceExpr:
		return f.slice(st, e)
	case *syntax.CallExpr:
		rs := f.call(st, e)
		if len(rs) > 0 {
			return rs[0]
		}
		return val{}
	case *syntax.UnaryExpr:
		return f.unary(st, e)
	case *syntax.BinaryExpr:
		return f.binary(st, e)
	case *syntax.CompositeLit:
		if s := structOf(tv.Type); s != nil {
			return f.structLit(st, e, s, tv.Type)
		}
		n := int64(0)
		keyed := false
		var elems []val
		for _, el := range e.Elts {
			if kv, ok := el.(*syntax.KeyValueExpr); ok {
				keyed = true
				f.eval(st, kv.Value)
				continue
			}
			elems = append(elems, f.eval(st, el))
			n++
		}
		l := seqLen(tv.Type)
		if keyed || l < -1 || l >= 0 && l != n {
			return f.fresh(st, tv.Type, "literal")
		}
		// The elements of a slice or an array literal: of constants, the
		// prefix sums are constants too.
		x := val{kind: vSeq, t: cint(n), seq: f.newSeq(), off: cint(0)}
		et := elemType(tv.Type)
		if !isInt(et) {
			return x
		}
		ps := []*big.Int{new(big.Int)}
		for _, y := range elems {
			k, ok := isConstTerm(y.t)
			if y.kind != vInt || !ok {
				ps = nil
				break
			}
			ps = append(ps, new(big.Int).Add(ps[len(ps)-1], k))
		}
		if ps != nil {
			if f.known == nil {
				f.known = map[int][]*big.Int{}
			}
			f.known[x.seq] = ps
			return x
		}
		for j, y := range elems {
			if y.kind == vInt {
				st.assume(verify.Cmp(f.element(st, x, cint(int64(j)), et), verify.EQ, y.t))
			}
		}
		return x
	case *syntax.FuncLit:
		f.lits = append(f.lits, e)
		return val{}
	case *syntax.TypeAssertExpr:
		f.eval(st, e.X)
		return f.fresh(st, tv.Type, types.ExprString(e))
	case *syntax.KeyValueExpr:
		f.eval(st, e.Value)
		return val{}
	}
	return f.fresh(st, tv.Type, "value")
}

// index evaluates xs[i] with its bounds check.
func (f *fn) index(st *state, e *syntax.IndexExpr) val {
	tv := f.info.Types[e]
	xt := f.typeOf(e.X)
	if len(e.Indices) != 1 || f.info.Types[e.X].IsType() {
		return f.fresh(st, tv.Type, "instance")
	}
	if _, ok := core(xt).(*types.Signature); ok {
		return val{} // an instantiation of a generic function
	}
	x := f.eval(st, e.X)
	i := f.eval(st, e.Indices[0])
	if seqLen(xt) < -1 {
		return f.fresh(st, tv.Type, types.ExprString(e)) // a map
	}
	if x.kind == vSeq && i.kind == vInt {
		f.runtimeCheck(st, e, verify.AndOf(verify.Cmp(cint(0), verify.LE, i.t), verify.Cmp(i.t, verify.LT, x.t)))
		if f.indexAt == nil {
			f.indexAt = map[*syntax.IndexExpr]verify.Term{}
		}
		f.indexAt[e] = i.t
		if x.seq != 0 && isInt(tv.Type) {
			return val{kind: vInt, t: f.element(st, x, i.t, tv.Type)}
		}
	} else {
		f.unprovable(e)
	}
	return f.received(st, tv.Type, types.ExprString(e))
}

// slice evaluates x[lo:hi:max] with its bounds check: the slice is in
// bounds if 0 ≤ lo ≤ hi ≤ max ≤ len(x), which implies the check against
// the capacity.
func (f *fn) slice(st *state, e *syntax.SliceExpr) val {
	x := f.eval(st, e.X)
	opt := func(b syntax.Expr, def verify.Term) (verify.Term, bool) {
		if b == nil {
			return def, true
		}
		v := f.eval(st, b)
		return v.t, v.kind == vInt
	}
	if x.kind != vSeq {
		x = val{kind: vSeq, t: f.newVar("len(" + types.ExprString(e.X) + ")")}
		st.assume(between(x.t, zeroInt, f.maxLen()))
	}
	lo, ok1 := opt(e.Low, cint(0))
	hi, ok2 := opt(e.High, x.t)
	mx, ok3 := opt(e.Max, hi)
	if ok1 && ok2 && ok3 {
		f.runtimeCheck(st, e, verify.AndOf(
			verify.Cmp(cint(0), verify.LE, lo), verify.Cmp(lo, verify.LE, hi),
			verify.Cmp(hi, verify.LE, mx), verify.Cmp(mx, verify.LE, x.t)))
		r := val{kind: vSeq, t: hi.Sub(lo), seq: x.seq}
		if x.seq != 0 {
			r.off = x.off.Add(lo)
		}
		return r
	}
	f.unprovable(e)
	return f.fresh(st, f.typeOf(e), types.ExprString(e))
}

func (f *fn) unary(st *state, e *syntax.UnaryExpr) val {
	t := f.typeOf(e)
	switch e.Op {
	case syntax.Add:
		return f.eval(st, e.X)
	case syntax.Not:
		return val{kind: vBool, f: verify.NotOf(f.cond(st, e.X))}
	case syntax.Sub:
		x := f.eval(st, e.X)
		if isInt(t) && !types.IsWrap(t) && x.kind == vInt {
			r := x.t.Scale(big.NewInt(-1))
			f.overflow(st, e, r, t)
			return val{kind: vInt, t: r}
		}
		f.unprovable(e)
	default:
		f.eval(st, e.X)
	}
	return f.fresh(st, t, types.ExprString(e))
}

// overflow proves that the exact result r of a checked operation of node
// n lies in the range of type t.
func (f *fn) overflow(st *state, n syntax.Node, r verify.Term, t types.Type) {
	if lo, hi, ok := f.info.IntBounds(t); ok {
		f.runtimeCheck(st, n, between(r, lo, hi))
	}
}

var relOf = map[syntax.Kind]verify.Rel{syntax.Eql: verify.EQ, syntax.Neq: verify.NE, syntax.Lss: verify.LT, syntax.Leq: verify.LE, syntax.Gtr: verify.GT, syntax.Geq: verify.GE}

func (f *fn) binary(st *state, e *syntax.BinaryExpr) val {
	switch e.Op {
	case syntax.LAnd, syntax.LOr:
		x := f.cond(st, e.X)
		// The right operand is evaluated only when the left one does not
		// decide: its facts hold under that condition.
		guard := x
		if e.Op == syntax.LOr {
			guard = verify.NotOf(x)
		}
		inner := st.clone()
		inner.assume(guard)
		n := len(inner.facts)
		y := f.cond(inner, e.Y)
		if extra := inner.facts[n:]; len(extra) > 0 {
			st.assume(verify.OrOf(verify.NotOf(guard), verify.AndOf(extra...)))
		}
		if e.Op == syntax.LAnd {
			return val{kind: vBool, f: verify.AndOf(x, y)}
		}
		return val{kind: vBool, f: verify.OrOf(x, y)}
	}
	if rel, ok := relOf[e.Op]; ok {
		xt := f.typeOf(e.X)
		x, y := f.eval(st, e.X), f.eval(st, e.Y)
		switch {
		case x.kind == vInt && y.kind == vInt && isInt(xt):
			return val{kind: vBool, f: verify.Cmp(x.t, rel, y.t)}
		case x.kind == vBool && y.kind == vBool && (rel == verify.EQ || rel == verify.NE):
			same := verify.OrOf(verify.AndOf(x.f, y.f), verify.AndOf(verify.NotOf(x.f), verify.NotOf(y.f)))
			if rel == verify.NE {
				same = verify.NotOf(same)
			}
			return val{kind: vBool, f: same}
		case x.kind == vEnum && y.kind == vEnum && (rel == verify.EQ || rel == verify.NE):
			return f.enumCmp(st, x, y, rel)
		}
		return f.fresh(st, types.Typ[types.Bool], types.ExprString(e))
	}
	x, y := f.eval(st, e.X), f.eval(st, e.Y)
	return f.arith(st, e, e.Op, x, y, f.typeOf(e), f.typeOf(e.Y))
}

// arith returns the value of x op y of type t for node n, proving the
// checks of the operation: overflow, division by zero and the sign of a
// shift count of type yt.
func (f *fn) arith(st *state, n syntax.Node, op syntax.Kind, x, y val, t, yt types.Type) val {
	name := "value"
	if e, ok := n.(syntax.Expr); ok {
		name = types.ExprString(e)
	}
	if !isInt(t) || x.kind != vInt || y.kind != vInt {
		f.unprovable(n)
		return f.fresh(st, t, name)
	}
	checked := !types.IsWrap(t)
	if !checked {
		// Wrapping arithmetic: the exact result modulo 2^n (expr.wrap.ops).
		switch op {
		case syntax.Add:
			return val{kind: vInt, t: f.wrapped(st, x.t.Add(y.t), t)}
		case syntax.Sub:
			return val{kind: vInt, t: f.wrapped(st, x.t.Sub(y.t), t)}
		case syntax.Mul:
			for _, p := range [][2]verify.Term{{x.t, y.t}, {y.t, x.t}} {
				if k, ok := isConstTerm(p[0]); ok {
					return val{kind: vInt, t: f.wrapped(st, p[1].Scale(k), t)}
				}
			}
		}
	}
	switch op {
	case syntax.Add, syntax.Sub:
		if !checked {
			break
		}
		r := x.t.Add(y.t)
		if op == syntax.Sub {
			r = x.t.Sub(y.t)
		}
		f.overflow(st, n, r, t)
		return val{kind: vInt, t: r}
	case syntax.Mul:
		if !checked {
			break
		}
		for _, p := range [][2]verify.Term{{x.t, y.t}, {y.t, x.t}} {
			if k, ok := isConstTerm(p[0]); ok {
				r := p[1].Scale(k)
				f.overflow(st, n, r, t)
				return val{kind: vInt, t: r}
			}
		}
		f.unprovable(n)
		r := f.fresh(st, t, name)
		return r
	case syntax.Quo, syntax.Rem:
		lo, _, _ := f.info.IntBounds(t)
		conds := []verify.Formula{verify.Cmp(y.t, verify.NE, cint(0))}
		if checked && lo.Sign() < 0 {
			conds = append(conds, verify.NotOf(verify.AndOf(verify.Cmp(x.t, verify.EQ, verify.ConstBig(lo)), verify.Cmp(y.t, verify.EQ, cint(-1)))))
		}
		f.runtimeCheck(st, n, verify.AndOf(conds...))
		if k, ok := isConstTerm(y.t); ok && k.Sign() > 0 {
			q := f.quotient(st, x.t, k)
			if op == syntax.Quo {
				return val{kind: vInt, t: q}
			}
			return val{kind: vInt, t: x.t.Sub(q.Scale(k))}
		}
		return f.fresh(st, t, name)
	case syntax.Shl, syntax.Shr:
		if isInt(yt) {
			if ylo, _, _ := f.info.IntBounds(yt); ylo != nil && ylo.Sign() < 0 {
				f.runtimeCheck(st, n, verify.Cmp(y.t, verify.GE, cint(0)))
			}
		}
		if k, ok := isConstTerm(y.t); ok && op == syntax.Shr && k.Sign() >= 0 && k.Cmp(big.NewInt(64)) < 0 {
			// x >> k is ⌊x / 2^k⌋ for every x.
			q := f.floorPow2(st, x.t, uint(k.Int64()))
			f.inRange(st, q, t)
			return val{kind: vInt, t: q}
		}
	case syntax.And, syntax.Or, syntax.Xor, syntax.AndNot:
		if r, ok := f.bitwise(st, op, x.t, y.t, t); ok {
			return val{kind: vInt, t: r}
		}
	}
	if op == syntax.Shl {
		// A left shift drops the bits above the width.
		if k, ok := isConstTerm(y.t); ok && k.Sign() >= 0 && k.Cmp(big.NewInt(64)) < 0 {
			d := new(big.Int).Lsh(oneInt, uint(k.Int64()))
			return val{kind: vInt, t: f.wrapped(st, x.t.Scale(d), t)}
		}
	}
	return f.fresh(st, t, name)
}

// width returns the width in bits and the signedness of integer type t.
func (f *fn) width(t types.Type) (int, bool) {
	lo, hi, ok := f.info.IntBounds(t)
	if !ok {
		return 0, false
	}
	signed := lo.Sign() < 0
	return hi.BitLen() + map[bool]int{true: 1, false: 0}[signed], signed
}

// wrapped returns x modulo 2^n into the range of integer type t of n bits
// (bit-vector arithmetic, stage 2 of the solver): r = x - 2^n·q.
func (f *fn) wrapped(st *state, x verify.Term, t types.Type) verify.Term {
	n, _ := f.width(t)
	lo, hi, ok := f.info.IntBounds(t)
	if !ok || n == 0 {
		return f.fresh(st, t, "wrapped").t
	}
	if k, ok := isConstTerm(x); ok {
		m := new(big.Int).Lsh(oneInt, uint(n))
		v := new(big.Int).Sub(k, lo)
		v.Mod(v, m)
		return verify.ConstBig(v.Add(v, lo))
	}
	q := f.newVar("carry")
	r := x.Sub(q.Scale(new(big.Int).Lsh(oneInt, uint(n))))
	st.assume(between(r, lo, hi))
	return r
}

// bits returns the bits of the n-bit two's complement value x, by a chain
// of halvings x_i = 2·x_(i+1) + b_i from x_0 = x: each equation has three
// variables, so that the elimination finds the bits of a known value.
func (f *fn) bits(st *state, x verify.Term, n int, signed bool) []verify.Term {
	return st.def(fmt.Sprintf("bits%d/%t", n, signed), x, func() []verify.Term { return f.newBits(st, x, n, signed) })
}

func (f *fn) newBits(st *state, x verify.Term, n int, signed bool) []verify.Term {
	bs := make([]verify.Term, n)
	cur := x
	two := big.NewInt(2)
	for i := 0; i < n; i++ {
		b := f.newVar("bit")
		st.assume(between(b, zeroInt, oneInt))
		bs[i] = b
		if i == n-1 {
			// The top bit: the rest is b, or -b for a signed value.
			top := b
			if signed {
				top = b.Scale(big.NewInt(-1))
			}
			st.assume(verify.Cmp(cur, verify.EQ, top))
			break
		}
		next := f.newVar("half")
		rest := n - 1 - i
		if signed {
			lim := new(big.Int).Lsh(oneInt, uint(rest))
			st.assume(between(next, new(big.Int).Neg(lim), new(big.Int).Sub(lim, oneInt)))
		} else {
			st.assume(between(next, zeroInt, new(big.Int).Sub(new(big.Int).Lsh(oneInt, uint(rest)), oneInt)))
		}
		st.assume(verify.Cmp(cur, verify.EQ, next.Scale(two).Add(b)))
		cur = next
	}
	return bs
}

// bitwise returns x op y for the bitwise operations of n-bit values of
// type t: a mask 2^k-1 is the remainder modulo 2^k, other operations go
// bit by bit.
func (f *fn) bitwise(st *state, op syntax.Kind, x, y verify.Term, t types.Type) (verify.Term, bool) {
	n, signed := f.width(t)
	if n == 0 || n > 64 {
		return verify.Term{}, false
	}
	kx, cx := isConstTerm(x)
	ky, cy := isConstTerm(y)
	if cx && cy {
		return verify.Term{}, false // folded by the checker
	}
	if op == syntax.And && (cx || cy) {
		k, v := ky, x
		if cx {
			k, v = kx, y
		}
		if k.Sign() >= 0 && new(big.Int).And(k, new(big.Int).Add(k, oneInt)).Sign() == 0 {
			// v & (2^m - 1) is v modulo 2^m.
			m := uint(k.BitLen())
			return v.Sub(f.floorPow2(st, v, m).Scale(new(big.Int).Lsh(oneInt, m))), true
		}
	}
	bx := f.constBits(kx, cx, n)
	by := f.constBits(ky, cy, n)
	if bx == nil {
		bx = f.bits(st, x, n, signed)
	}
	if by == nil {
		by = f.bits(st, y, n, signed)
	}
	r := verify.Const(0)
	zs := make([]verify.Term, n)
	for i := 0; i < n; i++ {
		a, b := bx[i], by[i]
		var z verify.Term
		ka, ca := isConstTerm(a)
		kb, cb := isConstTerm(b)
		switch {
		case ca && cb:
			z = verify.ConstBig(big.NewInt(bitOp(op, ka.Int64(), kb.Int64())))
		case ca || cb:
			// One bit is a constant k, the other the variable bit v.
			k, v := kb, a
			if ca {
				k, v = ka, b
			}
			one := k.Sign() != 0
			switch op {
			case syntax.And:
				z = verify.Const(0)
				if one {
					z = v
				}
			case syntax.Or:
				z = v
				if one {
					z = verify.Const(1)
				}
			case syntax.Xor:
				z = v
				if one {
					z = verify.Const(1).Sub(v)
				}
			case syntax.AndNot:
				switch {
				case cb && one: // a &^ 1
					z = verify.Const(0)
				case cb: // a &^ 0
					z = v
				case one: // 1 &^ b
					z = verify.Const(1).Sub(v)
				default: // 0 &^ b
					z = verify.Const(0)
				}
			}
		default:
			z = f.newVar("bit")
			and := func(r, p, q verify.Term) {
				st.assume(verify.AndOf(verify.Cmp(r, verify.LE, p), verify.Cmp(r, verify.LE, q), verify.Cmp(r, verify.GE, p.Add(q).Sub(verify.Const(1))), verify.Cmp(r, verify.GE, verify.Const(0))))
			}
			switch op {
			case syntax.And:
				and(z, a, b)
			case syntax.AndNot:
				and(z, a, verify.Const(1).Sub(b))
			case syntax.Or:
				st.assume(verify.AndOf(verify.Cmp(z, verify.GE, a), verify.Cmp(z, verify.GE, b), verify.Cmp(z, verify.LE, a.Add(b)), verify.Cmp(z, verify.LE, verify.Const(1))))
			case syntax.Xor:
				c := f.newVar("carry")
				and(c, a, b)
				st.assume(verify.Cmp(z, verify.EQ, a.Add(b).Sub(c.Scale(big.NewInt(2)))))
			}
		}
		zs[i] = z
		w := new(big.Int).Lsh(oneInt, uint(i))
		if signed && i == n-1 {
			w.Neg(w)
		}
		r = r.Add(z.Scale(w))
	}
	// The bits of the result are z: a value has one decomposition.
	st.def(fmt.Sprintf("bits%d/%t", n, signed), r, func() []verify.Term { return zs })
	return r, true
}

// constBits returns the bits of a constant of n bits, or nil.
func (f *fn) constBits(k *big.Int, ok bool, n int) []verify.Term {
	if !ok {
		return nil
	}
	v := new(big.Int).Set(k)
	if v.Sign() < 0 {
		v.Add(v, new(big.Int).Lsh(oneInt, uint(n)))
	}
	bs := make([]verify.Term, n)
	for i := 0; i < n; i++ {
		bs[i] = verify.Const(int64(v.Bit(i)))
	}
	return bs
}

func bitOp(op syntax.Kind, a, b int64) int64 {
	switch op {
	case syntax.And:
		return a & b
	case syntax.Or:
		return a | b
	case syntax.Xor:
		return a ^ b
	}
	return a &^ b
}

// floorPow2 returns ⌊x / 2^k⌋.
func (f *fn) floorPow2(st *state, x verify.Term, k uint) verify.Term {
	return st.def(fmt.Sprintf("floor%d", k), x, func() []verify.Term {
		d := new(big.Int).Lsh(oneInt, k)
		q := f.newVar("quotient")
		st.assume(verify.AndOf(verify.Cmp(q.Scale(d), verify.LE, x), verify.Cmp(x, verify.LE, q.Scale(d).Add(verify.ConstBig(new(big.Int).Sub(d, oneInt))))))
		return []verify.Term{q}
	})[0]
}

// quotient returns x / k truncated toward zero for a constant k > 0.
func (f *fn) quotient(st *state, x verify.Term, k *big.Int) verify.Term {
	q := f.newVar("quotient")
	km1 := verify.ConstBig(new(big.Int).Sub(k, oneInt))
	kq := q.Scale(k)
	st.assume(verify.OrOf(
		verify.AndOf(verify.Cmp(x, verify.GE, cint(0)), verify.Cmp(kq, verify.LE, x), verify.Cmp(x, verify.LE, kq.Add(km1))),
		verify.AndOf(verify.Cmp(x, verify.LT, cint(0)), verify.Cmp(kq.Sub(km1), verify.LE, x), verify.Cmp(x, verify.LE, kq))))
	return q
}

// ---------------------------------------------------------------------
// Calls.

// callee returns the function that a call calls statically, with its
// receiver expression for a method.
func (f *fn) callee(c *syntax.CallExpr) (*types.Func, syntax.Expr) {
	fun := unparen(c.Fun)
	if ix, ok := fun.(*syntax.IndexExpr); ok {
		fun = unparen(ix.X)
	}
	switch fe := fun.(type) {
	case *syntax.Name:
		obj, _ := f.info.Uses[fe].(*types.Func)
		return obj, nil
	case *syntax.SelectorExpr:
		if sel := f.info.Selections[fe]; sel != nil {
			if m, ok := sel.Obj.(*types.Func); ok && sel.Kind == types.MethodVal {
				return m, fe.X
			}
			return nil, nil
		}
		obj, _ := f.info.Uses[fe.Sel].(*types.Func)
		return obj, nil
	}
	return nil, nil
}

// contracts returns the requires and ensures clauses of a function.
func contracts(g *types.Func) (requires, ensures []*syntax.ContractStmt) {
	if g == nil || g.Decl == nil || g.Decl.Body == nil {
		return nil, nil
	}
	for _, s := range g.Decl.Body.List {
		c, ok := s.(*syntax.ContractStmt)
		if !ok {
			break
		}
		switch c.Word {
		case "requires":
			requires = append(requires, c)
		case "ensures":
			ensures = append(ensures, c)
		}
	}
	return requires, ensures
}

// rootVar returns the local variable of an argument passed inout.
// pathRoot returns the variable that holds the operand path e: a
// variable, or an element, a subslice or a field of one.
func (f *fn) pathRoot(e syntax.Expr) *types.Var {
	for {
		switch x := unparen(e).(type) {
		case *syntax.IndexExpr:
			e = x.X
			continue
		case *syntax.SliceExpr:
			e = x.X
			continue
		case *syntax.SelectorExpr:
			if s := f.info.Selections[x]; s != nil && s.Kind == types.FieldVal {
				e = x.X
				continue
			}
		}
		return f.rootVar(e)
	}
}

func (f *fn) rootVar(e syntax.Expr) *types.Var {
	if id, ok := unparen(e).(*syntax.Name); ok {
		v, _ := f.info.Uses[id].(*types.Var)
		return v
	}
	return nil
}

// call evaluates a call and returns its results.
func (f *fn) call(st *state, c *syntax.CallExpr) []val {
	tv := f.info.Types[c.Fun]
	results := func() []val {
		var rs []val
		switch t := f.info.Types[c].Type.(type) {
		case *types.Tuple:
			for _, v := range t.Vars {
				rs = append(rs, f.received(st, v.Type(), "result"))
			}
		case nil:
		default:
			rs = append(rs, f.received(st, t, types.ExprString(c)))
		}
		return rs
	}
	if tv.IsType() {
		// A conversion.
		if len(c.Args) != 1 {
			return nil
		}
		x := f.eval(st, c.Args[0])
		to, from := tv.Type, f.typeOf(c.Args[0])
		if target, ok := f.info.Narrowings[c.Args[0]]; ok {
			f.narrowing(st, c.Args[0], x, target)
		}
		switch {
		case isInt(to) && isInt(from) && !types.IsWrap(to) && !types.IsWrap(from) && x.kind == vInt:
			return []val{x}
		case isInt(to) && isInt(from) && x.kind == vInt && types.IsWrap(to):
			// A conversion to a wrap type keeps the value modulo 2^n
			// (type.conv.wrap).
			return []val{{kind: vInt, t: f.wrapped(st, x.t, to)}}
		case isInt(to) && isInt(from) && x.kind == vInt:
			return []val{x} // from a wrap type: proven to fit
		case x.kind == vSeq && seqLen(to) == -1 && (isString(to) || isString(from)):
			return []val{x} // the bytes of a string and back
		}
		return results()
	}
	if tv.IsBuiltin() {
		return f.builtin(st, c, results)
	}
	if v := variantOf(f.info, c.Fun); v != nil && enumOf(f.typeOf(c)) != nil {
		var args []val
		for _, a := range c.Args {
			args = append(args, f.eval(st, a))
		}
		return []val{f.variant(f.typeOf(c), v.Index(), args)}
	}
	g, recv := f.callee(c)
	var recvVal val
	if recv != nil {
		recvVal = f.eval(st, recv)
	}
	var args []val
	for _, a := range c.Args {
		args = append(args, f.eval(st, a))
	}
	if g == nil {
		f.eval(st, c.Fun)
		f.havocInout(st, c, nil)
		return results()
	}
	if g == f.self {
		f.recCalls = append(f.recCalls, recCall{st: st.clone(), args: append([]val{recvVal}, args...)})
	}
	if g.Decl == nil && g.Name() == "Get" && recv != nil && isOption(f.typeOf(recv)) {
		return f.optionGet(st, recvVal, f.typeOf(recv), results())
	}
	sig := g.Signature()
	env := map[*types.Var]val{}
	if sig.Recv != nil && recv != nil {
		env[sig.Recv] = recvVal
		f.boundary(st, sig.Recv.Type(), recvVal, c.Lparen, "of the receiver at this call")
	}
	params := sig.Params.Vars
	for i, p := range params {
		if i < len(args) && !(sig.Variadic && i == len(params)-1) {
			env[p] = args[i]
			f.boundary(st, p.Type(), args[i], c.Args[i].Pos(), "of the argument at this call")
		}
	}
	requires, ensures := contracts(g)
	cinfo := f.vc.infoOf(g)
	for _, r := range requires {
		if form, ok := f.contractGoal(cinfo, st, r.X, env, true); ok {
			f.obligation(st, form, c.Lparen, r.X, env, fmt.Sprintf("the precondition %s of %s at this call", types.ExprString(r.X), g.Name()))
			if fact, ok := f.contractIn(cinfo, st, r.X, env, false); ok {
				st.assume(fact)
			}
		}
	}
	// After the call: the inout arguments have new values, and the
	// postconditions hold (contract.obligation.assume), with old denoting
	// the values of the arguments before it (contract.expr.old).
	old := make(map[*types.Var]val, len(env))
	for p, v := range env {
		old[p] = v
	}
	post := f.havocInout(st, c, g)
	for p, v := range post {
		env[p] = v
	}
	rs := results()
	if sig.Results != nil {
		for i, r := range sig.Results.Vars {
			if i < len(rs) {
				env[r] = rs[i]
			}
		}
	}
	for _, en := range ensures {
		if isConstraint(f.info, en.X) {
			continue
		}
		e2 := map[*types.Var]val{}
		for k, v := range env {
			e2[k] = v
		}
		if rv := cinfo.ContractResults[en]; rv != nil && len(rs) == 1 {
			e2[rv] = rs[0]
		}
		if form, ok := f.translate(cinfo, st, en.X, e2, false, false, old); ok {
			st.assume(form)
		}
	}
	return rs
}

// havocInout gives the variables passed to inout parameters, and an inout
// receiver, new values after a call; it returns them by parameter.
func (f *fn) havocInout(st *state, c *syntax.CallExpr, g *types.Func) map[*types.Var]val {
	post := map[*types.Var]val{}
	sig, _ := core(f.typeOf(c.Fun)).(*types.Signature)
	if sig == nil {
		return post
	}
	set := func(p *types.Var, e syntax.Expr) {
		// The callee keeps the invariants (contract.type.boundaries 2).
		x := f.received(st, f.typeOf(e), types.ExprString(e))
		f.storeTo(st, e, x, false)
		if p != nil {
			post[p] = x
		}
	}
	if g != nil && g.Signature().Recv != nil && g.Signature().Recv.Inout() {
		if sel, ok := unparen(c.Fun).(*syntax.SelectorExpr); ok {
			set(g.Signature().Recv, sel.X)
		}
	}
	for i, p := range sig.Params.Vars {
		if p.Inout() && i < len(c.Args) {
			var gp *types.Var
			if g != nil && i < len(g.Signature().Params.Vars) {
				gp = g.Signature().Params.Vars[i]
			}
			set(gp, c.Args[i])
		}
	}
	return post
}

func (f *fn) builtin(st *state, c *syntax.CallExpr, results func() []val) []val {
	id, _ := unparen(c.Fun).(*syntax.Name)
	name := ""
	if id != nil {
		if b, ok := f.info.Uses[id].(*types.Builtin); ok && b.Pkg() == "" {
			name = b.Name()
		}
	}
	var args []val
	for _, a := range c.Args {
		args = append(args, f.eval(st, a))
	}
	intVal := func(t verify.Term) []val { return []val{{kind: vInt, t: t}} }
	if (name == "copy" || name == "clear") && len(c.Args) > 0 {
		// The elements of the first operand change (mem.inout.builtin).
		f.forget(st, c.Args[0])
	}
	switch name {
	case "len":
		if len(args) == 1 && args[0].kind == vSeq {
			return intVal(args[0].t)
		}
		if len(args) == 1 && isString(f.typeOf(c.Args[0])) || len(args) == 1 && seqLen(f.typeOf(c.Args[0])) == -1 {
			v := f.newVar(types.ExprString(c))
			st.assume(between(v, zeroInt, f.maxLen()))
			return intVal(v)
		}
	case "cap":
		v := f.newVar(types.ExprString(c))
		st.assume(between(v, zeroInt, f.maxLen()))
		if len(args) == 1 && args[0].kind == vSeq {
			st.assume(verify.Cmp(args[0].t, verify.LE, v))
		}
		return intVal(v)
	case "make":
		if len(args) >= 2 && args[1].kind == vInt && seqLen(f.typeOf(c)) == -1 {
			return []val{{kind: vSeq, t: args[1].t}}
		}
	case "append":
		if len(args) >= 1 && args[0].kind == vSeq {
			if c.Ellipsis.IsValid() && len(args) == 2 && args[1].kind == vSeq {
				return []val{{kind: vSeq, t: args[0].t.Add(args[1].t)}}
			}
			if !c.Ellipsis.IsValid() {
				return []val{{kind: vSeq, t: args[0].t.Add(cint(int64(len(args) - 1)))}}
			}
		}
	case "min", "max":
		t := f.typeOf(c)
		if isInt(t) {
			ok := true
			for _, a := range args {
				ok = ok && a.kind == vInt
			}
			if ok && len(args) > 0 {
				r := f.newVar(name)
				rel := verify.LE
				if name == "max" {
					rel = verify.GE
				}
				var some []verify.Formula
				for _, a := range args {
					st.assume(verify.Cmp(r, rel, a.t))
					some = append(some, verify.Cmp(r, verify.EQ, a.t))
				}
				st.assume(verify.OrOf(some...))
				return intVal(r)
			}
		}
	}
	return results()
}

// narrowing proves that the operand e, of value x, of a narrowing
// conversion lies in the range of the type it converts to
// (type.conv.int-narrow).
func (f *fn) narrowing(st *state, e syntax.Expr, x val, t types.Type) {
	if f.reported[e] {
		return
	}
	lo, hi, ok := f.info.IntBounds(t)
	why := ""
	if ok && x.kind == vInt {
		g := between(x.t, lo, hi)
		proved, model := f.prove(st, g, e.Pos(), verify.DefaultSteps)
		if proved {
			st.assume(g)
			return
		}
		if model != nil {
			why = fmt.Sprintf(", for example when %s = %s", types.ExprString(e), evalTerm(x.t, model))
			if _, isName := unparen(e).(*syntax.Name); isName {
				// The value of the variable is the counterexample.
			} else if more := f.counterexample(st, e, nil, model); more != "" {
				why += " (" + strings.TrimPrefix(more, ", for example when ") + ")"
			}
		}
	}
	f.reported[e] = true
	f.errorf(e.Pos(), "type.conv.int-narrow", "cannot convert %s (value of type %s) to type %s: the value may not fit%s, and it is not proven to (use conv.Narrow[%s] to get (%s, bool))", types.ExprString(e), f.typeOf(e), t, why, t, t)
}

// structLit evaluates a composite literal of struct type t, whose fields
// that it omits are zero, and proves the invariants of t for it
// (contract.type.boundaries 1).
func (f *fn) structLit(st *state, e *syntax.CompositeLit, s *types.Struct, t types.Type) val {
	x := f.zero(t)
	given := make([]bool, len(s.Fields))
	for i, el := range e.Elts {
		j := i
		if kv, ok := el.(*syntax.KeyValueExpr); ok {
			el = kv.Value
			j = -1
			if id, ok := kv.Key.(*syntax.Name); ok {
				for k, fl := range s.Fields {
					if fl.Name() == id.Name {
						j = k
					}
				}
			}
		}
		y := f.eval(st, el)
		if j >= 0 && j < len(x.fields) {
			x.fields[j] = y
			given[j] = true
		}
	}
	for j, fl := range s.Fields {
		if !given[j] && f.vc.zeroViolation(fl.Type(), f.info) != nil {
			return x // reported as an omitted field (contract.type.zero)
		}
	}
	f.boundary(st, t, x, e.Pos(), "for this composite literal")
	return x
}

// isOption reports whether t is an instance of Option.
func isOption(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj != nil && n.Obj.Name() == "Option" && n.Obj.Pkg() == nil
}

// enumCmp returns x == y or x != y for enum values.
func (f *fn) enumCmp(st *state, x, y val, rel verify.Rel) val {
	g, exact := sameVariant(x, y)
	if !exact {
		// Equal values are the same variant.
		b := f.fresh(st, types.Typ[types.Bool], "equal")
		st.assume(verify.OrOf(verify.NotOf(b.f), g))
		g = b.f
	}
	if rel == verify.NE {
		g = verify.NotOf(g)
	}
	return val{kind: vBool, f: g}
}

// optionGet returns the results of o.Get() for an Option value o of
// type t: the value of Some and true, or the zero value and false
// (type.option.get).
func (f *fn) optionGet(st *state, o val, t types.Type, results []val) []val {
	en := enumOf(t)
	if o.kind != vEnum || en == nil || len(results) != 2 || len(o.cases) != 2 || len(o.cases[1]) != 1 {
		return results
	}
	some := verify.Cmp(o.t, verify.EQ, cint(1))
	v := results[0]
	z := f.zero(en.Variants[1].Fields()[0].Type())
	st.assume(verify.OrOf(
		verify.AndOf(append([]verify.Formula{some}, equal(v, o.cases[1][0])...)...),
		verify.AndOf(append([]verify.Formula{verify.NotOf(some)}, equal(v, z)...)...)))
	return []val{v, {kind: vBool, f: some}}
}

// elemType returns the element type of a slice or an array type.
func elemType(t types.Type) types.Type {
	switch u := core(t).(type) {
	case *types.Slice:
		return u.Elem
	case *types.Array:
		return u.Elem
	}
	return nil
}
