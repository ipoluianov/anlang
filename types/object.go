package types

import (
	"go/constant"
	"sort"

	"an/syntax"
)

// Object is a named entity: a constant, type, variable, function,
// built-in function, imported package, label or nil.
type Object interface {
	Name() string
	Type() Type
	Pos() syntax.Pos // position of the declaring identifier; invalid for predeclared objects
	Parent() *Scope  // scope in which the object is declared; nil for fields, methods and labels
	base() *object
}

type object struct {
	parent *Scope
	pos    syntax.Pos
	name   string
	typ    Type
	pkg    *Package // package of fields and methods; their unexported names are visible only there
}

func (o *object) Name() string     { return o.name }
func (o *object) Type() Type       { return o.typ }
func (o *object) Pos() syntax.Pos  { return o.pos }
func (o *object) Parent() *Scope   { return o.parent }
func (o *object) base() *object    { return o }
func (o *object) Exported() bool   { return o.name != "" && 'A' <= o.name[0] && o.name[0] <= 'Z' }
func (o *object) Pkg() *Package    { return o.pkg }
func (o *object) setType(typ Type) { o.typ = typ }

// Const is a declared constant.
type Const struct {
	object
	Val constant.Value
}

// TypeName is a declared type: the name of a defined type or an alias.
type TypeName struct {
	object
}

// Var is a variable, a parameter, a result or a struct field.
type Var struct {
	object
	inout    bool // an inout parameter (decl.param.inout)
	field    bool
	embedded bool         // an embedded struct field
	owner    *funcContext // function or function literal that declares a local variable
	second   bool         // holds a second-class closure (expr.closure.capture-inout)
	param    bool         // a parameter or a result
	used     bool
}

// Inout reports whether v is an inout parameter.
func (v *Var) Inout() bool { return v.inout }

// Embedded reports whether v is an embedded struct field.
func (v *Var) Embedded() bool { return v.embedded }

// IsField reports whether v is a struct field.
func (v *Var) IsField() bool { return v.field }

// Func is a declared function.
type Func struct {
	object
	Decl *syntax.FuncDecl
}

// Signature returns the type of f.
func (f *Func) Signature() *Signature { return f.typ.(*Signature) }

// Builtin is a predeclared function, or a function of a standard library
// package that the compiler implements itself.
type Builtin struct {
	object
	id  builtinID
	pkg string // import path of the standard library package; "" for predeclared functions
}

// Pkg returns the import path of the standard library package that
// declares b, or "" if b is predeclared.
func (b *Builtin) Pkg() string { return b.pkg }

// PkgName is the name of an imported package in a file.
type PkgName struct {
	object
	Imported *Package
	used     bool
}

// Nil is the predeclared nil.
type Nil struct {
	object
}

// Label is a statement label.
type Label struct {
	object
	used bool
}

// Package is a package known to the checker.
type Package struct {
	Path  string
	Name  string
	Scope *Scope
}

// Scope maps names to objects. Lookups that fail continue in the parent
// scope.
type Scope struct {
	parent *Scope
	elems  map[string]Object
	isFunc bool // the outermost scope of a function body
}

// NewScope returns a scope nested in parent.
func NewScope(parent *Scope) *Scope {
	return &Scope{parent: parent, elems: map[string]Object{}}
}

// Parent returns the enclosing scope.
func (s *Scope) Parent() *Scope { return s.parent }

// Lookup returns the object named name in s itself, or nil.
func (s *Scope) Lookup(name string) Object { return s.elems[name] }

// LookupParent returns the object named name in s or the nearest
// enclosing scope that declares it, and that scope.
func (s *Scope) LookupParent(name string) (*Scope, Object) {
	for ; s != nil; s = s.parent {
		if obj := s.elems[name]; obj != nil {
			return s, obj
		}
	}
	return nil, nil
}

// Insert adds obj to s. If s already contains an object with the same
// name, Insert leaves s unchanged and returns that object.
func (s *Scope) Insert(obj Object) Object {
	name := obj.Name()
	if alt := s.elems[name]; alt != nil {
		return alt
	}
	s.elems[name] = obj
	if o := obj.base(); o.parent == nil {
		o.parent = s
	}
	return nil
}

// Names returns the names declared in s, sorted.
func (s *Scope) Names() []string {
	names := make([]string, 0, len(s.elems))
	for name := range s.elems {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
