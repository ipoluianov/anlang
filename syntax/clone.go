package syntax

import "reflect"

// Clone returns a deep copy of the syntax tree n. Positions are kept, so
// that diagnostics in a copy point at the original source. The compiler
// checks a copy of a body for each instance of a compile-time tuple
// function, a function using an/meta, and each element of a for const
// loop (spec/10-compile-time.md).
func Clone[N Node](n N) N {
	return cloneValue(reflect.ValueOf(n)).Interface().(N)
}

var (
	posType     = reflect.TypeOf(Pos{})
	commentType = reflect.TypeOf((*CommentGroup)(nil))
)

func cloneValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		if v.Type() == commentType {
			return v // comments are shared: they are not changed
		}
		c := reflect.New(v.Type().Elem())
		c.Elem().Set(cloneValue(v.Elem()))
		return c
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		c := reflect.New(v.Type()).Elem()
		c.Set(cloneValue(v.Elem()))
		return c
	case reflect.Struct:
		if v.Type() == posType {
			return v
		}
		c := reflect.New(v.Type()).Elem()
		for i := range v.NumField() {
			if c.Field(i).CanSet() {
				c.Field(i).Set(cloneValue(v.Field(i)))
			}
		}
		return c
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		c := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			c.Index(i).Set(cloneValue(v.Index(i)))
		}
		return c
	}
	return v
}
