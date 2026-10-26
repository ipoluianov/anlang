package lower

import (
	"go/constant"
	"strconv"

	"an/ir"
	"an/syntax"
	"an/types"
)

func (fb *funcBuilder) stmts(list []syntax.Stmt) {
	for _, s := range list {
		fb.stmt(s)
	}
}

func (fb *funcBuilder) stmt(s syntax.Stmt) {
	if fb.cur.Terminated() {
		// Unreachable code after return, panic, break or goto.
		fb.cur = fb.newBlock("unreachable")
	}
	switch s := s.(type) {
	case *syntax.EmptyStmt:
	case *syntax.ExprStmt:
		if call, ok := unparen(s.X).(*syntax.CallExpr); ok {
			fb.call(call, true)
			return
		}
		fb.expr(s.X)
	case *syntax.DeclStmt:
		fb.declStmt(s.Decl)
	case *syntax.AssignStmt:
		fb.assign(s)
	case *syntax.AsmStmt:
		fb.asm(s)
	case *syntax.IncDecStmt:
		op := syntax.Add
		if s.Tok == syntax.Dec {
			op = syntax.Sub
		}
		fb.opAssign(s.X, op, nil)
	case *syntax.BlockStmt:
		fb.stmts(s.List)
	case *syntax.IfStmt:
		fb.ifStmt(s)
	case *syntax.ForStmt:
		fb.forStmt(s, "")
	case *syntax.RangeStmt:
		fb.rangeStmt(s, "")
	case *syntax.SwitchStmt:
		fb.switchStmt(s, "")
	case *syntax.TypeSwitchStmt:
		fb.typeSwitch(s, "")
	case *syntax.LabeledStmt:
		fb.labeled(s)
	case *syntax.BranchStmt:
		fb.branchStmt(s)
	case *syntax.ReturnStmt:
		fb.returnStmt(s)
	case *syntax.DeferStmt:
		fb.deferStmt(s)
	case *syntax.GoStmt:
		fb.b.errorf(s.Pos(), 30, "go statements")
	case *syntax.SelectStmt:
		fb.b.errorf(s.Pos(), 30, "select statements")
	case *syntax.SendStmt:
		fb.b.errorf(s.Pos(), 30, "channel sends")
	default:
		fb.b.errorf(s.Pos(), 6, "translation of %T to IR", s)
	}
}

func (fb *funcBuilder) declStmt(d *syntax.GenDecl) {
	if d.Tok != syntax.Var {
		return
	}
	for _, spec := range d.Specs {
		s := spec.(*syntax.ValueSpec)
		var vals []ir.Value
		if len(s.Values) == 1 && len(s.Names) > 1 {
			vals = fb.multi(s.Values[0], len(s.Names))
		} else {
			for _, e := range s.Values {
				vals = append(vals, fb.copy(fb.expr(e)))
			}
		}
		for i, name := range s.Names {
			v, ok := fb.info.Defs[name].(*types.Var)
			if !ok {
				continue
			}
			t := fb.typ(v.Type())
			cell := fb.alloc(t, v.Name())
			fb.vars[v] = cell
			if i < len(vals) {
				fb.store(cell, fb.convert(vals[i], t))
			}
		}
	}
}

// multi translates an expression with n values: a call with several
// results, or a comma-ok form.
func (fb *funcBuilder) multi(e syntax.Expr, n int) []ir.Value {
	e = unparen(e)
	tv := fb.info.Types[e]
	if tu, ok := tv.Type.(*types.Tuple); ok && !isCommaOkExpr(e) {
		res := fb.expr(e)
		var vals []ir.Value
		for i := range tu.Len() {
			vals = append(vals, fb.emit(&ir.Extract{Tuple: res, Index: i}, fb.typ(tu.At(i).Type())))
		}
		return vals
	}
	var pair ir.Value
	switch x := e.(type) {
	case *syntax.IndexExpr:
		m := fb.expr(x.X)
		mt := m.Type().Underlying().(*types.Map)
		k := fb.convert(fb.expr(x.Indices[0]), mt.Key)
		pair = fb.emit(&ir.Lookup{X: m, Key: k, CommaOk: true}, commaOkType(mt.Elem))
	case *syntax.TypeAssertExpr:
		t := fb.typeOf(x.Type)
		pair = fb.emit(&ir.TypeAssert{X: fb.expr(x.X), Asserted: t, CommaOk: true}, commaOkType(t))
	case *syntax.UnaryExpr:
		fb.b.errorf(x.Pos(), 30, "channel receive")
		return []ir.Value{zero(types.Typ[types.Invalid]), zero(types.Typ[types.Bool])}
	default:
		fb.b.errorf(e.Pos(), 6, "multi-value expression %T", e)
		return nil
	}
	tu := pair.Type().(*types.Tuple)
	return []ir.Value{
		fb.emit(&ir.Extract{Tuple: pair, Index: 0}, tu.At(0).Type()),
		fb.emit(&ir.Extract{Tuple: pair, Index: 1}, tu.At(1).Type()),
	}
}

func isCommaOkExpr(e syntax.Expr) bool {
	switch e.(type) {
	case *syntax.IndexExpr, *syntax.TypeAssertExpr, *syntax.UnaryExpr:
		return true
	}
	return false
}

func commaOkType(t types.Type) *types.Tuple {
	return types.NewTuple(t, types.Typ[types.Bool])
}

func (fb *funcBuilder) assign(s *syntax.AssignStmt) {
	switch s.Tok {
	case syntax.Assign, syntax.Define:
	default:
		fb.opAssign(s.Lhs[0], assignOp(s.Tok), s.Rhs[0])
		return
	}
	var vals []ir.Value
	if len(s.Rhs) == 1 && len(s.Lhs) > 1 {
		vals = fb.multi(s.Rhs[0], len(s.Lhs))
	} else {
		for _, e := range s.Rhs {
			vals = append(vals, fb.copy(fb.expr(e)))
		}
	}
	// The operands of the left side are evaluated, then all values are
	// assigned.
	var targets []*place // nil for _
	for _, e := range s.Lhs {
		id, isName := unparen(e).(*syntax.Name)
		switch {
		case isName && id.Name == "_":
			targets = append(targets, nil)
		case isName && s.Tok == syntax.Define && fb.info.Defs[id] != nil:
			v := fb.info.Defs[id].(*types.Var)
			t := fb.typ(v.Type())
			cell := fb.alloc(t, v.Name())
			fb.vars[v] = cell
			targets = append(targets, &place{addr: cell, t: t})
		default:
			p := fb.place(e)
			if p == nil {
				return
			}
			targets = append(targets, p)
		}
	}
	for i, p := range targets {
		if i < len(vals) && p != nil {
			fb.assignTo(p, vals[i])
		}
	}
}

// place is the destination of an assignment: the variable or part of a
// variable at addr, or the element with key mapKey of the map at mapCell.
type place struct {
	addr    ir.Value
	mapCell ir.Value
	mapKey  ir.Value
	t       types.Type
}

// place evaluates the operands of the left side e of an assignment.
func (fb *funcBuilder) place(e syntax.Expr) *place {
	if ix, ok := unparen(e).(*syntax.IndexExpr); ok {
		if mt, ok := fb.typeOf(ix.X).Underlying().(*types.Map); ok {
			cell := fb.addr(ix.X)
			if cell == nil {
				cell = fb.inoutArg(ix.X)
			}
			k := fb.convert(fb.expr(ix.Indices[0]), mt.Key)
			return &place{mapCell: cell, mapKey: k, t: mt.Elem}
		}
	}
	a := fb.addr(e)
	if a == nil {
		fb.b.errorf(e.Pos(), 6, "assignment to %T", e)
		return nil
	}
	return &place{addr: a, t: a.Type().(*ir.Pointer).Elem}
}

// assignTo assigns v to p.
func (fb *funcBuilder) assignTo(p *place, v ir.Value) {
	v = fb.convert(v, p.t)
	if p.mapCell != nil {
		fb.emitStmt(&ir.MapUpdate{Map: p.mapCell, Key: p.mapKey, Value: v})
	} else {
		fb.store(p.addr, v)
	}
}

func assignOp(tok syntax.Kind) syntax.Kind {
	switch tok {
	case syntax.AddAssign:
		return syntax.Add
	case syntax.SubAssign:
		return syntax.Sub
	case syntax.MulAssign:
		return syntax.Mul
	case syntax.QuoAssign:
		return syntax.Quo
	case syntax.RemAssign:
		return syntax.Rem
	case syntax.AndAssign:
		return syntax.And
	case syntax.OrAssign:
		return syntax.Or
	case syntax.XorAssign:
		return syntax.Xor
	case syntax.ShlAssign:
		return syntax.Shl
	case syntax.ShrAssign:
		return syntax.Shr
	case syntax.AndNotAssign:
		return syntax.AndNot
	}
	return syntax.Illegal
}

// opAssign translates x op= y, and x++ and x-- with y nil.
func (fb *funcBuilder) opAssign(x syntax.Expr, op syntax.Kind, y syntax.Expr) {
	t := fb.typeOf(x)
	var rhs ir.Value
	if y == nil {
		rhs = constant_(t, oneConst)
	} else {
		rhs = fb.expr(y)
		if op != syntax.Shl && op != syntax.Shr {
			rhs = fb.convert(rhs, t)
		}
	}
	if ix, ok := unparen(x).(*syntax.IndexExpr); ok {
		if mt, ok := fb.typeOf(ix.X).Underlying().(*types.Map); ok {
			cell := fb.addr(ix.X)
			if cell == nil {
				cell = fb.inoutArg(ix.X)
			}
			k := fb.convert(fb.expr(ix.Indices[0]), mt.Key)
			old := fb.emit(&ir.Lookup{X: fb.load(cell), Key: k}, mt.Elem)
			v := fb.emit(&ir.BinOp{Op: op, X: old, Y: rhs}, t)
			fb.emitStmt(&ir.MapUpdate{Map: cell, Key: k, Value: v})
			return
		}
	}
	a := fb.addr(x)
	if a == nil {
		fb.b.errorf(x.Pos(), 6, "assignment to %T", x)
		return
	}
	v := fb.emit(&ir.BinOp{Op: op, X: fb.load(a), Y: rhs}, t)
	fb.store(a, v)
}

func (fb *funcBuilder) ifStmt(s *syntax.IfStmt) {
	if s.Init != nil {
		fb.stmt(s.Init)
	}
	if tv := fb.info.Types[s.Cond]; tv.Value != nil && tv.Value.Kind() == constant.Bool {
		// A constant condition: only the branch it takes, which in an
		// instance is the only one checked.
		if constant.BoolVal(tv.Value) {
			fb.stmts(s.Body.List)
		} else if s.Else != nil {
			fb.stmt(s.Else)
		}
		return
	}
	then, done := fb.newBlock("if.then"), fb.newBlock("if.done")
	els := done
	if s.Else != nil {
		els = fb.newBlock("if.else")
	}
	fb.cond(s.Cond, then, els)
	fb.cur = then
	fb.stmts(s.Body.List)
	fb.jump(done)
	if s.Else != nil {
		fb.cur = els
		fb.stmt(s.Else)
		fb.jump(done)
	}
	fb.cur = done
}

// cond translates a condition with short-circuit evaluation into
// branches.
func (fb *funcBuilder) cond(e syntax.Expr, t, f *ir.Block) {
	switch x := unparen(e).(type) {
	case *syntax.BinaryExpr:
		switch x.Op {
		case syntax.LAnd:
			rhs := fb.newBlock("cond.and")
			fb.cond(x.X, rhs, f)
			fb.cur = rhs
			fb.cond(x.Y, t, f)
			return
		case syntax.LOr:
			rhs := fb.newBlock("cond.or")
			fb.cond(x.X, t, rhs)
			fb.cur = rhs
			fb.cond(x.Y, t, f)
			return
		}
	case *syntax.UnaryExpr:
		if x.Op == syntax.Not {
			fb.cond(x.X, f, t)
			return
		}
	}
	fb.branch(fb.expr(e), t, f)
}

func (fb *funcBuilder) labeled(s *syntax.LabeledStmt) {
	label := s.Label.Name
	blk := fb.labelBlock(label)
	fb.jump(blk)
	fb.cur = blk
	switch t := s.Stmt.(type) {
	case *syntax.ForStmt:
		fb.forStmt(t, label)
	case *syntax.RangeStmt:
		fb.rangeStmt(t, label)
	case *syntax.SwitchStmt:
		fb.switchStmt(t, label)
	case *syntax.TypeSwitchStmt:
		fb.typeSwitch(t, label)
	default:
		fb.stmt(s.Stmt)
	}
}

func (fb *funcBuilder) labelBlock(label string) *ir.Block {
	if b, ok := fb.labels[label]; ok {
		return b
	}
	b := fb.newBlock("label " + label)
	fb.labels[label] = b
	return b
}

func (fb *funcBuilder) branchStmt(s *syntax.BranchStmt) {
	switch s.Tok {
	case syntax.Goto:
		fb.jump(fb.labelBlock(s.Label.Name))
		return
	case syntax.Fallthrough:
		fb.jump(fb.fallthroughTo)
		return
	}
	for i := len(fb.targets) - 1; i >= 0; i-- {
		t := fb.targets[i]
		if s.Label != nil && t.label != s.Label.Name {
			continue
		}
		if t.yield {
			fb.yieldBranch(s, t)
			return
		}
		if s.Tok == syntax.Continue && t.cont == nil {
			continue // a switch
		}
		if s.Tok == syntax.Break {
			fb.jump(t.brk)
		} else {
			fb.jump(t.cont)
		}
		return
	}
	if fb.yield != nil {
		// A branch out of the body of a range-over-func loop.
		fb.yieldBranch(s, nil)
		return
	}
	fb.b.errorf(s.Pos(), 6, "branch statement")
}

func (fb *funcBuilder) forStmt(s *syntax.ForStmt, label string) {
	if s.Init != nil {
		fb.stmt(s.Init)
	}
	head, body, post, done := fb.newBlock("for.head"), fb.newBlock("for.body"), fb.newBlock("for.post"), fb.newBlock("for.done")
	fb.jump(head)
	fb.cur = head
	if s.Cond != nil {
		fb.cond(s.Cond, body, done)
	} else {
		fb.jump(body)
	}
	// Closures capture copies, so each iteration sees its own values of
	// the loop variables (expr.closure.loop) without new cells.
	fb.cur = body
	fb.targets = append(fb.targets, &target{label: label, brk: done, cont: post})
	fb.stmts(s.Body.List)
	fb.targets = fb.targets[:len(fb.targets)-1]
	fb.jump(post)
	fb.cur = post
	if s.Post != nil {
		fb.stmt(s.Post)
	}
	fb.jump(head)
	fb.cur = done
}

func (fb *funcBuilder) returnStmt(s *syntax.ReturnStmt) {
	sig := fb.fn.Sig
	if fb.yield != nil {
		fb.yieldReturn(s)
		return
	}
	if len(s.Results) == 0 {
		fb.ret(nil)
		return
	}
	var vals []ir.Value
	if len(s.Results) == 1 && sig.Results.Len() > 1 {
		vals = fb.multi(s.Results[0], sig.Results.Len())
	} else {
		for _, e := range s.Results {
			vals = append(vals, fb.copy(fb.expr(e)))
		}
	}
	for i := range vals {
		vals[i] = fb.convert(vals[i], sig.Results.At(i).Type())
	}
	fb.emitStmt(&ir.Return{Results: vals})
}

func (fb *funcBuilder) deferStmt(s *syntax.DeferStmt) {
	v := fb.call(s.Call, true)
	call, ok := v.(*ir.Call)
	if !ok {
		fb.b.errorf(s.Pos(), 6, "defer of this call")
		return
	}
	// The call was emitted: replace it by a deferred call.
	instrs := fb.cur.Instrs
	if len(instrs) > 0 && instrs[len(instrs)-1] == ir.Instr(call) {
		fb.cur.Instrs = instrs[:len(instrs)-1]
	}
	fb.emitStmt(&ir.Defer{Call: call})
}

var oneConst = constOne()

// asm translates an asm statement (stmt.asm.syntax): the output
// registers are stored to the left-hand side.
func (fb *funcBuilder) asm(s *syntax.AsmStmt) {
	text, _ := strconv.Unquote(s.Text.Value)
	a := &ir.Asm{Text: text}
	if r := s.Regs(); r != nil {
		a.In = fb.expr(r)
	}
	var t types.Type = types.NewTuple()
	if len(s.Lhs) == 1 {
		t = fb.typeOf(s.Lhs[0])
	}
	v := fb.emit(a, t)
	if len(s.Lhs) == 1 {
		if addr := fb.addr(s.Lhs[0]); addr != nil {
			fb.store(addr, v)
		}
	}
}
