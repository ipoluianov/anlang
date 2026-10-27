package vc

import (
	"fmt"
	"math/big"

	"an/syntax"
	"an/types"
	"an/verify"
)

// contractIn translates a contract expression of package info with
// unbounded integers (contract.expr.math): env gives the values of the
// parameters and results; the other variables have their values in st.
// It reports a construct that it does not translate if report is set.
func (f *fn) contractIn(info *types.Info, st *state, e syntax.Expr, env map[*types.Var]val, report bool) (verify.Formula, bool) {
	return f.translate(info, st, e, env, report, false, f.oldOf(info))
}

// contractGoal translates a contract expression as contractIn does, for
// the goal of an obligation: the quantifiers differ (std.math.quantifier).
func (f *fn) contractGoal(info *types.Info, st *state, e syntax.Expr, env map[*types.Var]val, report bool) (verify.Formula, bool) {
	return f.translate(info, st, e, env, report, true, f.oldOf(info))
}

// oldOf returns the values at entry that old denotes in a contract
// expression of package info checked in the body of f
// (contract.expr.old).
func (f *fn) oldOf(info *types.Info) map[*types.Var]val {
	if info == f.info {
		return f.entry
	}
	return nil
}

// translate translates a contract expression; old gives the values of the
// parameters that old denotes.
func (f *fn) translate(info *types.Info, st *state, e syntax.Expr, env map[*types.Var]val, report, goal bool, old map[*types.Var]val) (verify.Formula, bool) {
	if info == nil {
		return verify.Formula{}, false
	}
	c := &contract{f: f, info: info, st: st, env: env, old: old, goal: goal}
	x := c.eval(e)
	if c.bad != nil {
		if report {
			f.errorf(c.bad.Pos(), "contract.obligation.unproven", "%s in a contract expression: not supported yet (spec/24-roadmap.md, step 23)", c.why)
		}
		return verify.Formula{}, false
	}
	if x.kind != vBool {
		return verify.Formula{}, false
	}
	return x.f, true
}

type contract struct {
	f    *fn
	info *types.Info
	st   *state
	env  map[*types.Var]val
	// old gives the values at entry of the parameters, which the
	// argument of old reads (contract.expr.old).
	old   map[*types.Var]val
	inOld bool
	bad   syntax.Expr
	why   string
	// goal is set for the goal of an obligation, neg under an odd number
	// of negations, and mixed under == or != of booleans, where a
	// quantifier has both polarities.
	goal, neg bool
	mixed     int
}

func (c *contract) fail(e syntax.Expr, why string) val {
	if c.bad == nil {
		c.bad, c.why = e, why
	}
	return val{}
}

func (c *contract) eval(e syntax.Expr) val {
	f, info := c.f, c.info
	tv := info.Types[e]
	if tv.Value != nil {
		save := f.info
		f.info = info
		x := f.eval(c.st, e) // a constant
		f.info = save
		return x
	}
	switch e := e.(type) {
	case *syntax.ParenExpr:
		return c.eval(e.X)
	case *syntax.Name:
		if vr, ok := info.Uses[e].(*types.Variant); ok && !vr.HasData() && enumOf(tv.Type) != nil {
			return f.variant(tv.Type, vr.Index(), nil)
		}
		v, ok := info.Uses[e].(*types.Var)
		if !ok {
			return c.fail(e, fmt.Sprintf("%s", e.Name))
		}
		if c.inOld {
			// A parameter at entry, a variable bound in the argument,
			// or a package-level variable, of which nothing is known.
			if x, ok := c.old[v]; ok {
				return x
			}
			if x, ok := c.env[v]; ok {
				return x
			}
			return f.received(c.st, v.Type(), "old("+v.Name()+")")
		}
		if x, ok := c.env[v]; ok {
			return x
		}
		if info == f.info {
			return f.read(c.st, v)
		}
		return f.fresh(c.st, v.Type(), v.Name())
	case *syntax.UnaryExpr:
		if e.Op == syntax.Not {
			c.neg = !c.neg
			x := c.eval(e.X)
			c.neg = !c.neg
			if x.kind == vBool {
				return val{kind: vBool, f: verify.NotOf(x.f)}
			}
			return c.fail(e, "the operation !")
		}
		x := c.eval(e.X)
		switch {
		case e.Op == syntax.Sub && x.kind == vInt:
			return val{kind: vInt, t: x.t.Scale(big.NewInt(-1))}
		case e.Op == syntax.Add:
			return x
		}
	case *syntax.BinaryExpr:
		if e.Op == syntax.Eql || e.Op == syntax.Neq {
			c.mixed++
			defer func() { c.mixed-- }()
		}
		x, y := c.eval(e.X), c.eval(e.Y)
		switch {
		case e.Op == syntax.LAnd && x.kind == vBool && y.kind == vBool:
			return val{kind: vBool, f: verify.AndOf(x.f, y.f)}
		case e.Op == syntax.LOr && x.kind == vBool && y.kind == vBool:
			return val{kind: vBool, f: verify.OrOf(x.f, y.f)}
		case x.kind == vInt && y.kind == vInt:
			if rel, ok := relOf[e.Op]; ok {
				return val{kind: vBool, f: verify.Cmp(x.t, rel, y.t)}
			}
			switch e.Op {
			case syntax.Add:
				return val{kind: vInt, t: x.t.Add(y.t)}
			case syntax.Sub:
				return val{kind: vInt, t: x.t.Sub(y.t)}
			case syntax.Mul:
				if k, ok := isConstTerm(x.t); ok {
					return val{kind: vInt, t: y.t.Scale(k)}
				}
				if k, ok := isConstTerm(y.t); ok {
					return val{kind: vInt, t: x.t.Scale(k)}
				}
			case syntax.Quo, syntax.Rem:
				if k, ok := isConstTerm(y.t); ok && k.Sign() > 0 {
					q := f.quotient(c.st, x.t, k)
					if e.Op == syntax.Quo {
						return val{kind: vInt, t: q}
					}
					return val{kind: vInt, t: x.t.Sub(q.Scale(k))}
				}
			case syntax.Shl, syntax.Shr:
				if r, ok := c.shift(e.Op, x.t, y.t); ok {
					return val{kind: vInt, t: r}
				}
			case syntax.And, syntax.Or, syntax.Xor, syntax.AndNot:
				if r, ok := c.bitwise(e.Op, x.t, y.t, tv.Type); ok {
					return val{kind: vInt, t: r}
				}
			}
		case x.kind == vEnum && y.kind == vEnum && (e.Op == syntax.Eql || e.Op == syntax.Neq):
			return f.enumCmp(c.st, x, y, relOf[e.Op])
		case x.kind == vBool && y.kind == vBool && (e.Op == syntax.Eql || e.Op == syntax.Neq):
			same := verify.OrOf(verify.AndOf(x.f, y.f), verify.AndOf(verify.NotOf(x.f), verify.NotOf(y.f)))
			if e.Op == syntax.Neq {
				same = verify.NotOf(same)
			}
			return val{kind: vBool, f: same}
		}
		if c.bad == nil {
			return c.fail(e, "the operation "+e.Op.String())
		}
	case *syntax.CallExpr:
		if sel, ok := unparen(e.Fun).(*syntax.SelectorExpr); ok && len(e.Args) == 3 {
			if b, ok := info.Uses[sel.Sel].(*types.Builtin); ok && b.Pkg() == "an/math" && (b.Name() == "ForAll" || b.Name() == "Exists") {
				return c.quantifier(e, b.Name() == "ForAll")
			}
		}
		if id, ok := unparen(e.Fun).(*syntax.Name); ok && len(e.Args) == 1 {
			if b, ok := info.Uses[id].(*types.Builtin); ok && b.Pkg() == "" && b.Name() == "old" {
				saved := c.inOld
				c.inOld = true
				x := c.eval(e.Args[0])
				c.inOld = saved
				return x
			}
		}
		if vr := variantOf(info, e.Fun); vr != nil && enumOf(tv.Type) != nil {
			var args []val
			for _, a := range e.Args {
				args = append(args, c.eval(a))
			}
			return f.variant(tv.Type, vr.Index(), args)
		}
		ftv := info.Types[e.Fun]
		if ftv.IsType() && len(e.Args) == 1 {
			x := c.eval(e.Args[0])
			if x.kind == vInt && isInt(ftv.Type) {
				return x // the value of an integer
			}
		}
		if id, ok := unparen(e.Fun).(*syntax.Name); ok && len(e.Args) > 0 {
			if b, ok := info.Uses[id].(*types.Builtin); ok && b.Pkg() == "" && (b.Name() == "min" || b.Name() == "max") {
				// The least or the greatest argument.
				r := f.newVar(b.Name())
				rel := verify.LE
				if b.Name() == "max" {
					rel = verify.GE
				}
				var some []verify.Formula
				for _, a := range e.Args {
					x := c.eval(a)
					if x.kind != vInt {
						return c.fail(e, "the call "+types.ExprString(e))
					}
					c.st.assume(verify.Cmp(r, rel, x.t))
					some = append(some, verify.Cmp(r, verify.EQ, x.t))
				}
				c.st.assume(verify.OrOf(some...))
				return val{kind: vInt, t: r}
			}
		}
		if id, ok := unparen(e.Fun).(*syntax.Name); ok && len(e.Args) == 1 {
			if b, ok := info.Uses[id].(*types.Builtin); ok && b.Pkg() == "" && b.Name() == "len" {
				x := c.eval(e.Args[0])
				if x.kind == vSeq {
					return val{kind: vInt, t: x.t}
				}
			}
		}
		if sel, ok := unparen(e.Fun).(*syntax.SelectorExpr); ok && len(e.Args) == 1 {
			if b, ok := info.Uses[sel.Sel].(*types.Builtin); ok && b.Pkg() == "an/math" && b.Name() == "Sum" {
				x := c.eval(e.Args[0])
				if x.kind == vSeq && x.seq != 0 {
					return val{kind: vInt, t: f.sum(c.st, x)}
				}
				if x.kind == vSeq {
					return val{kind: vInt, t: f.newVar("sum")}
				}
			}
		}
		return c.fail(e, "the call "+types.ExprString(e))
	case *syntax.IndexExpr:
		// Outside the bounds of its operand, an element has an unknown
		// value (contract.expr.elements).
		if len(e.Indices) == 1 {
			x, i := c.eval(e.X), c.eval(e.Indices[0])
			if et := tv.Type; x.kind == vSeq && i.kind == vInt && isInt(et) {
				if x.seq == 0 {
					return val{kind: vInt, t: f.newVar("element")}
				}
				save := f.info
				f.info = info
				defer func() { f.info = save }()
				return val{kind: vInt, t: f.element(c.st, x, i.t, et)}
			}
		}
		return c.fail(e, "the index expression "+types.ExprString(e))
	case *syntax.SliceExpr:
		x := c.eval(e.X)
		bound := func(b syntax.Expr, def verify.Term) (verify.Term, bool) {
			if b == nil {
				return def, true
			}
			v := c.eval(b)
			return v.t, v.kind == vInt
		}
		lo, ok1 := bound(e.Low, cint(0))
		if x.kind == vSeq && ok1 {
			if hi, ok2 := bound(e.High, x.t); ok2 {
				r := val{kind: vSeq, t: hi.Sub(lo), seq: x.seq}
				if x.seq != 0 {
					r.off = x.off.Add(lo)
				}
				return r
			}
		}
		return c.fail(e, "the slice expression "+types.ExprString(e))
	case *syntax.SelectorExpr:
		if sel := info.Selections[e]; sel != nil && sel.Kind == types.FieldVal {
			if y, ok := field(c.eval(e.X), sel.Index); ok && y.kind != vNone {
				return y
			}
			if c.bad == nil && (isInt(tv.Type) || isBool(tv.Type)) {
				return f.fresh(c.st, tv.Type, types.ExprString(e)) // a field of a value that is not tracked
			}
		}
		if id, ok := unparen(e.X).(*syntax.Name); ok {
			if _, ok := info.Uses[id].(*types.PkgName); ok {
				if vr, ok := info.Uses[e.Sel].(*types.Variant); ok && !vr.HasData() && enumOf(tv.Type) != nil {
					return f.variant(tv.Type, vr.Index(), nil)
				}
				if v, ok := info.Uses[e.Sel].(*types.Var); ok {
					return f.fresh(c.st, v.Type(), v.Name())
				}
			}
		}
		return c.fail(e, "the selector "+types.ExprString(e))
	}
	return c.fail(e, "the expression "+types.ExprString(e))
}

// shift returns x << k or x >> k for a constant k ≥ 0 on mathematical
// integers (contract.expr.bitwise): x·2^k and ⌊x / 2^k⌋.
func (c *contract) shift(op syntax.Kind, x, y verify.Term) (verify.Term, bool) {
	k, ok := isConstTerm(y)
	if !ok || k.Sign() < 0 || k.Cmp(big.NewInt(64)) >= 0 {
		return verify.Term{}, false
	}
	d := new(big.Int).Lsh(oneInt, uint(k.Int64()))
	if op == syntax.Shl {
		return x.Scale(d), true
	}
	return c.f.floorPow2(c.st, x, uint(k.Int64())), true
}

// bitwise returns x op y on mathematical integers in two's complement
// with infinitely many bits (contract.expr.bitwise). A mask 2^m-1 is the
// remainder modulo 2^m for every integer. Otherwise the bits of type t
// give the result when both operands lie in its range; outside it the
// result is unknown, so that the facts stay sound.
func (c *contract) bitwise(op syntax.Kind, x, y verify.Term, t types.Type) (verify.Term, bool) {
	if !isInt(t) {
		return verify.Term{}, false
	}
	if kx, ok := isConstTerm(x); ok {
		if ky, ok := isConstTerm(y); ok {
			z := new(big.Int)
			switch op {
			case syntax.And:
				z.And(kx, ky)
			case syntax.Or:
				z.Or(kx, ky)
			case syntax.Xor:
				z.Xor(kx, ky)
			default:
				z.AndNot(kx, ky)
			}
			return verify.ConstBig(z), true
		}
	}
	f := c.f
	save := f.info
	f.info = c.info
	defer func() { f.info = save }()
	lo, hi, ok := f.info.IntBounds(t)
	if !ok {
		return verify.Term{}, false
	}
	sub := &state{vars: c.st.vars, defs: c.st.cloneDefs()}
	r, ok := f.bitwise(sub, op, x, y, t)
	if !ok {
		return verify.Term{}, false
	}
	if op == syntax.And && (isMask(x) || isMask(y)) {
		for _, g := range sub.facts {
			c.st.assume(g)
		}
		c.st.defs = sub.defs
		return r, true
	}
	guard := verify.AndOf(between(x, lo, hi), between(y, lo, hi))
	c.st.assume(verify.OrOf(verify.AndOf(guard, verify.AndOf(sub.facts...)), verify.NotOf(guard)))
	return r, true
}

// isMask reports whether t is a constant 2^m-1, m ≥ 0.
func isMask(t verify.Term) bool {
	k, ok := isConstTerm(t)
	return ok && k.Sign() >= 0 && new(big.Int).And(k, new(big.Int).Add(k, oneInt)).Sign() == 0
}

// quantifier translates math.ForAll(lo, hi, func(i T) bool { return p })
// or math.Exists (std.math.quantifier). Where the formula needs the
// quantifier for every value — a ForAll that the goal states, an Exists
// that a fact states, or either under a negation the other way — it is
// p for a new variable i in [lo, hi). Elsewhere it is a new boolean b,
// and the proofs add instances of p at the integers of the elements they
// read: b and i in [lo, hi) imply p for ForAll; p and i in [lo, hi)
// imply b for Exists.
func (c *contract) quantifier(e *syntax.CallExpr, all bool) val {
	f := c.f
	if c.mixed > 0 {
		return c.fail(e, "a quantifier under == or !=")
	}
	lo, hi := c.eval(e.Args[0]), c.eval(e.Args[1])
	lit, _ := unparen(e.Args[2]).(*syntax.FuncLit)
	if lo.kind != vInt || hi.kind != vInt || lit == nil || len(lit.Type.Params.List) != 1 || len(lit.Type.Params.List[0].Names) != 1 {
		return c.fail(e, "the quantifier "+types.ExprString(e.Fun))
	}
	param, _ := c.info.Defs[lit.Type.Params.List[0].Names[0]].(*types.Var)
	ret, _ := lit.Body.List[0].(*syntax.ReturnStmt)
	if param == nil || ret == nil || len(ret.Results) != 1 {
		return c.fail(e, "the quantifier "+types.ExprString(e.Fun))
	}
	body := ret.Results[0]
	positive := c.goal != c.neg // the polarity of a goal
	// instance evaluates p at i = k in state st, in the context of c.
	vars := make(map[*types.Var]val, len(c.st.vars))
	for v, x := range c.st.vars {
		vars[v] = x
	}
	env := make(map[*types.Var]val, len(c.env)+1)
	for v, x := range c.env {
		env[v] = x
	}
	sub := *c
	instance := func(st *state, k verify.Term) (verify.Formula, bool) {
		in := sub
		in.st, in.bad = st, nil
		saved := st.vars
		st.vars = vars
		defer func() { st.vars = saved }()
		in.env = make(map[*types.Var]val, len(env)+1)
		for v, x := range env {
			in.env[v] = x
		}
		in.env[param] = val{kind: vInt, t: k}
		x := in.eval(body)
		return x.f, in.bad == nil && x.kind == vBool
	}
	inRange := func(k verify.Term) verify.Formula {
		return verify.AndOf(verify.Cmp(lo.t, verify.LE, k), verify.Cmp(k, verify.LT, hi.t))
	}
	if all == positive {
		// For every value: a new variable.
		k := f.newVar(param.Name())
		p, ok := instance(c.st, k)
		if !ok {
			return c.fail(e, "the predicate of "+types.ExprString(e.Fun))
		}
		if all {
			return val{kind: vBool, f: verify.OrOf(verify.NotOf(inRange(k)), p)}
		}
		return val{kind: vBool, f: verify.AndOf(inRange(k), p)}
	}
	b := f.fresh(c.st, types.Typ[types.Bool], types.ExprString(e.Fun))
	c.st.quants = append(c.st.quants[:len(c.st.quants):len(c.st.quants)], quant{b: b.f, all: all, inRange: inRange, instance: instance, offsets: offsets(vars, env, c.old), shifts: shifts(c.info, body, param)})
	return b
}

// quant is a quantifier that the proofs instantiate.
type quant struct {
	b        verify.Formula
	all      bool
	inRange  func(verify.Term) verify.Formula
	instance func(*state, verify.Term) (verify.Formula, bool)
	offsets  []verify.Term // of the sequences that the predicate may read
	shifts   []int64       // d of the elements x[i+d] that the predicate reads
}

// offsets returns the offsets of the sequences among values.
func offsets(maps ...map[*types.Var]val) []verify.Term {
	seen := map[string]bool{"0": true}
	out := []verify.Term{cint(0)}
	var add func(x val)
	add = func(x val) {
		switch x.kind {
		case vSeq:
			if x.seq != 0 && !seen[x.off.String()] {
				seen[x.off.String()] = true
				out = append(out, x.off)
			}
		case vStruct:
			for _, y := range x.fields {
				add(y)
			}
		}
	}
	for _, m := range maps {
		for _, x := range m {
			add(x)
		}
	}
	return out
}

// maxInstances bounds the instances of a quantifier in a proof.
const maxInstances = 32

// instantiate returns the instances of the quantifiers of
// st at the indices of the elements that st reads: the element at
// position a of a sequence is element a - off of a part of it at offset
// off.
func (f *fn) instantiate(st *state) (side, insts []verify.Formula) {
	for _, q := range st.quants {
		seen := map[string]bool{}
		var cands []verify.Term
		add := func(t verify.Term) {
			if len(cands) < maxInstances && !seen[t.String()] {
				seen[t.String()] = true
				cands = append(cands, t)
			}
		}
		for _, d := range q.shifts {
			for _, a := range st.reads {
				for _, o := range q.offsets {
					add(a.Sub(o).Sub(cint(d)))
				}
			}
		}
		for _, k := range cands {
			sub := st.clone()
			n := len(sub.facts)
			p, ok := q.instance(sub, k)
			if !ok {
				continue
			}
			side = append(side, sub.facts[n:]...)
			if q.all {
				insts = append(insts, verify.OrOf(verify.NotOf(q.b), verify.NotOf(q.inRange(k)), p))
			} else {
				insts = append(insts, verify.OrOf(verify.NotOf(q.inRange(k)), verify.NotOf(p), q.b))
			}
		}
	}
	return side, insts
}

// shifts returns the constants d of the index expressions x[i+d], x[i-d]
// and x[i] (d = 0) of e for the variable i; 0 if there are none.
func shifts(info *types.Info, e syntax.Expr, i *types.Var) []int64 {
	seen := map[int64]bool{}
	var out []int64
	add := func(d int64) {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	isVar := func(x syntax.Expr) bool {
		id, ok := unparen(x).(*syntax.Name)
		return ok && info.Uses[id] == i
	}
	constant := func(x syntax.Expr) (int64, bool) {
		tv := info.Types[x]
		if tv.Value == nil {
			return 0, false
		}
		k, ok := new(big.Int).SetString(tv.Value.ExactString(), 10)
		return k.Int64(), ok && k.IsInt64()
	}
	syntax.Walk(e, func(n syntax.Node) bool {
		ix, ok := n.(*syntax.IndexExpr)
		if !ok || len(ix.Indices) != 1 {
			return true
		}
		switch x := unparen(ix.Indices[0]).(type) {
		case *syntax.BinaryExpr:
			if k, ok := constant(x.Y); ok && isVar(x.X) && (x.Op == syntax.Add || x.Op == syntax.Sub) {
				if x.Op == syntax.Sub {
					k = -k
				}
				add(k)
			}
		default:
			if isVar(x) {
				add(0)
			}
		}
		return true
	})
	if len(out) == 0 {
		add(0)
	}
	return out
}
