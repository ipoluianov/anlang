package lower

import (
	"go/constant"
	"strings"

	"an/ir"
	"an/syntax"
	"an/types"
)

// call translates a call. A call of panic ends the block.
func (fb *funcBuilder) call(e *syntax.CallExpr, stmt bool) ir.Value {
	if mc := fb.info.MetaCalls[e]; mc != nil {
		return fb.metaCall(e, mc)
	}
	if inst := fb.info.TemplateCalls[e]; inst != nil {
		return fb.templateCall(e, inst)
	}
	tv := fb.info.Types[e.Fun]
	switch {
	case tv.IsType():
		return fb.conversion(e)
	case tv.IsBuiltin():
		return fb.builtin(e)
	}
	if sf, ok := unparen(e.Fun).(*syntax.SelectorExpr); ok {
		if sel := fb.info.Selections[sf]; sel != nil && sel.Kind == types.MethodVal {
			return fb.methodCall(e, sf, sel)
		}
	}
	sig, ok := types.CoreType(fb.typeOf(e.Fun)).(*types.Signature)
	if !ok {
		fb.b.errorf(e.Pos(), 6, "call of %s", fb.typeOf(e.Fun))
		return zero(fb.typeOf(e))
	}
	var callee ir.Value
	if v := fb.variantOf(e.Fun); v != nil {
		// The constructor of the instance of a generic enum type that the
		// call constructs.
		callee = &ir.FuncValue{Func: fb.b.ctorOf(v, fb.typeOf(e))}
	} else {
		callee = fb.expr(e.Fun)
	}
	if s, ok := types.CoreType(callee.Type()).(*types.Signature); ok {
		sig = s // the signature of the instance for a generic function
	}
	args := fb.args(e, sig)
	return fb.emit(&ir.Call{Callee: callee, Args: args}, resultType(sig))
}

// args translates the arguments of a call of a function with signature
// sig: copies for value parameters, addresses for inout parameters, a
// slice for the variadic parameter.
func (fb *funcBuilder) args(e *syntax.CallExpr, sig *types.Signature) []ir.Value {
	n := sig.Params.Len()
	if vals := fb.spread(e); vals != nil {
		return fb.packArgs(vals, sig, false)
	}
	var vals []ir.Value
	for i, a := range e.Args {
		var p *types.Var
		if i < n {
			p = sig.Params.At(i)
		}
		if p != nil && p.Inout() {
			vals = append(vals, fb.inoutArg(a))
			continue
		}
		v := fb.expr(a)
		vals = append(vals, fb.copy(v))
	}
	return fb.packArgs(vals, sig, e.Ellipsis.IsValid())
}

// spread returns the results of a call with several results that is the
// single argument of call e and so supplies all arguments, or nil.
func (fb *funcBuilder) spread(e *syntax.CallExpr) []ir.Value {
	if len(e.Args) != 1 {
		return nil
	}
	tu, ok := fb.typeOf(e.Args[0]).(*types.Tuple)
	if !ok || tu.Len() < 2 {
		return nil
	}
	res := fb.expr(e.Args[0])
	var vals []ir.Value
	for i := range tu.Len() {
		vals = append(vals, fb.emit(&ir.Extract{Tuple: res, Index: i}, tu.At(i).Type()))
	}
	return vals
}

// inoutArg returns the address for an inout argument.
func (fb *funcBuilder) inoutArg(e syntax.Expr) ir.Value {
	if s, ok := unparen(e).(*syntax.SliceExpr); ok {
		// A subrange of an addressable slice or array: the parameter
		// denotes the elements of the operand (mem.inout.subslice).
		x := fb.addr(s.X)
		opt := func(e syntax.Expr) ir.Value {
			if e == nil {
				return nil
			}
			return fb.expr(e)
		}
		return fb.emit(&ir.SubsliceAddr{X: x, Low: opt(s.Low), High: opt(s.High)}, &ir.Pointer{Elem: fb.typeOf(s)})
	}
	if a := fb.addr(e); a != nil {
		return a
	}
	v := fb.expr(e)
	cell := fb.alloc(v.Type(), "inout")
	fb.store(cell, v)
	return cell
}

// packArgs converts the arguments to the parameter types and packs the
// variadic arguments into a slice.
func (fb *funcBuilder) packArgs(vals []ir.Value, sig *types.Signature, spread bool) []ir.Value {
	n := sig.Params.Len()
	var args []ir.Value
	for i, v := range vals {
		if sig.Variadic && i >= n-1 && !spread {
			break
		}
		if i < n && !sig.Params.At(i).Inout() {
			v = fb.convert(v, sig.Params.At(i).Type())
		}
		args = append(args, v)
	}
	if sig.Variadic && !spread {
		st := sig.Params.At(n - 1).Type()
		et := st.Underlying().(*types.Slice).Elem
		var elems []ir.Value
		for _, v := range vals[min(n-1, len(vals)):] {
			elems = append(elems, fb.convert(v, et))
		}
		if len(elems) == 0 {
			args = append(args, zero(st))
		} else {
			args = append(args, fb.emit(&ir.MakeArray{Elems: elems}, st))
		}
	}
	return args
}

// methodCall translates x.M(args).
func (fb *funcBuilder) methodCall(e *syntax.CallExpr, sf *syntax.SelectorExpr, sel *types.Selection) ir.Value {
	m := sel.Obj.(*types.Func)
	fsig := fb.typeOf(sf).(*types.Signature)
	recvT := fb.typeOf(sf.X)
	// The receiver: the embedded field that holds the method.
	inout := false
	if ms := m.Signature(); ms.Recv != nil && ms.Recv.Inout() {
		inout = true
	}
	var recv ir.Value
	rt := recvT
	if inout {
		base := fb.addr(sf.X)
		if base == nil {
			base = fb.inoutArg(sf.X)
		}
		recv = fb.fieldPath(base, sel.Index, true)
		rt = recv.Type().(*ir.Pointer).Elem
	} else {
		recv = fb.fieldPath(fb.expr(sf.X), sel.Index, false)
		rt = recv.Type()
		recv = fb.copy(recv)
	}
	args := fb.args(e, fsig)
	if n, ok := rt.(*types.Named); ok && n.Origin().Obj.Pkg() == nil && n.Origin().Obj.Name() == "Option" && m.Name() == "Get" {
		return fb.emit(&ir.Call{Callee: &ir.Builtin{Bname: "Option.Get", Typ: resultType(fsig)}, Args: []ir.Value{recv}}, resultType(fsig))
	}
	if types.IsInterface(rt) {
		return fb.emit(&ir.Call{Callee: recv, Method: m.Name(), Args: args}, resultType(fsig))
	}
	// A method of a type parameter is the method of its type argument.
	if m.Decl == nil || types.IsTypeParam(sel.Recv) {
		obj, _ := fb.info.LookupFieldOrMethod(rt, m.Name())
		if cm, ok := obj.(*types.Func); ok {
			m = cm
		}
	}
	target := fb.b.method(fb.info, rt, m)
	return fb.emit(&ir.Call{Callee: &ir.FuncValue{Func: target}, Args: append([]ir.Value{recv}, args...)}, resultType(fsig))
}

// conversion translates T(x).
func (fb *funcBuilder) conversion(e *syntax.CallExpr) ir.Value {
	t := fb.typeOf(e)
	if c := fb.constOf(e); c != nil {
		return c
	}
	x := fb.expr(e.Args[0])
	if types.IsInterface(t) && !types.IsInterface(x.Type()) {
		return fb.convert(x, t)
	}
	if c, ok := x.(*ir.Const); ok && c.Val == nil && c.Computed == nil {
		return zero(t)
	}
	if types.Identical(x.Type(), t) {
		return x
	}
	return fb.emit(&ir.Convert{X: x}, t)
}

// builtin translates a call of a built-in function.
func (fb *funcBuilder) builtin(e *syntax.CallExpr) ir.Value {
	if c := fb.constOf(e); c != nil {
		return c
	}
	if b, ok := fb.info.Bytes[e]; ok {
		// A []u8 constant made at compile time (ct.encoding.const).
		elems := make([]any, len(b))
		for i, x := range b {
			elems[i] = constant.MakeInt64(int64(x))
		}
		return &ir.Const{Typ: fb.typeOf(e), Computed: &types.Composite{Elems: elems}}
	}
	var id *syntax.Name
	switch f := unparen(e.Fun).(type) {
	case *syntax.Name:
		id = f
	case *syntax.SelectorExpr:
		id = f.Sel
	}
	b, _ := fb.info.Uses[id].(*types.Builtin)
	name := b.Name()
	if b.Pkg() != "" {
		name = strings.TrimPrefix(b.Pkg(), "an/") + "." + name // fmt.Println, bits.WrappingAdd
	}
	t := fb.typeOf(e)
	call := func(args ...ir.Value) ir.Value {
		return fb.emit(&ir.Call{Callee: &ir.Builtin{Bname: name, Typ: t}, Args: args}, t)
	}
	switch name {
	case "make":
		mt := fb.typeOf(e.Args[0])
		switch mt.Underlying().(type) {
		case *types.Slice:
			n := fb.expr(e.Args[1])
			c := n
			if len(e.Args) > 2 {
				c = fb.expr(e.Args[2])
			}
			return fb.emit(&ir.MakeSlice{Len: fb.convert(n, types.Typ[types.Int]), Cap: fb.convert(c, types.Typ[types.Int])}, mt)
		case *types.Map:
			return fb.emit(&ir.MakeMap{}, mt)
		}
		fb.b.errorf(e.Pos(), 30, "channels")
		return zero(mt)
	case "close":
		fb.b.errorf(e.Pos(), 30, "channels")
		return zero(t)
	case "panic":
		fb.emitStmt(&ir.Panic{X: fb.convert(fb.convertArg(e.Args[0]), emptyInterface)})
		fb.cur = fb.newBlock("unreachable")
		return zero(t)
	case "copy", "delete", "clear":
		// The first operand is modified: its address.
		var a ir.Value
		var rest []ir.Value
		if vals := fb.spread(e); vals != nil {
			a = fb.alloc(vals[0].Type(), "operand")
			fb.store(a, vals[0])
			rest = vals[1:]
		} else {
			a = fb.addr(e.Args[0])
			if a == nil {
				a = fb.inoutArg(e.Args[0])
			}
			for _, x := range e.Args[1:] {
				rest = append(rest, fb.expr(x))
			}
		}
		args := []ir.Value{a}
		for _, v := range rest {
			if name == "delete" {
				v = fb.convert(v, a.Type().(*ir.Pointer).Elem.Underlying().(*types.Map).Key)
			}
			args = append(args, v)
		}
		return call(args...)
	case "append":
		if vals := fb.spread(e); vals != nil {
			et := vals[0].Type().Underlying().(*types.Slice).Elem
			args := []ir.Value{vals[0]}
			for _, v := range vals[1:] {
				args = append(args, fb.convert(fb.copy(v), et))
			}
			return call(args...)
		}
		s := fb.expr(e.Args[0])
		st := s.Type()
		args := []ir.Value{s}
		if e.Ellipsis.IsValid() {
			x := fb.expr(e.Args[1])
			if b, ok := x.Type().Underlying().(*types.Basic); ok && b.Info()&types.IsString != 0 {
				return fb.emit(&ir.Call{Callee: &ir.Builtin{Bname: "appendstring", Typ: t}, Args: []ir.Value{s, x}}, t)
			}
			args = append(args, fb.copy(x))
			return fb.emit(&ir.Call{Callee: &ir.Builtin{Bname: "appendslice", Typ: t}, Args: args}, t)
		}
		et := st.Underlying().(*types.Slice).Elem
		for _, x := range e.Args[1:] {
			args = append(args, fb.convert(fb.copy(fb.expr(x)), et))
		}
		return call(args...)
	case "bits.WrappingAdd", "bits.WrappingSub", "bits.WrappingMul",
		"bits.SaturatingAdd", "bits.SaturatingSub", "bits.SaturatingMul",
		"bits.CheckedAdd", "bits.CheckedSub", "bits.CheckedMul":
		at := t
		if tu, ok := t.(*types.Tuple); ok {
			at = tu.At(0).Type()
		}
		var args []ir.Value
		for _, x := range e.Args {
			args = append(args, fb.convert(fb.expr(x), at))
		}
		return call(args...)
	case "min", "max":
		var args []ir.Value
		for _, x := range e.Args {
			args = append(args, fb.convert(fb.expr(x), t))
		}
		return call(args...)
	}
	vals := fb.spread(e)
	if vals == nil {
		for _, x := range e.Args {
			if _, ok := fb.typeOf(x).(*types.TupleType); ok {
				vals = append(vals, fb.tupleArgs([]syntax.Expr{x}, nil)...) // fmt.Print(args...)
				continue
			}
			vals = append(vals, fb.convertArg(x))
		}
	}
	var args []ir.Value
	for _, v := range vals {
		if b.Pkg() == "an/fmt" {
			// fmt.Print and fmt.Println take ...any: interface values, so
			// that the methods Error and String of the dynamic types apply.
			v = fb.convert(v, emptyInterface)
		}
		args = append(args, v)
	}
	return call(args...)
}

var emptyInterface = &types.Interface{}

// convertArg translates an argument of a built-in function: untyped
// constants get their default types.
func (fb *funcBuilder) convertArg(e syntax.Expr) ir.Value {
	return fb.copy(fb.expr(e))
}
