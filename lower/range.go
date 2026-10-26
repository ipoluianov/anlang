package lower

import (
	"go/constant"

	"an/ir"
	"an/syntax"
	"an/types"
)

func walkNames(list []syntax.Stmt, f func(*syntax.Name)) {
	syntax.Walk(&syntax.BlockStmt{List: list}, func(n syntax.Node) bool {
		if id, ok := n.(*syntax.Name); ok {
			f(id)
		}
		return true
	})
}

func intConst(n int64) *ir.Const {
	return &ir.Const{Typ: types.Typ[types.Int], Val: constant.MakeInt64(n)}
}

// rangeVar returns the place to store an iteration value e in: a new
// cell for :=, the operand for =, or nil for _ or no variable.
func (fb *funcBuilder) rangeVar(e syntax.Expr, define bool, t types.Type) *place {
	if e == nil {
		return nil
	}
	id, isName := unparen(e).(*syntax.Name)
	if isName && id.Name == "_" {
		return nil
	}
	if define {
		v, ok := fb.info.Defs[id].(*types.Var)
		if !ok {
			return nil
		}
		t := fb.typ(v.Type())
		cell := fb.alloc(t, v.Name())
		fb.vars[v] = cell
		return &place{addr: cell, t: t}
	}
	return fb.place(e)
}

func (fb *funcBuilder) storeRange(p *place, v ir.Value) {
	if p != nil {
		fb.assignTo(p, v)
	}
}

func (fb *funcBuilder) rangeStmt(s *syntax.RangeStmt, label string) {
	if s.Const {
		fb.forConst(s, label)
		return
	}
	xt := fb.typeOf(s.X)
	define := s.Tok == syntax.Define
	if sig, ok := types.CoreType(xt).(*types.Signature); ok {
		fb.rangeFunc(s, sig, label)
		return
	}
	if _, ok := xt.Underlying().(*types.Chan); ok {
		fb.b.errorf(s.Pos(), 30, "range over a channel")
		return
	}
	// The range expression is evaluated once; the iteration works on a
	// copy of it (stmt.range.copy).
	x := fb.copy(fb.expr(s.X))
	var n ir.Value
	switch u := xt.Underlying().(type) {
	case *types.Basic:
		if u.Info()&types.IsInteger != 0 {
			n = x
		} else {
			n = fb.emit(&ir.Call{Callee: &ir.Builtin{Bname: "len", Typ: types.Typ[types.Int]}, Args: []ir.Value{x}}, types.Typ[types.Int])
		}
	case *types.Map:
		keys := fb.emit(&ir.Call{Callee: &ir.Builtin{Bname: "mapkeys", Typ: types.NewSlice(u.Key)}, Args: []ir.Value{x}}, types.NewSlice(u.Key))
		x = &mapRange{m: x, keys: keys}
		n = fb.emit(&ir.Call{Callee: &ir.Builtin{Bname: "len", Typ: types.Typ[types.Int]}, Args: []ir.Value{keys}}, types.Typ[types.Int])
	default:
		n = fb.emit(&ir.Call{Callee: &ir.Builtin{Bname: "len", Typ: types.Typ[types.Int]}, Args: []ir.Value{x}}, types.Typ[types.Int])
	}
	var it types.Type = types.Typ[types.Int]
	if b, ok := xt.Underlying().(*types.Basic); ok && b.Info()&types.IsInteger != 0 {
		it = xt
	}
	idx := fb.alloc(it, "index")
	fb.store(idx, &ir.Const{Typ: it, Val: constant.MakeInt64(0)})
	head, body, post, done := fb.newBlock("range.head"), fb.newBlock("range.body"), fb.newBlock("range.post"), fb.newBlock("range.done")
	fb.jump(head)
	fb.cur = head
	i := fb.load(idx)
	more := fb.emit(&ir.BinOp{Op: syntax.Lss, X: i, Y: fb.convert(n, it)}, types.Typ[types.Bool])
	fb.branch(more, body, done)
	fb.cur = body
	next := ir.Value(nil) // the index of the next iteration, for strings
	switch u := xt.Underlying().(type) {
	case *types.Basic:
		if u.Info()&types.IsString != 0 {
			pair := fb.emit(&ir.Call{Callee: &ir.Builtin{Bname: "decoderune", Typ: types.NewTuple(types.Typ[types.I32], types.Typ[types.Int])}, Args: []ir.Value{x, i}}, types.NewTuple(types.Typ[types.I32], types.Typ[types.Int]))
			r := fb.emit(&ir.Extract{Tuple: pair, Index: 0}, types.Typ[types.I32])
			w := fb.emit(&ir.Extract{Tuple: pair, Index: 1}, types.Typ[types.Int])
			next = fb.emit(&ir.BinOp{Op: syntax.Add, X: i, Y: w}, types.Typ[types.Int])
			fb.storeRange(fb.rangeVar(s.Key, define, it), i)
			fb.storeRange(fb.rangeVar(s.Value, define, types.Typ[types.I32]), r)
		} else {
			fb.storeRange(fb.rangeVar(s.Key, define, it), i)
		}
	case *types.Map:
		mr := x.(*mapRange)
		k := fb.emit(&ir.Index{X: mr.keys, Index: i}, u.Key)
		fb.storeRange(fb.rangeVar(s.Key, define, u.Key), k)
		if s.Value != nil {
			v := fb.emit(&ir.Lookup{X: mr.m, Key: k}, u.Elem)
			fb.storeRange(fb.rangeVar(s.Value, define, u.Elem), fb.copy(v))
		}
	default:
		fb.storeRange(fb.rangeVar(s.Key, define, it), i)
		if s.Value != nil {
			v := fb.emit(&ir.Index{X: x, Index: i, Proven: true}, elemType(xt))
			fb.storeRange(fb.rangeVar(s.Value, define, elemType(xt)), fb.copy(v))
		}
	}
	fb.targets = append(fb.targets, &target{label: label, brk: done, cont: post})
	fb.stmts(s.Body.List)
	fb.targets = fb.targets[:len(fb.targets)-1]
	fb.jump(post)
	fb.cur = post
	if next == nil {
		// The index is below the length, so the next one does not overflow.
		next = fb.emit(&ir.BinOp{Op: syntax.Add, X: fb.load(idx), Y: &ir.Const{Typ: it, Val: constant.MakeInt64(1)}, Proven: true}, it)
	}
	fb.store(idx, next)
	fb.jump(head)
	fb.cur = done
}

// mapRange is the snapshot of a map and its keys during a range loop; it
// is not an IR value.
type mapRange struct {
	ir.Value
	m, keys ir.Value
}

// yieldContext is the state of the body of a range-over-func loop, which
// is translated to the yield function: break, continue and return in the
// body set a state that the code after the iterator call acts on.
type yieldContext struct {
	state    ir.Value   // address of the state cell: 0 normal, 1 return, 2+i branch to exits[i]
	results  []ir.Value // addresses of the result cells of the enclosing function
	exits    []exit     // branches out of the loop to enclosing statements
	outer    []*target  // the targets around the loop
	returned bool       // the body contains a return
}

type exit struct {
	tok syntax.Kind
	t   *target
}

// rangeFunc translates a loop over an iterator function: the body
// becomes the yield function, which captures the variables of the
// enclosing function by address, since the body is a block of the
// enclosing function (stmt.if-for.go).
func (fb *funcBuilder) rangeFunc(s *syntax.RangeStmt, sig *types.Signature, label string) {
	ysig := types.CoreType(sig.Params.At(0).Type()).(*types.Signature)

	// The callee and leading arguments: a method value with an inout
	// receiver is called with the address of the receiver.
	var callee ir.Value
	var prefix []ir.Value
	if sf, ok := unparen(s.X).(*syntax.SelectorExpr); ok {
		if sel := fb.info.Selections[sf]; sel != nil && sel.Kind == types.MethodVal {
			m := sel.Obj.(*types.Func)
			if ms := m.Signature(); ms.Recv != nil && ms.Recv.Inout() {
				base := fb.addr(sf.X)
				recv := fb.fieldPath(base, sel.Index, true)
				callee = &ir.FuncValue{Func: fb.b.method(fb.info, recv.Type().(*ir.Pointer).Elem, m)}
				prefix = []ir.Value{recv}
			}
		}
	}
	if callee == nil {
		callee = fb.expr(s.X)
	}

	// The state and the result cells.
	state := fb.alloc(types.Typ[types.Int], "range.state")
	fb.store(state, intConst(0))
	var results []ir.Value
	if fb.yield != nil {
		results = fb.yield.results
	} else {
		for i := range fb.fn.Sig.Results.Len() {
			results = append(results, fb.alloc(fb.fn.Sig.Results.At(i).Type(), "range.result"))
		}
	}

	fb.b.nlits++
	yfn := ir.NewFunc(fb.fn.Name+"$yield"+itoa(fb.b.nlits), ysig, s.Pos())
	fb.b.prog.Funcs = append(fb.b.prog.Funcs, yfn)
	yb := fb.b.newFuncBuilder(fb.info, yfn, fb.tps, fb.targs)
	yb.parent, yb.byRef = fb, true
	yb.free = map[*types.Var]*ir.FreeVar{}
	stateFV := &ir.FreeVar{Vname: "state", Typ: state.Type()}
	yfn.FreeVars = append(yfn.FreeVars, stateFV)
	var resultFVs []ir.Value
	for _, r := range results {
		fv := &ir.FreeVar{Vname: "result", Typ: r.Type()}
		yfn.FreeVars = append(yfn.FreeVars, fv)
		resultFVs = append(resultFVs, fv)
	}
	ctx := &yieldContext{state: stateFV, results: resultFVs, outer: fb.targets}
	if fb.yield != nil {
		ctx.outer = append(append([]*target(nil), fb.yield.outer...), fb.targets...)
	}
	yb.yield = ctx

	// The parameters of the yield function are the iteration variables.
	define := s.Tok == syntax.Define
	lhs := []syntax.Expr{s.Key, s.Value}
	for i := range ysig.Params.Len() {
		p := ysig.Params.At(i)
		name := "v" + itoa(i)
		var e syntax.Expr
		if i < len(lhs) {
			e = lhs[i]
		}
		if p.Inout() {
			param := &ir.Param{Pname: name, Typ: &ir.Pointer{Elem: p.Type()}, Inout: true}
			yfn.Params = append(yfn.Params, param)
			if id, ok := e.(*syntax.Name); ok && define {
				if v, ok := fb.info.Defs[id].(*types.Var); ok {
					yb.vars[v] = param
				}
			}
			continue
		}
		param := &ir.Param{Pname: name, Typ: p.Type()}
		yfn.Params = append(yfn.Params, param)
		yb.storeRange(yb.rangeVar(e, define, p.Type()), param)
	}
	yb.targets = []*target{{label: label, yield: true}}
	yb.stmts(s.Body.List)
	if !yb.cur.Terminated() {
		yb.emitStmt(&ir.Return{Results: []ir.Value{&ir.Const{Typ: types.Typ[types.Bool], Val: constant.MakeBool(true)}}})
	}

	bindings := []ir.Value{state}
	bindings = append(bindings, results...)
	yieldVal := fb.closureWith(yfn, bindings, yb.captured, true)
	fb.emit(&ir.Call{Callee: callee, Args: append(prefix, yieldVal)}, &types.Tuple{})

	// Act on the state.
	st := fb.load(state)
	done := fb.newBlock("range.done")
	if len(ctx.exits) == 0 && !ctx.returned {
		fb.jump(done)
		fb.cur = done
		return
	}
	if ctx.returned {
		ret, next := fb.newBlock("range.return"), fb.newBlock("range.next")
		isRet := fb.emit(&ir.BinOp{Op: syntax.Eql, X: st, Y: intConst(1)}, types.Typ[types.Bool])
		fb.branch(isRet, ret, next)
		fb.cur = ret
		if fb.yield != nil {
			fb.store(fb.yield.state, intConst(1))
			fb.emitStmt(&ir.Return{Results: []ir.Value{&ir.Const{Typ: types.Typ[types.Bool], Val: constant.MakeBool(false)}}})
		} else {
			var rs []ir.Value
			for _, r := range results {
				rs = append(rs, fb.load(r))
			}
			fb.emitStmt(&ir.Return{Results: rs})
		}
		fb.cur = next
	}
	for i, ex := range ctx.exits {
		jump, next := fb.newBlock("range.exit"), fb.newBlock("range.next")
		is := fb.emit(&ir.BinOp{Op: syntax.Eql, X: st, Y: intConst(int64(2 + i))}, types.Typ[types.Bool])
		fb.branch(is, jump, next)
		fb.cur = jump
		fb.branchStmt(&syntax.BranchStmt{Tok: ex.tok, Label: &syntax.Name{Name: ex.t.label}})
		fb.cur = next
	}
	fb.jump(done)
	fb.cur = done
}

// yieldBranch translates break or continue in the body of a
// range-over-func loop: t is the loop itself, or nil for a target
// outside it.
func (fb *funcBuilder) yieldBranch(s *syntax.BranchStmt, t *target) {
	f := &ir.Const{Typ: types.Typ[types.Bool], Val: constant.MakeBool(false)}
	if t != nil && t.yield && (s.Label == nil || s.Label.Name == t.label) {
		if s.Tok == syntax.Continue {
			fb.emitStmt(&ir.Return{Results: []ir.Value{&ir.Const{Typ: types.Typ[types.Bool], Val: constant.MakeBool(true)}}})
		} else {
			fb.emitStmt(&ir.Return{Results: []ir.Value{f}})
		}
		return
	}
	ctx := fb.yield
	for i := len(ctx.outer) - 1; i >= 0; i-- {
		ot := ctx.outer[i]
		if s.Label != nil && ot.label != s.Label.Name || s.Label == nil && ot.cont == nil && s.Tok == syntax.Continue {
			continue
		}
		code := len(ctx.exits) + 2
		ctx.exits = append(ctx.exits, exit{tok: s.Tok, t: ot})
		fb.store(ctx.state, intConst(int64(code)))
		fb.emitStmt(&ir.Return{Results: []ir.Value{f}})
		return
	}
	fb.b.errorf(s.Pos(), 6, "this branch out of a range-over-func loop")
}

// yieldReturn translates return in the body of a range-over-func loop:
// the results are stored for the enclosing function.
func (fb *funcBuilder) yieldReturn(s *syntax.ReturnStmt) {
	ctx := fb.yield
	var vals []ir.Value
	if len(s.Results) == 1 && len(ctx.results) > 1 {
		vals = fb.multi(s.Results[0], len(ctx.results))
	} else {
		for _, e := range s.Results {
			vals = append(vals, fb.copy(fb.expr(e)))
		}
	}
	for i, v := range vals {
		fb.store(ctx.results[i], fb.convert(v, ctx.results[i].Type().(*ir.Pointer).Elem))
	}
	fb.store(ctx.state, intConst(1))
	ctx.returned = true
	fb.emitStmt(&ir.Return{Results: []ir.Value{&ir.Const{Typ: types.Typ[types.Bool], Val: constant.MakeBool(false)}}})
}

// closureWith makes a closure of fn over leading bindings and captured
// variables.
func (fb *funcBuilder) closureWith(fn *ir.Func, bindings []ir.Value, captured []*types.Var, byRef bool) ir.Value {
	c := fb.closure(fn, captured, byRef)
	mc := c.(*ir.MakeClosure)
	mc.Bindings = append(bindings, mc.Bindings...)
	return c
}
