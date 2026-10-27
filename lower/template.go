package lower

import (
	"fmt"
	"go/constant"

	"an/ir"
	"an/syntax"
	"an/types"
)

// template returns the IR function of an instance of a template: a
// function with a ...any parameter or a generic function using an/meta.
// The tuple becomes one parameter per element (ct.any.instance).
func (b *builder) template(inst *types.TemplateInstance) *ir.Func {
	info := b.declOf[inst.Func.Decl]
	key := info.Pkg.Path + "." + inst.Key
	if fn, ok := b.funcs[key]; ok {
		return fn
	}
	var params []types.Type
	if r := inst.Sig.Recv; r != nil {
		t := r.Type()
		if r.Inout() {
			t = &ir.Pointer{Elem: t}
		}
		params = append(params, t)
	}
	for _, v := range inst.Sig.Params.Vars {
		if tt, ok := v.Type().(*types.TupleType); ok {
			params = append(params, tt.Elems...)
			continue
		}
		t := v.Type()
		if v.Inout() {
			t = &ir.Pointer{Elem: t}
		}
		params = append(params, t)
	}
	sig := &types.Signature{Params: types.NewTuple(params...), Results: inst.Sig.Results}
	fn := ir.NewFunc(key, sig, inst.Decl.Name.Pos())
	b.funcs[key] = fn
	b.prog.Funcs = append(b.prog.Funcs, fn)
	b.queue = append(b.queue, func() {
		fb := b.newFuncBuilder(info, fn, nil, nil)
		if r := inst.Sig.Recv; r != nil {
			fb.param(r, r.Type())
		}
		for _, v := range inst.Sig.Params.Vars {
			if tt, ok := v.Type().(*types.TupleType); ok {
				for j, t := range tt.Elems {
					p := &ir.Param{Pname: fmt.Sprintf("%s$%d", v.Name(), j), Typ: t}
					fn.Params = append(fn.Params, p)
					fb.tuple = append(fb.tuple, p)
				}
				continue
			}
			fb.param(v, v.Type())
		}
		for _, v := range inst.Sig.Results.Vars {
			if v.Name() != "" {
				cell := fb.alloc(v.Type(), v.Name())
				fb.vars[v] = cell
				fb.results = append(fb.results, cell)
			}
		}
		fb.body(inst.Decl.Body.List)
		fb.finish()
	})
	return fn
}

// templateCall translates a call of a template.
func (fb *funcBuilder) templateCall(e *syntax.CallExpr, inst *types.TemplateInstance) ir.Value {
	fn := fb.b.template(inst)
	sig := inst.Sig
	n := sig.Params.Len()
	if sig.AnyTuple {
		n--
	}
	fixed := &types.Signature{Params: &types.Tuple{Vars: sig.Params.Vars[:n]}, Results: sig.Results}
	call := &syntax.CallExpr{Fun: e.Fun, Lparen: e.Lparen, Args: e.Args[:min(n, len(e.Args))], Rparen: e.Rparen}
	args := fb.args(call, fixed)
	if r := sig.Recv; r != nil {
		// A method: the receiver comes first.
		sel, ok := unparen(e.Fun).(*syntax.SelectorExpr)
		if !ok || len(fb.info.Selections[sel].Index) > 1 {
			fb.b.errorf(e.Pos(), 29, "calls of promoted methods with ...any parameters")
			return zero(resultType(sig))
		}
		var recv ir.Value
		if r.Inout() {
			recv = fb.inoutArg(sel.X)
		} else {
			recv = fb.copy(fb.expr(sel.X))
		}
		args = append([]ir.Value{recv}, args...)
	}
	if sig.AnyTuple {
		args = append(args, fb.tupleArgs(e.Args[n:], inst.Tuple)...)
	}
	return fb.emit(&ir.Call{Callee: &ir.FuncValue{Func: fn}, Args: args}, resultType(sig))
}

// tupleArgs translates the arguments that form a tuple of the given
// element types; a forwarded tuple, args..., gives the values of the
// tuple parameters of the current function (ct.any.use 2).
func (fb *funcBuilder) tupleArgs(exprs []syntax.Expr, elems []types.Type) []ir.Value {
	var vals []ir.Value
	for _, a := range exprs {
		if _, ok := fb.typeOf(a).(*types.TupleType); ok {
			for _, p := range fb.tuple {
				vals = append(vals, fb.copy(p))
			}
			continue
		}
		vals = append(vals, fb.copy(fb.expr(a)))
	}
	for i := range vals {
		if i < len(elems) {
			vals[i] = fb.convert(vals[i], elems[i])
		}
	}
	return vals
}

// forConst translates a for const loop: its unrolled instances, one after
// another (ct.for.unroll, ct.for.control).
func (fb *funcBuilder) forConst(s *syntax.RangeStmt, label string) {
	units := fb.info.Unrolled[s]
	done := fb.newBlock("forconst.done")
	for _, u := range units {
		next := fb.newBlock("forconst.next")
		if v, ok := u.Obj.(*types.Var); ok && u.Tuple >= 0 && u.Tuple < len(fb.tuple) {
			cell := fb.alloc(v.Type(), v.Name())
			fb.store(cell, fb.copy(fb.tuple[u.Tuple]))
			fb.vars[v] = cell
		}
		fb.targets = append(fb.targets, &target{label: label, brk: done, cont: next})
		fb.stmts(u.Body.List)
		fb.targets = fb.targets[:len(fb.targets)-1]
		fb.jump(next)
		fb.cur = next
	}
	fb.jump(done)
	fb.cur = done
}

// metaCall translates a call of a method of a descriptor of an/meta.
func (fb *funcBuilder) metaCall(e *syntax.CallExpr, mc *types.MetaCall) ir.Value {
	t := fb.typeOf(e)
	switch mc.Kind {
	case "Get":
		v := fb.expr(e.Args[0])
		if mc.Variant >= 0 {
			return fb.emit(&ir.VariantField{X: v, Variant: mc.Variant, Field: mc.Index}, t)
		}
		return fb.emit(&ir.Field{X: v, Field: mc.Index}, t)
	case "Set":
		a := fb.addr(e.Args[0])
		if a == nil {
			fb.b.errorf(e.Pos(), 10, "f.Set of an operand that is not addressable")
			return nil
		}
		st := a.Type().(*ir.Pointer).Elem.Underlying().(*types.Struct)
		ft := st.Fields[mc.Index].Type()
		fa := fb.emit(&ir.FieldAddr{X: a, Field: mc.Index}, &ir.Pointer{Elem: ft})
		fb.store(fa, fb.convert(fb.copy(fb.expr(e.Args[1])), ft))
		return nil
	case "Is":
		v := fb.expr(e.Args[0])
		idx := fb.emit(&ir.VariantIndex{X: v}, types.Typ[types.Int])
		return fb.emit(&ir.BinOp{Op: syntax.Eql, X: idx, Y: &ir.Const{Typ: types.Typ[types.Int], Val: constant.MakeInt64(int64(mc.Variant))}}, types.Typ[types.Bool])
	case "Make":
		et := t.Underlying().(*types.Enum)
		fields := et.Variants[mc.Variant].Fields()
		var vals []ir.Value
		for i, a := range e.Args {
			vals = append(vals, fb.convert(fb.copy(fb.expr(a)), fields[i].Type()))
		}
		return fb.emit(&ir.MakeVariant{Variant: mc.Variant, Fields: vals}, t)
	}
	return nil
}
