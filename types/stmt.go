package types

import (
	"slices"
	"sort"

	"an/syntax"
)

// funcContext is the state of the function body being checked.
type funcContext struct {
	sig    *Signature
	vars   []*Var // local variables (decl.var.unused)
	labels map[string]*Label
	// enclosing lists the labeled statements around the current
	// statement, innermost last.
	enclosing []*syntax.LabeledStmt
}

// stmtContext holds the statements permitted at a point of a body.
type stmtContext int

const (
	breakOK stmtContext = 1 << iota
	continueOK
	fallthroughOK
)

// funcBody checks the body of a function declared in file scope file.
func (c *checker) funcBody(file *Scope, name string, sig *Signature, d *syntax.FuncDecl, body *syntax.BlockStmt) {
	ft := d.Type
	savedScope, savedFn := c.scope, c.fn
	defer func() { c.scope, c.fn = savedScope, savedFn }()
	c.fn = &funcContext{sig: sig, labels: map[string]*Label{}}
	c.scope = NewScope(file)
	c.scope.isFunc = true

	if sig.Recv != nil {
		c.declareParams(d.Recv, &Tuple{Vars: []*Var{sig.Recv}})
	}
	c.declareParams(ft.Params, sig.Params)
	c.declareParams(ft.Results, sig.Results)
	c.collectLabels(body)
	c.stmtList(0, body.List)
	if sig.Results.Len() > 0 && !c.isTerminatingList(body.List, "") {
		c.errorf(body.Rbrace, "stmt.go", "missing return")
	}

	var unused []*Var
	for _, v := range c.fn.vars {
		if !v.used && v.name != "_" {
			unused = append(unused, v)
		}
	}
	sort.Slice(unused, func(i, j int) bool { return posLess(unused[i].pos, unused[j].pos) })
	for _, v := range unused {
		c.errorf(v.pos, "decl.var.unused", "declared and not used: %s", v.name)
	}
	for _, name := range sortedLabels(c.fn.labels) {
		if l := c.fn.labels[name]; !l.used {
			c.errorf(l.pos, "stmt.go", "label %s defined and not used", name)
		}
	}
	c.flushUntyped()
}

func sortedLabels(m map[string]*Label) []string {
	var names []string
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// declareParams declares the named parameters or results of a function
// in the function scope.
func (c *checker) declareParams(list *syntax.FieldList, t *Tuple) {
	if list == nil {
		return
	}
	i := 0
	for _, f := range list.List {
		if len(f.Names) == 0 {
			i++
			continue
		}
		for _, name := range f.Names {
			// Duplicate names are reported by funcType.
			v := t.Vars[i]
			if name.Name == "_" {
				c.info.Defs[name] = nil
			} else if c.scope.Insert(v) == nil {
				c.info.Defs[name] = v
			}
			i++
		}
	}
}

func (c *checker) openScope() { c.scope = NewScope(c.scope) }

func (c *checker) closeScope() { c.scope = c.scope.parent }

func (c *checker) stmtList(ctxt stmtContext, list []syntax.Stmt) {
	inner := ctxt &^ fallthroughOK
	// fallthrough is permitted only as the last statement of a clause.
	last := len(list) - 1
	for last >= 0 {
		if e, ok := list[last].(*syntax.EmptyStmt); ok && e.Implicit {
			last--
			continue
		}
		break
	}
	for i, s := range list {
		ctx := inner
		if i == last {
			ctx = ctxt
		}
		c.stmt(ctx, s)
	}
}

func (c *checker) stmt(ctxt stmtContext, s syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.EmptyStmt, *syntax.BadStmt:

	case *syntax.DeclStmt:
		c.declStmt(s.Decl)

	case *syntax.LabeledStmt:
		c.fn.enclosing = append(c.fn.enclosing, s)
		c.stmt(ctxt, s.Stmt)
		c.fn.enclosing = c.fn.enclosing[:len(c.fn.enclosing)-1]

	case *syntax.ExprStmt:
		c.exprStmt(s)

	case *syntax.SendStmt:
		var ch, v operand
		c.expr(&ch, s.Chan)
		c.expr(&v, s.Value)
		if ch.mode == invalid || v.mode == invalid {
			return
		}
		t, ok := ch.typ.Underlying().(*Chan)
		if !ok {
			c.errorf(s.Arrow, "stmt.go", "invalid operation: cannot send to non-channel %s", &ch)
			return
		}
		if t.Dir == RecvOnly {
			c.errorf(s.Arrow, "stmt.go", "invalid operation: cannot send to receive-only channel %s", &ch)
			return
		}
		c.assignment(&v, t.Elem, "send")

	case *syntax.IncDecStmt:
		var x operand
		c.expr(&x, s.X)
		if x.mode == invalid {
			return
		}
		if !isNumeric(x.typ) {
			c.errorf(s.X.Pos(), "stmt.go", "invalid operation: %s%s (non-numeric type %s)", exprString(s.X), s.Tok, x.typ)
			return
		}
		c.lhsAddressable(s.X, &x)

	case *syntax.AssignStmt:
		switch s.Tok {
		case syntax.Define:
			c.shortVarDecl(s)
		case syntax.Assign:
			c.assignVars(s.Lhs, s.Rhs)
		default:
			c.opAssign(s)
		}

	case *syntax.GoStmt:
		c.callStmt("go", s.Call)

	case *syntax.DeferStmt:
		c.callStmt("defer", s.Call)

	case *syntax.ReturnStmt:
		c.returnStmt(s)

	case *syntax.BranchStmt:
		c.branchStmt(ctxt, s)

	case *syntax.BlockStmt:
		c.openScope()
		c.stmtList(ctxt&^fallthroughOK, s.List)
		c.closeScope()

	case *syntax.IfStmt:
		c.openScope()
		if s.Init != nil {
			c.stmt(0, s.Init)
		}
		c.condition(s.Cond, "if")
		c.stmt(ctxt&^fallthroughOK, s.Body)
		if s.Else != nil {
			c.stmt(ctxt&^fallthroughOK, s.Else)
		}
		c.closeScope()

	case *syntax.SwitchStmt:
		c.switchStmt(ctxt, s)

	case *syntax.TypeSwitchStmt:
		c.typeSwitchStmt(ctxt, s)

	case *syntax.SelectStmt:
		c.selectStmt(ctxt, s)

	case *syntax.ForStmt:
		c.openScope()
		if s.Init != nil {
			c.stmt(0, s.Init)
		}
		if s.Cond != nil {
			c.condition(s.Cond, "for")
		}
		if s.Post != nil {
			if a, ok := s.Post.(*syntax.AssignStmt); ok && a.Tok == syntax.Define {
				c.errorf(a.Pos(), "stmt.if-for.go", "cannot declare in post statement of for loop")
				c.useArgs(a.Rhs)
			} else {
				c.stmt(0, s.Post)
			}
		}
		c.stmt(breakOK|continueOK, s.Body)
		c.closeScope()

	case *syntax.RangeStmt:
		c.rangeStmt(s)

	case *syntax.ContractStmt:
		step := 21
		if s.Word == "invariant" {
			step = 23
		}
		c.notSupported(s.Pos(), step, s.Word+" clauses")

	default:
		c.errorf(s.Pos(), "stmt.go", "invalid statement")
	}
}

// condition checks the condition of an if or for statement.
func (c *checker) condition(e syntax.Expr, what string) {
	var x operand
	c.expr(&x, e)
	if x.mode == invalid {
		return
	}
	if !isBoolean(x.typ) {
		c.errorf(e.Pos(), "stmt.if-for.go", "non-boolean condition in %s statement", what)
		return
	}
	c.defaultUntyped(&x)
}

func (c *checker) exprStmt(s *syntax.ExprStmt) {
	var x operand
	c.rawExpr(&x, s.X, nil)
	if x.mode == invalid {
		return
	}
	call, isCall := unparen(s.X).(*syntax.CallExpr)
	switch {
	case isCall && !c.isConversion(call) && !c.isValueBuiltin(call):
		c.unusedError(call, &x)
		return
	case x.mode == commaok:
		if u, ok := unparen(s.X).(*syntax.UnaryExpr); ok && u.Op == syntax.Arrow {
			return // receive
		}
	}
	c.errorf(s.X.Pos(), "stmt.go", "%s is not used", exprString(s.X))
}

// isValueBuiltin reports whether call calls a built-in function whose
// result must be used.
func (c *checker) isValueBuiltin(call *syntax.CallExpr) bool {
	tv, ok := c.info.Types[call.Fun]
	if !ok || !tv.IsBuiltin() {
		return false
	}
	var id *syntax.Name
	switch f := unparen(call.Fun).(type) {
	case *syntax.Name:
		id = f
	case *syntax.SelectorExpr:
		id = f.Sel
	}
	b, _ := c.info.Uses[id].(*Builtin)
	if b == nil {
		return false
	}
	switch b.id {
	case _Append, _Cap, _Len, _Make, _Max, _Min:
		return true
	}
	return false
}

// unusedError reports a call whose error result is discarded implicitly
// (stmt.error.unused).
func (c *checker) unusedError(call *syntax.CallExpr, x *operand) {
	if x.mode == novalue || x.mode == invalid {
		return
	}
	var results []Type
	if t, ok := x.typ.(*Tuple); ok {
		for _, v := range t.Vars {
			results = append(results, v.typ)
		}
	} else {
		results = []Type{x.typ}
	}
	for _, t := range results {
		if c.isError(t) {
			c.errorf(call.Pos(), "stmt.error.unused", "result of type error of %s is discarded implicitly: assign it to _ to discard it", exprString(call))
			return
		}
	}
}

// isError reports whether t is error or implements it.
func (c *checker) isError(t Type) bool {
	if t == nil || !isValid(t) || isEmptyInterface(t) {
		return false
	}
	return c.missingMethod(t, universeError.underlying.(*Interface)) == ""
}

func (c *checker) callStmt(what string, call *syntax.CallExpr) {
	var x operand
	c.rawExpr(&x, call, nil)
	if x.mode == invalid {
		return
	}
	if c.isConversion(call) || c.isValueBuiltin(call) {
		c.errorf(call.Pos(), "stmt.go", "%s discards result of %s", what, exprString(call))
		return
	}
	if what == "defer" {
		c.unusedError(call, &x)
	}
}

// lhsAddressable checks that the operand x of expression e can be assigned
// to (stmt.assign.go).
func (c *checker) lhsAddressable(e syntax.Expr, x *operand) bool {
	switch x.mode {
	case invalid:
		return false
	case variable, mapindex:
		return true
	}
	if root := mapIndexRoot(c, e); root != nil {
		c.errorf(e.Pos(), "expr.addr.map", "cannot assign to %s: the map element %s is not addressable", exprString(e), exprString(root))
		return false
	}
	switch unparen(e).(type) {
	case *syntax.IndexExpr, *syntax.SelectorExpr:
		// Parts of values are addressable only through addressable
		// operands, also for slices (expr.addr.def).
		c.errorf(e.Pos(), "expr.addr.def", "cannot assign to %s: it is not addressable", exprString(e))
	default:
		c.errorf(e.Pos(), "stmt.assign.go", "cannot assign to %s (neither addressable nor a map index expression)", exprString(e))
	}
	return false
}

// mapIndexRoot returns the map index expression that e selects a part
// of, or nil.
func mapIndexRoot(c *checker, e syntax.Expr) syntax.Expr {
	for {
		switch x := unparen(e).(type) {
		case *syntax.SelectorExpr:
			e = x.X
		case *syntax.IndexExpr:
			if tv, ok := c.info.Types[x.X]; ok {
				if _, isMap := tv.Type.Underlying().(*Map); isMap {
					return x
				}
			}
			e = x.X
		default:
			return nil
		}
	}
}

// lhsVar checks the left operand of an assignment and returns its type,
// or nil for the blank identifier. Assigning to a variable does not use
// it (decl.var.unused).
func (c *checker) lhsVar(e syntax.Expr) (Type, bool) {
	if id, ok := unparen(e).(*syntax.Name); ok {
		if id.Name == "_" {
			c.info.Defs[id] = nil
			return nil, true
		}
		if v, ok := c.lookup(id.Name).(*Var); ok {
			c.info.Uses[id] = v
			c.objDecl(v)
			c.recordType(e, variable, v.typ, nil)
			return v.typ, isValid(v.typ)
		}
	}
	var x operand
	c.expr(&x, e)
	if !c.lhsAddressable(e, &x) {
		return Typ[Invalid], false
	}
	return x.typ, true
}

// assignVar assigns x to the left operand of type t (nil for _).
func (c *checker) assignVar(t Type, ok bool, x *operand) {
	if !ok {
		return
	}
	if t == nil {
		c.assignment(x, nil, "assignment")
		return
	}
	c.assignment(x, t, "assignment")
}

func (c *checker) assignVars(lhs, rhs []syntax.Expr) {
	if len(lhs) != len(rhs) && len(rhs) == 1 {
		types := make([]Type, len(lhs))
		oks := make([]bool, len(lhs))
		for i, e := range lhs {
			types[i], oks[i] = c.lhsVar(e)
		}
		xs := c.multiValue(rhs[0], len(lhs), true)
		for i, x := range xs {
			c.assignVar(types[i], oks[i], x)
		}
		return
	}
	if len(lhs) != len(rhs) {
		c.errorf(lhs[0].Pos(), "stmt.assign.go", "assignment mismatch: %d variables but %d values", len(lhs), len(rhs))
		for _, e := range lhs {
			c.lhsVar(e)
		}
		c.useArgs(rhs)
		return
	}
	for i, e := range lhs {
		t, ok := c.lhsVar(e)
		var x operand
		c.exprHint(&x, rhs[i], t)
		c.assignVar(t, ok, &x)
	}
}

func (c *checker) opAssign(s *syntax.AssignStmt) {
	if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
		c.errorf(s.TokPos, "stmt.assign.go", "assignment operation %s requires single-valued expressions", s.Tok)
		return
	}
	var x operand
	c.binary(&x, &syntax.BinaryExpr{X: s.Lhs[0], OpPos: s.TokPos, Op: assignOps[s.Tok], Y: s.Rhs[0]})
	if x.mode == invalid {
		return
	}
	var lhs operand
	c.expr(&lhs, s.Lhs[0])
	if c.lhsAddressable(s.Lhs[0], &lhs) {
		x.expr = s.Rhs[0]
		c.assignment(&x, lhs.typ, "assignment")
	}
}

func (c *checker) shortVarDecl(s *syntax.AssignStmt) {
	var newVars []*Var
	vars := make([]*Var, len(s.Lhs))
	types := make([]Type, len(s.Lhs))
	oks := make([]bool, len(s.Lhs))
	seen := map[string]bool{}
	for i, e := range s.Lhs {
		id, ok := e.(*syntax.Name)
		if !ok {
			c.errorf(e.Pos(), "stmt.assign.go", "non-name %s on left side of :=", exprString(e))
			c.useArgs([]syntax.Expr{e})
			continue
		}
		if seen[id.Name] && id.Name != "_" {
			c.errorf(id.Pos(), "stmt.assign.go", "%s repeated on left side of :=", id.Name)
			continue
		}
		seen[id.Name] = true
		if alt, ok := c.scope.Lookup(id.Name).(*Var); ok && id.Name != "_" {
			// Redeclaration: an assignment to the existing variable.
			c.info.Uses[id] = alt
			types[i], oks[i] = alt.typ, true
			continue
		}
		v := &Var{object: object{pos: id.Pos(), name: id.Name}}
		vars[i] = v
		oks[i] = true
		if id.Name != "_" {
			newVars = append(newVars, v)
		}
	}

	var xs []*operand
	if len(s.Lhs) != len(s.Rhs) && len(s.Rhs) == 1 {
		xs = c.multiValue(s.Rhs[0], len(s.Lhs), true)
	} else if len(s.Lhs) != len(s.Rhs) {
		c.errorf(s.Pos(), "stmt.assign.go", "assignment mismatch: %d variables but %d values", len(s.Lhs), len(s.Rhs))
		c.useArgs(s.Rhs)
	} else {
		for _, e := range s.Rhs {
			x := &operand{}
			c.expr(x, e)
			xs = append(xs, x)
		}
	}
	for i := range s.Lhs {
		var x *operand
		if i < len(xs) {
			x = xs[i]
		}
		v := vars[i]
		switch {
		case v != nil:
			v.typ = Typ[Invalid]
			if x != nil {
				c.assignment(x, nil, "assignment")
				if x.mode != invalid {
					v.typ = x.typ
				}
			}
		case x != nil && types[i] != nil:
			c.assignVar(types[i], oks[i], x)
		}
	}

	for i, e := range s.Lhs {
		if id, ok := e.(*syntax.Name); ok && vars[i] != nil {
			c.declare(id, vars[i])
			c.fn.vars = append(c.fn.vars, vars[i])
		}
	}
	if len(newVars) == 0 {
		c.errorf(s.TokPos, "stmt.assign.go", "no new variables on left side of :=")
	}
}

func (c *checker) returnStmt(s *syntax.ReturnStmt) {
	results := c.fn.sig.Results
	if len(s.Results) == 0 {
		if results.Len() > 0 && results.Vars[0].name == "" {
			c.errorf(s.Pos(), "stmt.go", "not enough return values\n\thave ()\n\twant %s", results)
		}
		return
	}
	if results.Len() == 0 {
		c.errorf(s.Results[0].Pos(), "stmt.go", "too many return values")
		c.useArgs(s.Results)
		return
	}
	if len(s.Results) == 1 && results.Len() > 1 {
		xs := c.multiValue(s.Results[0], results.Len(), false)
		for i, x := range xs {
			c.assignment(x, results.Vars[i].typ, "return statement")
		}
		return
	}
	if len(s.Results) != results.Len() {
		what := "not enough"
		if len(s.Results) > results.Len() {
			what = "too many"
		}
		c.errorf(s.Results[0].Pos(), "stmt.go", "%s return values\n\twant %s", what, results)
		c.useArgs(s.Results)
		return
	}
	for i, e := range s.Results {
		var x operand
		c.exprHint(&x, e, results.Vars[i].typ)
		c.assignment(&x, results.Vars[i].typ, "return statement")
	}
}

func (c *checker) branchStmt(ctxt stmtContext, s *syntax.BranchStmt) {
	if s.Label != nil {
		l := c.fn.labels[s.Label.Name]
		if l == nil {
			c.errorf(s.Label.Pos(), "stmt.go", "label %s not defined", s.Label.Name)
			return
		}
		c.info.Uses[s.Label] = l
		if s.Tok == syntax.Goto {
			l.used = true
			return // checked by collectLabels
		}
		for i := len(c.fn.enclosing) - 1; i >= 0; i-- {
			ls := c.fn.enclosing[i]
			if ls.Label.Name != s.Label.Name {
				continue
			}
			switch ls.Stmt.(type) {
			case *syntax.ForStmt, *syntax.RangeStmt:
				l.used = true
				return
			case *syntax.SwitchStmt, *syntax.TypeSwitchStmt, *syntax.SelectStmt:
				if s.Tok == syntax.Break {
					l.used = true
					return
				}
			}
			break
		}
		c.errorf(s.Label.Pos(), "stmt.go", "invalid %s label %s", s.Tok, s.Label.Name)
		return
	}
	switch s.Tok {
	case syntax.Break:
		if ctxt&breakOK == 0 {
			c.errorf(s.Pos(), "stmt.go", "break is not in a loop, switch, or select")
		}
	case syntax.Continue:
		if ctxt&continueOK == 0 {
			c.errorf(s.Pos(), "stmt.go", "continue is not in a loop")
		}
	case syntax.Fallthrough:
		if ctxt&fallthroughOK == 0 {
			c.errorf(s.Pos(), "stmt.go", "fallthrough statement out of place")
		}
	case syntax.Goto:
		c.errorf(s.Pos(), "stmt.go", "goto without label")
	}
}

func (c *checker) switchStmt(ctxt stmtContext, s *syntax.SwitchStmt) {
	c.openScope()
	defer c.closeScope()
	if s.Init != nil {
		c.stmt(0, s.Init)
	}
	var tag operand
	if s.Tag != nil {
		c.expr(&tag, s.Tag)
		c.defaultUntyped(&tag)
		if tag.mode != invalid && !comparable(tag.typ) && !hasNil(tag.typ) {
			c.errorf(s.Tag.Pos(), "stmt.go", "cannot switch on %s", &tag)
			tag.invalidate()
		}
	} else {
		tag = operand{mode: constant_, typ: Typ[Bool], val: constBool(true)}
	}

	seen := map[string]syntax.Pos{}
	var dflt *syntax.CaseClause
	clauses := s.Body.List
	for i, cs := range clauses {
		cl, ok := cs.(*syntax.CaseClause)
		if !ok {
			continue
		}
		if cl.List == nil {
			if dflt != nil {
				c.errorf(cl.Pos(), "stmt.go", "multiple defaults in switch (first at %s)", dflt.Pos())
			}
			dflt = cl
		}
		for _, e := range cl.List {
			c.caseValue(&tag, e, s.Tag != nil, seen)
		}
		c.openScope()
		inner := ctxt | breakOK
		if i < len(clauses)-1 {
			inner |= fallthroughOK
		} else {
			inner &^= fallthroughOK
		}
		c.stmtList(inner, cl.Body)
		c.closeScope()
	}
}

// caseValue checks a case expression against the switch tag.
func (c *checker) caseValue(tag *operand, e syntax.Expr, hasTag bool, seen map[string]syntax.Pos) {
	var x operand
	c.expr(&x, e)
	if x.mode == invalid || tag.mode == invalid {
		return
	}
	if !hasTag {
		if !isBoolean(x.typ) {
			c.errorf(e.Pos(), "stmt.go", "invalid case %s in switch (mismatched types %s and bool)", exprString(e), x.typ)
			return
		}
		c.defaultUntyped(&x)
		return
	}
	t := *tag
	t.expr = e
	xNil := x.isNil()
	c.matchTypes(&x, &t)
	if x.mode == invalid {
		return
	}
	if !c.comparableTypes(x.typ, tag.typ) && !(xNil && hasNil(tag.typ)) {
		c.errorf(e.Pos(), "stmt.go", "invalid case %s in switch on %s (mismatched types %s and %s)", exprString(e), exprString(tag.expr), x.typ, tag.typ)
		return
	}
	if !xNil {
		for _, z := range []*operand{tag, &x} {
			if !comparable(z.typ) {
				rule := "stmt.go"
				if _, ok := z.typ.Underlying().(*Chan); ok {
					rule = "expr.cmp.sync"
				}
				c.errorf(e.Pos(), rule, "invalid case %s in switch on %s (%s can only be compared to nil)", exprString(e), exprString(tag.expr), z.typ)
				return
			}
		}
	}
	if x.mode == constant_ {
		key := x.val.ExactString() + " " + x.typ.String()
		if prev, ok := seen[key]; ok {
			c.errorf(e.Pos(), "stmt.go", "duplicate case %s in expression switch (previous case at %s)", exprString(e), prev)
			return
		}
		seen[key] = e.Pos()
	}
}

func (c *checker) selectStmt(ctxt stmtContext, s *syntax.SelectStmt) {
	var dflt *syntax.CommClause
	for _, cs := range s.Body.List {
		cl, ok := cs.(*syntax.CommClause)
		if !ok {
			continue
		}
		c.openScope()
		switch comm := cl.Comm.(type) {
		case nil:
			if dflt != nil {
				c.errorf(cl.Pos(), "stmt.select.go", "multiple defaults in select (first at %s)", dflt.Pos())
			}
			dflt = cl
		case *syntax.SendStmt:
			c.stmt(0, comm)
		case *syntax.ExprStmt:
			if !isReceive(comm.X) {
				c.errorf(comm.Pos(), "stmt.select.go", "select case must be receive, send or assign recv")
			}
			c.stmt(0, comm)
		case *syntax.AssignStmt:
			if len(comm.Rhs) != 1 || !isReceive(comm.Rhs[0]) {
				c.errorf(comm.Pos(), "stmt.select.go", "select case must be receive, send or assign recv")
			}
			c.stmt(0, comm)
		default:
			c.errorf(comm.Pos(), "stmt.select.go", "select case must be receive, send or assign recv")
		}
		c.stmtList((ctxt|breakOK)&^fallthroughOK, cl.Body)
		c.closeScope()
	}
}

func isReceive(e syntax.Expr) bool {
	u, ok := unparen(e).(*syntax.UnaryExpr)
	return ok && u.Op == syntax.Arrow
}

func (c *checker) rangeStmt(s *syntax.RangeStmt) {
	if s.Const {
		c.notSupported(s.Pos(), 10, "for const")
		return
	}
	c.openScope()
	defer c.closeScope()

	var x operand
	c.expr(&x, s.X)
	var key, val Type
	maxVars := 2
	if x.mode != invalid {
		switch t := x.typ.Underlying().(type) {
		case *Basic:
			switch {
			case t.info&IsString != 0:
				key, val = Typ[Int], Typ[I32]
				c.defaultUntyped(&x)
			case t.info&IsInteger != 0:
				if isUntyped(t) {
					c.convertUntyped(&x, Typ[Int], "stmt.range.go", "range")
				}
				key, maxVars = x.typ, 1
			}
		case *Array:
			key, val = Typ[Int], t.Elem
		case *Slice:
			key, val = Typ[Int], t.Elem
		case *Map:
			key, val = t.Key, t.Elem
		case *Chan:
			if t.Dir == SendOnly {
				c.errorf(s.X.Pos(), "stmt.range.go", "range over send-only channel %s", &x)
				x.invalidate()
			}
			key, maxVars = t.Elem, 1
		case *Signature:
			key, val, maxVars = c.rangeFunc(s.X, t)
		}
		if key == nil && x.mode != invalid {
			c.errorf(s.X.Pos(), "stmt.range.go", "cannot range over %s", &x)
			x.invalidate()
		}
	}

	lhs := []syntax.Expr{s.Key, s.Value}
	types := []Type{key, val}
	for i, e := range lhs {
		if e != nil && i >= maxVars {
			c.errorf(e.Pos(), "stmt.range.go", "range over %s permits only %d iteration variable(s)", &x, maxVars)
			types[i] = nil // still declared, with an invalid type
		}
	}
	switch s.Tok {
	case syntax.Define:
		var vars []*Var
		var names []*syntax.Name
		for i, e := range lhs {
			if e == nil {
				continue
			}
			id, ok := e.(*syntax.Name)
			if !ok {
				c.errorf(e.Pos(), "stmt.range.go", "non-name %s on left side of :=", exprString(e))
				continue
			}
			v := &Var{object: object{pos: id.Pos(), name: id.Name, typ: Typ[Invalid]}}
			if x.mode != invalid && types[i] != nil {
				v.typ = types[i]
			}
			vars = append(vars, v)
			names = append(names, id)
		}
		for i, id := range names {
			c.declare(id, vars[i])
			c.fn.vars = append(c.fn.vars, vars[i])
		}
	case syntax.Assign:
		for i, e := range lhs {
			if e == nil {
				continue
			}
			t, ok := c.lhsVar(e)
			if ok && t != nil && x.mode != invalid && types[i] != nil && !c.assignableTo(types[i], t) {
				c.errorf(e.Pos(), "stmt.range.go", "cannot assign %s to %s (type %s) in range", types[i], exprString(e), t)
			}
		}
	}
	c.stmt(breakOK|continueOK, s.Body)
}

// rangeFunc returns the iteration types of an iterator function
// (stmt.range.go): func(yield func(K, V) bool) with 0 to 2 values.
func (c *checker) rangeFunc(e syntax.Expr, t *Signature) (key, val Type, n int) {
	if t.Params.Len() == 1 && t.Results.Len() == 0 {
		if y, ok := t.Params.Vars[0].typ.Underlying().(*Signature); ok && y.Results.Len() == 1 && isBoolean(y.Results.Vars[0].typ) && y.Params.Len() <= 2 {
			for _, p := range y.Params.Vars {
				if p.inout {
					c.notSupported(e.Pos(), 4, "iterators that yield inout access")
					return Typ[Invalid], Typ[Invalid], 2
				}
			}
			switch y.Params.Len() {
			case 0:
				return Typ[Invalid], nil, 0
			case 1:
				return y.Params.Vars[0].typ, nil, 1
			default:
				return y.Params.Vars[0].typ, y.Params.Vars[1].typ, 2
			}
		}
	}
	return nil, nil, 0
}

func (c *checker) typeSwitchStmt(ctxt stmtContext, s *syntax.TypeSwitchStmt) {
	c.openScope()
	defer c.closeScope()
	if s.Init != nil {
		c.stmt(0, s.Init)
	}
	var lhs *syntax.Name
	var guard *syntax.TypeAssertExpr
	switch a := s.Assign.(type) {
	case *syntax.ExprStmt:
		guard, _ = a.X.(*syntax.TypeAssertExpr)
	case *syntax.AssignStmt:
		if len(a.Lhs) == 1 && len(a.Rhs) == 1 && a.Tok == syntax.Define {
			lhs, _ = a.Lhs[0].(*syntax.Name)
			guard, _ = a.Rhs[0].(*syntax.TypeAssertExpr)
		}
	}
	if guard == nil || guard.Type != nil {
		c.errorf(s.Assign.Pos(), "stmt.go", "invalid type switch guard")
		return
	}
	if lhs != nil && lhs.Name == "_" {
		c.errorf(lhs.Pos(), "stmt.go", "no new variable on left side of :=")
		lhs = nil
	}

	var x operand
	c.expr(&x, guard.X)
	var xi *Interface
	if x.mode != invalid {
		var ok bool
		if xi, ok = x.typ.Underlying().(*Interface); !ok {
			c.errorf(guard.X.Pos(), "expr.go", "%s is not an interface", &x)
			x.invalidate()
		} else {
			c.recordType(guard, value, x.typ, nil)
		}
	}

	var seen []Type
	var seenNil, dflt syntax.Node
	used := false
	for _, cs := range s.Body.List {
		cl, ok := cs.(*syntax.CaseClause)
		if !ok {
			continue
		}
		if cl.List == nil {
			if dflt != nil {
				c.errorf(cl.Pos(), "stmt.go", "multiple defaults in switch (first at %s)", dflt.Pos())
			}
			dflt = cl
		}
		var single Type // the case type, if the clause has exactly one non-nil type
		for _, e := range cl.List {
			if id, ok := unparen(e).(*syntax.Name); ok {
				if n, ok := c.lookup(id.Name).(*Nil); ok {
					c.info.Uses[id] = n
					if seenNil != nil {
						c.errorf(e.Pos(), "stmt.go", "multiple nil cases in type switch (first at %s)", seenNil.Pos())
					}
					seenNil = e
					continue
				}
			}
			t := c.typExpr(e)
			if !isValid(t) || x.mode == invalid {
				continue
			}
			if slices.ContainsFunc(seen, func(s Type) bool { return Identical(s, t) }) {
				c.errorf(e.Pos(), "stmt.go", "duplicate case %s in type switch", t)
				continue
			}
			seen = append(seen, t)
			if c.possibleType(e, &x, xi, t, "type switch case") && len(cl.List) == 1 {
				single = t
			}
		}
		c.openScope()
		if lhs != nil {
			// The variable has the case type in a clause with one type,
			// and the type of the guard otherwise.
			typ := x.typ
			if single != nil {
				typ = single
			}
			if x.mode == invalid {
				typ = Typ[Invalid]
			}
			v := &Var{object: object{pos: lhs.Pos(), name: lhs.Name, typ: typ}}
			c.scope.Insert(v)
			c.stmtList((ctxt|breakOK)&^fallthroughOK, cl.Body)
			used = used || v.used
		} else {
			c.stmtList((ctxt|breakOK)&^fallthroughOK, cl.Body)
		}
		c.closeScope()
	}
	if lhs != nil {
		c.info.Defs[lhs] = nil
		if !used {
			c.errorf(lhs.Pos(), "decl.var.unused", "declared and not used: %s", lhs.Name)
		}
	}
}
