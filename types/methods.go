package types

import (
	"an/syntax"
)

// methodDecl is a method declaration found while collecting objects.
type methodDecl struct {
	file *Scope
	decl *syntax.FuncDecl
}

// interfaceInfo is an interface type literal, kept to report conflicting
// methods of embedded interfaces once all types are known.
type interfaceInfo struct {
	typ  *Interface
	expr *syntax.InterfaceType
}

// collectMethods associates the method declarations with their receiver
// base types (decl.receiver.base).
func (c *checker) collectMethods() {
	for _, md := range c.methodDecls {
		d := md.decl
		switch {
		case d.Binding != nil:
			c.errorf(d.Pos(), "decl.extern.no-method", "a method cannot be an external function")
		case d.Body == nil:
			c.errorf(d.Pos(), "decl.func.body", "missing function body")
		}
		if d.Type.TypeParams != nil {
			c.errorf(d.Type.TypeParams.Pos(), "decl.func.go", "methods cannot have type parameters")
		}
		tn := c.receiverBase(d.Recv, d)
		if tn == nil {
			continue
		}
		c.addMethod(tn, md.file, d)
	}
	c.resolveAliasReceivers()
}

// addMethod declares method d of type tn.
func (c *checker) addMethod(tn *TypeName, file *Scope, d *syntax.FuncDecl) {
	obj := &Func{object: object{pos: d.Name.Pos(), name: d.Name.Name, pkg: c.pkg}, Decl: d}
	c.decls[obj] = &declInfo{file: file, fdecl: d}
	c.info.Methods = append(c.info.Methods, obj)
	if d.Name.Name == "_" {
		c.info.Defs[d.Name] = nil
		return
	}
	c.info.Defs[d.Name] = obj
	if prev := lookupFunc(c.methods[tn], d.Name.Name); prev != nil {
		c.errorf(d.Name.Pos(), "decl.func.go", "method %s.%s already declared at %s", tn.name, d.Name.Name, prev.pos)
		return
	}
	c.methods[tn] = append(c.methods[tn], obj)
	if n, ok := tn.typ.(*Named); ok {
		n.methods = c.methods[tn] // the type was declared already
	}
}

// resolveAliasReceivers declares the methods whose receiver is an alias:
// the alias must denote a defined type of this package
// (decl.receiver.base).
func (c *checker) resolveAliasReceivers() {
	for _, am := range c.aliasMethods {
		c.objDecl(am.alias)
		n, ok := am.alias.typ.(*Named)
		if !ok || n.Obj.parent != c.pkgScope {
			if isValid(am.alias.typ) {
				c.errorf(am.name.Pos(), "decl.receiver.base", "the receiver type %s must denote a defined type of this package", am.name.Name)
			}
			continue
		}
		c.addMethod(n.Obj, c.decls[am.alias].file, am.decl)
	}
}

// receiverBase returns the type name of the receiver of a method, or nil
// after reporting an error. A receiver that is an alias of a defined type
// is resolved later, by resolveAliasReceivers.
func (c *checker) receiverBase(recv *syntax.FieldList, d *syntax.FuncDecl) *TypeName {
	if len(recv.List) != 1 || len(recv.List[0].Names) > 1 {
		c.errorf(recv.Pos(), "decl.receiver.base", "a method must have exactly one receiver")
		return nil
	}
	rt := unparen(recv.List[0].Type)
	if star, ok := rt.(*syntax.StarExpr); ok {
		// Reported, but the method is still declared, so that its uses
		// report no further errors.
		c.errorf(star.Pos(), "type.pointer.removed", "there are no pointer receivers in Anlang: use an inout receiver")
		rt = unparen(star.X)
	}
	switch t := rt.(type) {
	case *syntax.IndexExpr:
		c.notSupported(t.Pos(), 2, "methods of generic types")
	case *syntax.Name:
		obj := c.pkgScope.Lookup(t.Name)
		tn, ok := obj.(*TypeName)
		switch {
		case obj == nil && Universe.Lookup(t.Name) != nil:
			c.errorf(t.Pos(), "decl.receiver.base", "cannot define new methods on non-local type %s", t.Name)
		case obj == nil:
			c.undefined(t)
		case !ok:
			c.errorf(t.Pos(), "decl.receiver.base", "%s is not a type", t.Name)
		case c.decls[tn].tspec.Assign.IsValid():
			c.aliasMethods = append(c.aliasMethods, aliasMethod{tn, d, t})
		default:
			return tn
		}
	default:
		c.errorf(t.Pos(), "decl.receiver.base", "invalid receiver type %s", exprString(t))
	}
	return nil
}

// aliasMethod is a method whose receiver type is written as an alias.
type aliasMethod struct {
	alias *TypeName
	decl  *syntax.FuncDecl
	name  *syntax.Name
}

// receiverType returns the receiver type expression without a pointer.
func receiverType(recv *syntax.FieldList) syntax.Expr {
	rt := unparen(recv.List[0].Type)
	if star, ok := rt.(*syntax.StarExpr); ok {
		return star.X
	}
	return rt
}

// receiver returns the receiver variable of a method.
func (c *checker) receiver(recv *syntax.FieldList) *Var {
	f := recv.List[0]
	v := &Var{object: object{pos: f.Type.Pos(), typ: c.typExpr(receiverType(recv))}, param: true}
	if len(f.Names) == 1 {
		v.pos, v.name = f.Names[0].Pos(), f.Names[0].Name
	}
	if f.Inout.IsValid() {
		v.inout = true
		c.notSupported(f.Inout, 4, "inout receivers")
	}
	return v
}

// checkMethods checks the methods once all package-level types are
// known: the receiver base type must not be an interface
// (decl.receiver.base), and a struct type cannot have a field and a
// method with the same name (decl.func.go).
func (c *checker) checkMethods() {
	for _, m := range c.info.Methods {
		c.objDecl(m)
		if m.name == "_" {
			continue
		}
		named, ok := m.Signature().Recv.typ.(*Named)
		if !ok {
			continue
		}
		switch u := named.Underlying().(type) {
		case *Interface:
			c.errorf(m.Decl.Recv.Pos(), "decl.receiver.base", "invalid receiver type %s: an interface type cannot have methods", named)
		case *Struct:
			for _, f := range u.Fields {
				if f.name == m.name {
					c.errorf(m.pos, "decl.func.go", "field and method with the same name %s (field at %s)", m.name, f.pos)
				}
			}
		}
	}
}

// interfaceType checks an interface type literal.
func (c *checker) interfaceType(e *syntax.InterfaceType) Type {
	t := &Interface{}
	for _, f := range e.Elems {
		if len(f.Names) > 0 {
			ft, ok := f.Type.(*syntax.FuncType)
			if !ok {
				c.errorf(f.Pos(), "type.interface.go", "invalid method declaration")
				continue
			}
			sig := c.funcType(ft, false)
			for _, name := range f.Names {
				if name.Name == "_" {
					c.errorf(name.Pos(), "type.interface.go", "methods must have a unique non-blank name")
					continue
				}
				m := &Func{object: object{pos: name.Pos(), name: name.Name, typ: sig, pkg: c.pkg}}
				c.info.Defs[name] = m
				if lookupFunc(t.Methods, name.Name) != nil {
					c.errorf(name.Pos(), "type.interface.go", "duplicate method %s", name.Name)
					continue
				}
				t.Methods = append(t.Methods, m)
			}
			continue
		}
		switch unparen(f.Type).(type) {
		case *syntax.BinaryExpr, *syntax.UnaryExpr:
			c.notSupported(f.Type.Pos(), 2, "type constraints with type sets")
			continue
		}
		et := c.typExpr(f.Type)
		if !isValid(et) {
			continue
		}
		if n, ok := et.(*Named); ok && n.underlying == nil {
			// Being declared: an interface embedding itself, reported by validType.
			t.Embeddeds = append(t.Embeddeds, et)
			continue
		}
		if _, ok := et.Underlying().(*Interface); !ok {
			c.notSupported(f.Type.Pos(), 2, "type constraints with type sets")
			continue
		}
		t.Embeddeds = append(t.Embeddeds, et)
	}
	c.interfaces = append(c.interfaces, interfaceInfo{t, e})
	return t
}

// checkInterfaces reports methods with the same name and different
// signatures in an interface and the interfaces it embeds
// (type.interface.go).
func (c *checker) checkInterfaces() {
	for _, it := range c.interfaces {
		seen := map[string]*Func{}
		for _, m := range it.typ.Methods {
			seen[m.name] = m
		}
		for i, e := range it.typ.Embeddeds {
			u, ok := e.Underlying().(*Interface)
			if !ok {
				continue
			}
			for _, m := range u.MethodSet() {
				if prev, ok := seen[m.name]; ok && prev != m && !Identical(prev.typ, m.typ) {
					c.errorf(embeddedPos(it.expr, i), "type.interface.go", "duplicate method %s with different signatures", m.name)
					continue
				}
				seen[m.name] = m
			}
		}
	}
}

// embeddedPos returns the position of the i'th embedded element of an
// interface type literal.
func embeddedPos(e *syntax.InterfaceType, i int) syntax.Pos {
	for _, f := range e.Elems {
		if len(f.Names) == 0 {
			if i == 0 {
				return f.Pos()
			}
			i--
		}
	}
	return e.Pos()
}
