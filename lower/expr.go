package lower

import (
	"go/constant"

	"an/ir"
	"an/syntax"
	"an/types"
)

func unparen(e syntax.Expr) syntax.Expr {
	for {
		p, ok := e.(*syntax.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// constOf returns the constant value of e, or nil.
func (fb *funcBuilder) constOf(e syntax.Expr) ir.Value {
	tv, ok := fb.info.Types[e]
	if !ok || tv.Value == nil {
		return nil
	}
	t := fb.typeOf(e)
	return constant_(t, tv.Value)
}

func constant_(t types.Type, v constant.Value) *ir.Const {
	if b, ok := t.Underlying().(*types.Basic); ok {
		switch {
		case b.Info()&types.IsInteger != 0:
			v = constant.ToInt(v)
		case b.Info()&types.IsFloat != 0:
			v = constant.ToFloat(v)
		}
	}
	return &ir.Const{Typ: t, Val: v}
}

func zero(t types.Type) *ir.Const { return &ir.Const{Typ: t} }

// expr translates expression e to a value.
func (fb *funcBuilder) expr(e syntax.Expr) ir.Value {
	if c := fb.constOf(e); c != nil {
		return c
	}
	tv := fb.info.Types[e]
	if tv.Addressable() {
		if a := fb.addr(e); a != nil {
			return fb.load(a)
		}
	}
	switch e := e.(type) {
	case *syntax.ParenExpr:
		return fb.expr(e.X)
	case *syntax.Name:
		return fb.name(e)
	case *syntax.SelectorExpr:
		return fb.selector(e)
	case *syntax.IndexExpr:
		return fb.index(e)
	case *syntax.SliceExpr:
		return fb.slice(e)
	case *syntax.CallExpr:
		return fb.call(e, false)
	case *syntax.UnaryExpr:
		return fb.unary(e)
	case *syntax.BinaryExpr:
		return fb.binary(e)
	case *syntax.CompositeLit:
		return fb.compositeLit(e, fb.typeOf(e))
	case *syntax.FuncLit:
		return fb.funcLit(e)
	case *syntax.TypeAssertExpr:
		x := fb.expr(e.X)
		t := fb.typeOf(e.Type)
		return fb.emit(&ir.TypeAssert{X: x, Asserted: t}, t)
	}
	fb.b.errorf(e.Pos(), 6, "translation of %T to IR", e)
	return zero(fb.typeOf(e))
}

// name translates an identifier that is not a local variable.
func (fb *funcBuilder) name(e *syntax.Name) ir.Value {
	switch obj := fb.info.Uses[e].(type) {
	case *types.Var:
		if a := fb.local(obj); a != nil {
			return fb.load(a)
		}
		fb.b.errorf(e.Pos(), 6, "package-level variable %s", e.Name)
	case *types.Func:
		return &ir.FuncValue{Func: fb.funcInstance(e, obj)}
	case *types.Nil:
		return zero(fb.typeOf(e))
	case *types.Variant:
		return fb.variantValue(e, obj)
	case *types.Const:
		if obj.Computed {
			if fb.b.ready != nil && !fb.b.ready(obj) {
				fb.b.missing = append(fb.b.missing, obj)
			}
			return computed(obj.Type(), obj.Value)
		}
	}
	return zero(fb.typeOf(e))
}

// computed returns the IR constant of the value v of a computed constant
// of type t (ct.const.call).
func computed(t types.Type, v any) *ir.Const {
	switch v := v.(type) {
	case nil:
		return zero(t)
	case constant.Value:
		return constant_(t, v)
	}
	return &ir.Const{Typ: t, Computed: v}
}

// funcInstance returns the IR function that identifier e denotes, with
// the type arguments of its instantiation.
func (fb *funcBuilder) funcInstance(e *syntax.Name, f *types.Func) *ir.Func {
	var targs []types.Type
	if inst, ok := fb.info.Instances[e]; ok {
		for _, t := range inst.TypeArgs {
			targs = append(targs, fb.typ(t))
		}
	}
	return fb.b.function(f, targs)
}

// variantValue translates a variant used as a value: an enum value, or a
// constructor function.
func (fb *funcBuilder) variantValue(e syntax.Expr, v *types.Variant) ir.Value {
	t := fb.typeOf(e)
	if !v.HasData() {
		return fb.emit(&ir.MakeVariant{Variant: v.Index()}, t)
	}
	sig, ok := t.(*types.Signature)
	if !ok {
		return zero(t)
	}
	return &ir.FuncValue{Func: fb.b.ctor(v, sig)}
}

// ctorOf returns the constructor function of variant v of enum type
// enum, which may be an instance of a generic enum type.
func (b *builder) ctorOf(v *types.Variant, enum types.Type) *ir.Func {
	iv := enum.Underlying().(*types.Enum).Variants[v.Index()]
	return b.ctor(v, &types.Signature{Params: &types.Tuple{Vars: iv.Fields()}, Results: types.NewTuple(enum)})
}

// ctor returns the constructor function of a variant with data for the
// enum type given by the result of sig.
func (b *builder) ctor(v *types.Variant, sig *types.Signature) *ir.Func {
	enum := sig.Results.At(0).Type()
	key := enum.String() + "." + v.Name()
	if fn, ok := b.ctors[key]; ok {
		return fn
	}
	fn := ir.NewFunc(key, sig, v.Pos())
	b.ctors[key] = fn
	b.prog.Funcs = append(b.prog.Funcs, fn)
	entry := fn.NewBlock("entry")
	var fields []ir.Value
	for i := range sig.Params.Len() {
		p := &ir.Param{Pname: sig.Params.At(i).Name(), Typ: sig.Params.At(i).Type()}
		fn.Params = append(fn.Params, p)
		fields = append(fields, p)
	}
	mv := &ir.MakeVariant{Variant: v.Index(), Fields: fields}
	entry.Emit(mv, enum)
	entry.Emit(&ir.Return{Results: []ir.Value{mv}}, nil)
	return fn
}

// addr returns the address of addressable operand e, or nil.
func (fb *funcBuilder) addr(e syntax.Expr) ir.Value {
	switch e := unparen(e).(type) {
	case *syntax.Name:
		if v, ok := fb.info.Uses[e].(*types.Var); ok {
			return fb.local(v)
		}
		if v, ok := fb.info.Defs[e].(*types.Var); ok {
			return fb.local(v)
		}
	case *syntax.SelectorExpr:
		sel := fb.info.Selections[e]
		if sel == nil {
			if v, ok := fb.info.Uses[e.Sel].(*types.Var); ok {
				return fb.local(v) // a qualified hardware mapping
			}
			return nil
		}
		if sel.Kind != types.FieldVal {
			return nil
		}
		base := fb.addr(e.X)
		if base == nil {
			return nil
		}
		return fb.fieldPath(base, sel.Index, true)
	case *syntax.IndexExpr:
		xt := fb.typeOf(e.X)
		switch xt.Underlying().(type) {
		case *types.Array, *types.Slice:
			base := fb.addr(e.X)
			if base == nil {
				return nil
			}
			i := fb.expr(e.Indices[0])
			return fb.emit(&ir.IndexAddr{X: base, Index: i, Proven: fb.info.Proven[e]}, &ir.Pointer{Elem: elemType(xt)})
		}
	}
	return nil
}

func elemType(t types.Type) types.Type {
	switch u := t.Underlying().(type) {
	case *types.Array:
		return u.Elem
	case *types.Slice:
		return u.Elem
	case *types.Map:
		return u.Elem
	}
	return types.Typ[types.Invalid]
}

// fieldPath follows a path of field indices from base: addresses if
// addrs is set, values otherwise.
func (fb *funcBuilder) fieldPath(base ir.Value, index []int, addrs bool) ir.Value {
	for _, i := range index {
		var st types.Type
		if addrs {
			st = base.Type().(*ir.Pointer).Elem
		} else {
			st = base.Type()
		}
		s := st.Underlying().(*types.Struct)
		ft := s.Fields[i].Type()
		if addrs {
			base = fb.emit(&ir.FieldAddr{X: base, Field: i}, &ir.Pointer{Elem: ft})
		} else {
			base = fb.emit(&ir.Field{X: base, Field: i}, ft)
		}
	}
	return base
}

func (fb *funcBuilder) selector(e *syntax.SelectorExpr) ir.Value {
	sel := fb.info.Selections[e]
	if sel == nil {
		// A qualified identifier; a variant has its type at the selector.
		if v, ok := fb.info.Uses[e.Sel].(*types.Variant); ok {
			return fb.variantValue(e, v)
		}
		return fb.name(e.Sel)
	}
	switch sel.Kind {
	case types.FieldVal:
		return fb.fieldPath(fb.expr(e.X), sel.Index, false)
	case types.MethodExpr:
		return &ir.FuncValue{Func: fb.methodExpr(e, sel)}
	}
	// A method value: a closure over a copy of the receiver.
	return fb.methodValue(e, sel)
}

func (fb *funcBuilder) index(e *syntax.IndexExpr) ir.Value {
	xt := fb.typeOf(e.X)
	if _, ok := xt.(*types.Signature); ok {
		// An instantiation of a generic function: F[int].
		return fb.expr(e.X)
	}
	t := fb.typeOf(e)
	if m, ok := xt.Underlying().(*types.Map); ok {
		x := fb.expr(e.X)
		k := fb.convert(fb.expr(e.Indices[0]), m.Key)
		return fb.emit(&ir.Lookup{X: x, Key: k}, t)
	}
	x := fb.expr(e.X)
	i := fb.expr(e.Indices[0])
	return fb.emit(&ir.Index{X: x, Index: i, Proven: fb.info.Proven[e]}, t)
}

func (fb *funcBuilder) slice(e *syntax.SliceExpr) ir.Value {
	var x ir.Value
	if _, ok := fb.typeOf(e.X).Underlying().(*types.Array); ok {
		x = fb.addr(e.X)
		if x == nil {
			cell := fb.alloc(fb.typeOf(e.X), "array")
			fb.store(cell, fb.expr(e.X))
			x = cell
		}
	} else {
		x = fb.expr(e.X)
	}
	opt := func(e syntax.Expr) ir.Value {
		if e == nil {
			return nil
		}
		return fb.expr(e)
	}
	toLen := false
	if s, ok := fb.typeOf(e).Underlying().(*types.Slice); ok {
		toLen = !types.HasZero(s.Elem)
	}
	return fb.emit(&ir.Slice{X: x, Low: opt(e.Low), High: opt(e.High), Max: opt(e.Max), Proven: fb.info.Proven[e], ToLen: toLen}, fb.typeOf(e))
}

func (fb *funcBuilder) unary(e *syntax.UnaryExpr) ir.Value {
	if e.Op == syntax.Arrow {
		fb.b.errorf(e.Pos(), 30, "channel receive")
		return zero(fb.typeOf(e))
	}
	x := fb.expr(e.X)
	return fb.emit(&ir.UnOp{Op: e.Op, X: x, Proven: fb.info.Proven[e]}, fb.typeOf(e))
}

func (fb *funcBuilder) binary(e *syntax.BinaryExpr) ir.Value {
	t := fb.typeOf(e)
	if e.Op == syntax.LAnd || e.Op == syntax.LOr {
		return fb.logical(e, t)
	}
	xt, yt := fb.typeOf(e.X), fb.typeOf(e.Y)
	x, y := fb.expr(e.X), fb.expr(e.Y)
	switch e.Op {
	case syntax.Shl, syntax.Shr:
		x = fb.convert(x, t)
	case syntax.Eql, syntax.Neq, syntax.Lss, syntax.Leq, syntax.Gtr, syntax.Geq:
		// The operands of a comparison get a common type: an untyped
		// constant or nil the type of the other operand, a concrete value
		// the interface type of the other.
		switch {
		case types.IsInterface(xt) && !types.IsInterface(yt):
			y = fb.convert(y, xt)
		case types.IsInterface(yt) && !types.IsInterface(xt):
			x = fb.convert(x, yt)
		case isConst(x) && !isConst(y), uninstantiated(xt):
			x = fb.convert(x, yt)
		case isConst(y), uninstantiated(yt):
			y = fb.convert(y, xt)
		}
	default:
		x, y = fb.convert(x, t), fb.convert(y, t)
	}
	return fb.emit(&ir.BinOp{Op: e.Op, X: x, Y: y, Proven: fb.info.Proven[e]}, t)
}

// uninstantiated reports whether t is a generic type without type
// arguments: the type of a variant without data of a generic enum type
// that the context instantiates, such as None in o == None.
func uninstantiated(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && len(n.TypeParamList()) > 0 && len(n.TypeArgs()) == 0
}

func isConst(v ir.Value) bool {
	_, ok := v.(*ir.Const)
	return ok
}

// logical translates && and || with short-circuit evaluation.
func (fb *funcBuilder) logical(e *syntax.BinaryExpr, t types.Type) ir.Value {
	cell := fb.alloc(t, "cond")
	x := fb.expr(e.X)
	fb.store(cell, x)
	rhs, done := fb.newBlock("rhs"), fb.newBlock("done")
	if e.Op == syntax.LAnd {
		fb.branch(x, rhs, done)
	} else {
		fb.branch(x, done, rhs)
	}
	fb.cur = rhs
	fb.store(cell, fb.expr(e.Y))
	fb.jump(done)
	fb.cur = done
	return fb.load(cell)
}

// convert converts v to type t where the checker accepts an implicit
// conversion: a concrete value to an interface, a constant to its final
// type, a value to a type with the same underlying type.
func (fb *funcBuilder) convert(v ir.Value, t types.Type) ir.Value {
	if t == nil {
		return v
	}
	vt := v.Type()
	if types.Identical(vt, t) {
		return v
	}
	if c, ok := v.(*ir.Const); ok && c.Computed != nil {
		if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&types.IsTerminated != 0 {
			// A []u8 constant becomes a terminated string constant; the
			// terminator is appended where the bytes are stored
			// (type.string.terminator-const).
			if comp, ok := c.Computed.(*types.Composite); ok {
				bs := make([]byte, len(comp.Elems))
				for i, x := range comp.Elems {
					n, _ := constant.Int64Val(x.(constant.Value))
					bs[i] = byte(n)
				}
				return &ir.Const{Typ: t, Val: constant.MakeString(string(bs))}
			}
		}
	}
	if c, ok := v.(*ir.Const); ok && c.Computed == nil {
		if c.Val == nil {
			return zero(t)
		}
		if _, isBasic := t.Underlying().(*types.Basic); isBasic {
			return constant_(t, c.Val)
		}
		if types.IsInterface(t) {
			fb.b.addMethodSet(fb.info, c.Typ)
			return fb.emit(&ir.MakeInterface{X: c}, t)
		}
	}
	if types.IsInterface(t) && !types.IsInterface(vt) {
		fb.b.addMethodSet(fb.info, vt)
		return fb.emit(&ir.MakeInterface{X: v}, t)
	}
	if mv, ok := v.(*ir.MakeVariant); ok {
		// A variant value of a generic enum type gets the instance.
		mv.SetType(t)
		return mv
	}
	return fb.emit(&ir.Convert{X: v}, t)
}

// addMethodSet records the methods of a type converted to an interface.
func (b *builder) addMethodSet(info *types.Info, t types.Type) {
	if b.prog.Methods(t) != nil {
		return
	}
	ms := &ir.MethodSet{Type: t, Methods: map[string]*ir.Func{}}
	b.prog.MethodSets = append(b.prog.MethodSets, ms)
	for _, m := range info.MethodSet(t) {
		if m.Decl == nil {
			continue // a predeclared method, such as Option.Get
		}
		ms.Methods[m.Name()] = b.method(info, t, m)
	}
}

// method returns the IR function of method m of type t, which may be
// promoted through embedded fields: a wrapper then selects the field.
func (b *builder) method(info *types.Info, t types.Type, m *types.Func) *ir.Func {
	_, index := info.LookupFieldOrMethod(t, m.Name())
	recv := t
	if len(index) > 0 {
		// Promoted: a wrapper that selects the embedded field.
		key := t.String() + "." + m.Name() + "$promoted"
		if fn, ok := b.funcs[key]; ok {
			return fn
		}
		sig := m.Signature()
		wsig := &types.Signature{Params: sig.Params, Results: sig.Results, Variadic: sig.Variadic}
		fn := ir.NewFunc(key, wsig, m.Pos())
		b.funcs[key] = fn
		b.prog.Funcs = append(b.prog.Funcs, fn)
		fb := b.newFuncBuilder(info, fn, nil, nil)
		rp := &ir.Param{Pname: "recv", Typ: recv}
		fn.Params = append(fn.Params, rp)
		var args []ir.Value
		var x ir.Value = rp
		inner := t
		for _, i := range index {
			s := inner.Underlying().(*types.Struct)
			inner = s.Fields[i].Type()
			x = fb.emit(&ir.Field{X: x, Field: i}, inner)
		}
		args = append(args, x)
		for i := range sig.Params.Len() {
			p := &ir.Param{Pname: sig.Params.At(i).Name(), Typ: sig.Params.At(i).Type()}
			fn.Params = append(fn.Params, p)
			args = append(args, p)
		}
		target := b.concreteMethod(info, inner, m)
		var res ir.Value
		if target != nil {
			res = fb.emit(&ir.Call{Callee: &ir.FuncValue{Func: target}, Args: args}, resultType(sig))
		} else {
			res = fb.emit(&ir.Call{Callee: x, Method: m.Name(), Args: args[1:]}, resultType(sig))
		}
		fb.returnCall(res, sig)
		return fn
	}
	return b.concreteMethod(info, t, m)
}

// concreteMethod returns the IR function of the method of the defined
// type t, or nil if t is an interface.
func (b *builder) concreteMethod(info *types.Info, t types.Type, m *types.Func) *ir.Func {
	if types.IsInterface(t) {
		return nil
	}
	var targs []types.Type
	if n, ok := t.(*types.Named); ok {
		targs = n.TypeArgs()
	}
	return b.function(m, targs)
}

func resultType(sig *types.Signature) types.Type {
	switch sig.Results.Len() {
	case 0:
		return &types.Tuple{}
	case 1:
		return sig.Results.At(0).Type()
	}
	return sig.Results
}

// returnCall returns the results of a call.
func (fb *funcBuilder) returnCall(res ir.Value, sig *types.Signature) {
	switch sig.Results.Len() {
	case 0:
		fb.emitStmt(&ir.Return{})
	case 1:
		fb.emitStmt(&ir.Return{Results: []ir.Value{res}})
	default:
		var rs []ir.Value
		for i := range sig.Results.Len() {
			rs = append(rs, fb.emit(&ir.Extract{Tuple: res, Index: i}, sig.Results.At(i).Type()))
		}
		fb.emitStmt(&ir.Return{Results: rs})
	}
}

// methodValue translates x.M used as a value: a closure that calls the
// method on a copy of x.
func (fb *funcBuilder) methodValue(e *syntax.SelectorExpr, sel *types.Selection) ir.Value {
	recv := fb.copy(fb.expr(e.X))
	m := sel.Obj.(*types.Func)
	sig := fb.typeOf(e).(*types.Signature)
	fb.b.nlits++
	fn := ir.NewFunc(fb.fn.Name+"$bound"+itoa(fb.b.nlits), sig, e.Pos())
	fb.b.prog.Funcs = append(fb.b.prog.Funcs, fn)
	rt := recv.Type()
	fv := &ir.FreeVar{Vname: "recv", Typ: rt}
	fn.FreeVars = []*ir.FreeVar{fv}
	wb := fb.b.newFuncBuilder(fb.info, fn, nil, nil)
	var args []ir.Value
	for i := range sig.Params.Len() {
		p := &ir.Param{Pname: sig.Params.At(i).Name(), Typ: sig.Params.At(i).Type()}
		fn.Params = append(fn.Params, p)
		args = append(args, p)
	}
	var res ir.Value
	if types.IsInterface(rt) {
		res = wb.emit(&ir.Call{Callee: fv, Method: m.Name(), Args: args}, resultType(sig))
	} else {
		target := fb.b.method(fb.info, rt, m)
		res = wb.emit(&ir.Call{Callee: &ir.FuncValue{Func: target}, Args: append([]ir.Value{fv}, args...)}, resultType(sig))
	}
	wb.returnCall(res, sig)
	return fb.emit(&ir.MakeClosure{Fn: fn, Bindings: []ir.Value{recv}}, sig)
}

// methodExpr returns the function of a method expression T.M.
func (fb *funcBuilder) methodExpr(e *syntax.SelectorExpr, sel *types.Selection) *ir.Func {
	t := fb.typ(sel.Recv)
	m := sel.Obj.(*types.Func)
	if types.IsInterface(t) {
		sig := fb.typeOf(e).(*types.Signature)
		fb.b.nlits++
		fn := ir.NewFunc(fb.fn.Name+"$mexpr"+itoa(fb.b.nlits), sig, e.Pos())
		fb.b.prog.Funcs = append(fb.b.prog.Funcs, fn)
		wb := fb.b.newFuncBuilder(fb.info, fn, nil, nil)
		var args []ir.Value
		for i := range sig.Params.Len() {
			p := &ir.Param{Pname: "p" + itoa(i), Typ: sig.Params.At(i).Type()}
			fn.Params = append(fn.Params, p)
			args = append(args, p)
		}
		msig := m.Signature()
		res := wb.emit(&ir.Call{Callee: args[0], Method: m.Name(), Args: args[1:]}, resultType(msig))
		wb.returnCall(res, msig)
		return fn
	}
	return fb.b.method(fb.info, t, m)
}

func itoa(n int) string {
	return constant.MakeInt64(int64(n)).String()
}

// compositeLit translates a composite literal of type t.
func (fb *funcBuilder) compositeLit(e *syntax.CompositeLit, t types.Type) ir.Value {
	if e.Type != nil {
		if v := fb.variantOf(e.Type); v != nil {
			return fb.variantLit(e, v, t)
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		fields := make([]ir.Value, len(u.Fields))
		for i, f := range u.Fields {
			fields[i] = zero(f.Type())
		}
		for i, el := range e.Elts {
			idx := i
			if kv, ok := el.(*syntax.KeyValueExpr); ok {
				for j, f := range u.Fields {
					if f.Name() == kv.Key.(*syntax.Name).Name {
						idx = j
					}
				}
				el = kv.Value
			}
			fields[idx] = fb.element(el, u.Fields[idx].Type())
		}
		return fb.emit(&ir.MakeStruct{Fields: fields}, t)
	case *types.Array:
		elems := make([]ir.Value, u.Len)
		for i := range elems {
			elems[i] = zero(u.Elem)
		}
		fb.indexedElems(e, u.Elem, func(i int64, v ir.Value) { elems[i] = v })
		return fb.emit(&ir.MakeArray{Elems: elems}, t)
	case *types.Slice:
		var elems []ir.Value
		fb.indexedElems(e, u.Elem, func(i int64, v ir.Value) {
			for int64(len(elems)) <= i {
				elems = append(elems, zero(u.Elem))
			}
			elems[i] = v
		})
		return fb.emit(&ir.MakeArray{Elems: elems}, t)
	case *types.Map:
		cell := fb.alloc(t, "map")
		fb.store(cell, fb.emit(&ir.MakeMap{}, t))
		for _, el := range e.Elts {
			kv := el.(*syntax.KeyValueExpr)
			k := fb.element(kv.Key, u.Key)
			v := fb.element(kv.Value, u.Elem)
			fb.emitStmt(&ir.MapUpdate{Map: cell, Key: k, Value: v})
		}
		return fb.load(cell)
	}
	fb.b.errorf(e.Pos(), 6, "composite literal of type %s", t)
	return zero(t)
}

// element translates an element of a composite literal of type t.
func (fb *funcBuilder) element(e syntax.Expr, t types.Type) ir.Value {
	if cl, ok := e.(*syntax.CompositeLit); ok && cl.Type == nil {
		return fb.compositeLit(cl, t)
	}
	return fb.convert(fb.copy(fb.expr(e)), t)
}

func (fb *funcBuilder) indexedElems(e *syntax.CompositeLit, elem types.Type, set func(int64, ir.Value)) {
	var next int64
	for _, el := range e.Elts {
		if kv, ok := el.(*syntax.KeyValueExpr); ok {
			if c := fb.constOf(kv.Key); c != nil {
				next, _ = constant.Int64Val(c.(*ir.Const).Val)
			}
			el = kv.Value
		}
		set(next, fb.element(el, elem))
		next++
	}
}

// variantOf returns the variant that e names, or nil.
func (fb *funcBuilder) variantOf(e syntax.Expr) *types.Variant {
	switch e := unparen(e).(type) {
	case *syntax.Name:
		v, _ := fb.info.Uses[e].(*types.Variant)
		return v
	case *syntax.SelectorExpr:
		v, _ := fb.info.Uses[e.Sel].(*types.Variant)
		return v
	}
	return nil
}

func (fb *funcBuilder) variantLit(e *syntax.CompositeLit, v *types.Variant, t types.Type) ir.Value {
	enum, _ := t.Underlying().(*types.Enum)
	if enum == nil {
		return zero(t)
	}
	fv := enum.Variants[v.Index()].Fields()
	fields := make([]ir.Value, len(fv))
	for i, f := range fv {
		fields[i] = zero(f.Type())
	}
	for i, el := range e.Elts {
		idx := i
		if kv, ok := el.(*syntax.KeyValueExpr); ok {
			for j, f := range fv {
				if f.Name() == kv.Key.(*syntax.Name).Name {
					idx = j
				}
			}
			el = kv.Value
		}
		fields[idx] = fb.element(el, fv[idx].Type())
	}
	return fb.emit(&ir.MakeVariant{Variant: v.Index(), Fields: fields}, t)
}

// funcLit translates a function literal to a closure.
func (fb *funcBuilder) funcLit(e *syntax.FuncLit) ir.Value {
	sig := fb.typeOf(e).(*types.Signature)
	fb.b.nlits++
	fn := ir.NewFunc(fb.fn.Name+"$lit"+itoa(fb.b.nlits), sig, e.Pos())
	fb.b.prog.Funcs = append(fb.b.prog.Funcs, fn)
	lb := fb.b.newFuncBuilder(fb.info, fn, fb.tps, fb.targs)
	lb.parent = fb
	lb.free = map[*types.Var]*ir.FreeVar{}
	osig := fb.info.Types[e].Type.(*types.Signature)
	for i, v := range osig.Params.Vars {
		lb.param(v, sig.Params.At(i).Type())
	}
	for i, v := range osig.Results.Vars {
		if v.Name() != "" {
			cell := lb.alloc(sig.Results.At(i).Type(), v.Name())
			lb.vars[v] = cell
			lb.results = append(lb.results, cell)
		}
	}
	lb.body(e.Body.List)
	lb.finish()
	return fb.closure(fn, lb.captured, false)
}

// closure makes the closure of fn over the captured variables: their
// addresses if byRef or for inout variables, copies of their values
// otherwise.
func (fb *funcBuilder) closure(fn *ir.Func, captured []*types.Var, byRef bool) ir.Value {
	var bindings []ir.Value
	for _, v := range captured {
		a := fb.local(v)
		if v.Inout() || byRef {
			bindings = append(bindings, a)
		} else {
			bindings = append(bindings, fb.copy(fb.load(a)))
		}
	}
	return fb.emit(&ir.MakeClosure{Fn: fn, Bindings: bindings}, fn.Sig)
}
