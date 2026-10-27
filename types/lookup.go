package types

import (
	"fmt"
	"slices"
)

// lookupFieldOrMethod finds the field or method name of a value of type
// t, including fields and methods promoted through embedded fields
// (type.struct.go, decl.receiver.method-set). An unexported name is
// found only if it belongs to package pkg (decl.visibility.go); if pkg
// is nil, every name is found. index is the path of field
// indices to the field or to the embedded field that holds the method.
// ambiguous reports that the name is found more than once at the
// shallowest depth.
func (c *checker) lookupFieldOrMethod(t Type, pkg *Package, name string) (obj Object, index []int, ambiguous bool) {
	if name == "_" {
		return nil, nil, false
	}
	type embedded struct {
		typ       Type
		index     []int
		multiples bool // the type occurs more than once at this depth
	}
	current := []embedded{{typ: t}}
	seen := map[*Named]bool{}
	for len(current) > 0 {
		var next []embedded
		var found Object
		var foundIndex []int
		n := 0
		add := func(obj Object, index []int, multiples bool) {
			found, foundIndex = obj, index
			n++
			if multiples {
				n++
			}
		}
		for _, e := range current {
			typ := e.typ
			if named, ok := typ.(*Named); ok {
				if seen[named] {
					continue
				}
				seen[named] = true
				if m := lookupFunc(c.methodsOf(named), name); m != nil && visible(m, pkg) {
					add(m, e.index, e.multiples)
					continue
				}
			}
			switch u := typ.Underlying().(type) {
			case *Struct:
				for i, f := range u.Fields {
					index := append(slices.Clip(e.index), i)
					if f.name == name && visible(f, pkg) {
						add(f, index, e.multiples)
						continue
					}
					if f.embedded {
						next = append(next, embedded{f.typ, index, e.multiples})
					}
				}
			case *Interface:
				if m := lookupFunc(u.MethodSet(), name); m != nil && visible(m, pkg) {
					add(m, e.index, e.multiples)
				}
			}
		}
		switch {
		case n == 1:
			if f, ok := found.(*Func); ok {
				c.objDecl(f)
			}
			return found, foundIndex, false
		case n > 1:
			return nil, nil, true
		}
		// Types embedded more than once at the same depth are ambiguous.
		current = current[:0]
		for _, e := range next {
			if i := slices.IndexFunc(current, func(x embedded) bool { return Identical(x.typ, e.typ) }); i >= 0 {
				current[i].multiples = true
				continue
			}
			current = append(current, e)
		}
	}
	return nil, nil, false
}

// incomplete reports whether t has an embedded field of invalid type,
// directly or through other embedded fields. A missing field or method
// may then be promoted through it, so no error is reported.
func incomplete(t Type) bool {
	return incompleteIn(t, nil)
}

func incompleteIn(t Type, seen []*Named) bool {
	if n, ok := t.(*Named); ok {
		if slices.Contains(seen, n) {
			return false
		}
		seen = append(seen, n)
	}
	s, ok := t.Underlying().(*Struct)
	if !ok {
		return false
	}
	for _, f := range s.Fields {
		if f.embedded && (!isValid(f.typ) || incompleteIn(f.typ, seen)) {
			return true
		}
	}
	return false
}

// visible reports whether field or method obj is visible in package pkg.
func visible(obj Object, pkg *Package) bool {
	o := obj.base()
	return pkg == nil || o.pkg == nil || o.pkg == pkg || o.Exported()
}

// methodType returns the type of method m as called on a value: its
// signature without the receiver.
func methodType(m *Func) *Signature {
	sig, ok := m.typ.(*Signature)
	if !ok {
		return &Signature{Params: &Tuple{}, Results: &Tuple{}}
	}
	if sig.Recv == nil {
		return sig
	}
	return &Signature{Params: sig.Params, Results: sig.Results, Variadic: sig.Variadic, AnyTuple: sig.AnyTuple}
}

// missingMethod returns why type v does not implement interface t, or ""
// if it does (type.interface.go).
func (c *checker) missingMethod(v Type, t *Interface) string {
	for _, m := range t.MethodSet() {
		var have *Func
		if vi, ok := v.Underlying().(*Interface); ok {
			if have = lookupFunc(vi.MethodSet(), m.name); have != nil && !visible(have, m.pkg) {
				have = nil
			}
		} else {
			obj, _, ambiguous := c.lookupFieldOrMethod(v, m.pkg, m.name)
			if ambiguous {
				return fmt.Sprintf("ambiguous selector for method %s", m.name)
			}
			switch obj := obj.(type) {
			case *Func:
				have = obj
			case *Var:
				return fmt.Sprintf("%s is a field, not a method", m.name)
			}
		}
		if have == nil {
			if incomplete(v) {
				continue
			}
			return fmt.Sprintf("missing method %s", m.name)
		}
		if _, isIface := v.Underlying().(*Interface); !isIface && borrowingFunc(have) {
			return fmt.Sprintf("method %s is a borrowing method, which calls through interfaces cannot make (mem.ref.borrowing)", m.name)
		}
		if !Identical(methodType(have), methodType(m)) {
			return fmt.Sprintf("wrong type for method %s\n\t\thave %s%s\n\t\twant %s%s", m.name, m.name, sigString(methodType(have)), m.name, sigString(methodType(m)))
		}
	}
	return ""
}

func sigString(s *Signature) string {
	return s.String()[len("func"):]
}
