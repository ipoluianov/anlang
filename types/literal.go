package types

import (
	"go/constant"

	"an/syntax"
)

// compositeLit checks a composite literal (expr.lit.go). hint is the
// type of the literal if its type is elided.
func (c *checker) compositeLit(x *operand, e *syntax.CompositeLit, hint Type) {
	var typ Type
	switch t := e.Type.(type) {
	case nil:
		if hint == nil {
			c.errorf(e.Pos(), "expr.lit.go", "invalid composite literal type: missing type")
			c.skipElems(e)
			return
		}
		typ = hint
	case *syntax.ArrayType:
		if _, ok := t.Len.(*syntax.Ellipsis); ok {
			// [...]T: the length is given by the elements.
			elem := c.typExpr(t.Elt)
			n := c.indexedElems(e.Elts, elem, -1)
			typ = &Array{Len: n, Elem: elem}
			c.recordType(e.Type, typexpr, typ, nil)
			x.mode, x.typ = value, typ
			return
		}
		typ = c.typExpr(e.Type)
	default:
		typ = c.typExpr(e.Type)
	}
	if !isValid(typ) {
		c.skipElems(e)
		return
	}

	switch u := typ.Underlying().(type) {
	case *Struct:
		c.structLit(e, u)
	case *Array:
		c.indexedElems(e.Elts, u.Elem, u.Len)
	case *Slice:
		c.indexedElems(e.Elts, u.Elem, -1)
	case *Map:
		c.mapLit(e, u)
	default:
		c.errorf(e.Pos(), "expr.lit.go", "invalid composite literal type %s", typ)
		c.skipElems(e)
		return
	}
	x.mode, x.typ = value, typ
}

// skipElems checks the elements of an invalid literal for other errors.
func (c *checker) skipElems(e *syntax.CompositeLit) {
	for _, el := range e.Elts {
		if kv, ok := el.(*syntax.KeyValueExpr); ok {
			el = kv.Value
		}
		if _, ok := el.(*syntax.CompositeLit); ok {
			continue
		}
		var x operand
		c.rawExpr(&x, el, nil)
	}
}

func (c *checker) structLit(e *syntax.CompositeLit, t *Struct) {
	if len(e.Elts) == 0 {
		return
	}
	if _, keyed := e.Elts[0].(*syntax.KeyValueExpr); keyed {
		seen := map[string]bool{}
		for _, el := range e.Elts {
			kv, ok := el.(*syntax.KeyValueExpr)
			if !ok {
				c.errorf(el.Pos(), "expr.lit.go", "mixture of field:value and value elements in struct literal")
				continue
			}
			key, ok := kv.Key.(*syntax.Name)
			if !ok {
				c.errorf(kv.Key.Pos(), "expr.lit.go", "invalid field name %s in struct literal", exprString(kv.Key))
				continue
			}
			var field *Var
			for _, f := range t.Fields {
				if f.name == key.Name && f.name != "_" {
					field = f
				}
			}
			if field == nil {
				c.errorf(key.Pos(), "expr.lit.go", "unknown field %s in struct literal of type %s", key.Name, t)
				var x operand
				c.rawExpr(&x, kv.Value, nil)
				continue
			}
			c.info.Uses[key] = field
			if !visible(field, c.pkg) {
				c.errorf(key.Pos(), "decl.visibility.go", "cannot refer to unexported field %s in struct literal of type %s", key.Name, t)
			}
			if seen[key.Name] {
				c.errorf(key.Pos(), "expr.lit.go", "duplicate field name %s in struct literal", key.Name)
			}
			seen[key.Name] = true
			c.fieldValue(kv.Value, field.typ)
		}
		return
	}
	for i, el := range e.Elts {
		if _, ok := el.(*syntax.KeyValueExpr); ok {
			c.errorf(el.Pos(), "expr.lit.go", "mixture of field:value and value elements in struct literal")
			continue
		}
		if i >= len(t.Fields) {
			c.errorf(el.Pos(), "expr.lit.go", "too many values in struct literal of type %s", t)
			return
		}
		if f := t.Fields[i]; !visible(f, c.pkg) {
			c.errorf(el.Pos(), "decl.visibility.go", "implicit assignment to unexported field %s in struct literal of type %s", f.name, t)
		}
		c.fieldValue(el, t.Fields[i].typ)
	}
	if len(e.Elts) < len(t.Fields) {
		c.errorf(e.Rbrace, "expr.lit.go", "too few values in struct literal of type %s", t)
	}
}

// fieldValue checks a field value of a struct literal: unlike elements
// of arrays, slices and maps, it cannot elide its type (expr.lit.go).
func (c *checker) fieldValue(e syntax.Expr, t Type) {
	var x operand
	c.expr(&x, e)
	c.assignment(&x, t, "struct literal")
}

// element checks an element of a composite literal of type t.
func (c *checker) element(e syntax.Expr, t Type, context string) {
	var x operand
	c.exprHint(&x, e, t)
	c.assignment(&x, t, context)
}

// indexedElems checks the elements of an array or slice literal and
// returns the length: the largest index plus one. length is the array
// length, or -1 for slices and [...]T.
func (c *checker) indexedElems(elts []syntax.Expr, elem Type, length int64) int64 {
	seen := map[int64]bool{}
	var next, max int64
	for _, el := range elts {
		valid := true
		if kv, ok := el.(*syntax.KeyValueExpr); ok {
			var k operand
			c.expr(&k, kv.Key)
			valid = false
			if k.mode == constant_ && (isInteger(k.typ) || isUntyped(k.typ)) {
				if n, ok := int64Val(k.val); ok && n >= 0 && constant.ToInt(k.val).Kind() == constant.Int {
					c.convertUntyped(&k, Typ[Int], "expr.lit.go", "index")
					next, valid = n, true
				}
			}
			if !valid && k.mode != invalid {
				c.errorf(kv.Key.Pos(), "expr.lit.go", "index %s must be a non-negative integer constant", exprString(kv.Key))
			}
			el = kv.Value
		}
		if valid {
			if length >= 0 && next >= length {
				c.errorf(el.Pos(), "expr.lit.go", "index %d out of bounds [0:%d]", next, length)
			} else if seen[next] {
				c.errorf(el.Pos(), "expr.lit.go", "duplicate index %d in array or slice literal", next)
			}
			seen[next] = true
		}
		c.element(el, elem, "array or slice literal")
		next++
		if next > max {
			max = next
		}
	}
	return max
}

func (c *checker) mapLit(e *syntax.CompositeLit, t *Map) {
	seen := map[string]bool{}
	for _, el := range e.Elts {
		kv, ok := el.(*syntax.KeyValueExpr)
		if !ok {
			c.errorf(el.Pos(), "expr.lit.go", "missing key in map literal")
			continue
		}
		var k operand
		c.exprHint(&k, kv.Key, t.Key)
		c.assignment(&k, t.Key, "map literal")
		if k.mode == constant_ {
			key := k.val.ExactString() + " " + k.typ.String()
			if seen[key] {
				c.errorf(kv.Key.Pos(), "expr.lit.go", "duplicate key %s in map literal", exprString(kv.Key))
			}
			seen[key] = true
		}
		c.element(kv.Value, t.Elem, "map literal")
	}
}
