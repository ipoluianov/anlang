package types

import (
	"fmt"
	"go/constant"
	"strings"

	"an/syntax"
)

// Compile-time tuples, templates and for const (spec/10-compile-time.md
// §10.4–10.6).
//
// A function with a final ...any parameter, and a generic function whose
// body uses an/meta, for const or a type switch on a value of a type
// parameter, is a template. Its body is not checked once: it is checked
// for each instance, on a copy of the declaration in which the type
// parameters denote the type arguments and the ...any parameter is a
// tuple of the argument types (ct.any.instance, ct.typeswitch.static).

// TupleType is the type of a ...any parameter: a compile-time tuple of
// the types of the arguments of one instance (ct.any.tuple). In the
// signature of a template, Elems is nil.
type TupleType struct {
	Elems []Type
}

func (t *TupleType) Underlying() Type { return t }
func (t *TupleType) String() string {
	if t.Elems == nil {
		return "...any"
	}
	var s []string
	for _, e := range t.Elems {
		s = append(s, e.String())
	}
	return "...any(" + strings.Join(s, ", ") + ")"
}

// TemplateInstance is an instance of a template.
type TemplateInstance struct {
	Func     *Func
	Key      string           // identifies the instance among all functions
	Decl     *syntax.FuncDecl // the checked copy of the declaration
	Sig      *Signature       // the signature of the copy
	TypeArgs []Type
	Tuple    []Type     // the types of the ...any arguments
	Site     syntax.Pos // the call that produced the instance first
	checked  bool
}

// Unrolled is one instance of the body of a for const loop.
type Unrolled struct {
	Body *syntax.BlockStmt // the checked copy of the body
	Obj  Object            // the iteration variable: a *Const or a *Var
	// Tuple is the index of the tuple element that a *Var iteration
	// variable denotes.
	Tuple int
}

// MetaCall is a call of a method of a descriptor of an/meta, which the
// compiler replaces (ct.meta.compile-time-only).
type MetaCall struct {
	Kind    string // "Get", "Set", "Is" or "Make"
	Variant int    // the variant of Is and Make, and of Get for a variant field; -1 for a struct field
	Index   int    // the field of Get and Set
}

// FieldDesc is the value of a field descriptor meta.Field: field Index of
// struct type Type, or of variant Variant of enum type Type.
type FieldDesc struct {
	Type    Type
	Variant int // -1 for a struct field
	Index   int
	Field   *Var
}

// VariantDesc is the value of a variant descriptor meta.Variant.
type VariantDesc struct {
	Type    Type
	Index   int
	Variant *Variant
}

var metaField, metaVariant *Named

// newMetaPackage returns package an/meta (ct.meta.compile-time-only).
func newMetaPackage() *Package {
	pkg := newStdPackage("an/meta", _MetaName, _MetaFields, _MetaVariants, _MetaReject)
	for _, name := range []string{"Field", "Variant"} {
		obj := &TypeName{object{name: name, pkg: pkg}}
		n := &Named{Obj: obj, underlying: &Struct{}}
		obj.typ = n
		pkg.Scope.Insert(obj)
		if name == "Field" {
			metaField = n
		} else {
			metaVariant = n
		}
	}
	return pkg
}

// isTemplate reports whether function f is a template.
func (c *checker) isTemplate(f *Func) bool {
	sig := f.Signature()
	if sig.AnyTuple {
		return true
	}
	d := c.decls[f].fdecl
	if len(sig.TypeParams) == 0 || d == nil || d.Body == nil || sig.Recv != nil {
		// Generic methods do not exist; methods of generic types are
		// templates only with a ...any parameter.
		return false
	}
	tparams := map[string]bool{}
	for _, tp := range sig.TypeParams {
		tparams[tp.Obj.name] = true
	}
	params := map[string]bool{}
	for _, p := range sig.Params.Vars {
		if tp, ok := p.typ.(*TypeParam); ok && tparams[tp.Obj.name] {
			params[p.name] = true
		}
	}
	file := c.decls[f].file
	found := false
	syntax.Walk(d.Body, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.RangeStmt:
			found = found || n.Const
		case *syntax.SelectorExpr:
			if id, ok := n.X.(*syntax.Name); ok {
				if pn, ok := file.Lookup(id.Name).(*PkgName); ok && pn.Imported.Path == "an/meta" {
					found = true
				}
			}
		case *syntax.TypeAssertExpr:
			if id, ok := n.X.(*syntax.Name); ok && n.Type == nil && params[id.Name] {
				found = true // a static type switch
			}
		}
		return !found
	})
	return found
}

// templateInstance returns the instance of template f for the type arguments
// and the types of the ...any arguments, creating it at call site pos.
func (c *checker) templateInstance(f *Func, targs, tuple []Type, pos syntax.Pos) *TemplateInstance {
	var b strings.Builder
	b.WriteString(f.name)
	if len(targs) > 0 {
		var s []string
		for _, t := range targs {
			s = append(s, t.String())
		}
		b.WriteString("[" + strings.Join(s, ",") + "]")
	}
	if f.Signature().AnyTuple {
		var s []string
		for _, t := range tuple {
			s = append(s, t.String())
		}
		b.WriteString("(" + strings.Join(s, ",") + ")")
	}
	key := b.String()
	if inst := c.tmplInsts[key]; inst != nil {
		return inst
	}
	inst := &TemplateInstance{Func: f, Key: key, TypeArgs: targs, Tuple: tuple, Site: pos, Decl: syntax.Clone(c.decls[f].fdecl)}
	c.tmplInsts[key] = inst
	c.info.TemplateInstances = append(c.info.TemplateInstances, inst)
	// The signature of the instance is computed now, the body when the
	// checking of the other bodies is complete.
	c.instanceSig(inst)
	c.delayed = append(c.delayed, func() { c.checkInstance(inst) })
	return inst
}

// instanceScope returns the scope in which the type parameters of the
// template denote the type arguments of the instance.
func (c *checker) instanceScope(inst *TemplateInstance) *Scope {
	scope := NewScope(c.decls[inst.Func].file)
	if tps := inst.Decl.Type.TypeParams; tps != nil {
		i := 0
		for _, f := range tps.List {
			for _, name := range f.Names {
				if i < len(inst.TypeArgs) {
					scope.Insert(&TypeName{object{pos: name.Pos(), name: name.Name, typ: inst.TypeArgs[i]}})
				}
				i++
			}
		}
	}
	return scope
}

func (c *checker) instanceSig(inst *TemplateInstance) {
	savedScope, savedTuple := c.scope, c.instTuple
	c.scope, c.instTuple = c.instanceScope(inst), inst.Tuple
	if c.instTuple == nil {
		c.instTuple = []Type{}
	}
	ft := *inst.Decl.Type
	ft.TypeParams = nil
	inst.Sig = c.funcType(&ft, false)
	if inst.Decl.Recv != nil {
		inst.Sig.Recv, _ = c.receiver(inst.Decl.Recv)
	}
	c.scope, c.instTuple = savedScope, savedTuple
}

// checkInstance checks the body of an instance.
func (c *checker) checkInstance(inst *TemplateInstance) {
	if inst.checked || inst.Decl.Body == nil {
		return
	}
	inst.checked = true
	saved, savedName, savedNote := c.instance, c.funcName, c.errNote
	c.instance, c.funcName = inst, inst.Key
	c.errNote = fmt.Sprintf(" (in the instance %s produced at %s)", inst.Key, inst.Site)
	defer func() { c.instance, c.funcName, c.errNote = saved, savedName, savedNote }()
	ft := *inst.Decl.Type
	ft.TypeParams = nil
	c.funcBody(c.instanceScope(inst), inst.Sig, inst.Decl.Recv, &ft, inst.Decl.Body, false)
}

// tupleArgs returns the types of the arguments of a call of template sig
// that form the ...any tuple, and reports wrong uses of ... (ct.any.no-slice).
func (c *checker) tupleArgs(e *syntax.CallExpr, sig *Signature, args []*operand) ([]Type, bool) {
	n := sig.Params.Len() - 1
	tuple := []Type{}
	for i, a := range args {
		if i < n || a.mode == invalid {
			continue
		}
		if e.Ellipsis.IsValid() && i == len(args)-1 {
			tt, ok := a.typ.(*TupleType)
			if !ok {
				c.errorf(a.expr.Pos(), "ct.any.no-slice", "cannot pass %s with ... to a ...any parameter: each argument is an element of the tuple", a)
				return nil, false
			}
			tuple = append(tuple, tt.Elems...)
			continue
		}
		c.defaultUntyped(a)
		tuple = append(tuple, a.typ)
	}
	return tuple, true
}

// templateCall records the instance that a call of a template uses.
func (c *checker) templateCall(e *syntax.CallExpr, sig *Signature, args []*operand) {
	var id *syntax.Name
	switch f := unparen(e.Fun).(type) {
	case *syntax.Name:
		id = f
	case *syntax.SelectorExpr:
		id = f.Sel
	case *syntax.IndexExpr:
		switch g := unparen(f.X).(type) {
		case *syntax.Name:
			id = g
		case *syntax.SelectorExpr:
			id = g.Sel
		}
	}
	if id == nil {
		return
	}
	f, ok := c.info.Uses[id].(*Func)
	if !ok {
		return
	}
	if f.pkg != c.pkg && (f.Signature().AnyTuple || c.decls[f] == nil && len(f.Signature().TypeParams) > 0 && f.Decl != nil && usesMeta(f.Decl)) {
		c.notSupported(e.Pos(), 29, "calls of functions with ...any parameters or an/meta of other packages")
		return
	}
	if !c.templates[f] {
		return
	}
	var targs []Type
	if inst, ok := c.info.Instances[id]; ok {
		targs = inst.TypeArgs
	}
	for _, t := range targs {
		if isGeneric(t) || containsTypeParam(t) {
			c.notSupported(e.Pos(), 10, "a call of "+f.name+" with type arguments that are type parameters of a function that is not itself a template")
			return
		}
	}
	var tuple []Type
	if f.Signature().AnyTuple {
		var ok bool
		if tuple, ok = c.tupleArgs(e, f.Signature(), args); !ok {
			return
		}
	}
	c.info.TemplateCalls[e] = c.templateInstance(f, targs, tuple, e.Pos())
	_ = sig
}

// usesMeta reports whether a declaration of another package may be a
// template.
func usesMeta(d *syntax.FuncDecl) bool {
	found := false
	if d.Body != nil {
		syntax.Walk(d.Body, func(n syntax.Node) bool {
			if r, ok := n.(*syntax.RangeStmt); ok && r.Const {
				found = true
			}
			if s, ok := n.(*syntax.SelectorExpr); ok {
				if id, ok := s.X.(*syntax.Name); ok && id.Name == "meta" {
					found = true
				}
			}
			return !found
		})
	}
	return found
}

// containsTypeParam reports whether t is or contains a type parameter.
func containsTypeParam(t Type) bool {
	found := false
	var visit func(Type, int)
	visit = func(t Type, depth int) {
		if found || t == nil || depth > 8 {
			return
		}
		switch t := t.(type) {
		case *TypeParam:
			found = true
		case *Named:
			for _, a := range t.targs {
				visit(a, depth+1)
			}
		case *Slice:
			visit(t.Elem, depth+1)
		case *Array:
			visit(t.Elem, depth+1)
		case *Map:
			visit(t.Key, depth+1)
			visit(t.Elem, depth+1)
		case *Chan:
			visit(t.Elem, depth+1)
		case *Signature:
			for _, v := range append(append([]*Var(nil), t.Params.Vars...), t.Results.Vars...) {
				visit(v.typ, depth+1)
			}
		case *Struct:
			for _, f := range t.Fields {
				visit(f.typ, depth+1)
			}
		}
	}
	visit(t, 0)
	return found
}

// forConst checks a for const loop: its body once for each element of
// the sequence (ct.for.sequence, ct.for.unroll, ct.for.typecheck).
func (c *checker) forConst(s *syntax.RangeStmt) {
	id, ok := s.Key.(*syntax.Name)
	if !ok || s.Value != nil || s.Tok != syntax.Define {
		c.errorf(s.Pos(), "ct.for.sequence", "for const declares exactly one iteration variable with :=")
		return
	}
	type element struct {
		obj   Object
		tuple int
		desc  string
	}
	var elems []element
	var x operand
	savedOK := c.tupleOK
	c.tupleOK = true
	c.expr(&x, s.X)
	c.tupleOK = savedOK
	if x.mode == invalid {
		return
	}
	switch {
	case isTupleOperand(&x):
		for i, t := range x.typ.(*TupleType).Elems {
			v := &Var{object: object{pos: id.Pos(), name: id.Name, typ: t}, used: true, readonly: true}
			elems = append(elems, element{v, i, fmt.Sprintf("argument %d of type %s", i, t)})
		}
	case x.mode == constant_ && isInteger(x.typ):
		n, ok := int64Val(x.val)
		if !ok || n > 1<<16 {
			c.errorf(s.X.Pos(), "ct.for.sequence", "for const over %s: too many instances", &x)
			return
		}
		t := defaultType(x.typ)
		for i := range n {
			k := &Const{object: object{pos: id.Pos(), name: id.Name, typ: t}, Val: constant.MakeInt64(i)}
			elems = append(elems, element{k, -1, fmt.Sprintf("%s = %d", id.Name, i)})
		}
	case x.mode == value && hasSeq(c, s.X):
		seq, _ := c.metaSeq(s.X)
		for i, k := range seq {
			d := &Const{object: object{pos: id.Pos(), name: id.Name, typ: k.typ}, Value: k.Value}
			elems = append(elems, element{d, -1, fmt.Sprintf("element %d (%s)", i, descName(k))})
		}
	default:
		if vals, t, ok := c.constElems(s.X); ok {
			for i, v := range vals {
				k := &Const{object: object{pos: id.Pos(), name: id.Name, typ: t}, Val: v}
				elems = append(elems, element{k, -1, fmt.Sprintf("element %d", i)})
			}
			break
		}
		c.errorf(s.X.Pos(), "ct.for.sequence", "cannot use %s as the sequence of for const: want a ...any parameter, an integer constant, a constant array or slice of constants, or a sequence of an/meta", &x)
		return
	}
	var unrolled []*Unrolled
	savedNote := c.errNote
	defer func() { c.errNote = savedNote }()
	for _, el := range elems {
		body := syntax.Clone(s.Body)
		c.errNote = fmt.Sprintf(" (in the instance of for const for %s)", el.desc) + savedNote
		c.openScope()
		c.scope.Insert(el.obj)
		c.info.Defs[id] = el.obj
		savedUnrolled := c.unrolling
		c.unrolling = true
		c.stmt(breakOK|continueOK, body)
		c.unrolling = savedUnrolled
		c.closeScope()
		unrolled = append(unrolled, &Unrolled{Body: body, Obj: el.obj, Tuple: el.tuple})
	}
	c.info.Unrolled[s] = unrolled
}

func hasSeq(c *checker, e syntax.Expr) bool {
	_, ok := c.metaSeq(e)
	return ok
}

func descName(k *Const) string {
	switch d := k.Value.(type) {
	case *FieldDesc:
		return d.Field.name
	case *VariantDesc:
		return d.Variant.name
	}
	return k.name
}

func isTupleOperand(x *operand) bool {
	_, ok := x.typ.(*TupleType)
	return ok
}

// constElems returns the elements of a constant array or slice written
// as a composite literal of constants (ct.for.sequence 4).
func (c *checker) constElems(e syntax.Expr) ([]constant.Value, Type, bool) {
	var k *Const
	switch e := unparen(e).(type) {
	case *syntax.Name:
		k, _ = c.info.Uses[e].(*Const)
	case *syntax.SelectorExpr:
		k, _ = c.info.Uses[e.Sel].(*Const)
	}
	if k == nil || !k.Computed {
		return nil, nil, false
	}
	lit, ok := unparen(k.Init).(*syntax.CompositeLit)
	if !ok {
		return nil, nil, false
	}
	var elem Type
	switch u := k.typ.Underlying().(type) {
	case *Array:
		elem = u.Elem
	case *Slice:
		elem = u.Elem
	default:
		return nil, nil, false
	}
	var vals []constant.Value
	for _, el := range lit.Elts {
		if _, ok := el.(*syntax.KeyValueExpr); ok {
			return nil, nil, false
		}
		tv, ok := c.info.Types[el]
		if !ok || tv.Value == nil {
			return nil, nil, false
		}
		vals = append(vals, tv.Value)
	}
	return vals, elem, true
}

// metaSeq returns the descriptors of a sequence of an/meta: meta.Fields(T),
// meta.Variants(E) or x.Fields of a variant descriptor x, or nil.
func (c *checker) metaSeq(e syntax.Expr) ([]*Const, bool) {
	seq, ok := c.metaSeqs[unparen(e)]
	return seq, ok
}

// metaBuiltin checks a call of a function of an/meta.
func (c *checker) metaBuiltin(x *operand, e *syntax.CallExpr, id builtinID) {
	name := exprString(e.Fun)
	if id == _MetaReject {
		if len(e.Args) != 1 {
			c.errorf(e.Pos(), "ct.reject.call", "%s takes one constant string", name)
			return
		}
		var m operand
		c.expr(&m, e.Args[0])
		if m.mode != constant_ || !isString(m.typ) {
			c.errorf(e.Args[0].Pos(), "ct.reject.call", "the message of %s must be a constant string", name)
			return
		}
		pos := e.Pos()
		if c.instance != nil {
			pos = c.instance.Site
		}
		c.errors = append(c.errors, &syntax.Error{Pos: pos, Rule: "ct.reject.call", Msg: constant.StringVal(m.val)})
		x.mode = novalue
		return
	}
	if len(e.Args) != 1 {
		c.errorf(e.Pos(), "ct.meta.arg-type", "%s takes one type", name)
		x.invalidate()
		return
	}
	t := c.typExpr(e.Args[0])
	if !isValid(t) {
		x.invalidate()
		return
	}
	if containsTypeParam(t) {
		c.errorf(e.Args[0].Pos(), "ct.meta.arg-type", "%s of a type parameter is evaluated in each instance", name)
		x.invalidate()
		return
	}
	switch id {
	case _MetaName:
		n := ""
		if named, ok := t.(*Named); ok {
			n = named.Obj.name
		}
		x.mode, x.typ, x.val = constant_, Typ[UntypedString], constant.MakeString(n)
	case _MetaFields:
		st, ok := t.Underlying().(*Struct)
		if !ok {
			c.errorf(e.Args[0].Pos(), "ct.meta.arg-type", "meta.Fields(%s): %s is not a struct type", t, t)
			x.invalidate()
			return
		}
		var seq []*Const
		for i, f := range st.Fields {
			if !c.fieldVisible(t, f) {
				continue // ct.meta.visibility
			}
			seq = append(seq, &Const{object: object{pos: e.Pos(), name: "field", typ: metaField}, Value: &FieldDesc{Type: t, Variant: -1, Index: i, Field: f}})
		}
		c.metaSeqs[e] = seq
		x.mode, x.typ = value, metaField
	case _MetaVariants:
		en, ok := t.Underlying().(*Enum)
		if !ok {
			c.errorf(e.Args[0].Pos(), "ct.meta.arg-type", "meta.Variants(%s): %s is not an enum type", t, t)
			x.invalidate()
			return
		}
		var seq []*Const
		for i, v := range en.Variants {
			seq = append(seq, &Const{object: object{pos: e.Pos(), name: "variant", typ: metaVariant}, Value: &VariantDesc{Type: t, Index: i, Variant: v}})
		}
		c.metaSeqs[e] = seq
		x.mode, x.typ = value, metaVariant
	}
	x.expr = e
}

// fieldVisible reports whether meta.Fields lists field f of type t in the
// current package (ct.meta.visibility).
func (c *checker) fieldVisible(t Type, f *Var) bool {
	if f.Exported() {
		return true
	}
	n, ok := t.(*Named)
	return ok && n.Obj.pkg == c.pkg
}

// descriptor returns the descriptor that e denotes, or nil.
func (c *checker) descriptor(e syntax.Expr) any {
	id, ok := unparen(e).(*syntax.Name)
	if !ok {
		return nil
	}
	if k, ok := c.lookup(id.Name).(*Const); ok {
		switch d := k.Value.(type) {
		case *FieldDesc, *VariantDesc:
			c.info.Uses[id] = k
			return d
		}
	}
	return nil
}

// descSelector checks a selector of a descriptor: f.Name, f.Index,
// f.Exported, f.Tag, f.Type, x.Name, x.Index, x.Fields. It reports false
// if e is not a selector of a descriptor.
func (c *checker) descSelector(x *operand, e *syntax.SelectorExpr) bool {
	d := c.descriptor(e.X)
	if d == nil {
		return false
	}
	x.expr = e
	str := func(s string) { x.mode, x.typ, x.val = constant_, Typ[UntypedString], constant.MakeString(s) }
	num := func(n int) { x.mode, x.typ, x.val = constant_, Typ[UntypedInt], constant.MakeInt64(int64(n)) }
	switch d := d.(type) {
	case *FieldDesc:
		switch e.Sel.Name {
		case "Name":
			str(d.Field.name)
		case "Index":
			num(d.Index)
		case "Exported":
			x.mode, x.typ, x.val = constant_, Typ[UntypedBool], constant.MakeBool(d.Field.Exported())
		case "Tag":
			tag := ""
			if st, ok := d.Type.Underlying().(*Struct); ok && d.Variant < 0 && d.Index < len(st.Tags) {
				tag = st.Tags[d.Index]
			}
			str(tag)
		case "Type":
			x.mode, x.typ = typexpr, d.Field.typ
		default:
			c.errorf(e.Sel.Pos(), "ct.meta.compile-time-only", "meta.Field has no %s", e.Sel.Name)
			x.invalidate()
		}
	case *VariantDesc:
		switch e.Sel.Name {
		case "Name":
			str(d.Variant.name)
		case "Index":
			num(d.Index)
		case "Fields":
			var seq []*Const
			for i, f := range d.Variant.fields {
				seq = append(seq, &Const{object: object{pos: e.Pos(), name: "field", typ: metaField}, Value: &FieldDesc{Type: d.Type, Variant: d.Index, Index: i, Field: f}})
			}
			c.metaSeqs[e] = seq
			x.mode, x.typ = value, metaField
		default:
			c.errorf(e.Sel.Pos(), "ct.meta.compile-time-only", "meta.Variant has no %s", e.Sel.Name)
			x.invalidate()
		}
	}
	if x.mode == constant_ {
		c.recordType(e, constant_, x.typ, x.val)
	}
	return true
}

// descCall checks a call of a method of a descriptor: f.Get(v),
// f.Set(v, x), x.Is(v), x.Make(...). It reports false if e is not such a
// call.
func (c *checker) descCall(x *operand, e *syntax.CallExpr) bool {
	sel, ok := unparen(e.Fun).(*syntax.SelectorExpr)
	if !ok {
		return false
	}
	d := c.descriptor(sel.X)
	if d == nil {
		return false
	}
	x.expr = e
	args := c.args(e.Args, nil)
	for _, a := range args {
		if a.mode == invalid {
			x.invalidate()
			return true
		}
	}
	nargs := func(n int) bool {
		if len(args) != n {
			c.errorf(e.Pos(), "ct.meta.compile-time-only", "%s takes %d arguments", exprString(e.Fun), n)
			x.invalidate()
			return false
		}
		return true
	}
	switch d := d.(type) {
	case *FieldDesc:
		switch sel.Sel.Name {
		case "Get":
			if !nargs(1) {
				return true
			}
			if !Identical(args[0].typ, d.Type) {
				c.errorf(e.Args[0].Pos(), "ct.meta.field-access", "%s is a field of %s, not of %s", d.Field.name, d.Type, args[0].typ)
				x.invalidate()
				return true
			}
			if d.Variant >= 0 {
				c.info.MetaCalls[e] = &MetaCall{Kind: "Get", Variant: d.Variant, Index: d.Index}
			} else {
				c.info.MetaCalls[e] = &MetaCall{Kind: "Get", Variant: -1, Index: d.Index}
			}
			x.mode, x.typ = value, d.Field.typ
		case "Set":
			if !nargs(2) {
				return true
			}
			if !Identical(args[0].typ, d.Type) || d.Variant >= 0 {
				c.errorf(e.Args[0].Pos(), "ct.meta.field-access", "%s is a field of %s, not of %s", d.Field.name, d.Type, args[0].typ)
				x.invalidate()
				return true
			}
			c.inoutArg(args[0], &Var{object: object{typ: d.Type}, inout: true}, "f.Set")
			c.assignment(args[1], d.Field.typ, "argument to f.Set")
			c.info.MetaCalls[e] = &MetaCall{Kind: "Set", Variant: -1, Index: d.Index}
			x.mode = novalue
		default:
			c.errorf(sel.Sel.Pos(), "ct.meta.compile-time-only", "meta.Field has no method %s", sel.Sel.Name)
			x.invalidate()
		}
	case *VariantDesc:
		switch sel.Sel.Name {
		case "Is":
			if !nargs(1) {
				return true
			}
			if !Identical(args[0].typ, d.Type) {
				c.errorf(e.Args[0].Pos(), "ct.meta.field-access", "%s is a variant of %s, not of %s", d.Variant.name, d.Type, args[0].typ)
				x.invalidate()
				return true
			}
			c.info.MetaCalls[e] = &MetaCall{Kind: "Is", Variant: d.Index}
			x.mode, x.typ = value, Typ[UntypedBool]
		case "Make":
			if !nargs(len(d.Variant.fields)) {
				return true
			}
			for i, a := range args {
				c.assignment(a, d.Variant.fields[i].typ, "argument to x.Make")
			}
			c.info.MetaCalls[e] = &MetaCall{Kind: "Make", Variant: d.Index}
			x.mode, x.typ = value, d.Type
		default:
			c.errorf(sel.Sel.Pos(), "ct.meta.compile-time-only", "meta.Variant has no method %s", sel.Sel.Name)
			x.invalidate()
		}
	}
	return true
}

// staticTypeSwitch checks a type switch on a value that is not an
// interface value in an instance: only the matching clause is checked
// (ct.typeswitch.static, ct.typeswitch.binding, ct.typeswitch.default).
func (c *checker) staticTypeSwitch(ctxt stmtContext, s *syntax.TypeSwitchStmt, lhs *syntax.Name, x *operand) {
	chosen := -1
	dflt := -1
	for i, cs := range s.Body.List {
		cl, ok := cs.(*syntax.CaseClause)
		if !ok {
			continue
		}
		if cl.List == nil {
			dflt = i
			continue
		}
		for _, e := range cl.List {
			t := c.typExpr(e)
			if !isValid(t) || chosen >= 0 {
				continue
			}
			if Identical(t, x.typ) {
				chosen = i
			} else if it, ok := t.Underlying().(*Interface); ok && c.missingMethod(x.typ, it) == "" {
				chosen = i
			}
		}
	}
	if chosen < 0 {
		chosen = dflt
	}
	c.info.StaticSwitch[s] = chosen
	if chosen < 0 {
		c.errorf(s.Pos(), "ct.typeswitch.default", "no case of the type switch matches %s, and there is no default case", x.typ)
		return
	}
	cl := s.Body.List[chosen].(*syntax.CaseClause)
	c.openScope()
	if lhs != nil {
		v := &Var{object: object{pos: lhs.Pos(), name: lhs.Name, typ: x.typ}, owner: c.fn, used: true}
		c.scope.Insert(v)
	}
	c.stmtList((ctxt|breakOK)&^fallthroughOK, cl.Body)
	c.closeScope()
	if lhs != nil {
		c.info.Defs[lhs] = nil
	}
}
