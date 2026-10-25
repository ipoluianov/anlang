package types

import "an/syntax"

// SelectionKind is the kind of a selector expression.
type SelectionKind int

const (
	FieldVal   SelectionKind = iota // x.f, a field
	MethodVal                       // x.m, a method, called or as a value
	MethodExpr                      // T.m, a method expression
)

// Selection describes a selector x.f of a field or method.
type Selection struct {
	Kind  SelectionKind
	Recv  Type   // the type of x
	Obj   Object // the field (*Var) or method (*Func)
	Index []int  // the path of field indices through embedded fields; for a method, to the embedded field that holds it
}

// Instance is the instantiation of a generic function or type at a use.
type Instance struct {
	TypeArgs []Type
	Type     Type // the instantiated signature or type
}

// instanceName returns the identifier that names the generic function or
// type in e: F, pkg.F or F[...].
func instanceName(e syntax.Expr) *syntax.Name {
	switch e := unparen(e).(type) {
	case *syntax.Name:
		return e
	case *syntax.SelectorExpr:
		return e.Sel
	case *syntax.IndexExpr:
		return instanceName(e.X)
	}
	return nil
}

func (c *checker) recordInstance(e syntax.Expr, targs []Type, t Type) {
	if id := instanceName(e); id != nil {
		c.info.Instances[id] = Instance{TypeArgs: targs, Type: t}
	}
}

// api returns a checker for the type operations of this API.
func (info *Info) api() *checker {
	if info.ctx == nil {
		info.ctx = &checker{
			conf:      &Config{},
			info:      info,
			intBits:   64,
			addrBits:  64,
			decls:     map[Object]*declInfo{},
			instances: map[string]*Named{},
			lazy:      map[*TypeName]*lazyAlias{},
		}
		info.ctx.pkg = info.Pkg
	}
	return info.ctx
}

// Subst returns t with the type parameters tps replaced by targs.
func (info *Info) Subst(t Type, tps []*TypeParam, targs []Type) Type {
	return info.api().subst(t, newSubstMap(tps, targs))
}

// LookupFieldOrMethod finds the field or method name of type t, with all
// names visible. For an instance of a generic type, a method has its
// type arguments substituted; its Decl is the declaration in the generic
// type.
func (info *Info) LookupFieldOrMethod(t Type, name string) (obj Object, index []int) {
	obj, index, _ = info.api().lookupFieldOrMethod(t, nil, name)
	return obj, index
}

// Implements reports whether type t implements interface iface.
func (info *Info) Implements(t Type, iface *Interface) bool {
	return info.api().missingMethod(t, iface) == ""
}

// MethodSet returns the methods of type t that are callable through an
// interface: the declared and promoted methods of a defined type.
func (info *Info) MethodSet(t Type) []*Func {
	c := info.api()
	seen := map[string]bool{}
	var ms []*Func
	var collect func(t Type, depth int)
	collect = func(t Type, depth int) {
		if depth > 8 {
			return
		}
		if n, ok := t.(*Named); ok {
			for _, m := range c.methodsOf(n) {
				if !seen[m.name] {
					seen[m.name] = true
				}
			}
		}
		if s, ok := t.Underlying().(*Struct); ok {
			for _, f := range s.Fields {
				if f.embedded {
					collect(f.typ, depth+1)
				}
			}
		}
	}
	collect(t, 0)
	for name := range seen {
		if obj, _, ambiguous := c.lookupFieldOrMethod(t, nil, name); !ambiguous {
			if m, ok := obj.(*Func); ok {
				ms = append(ms, m)
			}
		}
	}
	return ms
}

// TypeParams returns the type parameters of a generic defined type.
func (t *Named) TypeParamList() []*TypeParam { return t.TypeParams }

// Obj returns the declared name of a type parameter.
func (t *TypeParam) ObjName() *TypeName { return t.Obj }

// IsTypeParam reports whether t is a type parameter.
func IsTypeParam(t Type) bool { return isTypeParam(t) }

// IsInterface reports whether t is an interface type.
func IsInterface(t Type) bool { return isInterfaceType(t) }

// CoreType returns the underlying type of t, or for a type parameter its
// core type, or nil.
func CoreType(t Type) Type { return coreType(t) }

// Comparable reports whether values of t are comparable.
func Comparable(t Type) bool { return comparable(t) }

// Addressable reports whether the expression is an addressable operand
// (expr.addr.def).
func (tv TypeAndValue) Addressable() bool { return tv.mode == variable }

// IsMapIndex reports whether the expression is a map index expression.
func (tv TypeAndValue) IsMapIndex() bool { return tv.mode == mapindex }

// Default returns the default type of an untyped type, or t.
func Default(t Type) Type { return defaultType(t) }

// Untyped reports whether t is the type of an untyped constant.
func Untyped(t Type) bool { return isUntyped(t) }

// Recv returns the receiver type parameters of a method signature.
func (s *Signature) RecvTypeParamList() []*TypeParam { return s.RecvTypeParams }

// NewSlice returns the slice type []elem.
func NewSlice(elem Type) *Slice { return &Slice{Elem: elem} }

// NewTuple returns a tuple of values of the given types.
func NewTuple(ts ...Type) *Tuple {
	t := &Tuple{}
	for _, typ := range ts {
		t.Vars = append(t.Vars, &Var{object: object{typ: typ}})
	}
	return t
}
