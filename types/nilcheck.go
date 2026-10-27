package types

import (
	"fmt"
	"strings"

	"an/syntax"
)

// The nil safety analysis (spec/06-memory.md §9): a method of an
// interface operand is selected, or an operand of function type is
// called, only where flow analysis proves the operand non-nil
// (mem.nil.call). Facts are kept per access path (mem.nil.facts) and
// invalidated by assignments and inout accesses to the path or a prefix
// (mem.nil.invalidate).

// facts is the set of access paths known non-nil at a program point. A
// nil set stands for an unreachable point.
type facts map[string]bool

func (f facts) clone() facts {
	if f == nil {
		return nil
	}
	g := make(facts, len(f))
	for k := range f {
		g[k] = true
	}
	return g
}

// meet returns the facts that hold on both of two paths into a point.
func meet(a, b facts) facts {
	switch {
	case a == nil:
		return b.clone()
	case b == nil:
		return a.clone()
	}
	m := facts{}
	for k := range a {
		if b[k] {
			m[k] = true
		}
	}
	return m
}

// kill removes the facts about path key and the paths it is a prefix of.
func (f facts) kill(key string) {
	for k := range f {
		if k == key || strings.HasPrefix(k, key+".") || strings.HasPrefix(k, key+"[") {
			delete(f, k)
		}
	}
}

// killRoot removes the facts about every path rooted in v.
func (f facts) killRoot(v *Var) { f.kill(rootKey(v)) }

func rootKey(v *Var) string { return fmt.Sprintf("%p", v) }

// pathKey returns the key of an access path, or "" if the path cannot
// carry facts: an index that is not constant may denote another element
// after any assignment.
func pathKey(p accessPath) string {
	var b strings.Builder
	b.WriteString(rootKey(p.root))
	for _, e := range p.elems {
		switch {
		case e.field != "":
			b.WriteString("." + e.field)
		case !e.slice && e.index != nil:
			b.WriteString("[" + e.index.ExactString() + "]")
		default:
			return ""
		}
	}
	return b.String()
}

// breakTarget collects the facts at the break statements of a loop,
// switch or select.
type breakTarget struct {
	stmt   syntax.Stmt
	label  string
	breaks facts
	any    bool // a break was seen
}

type nilChecker struct {
	c       *checker
	targets []*breakTarget
	gotos   map[string]bool // labels that are the target of a goto
	// guards are the ok variables of the comma-ok forms, with the
	// variables of the values that are usable only where ok is true
	// (type.zero.ok-use).
	guards map[*Var][]okValue
	// guarded are those value variables.
	guarded map[*Var]okValue
}

// okValue is the value variable of a comma-ok form.
type okValue struct {
	v    *Var
	rule string
	what string
}

// validKey is the key of the fact that the value variable v holds a
// value of its type: the comma-ok form found one, or v was assigned
// after it (type.zero.ok-use).
func validKey(v *Var) string { return rootKey(v) + ".!ok" }

// checkNil runs the nil safety analysis on a function body. captured
// holds the facts known when a function literal is created; params are
// the parameters of the function, of which those of function type are
// known non-nil (mem.nil.func-param).
func (c *checker) checkNil(body *syntax.BlockStmt, captured facts, params []*Var) {
	n := &nilChecker{c: c, gotos: map[string]bool{}, guards: map[*Var][]okValue{}, guarded: map[*Var]okValue{}}
	syntax.Walk(body, func(node syntax.Node) bool {
		if b, ok := node.(*syntax.BranchStmt); ok && b.Tok == syntax.Goto && b.Label != nil {
			n.gotos[b.Label.Name] = true
		}
		_, isLit := node.(*syntax.FuncLit)
		return !isLit
	})
	f := captured.clone()
	if f == nil {
		f = facts{}
	}
	for _, p := range params {
		if isFuncType(p.typ) {
			f[rootKey(p)] = true
		}
	}
	n.stmts(body.List, f)
}

func (n *nilChecker) stmts(list []syntax.Stmt, f facts) facts {
	for _, s := range list {
		f = n.stmt(s, f)
	}
	return f
}

func (n *nilChecker) stmt(s syntax.Stmt, f facts) facts {
	if f == nil {
		f = facts{} // unreachable code is still checked, without facts
	}
	switch s := s.(type) {
	case *syntax.ExprStmt:
		n.expr(s.X, f)
		if isPanicCall(n.c, s.X) {
			return nil
		}
	case *syntax.DeclStmt:
		n.declStmt(s.Decl, f)
	case *syntax.AssignStmt:
		n.assign(s, f)
	case *syntax.IncDecStmt:
		n.expr(s.X, f)
	case *syntax.SendStmt:
		n.expr(s.Chan, f)
		n.expr(s.Value, f)
	case *syntax.GoStmt:
		n.expr(s.Call, f)
	case *syntax.DeferStmt:
		n.expr(s.Call, f)
	case *syntax.ReturnStmt:
		for _, e := range s.Results {
			n.expr(e, f)
		}
		return nil
	case *syntax.BranchStmt:
		return n.branch(s, f)
	case *syntax.BlockStmt:
		return n.stmts(s.List, f.clone())
	case *syntax.LabeledStmt:
		if n.gotos[s.Label.Name] {
			f = facts{} // predecessors through goto are not tracked
		}
		return n.labeled(s, f)
	case *syntax.IfStmt:
		return n.ifStmt(s, f)
	case *syntax.ForStmt:
		return n.forStmt(s, f, "")
	case *syntax.RangeStmt:
		return n.rangeStmt(s, f, "")
	case *syntax.SwitchStmt:
		return n.switchStmt(s, f, "")
	case *syntax.TypeSwitchStmt:
		return n.typeSwitch(s, f, "")
	case *syntax.SelectStmt:
		return n.selectStmt(s, f, "")
	}
	return f
}

func (n *nilChecker) labeled(s *syntax.LabeledStmt, f facts) facts {
	label := s.Label.Name
	switch t := s.Stmt.(type) {
	case *syntax.ForStmt:
		return n.forStmt(t, f, label)
	case *syntax.RangeStmt:
		return n.rangeStmt(t, f, label)
	case *syntax.SwitchStmt:
		return n.switchStmt(t, f, label)
	case *syntax.TypeSwitchStmt:
		return n.typeSwitch(t, f, label)
	case *syntax.SelectStmt:
		return n.selectStmt(t, f, label)
	}
	return n.stmt(s.Stmt, f)
}

func (n *nilChecker) branch(s *syntax.BranchStmt, f facts) facts {
	switch s.Tok {
	case syntax.Break:
		for i := len(n.targets) - 1; i >= 0; i-- {
			t := n.targets[i]
			if s.Label == nil || t.label == s.Label.Name {
				if t.any {
					t.breaks = meet(t.breaks, f)
				} else {
					t.breaks, t.any = f.clone(), true
				}
				break
			}
		}
	case syntax.Fallthrough:
		return f // merged into the next clause by switchStmt
	}
	return nil
}

func (n *nilChecker) push(s syntax.Stmt, label string) *breakTarget {
	t := &breakTarget{stmt: s, label: label}
	n.targets = append(n.targets, t)
	return t
}

func (n *nilChecker) pop() { n.targets = n.targets[:len(n.targets)-1] }

// after merges the facts at the end of a statement with those at its
// break statements.
func (t *breakTarget) after(f facts) facts {
	if !t.any {
		return f
	}
	if f == nil {
		return t.breaks
	}
	return meet(f, t.breaks)
}

func (n *nilChecker) ifStmt(s *syntax.IfStmt, f facts) facts {
	f = f.clone()
	if s.Init != nil {
		f = n.stmt(s.Init, f)
	}
	t, e := n.cond(s.Cond, f)
	thenOut := n.stmts(s.Body.List, t)
	elseOut := e
	if s.Else != nil {
		elseOut = n.stmt(s.Else, e)
	}
	return meet2(thenOut, elseOut)
}

// meet2 is meet that keeps "unreachable" when both are unreachable.
func meet2(a, b facts) facts {
	if a == nil && b == nil {
		return nil
	}
	return meet(a, b)
}

func (n *nilChecker) forStmt(s *syntax.ForStmt, f facts, label string) facts {
	f = f.clone()
	if s.Init != nil {
		f = n.stmt(s.Init, f)
	}
	// Facts about variables the loop modifies do not hold at the head.
	n.killAssigned(f, s.Body, s.Post, s.Cond)
	t, e := facts(f.clone()), facts(nil)
	if s.Cond != nil {
		t, e = n.cond(s.Cond, f)
	}
	target := n.push(s, label)
	out := n.stmts(s.Body.List, t.clone())
	if s.Post != nil && out != nil {
		n.stmt(s.Post, out)
	}
	n.pop()
	return target.after(e)
}

func (n *nilChecker) rangeStmt(s *syntax.RangeStmt, f facts, label string) facts {
	f = f.clone()
	n.expr(s.X, f)
	n.killAssigned(f, s.Body)
	for _, e := range []syntax.Expr{s.Key, s.Value} {
		if e != nil {
			n.killPath(e, f)
		}
	}
	target := n.push(s, label)
	n.stmts(s.Body.List, f.clone())
	n.pop()
	return target.after(f)
}

func (n *nilChecker) switchStmt(s *syntax.SwitchStmt, f facts, label string) facts {
	f = f.clone()
	if s.Init != nil {
		f = n.stmt(s.Init, f)
	}
	if s.Tag != nil {
		n.expr(s.Tag, f)
	}
	target := n.push(s, label)
	var out, prev facts
	reached := false
	hasDefault := false
	rest := f.clone() // facts when no case so far matched
	for _, cs := range s.Body.List {
		cl := cs.(*syntax.CaseClause)
		entry := rest.clone()
		if cl.List == nil {
			hasDefault = true
		}
		if s.Tag == nil && len(cl.List) == 1 {
			// A tagless switch: the case condition holds in the clause.
			var notMatched facts
			entry, notMatched = n.cond(cl.List[0], rest)
			rest = notMatched
		} else {
			for _, e := range cl.List {
				n.expr(e, rest)
			}
		}
		if prev != nil {
			entry = meet(entry, prev) // fallthrough
		}
		end := n.stmts(cl.Body, entry)
		prev = nil
		if len(cl.Body) > 0 {
			if b, ok := cl.Body[len(cl.Body)-1].(*syntax.BranchStmt); ok && b.Tok == syntax.Fallthrough {
				prev = end
				continue
			}
		}
		if reached {
			out = meet2(out, end)
		} else {
			out, reached = end, true
		}
	}
	n.pop()
	if !hasDefault {
		out = meet2(out, rest)
		if !reached {
			out = rest
		}
	}
	return target.after(out)
}

func (n *nilChecker) typeSwitch(s *syntax.TypeSwitchStmt, f facts, label string) facts {
	f = f.clone()
	if s.Init != nil {
		f = n.stmt(s.Init, f)
	}
	var guard syntax.Expr
	var bound *syntax.Name
	switch a := s.Assign.(type) {
	case *syntax.ExprStmt:
		if g, ok := a.X.(*syntax.TypeAssertExpr); ok {
			guard = g.X
		}
	case *syntax.AssignStmt:
		if g, ok := a.Rhs[0].(*syntax.TypeAssertExpr); ok {
			guard = g.X
		}
		bound, _ = a.Lhs[0].(*syntax.Name)
	}
	if guard != nil {
		n.expr(guard, f)
	}
	target := n.push(s, label)
	var out facts
	reached := false
	for _, cs := range s.Body.List {
		cl := cs.(*syntax.CaseClause)
		entry := f.clone()
		if len(cl.List) > 0 && !isNilCase(n.c, cl) {
			// A case with types matches only non-nil values.
			if guard != nil {
				n.setNonNil(guard, entry)
			}
			if bound != nil {
				n.setBoundNonNil(bound, cl, entry)
			}
		}
		end := n.stmts(cl.Body, entry)
		if reached {
			out = meet2(out, end)
		} else {
			out, reached = end, true
		}
	}
	n.pop()
	return target.after(meet2(out, f))
}

// setBoundNonNil records that the variable of a type switch is non-nil in
// a clause: the variable of each clause is a distinct object, found by
// its use in the clause.
func (n *nilChecker) setBoundNonNil(bound *syntax.Name, cl *syntax.CaseClause, f facts) {
	syntax.Walk(&syntax.BlockStmt{List: cl.Body}, func(node syntax.Node) bool {
		if id, ok := node.(*syntax.Name); ok && id.Name == bound.Name {
			if v, ok := n.c.info.Uses[id].(*Var); ok && v.pos == bound.Pos() {
				f[rootKey(v)] = true
			}
		}
		return true
	})
}

func isNilCase(c *checker, cl *syntax.CaseClause) bool {
	for _, e := range cl.List {
		if id, ok := unparen(e).(*syntax.Name); ok {
			if _, ok := c.info.Uses[id].(*Nil); ok {
				return true
			}
		}
	}
	return false
}

func (n *nilChecker) selectStmt(s *syntax.SelectStmt, f facts, label string) facts {
	target := n.push(s, label)
	var out facts
	reached := false
	for _, cs := range s.Body.List {
		cl := cs.(*syntax.CommClause)
		entry := f.clone()
		if cl.Comm != nil {
			entry = n.stmt(cl.Comm, entry)
		}
		end := n.stmts(cl.Body, entry)
		if reached {
			out = meet2(out, end)
		} else {
			out, reached = end, true
		}
	}
	n.pop()
	if !reached {
		return nil // select {} blocks forever
	}
	return target.after(out)
}

func (n *nilChecker) declStmt(d *syntax.GenDecl, f facts) {
	if d.Tok != syntax.Var {
		return
	}
	for _, spec := range d.Specs {
		s := spec.(*syntax.ValueSpec)
		for _, e := range s.Values {
			n.expr(e, f)
		}
		for i, name := range s.Names {
			v, ok := n.c.info.Defs[name].(*Var)
			if !ok {
				continue
			}
			f.killRoot(v)
			if len(s.Values) == len(s.Names) {
				n.assignFacts(accessPath{root: v}, v.typ, s.Values[i], f)
			}
		}
		lhs := make([]syntax.Expr, len(s.Names))
		for i, name := range s.Names {
			lhs[i] = name
		}
		n.commaOk(lhs, s.Values, f)
	}
}

func (n *nilChecker) assign(s *syntax.AssignStmt, f facts) {
	for i, e := range s.Rhs {
		if len(s.Lhs) == len(s.Rhs) && isBlank(s.Lhs[i]) {
			if _, ok := unparen(e).(*syntax.Name); ok {
				continue // a discarded value is not used
			}
		}
		n.expr(e, f)
	}
	defer n.commaOk(s.Lhs, s.Rhs, f)
	for _, e := range s.Lhs {
		// The operands of the left side, such as indices, are evaluated too.
		n.lhsOperands(e, f)
	}
	for i, e := range s.Lhs {
		p, ok := n.lhsPath(e)
		if !ok {
			continue
		}
		key := pathKey(p)
		if key == "" {
			f.killRoot(p.root) // an element with an unknown index
			continue
		}
		f.kill(key)
		if len(s.Lhs) == len(s.Rhs) && (s.Tok == syntax.Assign || s.Tok == syntax.Define) {
			t := n.c.info.Types[e].Type
			if t == nil && len(p.elems) == 0 {
				t = p.root.typ // a variable declared by :=
			}
			n.assignFacts(p, t, s.Rhs[i], f)
		}
	}
}

// lhsPath returns the access path of the left operand of an assignment.
func (n *nilChecker) lhsPath(e syntax.Expr) (accessPath, bool) {
	if id, ok := unparen(e).(*syntax.Name); ok {
		if v, ok := n.c.info.Defs[id].(*Var); ok {
			return accessPath{root: v}, true
		}
	}
	return n.c.pathOf(e)
}

func (n *nilChecker) lhsOperands(e syntax.Expr, f facts) {
	switch e := unparen(e).(type) {
	case *syntax.SelectorExpr:
		n.lhsOperands(e.X, f)
	case *syntax.IndexExpr:
		n.lhsOperands(e.X, f)
		for _, i := range e.Indices {
			n.expr(i, f)
		}
	}
}

// assignFacts records the facts that follow from assigning the value of
// e to path p of type t (mem.nil.facts, 1).
func (n *nilChecker) assignFacts(p accessPath, t Type, e syntax.Expr, f facts) {
	key := pathKey(p)
	if key == "" {
		return
	}
	if n.nonNil(e, t, f) {
		f[key] = true
	}
	// Fields given by a struct literal.
	if lit, ok := unparen(e).(*syntax.CompositeLit); ok {
		s, ok := coreOrNil(n.c.info.Types[lit].Type).(*Struct)
		if !ok {
			return
		}
		for i, el := range lit.Elts {
			var field *Var
			if kv, ok := el.(*syntax.KeyValueExpr); ok {
				if key, ok := kv.Key.(*syntax.Name); ok {
					field, _ = n.c.info.Uses[key].(*Var)
				}
				el = kv.Value
			} else if i < len(s.Fields) {
				field = s.Fields[i]
			}
			if field != nil {
				n.assignFacts(p.with(accessStep{field: field.name}), field.typ, el, f)
			}
		}
	}
}

func coreOrNil(t Type) Type {
	if t == nil {
		return nil
	}
	return coreType(t)
}

// nonNil reports whether the value of e is known non-nil.
func (n *nilChecker) nonNil(e syntax.Expr, target Type, f facts) bool {
	e = unparen(e)
	tv, ok := n.c.info.Types[e]
	if !ok {
		return false
	}
	// A value of a non-interface type assigned to an interface is a
	// conversion, which is non-nil.
	if target != nil && isInterfaceType(target) && tv.Type != nil && !isInterfaceType(tv.Type) && !isUntyped(tv.Type) && !isTypeParam(tv.Type) {
		return true
	}
	switch x := e.(type) {
	case *syntax.FuncLit:
		return true
	case *syntax.Name:
		switch obj := n.c.info.Uses[x].(type) {
		case *Func, *Variant, *Builtin:
			return true
		case *Var:
			return f[rootKey(obj)]
		}
	case *syntax.SelectorExpr:
		switch n.c.info.Uses[x.Sel].(type) {
		case *Func, *Variant, *Builtin:
			return true // a function of a package, a method value or a constructor
		}
	case *syntax.CallExpr:
		if n.c.isConversion(x) && len(x.Args) == 1 {
			// T(x) with an interface T and a non-interface x, or a
			// conversion of a non-nil value.
			at := n.c.info.Types[x.Args[0]].Type
			if isInterfaceType(tv.Type) && at != nil && !isInterfaceType(at) && !isUntyped(at) {
				return true
			}
			return n.nonNil(x.Args[0], nil, f)
		}
	case *syntax.IndexExpr:
		if ftv, ok := n.c.info.Types[x.X]; ok {
			if _, isSig := ftv.Type.(*Signature); isSig {
				return true // an instantiation of a generic function
			}
		}
	}
	if p, ok := n.c.pathOf(e); ok {
		if key := pathKey(p); key != "" {
			return f[key]
		}
	}
	return false
}

// cond evaluates a condition and returns the facts when it is true and
// when it is false (mem.nil.facts, 2).
func (n *nilChecker) cond(e syntax.Expr, f facts) (t, fa facts) {
	switch x := unparen(e).(type) {
	case *syntax.BinaryExpr:
		switch x.Op {
		case syntax.LAnd:
			t1, f1 := n.cond(x.X, f)
			t2, f2 := n.cond(x.Y, t1)
			return t2, meet(f1, f2)
		case syntax.LOr:
			t1, f1 := n.cond(x.X, f)
			t2, f2 := n.cond(x.Y, f1)
			return meet(t1, t2), f2
		case syntax.Eql, syntax.Neq:
			n.expr(x.X, f)
			n.expr(x.Y, f)
			var operand syntax.Expr
			switch {
			case isNilExpr(n.c, x.Y):
				operand = x.X
			case isNilExpr(n.c, x.X):
				operand = x.Y
			}
			t, fa = f.clone(), f.clone()
			if operand != nil {
				if x.Op == syntax.Neq {
					n.setNonNil(operand, t)
				} else {
					n.setNonNil(operand, fa)
				}
			}
			return t, fa
		}
	case *syntax.UnaryExpr:
		if x.Op == syntax.Not {
			t, fa = n.cond(x.X, f)
			return fa, t
		}
	case *syntax.Name:
		if b, ok := n.c.info.Uses[x].(*Var); ok && len(n.guards[b]) > 0 {
			t, fa = f.clone(), f.clone()
			for _, g := range n.guards[b] {
				t[validKey(g.v)] = true
			}
			return t, fa
		}
	}
	n.expr(e, f)
	return f.clone(), f.clone()
}

func isNilExpr(c *checker, e syntax.Expr) bool {
	id, ok := unparen(e).(*syntax.Name)
	if !ok {
		return false
	}
	_, ok = c.info.Uses[id].(*Nil)
	return ok
}

func (n *nilChecker) setNonNil(e syntax.Expr, f facts) {
	if p, ok := n.c.pathOf(e); ok {
		if key := pathKey(p); key != "" {
			f[key] = true
		}
	}
}

// killPath invalidates the facts about the path of e and its extensions.
func (n *nilChecker) killPath(e syntax.Expr, f facts) {
	p, ok := n.lhsPath(e)
	if !ok {
		return
	}
	if key := pathKey(p); key != "" {
		f.kill(key)
	} else {
		f.killRoot(p.root)
	}
}

// killAssigned invalidates the facts about the variables that the given
// statements and expressions assign or access with inout: a loop may
// change them before the head is reached again.
func (n *nilChecker) killAssigned(f facts, nodes ...syntax.Node) {
	for _, node := range nodes {
		if node == nil {
			continue
		}
		syntax.Walk(node, func(m syntax.Node) bool {
			switch s := m.(type) {
			case *syntax.AssignStmt:
				for _, e := range s.Lhs {
					n.killPath(e, f)
				}
			case *syntax.IncDecStmt:
				n.killPath(s.X, f)
			case *syntax.RangeStmt:
				for _, e := range []syntax.Expr{s.Key, s.Value} {
					if e != nil {
						n.killPath(e, f)
					}
				}
			case *syntax.CallExpr:
				if tv, ok := n.c.info.Types[s.Fun]; ok {
					if sig, ok := coreOrNil(tv.Type).(*Signature); ok {
						for _, a := range n.c.inoutAccesses(s, sig) {
							n.killPath(a, f)
						}
					}
				}
			case *syntax.FuncLit:
				return false
			}
			return true
		})
	}
}

// expr checks the calls and method selections in e with facts f.
func (n *nilChecker) expr(e syntax.Expr, f facts) {
	switch x := e.(type) {
	case *syntax.Name:
		if v, ok := n.c.info.Uses[x].(*Var); ok {
			if g, ok := n.guarded[v]; ok && !f[validKey(v)] {
				n.c.zeroUse(x.Pos(), v.typ, g.rule, g.what+": "+v.name+" is used where "+n.okName(v)+" may be false")
			}
		}
	case nil:
	case *syntax.ParenExpr:
		n.expr(x.X, f)
	case *syntax.FuncLit:
		// The literal holds copies of the captured variables: facts known
		// now hold in its body, except for inout variables, which it
		// captures by reference.
		captured := facts{}
		for k := range f {
			captured[k] = true
		}
		n.killInoutCaptures(x, captured)
		var params []*Var
		if sig, ok := n.c.info.Types[x].Type.(*Signature); ok {
			params = sig.Params.Vars
		}
		n.c.checkNil(x.Body, captured, params)
	case *syntax.CallExpr:
		n.callee(x, f)
		for _, a := range x.Args {
			n.expr(a, f)
		}
		n.funcArgs(x, f)
		// inout accesses invalidate facts (mem.nil.invalidate).
		if tv, ok := n.c.info.Types[x.Fun]; ok {
			if sig, ok := coreOrNil(tv.Type).(*Signature); ok {
				for _, a := range n.c.inoutAccesses(x, sig) {
					n.killPath(a, f)
				}
			}
		}
		// A closure that captures inout variables may assign them.
		for _, v := range n.c.capturedBy(x.Fun) {
			f.killRoot(v)
		}
	case *syntax.SelectorExpr:
		n.expr(x.X, f)
		n.methodOfInterface(x, f)
	case *syntax.IndexExpr:
		n.expr(x.X, f)
		for _, i := range x.Indices {
			n.expr(i, f)
		}
	case *syntax.SliceExpr:
		n.expr(x.X, f)
		n.expr(x.Low, f)
		n.expr(x.High, f)
		n.expr(x.Max, f)
	case *syntax.TypeAssertExpr:
		n.expr(x.X, f)
	case *syntax.UnaryExpr:
		n.expr(x.X, f)
	case *syntax.BinaryExpr:
		if x.Op == syntax.LAnd || x.Op == syntax.LOr {
			n.cond(x, f)
			return
		}
		n.expr(x.X, f)
		n.expr(x.Y, f)
	case *syntax.CompositeLit:
		for _, el := range x.Elts {
			n.expr(el, f)
		}
	case *syntax.KeyValueExpr:
		if _, isName := x.Key.(*syntax.Name); !isName {
			n.expr(x.Key, f)
		}
		n.expr(x.Value, f)
	}
}

func (n *nilChecker) killInoutCaptures(lit *syntax.FuncLit, f facts) {
	syntax.Walk(lit.Body, func(m syntax.Node) bool {
		if id, ok := m.(*syntax.Name); ok {
			if v, ok := n.c.info.Uses[id].(*Var); ok && v.inout {
				f.killRoot(v)
			}
		}
		return true
	})
}

// methodOfInterface checks the selection of a method of an interface
// operand (mem.nil.call).
func (n *nilChecker) methodOfInterface(x *syntax.SelectorExpr, f facts) {
	m, ok := n.c.info.Uses[x.Sel].(*Func)
	if !ok {
		return
	}
	tv, ok := n.c.info.Types[x.X]
	if !ok || tv.IsType() || tv.Type == nil || !isInterfaceType(tv.Type) {
		return
	}
	if !n.nonNil(x.X, nil, f) {
		n.c.errorf(x.Sel.Pos(), "mem.nil.call", "%s.%s: %s may be nil here; check %s != nil first", exprString(x.X), m.name, exprString(x.X), exprString(x.X))
	}
}

// callee checks the call of an operand of function type (mem.nil.call).
func (n *nilChecker) callee(call *syntax.CallExpr, f facts) {
	fun := unparen(call.Fun)
	n.expr(fun, f)
	tv, ok := n.c.info.Types[fun]
	if !ok || !tv.IsValue() || tv.Type == nil {
		return
	}
	if _, ok := coreOrNil(tv.Type).(*Signature); !ok || isTypeParam(tv.Type) {
		return
	}
	if !n.nonNil(fun, nil, f) {
		n.c.errorf(call.Fun.Pos(), "mem.nil.call", "call of %s: the function value may be nil here; check %s != nil first", exprString(fun), exprString(fun))
	}
}

func isPanicCall(c *checker, e syntax.Expr) bool {
	call, ok := unparen(e).(*syntax.CallExpr)
	if !ok {
		return false
	}
	id, ok := unparen(call.Fun).(*syntax.Name)
	if !ok {
		return false
	}
	b, ok := c.info.Uses[id].(*Builtin)
	return ok && b.id == _Panic
}

// isFuncType reports whether t is a function type (not a type parameter).
func isFuncType(t Type) bool {
	if t == nil || isTypeParam(t) {
		return false
	}
	_, ok := t.Underlying().(*Signature)
	return ok
}

// funcArgs checks that the arguments for parameters of function type are
// known non-nil (mem.nil.func-param).
func (n *nilChecker) funcArgs(call *syntax.CallExpr, f facts) {
	tv, ok := n.c.info.Types[call.Fun]
	if !ok || !tv.IsValue() || tv.Type == nil {
		return
	}
	sig, ok := coreOrNil(tv.Type).(*Signature)
	if !ok {
		return
	}
	params := sig.Params.Vars
	if len(call.Args) == 1 && len(params) > 1 {
		return // the results of a call with several results
	}
	for i, a := range call.Args {
		var t Type
		switch {
		case sig.Variadic && i >= len(params)-1 && !call.Ellipsis.IsValid():
			t = params[len(params)-1].typ.(*Slice).Elem
		case i < len(params):
			t = params[i].typ
		}
		if !isFuncType(t) || n.nonNil(a, t, f) {
			continue
		}
		n.c.errorf(a.Pos(), "mem.nil.func-param", "argument %s for a parameter of function type may be nil; pass a function or check it first", exprString(a))
	}
}

// commaOk records the comma-ok assignment of lhs from rhs, whose value
// variable is usable only where the ok variable is true; and, for other
// assignments, that the assigned value variables hold values and that
// the assigned ok variables no longer guard them (type.zero.ok-use).
func (n *nilChecker) commaOk(lhs, rhs []syntax.Expr, f facts) {
	varOf := func(e syntax.Expr) *Var {
		id, ok := unparen(e).(*syntax.Name)
		if !ok {
			return nil
		}
		if v, ok := n.c.info.Defs[id].(*Var); ok {
			return v
		}
		v, _ := n.c.info.Uses[id].(*Var)
		return v
	}
	for _, e := range lhs {
		if v := varOf(e); v != nil {
			delete(n.guards, v) // ok assigned again
			if _, ok := n.guarded[v]; ok {
				f[validKey(v)] = true
			}
		}
	}
	if len(lhs) != 2 || len(rhs) != 1 {
		return
	}
	v, b := varOf(lhs[0]), varOf(lhs[1])
	if v == nil {
		return
	}
	var g okValue
	switch x := unparen(rhs[0]).(type) {
	case *syntax.IndexExpr:
		if _, ok := coreOrNil(n.c.info.Types[x.X].Type).(*Map); !ok {
			return
		}
		g = okValue{v, "type.zero.map-index", "comma-ok map index " + exprString(x)}
	case *syntax.TypeAssertExpr:
		g = okValue{v, "type.zero.assert", "comma-ok type assertion " + exprString(x)}
	case *syntax.UnaryExpr:
		if x.Op != syntax.Arrow {
			return
		}
		g = okValue{v, "type.zero.chan-receive", "comma-ok receive " + exprString(x)}
	default:
		return
	}
	n.guarded[v] = g
	delete(f, validKey(v))
	if b != nil {
		n.guards[b] = append(n.guards[b], g)
	}
}

// okName returns the name of the ok variable that guards v.
func (n *nilChecker) okName(v *Var) string {
	for b, gs := range n.guards {
		for _, g := range gs {
			if g.v == v {
				return b.name
			}
		}
	}
	return "ok"
}

func isBlank(e syntax.Expr) bool {
	id, ok := unparen(e).(*syntax.Name)
	return ok && id.Name == "_"
}
