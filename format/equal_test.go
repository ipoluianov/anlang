package format

import (
	"reflect"
	"slices"
	"strconv"
	"strings"

	"kern/syntax"
)

// sameTree reports whether two parsed files have the same syntax tree,
// ignoring positions, comments and the differences that formatting makes
// on purpose: parentheses (the nesting of the tree already fixes
// precedence), the case of number prefixes and exponents, the quoting of
// strings, the order of imports and empty statements.
func sameTree(a, b *syntax.File) bool {
	return equalValue(reflect.ValueOf(normalizeFile(a)), reflect.ValueOf(normalizeFile(b)))
}

var (
	posType       = reflect.TypeOf(syntax.Pos{})
	commentType   = reflect.TypeOf([]*syntax.CommentGroup(nil))
	parenType     = reflect.TypeOf(&syntax.ParenExpr{})
	litType       = reflect.TypeOf(&syntax.BasicLit{})
	emptyType     = reflect.TypeOf(&syntax.EmptyStmt{})
	fieldListType = reflect.TypeOf(&syntax.FieldList{})
)

func normalizeFile(f *syntax.File) *syntax.File {
	g := *f
	g.Imports = nil
	g.Comments = nil
	decls := slices.Clone(f.Decls)
	for i, d := range decls {
		if gd, ok := d.(*syntax.GenDecl); ok && gd.Tok == syntax.Import {
			c := *gd
			c.Specs = slices.Clone(gd.Specs)
			slices.SortStableFunc(c.Specs, compareSpecs)
			decls[i] = &c
		}
	}
	g.Decls = decls
	return &g
}

func unparen(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Interface && !v.IsNil() && v.Elem().Type() == parenType {
		v = v.Elem().Elem().FieldByName("X")
	}
	return v
}

func equalValue(a, b reflect.Value) bool {
	a, b = unparen(a), unparen(b)
	if a.Kind() == reflect.Interface {
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		a, b = a.Elem(), b.Elem()
	}
	if a.Type() != b.Type() {
		return false
	}
	switch a.Kind() {
	case reflect.Pointer:
		if a.Type() == fieldListType {
			// An empty result list "()" is the same as none.
			if emptyList(a) && emptyList(b) {
				return true
			}
		}
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		if a.Type() == litType {
			x, y := a.Interface().(*syntax.BasicLit), b.Interface().(*syntax.BasicLit)
			if x.Kind == syntax.String && y.Kind == syntax.String {
				// Import paths are rewritten to canonical quoting.
				xs, err1 := strconv.Unquote(x.Value)
				ys, err2 := strconv.Unquote(y.Value)
				if err1 == nil && err2 == nil {
					return xs == ys
				}
			}
			return x.Kind == y.Kind && strings.EqualFold(normalizedNumber(x).Value, normalizedNumber(y).Value)
		}
		if a.Type() == emptyType {
			return true
		}
		return equalValue(a.Elem(), b.Elem())
	case reflect.Struct:
		if a.Type() == posType {
			return true
		}
		for i := 0; i < a.NumField(); i++ {
			if a.Type().Field(i).Type == commentType {
				continue
			}
			if !equalValue(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice:
		x, y := dropEmpty(a), dropEmpty(b)
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equalValue(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a.Interface() == b.Interface()
}

// dropEmpty returns the elements of a slice, without empty statements,
// which the printer does not print.
func dropEmpty(v reflect.Value) []reflect.Value {
	var out []reflect.Value
	for i := 0; i < v.Len(); i++ {
		e := v.Index(i)
		if e.Kind() == reflect.Interface && !e.IsNil() && e.Elem().Type() == emptyType {
			continue
		}
		out = append(out, e)
	}
	return out
}

func emptyList(v reflect.Value) bool {
	return v.IsNil() || v.Elem().FieldByName("List").Len() == 0
}
