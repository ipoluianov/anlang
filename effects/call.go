package effects

import (
	"an/syntax"
	"an/types"
)

func (w *walker) call(n *syntax.CallExpr) {
	a, fn := w.a, w.fn
	tv := a.info.Types[n.Fun]
	switch {
	case tv.IsType():
		w.conversion(n)
		return
	case tv.IsBuiltin():
		w.builtin(n)
		return
	}
	if inst := a.info.TemplateCalls[n]; inst != nil && inst.Sig != nil {
		w.argContexts(n, inst.Sig)
		w.static(n, instKey(a.info, inst), inst.Sig, nil, nil)
		return
	}
	sig, _ := coreOf(a.typeOf(n.Fun)).(*types.Signature)
	if sig == nil {
		return
	}
	w.argContexts(n, sig)
	pos := n.Lparen
	fun := unparen(n.Fun)
	var instID *syntax.Name
	if ix, ok := fun.(*syntax.IndexExpr); ok {
		fun = unparen(ix.X)
	}
	switch f := fun.(type) {
	case *syntax.Name:
		instID = f
		switch obj := a.info.Uses[f].(type) {
		case *types.Func:
			w.static(n, funcKey(obj), obj.Signature(), nil, a.typeArgs(instID))
			return
		case *types.Var:
			if fn.paramOwner(obj) != nil {
				fn.prefs = append(fn.prefs, prefFact{kind: ParamCall, v: obj, origin: origin(pos, "call of "+f.Name)})
				return
			}
		case *types.Variant:
			return
		}
	case *syntax.SelectorExpr:
		sel := a.info.Selections[f]
		if sel == nil {
			switch obj := a.info.Uses[f.Sel].(type) {
			case *types.Func:
				w.static(n, funcKey(obj), obj.Signature(), nil, a.typeArgs(f.Sel))
				return
			case *types.Variant:
				return
			}
			break
		}
		m, isMethod := sel.Obj.(*types.Func)
		switch {
		case sel.Kind == types.MethodVal && isMethod:
			recv := sel.Recv
			switch {
			case types.IsTypeParam(recv):
				fn.prefs = append(fn.prefs, prefFact{kind: TypeParamMethod, tp: recv.(*types.TypeParam), method: m.Name(), origin: origin(pos, "call of "+m.Name())})
			case types.IsInterface(recv):
				if v := w.paramVar(f.X); v != nil {
					fn.prefs = append(fn.prefs, prefFact{kind: ParamMethod, v: v, method: m.Name(), origin: origin(pos, "call of "+m.Name())})
				} else {
					fn.open = append(fn.open, refFact{Ref{Kind: OpenMethod, Method: m.Name(), Type: typeKey(recv)}, origin(pos, "call of "+m.Name())})
				}
			default:
				if m.Signature().Recv != nil && m.Signature().Recv.Inout() {
					w.write(f.X, pos)
				}
				recvArg := w.classify(f.X)
				w.static(n, funcKey(m), m.Signature(), &recvArg, recvTypeArgs(a, recv))
			}
			return
		case sel.Kind == types.MethodExpr && isMethod:
			// T.M(x, args): the receiver is the first argument.
			if len(n.Args) > 0 {
				recvArg := w.classify(n.Args[0])
				rest := &syntax.CallExpr{Fun: n.Fun, Lparen: n.Lparen, Args: n.Args[1:], Ellipsis: n.Ellipsis, Rparen: n.Rparen}
				w.static(rest, funcKey(m), m.Signature(), &recvArg, recvTypeArgs(a, sel.Recv))
			}
			return
		}
	case *syntax.FuncLit:
		// The literal gets its key when the walker reaches it, after the
		// call.
		fn.calls = append(fn.calls, &callFact{pos: pos, lit: f, args: w.argInfos(n, sig), origin: origin(pos, "")})
		return
	}
	fn.open = append(fn.open, refFact{Ref{Kind: OpenFunc, Type: sigKey(sig)}, origin(pos, "call of a function value")})
}

// paramVar returns the parameter of the function or an enclosing one
// that e denotes, or nil.
func (w *walker) paramVar(e syntax.Expr) *types.Var {
	id, ok := unparen(e).(*syntax.Name)
	if !ok {
		return nil
	}
	v, ok := w.a.info.Uses[id].(*types.Var)
	if !ok || w.fn.paramOwner(v) == nil {
		return nil
	}
	return v
}

func (a *analyzer) typeArgs(id *syntax.Name) []types.Type {
	if inst, ok := a.info.Instances[id]; ok {
		return inst.TypeArgs
	}
	return nil
}

// recvTypeArgs returns the type arguments of the receiver type of a
// method of a generic type.
func recvTypeArgs(a *analyzer, t types.Type) []types.Type {
	if n, ok := t.(*types.Named); ok {
		return n.TypeArgs()
	}
	return nil
}

// static records a static call (eff.infer.rules 2).
func (w *walker) static(n *syntax.CallExpr, key string, sig *types.Signature, recv *argInfo, targs []types.Type) {
	c := &callFact{pos: n.Lparen, callee: key, args: w.argInfos(n, sig), origin: origin(n.Lparen, "")}
	if recv != nil {
		c.args[-1] = *recv
	}
	for _, t := range targs {
		c.targs = append(c.targs, w.classifyType(t))
	}
	w.fn.calls = append(w.fn.calls, c)
}

func (w *walker) argInfos(n *syntax.CallExpr, sig *types.Signature) map[int]argInfo {
	args := map[int]argInfo{}
	if len(n.Args) == 1 && sig.Params.Len() > 1 {
		return args // a call with several results supplies the arguments
	}
	for i, e := range n.Args {
		if i < sig.Params.Len() && !(sig.Variadic && i >= sig.Params.Len()-1) {
			args[i] = w.classify(e)
		}
	}
	return args
}

// argContexts handles the arguments as uses of values of the parameter
// types, and inout arguments as writes.
func (w *walker) argContexts(n *syntax.CallExpr, sig *types.Signature) {
	np := sig.Params.Len()
	if len(n.Args) == 1 && np > 1 {
		return
	}
	for i, e := range n.Args {
		var pt types.Type
		var inout bool
		switch {
		case sig.Variadic && i >= np-1 && !n.Ellipsis.IsValid():
			pt = sig.Params.At(np - 1).Type().Underlying().(*types.Slice).Elem
		case i < np:
			pt, inout = sig.Params.At(i).Type(), sig.Params.At(i).Inout()
		}
		if inout {
			w.write(e, e.Pos())
			continue
		}
		w.conv(e, pt)
	}
}

// classify describes an argument for the substitution of parametric
// references.
func (w *walker) classify(e syntax.Expr) argInfo {
	a := w.a
	e = unparen(e)
	switch x := e.(type) {
	case *syntax.Name:
		switch obj := a.info.Uses[x].(type) {
		case *types.Var:
			if w.fn.paramOwner(obj) != nil && (types.IsInterface(obj.Type()) || isSignature(obj.Type()) || types.IsTypeParam(obj.Type())) {
				return argInfo{kind: argParam, v: obj}
			}
		case *types.Func:
			return argInfo{kind: argFuncs, keys: []string{funcKey(obj)}}
		}
	case *syntax.FuncLit:
		// Its key is assigned when the walker reaches it, after the call.
		return argInfo{kind: argFuncs, lit: x}
	case *syntax.SelectorExpr:
		if sel := a.info.Selections[x]; sel != nil {
			if m, ok := sel.Obj.(*types.Func); ok {
				switch {
				case sel.Kind == types.MethodVal && types.IsInterface(sel.Recv):
					if v := w.paramVar(x.X); v != nil {
						return argInfo{kind: argParamMethod, v: v, method: m.Name()}
					}
					return argInfo{kind: argOpenFunc, key: sigKey(a.typeOf(x))}
				case sel.Kind == types.MethodVal && types.IsTypeParam(sel.Recv):
				default:
					return argInfo{kind: argFuncs, keys: []string{funcKey(m)}}
				}
			}
		} else if f, ok := a.info.Uses[x.Sel].(*types.Func); ok {
			return argInfo{kind: argFuncs, keys: []string{funcKey(f)}}
		}
	}
	t := a.typeOf(e)
	if t == nil {
		return argInfo{}
	}
	switch {
	case types.IsTypeParam(t):
		return argInfo{kind: argTypeParam, tp: t.(*types.TypeParam)}
	case types.IsInterface(t):
		return argInfo{kind: argOpenIface, key: typeKey(t)}
	case isSignature(t):
		return argInfo{kind: argOpenFunc, key: sigKey(t)}
	}
	return argInfo{kind: argType, typ: t}
}

func (w *walker) classifyType(t types.Type) argInfo {
	switch {
	case types.IsTypeParam(t):
		return argInfo{kind: argTypeParam, tp: t.(*types.TypeParam)}
	case types.IsInterface(t):
		return argInfo{kind: argOpenIface, key: typeKey(t), typ: t}
	}
	return argInfo{kind: argType, typ: t}
}

func (w *walker) conversion(n *syntax.CallExpr) {
	a, fn := w.a, w.fn
	if len(n.Args) != 1 {
		return
	}
	to, from := a.typeOf(n.Fun), a.typeOf(n.Args[0])
	if to == nil || from == nil {
		return
	}
	w.conv(n.Args[0], to)
	if a.isConst(n) {
		return
	}
	tb, toBasic := coreOf(to).(*types.Basic)
	fb, fromBasic := coreOf(from).(*types.Basic)
	_, fromSlice := coreOf(from).(*types.Slice)
	_, toSlice := coreOf(to).(*types.Slice)
	_, toArray := coreOf(to).(*types.Array)
	switch {
	case toBasic && fromBasic && tb.Info()&types.IsInteger != 0 && fb.Info()&types.IsFloat != 0:
		fn.source(Panic, n.Lparen, "conversion of a floating-point value to an integer type")
	case toBasic && tb.Info()&types.IsTerminated != 0:
		fn.source(Panic, n.Lparen, "conversion to "+to.String())
		fn.source(Alloc, n.Lparen, "conversion to "+to.String())
	case fromSlice && toArray:
		fn.source(Panic, n.Lparen, "conversion of a slice to an array")
	case toBasic && tb.Info()&types.IsString != 0 && (fromSlice || fromBasic && fb.Info()&types.IsInteger != 0):
		fn.source(Alloc, n.Lparen, "conversion to a string")
	case toSlice && fromBasic && fb.Info()&types.IsString != 0 && !a.isConst(n.Args[0]):
		// The conversion of a constant string is constant
		// (eff.source.alloc-ops).
		fn.source(Alloc, n.Lparen, "conversion of a string to a slice")
	}
}

func (w *walker) builtin(n *syntax.CallExpr) {
	a, fn := w.a, w.fn
	name := a.builtinName(n.Fun)
	pos := n.Lparen
	switch name {
	case "append":
		if len(n.Args) > 1 {
			fn.source(Alloc, pos, "append")
			if st, ok := coreOf(a.typeOf(n.Args[0])).(*types.Slice); ok && !n.Ellipsis.IsValid() {
				for _, e := range n.Args[1:] {
					w.conv(e, st.Elem)
				}
			}
		}
	case "make":
		fn.source(Alloc, pos, "make")
		for _, e := range n.Args[1:] {
			if !a.isConst(e) {
				fn.source(Panic, pos, "make with a length or capacity that is not constant")
				break
			}
		}
	case "copy", "clear":
		fn.source(Alloc, pos, name)
	case "delete":
		fn.source(Alloc, pos, "delete")
		if m, ok := coreOf(a.typeOf(n.Args[0])).(*types.Map); ok && hasInterface(m.Key) {
			fn.source(Panic, pos, "delete with an interface key")
		}
	case "panic":
		fn.source(Panic, pos, "panic")
	case "close":
		fn.source(Panic, pos, "close")
	case "print", "println":
		fn.source(Console, pos, name)
	case "fmt.Print", "fmt.Println", "fmt.Printf", "fmt.Sprint", "fmt.Sprintln", "fmt.Sprintf":
		if name == "fmt.Print" || name == "fmt.Println" || name == "fmt.Printf" {
			// A failed write panics (std.fmt.print).
			fn.source(Console, pos, name)
			fn.source(Panic, pos, name)
		} else {
			fn.source(Alloc, pos, name)
		}
		// The formatting calls the methods Error and String of the
		// arguments.
		var argTypes []types.Type
		for _, e := range n.Args {
			t := a.typeOf(e)
			if tt, ok := t.(*types.TupleType); ok {
				argTypes = append(argTypes, tt.Elems...) // fmt.Print(args...)
			} else if t != nil {
				argTypes = append(argTypes, t)
			}
		}
		for _, t := range argTypes {
			for _, m := range []string{"Error", "String"} {
				switch {
				case types.IsInterface(t):
					for _, im := range methods(t) {
						if im == m {
							fn.open = append(fn.open, refFact{Ref{Kind: OpenMethod, Method: m, Type: typeKey(t)}, origin(pos, name)})
						}
					}
				case types.IsTypeParam(t):
				default:
					if key := a.methodKey(t, m); key != "" {
						fn.adds = append(fn.adds, addFact{key, origin(pos, name)})
					}
				}
			}
		}
	case "gen.ReadFile":
		fn.source(ReadInput, pos, "gen.ReadFile")
	case "console.ReadByte":
		// It waits for input (std.console.read).
		fn.source(Console, pos, name)
		fn.source(Block, pos, name)
	case "effect.Mark":
		if e, ok := a.effectConst(n.Args[0]); ok {
			fn.source(e, pos, "effect.Mark")
		}
	case "effect.Defer":
		for _, arg := range n.Args {
			if e, ok := a.effectConst(arg); ok {
				fn.deferred = append(fn.deferred, effectFact{e, origin(pos, "effect.Defer")})
			}
		}
	}
}
