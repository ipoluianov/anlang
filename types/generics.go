package types

import (
	"fmt"
	"strings"

	"an/syntax"
)

// TypeParam is a type parameter (type.param.go). Its underlying type is
// the interface of its constraint, as in go/types: methods of the
// constraint can be called on values of the type parameter.
type TypeParam struct {
	Obj   *TypeName
	index int
	bound Type // the constraint: an interface type

	zero      *zeroReason // why it requires a zero value, if it does (type.zero.param-constraint)
	origParam *TypeParam  // for a receiver type parameter, the type parameter of the generic type
}

// RequiresZero reports whether the type parameter has the inferred
// constraint "requires a zero value", and describes why
// (type.zero.param-tools).
func (t *TypeParam) RequiresZero() (bool, string) {
	if t.zero == nil {
		return false, ""
	}
	return true, zeroChain(t)
}

func (t *TypeParam) Underlying() Type {
	if t.bound == nil {
		return &Interface{}
	}
	if i, ok := t.bound.Underlying().(*Interface); ok {
		return i
	}
	return &Interface{}
}

func (t *TypeParam) String() string { return t.Obj.name }

// iface returns the interface of the constraint of t.
func (t *TypeParam) iface() *Interface { return t.Underlying().(*Interface) }

// genericAlias is the type of a generic type alias; it is usable only
// when instantiated.
type genericAlias struct {
	obj *TypeName
	tps []*TypeParam
	rhs Type // nil while the declaration is being checked
}

func (t *genericAlias) Underlying() Type { return Typ[Invalid] }
func (t *genericAlias) String() string   { return t.obj.name }

// Term is a term of a union in a constraint: T or ~T.
type Term struct {
	Tilde bool
	Type  Type
}

func (t *Term) String() string {
	if t.Tilde {
		return "~" + t.Type.String()
	}
	return t.Type.String()
}

// Union is a union of terms, an element of a constraint interface.
type Union struct {
	Terms   []*Term
	invalid bool // a term was invalid; an error has been reported
}

func (t *Union) Underlying() Type { return t }

func (t *Union) String() string {
	var s []string
	for _, term := range t.Terms {
		s = append(s, term.String())
	}
	return strings.Join(s, " | ")
}

// typeSet is the set of types of an interface: all types (all), or the
// types given by terms; comparable restricts it to comparable types.
type typeSet struct {
	all        bool
	terms      []*Term
	comparable bool
}

// typeSet returns the type set of t. Like the method set, it is not
// cached while an embedded interface is being declared.
func (t *Interface) typeSet() *typeSet {
	if t.tsComplete {
		return t.ts
	}
	if t.tsComputing {
		return &typeSet{all: true}
	}
	t.tsComputing = true
	defer func() { t.tsComputing = false }()
	complete := true
	ts := &typeSet{all: true, comparable: t.isComparable}
	for _, e := range t.Embeddeds {
		var es *typeSet
		switch e := e.(type) {
		case *Union:
			es = &typeSet{}
			for _, term := range e.Terms {
				if ti, ok := term.Type.Underlying().(*Interface); ok && !term.Tilde {
					// An interface term contributes its type set.
					s := ti.typeSet()
					if s.all {
						es.all = true
					}
					es.terms = append(es.terms, s.terms...)
					continue
				}
				es.terms = append(es.terms, term)
			}
			if es.all {
				es.terms = nil
			}
		default:
			if n, ok := e.(*Named); ok && n.underlying == nil {
				complete = false
				continue
			}
			u, ok := e.Underlying().(*Interface)
			if !ok {
				continue
			}
			es = u.typeSet()
			complete = complete && u.tsComplete
		}
		ts.comparable = ts.comparable || es.comparable
		if !es.all {
			if ts.all {
				ts.all, ts.terms = false, es.terms
			} else {
				ts.terms = intersectTerms(ts.terms, es.terms)
			}
		}
	}
	t.ts, t.tsComplete = ts, complete
	return ts
}

// isConstraint reports whether t restricts its type set by terms or by
// comparable, so that it can be used only as a constraint (type.param.go).
func (t *Interface) isConstraint() bool {
	if t.isComparable {
		return true
	}
	ts := t.typeSet()
	return !ts.all || ts.comparable
}

func intersectTerms(a, b []*Term) []*Term {
	var r []*Term
	for _, x := range a {
		for _, y := range b {
			if z := intersectTerm(x, y); z != nil {
				r = append(r, z)
			}
		}
	}
	return r
}

func intersectTerm(x, y *Term) *Term {
	switch {
	case x.Tilde && y.Tilde:
		if Identical(x.Type.Underlying(), y.Type.Underlying()) {
			return x
		}
	case x.Tilde:
		if Identical(y.Type.Underlying(), x.Type) {
			return y
		}
	case y.Tilde:
		if Identical(x.Type.Underlying(), y.Type) {
			return x
		}
	default:
		if Identical(x.Type, y.Type) {
			return x
		}
	}
	return nil
}

// termsOf returns the terms of the type set of type parameter t, and
// whether the type set is restricted by terms at all.
func termsOf(t *TypeParam) ([]*Term, bool) {
	ts := t.iface().typeSet()
	return ts.terms, !ts.all
}

// underIs reports whether f holds for the underlying type of t, or, for
// a type parameter, for the underlying type of every type in its type
// set, which must be restricted by terms.
func underIs(t Type, f func(Type) bool) bool {
	if tp, ok := t.(*TypeParam); ok {
		terms, restricted := termsOf(tp)
		if !restricted || len(terms) == 0 {
			return false
		}
		for _, term := range terms {
			if !f(term.Type.Underlying()) {
				return false
			}
		}
		return true
	}
	return f(t.Underlying())
}

// coreType returns the underlying type of t, or, for a type parameter,
// the underlying type shared by all types in its type set; nil if there
// is none.
func coreType(t Type) Type {
	tp, ok := t.(*TypeParam)
	if !ok {
		return t.Underlying()
	}
	terms, restricted := termsOf(tp)
	if !restricted || len(terms) == 0 {
		return nil
	}
	u := terms[0].Type.Underlying()
	for _, term := range terms[1:] {
		if Identical(term.Type.Underlying(), u) {
			continue
		}
		// Channels with the same element type: the most restricted direction.
		ch, ok1 := u.(*Chan)
		tc, ok2 := term.Type.Underlying().(*Chan)
		if !ok1 || !ok2 || !Identical(ch.Elem, tc.Elem) {
			return nil
		}
		switch {
		case ch.Dir == SendRecv:
			u = tc
		case tc.Dir != SendRecv && tc.Dir != ch.Dir:
			return nil
		}
	}
	return u
}

// core is coreType, with Typ[Invalid] for a type parameter without a
// core type: operations that need one are then reported as invalid.
func core(t Type) Type {
	if u := coreType(t); u != nil {
		return u
	}
	return Typ[Invalid]
}

func isTypeParam(t Type) bool {
	_, ok := t.(*TypeParam)
	return ok
}

// declareTypeParams declares the type parameters of a generic function
// or type in the current scope and checks their constraints.
func (c *checker) declareTypeParams(list *syntax.FieldList) []*TypeParam {
	var tps []*TypeParam
	for _, f := range list.List {
		for _, name := range f.Names {
			obj := &TypeName{object{pos: name.Pos(), name: name.Name, pkg: c.pkg}}
			tp := &TypeParam{Obj: obj, index: len(tps)}
			obj.typ = tp
			c.declare(name, obj)
			tps = append(tps, tp)
		}
	}
	i := 0
	for _, f := range list.List {
		bound := c.constraint(f.Type)
		for range f.Names {
			tps[i].bound = bound
			i++
		}
	}
	return tps
}

// constraint checks a type constraint. A constraint that is not an
// interface, such as ~int | f64, stands for an interface with that
// single element.
func (c *checker) constraint(e syntax.Expr) Type {
	if isUnion(e) {
		u := c.union(e)
		if u.invalid {
			return Typ[Invalid]
		}
		return &Interface{Embeddeds: []Type{u}}
	}
	t := c.typExprAny(e)
	switch {
	case !isValid(t):
		return Typ[Invalid]
	case isTypeParam(t):
		c.errorf(e.Pos(), "type.param.go", "cannot use a type parameter as constraint")
		return &Interface{}
	}
	if _, ok := t.Underlying().(*Interface); ok {
		return t
	}
	if n, ok := t.(*Named); ok && n.underlying == nil {
		return t
	}
	return &Interface{Embeddeds: []Type{&Union{Terms: []*Term{{Type: t}}}}}
}

func isUnion(e syntax.Expr) bool {
	switch e := unparen(e).(type) {
	case *syntax.BinaryExpr:
		return e.Op == syntax.Or
	case *syntax.UnaryExpr:
		return e.Op == syntax.Tilde
	}
	return false
}

// union checks a union of terms in a constraint.
func (c *checker) union(e syntax.Expr) *Union {
	u := &Union{}
	var walk func(e syntax.Expr)
	walk = func(e syntax.Expr) {
		if b, ok := unparen(e).(*syntax.BinaryExpr); ok && b.Op == syntax.Or {
			walk(b.X)
			walk(b.Y)
			return
		}
		term := &Term{}
		if t, ok := unparen(e).(*syntax.UnaryExpr); ok && t.Op == syntax.Tilde {
			term.Tilde = true
			e = t.X
		}
		typ := c.typExprAny(e)
		switch {
		case !isValid(typ):
			u.invalid = true
			return
		case isTypeParam(typ):
			c.errorf(e.Pos(), "type.param.go", "term %s cannot be a type parameter", typ)
			return
		case term.Tilde && isInterfaceType(typ):
			c.errorf(e.Pos(), "type.param.go", "invalid use of ~ (%s is an interface)", typ)
			u.invalid = true
			return
		case term.Tilde && !Identical(typ, typ.Underlying()):
			c.errorf(e.Pos(), "type.param.go", "invalid use of ~ (underlying type of %s is %s)", typ, typ.Underlying())
			return
		}
		if ti, ok := typ.Underlying().(*Interface); ok {
			switch {
			case len(ti.MethodSet()) > 0:
				c.errorf(e.Pos(), "type.param.go", "cannot use %s in union (it has methods)", typ)
				return
			case ti.typeSet().comparable:
				c.errorf(e.Pos(), "type.param.go", "cannot use %s in union (it is or embeds comparable)", typ)
				return
			}
		}
		for _, prev := range u.Terms {
			if !isInterfaceType(prev.Type) && !isInterfaceType(typ) && intersectTerm(prev, &Term{Tilde: term.Tilde, Type: typ}) != nil {
				c.errorf(e.Pos(), "type.param.go", "overlapping terms %s and %s", &Term{Tilde: term.Tilde, Type: typ}, prev)
				break
			}
		}
		term.Type = typ
		u.Terms = append(u.Terms, term)
	}
	walk(e)
	return u
}

// substMap maps type parameters to type arguments.
type substMap map[*TypeParam]Type

func newSubstMap(tps []*TypeParam, targs []Type) substMap {
	m := substMap{}
	for i, tp := range tps {
		if i < len(targs) {
			m[tp] = targs[i]
		}
	}
	return m
}

// subst returns t with the type parameters of m replaced by their type
// arguments. Unchanged types are returned as they are.
func (c *checker) subst(t Type, m substMap) Type {
	if len(m) == 0 || t == nil {
		return t
	}
	switch t := t.(type) {
	case *TypeParam:
		if r, ok := m[t]; ok {
			return r
		}
	case *Array:
		if e := c.subst(t.Elem, m); e != t.Elem {
			return &Array{Len: t.Len, Elem: e}
		}
	case *Slice:
		if e := c.subst(t.Elem, m); e != t.Elem {
			return &Slice{Elem: e}
		}
	case *Map:
		k, e := c.subst(t.Key, m), c.subst(t.Elem, m)
		if k != t.Key || e != t.Elem {
			return &Map{Key: k, Elem: e}
		}
	case *Chan:
		if e := c.subst(t.Elem, m); e != t.Elem {
			return &Chan{Dir: t.Dir, Elem: e}
		}
	case *Struct:
		changed := false
		fields := make([]*Var, len(t.Fields))
		for i, f := range t.Fields {
			ft := c.subst(f.typ, m)
			if ft != f.typ {
				changed = true
				nf := *f
				nf.typ = ft
				fields[i] = &nf
			} else {
				fields[i] = f
			}
		}
		if changed {
			return &Struct{Fields: fields, Tags: t.Tags, Layout: t.Layout}
		}
	case *Tuple:
		if t == nil {
			return t
		}
		return c.substTuple(t, m)
	case *Signature:
		p, r := c.substTuple(t.Params, m), c.substTuple(t.Results, m)
		if p != t.Params || r != t.Results {
			return &Signature{Recv: t.Recv, Params: p, Results: r, Variadic: t.Variadic, TypeParams: t.TypeParams, RecvTypeParams: t.RecvTypeParams}
		}
	case *Interface:
		changed := false
		var methods []*Func
		for _, f := range t.Methods {
			ft := c.subst(f.typ, m)
			if ft != f.typ {
				changed = true
				nf := *f
				nf.typ = ft
				f = &nf
			}
			methods = append(methods, f)
		}
		var embeddeds []Type
		for _, e := range t.Embeddeds {
			ne := c.subst(e, m)
			changed = changed || ne != e
			embeddeds = append(embeddeds, ne)
		}
		if changed {
			return &Interface{Methods: methods, Embeddeds: embeddeds, isComparable: t.isComparable}
		}
	case *Enum:
		changed := false
		variants := make([]*Variant, len(t.Variants))
		for i, v := range t.Variants {
			nv := *v
			nv.orig, nv.ctor = v.origin(), nil
			nv.fields = nil
			for _, f := range v.fields {
				nf := *f
				nf.typ = c.subst(f.typ, m)
				changed = changed || nf.typ != f.typ
				nv.fields = append(nv.fields, &nf)
			}
			variants[i] = &nv
		}
		if changed {
			return &Enum{Variants: variants}
		}
	case *Union:
		changed := false
		var terms []*Term
		for _, term := range t.Terms {
			nt := c.subst(term.Type, m)
			changed = changed || nt != term.Type
			terms = append(terms, &Term{Tilde: term.Tilde, Type: nt})
		}
		if changed {
			return &Union{Terms: terms}
		}
	case *Named:
		if t.orig != nil {
			changed := false
			targs := make([]Type, len(t.targs))
			for i, a := range t.targs {
				targs[i] = c.subst(a, m)
				changed = changed || targs[i] != a
			}
			if changed {
				return c.instantiate(syntax.Pos{}, t.orig, targs, false)
			}
		}
	case *lazyAlias:
		if t.actual != nil {
			return c.subst(t.actual, m)
		}
	}
	return t
}

func (c *checker) substTuple(t *Tuple, m substMap) *Tuple {
	if t == nil {
		return nil
	}
	changed := false
	vars := make([]*Var, len(t.Vars))
	for i, v := range t.Vars {
		vt := c.subst(v.typ, m)
		if vt != v.typ {
			changed = true
			nv := *v
			nv.typ = vt
			vars[i] = &nv
		} else {
			vars[i] = v
		}
	}
	if !changed {
		return t
	}
	return &Tuple{Vars: vars}
}

// maxInstanceDepth bounds the nesting of type arguments, to stop
// instantiation cycles such as T[P] containing T[[]P].
const maxInstanceDepth = 32

// instantiate returns the instance of the generic type orig with the
// type arguments targs. If verify is set, the type arguments are checked
// against the constraints once all declarations are complete.
func (c *checker) instantiate(pos syntax.Pos, orig *Named, targs []Type, verify bool) Type {
	if len(targs) != len(orig.TypeParams) {
		c.errorf(pos, "type.param.go", "got %d type arguments for %s, want %d", len(targs), orig.Obj.name, len(orig.TypeParams))
		return Typ[Invalid]
	}
	var key strings.Builder
	fmt.Fprintf(&key, "%p", orig)
	for _, a := range targs {
		if !isValid(a) {
			return Typ[Invalid]
		}
		key.WriteString(";")
		key.WriteString(typeKey(a))
	}
	if depth := instanceDepth(targs); depth > maxInstanceDepth {
		c.errorf(pos, "type.param.go", "instantiation cycle: %s is instantiated with ever deeper type arguments", orig.Obj.name)
		return Typ[Invalid]
	}
	if verify {
		c.later(func() { c.verify(pos, orig.Obj.name, orig.TypeParams, targs) })
		c.recordZeroEdges(pos, orig.Obj.name, orig.TypeParams, targs)
	}
	if n := c.instances[key.String()]; n != nil {
		return n
	}
	n := &Named{Obj: orig.Obj, orig: orig, targs: targs}
	// The underlying type is computed when it is needed: the generic type
	// may still be being declared, and expanding eagerly would not stop
	// for recursive instances.
	n.expand = func() {
		if orig.underlying == nil {
			return
		}
		n.expand = nil
		n.underlying = c.subst(orig.underlying, newSubstMap(orig.TypeParams, targs))
	}
	c.instances[key.String()] = n
	return n
}

// typeKey returns a string that identifies type t for the instance cache.
func typeKey(t Type) string {
	switch t := t.(type) {
	case *Named:
		var b strings.Builder
		fmt.Fprintf(&b, "%p", t.Obj)
		if t.orig != nil {
			fmt.Fprintf(&b, "%p", t.orig)
		}
		for _, a := range t.targs {
			b.WriteString("[" + typeKey(a) + "]")
		}
		return b.String()
	case *TypeParam:
		return fmt.Sprintf("%p", t)
	}
	return t.String()
}

func instanceDepth(targs []Type) int {
	depth := 0
	for _, a := range targs {
		d := strings.Count(a.String(), "[")
		if n, ok := a.(*Named); ok && n.orig != nil {
			d = 1 + instanceDepth(n.targs)
		}
		depth = max(depth, d)
	}
	return depth
}

// later schedules f to run when all declarations and bodies are checked.
func (c *checker) later(f func()) { c.delayed = append(c.delayed, f) }

// verify checks type arguments against the constraints of their type
// parameters (type.param.go).
func (c *checker) verify(pos syntax.Pos, name string, tps []*TypeParam, targs []Type) bool {
	m := newSubstMap(tps, targs)
	for i, tp := range tps {
		bound := c.subst(tp.bound, m)
		if why := c.satisfies(targs[i], bound); why != "" {
			c.errorf(pos, "type.param.go", "%s does not satisfy %s in %s (%s)", targs[i], bound, name, why)
			return false
		}
	}
	return true
}

// satisfies returns why type t does not satisfy constraint bound, or "".
func (c *checker) satisfies(t, bound Type) string {
	iface, ok := bound.Underlying().(*Interface)
	if !ok || !isValid(t) {
		return ""
	}
	if why := c.missingMethod(t, iface); why != "" {
		return why
	}
	ts := iface.typeSet()
	if ts.comparable && !comparable(t) {
		return t.String() + " is not comparable"
	}
	if ts.all {
		return ""
	}
	if tp, ok := t.(*TypeParam); ok {
		terms, restricted := termsOf(tp)
		if !restricted {
			return t.String() + " has an unrestricted type set"
		}
		for _, term := range terms {
			if !termIn(term, ts.terms) {
				return fmt.Sprintf("%s is not in the type set", term)
			}
		}
		return ""
	}
	for _, term := range ts.terms {
		if term.Tilde && Identical(t.Underlying(), term.Type) || !term.Tilde && Identical(t, term.Type) {
			return ""
		}
	}
	return t.String() + " is not in the type set"
}

// termIn reports whether the types of term x are all in the union of terms.
func termIn(x *Term, terms []*Term) bool {
	for _, y := range terms {
		switch {
		case y.Tilde && Identical(x.Type.Underlying(), y.Type):
			return true
		case !y.Tilde && !x.Tilde && Identical(x.Type, y.Type):
			return true
		}
	}
	return false
}

// instanceType checks the instantiation T[A, B] of a generic type.
func (c *checker) instanceType(e *syntax.IndexExpr, t Type) Type {
	if ga, ok := t.(*genericAlias); ok {
		targs := c.typeArgs(e.Indices)
		if targs == nil || ga.rhs == nil {
			return Typ[Invalid]
		}
		if len(targs) != len(ga.tps) {
			c.errorf(e.Pos(), "type.param.go", "got %d type arguments for %s, want %d", len(targs), ga.obj.name, len(ga.tps))
			return Typ[Invalid]
		}
		c.later(func() { c.verify(e.Pos(), ga.obj.name, ga.tps, targs) })
		return c.subst(ga.rhs, newSubstMap(ga.tps, targs))
	}
	n, ok := t.(*Named)
	if !ok || len(n.TypeParams) == 0 || n.orig != nil {
		c.errorf(e.Pos(), "type.param.go", "%s is not a generic type", t)
		c.useTypes(e.Indices)
		return Typ[Invalid]
	}
	targs := c.typeArgs(e.Indices)
	if targs == nil {
		return Typ[Invalid]
	}
	inst := c.instantiate(e.Pos(), n, targs, true)
	c.recordInstance(e, targs, inst)
	return inst
}

// typeArgs checks a list of type arguments; it returns nil if one is
// invalid.
func (c *checker) typeArgs(list []syntax.Expr) []Type {
	var targs []Type
	ok := true
	for _, a := range list {
		t := c.typExpr(a)
		ok = ok && isValid(t)
		targs = append(targs, t)
	}
	if !ok {
		return nil
	}
	return targs
}

// useTypes checks type expressions of an invalid construct for other
// errors.
func (c *checker) useTypes(list []syntax.Expr) {
	for _, a := range list {
		c.typExpr(a)
	}
}

// typesOf returns the types in the type set of t: t itself, or for a type
// parameter the types of its terms; nil if the type set is not
// restricted by terms.
func typesOf(t Type) []Type {
	tp, ok := t.(*TypeParam)
	if !ok {
		return []Type{t}
	}
	terms, restricted := termsOf(tp)
	if !restricted || len(terms) == 0 {
		return nil
	}
	var ts []Type
	for _, term := range terms {
		ts = append(ts, term.Type)
	}
	return ts
}

// methodsOf returns the methods of a defined type; for an instance, the
// methods of the generic type with the type arguments substituted.
func (c *checker) methodsOf(n *Named) []*Func {
	if n.orig == nil {
		return n.methods
	}
	if n.instMethods != nil || len(n.orig.methods) == 0 {
		return n.instMethods
	}
	for _, m := range n.orig.methods {
		c.objDecl(m)
		sig, ok := m.typ.(*Signature)
		if !ok || sig.Recv == nil {
			continue
		}
		smap := newSubstMap(sig.RecvTypeParams, n.targs)
		ns := *c.subst(sig, smap).(*Signature)
		recv := *sig.Recv
		recv.typ = n
		ns.Recv, ns.RecvTypeParams = &recv, nil
		im := &Func{object: m.object, Decl: m.Decl}
		im.typ = &ns
		n.instMethods = append(n.instMethods, im)
	}
	return n.instMethods
}

// isGeneric reports whether t is the signature of a generic function.
func isGeneric(t Type) bool {
	sig, ok := t.(*Signature)
	return ok && len(sig.TypeParams) > 0
}

// funcInst checks the explicit instantiation f[A, B] of a generic
// function. With fewer type arguments than type parameters, the rest
// must be inferred in a call; x then keeps the generic signature and the
// given type arguments.
func (c *checker) funcInst(x *operand, e *syntax.IndexExpr) {
	sig := x.typ.(*Signature)
	targs := c.typeArgs(e.Indices)
	if targs == nil {
		x.invalidate()
		return
	}
	if len(targs) > len(sig.TypeParams) {
		c.errorf(e.Indices[len(sig.TypeParams)].Pos(), "type.param.go", "got %d type arguments for %s, want %d", len(targs), exprString(e.X), len(sig.TypeParams))
		x.invalidate()
		return
	}
	x.expr = e
	if len(targs) < len(sig.TypeParams) {
		// The remaining type arguments may follow from the constraints.
		u := &unifier{c: c, tps: sig.TypeParams, bound: map[*TypeParam]Type{}}
		for i, t := range targs {
			u.bound[sig.TypeParams[i]] = t
		}
		u.fromConstraints()
		if len(u.bound) < len(sig.TypeParams) {
			x.targs = targs
			return
		}
		targs = make([]Type, len(sig.TypeParams))
		m := substMap(u.bound)
		for i, tp := range sig.TypeParams {
			targs[i] = u.bound[tp]
			for range len(sig.TypeParams) {
				targs[i] = c.subst(targs[i], m)
			}
		}
	}
	x.typ = c.instantiateSig(e.Pos(), exprString(e.X), sig, targs, e)
}

// instantiateSig substitutes type arguments for the type parameters of a
// generic function; the constraints are checked later.
func (c *checker) instantiateSig(pos syntax.Pos, name string, sig *Signature, targs []Type, use syntax.Expr) *Signature {
	tps := sig.TypeParams
	if c.fn != nil && c.fn.generic != nil && len(c.fn.generic.TypeParams) > 0 && &c.fn.generic.TypeParams[0] == &tps[0] {
		// A generic function instantiating itself with type arguments
		// built from its own type parameters never stops instantiating.
		u := &unifier{tps: tps}
		for i, a := range targs {
			if !isTypeParam(a) && u.mentions(a, 0) {
				c.errorf(pos, "type.param.go", "instantiation cycle: %s instantiated as %s", tps[i], a)
				break
			}
		}
	}
	c.later(func() { c.verify(pos, name, tps, targs) })
	c.recordZeroEdges(pos, name, tps, targs)
	ns := *c.subst(sig, newSubstMap(tps, targs)).(*Signature)
	ns.TypeParams = nil
	c.recordInstance(use, targs, &ns)
	return &ns
}

// unifier infers type arguments by matching parameter types with
// argument types.
type unifier struct {
	c       *checker
	tps     []*TypeParam
	bound   map[*TypeParam]Type
	derived map[*TypeParam]bool // bound to a term of its own constraint
	self    bool                // a type parameter may be bound to itself, as in a recursive call
	depth   int
}

// maxUnifyDepth stops the unification of recursive types, which match if
// no difference is found within this depth.
const maxUnifyDepth = 64

func (u *unifier) param(t Type) *TypeParam {
	if tp, ok := t.(*TypeParam); ok {
		for _, p := range u.tps {
			if p == tp {
				return tp
			}
		}
	}
	return nil
}

// unify reports whether parameter type x matches argument type y,
// binding the type parameters in x. As in assignments, a defined type
// matches a type literal with the same underlying type.
func (u *unifier) unify(x, y Type) bool {
	if u.depth > maxUnifyDepth {
		return true
	}
	u.depth++
	defer func() { u.depth-- }()
	x, y = unalias(x), unalias(y)
	if tp := u.param(x); tp != nil {
		if x == y && !u.self {
			return true // binds nothing
		}
		b := u.bound[tp]
		switch {
		case b == nil:
			u.bound[tp] = y
			return true
		case Identical(b, y):
			return true
		case Identical(b.Underlying(), y.Underlying()):
			// Prefer the defined type.
			if _, ok := y.(*Named); ok {
				if _, ok := b.(*Named); !ok {
					u.bound[tp] = y
				}
			}
			return true
		}
		return false
	}
	if Identical(x, y) && !u.mentions(x, 0) {
		return true
	}
	if ytp, ok := y.(*TypeParam); ok && u.param(y) == nil && !isTypeParam(x) {
		// A value of another type parameter matches through its core type.
		if core := coreType(ytp); core != nil {
			return u.unify(x, core)
		}
		return false
	}
	if xn, ok := x.(*Named); ok && xn.orig != nil {
		if yn, ok := y.(*Named); ok && yn.orig == xn.orig && len(xn.targs) == len(yn.targs) {
			// Instances of the same generic type: their type arguments.
			for i := range xn.targs {
				if !u.unify(xn.targs[i], yn.targs[i]) {
					return false
				}
			}
			return true
		}
	}
	if xi, ok := x.Underlying().(*Interface); ok && !isTypeParam(x) && !isTypeParam(y) {
		if _, yIface := y.Underlying().(*Interface); !yIface || !Identical(x, y) {
			return u.unifyMethods(xi, y)
		}
	}
	xn, xNamed := x.(*Named)
	yn, yNamed := y.(*Named)
	if xNamed && yNamed {
		if xn.orig == nil || xn.orig != yn.orig || len(xn.targs) != len(yn.targs) {
			return false
		}
		for i := range xn.targs {
			if !u.unify(xn.targs[i], yn.targs[i]) {
				return false
			}
		}
		return true
	}
	if yNamed && !isTypeParam(x) {
		if _, ok := x.(*Interface); !ok {
			return u.unify(x, y.Underlying())
		}
	}
	if xNamed {
		return u.unify(x.Underlying(), y)
	}
	switch x := x.(type) {
	case *Array:
		y, ok := y.(*Array)
		return ok && x.Len == y.Len && u.unify(x.Elem, y.Elem)
	case *Slice:
		y, ok := y.(*Slice)
		return ok && u.unify(x.Elem, y.Elem)
	case *Map:
		y, ok := y.(*Map)
		return ok && u.unify(x.Key, y.Key) && u.unify(x.Elem, y.Elem)
	case *Chan:
		y, ok := y.(*Chan)
		return ok && (x.Dir == y.Dir || y.Dir == SendRecv) && u.unify(x.Elem, y.Elem)
	case *Struct:
		y, ok := y.(*Struct)
		if !ok || len(x.Fields) != len(y.Fields) {
			return false
		}
		for i, f := range x.Fields {
			if f.name != y.Fields[i].name || !u.unify(f.typ, y.Fields[i].typ) {
				return false
			}
		}
		return true
	case *Signature:
		y, ok := y.(*Signature)
		return ok && x.Variadic == y.Variadic && u.unifyTuple(x.Params, y.Params) && u.unifyTuple(x.Results, y.Results)
	case *Interface:
		// A value assigned to an interface parameter binds nothing; the
		// assignment is checked after inference.
		return true
	}
	return false
}

// unifyMethods matches the methods of interface x with the methods of
// type y, so that I[T] infers T from the methods of a type that
// implements it. Missing methods are reported by the assignment.
func (u *unifier) unifyMethods(x *Interface, y Type) bool {
	for _, m := range x.MethodSet() {
		var have *Func
		if yi, ok := y.Underlying().(*Interface); ok {
			have = lookupFunc(yi.MethodSet(), m.name)
		} else if obj, _, _ := u.c.lookupFieldOrMethod(y, m.pkg, m.name); obj != nil {
			have, _ = obj.(*Func)
		}
		if have != nil && !u.unify(methodType(m), methodType(have)) {
			return false
		}
	}
	return true
}

func (u *unifier) unifyTuple(x, y *Tuple) bool {
	if x.Len() != y.Len() {
		return false
	}
	for i := range x.Len() {
		if !u.unify(x.Vars[i].typ, y.Vars[i].typ) {
			return false
		}
	}
	return true
}

// fromConstraints infers type arguments from the core types of the
// constraints: for [S ~[]E, E any], S bound to []int binds E to int.
func (u *unifier) fromConstraints() {
	for range len(u.tps) + 1 {
		changed := false
		for _, tp := range u.tps {
			before := len(u.bound)
			if b := u.bound[tp]; b != nil && len(tp.iface().MethodSet()) > 0 {
				// The methods of the constraint, as in [P interface{ m(R) }, R any].
				u.unifyMethods(tp.iface(), b)
			}
			term := coreTerm(tp)
			if term == nil {
				changed = changed || len(u.bound) != before
				continue
			}
			if b := u.bound[tp]; b != nil && !u.derived[tp] {
				if btp, ok := b.(*TypeParam); ok {
					// Bound to a type parameter: its core type.
					if core := coreType(btp); core != nil {
						u.self = true
						u.unify(term.Type, core)
						u.self = false
					}
				} else if term.Tilde {
					u.unify(term.Type, b.Underlying())
				} else {
					u.unify(term.Type, b)
				}
			} else if b == nil && !term.Tilde {
				u.bound[tp] = term.Type
				if u.derived == nil {
					u.derived = map[*TypeParam]bool{}
				}
				u.derived[tp] = true
			}
			changed = changed || len(u.bound) != before
		}
		if !changed {
			return
		}
	}
}

// inferCall infers the type arguments of a call of a generic function
// and returns the instantiated signature, or nil after an error
// (type.param.go). targs are explicitly given type arguments.
func (c *checker) inferCall(e *syntax.CallExpr, sig *Signature, targs []Type, args []*operand) *Signature {
	name := exprString(e.Fun)
	if ix, ok := unparen(e.Fun).(*syntax.IndexExpr); ok {
		name = exprString(ix.X)
	}
	u := &unifier{c: c, tps: sig.TypeParams, bound: map[*TypeParam]Type{}}
	for i, t := range targs {
		u.bound[sig.TypeParams[i]] = t
	}
	params := sig.Params.Vars
	paramType := func(i int) Type {
		n := len(params)
		switch {
		case sig.Variadic && i >= n-1 && !e.Ellipsis.IsValid():
			return params[n-1].typ.(*Slice).Elem
		case i < n:
			return params[i].typ
		}
		return nil
	}
	for i, a := range args {
		pt := paramType(i)
		if pt == nil || a.mode == invalid {
			return nil
		}
		if isUntyped(a.typ) || isGenericEnumValue(a.typ) {
			continue // typed by the parameter after inference
		}
		u.self = true
		ok := u.unify(pt, a.typ)
		u.self = false
		if !ok {
			if hasInvalid(pt) {
				return nil
			}
			c.errorf(a.expr.Pos(), "type.param.go", "in call to %s, type %s of %s does not match %s", name, a.typ, exprString(a.expr), c.subst(pt, u.bound))
			return nil
		}
	}
	u.fromConstraints()
	// Untyped constants give their default types to type parameters that
	// are still unknown; with several, the "largest" kind wins.
	untyped := map[*TypeParam]Type{}
	for i, a := range args {
		if !isUntyped(a.typ) || a.isNil() {
			continue
		}
		if tp := u.param(paramType(i)); tp != nil && u.bound[tp] == nil {
			if t := untyped[tp]; t == nil || a.typ.(*Basic).kind > t.(*Basic).kind {
				untyped[tp] = a.typ
			}
		}
	}
	for tp, t := range untyped {
		u.bound[tp] = defaultType(t)
	}
	u.fromConstraints()

	m := substMap(u.bound)
	result := make([]Type, len(sig.TypeParams))
	for i, tp := range sig.TypeParams {
		t := u.bound[tp]
		if t == nil {
			if !invalidParams(sig.TypeParams) {
				c.errorf(e.Rparen, "type.param.go", "in call to %s, cannot infer %s", name, tp)
			}
			return nil
		}
		// Bindings may refer to other type parameters.
		for range len(sig.TypeParams) {
			t = c.subst(t, m)
		}
		result[i] = t
	}
	return c.instantiateSig(e.Pos(), name, sig, result, e.Fun)
}

// inferFromTarget infers the type arguments of a generic function used
// as a value of function type t, as in Go: var f func(int) int = Double.
func (c *checker) inferFromTarget(x *operand, t *Signature) {
	sig := x.typ.(*Signature)
	u := &unifier{c: c, tps: sig.TypeParams, bound: map[*TypeParam]Type{}}
	for i, a := range x.targs {
		u.bound[sig.TypeParams[i]] = a
	}
	if sig.Variadic != t.Variadic || !u.unifyTuple(sig.Params, t.Params) || !u.unifyTuple(sig.Results, t.Results) {
		return
	}
	targs := make([]Type, len(sig.TypeParams))
	for i, tp := range sig.TypeParams {
		if targs[i] = u.bound[tp]; targs[i] == nil {
			return
		}
	}
	x.typ, x.targs = c.instantiateSig(x.expr.Pos(), exprString(x.expr), sig, targs, x.expr), nil
}

func orInvalid(t Type) Type {
	if t == nil {
		return Typ[Invalid]
	}
	return t
}

// isNamedType reports whether t is a named type in the sense of the Go
// specification: predeclared, defined or a type parameter.
func isNamedType(t Type) bool {
	switch t.(type) {
	case *Basic, *Named, *TypeParam:
		return true
	}
	return false
}

// allTerms reports whether f holds for every type in the type set of tp,
// which must be restricted by terms.
func (c *checker) allTerms(tp *TypeParam, f func(Type) bool) bool {
	terms, restricted := termsOf(tp)
	if !restricted || len(terms) == 0 {
		return false
	}
	for _, term := range terms {
		if !f(term.Type) {
			return false
		}
	}
	return true
}

// coreTerm returns the single term that describes the type set of tp
// for inference: its only term, or ~U if all terms have the underlying
// type U.
func coreTerm(tp *TypeParam) *Term {
	terms, restricted := termsOf(tp)
	if !restricted || len(terms) == 0 {
		return nil
	}
	if len(terms) == 1 {
		return terms[0]
	}
	u := terms[0].Type.Underlying()
	for _, t := range terms[1:] {
		if !Identical(t.Type.Underlying(), u) {
			return nil
		}
	}
	return &Term{Tilde: true, Type: u}
}

// isBytesOrString reports whether t is a string type or a []u8 type.
func isBytesOrString(t Type) bool {
	if isString(t) {
		return true
	}
	s, ok := t.Underlying().(*Slice)
	return ok && Identical(s.Elem.Underlying(), Typ[U8])
}

// indexElem returns the element type shared by the types in the type set
// of tp if all of them can be indexed, or nil.
func indexElem(tp *TypeParam) Type {
	terms, restricted := termsOf(tp)
	if !restricted || len(terms) == 0 {
		return nil
	}
	var elem Type
	for _, term := range terms {
		var e Type
		switch u := term.Type.Underlying().(type) {
		case *Basic:
			if u.info&IsString != 0 {
				e = Typ[U8]
			}
		case *Array:
			e = u.Elem
		case *Slice:
			e = u.Elem
		}
		if e == nil || elem != nil && !Identical(e, elem) {
			return nil
		}
		elem = e
	}
	return elem
}

// invalidParams reports whether a type parameter has an invalid
// constraint; an error has then been reported already.
func invalidParams(tps []*TypeParam) bool {
	for _, tp := range tps {
		if !isValid(tp.bound) {
			return true
		}
	}
	return false
}

// mentions reports whether t contains one of the type parameters being
// inferred.
func (u *unifier) mentions(t Type, depth int) bool {
	if depth > 16 {
		return false
	}
	switch t := t.(type) {
	case *TypeParam:
		return u.param(t) != nil
	case *Array:
		return u.mentions(t.Elem, depth+1)
	case *Slice:
		return u.mentions(t.Elem, depth+1)
	case *Map:
		return u.mentions(t.Key, depth+1) || u.mentions(t.Elem, depth+1)
	case *Chan:
		return u.mentions(t.Elem, depth+1)
	case *Named:
		for _, a := range t.targs {
			if u.mentions(a, depth+1) {
				return true
			}
		}
	case *Signature:
		for _, v := range append(append([]*Var(nil), t.Params.Vars...), t.Results.Vars...) {
			if u.mentions(v.typ, depth+1) {
				return true
			}
		}
	case *Struct:
		for _, f := range t.Fields {
			if u.mentions(f.typ, depth+1) {
				return true
			}
		}
	}
	return false
}

// fromOwnConstraints instantiates a generic function passed as an
// argument if all its type arguments follow from its constraints, as
// for f(g) with func g[Q int](Q).
func (c *checker) fromOwnConstraints(x *operand) {
	sig := x.typ.(*Signature)
	u := &unifier{c: c, tps: sig.TypeParams, bound: map[*TypeParam]Type{}}
	for i, a := range x.targs {
		u.bound[sig.TypeParams[i]] = a
	}
	u.fromConstraints()
	targs := make([]Type, len(sig.TypeParams))
	m := substMap(u.bound)
	for i, tp := range sig.TypeParams {
		if targs[i] = u.bound[tp]; targs[i] == nil {
			return
		}
		for range len(sig.TypeParams) {
			targs[i] = c.subst(targs[i], m)
		}
	}
	x.typ, x.targs = c.instantiateSig(x.expr.Pos(), exprString(x.expr), sig, targs, x.expr), nil
}

// mapKey returns the key type shared by the map types in the type set of
// tp, or nil.
func mapKey(tp *TypeParam) Type {
	terms, restricted := termsOf(tp)
	if !restricted || len(terms) == 0 {
		return nil
	}
	var key Type
	for _, term := range terms {
		m, ok := term.Type.Underlying().(*Map)
		if !ok || key != nil && !Identical(m.Key, key) {
			return nil
		}
		key = m.Key
	}
	return key
}

func isInterfaceType(t Type) bool {
	if isTypeParam(t) {
		return false
	}
	_, ok := t.Underlying().(*Interface)
	return ok
}
