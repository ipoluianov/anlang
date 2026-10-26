package ct

import (
	"fmt"
	"go/constant"
	"reflect"

	"an/types"
)

// Decode stores the value v of a computed constant of type t in dst, a Go
// value of a corresponding type: strings, integers and booleans for basic
// types, integers for enums without data (the variant number), structs
// with fields of the same names (fields that dst lacks are skipped),
// and slices. A value of an interface type
// is decoded into a struct: its field Type receives the name of the
// dynamic type, and the fields of the dynamic struct value go to the
// fields of the same names. The toolchain reads the descriptions of
// architectures, platforms and targets this way.
func Decode(v any, t types.Type, dst reflect.Value) error {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		c, _ := v.(constant.Value)
		switch dst.Kind() {
		case reflect.String:
			if c != nil {
				dst.SetString(constant.StringVal(c))
			}
		case reflect.Int:
			if c != nil {
				n, _ := constant.Int64Val(constant.ToInt(c))
				dst.SetInt(n)
			}
		case reflect.Bool:
			if c != nil {
				dst.SetBool(constant.BoolVal(c))
			}
		default:
			return fmt.Errorf("cannot store %s in %s", t, dst.Type())
		}
	case *types.Enum:
		c, _ := v.(*types.Composite)
		if c != nil && dst.Kind() == reflect.Int {
			dst.SetInt(int64(c.Variant))
		}
	case *types.Struct:
		c, _ := v.(*types.Composite)
		if c == nil {
			return nil
		}
		for i, f := range u.Fields {
			fv := dst.FieldByName(f.Name())
			if !fv.IsValid() {
				continue // a field the reader does not need
			}
			if err := Decode(c.Elems[i], f.Type(), fv); err != nil {
				return fmt.Errorf("%s: %v", f.Name(), err)
			}
		}
	case *types.Slice:
		c, _ := v.(*types.Composite)
		if c == nil {
			return nil
		}
		s := reflect.MakeSlice(dst.Type(), len(c.Elems), len(c.Elems))
		for i, e := range c.Elems {
			if err := Decode(e, u.Elem, s.Index(i)); err != nil {
				return fmt.Errorf("[%d]: %v", i, err)
			}
		}
		dst.Set(s)
	case *types.Interface:
		iv, _ := v.(*types.IfaceValue)
		if iv == nil {
			return nil
		}
		name := iv.Type.String()
		if n, ok := iv.Type.(*types.Named); ok {
			name = n.Obj.Name()
		}
		tf := dst.FieldByName("Type")
		if !tf.IsValid() || tf.Kind() != reflect.String {
			return fmt.Errorf("cannot store a value of %s in %s", t, dst.Type())
		}
		tf.SetString(name)
		if st, ok := iv.Type.Underlying().(*types.Struct); ok {
			c, _ := iv.Value.(*types.Composite)
			if c == nil {
				return nil
			}
			for i, f := range st.Fields {
				if fv := dst.FieldByName(f.Name()); fv.IsValid() {
					if err := Decode(c.Elems[i], f.Type(), fv); err != nil {
						return fmt.Errorf("%s.%s: %v", name, f.Name(), err)
					}
				}
			}
		}
	default:
		return fmt.Errorf("cannot decode a value of type %s", t)
	}
	return nil
}
