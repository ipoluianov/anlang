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
	if ix, ok := rt.(*syntax.IndexExpr); ok {
		rt = unparen(ix.X) // a method of a generic type
	}
	switch t := rt.(type) {
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
func (c *checker) receiver(recv *syntax.FieldList) (*Var, []*TypeParam) {
	f := recv.List[0]
	rt := receiverType(recv)
	var typ Type
	var rtps []*TypeParam
	if ix, ok := unparen(rt).(*syntax.IndexExpr); ok {
		typ, rtps = c.genericReceiver(ix)
	} else {
		typ = c.typExpr(rt)
	}
	v := &Var{object: object{pos: f.Type.Pos(), typ: typ}, param: true}
	if len(f.Names) == 1 {
		v.pos, v.name = f.Names[0].Pos(), f.Names[0].Name
	}
	if f.Inout.IsValid() {
		v.inout = true
	}
	return v, rtps
}

// genericReceiver checks the receiver type T[P, Q] of a method of a
// generic type: P and Q are new type parameters, bound to the type
// parameters of T by position.
func (c *checker) genericReceiver(ix *syntax.IndexExpr) (Type, []*TypeParam) {
	var x operand
	c.rawExpr(&x, ix.X, nil)
	n, ok := x.typ.(*Named)
	if x.mode != typexpr || !ok || len(n.TypeParams) == 0 || n.orig != nil {
		if x.mode != invalid {
			c.errorf(ix.Pos(), "decl.receiver.base", "%s is not a generic type", exprString(ix.X))
		}
		return Typ[Invalid], nil
	}
	if len(ix.Indices) != len(n.TypeParams) {
		c.errorf(ix.Pos(), "type.param.go", "got %d type parameters in the receiver, want %d", len(ix.Indices), len(n.TypeParams))
		return Typ[Invalid], nil
	}
	var rtps []*TypeParam
	var targs []Type
	for i, idx := range ix.Indices {
		name, ok := idx.(*syntax.Name)
		if !ok {
			c.errorf(idx.Pos(), "type.param.go", "receiver type parameter %s must be an identifier", exprString(idx))
			return Typ[Invalid], nil
		}
		obj := &TypeName{object{pos: name.Pos(), name: name.Name, pkg: c.pkg}}
		tp := &TypeParam{Obj: obj, index: i}
		obj.typ = tp
		c.declare(name, obj)
		rtps = append(rtps, tp)
		targs = append(targs, tp)
	}
	m := newSubstMap(n.TypeParams, targs)
	for i, tp := range rtps {
		tp.bound = c.subst(n.TypeParams[i].bound, m)
		tp.origParam = n.TypeParams[i]
	}
	t := c.instantiate(ix.Pos(), n, targs, false)
	c.recordType(ix, typexpr, t, nil)
	return t, rtps
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
	invalid := false // an element was invalid; an error has been reported
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
		if isUnion(f.Type) {
			u := c.union(f.Type)
			invalid = invalid || u.invalid
			t.Embeddeds = append(t.Embeddeds, u)
			continue
		}
		et := c.typExprAny(f.Type)
		if !isValid(et) {
			invalid = true
			continue
		}
		if isTypeParam(et) {
			c.errorf(f.Type.Pos(), "type.param.go", "cannot embed a type parameter")
			continue
		}
		if n, ok := et.(*Named); ok && n.underlying == nil {
			// Being declared: an interface embedding itself, reported by validType.
			t.Embeddeds = append(t.Embeddeds, et)
			continue
		}
		if _, ok := et.Underlying().(*Interface); !ok {
			// A single type term of a constraint.
			t.Embeddeds = append(t.Embeddeds, &Union{Terms: []*Term{{Type: et}}})
			continue
		}
		t.Embeddeds = append(t.Embeddeds, et)
	}
	c.interfaces = append(c.interfaces, interfaceInfo{t, e})
	if invalid {
		return Typ[Invalid]
	}
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
