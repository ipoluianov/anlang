package types

import (
	"fmt"
	"sort"
	"strings"

	"an/syntax"
)

// Enum is an enum type: a sum of variants (type.enum). It is always the
// underlying type of a defined type (type.enum.defined).
type Enum struct {
	Variants []*Variant
}

func (t *Enum) Underlying() Type { return t }
func (t *Enum) String() string   { return typeString(t) }

// Variant is a variant of an enum type. Its name is declared in the
// block that declares the enum type (type.enum.variant-scope).
type Variant struct {
	object
	enum   *Named // the declared enum type
	index  int
	fields []*Var // fields of a variant with data
	data   bool   // the variant is written with braces and is a constructor
	ctor   *Signature
	orig   *Variant // the variant of the generic enum type, for an instance
}

// Fields returns the fields of the variant.
func (v *Variant) Fields() []*Var { return v.fields }

// HasData reports whether the variant carries data (type.enum.constructor).
func (v *Variant) HasData() bool { return v.data }

// Index returns the position of the variant in its enum type.
func (v *Variant) Index() int { return v.index }

// Enum returns the declared enum type of the variant.
func (v *Variant) Enum() *Named { return v.enum }

func (v *Variant) origin() *Variant {
	if v.orig != nil {
		return v.orig
	}
	return v
}

// declareVariants creates the variant objects of an enum type
// declaration; their fields are checked with the type declaration.
func (c *checker) newVariants(obj *TypeName, et *syntax.EnumType) []*Variant {
	var vs []*Variant
	for i, sv := range et.Variants {
		v := &Variant{object: object{pos: sv.Name.Pos(), name: sv.Name.Name, pkg: c.pkg}, index: i, data: sv.Fields != nil}
		vs = append(vs, v)
	}
	c.enumVariants[obj] = vs
	return vs
}

// enumType checks the variants of the enum type named (type.enum.fields,
// type.enum.nonempty).
func (c *checker) enumType(et *syntax.EnumType, named *Named) *Enum {
	vs := c.enumVariants[named.Obj]
	en := &Enum{Variants: vs}
	if len(et.Variants) == 0 {
		c.errorf(et.Pos(), "type.enum.nonempty", "an enum type must declare at least one variant")
	}
	seen := map[string]bool{}
	for i, sv := range et.Variants {
		v := vs[i]
		v.enum = named
		if seen[v.name] {
			c.errorf(sv.Name.Pos(), "type.enum.nonempty", "duplicate variant %s in enum type %s", v.name, named.Obj.name)
		}
		seen[v.name] = true
		if sv.Fields == nil {
			continue
		}
		fieldNames := map[string]bool{}
		for _, f := range sv.Fields.List {
			ft := f.Type
			if e, ok := ft.(*syntax.Ellipsis); ok {
				c.errorf(e.Pos(), "type.enum.fields", "a variant field cannot be variadic")
				ft = e.Elt
			}
			if f.Inout.IsValid() {
				c.errorf(f.Inout, "type.enum.fields", "a variant field cannot be inout")
			}
			typ := c.typExpr(ft)
			if len(f.Names) == 0 {
				c.errorf(ft.Pos(), "type.enum.fields", "every field of variant %s must have a name", v.name)
				continue
			}
			for _, name := range f.Names {
				if fieldNames[name.Name] && name.Name != "_" {
					c.errorf(name.Pos(), "type.enum.nonempty", "duplicate field %s in variant %s", name.Name, v.name)
				}
				fieldNames[name.Name] = true
				fv := &Var{object: object{pos: name.Pos(), name: name.Name, typ: typ}, field: true}
				c.info.Defs[name] = fv
				v.fields = append(v.fields, fv)
			}
		}
	}
	for _, v := range vs {
		v.typ = named
	}
	return en
}

// enumOf returns the enum type of t, or nil.
func enumOf(t Type) *Enum {
	e, _ := core(t).(*Enum)
	return e
}

// isGenericEnumValue reports whether t is a generic enum type used as the
// type of a variant value whose type arguments are not known yet
// (expr.variant.infer).
func isGenericEnumValue(t Type) bool {
	n, ok := t.(*Named)
	if !ok || n.orig != nil || len(n.TypeParams) == 0 {
		return false
	}
	_, ok = n.underlying.(*Enum)
	return ok
}

// variantOperand gives x the value of variant v: the enum value of a
// variant without data, or the constructor of a variant with data
// (type.enum.value, type.enum.constructor).
func (c *checker) variantOperand(x *operand, v *Variant) {
	if !isValid(v.typ) {
		x.invalidate()
		return
	}
	if !v.data {
		// For a generic enum type, the type arguments come from the context.
		x.mode, x.typ = value, v.enum
		return
	}
	x.mode, x.typ = value, c.ctorSig(v)
}

// ctorSig returns the signature of the constructor of variant v: its
// fields are the parameters, and the result is the enum type.
func (c *checker) ctorSig(v *Variant) *Signature {
	if v.ctor != nil {
		return v.ctor
	}
	var params []*Var
	for _, f := range v.fields {
		params = append(params, &Var{object: object{pos: f.pos, name: f.name, typ: f.typ}, param: true})
	}
	var result Type = v.enum
	tps := v.enum.TypeParams
	if len(tps) > 0 {
		targs := make([]Type, len(tps))
		for i, tp := range tps {
			targs[i] = tp
		}
		result = c.instantiate(v.pos, v.enum, targs, false)
	}
	v.ctor = &Signature{
		Params:     &Tuple{Vars: params},
		Results:    &Tuple{Vars: []*Var{{object: object{typ: result}, param: true}}},
		TypeParams: tps,
	}
	return v.ctor
}

// variantOf returns the variant that a pattern or literal type e names,
// or nil; it reports no errors.
func (c *checker) variantOf(e syntax.Expr) *Variant {
	switch e := unparen(e).(type) {
	case *syntax.Name:
		if v, ok := c.lookup(e.Name).(*Variant); ok {
			c.info.Uses[e] = v
			c.objDecl(v)
			return v
		}
	case *syntax.SelectorExpr:
		id, ok := unparen(e.X).(*syntax.Name)
		if !ok {
			return nil
		}
		p, ok := c.lookup(id.Name).(*PkgName)
		if !ok || p.Imported.Scope == nil {
			return nil
		}
		if v, ok := p.Imported.Scope.Lookup(e.Sel.Name).(*Variant); ok && v.Exported() {
			p.used = true
			c.info.Uses[id], c.info.Uses[e.Sel] = p, v
			return v
		}
	}
	return nil
}

// variantLit checks the literal V{fields} of a variant with data
// (expr.variant.construct). hint is the type required by the context.
func (c *checker) variantLit(x *operand, e *syntax.CompositeLit, v *Variant, hint Type) {
	if !isValid(v.typ) {
		c.skipElems(e)
		return
	}
	if !v.data {
		c.errorf(e.Pos(), "expr.variant.construct", "variant %s has no fields: write %s", v.name, v.name)
		c.skipElems(e)
		return
	}
	enum := Type(v.enum)
	fields := v.fields
	if len(v.enum.TypeParams) > 0 {
		enum = c.inferVariant(e, v, hint)
		if enum == nil {
			x.invalidate()
			return
		}
		fields = enumOf(enum).Variants[v.index].fields
	}
	tags := make([]string, len(fields))
	c.structLit(e, &Struct{Fields: fields, Tags: tags})
	x.mode, x.typ = value, enum
}

// inferVariant returns the instance of the generic enum type of v for a
// literal of v: from the context, or from the field values
// (expr.variant.infer). It returns nil after an error.
func (c *checker) inferVariant(e *syntax.CompositeLit, v *Variant, hint Type) Type {
	if h, ok := hint.(*Named); ok && h.orig == v.enum {
		return h
	}
	u := &unifier{c: c, tps: v.enum.TypeParams, bound: map[*TypeParam]Type{}}
	var untyped []struct {
		f *Var
		x *operand
	}
	n := len(c.errors)
	for i, el := range e.Elts {
		var f *Var
		if kv, ok := el.(*syntax.KeyValueExpr); ok {
			if key, ok := kv.Key.(*syntax.Name); ok {
				for _, vf := range v.fields {
					if vf.name == key.Name {
						f = vf
					}
				}
			}
			el = kv.Value
		} else if i < len(v.fields) {
			f = v.fields[i]
		}
		if f == nil {
			continue
		}
		var y operand
		c.rawExpr(&y, el, nil)
		switch {
		case y.mode == invalid:
		case isUntyped(y.typ):
			untyped = append(untyped, struct {
				f *Var
				x *operand
			}{f, &y})
		default:
			u.unify(f.typ, y.typ)
		}
	}
	// The elements are checked again with their final types.
	c.errors = c.errors[:n]
	for _, ut := range untyped {
		if tp := u.param(ut.f.typ); tp != nil && u.bound[tp] == nil && !ut.x.isNil() {
			u.bound[tp] = defaultType(ut.x.typ)
		}
	}
	targs := make([]Type, len(v.enum.TypeParams))
	for i, tp := range v.enum.TypeParams {
		if targs[i] = u.bound[tp]; targs[i] == nil {
			c.errorf(e.Pos(), "expr.variant.infer", "cannot infer %s of %s from the fields: give the type, as in a variable declaration", tp, v.name)
			return nil
		}
	}
	return c.instantiate(e.Pos(), v.enum, targs, true)
}

// resolveGenericEnum converts a variant value of a generic enum type to
// the instance t required by the context, and reports whether it did.
func resolveGenericEnum(x *operand, t Type) bool {
	if !isGenericEnumValue(x.typ) {
		return false
	}
	if n, ok := t.(*Named); ok && n.orig == x.typ {
		x.typ = t
		return true
	}
	return false
}

// genericEnumError reports a variant value whose type arguments cannot
// be inferred.
func (c *checker) genericEnumError(x *operand) {
	rule := "expr.variant.infer"
	if x.typ == universeOption {
		rule = "type.option.none-assign"
	}
	c.errorf(x.expr.Pos(), rule, "cannot infer the type arguments of %s: it needs a context of type %s[…]", exprString(x.expr), x.typ)
	x.invalidate()
}

// variantSwitch checks a switch over an enum value with variant patterns
// (spec/05-statements.md §1).
func (c *checker) variantSwitch(ctxt stmtContext, s *syntax.SwitchStmt, tag *operand, enum *Enum) {
	origin := tagEnumType(tag.typ)
	seen := map[int]syntax.Pos{}
	var dflt syntax.Node
	badCase := false // an invalid case: exhaustiveness is not reported
	clauses := s.Body.List
	for _, cs := range clauses {
		cl, ok := cs.(*syntax.CaseClause)
		if !ok {
			continue
		}
		if cl.List == nil {
			if dflt != nil {
				c.errorf(cl.Pos(), "stmt.go", "multiple defaults in switch (first at %s)", dflt.Pos())
			}
			dflt = cl
		}
		var bound *Variant
		var bindings, invalidBindings []syntax.Expr
		for _, e := range cl.List {
			name, elts, braces := patternParts(e)
			v := c.variantOf(name)
			switch {
			case v == nil:
				if c.lookup(exprString(name)) == nil && !isPatternShape(e) {
					c.useArgs([]syntax.Expr{e})
				}
				c.errorf(e.Pos(), "stmt.switch.variant.tag", "case %s in a switch over %s must be a variant pattern", exprString(e), tag.typ)
				invalidBindings, badCase = append(invalidBindings, elts...), true
				continue
			case v.enum != origin:
				c.errorf(e.Pos(), "stmt.switch.variant.name", "%s is not a variant of %s", exprString(name), tag.typ)
				invalidBindings, badCase = append(invalidBindings, elts...), true
				continue
			}
			tv := enum.Variants[v.index]
			if prev, ok := seen[v.index]; ok {
				c.errorf(e.Pos(), "stmt.switch.variant.duplicate", "duplicate case %s in variant switch (previous case at %s)", v.name, prev)
			} else {
				seen[v.index] = e.Pos()
			}
			if !braces {
				continue
			}
			switch {
			case len(cl.List) > 1:
				c.errorf(e.Pos(), "stmt.switch.variant.multi", "a case with several patterns cannot bind fields")
				invalidBindings = append(invalidBindings, elts...)
			case !tv.data:
				c.errorf(e.Pos(), "stmt.switch.variant.bindings", "variant %s has no fields to bind", v.name)
				invalidBindings = append(invalidBindings, elts...)
			case len(elts) != len(tv.fields):
				c.errorf(e.Pos(), "stmt.switch.variant.bindings", "pattern %s binds %d fields, variant %s has %d", exprString(e), len(elts), v.name, len(tv.fields))
				invalidBindings = append(invalidBindings, elts...)
			default:
				bound, bindings = tv, elts
			}
		}

		c.openScope()
		// Bindings of an invalid pattern are declared invalid, so their
		// uses report no further errors.
		for _, b := range invalidBindings {
			if id, ok := b.(*syntax.Name); ok && id.Name != "_" && c.scope.Lookup(id.Name) == nil {
				c.declare(id, &Var{object: object{pos: id.Pos(), name: id.Name, typ: Typ[Invalid]}, used: true})
			}
		}
		if bound != nil {
			for i, b := range bindings {
				id, ok := b.(*syntax.Name)
				if !ok {
					c.errorf(b.Pos(), "stmt.switch.variant.bindings", "a pattern binding must be an identifier, not %s", exprString(b))
					continue
				}
				v := &Var{object: object{pos: id.Pos(), name: id.Name, typ: bound.fields[i].typ}}
				c.declare(id, v)
				if id.Name != "_" {
					c.fn.vars = append(c.fn.vars, v)
				}
			}
		}
		c.stmtList((ctxt|breakOK|variantSwitchOK)&^fallthroughOK, cl.Body)
		c.closeScope()
	}

	if dflt == nil && !badCase {
		var missing []string
		hidden := false
		for _, v := range enum.Variants {
			if _, ok := seen[v.index]; ok {
				continue
			}
			if !v.Exported() && origin.Obj.pkg != c.pkg {
				hidden = true
			}
			missing = append(missing, v.name)
		}
		switch {
		case hidden:
			c.errorf(s.Pos(), "stmt.switch.variant.exhaustive", "a switch over %s outside its package must have a default case: %s has unexported variants (type.enum.variant-export)", tag.typ, origin.Obj.name)
		case len(missing) > 0:
			sort.Strings(missing)
			c.errorf(s.Pos(), "stmt.switch.variant.exhaustive", "switch over %s is not exhaustive: missing %s", tag.typ, strings.Join(missing, ", "))
		}
	}
	// The switch is exhaustive or has a default case for the terminating
	// statement rule (stmt.terminating.variant): otherwise an error has
	// been reported, and it is not repeated as a missing return.
	c.variantSwitches[s] = true
}

// tagEnumType returns the declared generic or non-generic enum type of
// the tag type t.
func tagEnumType(t Type) *Named {
	if n, ok := t.(*Named); ok {
		return n.Origin()
	}
	return nil
}

// patternParts splits a variant pattern into the variant name and, for a
// pattern with braces, its bindings.
func patternParts(e syntax.Expr) (name syntax.Expr, bindings []syntax.Expr, braces bool) {
	if cl, ok := unparen(e).(*syntax.CompositeLit); ok && cl.Type != nil {
		return cl.Type, cl.Elts, true
	}
	return e, nil, false
}

func isPatternShape(e syntax.Expr) bool {
	switch e := unparen(e).(type) {
	case *syntax.Name, *syntax.SelectorExpr:
		return true
	case *syntax.CompositeLit:
		return e.Type != nil
	}
	return false
}

// enumString writes an enum type literal.
func writeEnum(b *strings.Builder, t *Enum) {
	b.WriteString("enum{")
	for i, v := range t.Variants {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(v.name)
		if v.data {
			b.WriteString("{")
			for j, f := range v.fields {
				if j > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(b, "%s %s", f.name, f.typ)
			}
			b.WriteString("}")
		}
	}
	b.WriteString("}")
}
