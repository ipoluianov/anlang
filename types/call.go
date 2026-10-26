package types

import (
	"go/constant"
	"strings"
	"unicode/utf8"

	"an/syntax"
)

func (c *checker) callExpr(x *operand, e *syntax.CallExpr) {
	if c.descCall(x, e) {
		return
	}
	if ix, ok := unparen(e.Fun).(*syntax.IndexExpr); ok && c.isMapCall(e) {
		var f operand
		c.rawExpr(&f, ix.X, nil)
		c.unsafeMap(x, e, ix)
		return
	}
	var fun operand
	savedCallee := c.callee
	c.callee = unparen(e.Fun)
	c.rawExpr(&fun, e.Fun, nil)
	c.callee = savedCallee
	if fun.second {
		if lit, ok := unparen(e.Fun).(*syntax.FuncLit); ok {
			c.secondLits[lit] = true
		}
	}
	x.expr = e
	switch fun.mode {
	case invalid:
		c.useArgs(e.Args)
		x.invalidate()
		return
	case typexpr:
		c.conversionCall(x, e, fun.typ)
		return
	case builtin:
		c.builtinCall(x, e, fun.id)
		return
	}
	c.singleValue(&fun)
	if fun.mode == invalid {
		c.useArgs(e.Args)
		x.invalidate()
		return
	}
	sig, ok := core(fun.typ).(*Signature)
	if !ok {
		c.errorf(e.Pos(), "expr.go", "invalid operation: cannot call non-function %s", &fun)
		c.useArgs(e.Args)
		x.invalidate()
		return
	}
	var args []*operand
	savedOK := c.tupleOK
	c.tupleOK = e.Ellipsis.IsValid() // forwarding a tuple: f(args...) (ct.any.use)
	if len(sig.TypeParams) > 0 {
		args = c.args(e.Args, nil)
	} else {
		args = c.args(e.Args, sig)
	}
	c.tupleOK = savedOK
	for i, a := range args {
		if _, ok := a.typ.(*TupleType); ok && (i != len(args)-1 || !sig.AnyTuple) {
			c.errorf(a.expr.Pos(), "ct.any.use", "a ...any parameter may be passed only as the final argument of a call of a function with a ...any parameter, written with ...")
			x.invalidate()
			return
		}
	}
	if len(sig.TypeParams) > 0 {
		if sig.AnyTuple {
			// The tuple does not take part in inference.
			n := sig.Params.Len() - 1
			fixed := *sig
			fixed.Params = &Tuple{Vars: sig.Params.Vars[:n]}
			fixed.AnyTuple = false
			iargs := args
			if len(iargs) > n {
				iargs = iargs[:n]
			}
			inst := c.inferCall(e, &fixed, fun.targs, iargs)
			if inst == nil {
				x.invalidate()
				return
			}
			full := *inst
			full.Params = &Tuple{Vars: append(append([]*Var(nil), inst.Params.Vars...), sig.Params.Vars[n])}
			full.AnyTuple = true
			sig = &full
		} else if sig = c.inferCall(e, sig, fun.targs, args); sig == nil {
			x.invalidate()
			return
		}
	}
	c.arguments(e, sig, args)
	if sig.AnyTuple {
		c.templateCall(e, sig, args)
	} else if len(c.templates) > 0 {
		c.templateCall(e, sig, args)
	}
	c.inoutOverlap(c.inoutAccesses(e, sig))
	switch sig.Results.Len() {
	case 0:
		x.mode = novalue
	case 1:
		x.mode, x.typ = value, sig.Results.Vars[0].typ
	default:
		x.mode, x.typ = value, sig.Results
	}
}

// useArgs checks arguments of an invalid call for other errors.
func (c *checker) useArgs(args []syntax.Expr) {
	for _, a := range args {
		var x operand
		c.rawExpr(&x, a, nil)
	}
}

// args checks call arguments. A single call argument with several
// results is expanded into one operand per result. The parameter types
// of sig, if given, are hints for generic functions passed as arguments.
func (c *checker) args(args []syntax.Expr, sig *Signature) []*operand {
	if len(args) == 1 {
		x := &operand{}
		c.rawExpr(x, args[0], nil)
		if t, ok := x.typ.(*Tuple); ok && x.mode == value {
			var xs []*operand
			for _, v := range t.Vars {
				xs = append(xs, &operand{mode: value, expr: args[0], typ: v.typ})
			}
			return xs
		}
		if x.mode == value && isGeneric(x.typ) {
			if ts, ok := core(orInvalid(paramHint(sig, 0))).(*Signature); ok {
				c.inferFromTarget(x, ts)
			}
			if isGeneric(x.typ) {
				c.fromOwnConstraints(x)
			}
		}
		c.valueOnly(x)
		return []*operand{x}
	}
	var xs []*operand
	saved := c.inArgs
	c.inArgs = true
	defer func() { c.inArgs = saved }()
	for i, a := range args {
		x := &operand{}
		c.exprHint(x, a, paramHint(sig, i))
		xs = append(xs, x)
	}
	return xs
}

// paramHint returns the type of the i'th parameter of sig, or nil.
func paramHint(sig *Signature, i int) Type {
	if sig == nil {
		return nil
	}
	n := sig.Params.Len()
	switch {
	case sig.Variadic && i >= n-1:
		return sig.Params.Vars[n-1].typ.(*Slice).Elem
	case i < n:
		return sig.Params.Vars[i].typ
	}
	return nil
}

// arguments checks the arguments of a call of a function with signature
// sig.
func (c *checker) arguments(e *syntax.CallExpr, sig *Signature, args []*operand) {
	for _, a := range args {
		if a.mode == invalid {
			return
		}
	}
	name := exprString(e.Fun)
	params := sig.Params.Vars
	nparams := len(params)
	if sig.AnyTuple {
		// The fixed parameters; the other arguments form the tuple.
		if len(args) < nparams-1 {
			c.argCountError(e, sig, args)
			return
		}
		for i, a := range args {
			if i < nparams-1 {
				c.paramArg(a, params[i], "argument to "+name)
			}
		}
		return
	}
	if e.Ellipsis.IsValid() {
		if !sig.Variadic {
			c.errorf(e.Ellipsis, "expr.go", "have ... argument in call to non-variadic %s", name)
			return
		}
		if len(args) != nparams {
			c.argCountError(e, sig, args)
			return
		}
		for i, a := range args {
			c.paramArg(a, params[i], "argument to "+name)
		}
		return
	}
	if sig.Variadic {
		if len(args) < nparams-1 {
			c.argCountError(e, sig, args)
			return
		}
		for i, a := range args {
			if i < nparams-1 {
				c.paramArg(a, params[i], "argument to "+name)
				continue
			}
			c.assignment(a, params[nparams-1].typ.(*Slice).Elem, "argument to "+name)
		}
		return
	}
	if len(args) != nparams {
		c.argCountError(e, sig, args)
		return
	}
	for i, a := range args {
		c.paramArg(a, params[i], "argument to "+name)
	}
}

// paramArg checks the argument x for parameter p.
func (c *checker) paramArg(x *operand, p *Var, context string) {
	if p.inout {
		c.inoutArg(x, p, context)
		return
	}
	c.assignment(x, p.typ, context)
}

// inoutAccesses returns the operands of the inout accesses of a call:
// the inout receiver of a method and the arguments for inout parameters.
func (c *checker) inoutAccesses(e *syntax.CallExpr, sig *Signature) []syntax.Expr {
	var exprs []syntax.Expr
	if sel, ok := unparen(e.Fun).(*syntax.SelectorExpr); ok {
		if m, ok := c.info.Uses[sel.Sel].(*Func); ok {
			if ms, ok := m.typ.(*Signature); ok && ms.Recv != nil && ms.Recv.inout {
				if tv := c.info.Types[sel.X]; !tv.IsType() {
					exprs = append(exprs, sel.X)
				}
			}
		}
	}
	for i, p := range sig.Params.Vars {
		if p.inout && i < len(e.Args) {
			exprs = append(exprs, e.Args[i])
		}
	}
	return exprs
}

func (c *checker) argCountError(e *syntax.CallExpr, sig *Signature, args []*operand) {
	what := "not enough"
	pos := e.Rparen
	if len(args) > sig.Params.Len() {
		what = "too many"
		pos = args[sig.Params.Len()].expr.Pos()
	}
	var have []string
	for _, a := range args {
		have = append(have, defaultType(a.typ).String())
	}
	var b strings.Builder
	writeTuple(&b, sig.Params, sig.Variadic)
	c.errorf(pos, "expr.go", "%s arguments in call to %s\n\thave (%s)\n\twant %s", what, exprString(e.Fun), strings.Join(have, ", "), b.String())
}

func (c *checker) conversionCall(x *operand, e *syntax.CallExpr, t Type) {
	if len(e.Args) != 1 || e.Ellipsis.IsValid() {
		c.errorf(e.Pos(), "type.conv.go", "conversion to %s needs exactly one argument", t)
		c.useArgs(e.Args)
		x.invalidate()
		return
	}
	c.expr(x, e.Args[0])
	if x.second {
		c.secondClassError(x, "in a conversion")
		return
	}
	if x.mode == invalid || !isValid(t) {
		x.invalidate()
		return
	}
	if ti, ok := t.Underlying().(*Interface); ok && !isTypeParam(t) && ti.isConstraint() {
		c.errorf(e.Fun.Pos(), "type.param.go", "cannot use interface %s in conversion: it contains type constraints or is comparable", t)
		x.invalidate()
		return
	}
	c.conversion(x, t)
	x.expr = e
}

// conversion checks the conversion of x to type t (spec/02-types.md §2.14).
func (c *checker) conversion(x *operand, t Type) {
	if t == EffectType && x.mode != invalid && !Identical(x.typ, EffectType) {
		c.errorf(x.expr.Pos(), "eff.model.identity", "cannot convert %s to effect.Effect: effects are declared, not converted", x)
		x.invalidate()
		return
	}
	tb, tBasic := t.Underlying().(*Basic)
	if x.mode == constant_ && tBasic && isConstType(tb) || x.mode == constant_ && tBasic && tb.info&IsTerminated != 0 {
		c.constConversion(x, t, tb)
		return
	}
	if isUntyped(x.typ) {
		switch {
		case x.isNil():
			if !hasNil(t) {
				c.errorf(x.expr.Pos(), "type.nil.types", "cannot convert nil to type %s", t)
				x.invalidate()
				return
			}
			c.updateExprType(x.expr, t, true)
			x.mode, x.typ, x.val = value, t, nil
			return
		case tBasic && (isNumeric(x.typ) && tb.info&(IsNumeric|IsString) != 0 || isBoolean(x.typ) && isBoolean(tb)):
			// A non-constant untyped value: a comparison or a shift. A
			// shifted constant gets type t, which must be an integer type.
			c.updateExprType(x.expr, t, true)
			x.mode, x.typ, x.val = value, t, nil
			return
		}
		c.defaultUntyped(x)
		if x.mode == invalid {
			return
		}
	}
	rule, ok := c.convertible(x.typ, t)
	why := ""
	if !ok && rule == "type.conv.int-narrow" {
		ok, why = c.provenFits(x.expr, t)
		if !ok && c.conf.DeferNarrowing && c.fn != nil {
			// The facts of the program point may prove it.
			c.info.Narrowings[x.expr] = t
			ok = true
		}
	}
	if !ok {
		switch rule {
		case "type.conv.int-narrow":
			if why != "" {
				why = ", for example when " + why
			}
			c.errorf(x.expr.Pos(), rule, "cannot convert %s to type %s: the value may not fit%s, and it is not proven to (use conv.Narrow[%s] to get (%s, bool))", x, t, why, t, t)
		default:
			c.errorf(x.expr.Pos(), rule, "cannot convert %s to type %s", x, t)
		}
		x.invalidate()
		return
	}
	x.mode, x.typ, x.val = value, t, nil
}

func (c *checker) constConversion(x *operand, t Type, tb *Basic) {
	val := x.val
	switch {
	case tb.info&IsTerminated != 0:
		if !isString(x.typ) {
			c.errorf(x.expr.Pos(), "type.conv.go", "cannot convert %s to type %s", x, t)
			x.invalidate()
			return
		}
		term := "\x00"
		if tb.kind == Dstring {
			term = "$"
		}
		if strings.Contains(constant.StringVal(val), term) {
			c.errorf(x.expr.Pos(), "type.string.terminator-const", "constant %s contains the terminator %q of %s", exprString(x.expr), term, t)
			x.invalidate()
			return
		}
		x.mode, x.typ = value, t
		x.val = nil
		return
	case tb.info&IsString != 0 && isInteger(x.typ):
		// Integer to string yields the UTF-8 encoding of the code point (type.conv.go).
		r := rune(utf8.RuneError)
		if n, ok := int64Val(val); ok && n >= 0 && n <= utf8.MaxRune {
			r = rune(n)
		}
		val = constant.MakeString(string(r))
	case tb.info&IsWrapping != 0 && isInteger(x.typ):
		val = wrapConst(val, tb) // truncates (type.conv.wrap)
	case tb.info&IsNumeric != 0 && isNumeric(x.typ):
		v, ok := c.representable(val, tb)
		if !ok || tb.info&IsInteger != 0 && constant.ToInt(val).Kind() != constant.Int {
			c.errorf(x.expr.Pos(), "type.conv.constant", "cannot convert %s to type %s", x, t)
			x.invalidate()
			return
		}
		val = v
	default:
		if _, ok := c.representable(val, tb); !ok {
			c.errorf(x.expr.Pos(), "type.conv.go", "cannot convert %s to type %s", x, t)
			x.invalidate()
			return
		}
	}
	if isUntyped(x.typ) {
		c.updateExprType(x.expr, defaultType(x.typ), true)
	}
	x.mode, x.typ, x.val = constant_, t, val
}

// convertible reports whether a non-constant value of type v can be
// converted to type t, and otherwise the violated rule.
func (c *checker) convertible(v, t Type) (string, bool) {
	if c.assignableTo(v, t) {
		return "", true
	}
	if isTypeParam(v) || isTypeParam(t) {
		// Every type in one type set must convert to every type in the
		// other (type.param.go).
		vs, ts := typesOf(v), typesOf(t)
		if vs == nil || ts == nil {
			return "type.param.go", false
		}
		for _, a := range vs {
			for _, b := range ts {
				if rule, ok := c.convertible(a, b); !ok {
					return rule, false
				}
			}
		}
		return "", true
	}
	vu, tu := v.Underlying(), t.Underlying()
	if identicalIgnoreTags(vu, tu) {
		return "", true
	}
	if vs, ok := vu.(*Slice); ok {
		// A slice converts to an array; the conversion panics if the
		// slice is shorter than the array (type.conv.go).
		if ta, ok := tu.(*Array); ok && Identical(vs.Elem, ta.Elem) {
			return "", true
		}
	}
	vb, vBasic := vu.(*Basic)
	tb, tBasic := tu.(*Basic)
	switch {
	case vBasic && tBasic && vb.info&IsInteger != 0 && tb.info&IsInteger != 0:
		if tb.info&IsWrapping != 0 || c.widens(vb, tb) {
			// A conversion to a wrapping type truncates (type.conv.wrap).
			return "", true
		}
		return "type.conv.int-narrow", false
	case vBasic && tBasic && vb.info&IsNumeric != 0 && tb.info&IsNumeric != 0:
		return "", true // float-to-integer conversions panic when out of range (type.conv.float-int)
	case tBasic && tb.info&IsString != 0:
		if vBasic && vb.info&IsInteger != 0 {
			return "", true
		}
		if s, ok := vu.(*Slice); ok && isByteOrRune(s.Elem) {
			return "", true
		}
	case vBasic && vb.info&IsString != 0:
		if s, ok := tu.(*Slice); ok && isByteOrRune(s.Elem) {
			return "", true
		}
		if tBasic && tb.info&IsTerminated != 0 {
			return "", true // copies and panics on an embedded terminator (type.string.terminator-conv)
		}
	case tBasic && tb.info&IsTerminated != 0:
		if s, ok := vu.(*Slice); ok && Identical(s.Elem, Typ[U8]) {
			return "", true
		}
	}
	return "type.conv.go", false
}

// isByteOrRune reports whether t is u8 or i32, the element types of the
// slices that convert to and from strings.
func isByteOrRune(t Type) bool {
	b, ok := t.Underlying().(*Basic)
	return ok && (b.kind == U8 || b.kind == I32)
}

// widens reports whether every value of integer type from is
// representable in integer type to (type.conv.int-widen).
func (c *checker) widens(from, to *Basic) bool {
	fb, fs := c.intRange(from)
	tb, ts := c.intRange(to)
	switch {
	case fs == ts:
		return tb >= fb
	case !fs && ts:
		return tb > fb
	}
	return false
}

// builtinCall checks a call of a built-in function. As in Go, a single
// argument that is a call with several results supplies all arguments.
func (c *checker) builtinCall(x *operand, e *syntax.CallExpr, id builtinID) {
	name := exprString(e.Fun)
	x.mode, x.typ, x.val = novalue, Typ[Invalid], nil
	if id >= _MetaName && id <= _MetaReject {
		c.metaBuiltin(x, e, id)
		return
	}
	var spread []*operand // the elements of a tuple forwarded to an/fmt
	if e.Ellipsis.IsValid() && id >= _FmtPrint && id <= _FmtSprintf && len(e.Args) > 0 {
		// Forwarding ...any arguments (ct.any.use 2).
		var last operand
		c.tupleOK = true
		c.expr(&last, e.Args[len(e.Args)-1])
		c.tupleOK = false
		tt, ok := last.typ.(*TupleType)
		if last.mode != invalid && !ok {
			c.errorf(last.expr.Pos(), "ct.any.no-slice", "cannot pass %s with ... to %s: each argument is an element of the tuple", &last, name)
		}
		if !ok {
			x.invalidate()
			return
		}
		for _, t := range tt.Elems {
			spread = append(spread, &operand{mode: value, expr: last.expr, typ: t})
		}
		e = &syntax.CallExpr{Fun: e.Fun, Lparen: e.Lparen, Args: e.Args[:len(e.Args)-1], Rparen: e.Rparen}
	}
	if e.Ellipsis.IsValid() && id != _Append {
		c.errorf(e.Ellipsis, "expr.go", "invalid use of ... with built-in %s", name)
		c.useArgs(e.Args)
		x.invalidate()
		return
	}

	if id == _Make {
		c.makeCall(x, e)
		return
	}
	c.tupleOK = id == _Len
	if id == _Assert {
		c.inContract++
	}
	args := c.args(e.Args, nil)
	if id == _Assert {
		c.inContract--
	}
	c.tupleOK = false
	args = append(args, spread...)
	for _, a := range args {
		if a.mode == invalid {
			x.invalidate()
			return
		}
	}
	nargs := func(min, max int) bool {
		n := len(args)
		if n < min || max >= 0 && n > max {
			msg := "not enough"
			if n > min {
				msg = "too many"
			}
			c.errorf(e.Pos(), "expr.go", "%s arguments for %s (expected %d, found %d)", msg, exprString(e), min, n)
			x.invalidate()
			return false
		}
		return true
	}

	switch id {
	case _Len, _Cap:
		if !nargs(1, 1) {
			return
		}
		a := args[0]
		var val constant.Value
		ok := false
		if tt, isTuple := a.typ.(*TupleType); isTuple && id == _Len {
			// The length of a tuple is a constant (ct.any.use 3).
			x.mode, x.typ, x.val = constant_, Typ[Int], constant.MakeInt64(int64(len(tt.Elems)))
			x.expr = e
			return
		}
		switch t := core(a.typ).(type) {
		case *Basic:
			if t.info&IsString != 0 && id == _Len {
				ok = true
				if a.mode == constant_ {
					val = constant.MakeInt64(int64(len(constant.StringVal(a.val))))
				}
			}
		case *Array:
			ok = true
			if !hasCallOrRecv(e.Args[0]) {
				val = constant.MakeInt64(t.Len)
			}
		case *Slice, *Chan:
			ok = true
		case *Map:
			ok = id == _Len
		}
		if !ok {
			c.errorf(a.expr.Pos(), "expr.go", "invalid argument: %s for built-in %s", a, name)
			x.invalidate()
			return
		}
		c.defaultUntyped(a)
		if val != nil {
			x.mode, x.typ, x.val = constant_, Typ[Int], val
		} else {
			x.mode, x.typ = value, Typ[Int]
		}

	case _Append:
		if !nargs(1, -1) {
			return
		}
		s := args[0]
		st, ok := core(s.typ).(*Slice)
		if !ok {
			c.errorf(s.expr.Pos(), "expr.go", "invalid argument: %s is not a slice", s)
			x.invalidate()
			return
		}
		x.mode, x.typ = value, s.typ
		if e.Ellipsis.IsValid() {
			if len(args) != 2 {
				c.errorf(e.Pos(), "expr.go", "can only use ... with final argument in list")
				return
			}
			a := args[1]
			if Identical(st.Elem.Underlying(), Typ[U8]) && (isString(a.typ) || isTypeParam(a.typ) && c.allTerms(a.typ.(*TypeParam), isBytesOrString)) {
				c.defaultUntyped(a)
				return
			}
			// The variadic parameter has the unnamed type []E.
			c.assignment(a, &Slice{Elem: st.Elem}, "argument to append")
			return
		}
		for _, a := range args[1:] {
			c.assignment(a, st.Elem, "argument to append")
		}

	case _Copy:
		if !nargs(2, 2) {
			return
		}
		dst, src := args[0], args[1]
		ds, ok := core(dst.typ).(*Slice)
		if !ok {
			c.errorf(dst.expr.Pos(), "expr.go", "invalid argument: copy expects slice arguments; found %s", dst)
			x.invalidate()
			return
		}
		c.builtinInout(dst, name)
		c.defaultUntyped(src)
		if ss, ok := core(src.typ).(*Slice); ok && Identical(ds.Elem, ss.Elem) ||
			isString(src.typ) && Identical(ds.Elem.Underlying(), Typ[U8]) {
			x.mode, x.typ = value, Typ[Int]
			return
		}
		c.errorf(src.expr.Pos(), "expr.go", "invalid argument: arguments to copy %s and %s have different element types", dst, src)
		x.invalidate()

	case _Delete:
		if !nargs(2, 2) {
			return
		}
		mt, ok := core(args[0].typ).(*Map)
		if tp, isTP := args[0].typ.(*TypeParam); isTP && !ok {
			// Maps with the same key type (type.param.go).
			if k := mapKey(tp); k != nil {
				mt, ok = &Map{Key: k}, true
			}
		}
		if !ok {
			c.errorf(args[0].expr.Pos(), "expr.go", "invalid argument: %s is not a map", args[0])
			return
		}
		c.builtinInout(args[0], name)
		c.assignment(args[1], mt.Key, "argument to delete")

	case _Max, _Min:
		if !nargs(1, -1) {
			return
		}
		*x = *args[0]
		for _, y := range args[1:] {
			if x.mode == invalid {
				break
			}
			c.matchTypes(x, y)
			if x.mode == invalid || y.mode == invalid || !Identical(x.typ, y.typ) && !(isUntyped(x.typ) && isUntyped(y.typ)) {
				if x.mode != invalid && y.mode != invalid {
					c.errorf(y.expr.Pos(), "expr.go", "invalid argument: mismatched types %s and %s in call to %s", x.typ, y.typ, name)
				}
				x.invalidate()
				break
			}
			if x.mode == constant_ && y.mode == constant_ {
				less := constant.Compare(y.val, constOp[syntax.Lss], x.val)
				if less == (id == _Min) {
					x.val = y.val
				}
			} else {
				x.mode, x.val = value, nil
			}
		}
		if x.mode != invalid && !isOrdered(x.typ) {
			c.errorf(args[0].expr.Pos(), "expr.go", "invalid argument: %s cannot be ordered", x)
			x.invalidate()
			return
		}
		if x.mode == value {
			c.defaultUntyped(x)
		}
		x.expr = e

	case _BitsWrappingAdd, _BitsWrappingSub, _BitsWrappingMul,
		_BitsSaturatingAdd, _BitsSaturatingSub, _BitsSaturatingMul,
		_BitsCheckedAdd, _BitsCheckedSub, _BitsCheckedMul:
		if !nargs(2, 2) {
			return
		}
		a, b := args[0], args[1]
		c.matchTypes(a, b)
		if a.mode == invalid || b.mode == invalid {
			x.invalidate()
			return
		}
		if !Identical(a.typ, b.typ) {
			c.errorf(b.expr.Pos(), "expr.bits.functions", "invalid argument: mismatched types %s and %s in call to %s", a.typ, b.typ, name)
			x.invalidate()
			return
		}
		c.defaultUntyped(a)
		c.defaultUntyped(b)
		t := a.typ
		checked := func(t Type) bool { return isInteger(t) && !isWrapping(t) }
		if tp, ok := t.(*TypeParam); ok && !c.allTerms(tp, checked) || !isTypeParam(t) && !checked(t) {
			c.errorf(a.expr.Pos(), "expr.bits.functions", "invalid argument: %s must have a checked integer type", a)
			x.invalidate()
			return
		}
		x.mode, x.typ = value, t
		if id >= _BitsCheckedAdd {
			x.typ = NewTuple(t, Typ[Bool])
		}
		x.expr = e

	case _EffectMark, _EffectDefer, _EffectWithout:
		if id == _EffectMark && !nargs(1, 1) || id != _EffectMark && !nargs(1, -1) {
			return
		}
		rule := map[builtinID]string{_EffectMark: "eff.custom.mark", _EffectDefer: "eff.deferred.declare", _EffectWithout: "eff.without.form"}[id]
		for _, a := range args {
			if a.mode != constant_ || !Identical(a.typ, EffectType) {
				c.errorf(a.expr.Pos(), rule, "argument %s of %s must be a constant of type effect.Effect", a, name)
				x.invalidate()
				return
			}
		}
		if id == _EffectWithout {
			if !c.inEnsures {
				c.errorf(e.Pos(), "eff.without.form", "%s may appear only as the whole expression of an ensures clause", name)
				x.invalidate()
				return
			}
			x.mode, x.typ = value, Typ[Bool]
		}
		x.expr = e

	case _UnsafeData:
		if !nargs(1, 1) {
			return
		}
		a := args[0]
		_, isSlice := core(a.typ).(*Slice)
		b, isBasic := a.typ.Underlying().(*Basic)
		if !isString(a.typ) && !isSlice && !(isBasic && b.info&IsTerminated != 0) {
			c.errorf(a.expr.Pos(), "mem.unsafe.data", "unsafe.Data takes a string or a slice, not %s", a)
			x.invalidate()
			return
		}
		x.mode, x.typ, x.expr = value, Typ[Usize], e

	case _UnsafeMap:
		c.errorf(e.Pos(), "mem.unsafe.map", "unsafe.Map takes a type argument: unsafe.Map[T](addr)")
		x.invalidate()

	case _ConsoleReadByte:
		if !nargs(0, 0) {
			return
		}
		x.mode, x.typ, x.expr = value, NewTuple(Typ[U8], Typ[Bool]), e

	case _MathSum:
		// The sum of the elements as a mathematical integer, which takes
		// the type of the other operand (std.math.contract).
		if !nargs(1, 1) {
			return
		}
		if c.inContract == 0 {
			c.errorf(e.Pos(), "std.math.contract", "%s may appear only in contract expressions and assertions", name)
			x.invalidate()
			return
		}
		a := args[0]
		var elem Type
		switch t := core(a.typ).(type) {
		case *Slice:
			elem = t.Elem
		case *Array:
			elem = t.Elem
		}
		if elem == nil || !isInteger(elem) {
			c.errorf(a.expr.Pos(), "std.math.contract", "%s takes a slice or an array of integers, not %s", name, a)
			x.invalidate()
			return
		}
		x.mode, x.typ, x.expr = value, Typ[UntypedInt], e

	case _MathForAll, _MathExists:
		// A bounded quantifier over the integers of [lo, hi): a predicate
		// literal with one integer parameter (std.math.quantifier).
		if !nargs(3, 3) {
			return
		}
		if c.inContract == 0 {
			c.errorf(e.Pos(), "std.math.contract", "%s may appear only in contract expressions and assertions", name)
			x.invalidate()
			return
		}
		pred := args[2]
		lit, isLit := unparen(e.Args[2]).(*syntax.FuncLit)
		sig, _ := core(pred.typ).(*Signature)
		ok := isLit && sig != nil && sig.Params.Len() == 1 && isInteger(sig.Params.Vars[0].typ) &&
			sig.Results.Len() == 1 && isBoolean(sig.Results.Vars[0].typ) &&
			len(lit.Body.List) == 1
		if ok {
			_, ok = lit.Body.List[0].(*syntax.ReturnStmt)
		}
		if !ok {
			c.errorf(e.Args[2].Pos(), "std.math.quantifier", "the predicate of %s must be a function literal func(i T) bool { return … } with an integer type T", name)
			x.invalidate()
			return
		}
		it := sig.Params.Vars[0].typ
		for _, a := range args[:2] {
			c.assignment(a, it, "argument to "+name)
		}
		x.mode, x.typ, x.expr = value, Typ[Bool], e

	case _GenReadFile:
		if !nargs(1, 1) {
			return
		}
		if a := args[0]; !isString(a.typ) {
			c.errorf(a.expr.Pos(), "ct.eval.read-input", "gen.ReadFile takes the name of a file, not %s", a)
			x.invalidate()
			return
		}
		x.mode, x.typ, x.expr = value, NewSlice(Typ[U8]), e

	case _EncodingCP866:
		if !nargs(1, 1) {
			return
		}
		a := args[0]
		if a.mode != constant_ || !isString(a.typ) {
			c.errorf(a.expr.Pos(), "ct.encoding.const", "the argument of %s must be a constant string", name)
			x.invalidate()
			return
		}
		b, bad := encodeCP866(constant.StringVal(a.val))
		if bad != "" {
			c.errorf(a.expr.Pos(), "ct.encoding.unmappable", "%s cannot represent %q", name, bad)
			x.invalidate()
			return
		}
		x.mode, x.typ, x.expr, x.bytes = value, NewSlice(Typ[U8]), e, b
		c.info.Bytes[e] = b

	case _EffectDeclare:
		if !nargs(1, 1) {
			return
		}
		if c.declConst == nil || unparen(c.declInit) != e {
			c.errorf(e.Pos(), "eff.custom.declare", "%s may appear only as the initializer of a package-level constant", name)
			x.invalidate()
			return
		}
		a := args[0]
		if a.mode != constant_ || !isString(a.typ) {
			c.errorf(a.expr.Pos(), "eff.custom.declare", "the name of an effect must be a constant string; found %s", a)
			x.invalidate()
			return
		}
		bare := constant.StringVal(a.val)
		if bare == "" || strings.ContainsAny(bare, "#\n") {
			c.errorf(a.expr.Pos(), "eff.custom.declare", "invalid effect name %q", bare)
			x.invalidate()
			return
		}
		c.updateExprType(a.expr, Typ[String], true)
		x.mode, x.typ = constant_, EffectType
		x.val = constant.MakeString(c.pkg.Path + "." + c.declConst.name + "#" + bare)
		x.expr = e

	case _Panic:
		if !nargs(1, 1) {
			return
		}
		if !args[0].isNil() { // panic(nil) is allowed, as in Go
			c.defaultUntyped(args[0])
		}

	case _Print, _Println, _FmtPrint, _FmtPrintln:
		for _, a := range args {
			c.defaultUntyped(a)
		}

	case _Assert:
		// A proof obligation at compile time (contract.assert.form).
		if !nargs(1, 1) {
			return
		}
		a := args[0]
		c.defaultUntyped(a)
		if !isBoolean(a.typ) {
			c.errorf(a.expr.Pos(), "contract.assert.form", "the argument of assert must be a boolean, not %s", a)
		}

	case _FmtSprint, _FmtSprintln:
		for _, a := range args {
			c.defaultUntyped(a)
		}
		x.mode, x.typ = value, Typ[String]

	case _FmtPrintf, _FmtSprintf:
		if !nargs(1, -1) {
			return
		}
		for _, a := range args[1:] {
			c.defaultUntyped(a)
		}
		c.checkPrintf(name, args)
		if id == _FmtSprintf {
			x.mode, x.typ = value, Typ[String]
		}

	case _Clear:
		if !nargs(1, 1) {
			return
		}
		switch u := core(args[0].typ).(type) {
		case *Map:
			c.builtinInout(args[0], name)
		case *Slice:
			// Clearing a slice sets its elements to zero values (type.zero.uses).
			c.zeroUse(args[0].expr.Pos(), u.Elem, "type.zero.uses", "clear of a slice")
			c.builtinInout(args[0], name)
		default:
			c.errorf(args[0].expr.Pos(), "expr.go", "invalid argument: %s must be a map or slice", args[0])
		}

	case _Close:
		if !nargs(1, 1) {
			return
		}
		if tp, ok := args[0].typ.(*TypeParam); ok && c.allTerms(tp, func(t Type) bool {
			ch, ok := t.Underlying().(*Chan)
			return ok && ch.Dir != RecvOnly
		}) {
			return
		}
		ch, ok := core(args[0].typ).(*Chan)
		if !ok {
			c.errorf(args[0].expr.Pos(), "expr.go", "invalid operation: non-channel %s", args[0])
		} else if ch.Dir == RecvOnly {
			c.errorf(args[0].expr.Pos(), "expr.go", "invalid operation: cannot close receive-only channel %s", args[0])
		}
	}
}

func (c *checker) makeCall(x *operand, e *syntax.CallExpr) {
	if len(e.Args) == 0 {
		c.errorf(e.Pos(), "expr.go", "not enough arguments for %s (expected 1, found 0)", exprString(e))
		x.invalidate()
		return
	}
	t := c.typExpr(e.Args[0])
	if !isValid(t) {
		c.useArgs(e.Args[1:])
		x.invalidate()
		return
	}
	min, max := 1, 1
	switch core(t).(type) {
	case *Slice:
		min, max = 2, 3
	case *Map, *Chan:
		min, max = 1, 2
	default:
		c.errorf(e.Args[0].Pos(), "expr.go", "invalid argument: cannot make %s; type must be slice, map, or channel", exprString(e.Args[0]))
		c.useArgs(e.Args[1:])
		x.invalidate()
		return
	}
	if n := len(e.Args); n < min || n > max {
		msg := "not enough"
		if n > min {
			msg = "too many"
		}
		c.errorf(e.Pos(), "expr.go", "%s arguments for %s (expected %d, found %d)", msg, exprString(e), min, n)
		c.useArgs(e.Args[1:])
		x.invalidate()
		return
	}
	var sizes []int64
	for _, a := range e.Args[1:] {
		sizes = append(sizes, c.index(a, -1))
	}
	if s, ok := core(t).(*Slice); ok && sizes[0] != 0 {
		// The elements up to the length are zero values (type.zero.make).
		c.zeroUse(e.Args[1].Pos(), s.Elem, "type.zero.make", "make with a non-zero length")
	}
	if len(sizes) == 2 && sizes[0] >= 0 && sizes[1] >= 0 && sizes[0] > sizes[1] {
		c.errorf(e.Args[1].Pos(), "expr.go", "invalid argument: length and capacity swapped")
	}
	x.mode, x.typ = value, t
}

// hasCallOrRecv reports whether e contains a function call or a channel
// receive, which make len and cap of an array non-constant.
func hasCallOrRecv(e syntax.Expr) bool {
	found := false
	var walk func(syntax.Expr)
	walk = func(e syntax.Expr) {
		if found || e == nil {
			return
		}
		switch e := e.(type) {
		case *syntax.CallExpr:
			found = true
		case *syntax.UnaryExpr:
			if e.Op == syntax.Arrow {
				found = true
			}
			walk(e.X)
		case *syntax.ParenExpr:
			walk(e.X)
		case *syntax.SelectorExpr:
			walk(e.X)
		case *syntax.IndexExpr:
			walk(e.X)
			for _, i := range e.Indices {
				walk(i)
			}
		case *syntax.SliceExpr:
			walk(e.X)
			walk(e.Low)
			walk(e.High)
			walk(e.Max)
		case *syntax.BinaryExpr:
			walk(e.X)
			walk(e.Y)
		case *syntax.CompositeLit:
			for _, el := range e.Elts {
				walk(el)
			}
		case *syntax.KeyValueExpr:
			walk(e.Key)
			walk(e.Value)
		}
	}
	walk(e)
	return found
}
