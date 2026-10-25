package types

import (
	"go/constant"
	"slices"
	"strings"

	"an/syntax"
)

// objDecl checks the declaration of a package-level object, unless it
// has been checked already. Declarations are checked on demand, so that
// an object may be used before its declaration in the source.
func (c *checker) objDecl(obj Object) {
	if obj.Type() != nil {
		return
	}
	di := c.decls[obj]
	if di == nil {
		return
	}
	if i := slices.Index(c.objPath, obj); i >= 0 {
		if tn, ok := obj.(*TypeName); ok && di.tspec.Assign.IsValid() && throughDefined(c.objPath[i:]) {
			// An alias used in a cycle through a defined type is valid;
			// until its declaration is complete, it has a lazy type.
			if c.lazy[tn] == nil {
				c.lazy[tn] = &lazyAlias{obj: tn}
			}
			return
		}
		c.cycleError(c.objPath[i:])
		obj.base().setType(Typ[Invalid])
		return
	}
	c.objPath = append(c.objPath, obj)
	savedScope, savedFn, savedIota, savedUntyped, savedErrPos := c.scope, c.fn, c.iota, c.untyped, c.errPos
	c.scope, c.fn, c.iota, c.untyped, c.errPos = di.file, nil, nil, map[syntax.Expr]untypedInfo{}, syntax.Pos{}
	defer func() {
		c.flushUntyped()
		c.scope, c.fn, c.iota, c.untyped, c.errPos = savedScope, savedFn, savedIota, savedUntyped, savedErrPos
		c.objPath = c.objPath[:len(c.objPath)-1]
	}()

	switch obj := obj.(type) {
	case *Const:
		c.constDecl(obj, di.typ, di.init, di.iota, di.spec != di.from)
	case *Var:
		c.errorf(obj.pos, "decl.var.package-kinds", "package-level variable %s: only atomic, sync and hardware mapping types are allowed at package level", obj.name)
		vars := di.vars
		if vars == nil {
			vars = []*Var{obj}
		}
		var inits []syntax.Expr
		if di.init != nil {
			inits = []syntax.Expr{di.init}
		}
		c.varDecl(vars, di.typ, inits)
	case *TypeName:
		c.typeDecl(obj, di.tspec)
	case *Func:
		// The type parameters of the function and of the receiver are
		// declared in a scope between the file scope and the body.
		d := di.fdecl
		c.scope = NewScope(di.file)
		di.scope = c.scope
		var recv *Var
		var rtps, tps []*TypeParam
		if d.Recv != nil {
			recv, rtps = c.receiver(d.Recv)
		} else if d.Type.TypeParams != nil {
			tps = c.declareTypeParams(d.Type.TypeParams)
		}
		sig := c.funcType(d.Type, false)
		sig.Recv, sig.RecvTypeParams, sig.TypeParams = recv, rtps, tps
		obj.typ = sig
	}
}

func (c *checker) cycleError(cycle []Object) {
	var names []string
	for _, obj := range cycle {
		names = append(names, obj.Name())
	}
	first := cycle[0]
	rule := "decl.const.go"
	switch first.(type) {
	case *TypeName:
		rule = "type.go"
	case *Var:
		rule = "decl.var.package-init"
	}
	c.errorf(first.Pos(), rule, "initialization cycle or invalid recursive declaration: %s refers to itself through %s", first.Name(), strings.Join(append(names, first.Name()), " -> "))
}

// constDecl checks a constant declaration (decl.const.go). If the type
// and initializer are inherited from an earlier spec, errors in them are
// reported at the constant's name.
func (c *checker) constDecl(obj *Const, typ, init syntax.Expr, iota int, inherited bool) {
	if inherited {
		saved := c.errPos
		c.errPos = obj.pos
		defer func() { c.errPos = saved }()
	}
	// The type stays nil while the initializer is checked, so that a
	// constant referring to itself is reported as a cycle by objDecl.
	c.iota = constant.MakeInt64(int64(iota))
	defer func() {
		c.iota = nil
		if obj.typ == nil {
			obj.typ = Typ[Invalid]
		}
	}()

	var t Type
	if typ != nil {
		t = c.typExpr(typ)
		if !isValid(t) {
			return
		}
		if !isConstType(t) {
			c.notSupported(typ.Pos(), 10, "constants of type "+t.String())
			return
		}
	}
	if init == nil {
		return
	}
	var x operand
	c.expr(&x, init)
	if x.mode == invalid {
		return
	}
	if x.mode != constant_ {
		if call, ok := unparen(init).(*syntax.CallExpr); ok && x.mode != typexpr && !c.isConversion(call) {
			c.notSupported(init.Pos(), 10, "compile-time evaluation of calls in constant declarations")
		} else {
			c.errorf(init.Pos(), "decl.const.go", "%s is not constant", &x)
		}
		return
	}
	if t != nil {
		// Without a type, the constant keeps the operand's untyped type.
		c.assignment(&x, t, "constant declaration")
		if x.mode == invalid {
			return
		}
	}
	obj.typ, obj.Val = x.typ, x.val
}

// isConversion reports whether call is a conversion T(x).
func (c *checker) isConversion(call *syntax.CallExpr) bool {
	tv, ok := c.info.Types[call.Fun]
	return ok && tv.IsType()
}

// varDecl checks the declaration of vars with an optional type and
// initializers: one per variable, or one multi-value initializer.
func (c *checker) varDecl(vars []*Var, typ syntax.Expr, inits []syntax.Expr) {
	var t Type
	if typ != nil {
		t = c.typExpr(typ)
	}
	if len(inits) == 0 {
		for _, v := range vars {
			if t == nil {
				v.typ = Typ[Invalid]
			} else {
				v.typ = t
			}
		}
		return
	}
	c.initVars(vars, t, inits)
}

// initVars assigns the initializers to vars, which get type t or, if t is
// nil, the types of the values.
func (c *checker) initVars(vars []*Var, t Type, inits []syntax.Expr) {
	setTypes := func(x []*operand) {
		for i, v := range vars {
			switch {
			case t != nil:
				v.typ = t
			case i < len(x) && x[i].mode != invalid:
				v.typ = x[i].typ
			default:
				v.typ = Typ[Invalid]
			}
		}
	}
	if len(inits) == 1 && len(vars) > 1 {
		xs := c.multiValue(inits[0], len(vars), true)
		if xs == nil {
			setTypes(nil)
			return
		}
		for _, x := range xs {
			c.assignment(x, t, "variable declaration")
		}
		setTypes(xs)
		return
	}
	var xs []*operand
	for i, e := range inits {
		x := &operand{}
		c.exprHint(x, e, t)
		if i < len(vars) {
			c.assignment(x, t, "variable declaration")
		}
		xs = append(xs, x)
	}
	if len(inits) != len(vars) {
		c.errorf(inits[0].Pos(), "stmt.assign.go", "assignment mismatch: %d variables but %d values", len(vars), len(inits))
		setTypes(nil)
		return
	}
	setTypes(xs)
}

// multiValue checks e, which must produce n values: a call with n
// results, or, if commaOK is set and n is 2, a comma-ok expression. It
// returns nil after reporting an error.
func (c *checker) multiValue(e syntax.Expr, n int, commaOK bool) []*operand {
	var x operand
	c.rawExpr(&x, e, nil)
	if x.mode == invalid {
		return nil
	}
	if t, ok := x.typ.(*Tuple); ok && x.mode == value {
		if t.Len() != n {
			c.errorf(e.Pos(), "stmt.assign.go", "assignment mismatch: %d variables but %s returns %d values", n, exprString(e), t.Len())
			return nil
		}
		var xs []*operand
		for _, v := range t.Vars {
			xs = append(xs, &operand{mode: value, expr: e, typ: v.typ})
		}
		return xs
	}
	if (x.mode == commaok || x.mode == mapindex) && commaOK && n == 2 {
		c.recordCommaOK(&x)
		return []*operand{
			{mode: value, expr: e, typ: x.typ},
			{mode: value, expr: e, typ: Typ[UntypedBool]},
		}
	}
	c.errorf(e.Pos(), "stmt.assign.go", "assignment mismatch: %d variables but 1 value", n)
	return nil
}

// recordCommaOK records the type of a comma-ok expression as a pair.
func (c *checker) recordCommaOK(x *operand) {
	c.recordType(x.expr, commaok, &Tuple{Vars: []*Var{
		{object: object{typ: x.typ}},
		{object: object{typ: Typ[Bool]}},
	}}, nil)
}

// typeDecl checks a type declaration.
func (c *checker) typeDecl(obj *TypeName, spec *syntax.TypeSpec) {
	if spec.TypeParams != nil && spec.Assign.IsValid() {
		// A generic alias, as in Go 1.24 (type.param.go).
		c.scope = NewScope(c.scope)
		defer c.closeScope()
		ga := &genericAlias{obj: obj}
		obj.typ = ga
		ga.tps = c.declareTypeParams(spec.TypeParams)
		ga.rhs = c.typExprAny(spec.Type)
		return
	}
	if spec.Assign.IsValid() {
		t := c.typExprAny(spec.Type)
		obj.typ = t
		if l := c.lazy[obj]; l != nil {
			l.actual = t
			for _, n := range l.waiting {
				n.underlying = t.Underlying()
				c.validType(n, n, nil)
			}
		}
		return
	}
	named := &Named{Obj: obj, methods: c.methods[obj]}
	obj.typ = named
	if _, ok := spec.Type.(*syntax.EnumType); ok {
		named.underlying = Typ[Invalid]
		c.notSupported(spec.Type.Pos(), 3, "enum types")
		return
	}
	if spec.TypeParams != nil {
		// The type parameters are in scope in the type declaration.
		c.scope = NewScope(c.scope)
		defer c.closeScope()
		named.TypeParams = c.declareTypeParams(spec.TypeParams)
	}
	rhs := c.typExprAny(spec.Type)
	if isTypeParam(rhs) {
		c.errorf(spec.Type.Pos(), "type.param.go", "cannot use a type parameter as the type in a type declaration")
		named.underlying = Typ[Invalid]
		return
	}
	if l, ok := rhs.(*lazyAlias); ok && l.actual == nil {
		// The underlying type is known when the alias is.
		l.waiting = append(l.waiting, named)
		return
	}
	if n, ok := rhs.(*Named); ok && n.orig != nil {
		n.Underlying() // expands the instance
	}
	if n, ok := rhs.(*Named); ok && n.underlying == nil {
		c.errorf(spec.Name.Pos(), "type.go", "invalid recursive type %s", obj.name)
		named.underlying = Typ[Invalid]
		return
	}
	named.underlying = rhs.Underlying()
	c.validType(named, named, nil)
}

// validType reports an invalid recursive type: a defined type that
// contains itself through arrays and struct fields, so its values would
// be infinitely large. Slices, maps, channels and functions break the
// cycle.
func (c *checker) validType(root *Named, t Type, path []*Named) {
	switch t := t.(type) {
	case *Named:
		if t.orig != nil {
			// An instance contains the generic type: T[P] in T is a cycle.
			if t.orig == root || slices.Contains(path, t.orig) {
				if t.orig == root {
					c.errorf(root.Obj.pos, "type.go", "invalid recursive type %s", root.Obj.name)
				}
				return
			}
			if len(path) > maxInstanceDepth {
				return
			}
			c.validType(root, t.Underlying(), append(path, t))
			return
		}
		if slices.Contains(path, t) {
			if t == root {
				c.errorf(root.Obj.pos, "type.go", "invalid recursive type %s", root.Obj.name)
			}
			return
		}
		if t.underlying != nil {
			c.validType(root, t.underlying, append(path, t))
		}
	case *Array:
		c.validType(root, t.Elem, path)
	case *Struct:
		for _, f := range t.Fields {
			c.validType(root, f.typ, path)
		}
	case *Interface:
		for _, e := range t.Embeddeds {
			c.validType(root, e, path)
		}
	}
}

// funcType returns the signature of a function type. Parameter and
// result names are declared later, when the body is checked.
func (c *checker) funcType(ft *syntax.FuncType, literal bool) *Signature {
	sig := &Signature{}
	sig.Params, sig.Variadic = c.params(ft.Params, true)
	sig.Results, _ = c.params(ft.Results, false)
	// Parameter and result names share one block (decl.visibility.go).
	seen := map[string]bool{}
	for _, v := range append(append([]*Var(nil), sig.Params.Vars...), sig.Results.Vars...) {
		if v.name == "" || v.name == "_" {
			continue
		}
		if seen[v.name] {
			c.errorf(v.pos, "decl.visibility.go", "%s redeclared in this block", v.name)
		}
		seen[v.name] = true
	}
	return sig
}

func (c *checker) params(list *syntax.FieldList, isParams bool) (*Tuple, bool) {
	t := &Tuple{}
	if list == nil {
		return t, false
	}
	variadic := false
	for i, f := range list.List {
		ftype := f.Type
		if e, ok := ftype.(*syntax.Ellipsis); ok {
			ftype = e.Elt
			switch {
			case !isParams || i != len(list.List)-1 || len(f.Names) > 1:
				c.errorf(e.Pos(), "decl.func.go", "can only use ... with final parameter in list")
			case f.Inout.IsValid():
				c.errorf(f.Inout, "decl.param.inout-variadic", "inout and ... cannot be combined in one parameter")
			default:
				variadic = true
			}
		}
		var typ Type
		if id, ok := unparen(ftype).(*syntax.Name); ok && id.Name == "any" && variadic && c.lookup(id.Name) == universeAny {
			c.notSupported(ftype.Pos(), 10, "...any parameters")
			typ = Typ[Invalid]
		} else {
			typ = c.typExpr(ftype)
		}
		if variadic && i == len(list.List)-1 {
			typ = &Slice{Elem: typ}
		}
		inout := false
		_, isEllipsis := f.Type.(*syntax.Ellipsis)
		if f.Inout.IsValid() && !isEllipsis {
			if isParams {
				inout = true
				c.notSupported(f.Inout, 4, "inout parameters")
			} else {
				c.errorf(f.Inout, "decl.param.inout-result", "results cannot be inout")
			}
		}
		if len(f.Names) == 0 {
			t.Vars = append(t.Vars, &Var{object: object{pos: ftype.Pos(), typ: typ}, inout: inout, param: true})
			continue
		}
		for _, name := range f.Names {
			t.Vars = append(t.Vars, &Var{object: object{pos: name.Pos(), name: name.Name, typ: typ}, inout: inout, param: true})
		}
	}
	return t, variadic
}

// declare declares obj in the current scope. The blank identifier is
// not declared.
func (c *checker) declare(name *syntax.Name, obj Object) {
	c.info.Defs[name] = obj
	if v, ok := obj.(*Var); ok && c.fn != nil {
		v.owner = c.fn
	}
	if name.Name == "_" {
		c.info.Defs[name] = nil
		obj.base().parent = c.scope
		return
	}
	if alt := c.scope.Insert(obj); alt != nil {
		c.errorf(name.Pos(), "decl.visibility.go", "%s redeclared in this block (previous declaration at %s)", name.Name, alt.Pos())
		if v, ok := obj.(*Var); ok {
			v.used = true // not declared, so not reported as unused
		}
	}
}

// declStmt checks a declaration inside a function body.
func (c *checker) declStmt(d *syntax.GenDecl) {
	switch d.Tok {
	case syntax.Const:
		var last *syntax.ValueSpec
		for i, s := range d.Specs {
			s := s.(*syntax.ValueSpec)
			if s.Type != nil || len(s.Values) > 0 {
				last = s
			}
			c.checkConstArity(s, last)
			var objs []*Const
			for j, name := range s.Names {
				obj := &Const{object: object{pos: name.Pos(), name: name.Name}}
				var typ, init syntax.Expr
				if last != nil {
					typ = last.Type
					if j < len(last.Values) {
						init = last.Values[j]
					}
				}
				c.constDecl(obj, typ, init, i, last != s)
				objs = append(objs, obj)
			}
			// Constants are in scope after the spec (decl.visibility.go).
			for j, name := range s.Names {
				c.declare(name, objs[j])
			}
		}
	case syntax.Var:
		for _, s := range d.Specs {
			s := s.(*syntax.ValueSpec)
			var vars []*Var
			for _, name := range s.Names {
				vars = append(vars, &Var{object: object{pos: name.Pos(), name: name.Name}})
			}
			multi := len(s.Names) > 1 && len(s.Values) == 1
			if !multi {
				c.checkVarArity(s)
			}
			if multi || len(s.Values) == len(s.Names) || len(s.Values) == 0 {
				c.varDecl(vars, s.Type, s.Values)
			} else {
				c.varDecl(vars, s.Type, nil)
				for _, e := range s.Values {
					var x operand
					c.expr(&x, e)
				}
			}
			for i, name := range s.Names {
				c.declare(name, vars[i])
				c.fn.vars = append(c.fn.vars, vars[i])
			}
		}
	case syntax.Type:
		for _, s := range d.Specs {
			s := s.(*syntax.TypeSpec)
			obj := &TypeName{object{pos: s.Name.Pos(), name: s.Name.Name}}
			// The type is in scope in its own declaration (decl.visibility.go).
			c.declare(s.Name, obj)
			c.typeDecl(obj, s)
		}
	}
}

// throughDefined reports whether a cycle of declarations contains a
// defined type, which makes a cycle through aliases valid.
func throughDefined(cycle []Object) bool {
	for _, obj := range cycle {
		if tn, ok := obj.(*TypeName); ok {
			if _, ok := tn.typ.(*Named); ok {
				return true
			}
		}
	}
	return false
}
