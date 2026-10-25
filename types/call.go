package types

import (
	"go/constant"
	"strings"
	"unicode/utf8"

	"an/syntax"
)

func (c *checker) callExpr(x *operand, e *syntax.CallExpr) {
	var fun operand
	c.rawExpr(&fun, e.Fun, nil)
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
	if len(sig.TypeParams) > 0 {
		args = c.args(e.Args, nil)
	} else {
		args = c.args(e.Args, sig)
	}
	if len(sig.TypeParams) > 0 {
		if sig = c.inferCall(e, sig, fun.targs, args); sig == nil {
			x.invalidate()
			return
		}
	}
	c.arguments(e, sig, args)
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
			c.assignment(a, params[i].typ, "argument to "+name)
		}
		return
	}
	if sig.Variadic {
		if len(args) < nparams-1 {
			c.argCountError(e, sig, args)
			return
		}
		for i, a := range args {
			t := params[min(i, nparams-1)].typ
			if i >= nparams-1 {
				t = t.(*Slice).Elem
			}
			c.assignment(a, t, "argument to "+name)
		}
		return
	}
	if len(args) != nparams {
		c.argCountError(e, sig, args)
		return
	}
	for i, a := range args {
		c.assignment(a, params[i].typ, "argument to "+name)
	}
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
	if rule, ok := c.convertible(x.typ, t); !ok {
		switch rule {
		case "type.conv.int-narrow":
			c.errorf(x.expr.Pos(), rule, "cannot convert %s to type %s: the value may not fit and it is not proven to (use conv.Narrow[%s] to get (%s, bool))", x, t, t, t)
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
		if c.widens(vb, tb) {
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
	args := c.args(e.Args, nil)
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

	case _Clear:
		if !nargs(1, 1) {
			return
		}
		switch u := core(args[0].typ).(type) {
		case *Map:
		case *Slice:
			// Clearing a slice sets its elements to zero values (type.zero.uses).
			c.zeroUse(args[0].expr.Pos(), u.Elem, "type.zero.uses", "clear of a slice")
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
