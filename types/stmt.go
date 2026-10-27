package types

import (
	"go/constant"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"an/syntax"
)

// funcContext is the state of the function body being checked.
type funcContext struct {
	parent    *funcContext // the enclosing function of a function literal
	captures  bool         // a function literal captures an inout variable (expr.closure.capture-inout)
	captured  []*Var       // the inout variables of enclosing functions that it captures
	predicate bool         // a function literal in a contract expression: the predicate of a quantifier
	name      string       // the declared function or method, for diagnostics
	sig       *Signature
	generic   *Signature // the enclosing generic function, if any
	vars      []*Var     // local variables (decl.var.unused)
	labels    map[string]*Label
	// enclosing lists the labeled statements around the current
	// statement, innermost last.
	enclosing []*syntax.LabeledStmt
}

// isEffectWithout reports whether e is a call of effect.Without.
func (c *checker) isEffectWithout(e syntax.Expr) bool {
	call, ok := unparen(e).(*syntax.CallExpr)
	if !ok {
		return false
	}
	sel, ok := unparen(call.Fun).(*syntax.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := unparen(sel.X).(*syntax.Name)
	if !ok {
		return false
	}
	pkg, ok := c.lookup(id.Name).(*PkgName)
	return ok && pkg.Imported.Path == "an/effect" && sel.Sel.Name == "Without"
}

// stmtContext holds the statements permitted at a point of a body.
type stmtContext int

const (
	breakOK stmtContext = 1 << iota
	continueOK
	fallthroughOK
	variantSwitchOK // directly in a case clause of a variant switch
)

// funcBody checks the body of a function declared in file scope file.
// funcBody checks the body of a function, a method or a function
// literal. parent is the enclosing scope: the file scope, or for a
// function literal the scope in which it appears (expr.closure.capture-copy).
func (c *checker) funcBody(parent *Scope, sig *Signature, recv *syntax.FieldList, ft *syntax.FuncType, body *syntax.BlockStmt, literal bool) (captures bool) {
	savedScope, savedFn := c.scope, c.fn
	defer func() { c.scope, c.fn = savedScope, savedFn }()
	nerrors := len(c.errors)
	c.fn = &funcContext{sig: sig, labels: map[string]*Label{}, name: c.funcName}
	defer func() { captures, c.litCaptured = c.fn.captures, c.fn.captured }()
	savedHeld := c.held
	if literal {
		c.fn.parent = savedFn
		c.fn.predicate = c.inContract > 0
	} else {
		c.held = nil
	}
	defer func() { c.held = savedHeld }()
	if len(sig.TypeParams) > 0 {
		c.fn.generic = sig
	} else if literal && savedFn != nil {
		c.fn.generic, c.fn.name = savedFn.generic, savedFn.name
	}
	c.scope = NewScope(parent)
	c.scope.isFunc = true

	if sig.Recv != nil {
		c.declareParams(recv, &Tuple{Vars: []*Var{sig.Recv}})
	}
	c.declareParams(ft.Params, sig.Params)
	c.declareParams(ft.Results, sig.Results)
	if ft.Results != nil {
		// Named results start with the zero value (type.zero.uses).
		for _, v := range sig.Results.Vars {
			if v.name != "" {
				c.zeroUse(v.pos, v.typ, "type.zero.named-result", "named result "+v.name)
			}
		}
	}
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
	if !literal {
		// Untyped expressions around a function literal get their types
		// when the enclosing expression is complete.
		c.flushUntyped()
		if len(c.errors) == nerrors {
			// The literals in the body are checked with it.
			c.checkNil(body, nil, sig.Params.Vars)
		}
	}
	return false // set by the deferred function
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
			v.owner = c.fn
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

	case *syntax.AsmStmt:
		c.asmStmt(s)

	case *syntax.SendStmt:
		var ch, v operand
		c.expr(&ch, s.Chan)
		c.expr(&v, s.Value)
		if ch.mode == invalid || v.mode == invalid {
			return
		}
		t, ok := core(ch.typ).(*Chan)
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
		if tv := c.info.Types[s.Cond]; (c.instance != nil || c.unrolling) && tv.Value != nil && tv.Value.Kind() == constant.Bool {
			// In an instance, only the branch that a constant condition
			// takes is checked (ct.reject.call).
			if constant.BoolVal(tv.Value) {
				c.stmt(ctxt&^fallthroughOK, s.Body)
			} else if s.Else != nil {
				c.stmt(ctxt&^fallthroughOK, s.Else)
			}
			c.closeScope()
			break
		}
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
		if s.Word == "ensures" && c.isEffectWithout(s.X) {
			// ensures effect.Without(...) is checked by effect inference
			// (eff.without.form, eff.without.check).
			c.inEnsures = true
			var x operand
			c.expr(&x, s.X)
			c.inEnsures = false
			if c.fn.parent != nil {
				c.errorf(s.Pos(), "eff.without.form", "ensures effect.Without is not permitted in a function literal")
			}
			return
		}
		c.contract(s)

	default:
		c.errorf(s.Pos(), "stmt.go", "invalid statement")
	}
}

// contract checks a requires or ensures clause: a boolean expression; in
// an ensures clause of a function with one unnamed result, result denotes
// it (contract.syntax.bool, contract.expr.result).
func (c *checker) contract(s *syntax.ContractStmt) {
	if c.fn == nil {
		return
	}
	saved := c.scope
	c.scope = NewScope(saved)
	defer func() { c.scope = saved }()
	if s.Word == "ensures" {
		if r := c.fn.sig.Results; r.Len() == 1 && r.Vars[0].name == "" {
			v := &Var{object: object{name: "result", typ: r.Vars[0].typ, pos: s.Pos()}, owner: c.fn}
			v.used = true
			c.scope.Insert(v)
			c.info.ContractResults[s] = v
		}
	}
	c.oldOK = s.Word != "requires"
	c.contractExpr(s.X, s.Word)
	c.oldOK = false
}

// contractExpr checks the boolean expression of a contract clause.
func (c *checker) contractExpr(e syntax.Expr, word string) {
	var x operand
	c.inContract++
	c.expr(&x, e)
	c.inContract--
	if x.mode == invalid {
		return
	}
	c.defaultUntyped(&x)
	if !isBoolean(x.typ) {
		c.errorf(e.Pos(), "contract.syntax.bool", "the expression of a %s clause must be a boolean, not %s", word, &x)
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
			c.dropPendingZero(s.X) // the received value is discarded
			return
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
	if what == "go" {
		c.goCall(call)
	}
	if what == "defer" {
		c.unusedError(call, &x)
		c.deferCall(call)
	}
}

// holdCaptures records the captures of the closure e that v holds.
func (c *checker) holdCaptures(v *Var, e syntax.Expr) {
	if c.varCaptures == nil {
		c.varCaptures = map[*Var][]*Var{}
	}
	c.varCaptures[v] = c.capturedBy(e)
}

// capturedBy returns the inout variables that the second-class closure e
// captures.
func (c *checker) capturedBy(e syntax.Expr) []*Var {
	switch x := unparen(e).(type) {
	case *syntax.FuncLit:
		return c.litCaptures[x]
	case *syntax.Name:
		if v, ok := c.info.Uses[x].(*Var); ok {
			return c.varCaptures[v]
		}
	}
	return nil
}

// deferCall checks that a deferred call holds no access to an inout loop
// variable, whose access ends with the iteration, before the deferred
// call runs (stmt.defer.inout-loop).
func (c *checker) deferCall(call *syntax.CallExpr) {
	loopVar := func(v *Var) bool { return v != nil && v.inout && !v.param }
	for _, v := range c.capturedBy(call.Fun) {
		if loopVar(v) {
			c.errorf(call.Pos(), "stmt.defer.inout-loop", "cannot defer a call of a closure that captures the inout loop variable %s: the deferred call runs after the iteration ends", v.name)
			return
		}
	}
	tv, ok := c.info.Types[call.Fun]
	if !ok {
		return
	}
	sig, ok := core(tv.Type).(*Signature)
	if !ok {
		return
	}
	for _, e := range c.inoutAccesses(call, sig) {
		if p, ok := c.pathOf(e); ok && loopVar(p.root) {
			c.errorf(e.Pos(), "stmt.defer.inout-loop", "cannot pass %s to an inout parameter of a deferred call: the access to the inout loop variable %s ends with the iteration", exprString(e), p.root.name)
		}
	}
}

// computedRoot returns the computed constant that e is a part of, or
// nil.
func (c *checker) computedRoot(e syntax.Expr) *Const {
	for {
		switch x := unparen(e).(type) {
		case *syntax.IndexExpr:
			e = x.X
		case *syntax.SelectorExpr:
			if _, ok := c.info.Selections[x]; !ok {
				if k, ok := c.info.Uses[x.Sel].(*Const); ok && k.Computed {
					return k
				}
				return nil
			}
			e = x.X
		case *syntax.Name:
			if k, ok := c.info.Uses[x].(*Const); ok && k.Computed {
				return k
			}
			return nil
		default:
			return nil
		}
	}
}

// lhsAddressable checks that the operand x of expression e can be assigned
// to (stmt.assign.go).
func (c *checker) lhsAddressable(e syntax.Expr, x *operand) bool {
	switch x.mode {
	case invalid:
		return false
	case variable, mapindex:
		return !c.capturedWrite(e)
	}
	if k := c.computedRoot(e); k != nil {
		c.errorf(e.Pos(), "ct.const.immutable", "cannot assign to %s: the constant %s cannot be modified", exprString(e), k.name)
		return false
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

// capturedWrite reports a modification of a variable captured by a
// function literal, or of a part of it (expr.closure.captured-readonly).
func (c *checker) capturedWrite(e syntax.Expr) bool {
	root := e
	for {
		switch x := unparen(root).(type) {
		case *syntax.SelectorExpr:
			root = x.X
			continue
		case *syntax.IndexExpr:
			root = x.X
			continue
		case *syntax.SliceExpr:
			root = x.X
			continue
		}
		break
	}
	id, ok := unparen(root).(*syntax.Name)
	if !ok {
		return false
	}
	v, ok := c.info.Uses[id].(*Var)
	if ok && v.inout && v.owner != nil && v.owner != c.fn {
		c.useInoutVar(v) // a write captures it too
	}
	if !ok || v.owner == nil || v.owner == c.fn || v.inout {
		return false // inout variables are captured by reference (expr.closure.capture-inout)
	}
	c.errorf(e.Pos(), "expr.closure.captured-readonly", "cannot modify %s: %s is captured by the function literal, which holds a read-only copy", exprString(e), id.Name)
	return true
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
			if v.readonly {
				c.errorf(e.Pos(), "ct.for.binding", "cannot assign to %s: the iteration variable of for const over a tuple is immutable", id.Name)
				return Typ[Invalid], false
			}
			c.objDecl(v)
			if v.inout {
				v.used = true // the assignment writes through the inout access
			}
			c.recordType(e, variable, v.typ, nil)
			c.checkHeld(e)
			if c.capturedWrite(e) {
				return Typ[Invalid], false
			}
			return v.typ, isValid(v.typ)
		}
	}
	var x operand
	c.expr(&x, e)
	if x.mode == mapindex {
		c.dropPendingZero(e) // m[k] = v does not read m[k]
	}
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
				// A new variable may hold a second-class closure.
				c.secondOK = true
				second := x.second
				c.assignment(x, nil, "assignment")
				c.secondOK = false
				if x.mode != invalid {
					v.typ, v.second = x.typ, second
					if second {
						c.holdCaptures(v, x.expr)
					}
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
		// A borrowing function returns an iterator that captures its
		// inout parameters (mem.ref.borrowing).
		saved := c.secondOK
		if x.second && c.fn.parent == nil && results.Len() == 1 && isIterator(results.Vars[i].typ) && c.ownInout(e) {
			c.secondOK = true
		}
		c.assignment(&x, results.Vars[i].typ, "return statement")
		c.secondOK = saved
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
		if ctxt&variantSwitchOK != 0 {
			c.errorf(s.Pos(), "stmt.switch.variant.no-fallthrough", "fallthrough is not permitted in a variant switch")
		} else if ctxt&fallthroughOK == 0 {
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
	defer func() {
		if tag.mode == invalid {
			c.invalidSwitch[s] = true
		}
	}()
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
	if tag.mode != invalid && isGenericEnumValue(tag.typ) {
		c.genericEnumError(&tag)
	}
	if tag.mode != invalid && s.Tag != nil {
		if enum := enumOf(tag.typ); enum != nil {
			c.variantSwitch(ctxt, s, &tag, enum)
			return
		}
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
			if tag.mode == invalid {
				// The case expressions may be patterns of an enum type
				// that could not be checked: they are checked only to
				// mark the variables they use, without diagnostics.
				n := len(c.errors)
				c.useArgs([]syntax.Expr{e})
				c.errors = c.errors[:n]
				continue
			}
			c.caseValue(&tag, e, s.Tag != nil, seen)
		}
		c.openScope()
		if tag.mode == invalid {
			// Bindings of what may be variant patterns are declared
			// invalid, so their uses report no further errors.
			for _, e := range cl.List {
				if _, elts, braces := patternParts(e); braces {
					for _, b := range elts {
						if id, ok := b.(*syntax.Name); ok && id.Name != "_" && c.scope.Lookup(id.Name) == nil {
							c.declare(id, &Var{object: object{pos: id.Pos(), name: id.Name, typ: Typ[Invalid]}, used: true})
						}
					}
				}
			}
		}
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
		c.forConst(s)
		return
	}
	c.openScope()
	defer c.closeScope()

	var x operand
	// The range statement calls an iterator directly: a method value with
	// an inout receiver may be the range expression.
	savedCallee := c.callee
	c.callee = unparen(s.X)
	c.expr(&x, s.X)
	c.callee = savedCallee
	var key, val Type
	var inout [2]bool
	maxVars := 2
	if x.mode != invalid {
		switch t := core(x.typ).(type) {
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
			key, val, maxVars, inout = c.rangeFunc(s.X, t)
		}
		if key == nil && x.mode != invalid {
			c.errorf(s.X.Pos(), "stmt.range.go", "cannot range over %s", &x)
			x.invalidate()
		}
	}

	lhs := []syntax.Expr{s.Key, s.Value}
	types := []Type{key, val}
	if inout != [2]bool{} {
		// The loop holds the operand of the iterator while the body runs
		// (stmt.range.inout-exclusive).
		saved := c.held
		c.held = append(append([]heldPath(nil), c.held...), c.heldPaths(s.X)...)
		defer func() { c.held = saved }()
	}
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
			v := &Var{object: object{pos: id.Pos(), name: id.Name, typ: Typ[Invalid]}, inout: inout[i]}
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
		if id, ok := s.Key.(*syntax.Name); ok && len(vars) > 0 && vars[0].name == id.Name && x.mode != invalid {
			if n, ok := c.rangeLength(s.X, &x); ok && !c.assigns(s.Body, id.Name) {
				c.rangeKeys[vars[0]] = n // type.conv.int-narrow-facts 6
			}
		}
	case syntax.Assign:
		for i, e := range lhs {
			if e == nil {
				continue
			}
			if inout[i] {
				c.errorf(e.Pos(), "stmt.range.inout", "the inout loop variable %s must be declared with :=", exprString(e))
				if id, ok := e.(*syntax.Name); ok {
					if v, ok := c.lookup(id.Name).(*Var); ok {
						v.used = true // no further error
					}
				}
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

// rangeLength returns the number of iterations of a range over x when it
// is known: an integer constant, an array, a composite literal or a
// conversion of a constant string (type.conv.int-narrow-facts 6).
func (c *checker) rangeLength(e syntax.Expr, x *operand) (int64, bool) {
	if x.mode == constant_ && isInteger(x.typ) {
		return int64Val(x.val)
	}
	if a, ok := core(x.typ).(*Array); ok {
		return a.Len, true
	}
	switch e := unparen(e).(type) {
	case *syntax.CompositeLit:
		for _, el := range e.Elts {
			if _, ok := el.(*syntax.KeyValueExpr); ok {
				return 0, false
			}
		}
		return int64(len(e.Elts)), true
	case *syntax.CallExpr:
		if len(e.Args) == 1 && c.isConversion(e) {
			if tv := c.info.Types[e.Args[0]]; tv.Value != nil && tv.Value.Kind() == constant.String {
				str := constant.StringVal(tv.Value)
				if s, ok := core(x.typ).(*Slice); ok && isByteOrRune(s.Elem) && s.Elem.Underlying().(*Basic).kind == I32 {
					return int64(utf8.RuneCountInString(str)), true
				}
				return int64(len(str)), true
			}
		}
	}
	if x.mode == constant_ && x.val.Kind() == constant.String {
		return int64(len(constant.StringVal(x.val))), true
	}
	return 0, false
}

// assigns reports whether the statements of body may modify the variable
// named name: they assign it, increment or decrement it, or pass it as
// an argument, which may be an inout argument. Conversions and the
// built-in functions that do not modify their arguments are not calls
// for this purpose.
func (c *checker) assigns(body *syntax.BlockStmt, name string) bool {
	found := false
	is := func(e syntax.Expr) bool {
		id, ok := unparen(e).(*syntax.Name)
		return ok && id.Name == name
	}
	syntax.Walk(body, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.AssignStmt:
			if n.Tok != syntax.Define {
				for _, e := range n.Lhs {
					found = found || is(e)
				}
			}
		case *syntax.IncDecStmt:
			found = found || is(n.X)
		case *syntax.CallExpr:
			if c.readsArgs(n.Fun) {
				break
			}
			for _, e := range n.Args {
				found = found || is(e)
			}
		case *syntax.RangeStmt:
			if n.Tok == syntax.Assign {
				found = found || n.Key != nil && is(n.Key) || n.Value != nil && is(n.Value)
			}
		}
		return !found
	})
	return found
}

// readsArgs reports whether fun, the callee of a call in a body not yet
// checked, denotes a type or a built-in function that does not modify
// its arguments.
func (c *checker) readsArgs(fun syntax.Expr) bool {
	switch f := unparen(fun).(type) {
	case *syntax.Name:
		switch obj := c.lookup(f.Name).(type) {
		case *TypeName:
			return true
		case *Builtin:
			return obj.id != _Copy && obj.id != _Delete && obj.id != _Clear
		}
	case *syntax.ArrayType, *syntax.MapType, *syntax.FuncType, *syntax.ChanType:
		return true
	}
	return false
}

// rangeFunc returns the iteration types of an iterator function
// (stmt.range.go): func(yield func(K, V) bool) with 0 to 2 values, and
// which of them the iterator yields with inout access: only the last
// (stmt.range.inout-iter).
func (c *checker) rangeFunc(e syntax.Expr, t *Signature) (key, val Type, n int, inout [2]bool) {
	if t.Params.Len() == 1 && t.Results.Len() == 0 {
		if y, ok := t.Params.Vars[0].typ.Underlying().(*Signature); ok && y.Results.Len() == 1 && Identical(y.Results.Vars[0].typ, Typ[Bool]) && y.Params.Len() <= 2 {
			ps := y.Params.Vars
			if len(ps) == 2 && ps[0].inout {
				c.errorf(e.Pos(), "stmt.range.inout-iter", "an iterator may yield inout access only to its last value")
			}
			switch len(ps) {
			case 0:
				return Typ[Invalid], nil, 0, inout
			case 1:
				inout[0] = ps[0].inout
				return ps[0].typ, nil, 1, inout
			default:
				inout[1] = ps[1].inout
				return ps[0].typ, ps[1].typ, 2, inout
			}
		}
	}
	return nil, nil, 0, inout
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
	if x.mode != invalid && c.instance != nil && !isInterfaceType(x.typ) {
		c.staticTypeSwitch(ctxt, s, lhs, &x)
		return
	}
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
			v := &Var{object: object{pos: lhs.Pos(), name: lhs.Name, typ: typ}, owner: c.fn}
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

// goCall checks the call of a go statement for second-class references
// (mem.ref.second-class) and inout accesses.
func (c *checker) goCall(call *syntax.CallExpr) {
	switch f := unparen(call.Fun).(type) {
	case *syntax.Name:
		if v, ok := c.info.Uses[f].(*Var); ok && v.second {
			c.errorf(f.Pos(), "expr.closure.capture-inout", "cannot start %s in a go statement: it captures an inout variable", f.Name)
			return
		}
	case *syntax.FuncLit:
		if c.secondLits[f] {
			c.errorf(f.Pos(), "expr.closure.capture-inout", "cannot start a function literal that captures an inout variable in a go statement")
			return
		}
	}
	if tv, ok := c.info.Types[call.Fun]; ok {
		if sig, ok := core(tv.Type).(*Signature); ok && len(c.inoutAccesses(call, sig)) > 0 {
			c.notSupported(call.Pos(), 32, "inout arguments in go statements")
		}
	}
}

// asmStmt checks an asm statement (stmt.asm.restrictions,
// stmt.asm.syntax): the input registers are a struct value, and the
// output registers are assigned to a variable of a struct type.
func (c *checker) asmStmt(s *syntax.AsmStmt) {
	core := len(c.pkg.Profiles) > 0
	for _, p := range c.pkg.Profiles {
		core = core && p == "core"
	}
	if !core {
		c.errorf(s.Pos(), "stmt.asm.restrictions", "an asm statement is allowed only in a package of the core profile")
	}
	file := strings.TrimSuffix(pathBase(s.Pos().File), ".an")
	if c.conf.Arch == "" || !strings.HasSuffix(file, "_"+c.conf.Arch) {
		c.errorf(s.Pos(), "stmt.asm.restrictions", "an asm statement is allowed only in a file with the suffix of its architecture, such as _%s", c.conf.Arch)
	}
	if r := s.Regs(); r != nil {
		var x operand
		c.expr(&x, r)
		if x.mode != invalid {
			if _, ok := x.typ.Underlying().(*Struct); !ok {
				c.errorf(r.Pos(), "stmt.asm.syntax", "the input registers of asm must be a struct value, not %s", &x)
			}
		}
	}
	switch len(s.Lhs) {
	case 0:
	case 1:
		var x operand
		c.expr(&x, s.Lhs[0])
		if x.mode == invalid {
			return
		}
		if x.mode != variable {
			c.errorf(s.Lhs[0].Pos(), "stmt.asm.syntax", "cannot assign the output registers of asm to %s", &x)
			return
		}
		if _, ok := x.typ.Underlying().(*Struct); !ok {
			c.errorf(s.Lhs[0].Pos(), "stmt.asm.syntax", "the output registers of asm must be assigned to a struct, not %s", &x)
		}
	default:
		c.errorf(s.Lhs[1].Pos(), "stmt.asm.syntax", "asm assigns its output registers to one struct")
	}
}

// ownInout reports whether e is a function literal that captures only
// inout parameters and the inout receiver of the function being checked.
func (c *checker) ownInout(e syntax.Expr) bool {
	if _, ok := unparen(e).(*syntax.FuncLit); !ok {
		return false
	}
	own := map[*Var]bool{}
	if sig := c.fn.sig; sig != nil {
		if sig.Recv != nil {
			own[sig.Recv] = true
		}
		for _, p := range sig.Params.Vars {
			own[p] = true
		}
	}
	for _, v := range c.litCaptured {
		if !own[v] {
			return false
		}
	}
	return true
}
