package types

import (
	"go/constant"
	"go/token"

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
	c.singleValue(x)
	c.valueOnly(x)
}

// valueOnly reports an error if x is not a value.
func (c *checker) valueOnly(x *operand) {
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
	x.mode, x.typ, x.val, x.expr = invalid, Typ[Invalid], nil, e
	c.exprInternal(x, e, hint)
	x.expr = e
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
		c.notSupported(e.Pos(), 2, "function literals")

	case *syntax.CompositeLit:
		c.compositeLit(x, e, hint)

	case *syntax.SelectorExpr:
		c.selector(x, e)

	case *syntax.IndexExpr:
		c.indexExpr(x, e)

	case *syntax.SliceExpr:
		c.sliceExpr(x, e)

	case *syntax.TypeAssertExpr:
		c.expr(x, e.X)
		if e.Type == nil {
			c.errorf(e.Pos(), "expr.go", "use of .(type) outside type switch")
		} else {
			c.notSupported(e.Pos(), 2, "type assertions")
		}
		x.invalidate()

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
		if obj.typ == nil {
			// A local type used in its own alias declaration.
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
		if !isValid(obj.typ) {
			return
		}
		x.mode, x.typ = variable, obj.typ
	case *Func:
		c.objDecl(obj)
		x.mode, x.typ = value, obj.typ
	case *Builtin:
		x.mode, x.typ, x.id = builtin, Typ[Invalid], obj.id
	case *Nil:
		x.mode, x.typ = value, Typ[UntypedNil]
	}
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
			if obj == nil || !obj.base().Exported() {
				c.errorf(e.Sel.Pos(), "decl.visibility.go", "undefined: %s.%s", id.Name, e.Sel.Name)
				return
			}
			c.info.Uses[e.Sel] = obj
			switch obj := obj.(type) {
			case *Builtin:
				x.mode, x.typ, x.id = builtin, Typ[Invalid], obj.id
			default:
				c.notSupported(e.Pos(), 29, exprString(e))
			}
			return
		}
	}

	c.rawExpr(x, e.X, nil)
	x.expr = e
	switch x.mode {
	case invalid:
		return
	case typexpr:
		c.notSupported(e.Pos(), 2, "method expressions")
		x.invalidate()
		return
	}
	c.singleValue(x)
	c.valueOnly(x)
	if x.mode == invalid {
		return
	}
	if s, ok := x.typ.Underlying().(*Struct); ok {
		for _, f := range s.Fields {
			if f.name == e.Sel.Name && f.name != "_" {
				c.info.Uses[e.Sel] = f
				if x.mode != variable {
					x.mode = value
				}
				x.typ = f.typ
				return
			}
		}
	}
	if i, ok := x.typ.Underlying().(*Interface); ok && lookupMethod(i, e.Sel.Name) != nil {
		c.notSupported(e.Sel.Pos(), 2, "method calls")
		x.invalidate()
		return
	}
	c.errorf(e.Sel.Pos(), "expr.go", "%s undefined (type %s has no field or method %s)", exprString(e), x.typ, e.Sel.Name)
	x.invalidate()
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
		ch, ok := x.typ.Underlying().(*Chan)
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
		c.errorf(e.Pos(), "expr.go", "invalid operation: operator %s not defined on %s", e.Op, x)
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
		c.errorf(e.OpPos, "expr.go", "invalid operation: operator %s not defined on %s", e.Op, x)
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
	case !comparableTypes(x.typ, y.typ):
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

// comparableTypes reports whether operands of types x and y may be
// compared: one must be assignable to the type of the other (expr.cmp.go).
func comparableTypes(x, y Type) bool {
	if isUntyped(x) || isUntyped(y) {
		return Identical(x, y) || isUntyped(x) && isUntyped(y) && isNumeric(x) && isNumeric(y)
	}
	return assignable(x, y) || assignable(y, x)
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
	c.rawExpr(x, e.X, nil)
	if x.mode == typexpr {
		c.notSupported(e.Pos(), 2, "generic types")
		x.invalidate()
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
	switch t := x.typ.Underlying().(type) {
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
		return
	}
	c.errorf(e.Pos(), "expr.go", "invalid operation: cannot index %s", x)
	x.invalidate()
}

func (c *checker) sliceExpr(x *operand, e *syntax.SliceExpr) {
	c.expr(x, e.X)
	if x.mode == invalid {
		for _, i := range []syntax.Expr{e.Low, e.High, e.Max} {
			if i != nil {
				c.useArgs([]syntax.Expr{i})
			}
		}
		return
	}
	length := int64(-1)
	switch t := x.typ.Underlying().(type) {
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
	c.errorf(e.Pos(), "expr.go", "cannot slice %s", x)
	x.invalidate()
}
