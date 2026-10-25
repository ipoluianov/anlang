package types

import (
	"go/constant"
	"strconv"

	"an/syntax"
)

// lookup returns the object named name in the current scope.
func (c *checker) lookup(name string) Object {
	_, obj := c.scope.LookupParent(name)
	return obj
}

// undefined reports an undefined name, with the rule that removed it
// from Go if it is a Go predeclared identifier.
func (c *checker) undefined(id *syntax.Name) {
	if r, ok := removed[id.Name]; ok {
		if r.instead != "" {
			c.errorf(id.Pos(), r.rule, "%s does not exist in Anlang: use %s", id.Name, r.instead)
		} else {
			c.errorf(id.Pos(), r.rule, "%s does not exist in Anlang", id.Name)
		}
		return
	}
	c.errorf(id.Pos(), "decl.visibility.go", "undefined: %s", id.Name)
}

// typExpr checks a type expression and returns the type. It returns
// Typ[Invalid] after reporting an error.
func (c *checker) typExpr(e syntax.Expr) Type {
	t := c.typInternal(e)
	if !isValid(t) {
		t = Typ[Invalid]
	}
	c.recordType(e, typexpr, t, nil)
	return t
}

func (c *checker) typInternal(e syntax.Expr) Type {
	switch e := e.(type) {
	case *syntax.Name:
		var x operand
		c.ident(&x, e, true)
		switch x.mode {
		case invalid:
			return Typ[Invalid]
		case typexpr:
			if isEmptyInterface(x.typ) {
				c.errorf(e.Pos(), "type.any.positions", "%s may be used only as a type constraint or as the element type of a final ...any parameter", e.Name)
				return Typ[Invalid]
			}
			return x.typ
		}
		c.errorf(e.Pos(), "type.go", "%s is not a type", e.Name)
		return Typ[Invalid]

	case *syntax.SelectorExpr:
		var x operand
		c.selector(&x, e)
		switch x.mode {
		case invalid:
			return Typ[Invalid]
		case typexpr:
			return x.typ
		}
		c.errorf(e.Pos(), "type.go", "%s is not a type", exprString(e))
		return Typ[Invalid]

	case *syntax.ParenExpr:
		return c.typExpr(e.X)

	case *syntax.ArrayType:
		if e.Len == nil {
			return &Slice{Elem: c.typExpr(e.Elt)}
		}
		if _, ok := e.Len.(*syntax.Ellipsis); ok {
			c.errorf(e.Len.Pos(), "type.composite.go", "invalid use of [...] array outside a composite literal")
			c.typExpr(e.Elt)
			return Typ[Invalid]
		}
		n := c.arrayLength(e.Len)
		elem := c.typExpr(e.Elt)
		if n < 0 {
			return Typ[Invalid]
		}
		return &Array{Len: n, Elem: elem}

	case *syntax.Ellipsis:
		c.errorf(e.Pos(), "decl.func.go", "invalid use of ...")
		return Typ[Invalid]

	case *syntax.MapType:
		key := c.typExpr(e.Key)
		elem := c.typExpr(e.Value)
		if isValid(key) && !comparable(key) {
			if n, ok := key.(*Named); !ok || n.underlying != nil {
				c.errorf(e.Key.Pos(), "type.composite.go", "invalid map key type %s", key)
			}
		}
		return &Map{Key: key, Elem: elem}

	case *syntax.ChanType:
		dir := SendRecv
		switch e.Dir {
		case syntax.ChanSend:
			dir = SendOnly
		case syntax.ChanRecv:
			dir = RecvOnly
		}
		return &Chan{Dir: dir, Elem: c.typExpr(e.Value)}

	case *syntax.FuncType:
		return c.funcType(e, false)

	case *syntax.StructType:
		return c.structType(e)

	case *syntax.InterfaceType:
		if len(e.Elems) == 0 {
			c.errorf(e.Pos(), "type.interface.empty-value", "interface{} may be used only as a type constraint or as the element type of a final ...any parameter")
			return Typ[Invalid]
		}
		c.notSupported(e.Pos(), 2, "interface types")
		return Typ[Invalid]

	case *syntax.EnumType:
		c.errorf(e.Pos(), "type.enum.defined", "an enum type may appear only as the underlying type in a type declaration")
		return Typ[Invalid]

	case *syntax.StarExpr:
		c.errorf(e.Pos(), "type.pointer.removed", "there are no pointer types in Anlang: use inout parameters, slices or ffi.Handle")
		return Typ[Invalid]

	case *syntax.IndexExpr:
		var x operand
		c.rawExpr(&x, e.X, nil)
		if x.mode != invalid {
			c.notSupported(e.Pos(), 2, "generic types")
		}
		return Typ[Invalid]

	case *syntax.BadExpr:
		return Typ[Invalid]
	}
	c.errorf(e.Pos(), "type.go", "%s is not a type", exprString(e))
	return Typ[Invalid]
}

// arrayLength checks an array length and returns it, or -1 after an error.
func (c *checker) arrayLength(e syntax.Expr) int64 {
	var x operand
	c.expr(&x, e)
	if x.mode == invalid {
		return -1
	}
	if x.mode != constant_ {
		c.errorf(e.Pos(), "type.composite.go", "array length %s must be constant", exprString(e))
		return -1
	}
	if isUntyped(x.typ) || isInteger(x.typ) {
		if v := constant.ToInt(x.val); v.Kind() == constant.Int {
			if n, ok := constant.Int64Val(v); ok && n >= 0 {
				if _, ok := c.representable(v, Typ[Int]); ok {
					c.convertUntyped(&x, Typ[Int], "type.composite.go", "array length")
					return n
				}
			}
		}
	}
	c.errorf(e.Pos(), "type.composite.go", "invalid array length %s", exprString(e))
	return -1
}

func (c *checker) structType(e *syntax.StructType) Type {
	t := &Struct{}
	for _, cl := range e.Clauses {
		switch cl.Word {
		case "invariant":
			c.notSupported(cl.WordPos, 23, "type invariants")
		case "layout":
			if t.Layout != "" {
				c.errorf(cl.WordPos, "type.struct.layout-once", "a struct type may contain at most one layout clause")
				continue
			}
			kind, _ := cl.X.(*syntax.Name)
			if kind == nil || (kind.Name != "c" && kind.Name != "packed") {
				c.errorf(cl.X.Pos(), "type.struct.go", "invalid layout kind %s: want c or packed", exprString(cl.X))
				continue
			}
			t.Layout = kind.Name
		}
	}
	seen := map[string]syntax.Pos{}
	for _, f := range e.Fields {
		typ := c.typExpr(f.Type)
		tag := ""
		if f.Tag != nil {
			if s, err := strconv.Unquote(f.Tag.Value); err == nil {
				tag = s
			}
		}
		if len(f.Names) == 0 {
			c.notSupported(f.Type.Pos(), 2, "embedded fields")
			continue
		}
		for _, name := range f.Names {
			v := &Var{object: object{pos: name.Pos(), name: name.Name, typ: typ}, field: true}
			c.info.Defs[name] = v
			if name.Name != "_" {
				if prev, ok := seen[name.Name]; ok {
					c.errorf(name.Pos(), "type.struct.go", "%s redeclared (previous declaration at %s)", name.Name, prev)
				}
				seen[name.Name] = name.Pos()
			}
			t.Fields = append(t.Fields, v)
			t.Tags = append(t.Tags, tag)
		}
		if t.Layout != "" && isValid(typ) && !layoutField(typ) {
			c.errorf(f.Type.Pos(), "type.layout.fields", "field of type %s in a struct with layout %s: want integer, floating-point, array or layout struct types", typ, t.Layout)
		}
	}
	return t
}

// layoutField reports whether a field of type t is allowed in a struct
// with a layout clause (type.layout.fields).
func layoutField(t Type) bool {
	switch t := t.Underlying().(type) {
	case *Basic:
		return t.info&(IsInteger|IsFloat) != 0
	case *Array:
		return layoutField(t.Elem)
	case *Struct:
		return t.Layout != ""
	}
	return false
}
