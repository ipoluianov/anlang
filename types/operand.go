package types

import (
	"go/constant"

	"an/syntax"
)

// operandMode is the kind of an operand.
type operandMode int

const (
	invalid   operandMode = iota // the operand is invalid; an error has been reported
	novalue                      // a call without results
	builtin                      // a built-in function
	typexpr                      // a type
	constant_                    // a constant; val holds the value
	variable                     // an addressable variable (expr.addr.def)
	mapindex                     // a map index expression m[k]
	value                        // any other value
	commaok                      // a value that may be used in a comma-ok assignment
)

// operand is the result of checking an expression.
type operand struct {
	mode   operandMode
	expr   syntax.Expr
	typ    Type
	val    constant.Value
	id     builtinID // for builtin
	targs  []Type    // type arguments of a partially instantiated generic function
	second bool      // a second-class closure (expr.closure.capture-inout)
}

func (x *operand) invalidate() { x.mode = invalid }

func (x *operand) isNil() bool {
	return x.mode == value && x.typ == Typ[UntypedNil]
}

// String describes the operand for diagnostics.
func (x *operand) String() string {
	var what string
	switch x.mode {
	case invalid:
		return "invalid operand"
	case novalue:
		return exprString(x.expr) + " (no value)"
	case builtin:
		return exprString(x.expr) + " (built-in)"
	case typexpr:
		return exprString(x.expr) + " (type)"
	case constant_:
		what = "constant"
		if s := exprString(x.expr); s != x.val.String() {
			what = "constant " + x.val.String()
		}
	case variable:
		what = "variable"
	default:
		what = "value"
	}
	if x.isNil() {
		return "nil"
	}
	return exprString(x.expr) + " (" + what + " of type " + x.typ.String() + ")"
}

// untypedInfo is an untyped expression whose final type is not yet
// known: it is decided by the context in which the expression is used.
type untypedInfo struct {
	isLhs bool // the left operand of a non-constant shift
	mode  operandMode
	typ   *Basic
	val   constant.Value
}

// record records the type of an expression in Info.Types, or remembers
// an untyped expression until its type is known.
func (c *checker) record(x *operand) {
	if x.expr == nil {
		return
	}
	var typ Type
	var val constant.Value
	switch x.mode {
	case invalid:
		typ = Typ[Invalid]
	case novalue:
		typ = &Tuple{}
	case constant_:
		typ, val = x.typ, x.val
	default:
		typ = x.typ
	}
	if isUntyped(typ) {
		c.rememberUntyped(x.expr, false, x.mode, typ.(*Basic), val)
		return
	}
	c.recordType(x.expr, x.mode, typ, val)
}

func (c *checker) recordType(e syntax.Expr, mode operandMode, typ Type, val constant.Value) {
	c.info.Types[e] = TypeAndValue{mode: mode, Type: typ, Value: val}
}

func (c *checker) rememberUntyped(e syntax.Expr, lhs bool, mode operandMode, typ *Basic, val constant.Value) {
	c.untyped[e] = untypedInfo{isLhs: lhs, mode: mode, typ: typ, val: val}
}

// updateExprType sets the final type of the untyped expression e and
// of its untyped operands. final reports whether typ is the final type,
// as opposed to another untyped type.
func (c *checker) updateExprType(e syntax.Expr, typ Type, final bool) {
	old, ok := c.untyped[e]
	if !ok {
		return
	}
	switch e := e.(type) {
	case *syntax.ParenExpr:
		c.updateExprType(e.X, typ, final)
	case *syntax.UnaryExpr:
		if old.val == nil {
			c.updateExprType(e.X, typ, final)
		}
	case *syntax.BinaryExpr:
		if old.val != nil {
			break // operands of a constant expression keep their types
		}
		switch {
		case isComparison(e.Op):
			// The operands keep their types; the result is a boolean.
		case isShift(e.Op):
			c.updateExprType(e.X, typ, final)
		default:
			c.updateExprType(e.X, typ, final)
			c.updateExprType(e.Y, typ, final)
		}
	}

	if !final && isUntyped(typ) {
		old.typ = typ.Underlying().(*Basic)
		c.untyped[e] = old
		return
	}
	delete(c.untyped, e)
	if old.isLhs && !isInteger(typ) {
		c.errorf(e.Pos(), "expr.go", "invalid operation: shifted operand %s (type %s) must be integer", exprString(e), typ)
		return
	}
	if old.val != nil {
		if b, ok := typ.Underlying().(*Basic); ok {
			if v, ok := c.representable(old.val, b); ok {
				old.val = v
			}
		}
	}
	c.recordType(e, old.mode, typ, old.val)
}

// flushUntyped records the remaining untyped expressions with their
// untyped types.
func (c *checker) flushUntyped() {
	for e, u := range c.untyped {
		c.recordType(e, u.mode, u.typ, u.val)
	}
	clear(c.untyped)
}

// convertUntyped converts the untyped operand x to the type target, as
// in an assignment or a binary operation with a typed operand. It reports
// an error and invalidates x if the conversion is not possible.
func (c *checker) convertUntyped(x *operand, target Type, rule, context string) {
	if x.mode == invalid || !isUntyped(x.typ) || !isValid(target) {
		return
	}
	if isUntyped(target) {
		// Both untyped: the result has the "larger" kind.
		if isNumeric(x.typ) && isNumeric(target) {
			if x.typ.(*Basic).kind < target.(*Basic).kind {
				c.updateExprType(x.expr, target, false)
				x.typ = target
			}
		} else if x.typ != target {
			c.mismatch(x, target, rule, context)
		}
		return
	}
	if tp, ok := target.(*TypeParam); ok {
		c.convertUntypedParam(x, tp, rule, context)
		return
	}
	switch t := target.Underlying().(type) {
	case *Basic:
		if x.isNil() {
			c.mismatch(x, target, rule, context)
			return
		}
		if x.mode == constant_ {
			v, ok := c.representable(x.val, t)
			if !ok {
				c.notRepresentable(x, target, rule)
				return
			}
			x.val = v
		} else {
			// A non-constant untyped bool or shift result.
			ok := isBoolean(x.typ) && t.info&IsBoolean != 0 ||
				isNumeric(x.typ) && t.info&IsNumeric != 0
			if !ok {
				c.mismatch(x, target, rule, context)
				return
			}
		}
	case *Interface:
		if !x.isNil() {
			// An untyped value assigned to an interface gets its default type.
			def := defaultType(x.typ)
			c.convertUntyped(x, def, rule, context)
			if x.mode == invalid {
				return
			}
			if c.missingMethod(def, t) != "" {
				c.mismatch(x, target, rule, context)
			}
			return
		}
	case *Slice, *Map, *Chan, *Signature:
		if !x.isNil() {
			c.mismatch(x, target, rule, context)
			return
		}
	default:
		c.mismatch(x, target, rule, context)
		return
	}
	c.updateExprType(x.expr, target, true)
	x.typ = target
}

// convertUntypedParam converts an untyped operand to a type parameter:
// it must be convertible to every type in the type set, and a constant
// becomes a value of the type parameter (type.param.go).
func (c *checker) convertUntypedParam(x *operand, tp *TypeParam, rule, context string) {
	terms, restricted := termsOf(tp)
	ok := restricted && len(terms) > 0
	for _, term := range terms {
		u := term.Type.Underlying()
		switch {
		case x.isNil():
			ok = ok && hasNil(term.Type)
		case x.mode == constant_:
			b, isBasic := u.(*Basic)
			if isBasic {
				_, fits := c.representable(x.val, b)
				ok = ok && fits
			} else {
				ok = false
			}
		default:
			ok = ok && (isBoolean(x.typ) && isBoolean(u) || isNumeric(x.typ) && isNumeric(u))
		}
	}
	if !ok {
		c.errorf(x.expr.Pos(), "type.param.go", "cannot use %s as %s value in %s: it is not valid for every type in the type set of %s", x, tp, context, tp)
		x.invalidate()
		return
	}
	c.updateExprType(x.expr, tp, true)
	x.mode, x.typ, x.val = value, tp, nil
}

func (c *checker) notRepresentable(x *operand, target Type, rule string) {
	if isInteger(target) && isNumeric(x.typ) && x.val.Kind() == constant.Float && constant.ToInt(x.val).Kind() != constant.Int {
		c.errorf(x.expr.Pos(), rule, "cannot use %s as %s value: truncated", x, target)
	} else if isNumeric(target) && isNumeric(x.typ) {
		c.errorf(x.expr.Pos(), "expr.int.const", "cannot use %s as %s value: overflows", x, target)
	} else {
		c.errorf(x.expr.Pos(), rule, "cannot use %s as %s value", x, target)
	}
	x.invalidate()
}

func (c *checker) mismatch(x *operand, target Type, rule, context string) {
	if x.isNil() && !hasNil(target) {
		c.errorf(x.expr.Pos(), "type.nil.types", "cannot use nil as %s value in %s", target, context)
	} else {
		c.errorf(x.expr.Pos(), rule, "cannot use %s as %s value in %s", x, target, context)
	}
	x.invalidate()
}

// defaultUntyped gives an untyped operand its default type.
func (c *checker) defaultUntyped(x *operand) {
	if x.mode == invalid || !isUntyped(x.typ) {
		return
	}
	if x.isNil() {
		c.errorf(x.expr.Pos(), "expr.nil.context", "use of untyped nil")
		x.invalidate()
		return
	}
	c.convertUntyped(x, defaultType(x.typ), "type.assign.go", "assignment")
}

// assignment checks that x can be assigned to a variable of type t and
// converts untyped operands. context describes the assignment for
// diagnostics.
func (c *checker) assignment(x *operand, t Type, context string) {
	c.singleValue(x)
	if x.second && !c.secondOK {
		c.secondClassError(x, "in "+context)
		return
	}
	switch x.mode {
	case invalid:
		return
	case constant_, variable, mapindex, value, commaok:
	default:
		c.errorf(x.expr.Pos(), "expr.go", "cannot use %s as value", x)
		x.invalidate()
		return
	}
	if isGenericEnumValue(x.typ) {
		if t == nil || !resolveGenericEnum(x, t) {
			c.genericEnumError(x)
			return
		}
		c.recordType(x.expr, x.mode, x.typ, nil)
	}
	if t == nil {
		// Declaration without a type: the operand's default type.
		c.defaultUntyped(x)
		return
	}
	if !isValid(t) {
		x.invalidate()
		return
	}
	if isUntyped(x.typ) {
		c.convertUntyped(x, t, "type.assign.go", context)
		return
	}
	if !isValid(x.typ) {
		x.invalidate()
		return
	}
	if !c.assignableTo(x.typ, t) {
		if ti, ok := t.Underlying().(*Interface); ok {
			c.errorf(x.expr.Pos(), "type.interface.go", "cannot use %s as %s value in %s: %s does not implement %s (%s)", x, t, context, x.typ, t, c.missingMethod(x.typ, ti))
		} else {
			c.errorf(x.expr.Pos(), "type.assign.go", "cannot use %s as %s value in %s", x, t, context)
		}
		x.invalidate()
	}
}

// singleValue reports an error if x is a multi-value expression.
func (c *checker) singleValue(x *operand) {
	if x.mode == value {
		if t, ok := x.typ.(*Tuple); ok {
			c.errorf(x.expr.Pos(), "expr.go", "multiple-value %s (value of type %s) in single-value context", exprString(x.expr), t)
			x.invalidate()
		}
	}
}
