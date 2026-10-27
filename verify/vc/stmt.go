package vc

import (
	"fmt"
	"math/big"

	"an/syntax"
	"an/types"
	"an/verify"
)

func (f *fn) stmts(st *state, list []syntax.Stmt) {
	for _, s := range list {
		if st.dead {
			return
		}
		f.stmt(st, s, "")
	}
}

// define marks a declared variable as local.
func (f *fn) define(id *syntax.Name) *types.Var {
	v, ok := f.info.Defs[id].(*types.Var)
	if ok {
		f.locals[v] = true
	}
	return v
}

func (f *fn) stmt(st *state, s syntax.Stmt, label string) {
	switch s := s.(type) {
	case *syntax.EmptyStmt, *syntax.ContractStmt:
	case *syntax.ExprStmt:
		if c, ok := unparen(s.X).(*syntax.CallExpr); ok {
			if f.isAssert(c) {
				if len(c.Args) == 1 {
					if g, ok := f.contractGoal(f.info, st, c.Args[0], nil, true); ok {
						f.obligation(st, g, c.Lparen, c.Args[0], nil, "the assertion "+types.ExprString(c.Args[0]))
						if h, ok := f.contractIn(f.info, st, c.Args[0], nil, false); ok {
							st.assume(h)
						}
					}
				}
				return
			}
			f.call(st, c)
			if f.isPanic(c) {
				st.dead = true
			}
			return
		}
		f.eval(st, s.X)
	case *syntax.DeclStmt:
		if s.Decl.Tok != syntax.Var {
			return
		}
		for _, spec := range s.Decl.Specs {
			vs, ok := spec.(*syntax.ValueSpec)
			if !ok {
				continue
			}
			vals := f.values(st, vs.Values, len(vs.Names))
			for i, id := range vs.Names {
				v := f.define(id)
				if v == nil {
					continue
				}
				x := f.zero(v.Type())
				if vals != nil {
					x = vals[i]
				}
				f.write(st, v, x)
			}
		}
	case *syntax.AssignStmt:
		f.assign(st, s)
	case *syntax.IncDecStmt:
		op := syntax.Add
		if s.Tok == syntax.Dec {
			op = syntax.Sub
		}
		f.opAssign(st, s, s.X, op, val{kind: vInt, t: cint(1)}, types.Typ[types.UntypedInt])
	case *syntax.BlockStmt:
		f.stmts(st, s.List)
	case *syntax.IfStmt:
		if s.Init != nil {
			f.stmt(st, s.Init, "")
		}
		c := f.cond(st, s.Cond)
		pre := st.clone()
		then := st.clone()
		then.assume(c)
		f.stmts(then, s.Body.List)
		els := st.clone()
		els.assume(verify.NotOf(c))
		if s.Else != nil {
			f.stmt(els, s.Else, "")
		}
		*st = *f.merge(pre, []*state{then, els})
	case *syntax.ForStmt:
		f.forStmt(st, s, label)
	case *syntax.RangeStmt:
		f.rangeStmt(st, s, label)
	case *syntax.SwitchStmt:
		f.switchStmt(st, s, label)
	case *syntax.TypeSwitchStmt:
		f.typeSwitch(st, s, label)
	case *syntax.LabeledStmt:
		f.stmt(st, s.Stmt, s.Label.Name)
	case *syntax.BranchStmt:
		f.branch(st, s)
	case *syntax.ReturnStmt:
		var rs []val
		if len(s.Results) == 0 {
			for _, r := range f.results {
				rs = append(rs, f.read(st, r))
			}
		} else {
			rs = f.values(st, s.Results, len(f.results))
		}
		f.exit(st, s.Return, rs)
		st.dead = true
	case *syntax.DeferStmt:
		for _, a := range s.Call.Args {
			f.eval(st, a)
		}
		// The deferred call may change its inout arguments after the
		// return (stmt.defer.go).
		if sig, _ := core(f.typeOf(s.Call.Fun)).(*types.Signature); sig != nil {
			for i, p := range sig.Params.Vars {
				if p.Inout() && i < len(s.Call.Args) {
					f.deferred = append(f.deferred, s.Call.Args[i])
				}
			}
		}
		if g, recv := f.callee(s.Call); g != nil && recv != nil && g.Signature().Recv != nil && g.Signature().Recv.Inout() {
			f.deferred = append(f.deferred, recv)
		}
	case *syntax.GoStmt:
		for _, a := range s.Call.Args {
			f.eval(st, a)
		}
	case *syntax.SendStmt:
		f.eval(st, s.Chan)
		f.boundary(st, f.typeOf(s.Value), f.eval(st, s.Value), s.Value.Pos(), "sent on a channel")
	case *syntax.AsmStmt:
		// The instructions may change any variable.
		for v := range st.vars {
			f.write(st, v, f.fresh(st, v.Type(), v.Name()))
		}
	case *syntax.SelectStmt:
		pre := st.clone()
		var outs []*state
		for _, cc := range s.Body.List {
			if comm, ok := cc.(*syntax.CommClause); ok {
				b := pre.clone()
				f.havoc(b, f.assigned(comm))
				f.stmts(b, comm.Body)
				outs = append(outs, b)
			}
		}
		*st = *f.merge(pre, outs)
	}
}

func (f *fn) isPanic(c *syntax.CallExpr) bool {
	id, ok := unparen(c.Fun).(*syntax.Name)
	if !ok {
		return false
	}
	b, ok := f.info.Uses[id].(*types.Builtin)
	return ok && b.Name() == "panic" && b.Pkg() == ""
}

// values evaluates the right side of an assignment of n values.
func (f *fn) values(st *state, es []syntax.Expr, n int) []val {
	if len(es) == 0 {
		return nil
	}
	if len(es) == n {
		var out []val
		for _, e := range es {
			out = append(out, f.eval(st, e))
		}
		return out
	}
	// One expression with n values: a call, or a comma-ok form.
	if c, ok := unparen(es[0]).(*syntax.CallExpr); ok && !f.info.Types[c.Fun].IsType() {
		rs := f.call(st, c)
		for len(rs) < n {
			rs = append(rs, val{})
		}
		return rs[:n]
	}
	f.eval(st, es[0])
	out := make([]val, n)
	if t, ok := f.info.Types[es[0]].Type.(*types.Tuple); ok {
		for i := 0; i < n && i < len(t.Vars); i++ {
			out[i] = f.fresh(st, t.Vars[i].Type(), "value")
		}
	}
	return out
}

// lvalue evaluates the operands of the left side of an assignment, with
// the bounds check of an index, and returns its variable, if any.
func (f *fn) lvalue(st *state, e syntax.Expr, define bool) *types.Var {
	switch e := unparen(e).(type) {
	case *syntax.Name:
		if e.Name == "_" {
			return nil
		}
		if define {
			if v := f.define(e); v != nil {
				return v
			}
		}
		v, _ := f.info.Uses[e].(*types.Var)
		return v
	case *syntax.IndexExpr:
		f.index(st, e)
	case *syntax.SelectorExpr:
		f.eval(st, e.X)
	}
	return nil
}

func (f *fn) assign(st *state, s *syntax.AssignStmt) {
	switch s.Tok {
	case syntax.Assign, syntax.Define:
	default:
		op := map[syntax.Kind]syntax.Kind{
			syntax.AddAssign: syntax.Add, syntax.SubAssign: syntax.Sub, syntax.MulAssign: syntax.Mul,
			syntax.QuoAssign: syntax.Quo, syntax.RemAssign: syntax.Rem, syntax.AndAssign: syntax.And,
			syntax.OrAssign: syntax.Or, syntax.XorAssign: syntax.Xor, syntax.ShlAssign: syntax.Shl,
			syntax.ShrAssign: syntax.Shr, syntax.AndNotAssign: syntax.AndNot,
		}[s.Tok]
		y := f.eval(st, s.Rhs[0])
		f.opAssign(st, s, s.Lhs[0], op, y, f.typeOf(s.Rhs[0]))
		return
	}
	vals := f.values(st, s.Rhs, len(s.Lhs))
	var vars []*types.Var
	for _, l := range s.Lhs {
		vars = append(vars, f.lvalue(st, l, s.Tok == syntax.Define))
	}
	for i, v := range vars {
		if i >= len(vals) {
			break
		}
		if v != nil {
			if v.IsPackageLevel() {
				f.boundary(st, v.Type(), vals[i], s.Lhs[i].Pos(), "stored into "+v.Name())
			}
			f.write(st, v, vals[i])
		} else {
			f.storeTo(st, s.Lhs[i], vals[i], true)
		}
	}
}

// storeTo records the store of x into a variable, an element or a field
// e, whose operands are evaluated: the variable that holds e gets its new
// value. With check, a store into an element or a field is a boundary of
// x (contract.type.boundaries 4).
func (f *fn) storeTo(st *state, e syntax.Expr, x val, check bool) {
	if _, ok := unparen(e).(*syntax.Name); !ok && check {
		f.boundary(st, f.typeOf(e), x, e.Pos(), "stored into "+types.ExprString(e))
	}
	switch e := unparen(e).(type) {
	case *syntax.Name:
		if v := f.rootVar(e); v != nil {
			f.write(st, v, x)
		}
	case *syntax.IndexExpr:
		v := f.rootVar(e.X)
		if v == nil {
			f.forget(st, e.X)
			return
		}
		xs := f.read(st, v)
		if xs.kind != vSeq || xs.seq == 0 {
			return
		}
		j, ok := f.indexAt[e]
		if ok && x.kind == vInt && isInt(f.typeOf(e)) {
			f.write(st, v, f.store(st, xs, j, x.t, f.typeOf(e)))
			return
		}
		xs.seq = f.newSeq()
		f.write(st, v, xs)
	case *syntax.SliceExpr:
		// An inout subslice: the elements of the variable change.
		f.forget(st, e.X)
	case *syntax.SelectorExpr:
		// A field of a variable: x.f, x.f.g.
		var path []int
		var root syntax.Expr = e
		for {
			sel, ok := unparen(root).(*syntax.SelectorExpr)
			if !ok {
				break
			}
			s := f.info.Selections[sel]
			if s == nil || s.Kind != types.FieldVal {
				f.forget(st, e.X)
				return
			}
			path = append(append([]int(nil), s.Index...), path...)
			root = sel.X
		}
		v := f.rootVar(root)
		if v == nil || !f.tracked(v) {
			f.forget(st, root)
			return
		}
		if y, ok := withField(f.read(st, v), path, x); ok {
			f.write(st, v, y)
		} else {
			f.write(st, v, f.fresh(st, v.Type(), v.Name()))
		}
	}
}

// forget gives the elements of the sequence that holds part e of a
// variable unknown values.
func (f *fn) forget(st *state, e syntax.Expr) {
	v := f.pathRoot(e)
	if v == nil || !f.tracked(v) {
		return
	}
	if xs := f.read(st, v); xs.kind == vSeq && xs.seq != 0 && f.rootVar(e) == v {
		xs.seq = f.newSeq() // the elements of v, of the same length
		f.write(st, v, xs)
		return
	}
	f.write(st, v, f.fresh(st, v.Type(), v.Name()))
}

// opAssign evaluates x op= y and x++ of statement n.
func (f *fn) opAssign(st *state, n syntax.Node, lhs syntax.Expr, op syntax.Kind, y val, yt types.Type) {
	t := f.typeOf(lhs)
	var x val
	v := f.rootVar(lhs)
	if l, ok := unparen(lhs).(*syntax.IndexExpr); ok {
		x = f.index(st, l)
	} else if _, ok := unparen(lhs).(*syntax.SelectorExpr); ok {
		x = f.eval(st, lhs)
	} else if v != nil {
		x = f.read(st, v)
	} else {
		f.lvalue(st, lhs, false)
		x = f.fresh(st, t, types.ExprString(lhs))
	}
	r := f.arith(st, n, op, x, y, t, yt)
	if v != nil {
		f.write(st, v, r)
	} else {
		f.storeTo(st, lhs, r, true)
	}
}

// ---------------------------------------------------------------------
// Joins.

func sameTerm(a, b verify.Term) bool { return a.String() == b.String() }

func sameVal(a, b val) bool {
	switch {
	case a.kind != b.kind:
		return false
	case a.kind == vInt:
		return sameTerm(a.t, b.t)
	case a.kind == vSeq:
		return sameTerm(a.t, b.t) && a.seq == b.seq && (a.seq == 0 || sameTerm(a.off, b.off))
	case a.kind == vBool:
		return a.f.String() == b.f.String()
	case a.kind == vStruct:
		if len(a.fields) != len(b.fields) {
			return false
		}
		for i := range a.fields {
			if !sameVal(a.fields[i], b.fields[i]) {
				return false
			}
		}
	case a.kind == vEnum:
		if !sameTerm(a.t, b.t) || len(a.cases) != len(b.cases) {
			return false
		}
		for k := range a.cases {
			if len(a.cases[k]) != len(b.cases[k]) {
				return false
			}
			for j := range a.cases[k] {
				if !sameVal(a.cases[k][j], b.cases[k][j]) {
					return false
				}
			}
		}
	}
	return true
}

// equal returns the facts that m equals x.
func equal(m, x val) []verify.Formula {
	if x.kind != m.kind {
		return nil
	}
	switch m.kind {
	case vInt, vSeq:
		return []verify.Formula{verify.Cmp(m.t, verify.EQ, x.t)}
	case vBool:
		return []verify.Formula{verify.OrOf(verify.AndOf(m.f, x.f), verify.AndOf(verify.NotOf(m.f), verify.NotOf(x.f)))}
	case vStruct:
		var fs []verify.Formula
		for i := range m.fields {
			if i < len(x.fields) {
				fs = append(fs, equal(m.fields[i], x.fields[i])...)
			}
		}
		return fs
	case vEnum:
		fs := []verify.Formula{verify.Cmp(m.t, verify.EQ, x.t)}
		for k := range m.cases {
			for j := range m.cases[k] {
				if k < len(x.cases) && j < len(x.cases[k]) {
					fs = append(fs, equal(m.cases[k][j], x.cases[k][j])...)
				}
			}
		}
		return fs
	}
	return nil
}

// merge joins the states that continue after a branch point whose state
// was pre: each fact after pre is assumed in a disjunct, and a variable
// with different values gets a new one that equals the value of each
// disjunct.
func (f *fn) merge(pre *state, outs []*state) *state {
	var alive []*state
	for _, o := range outs {
		if !o.dead {
			alive = append(alive, o)
		}
	}
	switch len(alive) {
	case 0:
		d := pre.clone()
		d.dead = true
		return d
	case 1:
		return alive[0]
	}
	res := &state{vars: map[*types.Var]val{}, facts: append([]verify.Formula(nil), pre.facts...), fvars: append([][]int(nil), pre.fvars...), defs: pre.cloneDefs(), apps: pre.cloneApps(), quants: pre.quants[:len(pre.quants):len(pre.quants)], reads: pre.reads[:len(pre.reads):len(pre.reads)]}
	n := len(pre.facts)
	extra := make([][]verify.Formula, len(alive))
	for i, o := range alive {
		extra[i] = append([]verify.Formula(nil), o.facts[n:]...)
	}
	for v, x0 := range alive[0].vars {
		same, everywhere := true, true
		for _, o := range alive[1:] {
			x, ok := o.vars[v]
			if !ok {
				everywhere = false
				break
			}
			if !sameVal(x0, x) {
				same = false
			}
		}
		if !everywhere {
			continue
		}
		if same {
			res.vars[v] = x0
			continue
		}
		m := f.fresh(res, v.Type(), v.Name())
		for i, o := range alive {
			extra[i] = append(extra[i], equal(m, o.vars[v])...)
		}
		res.vars[v] = m
	}
	var disj []verify.Formula
	trivial := false
	for _, e := range extra {
		if len(e) == 0 {
			trivial = true
		}
		disj = append(disj, verify.AndOf(e...))
	}
	if !trivial {
		res.assume(verify.OrOf(disj...))
	}
	return res
}

// ---------------------------------------------------------------------
// Loops.

// assigned returns the variables that n may assign, or an element or a
// field of: by assignment, by increment, as range variables, as inout
// arguments and receivers, and in asm statements.
func (f *fn) assigned(n syntax.Node) map[*types.Var]bool {
	out := map[*types.Var]bool{}
	add := func(e syntax.Expr) {
		// The variable that holds e: a store into an element, a subslice
		// or a field changes it too.
		if v := f.pathRoot(e); v != nil {
			out[v] = true
		}
	}
	syntax.Walk(n, func(m syntax.Node) bool {
		switch m := m.(type) {
		case *syntax.FuncLit:
			return false
		case *syntax.AssignStmt:
			for _, l := range m.Lhs {
				add(l)
			}
		case *syntax.IncDecStmt:
			add(m.X)
		case *syntax.RangeStmt:
			if m.Tok == syntax.Assign {
				if m.Key != nil {
					add(m.Key)
				}
				if m.Value != nil {
					add(m.Value)
				}
			}
		case *syntax.AsmStmt:
			for v := range f.locals {
				out[v] = true
			}
		case *syntax.CallExpr:
			sig, _ := core(f.typeOf(m.Fun)).(*types.Signature)
			if sig != nil {
				for i, p := range sig.Params.Vars {
					if p.Inout() && i < len(m.Args) {
						add(m.Args[i])
					}
				}
			}
			if g, recv := f.callee(m); g != nil && recv != nil && g.Signature().Recv != nil && g.Signature().Recv.Inout() {
				add(recv)
			}
		}
		return true
	})
	return out
}

// havoc gives the variables new unknown values.
func (f *fn) havoc(st *state, vars map[*types.Var]bool) {
	for v := range vars {
		if _, ok := st.vars[v]; ok {
			f.write(st, v, f.fresh(st, v.Type(), v.Name()))
		}
	}
}

// induction returns the variable of a for loop init; cond; post whose
// post statement adds a positive constant to it, or subtracts one (dir
// -1), and that the body does not assign (contract.loop.induction).
func (f *fn) induction(s *syntax.ForStmt) (*types.Var, int) {
	var v *types.Var
	dir := 0
	switch p := s.Post.(type) {
	case *syntax.IncDecStmt:
		v = f.rootVar(p.X)
		dir = 1
		if p.Tok == syntax.Dec {
			dir = -1
		}
	case *syntax.AssignStmt:
		if (p.Tok == syntax.AddAssign || p.Tok == syntax.SubAssign) && len(p.Lhs) == 1 {
			if k, ok := f.constant(p.Rhs[0]); ok && k.Sign() > 0 {
				v = f.rootVar(p.Lhs[0])
				dir = 1
				if p.Tok == syntax.SubAssign {
					dir = -1
				}
			}
		}
	}
	if v == nil || !isInt(v.Type()) || f.assigned(s.Body)[v] {
		return nil, 0
	}
	return v, dir
}

func (f *fn) pushJump(label string, loop bool) *jump {
	j := &jump{label: label, loop: loop}
	f.jumps = append(f.jumps, j)
	return j
}

func (f *fn) popJump() { f.jumps = f.jumps[:len(f.jumps)-1] }

func (f *fn) branch(st *state, s *syntax.BranchStmt) {
	switch s.Tok {
	case syntax.Break, syntax.Continue:
		for k := len(f.jumps) - 1; k >= 0; k-- {
			j := f.jumps[k]
			if s.Label != nil && j.label != s.Label.Name || s.Tok == syntax.Continue && !j.loop {
				continue
			}
			if s.Tok == syntax.Break {
				j.breaks = append(j.breaks, st.clone())
			} else {
				j.continues = append(j.continues, st.clone())
			}
			break
		}
	}
	st.dead = true
}

// invariants returns the invariant clauses of a loop body
// (contract.loop.header).
func invariants(body *syntax.BlockStmt) []*syntax.ContractStmt {
	var out []*syntax.ContractStmt
	for _, s := range body.List {
		c, ok := s.(*syntax.ContractStmt)
		if !ok || c.Word != "invariant" {
			break
		}
		out = append(out, c)
	}
	return out
}

// checkInvariants proves the invariants of a loop at its header in state
// st (contract.loop.obligations); when is "on entry to the loop" or
// "after an iteration of the loop".
func (f *fn) checkInvariants(st *state, invs []*syntax.ContractStmt, when string) {
	if st.dead {
		return
	}
	for _, c := range invs {
		if g, ok := f.contractGoal(f.info, st, c.X, nil, false); ok {
			f.obligation(st, g, c.Pos(), c.X, nil, fmt.Sprintf("the invariant %s %s", exprString(c.X), when))
		}
	}
}

// assumeInvariants assumes the invariants of a loop in state st.
func (f *fn) assumeInvariants(st *state, invs []*syntax.ContractStmt) {
	for _, c := range invs {
		if g, ok := f.contractIn(f.info, st, c.X, nil, true); ok {
			st.assume(g)
		}
	}
}

func (f *fn) forStmt(st *state, s *syntax.ForStmt, label string) {
	if s.Init != nil {
		f.stmt(st, s.Init, "")
	}
	invs := invariants(s.Body)
	f.checkInvariants(st, invs, "on entry to the loop")
	iv, dir := f.induction(s)
	var start val
	if iv != nil {
		start = f.read(st, iv)
	}
	header := st.clone()
	hv := f.assigned(s.Body)
	if s.Post != nil {
		for v := range f.assigned(s.Post) {
			hv[v] = true
		}
	}
	f.havoc(header, hv)
	if iv != nil && start.kind == vInt {
		// The induction variable has not passed its initial value.
		if x, ok := header.vars[iv]; ok && x.kind == vInt {
			rel := verify.GE
			if dir < 0 {
				rel = verify.LE
			}
			header.assume(verify.Cmp(x.t, rel, start.t))
		}
	}
	f.assumeInvariants(header, invs)
	j := f.pushJump(label, true)
	var c verify.Formula
	hasCond := s.Cond != nil
	if hasCond {
		c = f.cond(header, s.Cond)
	}
	body := header.clone()
	if hasCond {
		body.assume(c)
	}
	f.stmts(body, s.Body.List)
	end := f.merge(header, append([]*state{body}, j.continues...))
	if s.Post != nil && !end.dead {
		f.stmt(end, s.Post, "")
	}
	f.checkInvariants(end, invs, "after an iteration of the loop")
	f.popJump()
	exit := header.clone()
	if hasCond {
		exit.assume(verify.NotOf(c))
	} else {
		exit.dead = true
	}
	*st = *f.merge(header, append([]*state{exit}, j.breaks...))
}

func (f *fn) rangeStmt(st *state, s *syntax.RangeStmt, label string) {
	xt := f.typeOf(s.X)
	x := f.eval(st, s.X)
	var n verify.Term
	known := false
	switch {
	case isInt(xt) && x.kind == vInt:
		// A range over n ≤ 0 has no iterations: their number is max(n, 0).
		n, known = x.t, true
		if k, ok := isConstTerm(x.t); !ok || k.Sign() < 0 {
			m := f.newVar("iterations")
			st.assume(verify.OrOf(
				verify.AndOf(verify.Cmp(x.t, verify.GE, cint(0)), verify.Cmp(m, verify.EQ, x.t)),
				verify.AndOf(verify.Cmp(x.t, verify.LT, cint(0)), verify.Cmp(m, verify.EQ, cint(0)))))
			n = m
		}
	case x.kind == vSeq:
		n, known = x.t, true
	}
	invs := invariants(s.Body)
	// A range over a string counts bytes in its key and decodes runes:
	// its key is not the number of iterations.
	counted := known && (isInt(xt) || seqLen(xt) >= -1) && !isMapOrFunc(xt) && !isString(xt)
	if len(invs) > 0 && !counted {
		f.errorf(invs[0].Pos(), "contract.obligation.unproven", "the invariant of a range loop over %s: not supported yet (spec/24-roadmap.md, step 24)", xt)
		invs = nil
	}
	var kv *types.Var
	if s.Key != nil {
		kv = f.lvalue(st, s.Key, s.Tok == syntax.Define)
	}
	// The variable of an iterator that yields inout access: its value
	// satisfies the invariants of its type when yielded and at the end of
	// the iteration (contract.type.boundaries).
	inoutVar := s.Key
	if n := yieldsInout(xt); n == 2 {
		inoutVar = s.Value
	} else if n == 0 {
		inoutVar = nil
	}
	// At the header, the key is the number of completed iterations k
	// (contract.loop.range).
	withKey := func(at *state, k verify.Term) *state {
		c := at.clone()
		if kv != nil && isInt(kv.Type()) {
			f.write(c, kv, val{kind: vInt, t: k})
		}
		return c
	}
	if len(invs) > 0 {
		f.checkInvariants(withKey(st, cint(0)), invs, "on entry to the loop")
	}
	header := st.clone()
	hv := f.assigned(s.Body)
	f.havoc(header, hv)
	var k verify.Term
	if counted {
		k = f.newVar("iterations")
		header.assume(verify.AndOf(verify.Cmp(cint(0), verify.LE, k), verify.Cmp(k, verify.LE, n)))
		if len(invs) > 0 {
			h := withKey(header, k)
			f.assumeInvariants(h, invs)
			*header = *h
		}
	}
	j := f.pushJump(label, true)
	body := header.clone()
	var iv *types.Var // the inout loop variable
	if kv != nil {
		key := f.fresh(body, kv.Type(), kv.Name())
		if inoutVar == s.Key {
			key = f.received(body, kv.Type(), kv.Name())
			iv = kv
		}
		if counted && key.kind == vInt {
			body.assume(verify.Cmp(k, verify.LT, n))
			key = val{kind: vInt, t: k}
		} else if known && isString(xt) && key.kind == vInt {
			body.assume(verify.AndOf(verify.Cmp(cint(0), verify.LE, key.t), verify.Cmp(key.t, verify.LT, n)))
		}
		f.write(body, kv, key)
	} else if counted {
		body.assume(verify.Cmp(k, verify.LT, n))
	}
	if s.Value != nil {
		if vv := f.lvalue(body, s.Value, s.Tok == syntax.Define); vv != nil {
			v := f.fresh(body, vv.Type(), vv.Name())
			if inoutVar == s.Value {
				v = f.received(body, vv.Type(), vv.Name())
				iv = vv
			}
			if counted && x.kind == vSeq && x.seq != 0 && isInt(vv.Type()) {
				v = val{kind: vInt, t: f.element(body, x, k, vv.Type())} // the element k
			}
			f.write(body, vv, v)
		}
	}
	if iv != nil {
		f.inoutLoops = append(f.inoutLoops, iv)
	}
	f.stmts(body, s.Body.List)
	if iv != nil {
		f.inoutLoops = f.inoutLoops[:len(f.inoutLoops)-1]
		for _, at := range append(append([]*state{body}, j.continues...), j.breaks...) {
			f.boundary(at, iv.Type(), f.read(at, iv), s.Body.Rbrace, "of inout loop variable "+iv.Name()+" at the end of an iteration")
		}
	}
	if len(invs) > 0 {
		end := f.merge(header, append([]*state{body}, j.continues...))
		if !end.dead {
			f.checkInvariants(withKey(end, k.Add(cint(1))), invs, "after an iteration of the loop")
		}
	}
	f.popJump()
	exit := header.clone()
	if counted {
		exit.assume(verify.Cmp(k, verify.EQ, n))
		if len(invs) > 0 {
			h := withKey(exit, n)
			f.assumeInvariants(h, invs)
			*exit = *h
		}
	}
	// The range variables declared with = keep a value of the loop.
	for _, e := range []syntax.Expr{s.Key, s.Value} {
		if e != nil && s.Tok == syntax.Assign {
			if v := f.rootVar(e); v != nil {
				f.write(exit, v, f.fresh(exit, v.Type(), v.Name()))
			}
		}
	}
	*st = *f.merge(header, append([]*state{exit}, j.breaks...))
}

func isMapOrFunc(t types.Type) bool {
	if t == nil {
		return false
	}
	switch core(t).(type) {
	case *types.Map, *types.Signature, *types.Chan:
		return true
	}
	return false
}

// ---------------------------------------------------------------------
// Switch statements.

func (f *fn) switchStmt(st *state, s *syntax.SwitchStmt, label string) {
	if s.Init != nil {
		f.stmt(st, s.Init, "")
	}
	var tag val
	if s.Tag != nil {
		tag = f.eval(st, s.Tag)
	}
	pre := st.clone()
	j := f.pushJump(label, false)
	var outs []*state
	var conds []verify.Formula
	var deflt *syntax.CaseClause
	variants := s.Tag != nil && enumOf(f.typeOf(s.Tag)) != nil
	clauseCond := func(cc *syntax.CaseClause) verify.Formula {
		var alts []verify.Formula
		for _, e := range cc.List {
			if s.Tag == nil {
				alts = append(alts, f.cond(pre, e))
				continue
			}
			if variants {
				// A variant pattern (stmt.switch.variant).
				name := e
				if cl, ok := unparen(e).(*syntax.CompositeLit); ok && cl.Type != nil {
					name = cl.Type
				}
				if v := variantOf(f.info, name); v != nil && tag.kind == vEnum {
					alts = append(alts, verify.Cmp(tag.t, verify.EQ, cint(int64(v.Index()))))
				} else {
					alts = append(alts, f.fresh(pre, types.Typ[types.Bool], "case").f)
				}
				continue
			}
			x := f.eval(pre, e)
			switch {
			case tag.kind == vInt && x.kind == vInt:
				alts = append(alts, verify.Cmp(tag.t, verify.EQ, x.t))
			case tag.kind == vBool && x.kind == vBool:
				alts = append(alts, verify.OrOf(verify.AndOf(tag.f, x.f), verify.AndOf(verify.NotOf(tag.f), verify.NotOf(x.f))))
			default:
				alts = append(alts, f.fresh(pre, types.Typ[types.Bool], "case").f)
			}
		}
		return verify.OrOf(alts...)
	}
	for _, c := range s.Body.List {
		cc, ok := c.(*syntax.CaseClause)
		if !ok {
			continue
		}
		if cc.List == nil {
			deflt = cc
			continue
		}
		conds = append(conds, clauseCond(cc))
	}
	k := 0
	var fall *state // the end of a body that falls through to the next one
	for _, c := range s.Body.List {
		cc, ok := c.(*syntax.CaseClause)
		if !ok {
			continue
		}
		b := pre.clone()
		if cc.List == nil {
			for _, g := range conds {
				b.assume(verify.NotOf(g))
			}
		} else {
			b.assume(conds[k])
			k++
			if variants {
				f.bindVariant(b, cc, tag)
			}
		}
		if fall != nil {
			b = f.merge(pre, []*state{b, fall})
			fall = nil
		}
		body := cc.Body
		if n := len(body); n > 0 {
			if br, ok := body[n-1].(*syntax.BranchStmt); ok && br.Tok == syntax.Fallthrough {
				f.stmts(b, body[:n-1])
				fall = b
				continue
			}
		}
		f.stmts(b, body)
		outs = append(outs, b)
	}
	if deflt == nil {
		none := pre.clone()
		for _, g := range conds {
			none.assume(verify.NotOf(g))
		}
		outs = append(outs, none)
	}
	f.popJump()
	*st = *f.merge(pre, append(outs, j.breaks...))
}

// bindVariant gives the bindings of the pattern of a case clause of a
// variant switch over tag the values of the fields of the variant.
func (f *fn) bindVariant(st *state, cc *syntax.CaseClause, tag val) {
	if len(cc.List) != 1 {
		return
	}
	cl, ok := unparen(cc.List[0]).(*syntax.CompositeLit)
	if !ok || cl.Type == nil {
		return
	}
	v := variantOf(f.info, cl.Type)
	for j, el := range cl.Elts {
		id, ok := el.(*syntax.Name)
		if !ok {
			continue
		}
		b, ok := f.info.Defs[id].(*types.Var)
		if !ok {
			continue
		}
		f.locals[b] = true
		x := f.fresh(st, b.Type(), b.Name())
		if v != nil && tag.kind == vEnum && v.Index() < len(tag.cases) && j < len(tag.cases[v.Index()]) {
			x = tag.cases[v.Index()][j]
		}
		f.write(st, b, x)
	}
}

func (f *fn) typeSwitch(st *state, s *syntax.TypeSwitchStmt, label string) {
	pre := st.clone()
	j := f.pushJump(label, false)
	var outs []*state
	hasDefault := false
	for _, c := range s.Body.List {
		cc, ok := c.(*syntax.CaseClause)
		if !ok {
			continue
		}
		if cc.List == nil {
			hasDefault = true
		}
		b := pre.clone()
		f.stmts(b, cc.Body)
		outs = append(outs, b)
	}
	if !hasDefault {
		outs = append(outs, pre.clone())
	}
	f.popJump()
	*st = *f.merge(pre, append(outs, j.breaks...))
}

var _ = big.NewInt

// yieldsInout returns the position, 1 or 2, of the inout parameter of the
// yield function of an iterator type t, or 0 (stmt.range.inout-iter).
func yieldsInout(t types.Type) int {
	sig, ok := core(t).(*types.Signature)
	if !ok || sig.Params.Len() != 1 {
		return 0
	}
	y, ok := core(sig.Params.At(0).Type()).(*types.Signature)
	if !ok || y.Params.Len() == 0 {
		return 0
	}
	if n := y.Params.Len(); y.Params.At(n - 1).Inout() {
		return n
	}
	return 0
}
