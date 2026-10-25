package types

import (
	"go/constant"
	"go/token"
	"strings"

	"an/syntax"
)

// expr checks e, which must be a single value.
func (c *checker) expr(x *operand, e syntax.Expr) {
	c.exprHint(x, e, nil)
}

// exprHint checks e with a type hint for composite literals with an
// elided type.
func (c *checker) exprHint(x *operand, e syntax.Expr, hint Type) {
	c.rawExpr(x, e, hint)
	if hint != nil && resolveGenericEnum(x, hint) {
		c.recordType(e, x.mode, x.typ, nil)
	}
	if x.mode == value && isGeneric(x.typ) && hint != nil {
		if ts, ok := core(hint).(*Signature); ok {
			c.inferFromTarget(x, ts)
		}
	}
	if x.mode == value && isGeneric(x.typ) && c.inArgs {
		c.fromOwnConstraints(x)
	}
	c.singleValue(x)
	c.valueOnly(x)
}

// valueOnly reports an error if x is not a value.
func (c *checker) valueOnly(x *operand) {
	if x.mode == value && isGeneric(x.typ) {
		if !invalidParams(x.typ.(*Signature).TypeParams) {
			c.errorf(x.expr.Pos(), "type.param.go", "cannot use generic function %s without instantiation", exprString(x.expr))
		}
		x.invalidate()
		return
	}
	switch x.mode {
	case novalue:
		c.errorf(x.expr.Pos(), "expr.go", "%s (no value) used as value", exprString(x.expr))
	case builtin:
		c.errorf(x.expr.Pos(), "expr.go", "%s (built-in) must be called", exprString(x.expr))
	case typexpr:
		c.errorf(x.expr.Pos(), "expr.go", "%s (type) is not an expression", exprString(x.expr))
	default:
		return
	}
	x.invalidate()
}

// rawExpr checks e and records its type. The result may be a type, a
// built-in, no value or multiple values.
func (c *checker) rawExpr(x *operand, e syntax.Expr, hint Type) {
	x.mode, x.typ, x.val, x.expr, x.targs, x.second = invalid, Typ[Invalid], nil, e, nil, false
	c.exprInternal(x, e, hint)
	x.expr = e
	if c.pathDepth == 0 && len(c.held) > 0 {
		c.checkHeld(e)
	}
	if x.mode != typexpr {
		x.typ = unalias(x.typ)
	}
	switch x.mode {
	case constant_, variable, mapindex, value, commaok:
		if hasInvalid(x.typ) {
			x.invalidate() // reported where the invalid type arose
		}
	}
	if x.mode == commaok {
		// Recorded as a single value unless used in a comma-ok assignment.
		c.recordType(e, value, x.typ, nil)
		return
	}
	c.record(x)
}

func (c *checker) exprInternal(x *operand, e syntax.Expr, hint Type) {
	switch e := e.(type) {
	case *syntax.BadExpr:
		x.mode = invalid

	case *syntax.Name:
		c.ident(x, e, false)

	case *syntax.BasicLit:
		val, typ, ok := literalValue(e)
		if !ok {
			c.errorf(e.Pos(), "lex.literal.go", "invalid literal %s", e.Value)
			return
		}
		x.mode, x.typ, x.val = constant_, typ, val
		c.overflow(x)

	case *syntax.ParenExpr:
		c.rawExpr(x, e.X, hint)

	case *syntax.FuncLit:
		sig := c.funcType(e.Type, true)
		second := c.funcBody(c.scope, sig, nil, e.Type, e.Body, true)
		x.mode, x.typ, x.second = value, sig, second

	case *syntax.CompositeLit:
		c.compositeLit(x, e, hint)

	case *syntax.SelectorExpr:
		c.selector(x, e)

	case *syntax.IndexExpr:
		c.indexExpr(x, e)

	case *syntax.SliceExpr:
		c.sliceExpr(x, e)

	case *syntax.TypeAssertExpr:
		c.typeAssert(x, e)

	case *syntax.CallExpr:
		c.callExpr(x, e)

	case *syntax.StarExpr:
		c.errorf(e.Pos(), "expr.removed.deref", "there is no indirection operator * in Anlang")
		var y operand
		c.rawExpr(&y, e.X, nil)

	case *syntax.UnaryExpr:
		c.unary(x, e)

	case *syntax.BinaryExpr:
		c.binary(x, e)

	case *syntax.KeyValueExpr:
		c.errorf(e.Pos(), "expr.go", "unexpected key:value expression")

	case *syntax.ArrayType, *syntax.StructType, *syntax.FuncType, *syntax.InterfaceType,
		*syntax.MapType, *syntax.ChanType, *syntax.EnumType:
		x.mode, x.typ = typexpr, c.typExpr(e)

	case *syntax.Ellipsis:
		c.errorf(e.Pos(), "expr.go", "invalid use of ...")

	default:
		c.errorf(e.Pos(), "expr.go", "unexpected expression %s", exprString(e))
	}
}

// ident checks an identifier.
func (c *checker) ident(x *operand, id *syntax.Name, wantType bool) {
	x.mode, x.expr = invalid, id
	if id.Name == "_" {
		c.errorf(id.Pos(), "expr.go", "cannot use _ as value or type")
		return
	}
	obj := c.lookup(id.Name)
	if obj == nil {
		if step, ok := notYet[id.Name]; ok {
			what := id.Name
			if step == 2 {
				what = "generics (" + id.Name + ")"
			}
			c.notSupported(id.Pos(), step, what)
			return
		}
		c.undefined(id)
		return
	}
	c.info.Uses[id] = obj
	switch obj := obj.(type) {
	case *PkgName:
		c.errorf(id.Pos(), "expr.go", "use of package %s without selector", id.Name)
		return
	case *Const:
		c.objDecl(obj)
		if obj == universeIota {
			if c.iota == nil {
				c.errorf(id.Pos(), "decl.const.go", "cannot use iota outside constant declaration")
				return
			}
			x.mode, x.typ, x.val = constant_, Typ[UntypedInt], c.iota
			return
		}
		if !isValid(obj.typ) {
			return
		}
		x.mode, x.typ, x.val = constant_, obj.typ, obj.Val
	case *TypeName:
		c.objDecl(obj)
		if obj.typ == nil && c.lazy[obj] != nil {
			x.mode, x.typ = typexpr, c.lazy[obj]
			return
		}
		if obj.typ == nil {
			// A type used in its own alias declaration.
			c.errorf(id.Pos(), "type.go", "invalid recursive type: %s refers to itself", id.Name)
			return
		}
		if !isValid(obj.typ) {
			if n, ok := obj.typ.(*Named); !ok || n.underlying != nil {
				return
			}
		}
		x.mode, x.typ = typexpr, obj.typ
	case *Var:
		c.objDecl(obj)
		obj.used = true
		if obj.owner != nil && obj.owner != c.fn {
			switch {
			case obj.inout:
				c.useInoutVar(obj)
			case obj.second:
				c.errorf(id.Pos(), "expr.closure.capture-inout", "cannot capture %s in a function literal: it holds a closure that captures an inout variable", id.Name)
				return
			}
		}
		if !isValid(obj.typ) {
			return
		}
		x.mode, x.typ, x.second = variable, obj.typ, obj.second
	case *Func:
		c.objDecl(obj)
		x.mode, x.typ = value, obj.typ
	case *Builtin:
		x.mode, x.typ, x.id = builtin, Typ[Invalid], obj.id
	case *Variant:
		c.objDecl(obj)
		c.variantOperand(x, obj)
	case *Nil:
		x.mode, x.typ = value, Typ[UntypedNil]
	}
}

func isBuiltinObj(obj Object) bool {
	_, ok := obj.(*Builtin)
	return ok
}

// selector checks X.Sel: a qualified identifier or a field selector.
func (c *checker) selector(x *operand, e *syntax.SelectorExpr) {
	x.mode, x.expr = invalid, e
	if id, ok := e.X.(*syntax.Name); ok {
		if p, ok := c.lookup(id.Name).(*PkgName); ok {
			c.info.Uses[id] = p
			p.used = true
			if p.Imported.Scope == nil {
				return // the import was rejected
			}
			obj := p.Imported.Scope.Lookup(e.Sel.Name)
			switch {
			case obj == nil && strings.HasPrefix(p.Imported.Path, "an/"):
				// The standard library is built into this compiler only in part.
				c.notSupported(e.Pos(), 29, p.Imported.Path+"."+e.Sel.Name)
				return
			case obj == nil:
				c.errorf(e.Sel.Pos(), "decl.visibility.go", "undefined: %s.%s", id.Name, e.Sel.Name)
				return
			case !obj.base().Exported():
				c.errorf(e.Sel.Pos(), "decl.visibility.go", "name %s not exported by package %s", e.Sel.Name, id.Name)
				return
			}
			c.info.Uses[e.Sel] = obj
			// Imported packages are checked completely before their importers.
			if !isValid(obj.Type()) && !isBuiltinObj(obj) {
				return
			}
			switch obj := obj.(type) {
			case *Builtin:
				x.mode, x.typ, x.id = builtin, Typ[Invalid], obj.id
			case *Const:
				x.mode, x.typ, x.val = constant_, obj.typ, obj.Val
			case *TypeName:
				x.mode, x.typ = typexpr, obj.typ
			case *Var:
				x.mode, x.typ = variable, obj.typ
			case *Func:
				x.mode, x.typ = value, obj.typ
			case *Variant:
				c.variantOperand(x, obj)
			}
			return
		}
	}

	c.pathDepth++
	c.rawExpr(x, e.X, nil)
	c.pathDepth--
	x.expr = e
	switch x.mode {
	case invalid:
		return
	case typexpr:
		c.methodExpr(x, e)
		return
	}
	c.singleValue(x)
	c.valueOnly(x)
	if x.mode == invalid {
		return
	}
	obj, index, ambiguous := c.lookupFieldOrMethod(x.typ, c.pkg, e.Sel.Name)
	switch obj := obj.(type) {
	case *Var:
		c.info.Selections[e] = &Selection{Kind: FieldVal, Recv: x.typ, Obj: obj, Index: index}
		c.info.Uses[e.Sel] = obj
		if x.mode != variable {
			x.mode = value
		}
		x.typ, x.val = obj.typ, nil
		return
	case *Func:
		// A method value or the callee of a method call.
		c.info.Selections[e] = &Selection{Kind: MethodVal, Recv: x.typ, Obj: obj, Index: index}
		c.info.Uses[e.Sel] = obj
		if ms, ok := obj.typ.(*Signature); ok && ms.Recv != nil && ms.Recv.inout {
			if !c.inoutReceiver(x, e) {
				x.invalidate()
				return
			}
		}
		if n, ok := x.typ.(*Named); ok && n.orig == universeOption && obj.name == "Get" {
			// Get returns the zero value for None (type.option.get).
			c.zeroUse(e.Sel.Pos(), n.targs[0], "type.option.get", "Option.Get")
		}
		x.mode, x.typ, x.val = value, methodType(obj), nil
		return
	}
	switch {
	case ambiguous:
		c.errorf(e.Sel.Pos(), "type.struct.go", "ambiguous selector %s", exprString(e))
	case incomplete(x.typ):
	case enumOf(x.typ) != nil:
		c.errorf(e.Sel.Pos(), "type.enum.patterns", "%s undefined: the fields of an enum value are reached through patterns in a switch", exprString(e))
	case c.hidden(x.typ, e.Sel.Name):
		c.errorf(e.Sel.Pos(), "decl.visibility.go", "%s undefined (cannot refer to unexported field or method %s)", exprString(e), e.Sel.Name)
	default:
		c.errorf(e.Sel.Pos(), "expr.go", "%s undefined (type %s has no field or method %s)", exprString(e), x.typ, e.Sel.Name)
	}
	x.invalidate()
}

// hidden reports whether t has an unexported field or method name of
// another package.
func (c *checker) hidden(t Type, name string) bool {
	obj, _, _ := c.lookupFieldOrMethod(t, nil, name)
	return obj != nil
}

// typeAssert checks a type assertion x.(T) (type.interface.go). The
// single-value form panics when it fails (expr.call.type-assert).
func (c *checker) typeAssert(x *operand, e *syntax.TypeAssertExpr) {
	c.expr(x, e.X)
	if e.Type == nil {
		c.errorf(e.Pos(), "expr.go", "use of .(type) outside type switch")
		x.invalidate()
		return
	}
	t := c.typExpr(e.Type)
	if x.mode == invalid {
		return
	}
	xi, ok := x.typ.Underlying().(*Interface)
	if !ok {
		c.errorf(e.X.Pos(), "expr.go", "invalid operation: %s is not an interface", x)
		x.invalidate()
		return
	}
	if !isValid(t) {
		x.invalidate()
		return
	}
	if !c.possibleType(e.Type, x, xi, t, "type assertion") {
		x.invalidate()
		return
	}
	x.mode, x.typ, x.val = commaok, t, nil
}

// possibleType reports whether a value of interface type xi can hold a
// value of type t; if not, the assertion or case e is impossible.
func (c *checker) possibleType(e syntax.Expr, x *operand, xi *Interface, t Type, what string) bool {
	if _, ok := t.Underlying().(*Interface); ok {
		return true
	}
	if why := c.missingMethod(t, xi); why != "" {
		c.errorf(e.Pos(), "type.interface.go", "impossible %s: %s does not implement %s (%s)", what, t, x.typ, why)
		return false
	}
	return true
}

// inoutReceiver checks a selector x.M of a method with an inout
// receiver: x must be addressable (expr.method.inout-call), and the
// method value may only be called directly (expr.method.inout-value).
func (c *checker) inoutReceiver(x *operand, e *syntax.SelectorExpr) bool {
	if c.callee != e {
		c.errorf(e.Pos(), "expr.method.inout-value", "method %s has an inout receiver: it may only be called directly, not used as a value", exprString(e))
		return false
	}
	switch x.mode {
	case mapindex:
		c.errorf(e.X.Pos(), "expr.addr.map", "cannot call method %s with an inout receiver on a map element", e.Sel.Name)
		return false
	case variable:
	default:
		c.errorf(e.X.Pos(), "expr.method.inout-call", "cannot call method %s with an inout receiver on %s: it is not addressable", e.Sel.Name, x)
		return false
	}
	c.capturedInout(e.X)
	return true
}

// methodExpr checks a method expression T.M: a function whose first
// parameter is the receiver.
func (c *checker) methodExpr(x *operand, e *syntax.SelectorExpr) {
	t := x.typ
	obj, index, ambiguous := c.lookupFieldOrMethod(t, c.pkg, e.Sel.Name)
	m, ok := obj.(*Func)
	if !ok {
		if incomplete(t) {
			x.invalidate()
			return
		}
		if ambiguous {
			c.errorf(e.Sel.Pos(), "type.struct.go", "ambiguous selector %s", exprString(e))
		} else {
			c.errorf(e.Sel.Pos(), "expr.go", "%s undefined (type %s has no method %s)", exprString(e), t, e.Sel.Name)
		}
		x.invalidate()
		return
	}
	c.info.Uses[e.Sel] = m
	c.info.Selections[e] = &Selection{Kind: MethodExpr, Recv: t, Obj: m, Index: index}
	sig := methodType(m)
	recv := &Var{object: object{pos: e.Pos(), typ: t}, param: true}
	if ms, ok := m.typ.(*Signature); ok && ms.Recv != nil && ms.Recv.inout {
		if c.callee != e {
			c.errorf(e.Pos(), "expr.method.inout-value", "method expression %s has an inout receiver: it may only be called directly", exprString(e))
			x.invalidate()
			return
		}
		recv.inout = true
	}
	params := append([]*Var{recv}, sig.Params.Vars...)
	x.mode, x.typ, x.val = value, &Signature{Params: &Tuple{Vars: params}, Results: sig.Results, Variadic: sig.Variadic}, nil
}

func (c *checker) unary(x *operand, e *syntax.UnaryExpr) {
	if e.Op == syntax.And {
		c.errorf(e.Pos(), "expr.removed.address", "there is no address operator & in Anlang: use inout parameters")
		var y operand
		c.rawExpr(&y, e.X, nil)
		return
	}
	c.expr(x, e.X)
	x.expr = e
	if x.mode == invalid {
		return
	}
	switch e.Op {
	case syntax.Arrow:
		ch, ok := core(x.typ).(*Chan)
		if !ok {
			c.errorf(e.Pos(), "expr.go", "invalid operation: cannot receive from non-channel %s", x)
			x.invalidate()
			return
		}
		if ch.Dir == SendOnly {
			c.errorf(e.Pos(), "expr.go", "invalid operation: cannot receive from send-only channel %s", x)
			x.invalidate()
			return
		}
		x.mode, x.typ, x.val = commaok, ch.Elem, nil
		c.addPendingZero(e, ch.Elem, "type.zero.chan-receive", "single-value receive "+exprString(e))
		return
	case syntax.Tilde:
		c.errorf(e.Pos(), "expr.go", "invalid use of ~ outside a type constraint")
		x.invalidate()
		return
	}

	var ok bool
	switch e.Op {
	case syntax.Add, syntax.Sub:
		ok = isNumeric(x.typ)
	case syntax.Xor:
		ok = isInteger(x.typ)
	case syntax.Not:
		ok = isBoolean(x.typ)
	}
	if !ok {
		c.errorf(e.Pos(), opRule(x.typ), "invalid operation: operator %s not defined on %s", e.Op, x)
		x.invalidate()
		return
	}
	if x.mode == constant_ {
		prec := uint(0)
		if b, ok := x.typ.Underlying().(*Basic); ok && isUnsigned(b) && !isUntyped(b) {
			bits, _ := c.intRange(b)
			prec = uint(bits)
		}
		x.val = constant.UnaryOp(constOp[e.Op], x.val, prec)
		c.overflow(x)
		return
	}
	x.mode, x.val = value, nil
}

// overflow reports a typed constant that is not representable in its
// type (expr.int.const).
func (c *checker) overflow(x *operand) {
	if x.mode != constant_ {
		return
	}
	if isUntyped(x.typ) {
		// Untyped integer constants are exact up to maxConstBits, as in
		// go/types (expr.int.const).
		if x.val.Kind() == constant.Int && constant.BitLen(x.val) > maxConstBits {
			c.errorf(x.expr.Pos(), "expr.int.const", "constant overflow: %s exceeds %d bits", exprString(x.expr), maxConstBits)
			x.invalidate()
		}
		return
	}
	b, ok := x.typ.Underlying().(*Basic)
	if !ok {
		return
	}
	v, ok := c.representable(x.val, b)
	if !ok {
		c.errorf(x.expr.Pos(), "expr.int.const", "constant %s overflows %s", x.val, x.typ)
		x.invalidate()
		return
	}
	x.val = v
}

func (c *checker) binary(x *operand, e *syntax.BinaryExpr) {
	var y operand
	c.expr(x, e.X)
	c.expr(&y, e.Y)
	if x.mode == invalid {
		return
	}
	if y.mode == invalid {
		x.invalidate()
		return
	}
	if isShift(e.Op) {
		c.shift(x, &y, e)
		return
	}
	xNil, yNil := x.isNil(), y.isNil()
	c.matchTypes(x, &y)
	if x.mode == invalid || y.mode == invalid {
		x.invalidate()
		return
	}
	if isComparison(e.Op) {
		c.comparison(x, &y, xNil, yNil, e)
		return
	}
	if !Identical(x.typ, y.typ) {
		if isValid(x.typ) && isValid(y.typ) {
			c.errorf(e.OpPos, "expr.go", "invalid operation: %s (mismatched types %s and %s)", exprString(e), x.typ, y.typ)
		}
		x.invalidate()
		return
	}
	var ok bool
	switch e.Op {
	case syntax.Add:
		ok = isNumeric(x.typ) || isString(x.typ)
	case syntax.Sub, syntax.Mul, syntax.Quo:
		ok = isNumeric(x.typ)
	case syntax.Rem, syntax.And, syntax.Or, syntax.Xor, syntax.AndNot:
		ok = isInteger(x.typ)
	case syntax.LAnd, syntax.LOr:
		ok = isBoolean(x.typ)
	}
	if !ok {
		c.errorf(e.OpPos, opRule(x.typ), "invalid operation: operator %s not defined on %s", e.Op, x)
		x.invalidate()
		return
	}
	if (e.Op == syntax.Quo || e.Op == syntax.Rem) && y.mode == constant_ && isNumeric(y.typ) && constant.Sign(y.val) == 0 {
		c.errorf(e.Y.Pos(), "expr.int.div", "invalid operation: division by zero")
		x.invalidate()
		return
	}
	if x.mode == constant_ && y.mode == constant_ {
		op := constOp[e.Op]
		if op == token.QUO && isInteger(x.typ) {
			op = tokenQuoInt
		}
		x.val = constant.BinaryOp(x.val, op, y.val)
		x.expr = e
		c.overflow(x)
		return
	}
	x.mode, x.val = value, nil
}

// matchTypes converts an untyped operand to the type of the other
// operand of a binary operation.
func (c *checker) matchTypes(x, y *operand) {
	// A variant value of a generic enum type gets the instance of the
	// other operand (expr.variant.infer).
	if resolveGenericEnum(x, y.typ) || resolveGenericEnum(y, x.typ) {
		return
	}
	// nil compared with a value of a type that has nil.
	if x.isNil() && hasNil(y.typ) || y.isNil() && hasNil(x.typ) {
		if x.isNil() && !isUntyped(y.typ) {
			c.convertUntyped(x, y.typ, "expr.cmp.nil", "comparison")
		} else if y.isNil() && !isUntyped(x.typ) {
			c.convertUntyped(y, x.typ, "expr.cmp.nil", "comparison")
		}
		return
	}
	switch {
	case isUntyped(x.typ) && !isUntyped(y.typ):
		if canMix(x, y) {
			c.convertUntyped(x, y.typ, "expr.go", "binary operation")
		}
	case isUntyped(y.typ) && !isUntyped(x.typ):
		if canMix(y, x) {
			c.convertUntyped(y, x.typ, "expr.go", "binary operation")
		}
	case isUntyped(x.typ) && isUntyped(y.typ):
		if isNumeric(x.typ) && isNumeric(y.typ) {
			if x.typ.(*Basic).kind < y.typ.(*Basic).kind {
				c.convertUntyped(x, y.typ, "expr.go", "binary operation")
			} else {
				c.convertUntyped(y, x.typ, "expr.go", "binary operation")
			}
		}
	}
}

// canMix reports whether the untyped operand x can be converted to the
// type of y; if not, the mismatch is reported by the operation.
func canMix(x, y *operand) bool {
	if x.isNil() {
		return false
	}
	switch {
	case isNumeric(x.typ):
		return isNumeric(y.typ)
	case isBoolean(x.typ):
		return isBoolean(y.typ)
	case isString(x.typ):
		return isString(y.typ) || isBasic(y.typ, IsTerminated)
	}
	return false
}

// comparison checks a comparison; xNil and yNil report whether the
// operands were nil before matchTypes converted them.
func (c *checker) comparison(x, y *operand, xNil, yNil bool, e *syntax.BinaryExpr) {
	rule := "expr.cmp.go"
	var bad string
	switch {
	case xNil && yNil:
		rule, bad = "expr.cmp.nil", "operator "+e.Op.String()+" not defined on nil"
	case xNil || yNil:
		other := x
		if xNil {
			other = y
		}
		if e.Op != syntax.Eql && e.Op != syntax.Neq {
			rule, bad = "expr.cmp.nil", "operator "+e.Op.String()+" not defined on nil"
		} else if !hasNil(other.typ) {
			rule, bad = "expr.cmp.nil", "mismatched types "+other.typ.String()+" and untyped nil"
		}
	case !c.comparableTypes(x.typ, y.typ):
		bad = "mismatched types " + x.typ.String() + " and " + y.typ.String()
	case e.Op == syntax.Eql || e.Op == syntax.Neq:
		z := x
		if comparable(x.typ) {
			z = y
		}
		if !comparable(z.typ) {
			bad = "operator " + e.Op.String() + " not defined on " + z.typ.String()
			if _, ok := z.typ.Underlying().(*Chan); ok {
				rule = "expr.cmp.sync"
			} else if hasNil(z.typ) {
				bad = exprString(z.expr) + " (" + z.typ.String() + ") can only be compared to nil"
			}
		}
	default:
		if !isOrdered(x.typ) {
			bad = "operator " + e.Op.String() + " not defined on " + x.typ.String()
		}
	}
	if bad != "" {
		if rule == "expr.cmp.go" && (isTypeParam(x.typ) || isTypeParam(y.typ)) {
			rule = "type.param.go"
		}
		c.errorf(e.OpPos, rule, "invalid operation: %s (%s)", exprString(e), bad)
		x.invalidate()
		return
	}
	if x.mode == constant_ && y.mode == constant_ {
		x.val = constant.MakeBool(constant.Compare(x.val, constOp[e.Op], y.val))
		x.typ = Typ[UntypedBool]
		return
	}
	// The operands of a non-constant comparison get their final types.
	c.defaultUntyped(x)
	c.defaultUntyped(y)
	x.mode, x.typ, x.val = value, Typ[UntypedBool], nil
}

// opRule returns the rule violated by an operator not defined on type t:
// for a type parameter, one of its types does not support it
// (type.param.go).
func opRule(t Type) string {
	if isTypeParam(t) {
		return "type.param.go"
	}
	return "expr.go"
}

// comparableTypes reports whether operands of types x and y may be
// compared: one must be assignable to the type of the other (expr.cmp.go).
func (c *checker) comparableTypes(x, y Type) bool {
	if isUntyped(x) || isUntyped(y) {
		return Identical(x, y) || isUntyped(x) && isUntyped(y) && isNumeric(x) && isNumeric(y)
	}
	return c.assignableTo(x, y) || c.assignableTo(y, x)
}

// maxShift bounds the shift count of constant shifts.
const maxShift = 1100

// maxConstBits bounds the size of untyped integer constants.
const maxConstBits = 512

func (c *checker) shift(x, y *operand, e *syntax.BinaryExpr) {
	// The shift count.
	if y.mode == constant_ {
		v := constant.ToInt(y.val)
		// A constant count of any numeric type with an integer value is
		// accepted, as by go/types.
		if v.Kind() != constant.Int || !isNumeric(y.typ) {
			c.errorf(e.Y.Pos(), "expr.go", "invalid operation: shift count %s must be integer", y)
			x.invalidate()
			return
		}
		if constant.Sign(v) < 0 {
			c.errorf(e.Y.Pos(), "expr.go", "invalid operation: negative shift count %s", y)
			x.invalidate()
			return
		}
		if isUntyped(y.typ) {
			c.convertUntyped(y, Typ[Uint], "expr.go", "shift count")
		}
		y.val = v
	} else {
		c.defaultUntyped(y)
		if y.mode != invalid && !isInteger(y.typ) {
			c.errorf(e.Y.Pos(), "expr.go", "invalid operation: shift count %s must be integer", y)
			x.invalidate()
			return
		}
	}
	if y.mode == invalid {
		x.invalidate()
		return
	}

	// The shifted operand.
	if x.mode == constant_ {
		xv := constant.ToInt(x.val)
		if xv.Kind() != constant.Int {
			c.errorf(e.X.Pos(), "expr.go", "invalid operation: shifted operand %s must be integer", x)
			x.invalidate()
			return
		}
		if y.mode == constant_ {
			n, ok := int64Val(y.val)
			if !ok || n > maxShift {
				c.errorf(e.Y.Pos(), "expr.go", "invalid shift count %s", y)
				x.invalidate()
				return
			}
			if isUntyped(x.typ) && isFloat(x.typ) {
				x.typ = Typ[UntypedInt]
			}
			x.val = constant.Shift(xv, constOp[e.Op], uint(n))
			x.expr = e
			c.overflow(x)
			return
		}
		if isUntyped(x.typ) {
			// The type of the shifted operand comes from the context.
			c.rememberUntyped(e.X, true, x.mode, x.typ.(*Basic), x.val)
			x.mode, x.val = value, nil
			return
		}
	}
	if !isInteger(x.typ) {
		c.errorf(e.X.Pos(), "expr.go", "invalid operation: shifted operand %s must be integer", x)
		x.invalidate()
		return
	}
	x.mode, x.val = value, nil
}

// index checks an index operand and returns its constant value, or -1.
// length is the length of the indexed operand if known, or -1.
func (c *checker) index(e syntax.Expr, length int64) int64 {
	var x operand
	c.expr(&x, e)
	if x.mode == invalid {
		return -1
	}
	if isUntyped(x.typ) {
		c.convertUntyped(&x, Typ[Int], "expr.go", "index")
		if x.mode == invalid {
			return -1
		}
	}
	if !isInteger(x.typ) {
		c.errorf(e.Pos(), "expr.go", "invalid argument: index %s must be integer", &x)
		return -1
	}
	if x.mode != constant_ {
		return -1
	}
	n, ok := int64Val(x.val)
	if !ok || n < 0 {
		c.errorf(e.Pos(), "expr.bounds.check", "invalid argument: index %s must not be negative", &x)
		return -1
	}
	if length >= 0 && n >= length {
		c.errorf(e.Pos(), "expr.bounds.check", "invalid argument: index %s out of bounds [0:%d]", &x, length)
		return -1
	}
	return n
}

func (c *checker) indexExpr(x *operand, e *syntax.IndexExpr) {
	c.pathDepth++
	c.rawExpr(x, e.X, nil)
	c.pathDepth--
	switch {
	case x.mode == typexpr:
		x.typ = c.instanceType(e, x.typ)
		if !isValid(x.typ) {
			x.invalidate()
		}
		x.expr = e
		return
	case x.mode == value && isGeneric(x.typ):
		c.funcInst(x, e)
		return
	}
	c.singleValue(x)
	c.valueOnly(x)
	if x.mode == invalid {
		c.useArgs(e.Indices)
		return
	}
	x.expr = e
	if len(e.Indices) != 1 {
		c.errorf(e.Pos(), "expr.go", "unexpected comma: want a single index")
		x.invalidate()
		return
	}
	idx := e.Indices[0]
	switch t := core(x.typ).(type) {
	case *Basic:
		if t.info&IsString != 0 {
			length := int64(-1)
			if x.mode == constant_ {
				length = int64(len(constant.StringVal(x.val)))
			}
			c.index(idx, length)
			x.mode, x.typ, x.val = value, Typ[U8], nil
			return
		}
	case *Array:
		c.index(idx, t.Len)
		if x.mode != variable {
			x.mode = value
		}
		x.typ = t.Elem
		return
	case *Slice:
		c.index(idx, -1)
		if x.mode != variable {
			x.mode = value
		}
		x.typ = t.Elem
		return
	case *Map:
		var k operand
		c.expr(&k, idx)
		c.assignment(&k, t.Key, "map index")
		x.mode, x.typ = mapindex, t.Elem
		c.addPendingZero(e, t.Elem, "type.zero.map-index", "single-value map index "+exprString(e))
		return
	}
	if tp, ok := x.typ.(*TypeParam); ok {
		// Without a core type, indexing is permitted if every type in the
		// type set can be indexed with the same element type.
		if elem := indexElem(tp); elem != nil {
			c.index(idx, -1)
			if x.mode != variable {
				x.mode = value
			}
			x.typ, x.val = elem, nil
			return
		}
	}
	c.errorf(e.Pos(), "expr.go", "invalid operation: cannot index %s", x)
	x.invalidate()
}

func (c *checker) sliceExpr(x *operand, e *syntax.SliceExpr) {
	c.pathDepth++
	c.expr(x, e.X)
	c.pathDepth--
	if x.mode == invalid {
		for _, i := range []syntax.Expr{e.Low, e.High, e.Max} {
			if i != nil {
				c.useArgs([]syntax.Expr{i})
			}
		}
		return
	}
	length := int64(-1)
	switch t := core(x.typ).(type) {
	case *Basic:
		if t.info&IsString == 0 {
			goto bad
		}
		if e.Slice3 {
			c.errorf(e.Pos(), "expr.go", "invalid operation: 3-index slice of string")
			x.invalidate()
			return
		}
		if x.mode == constant_ {
			length = int64(len(constant.StringVal(x.val)))
		}
		if isUntyped(x.typ) {
			x.typ = Typ[String]
		}
		x.mode, x.val = value, nil
	case *Array:
		if x.mode != variable {
			c.errorf(e.Pos(), "expr.go", "invalid operation: %s (slice of unaddressable value)", exprString(e))
			x.invalidate()
			return
		}
		length = t.Len
		x.typ = &Slice{Elem: t.Elem}
	case *Slice:
	default:
		goto bad
	}
	{
		var idx [3]int64
		for i, ie := range []syntax.Expr{e.Low, e.High, e.Max} {
			idx[i] = -1
			if ie != nil {
				max := length
				if max >= 0 {
					max++ // a slice bound may equal the length
				}
				idx[i] = c.index(ie, max)
			}
		}
		for i := 0; i < 3; i++ {
			for j := i + 1; j < 3; j++ {
				if idx[i] >= 0 && idx[j] >= 0 && idx[i] > idx[j] {
					c.errorf(e.Pos(), "expr.bounds.check", "invalid slice indices: %d < %d", idx[j], idx[i])
				}
			}
		}
	}
	if x.mode != variable {
		x.mode = value
	}
	x.expr = e
	return
bad:
	if tp, ok := x.typ.(*TypeParam); ok && c.allTerms(tp, isBytesOrString) && !e.Slice3 {
		// string and []u8 types: the result has the type parameter's type.
		for _, i := range []syntax.Expr{e.Low, e.High} {
			if i != nil {
				c.index(i, -1)
			}
		}
		x.mode, x.expr = value, e
		return
	}
	c.errorf(e.Pos(), "expr.go", "cannot slice %s", x)
	x.invalidate()
}
