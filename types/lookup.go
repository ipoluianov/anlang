package types

import (
	"fmt"
	"slices"
)

// lookupFieldOrMethod finds the field or method name of a value of type
// t, including fields and methods promoted through embedded fields
// (type.struct.go, decl.receiver.method-set). index is the path of field
// indices to the field or to the embedded field that holds the method.
// ambiguous reports that the name is found more than once at the
// shallowest depth.
func (c *checker) lookupFieldOrMethod(t Type, name string) (obj Object, index []int, ambiguous bool) {
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
				if m := lookupFunc(named.methods, name); m != nil {
					add(m, e.index, e.multiples)
					continue
				}
			}
			switch u := typ.Underlying().(type) {
			case *Struct:
				for i, f := range u.Fields {
					index := append(slices.Clip(e.index), i)
					if f.name == name {
						add(f, index, e.multiples)
						continue
					}
					if f.embedded {
						next = append(next, embedded{f.typ, index, e.multiples})
					}
				}
			case *Interface:
				if m := lookupFunc(u.MethodSet(), name); m != nil {
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
	return &Signature{Params: sig.Params, Results: sig.Results, Variadic: sig.Variadic}
}

// missingMethod returns why type v does not implement interface t, or ""
// if it does (type.interface.go).
func (c *checker) missingMethod(v Type, t *Interface) string {
	for _, m := range t.MethodSet() {
		var have *Func
		if vi, ok := v.Underlying().(*Interface); ok {
			have = lookupFunc(vi.MethodSet(), m.name)
		} else {
			obj, _, ambiguous := c.lookupFieldOrMethod(v, m.name)
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
			return fmt.Sprintf("missing method %s", m.name)
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
