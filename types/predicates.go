package types

import (
	"go/constant"
	"math"
	"slices"
)

// isBasic reports whether t is a basic type with a property in info; for
// a type parameter, whether every type in its type set is.
func isBasic(t Type, info BasicInfo) bool {
	return underIs(t, func(u Type) bool {
		b, ok := u.(*Basic)
		return ok && b.info&info != 0
	})
}

func isBoolean(t Type) bool  { return isBasic(t, IsBoolean) }
func isInteger(t Type) bool  { return isBasic(t, IsInteger) }
func isUnsigned(t Type) bool { return isBasic(t, IsUnsigned) }
func isFloat(t Type) bool    { return isBasic(t, IsFloat) }
func isNumeric(t Type) bool  { return isBasic(t, IsNumeric) }
func isString(t Type) bool   { return isBasic(t, IsString) }
func isOrdered(t Type) bool  { return isBasic(t, IsOrdered) }
func isConstType(t Type) bool {
	return isBasic(t, IsConstType)
}

// isUntyped reports whether t is the type of an untyped constant or value.
func isUntyped(t Type) bool {
	b, ok := t.(*Basic)
	return ok && b.info&IsUntyped != 0
}

// isValid reports whether t is a valid type. A defined type whose
// declaration is being checked is valid.
func isValid(t Type) bool {
	if n, ok := t.(*Named); ok && n.underlying == nil {
		return true
	}
	return t != nil && t.Underlying() != Typ[Invalid]
}

// hasInvalid reports whether t contains an invalid type. An error has
// then been reported already, and operations on values of t report no
// further errors.
func hasInvalid(t Type) bool {
	return hasInvalidDepth(t, 0)
}

func hasInvalidDepth(t Type, depth int) bool {
	if t == nil || depth > 8 {
		return t == nil
	}
	if tp, ok := t.(*TypeParam); ok {
		return !isValid(tp.bound) // its constraint was invalid
	}
	if _, ok := t.(*Named); ok && depth > 0 {
		return !isValid(t) // the components of defined types are checked at their declaration
	}
	switch t := t.Underlying().(type) {
	case *Basic:
		return t.kind == Invalid
	case *Array:
		return hasInvalidDepth(t.Elem, depth+1)
	case *Slice:
		return hasInvalidDepth(t.Elem, depth+1)
	case *Map:
		return hasInvalidDepth(t.Key, depth+1) || hasInvalidDepth(t.Elem, depth+1)
	case *Chan:
		return hasInvalidDepth(t.Elem, depth+1)
	case *Struct:
		for _, f := range t.Fields {
			if hasInvalidDepth(f.typ, depth+1) {
				return true
			}
		}
	}
	// Functions with invalid parameter types are still called, so that
	// argument counts are checked.
	return false
}

// isEmptyInterface reports whether t is an interface type with an empty
// method set (type.interface.empty-value).
func isEmptyInterface(t Type) bool {
	i, ok := t.Underlying().(*Interface)
	return ok && i.Empty()
}

// hasNil reports whether nil is a value of t (type.nil.types, expr.cmp.nil).
func hasNil(t Type) bool {
	if isTypeParam(t) {
		return underIs(t, hasNil)
	}
	switch t.Underlying().(type) {
	case *Slice, *Map, *Chan, *Signature, *Interface:
		return true
	}
	return false
}

// Identical reports whether x and y are the same type.
func Identical(x, y Type) bool { return identical(x, y, false) }

// identicalIgnoreTags reports whether x and y are the same type when
// struct tags are ignored, as in conversions (type.conv.go).
func identicalIgnoreTags(x, y Type) bool { return identical(x, y, true) }

// ifacePair is a pair of interface types being compared, to stop the
// comparison of interfaces that refer to each other.
type ifacePair struct {
	x, y *Interface
	prev *ifacePair
}

func identical(x, y Type, noTags bool) bool { return identicalIn(x, y, noTags, nil) }

func identicalIn(x, y Type, noTags bool, pairs *ifacePair) bool {
	x, y = unalias(x), unalias(y)
	if x == y {
		return true
	}
	switch x := x.(type) {
	case *Array:
		y, ok := y.(*Array)
		return ok && x.Len == y.Len && identicalIn(x.Elem, y.Elem, noTags, pairs)
	case *Slice:
		y, ok := y.(*Slice)
		return ok && identicalIn(x.Elem, y.Elem, noTags, pairs)
	case *Map:
		y, ok := y.(*Map)
		return ok && identicalIn(x.Key, y.Key, noTags, pairs) && identicalIn(x.Elem, y.Elem, noTags, pairs)
	case *Chan:
		y, ok := y.(*Chan)
		return ok && x.Dir == y.Dir && identicalIn(x.Elem, y.Elem, noTags, pairs)
	case *Struct:
		y, ok := y.(*Struct)
		if !ok || len(x.Fields) != len(y.Fields) || x.Layout != y.Layout {
			return false
		}
		for i, f := range x.Fields {
			g := y.Fields[i]
			if f.name != g.name || !noTags && x.Tags[i] != y.Tags[i] || !identicalIn(f.typ, g.typ, noTags, pairs) {
				return false
			}
		}
		return true
	case *Tuple:
		y, ok := y.(*Tuple)
		if !ok || x.Len() != y.Len() {
			return false
		}
		for i := range x.Len() {
			if !identicalIn(x.Vars[i].typ, y.Vars[i].typ, noTags, pairs) {
				return false
			}
		}
		return true
	case *Signature:
		y, ok := y.(*Signature)
		if !ok || x.Variadic != y.Variadic || !identicalIn(x.Params, y.Params, noTags, pairs) || !identicalIn(x.Results, y.Results, noTags, pairs) {
			return false
		}
		for i := range x.Params.Len() {
			if x.Params.Vars[i].inout != y.Params.Vars[i].inout {
				return false
			}
		}
		return true
	case *Named:
		// Instances of the same generic type are identical if their type
		// arguments are.
		y, ok := y.(*Named)
		if !ok || x.orig == nil || y.orig == nil || x.orig != y.orig || len(x.targs) != len(y.targs) {
			return false
		}
		for i := range x.targs {
			if !identicalIn(x.targs[i], y.targs[i], noTags, pairs) {
				return false
			}
		}
		return true
	case *Interface:
		y, ok := y.(*Interface)
		if !ok || len(x.MethodSet()) != len(y.MethodSet()) {
			return false
		}
		for p := pairs; p != nil; p = p.prev {
			if p.x == x && p.y == y || p.x == y && p.y == x {
				return true
			}
		}
		pairs = &ifacePair{x, y, pairs}
		for _, m := range x.MethodSet() {
			if n := lookupFunc(y.MethodSet(), m.name); n == nil || !identicalIn(m.typ, n.typ, noTags, pairs) {
				return false
			}
		}
		return true
	}
	return false
}

// comparable reports whether values of t can be compared with == and !=
// (expr.cmp.go). Channels, as handle types, compare only with nil
// (expr.cmp.sync).
func comparable(t Type) bool { return comparableIn(t, nil) }

// comparableIn is comparable; seen holds the defined types being checked,
// so that invalid recursive types do not recurse forever.
func comparableIn(t Type, seen []*Named) bool {
	if tp, ok := t.(*TypeParam); ok {
		return tp.iface().typeSet().comparable || underIs(tp, func(u Type) bool { return comparableIn(u, seen) })
	}
	if n, ok := t.(*Named); ok {
		if slices.Contains(seen, n) {
			return true
		}
		seen = append(seen, n)
	}
	switch t := t.Underlying().(type) {
	case *Basic:
		// Invalid types are comparable, so no further error is reported.
		return t.kind != UntypedNil
	case *Struct:
		for _, f := range t.Fields {
			if !comparableIn(f.typ, seen) {
				return false
			}
		}
		return true
	case *Array:
		return comparableIn(t.Elem, seen)
	case *Interface:
		return true
	}
	return false
}

// assignableTo reports whether a value of type v is assignable to type t
// (type.assign.go). v must not be untyped.
func (c *checker) assignableTo(v, t Type) bool {
	v, t = unalias(v), unalias(t)
	if Identical(v, t) {
		return true
	}
	// With type parameters, assignability holds for every type in the
	// type set, as in Go (type.param.go).
	if tp, ok := t.(*TypeParam); ok {
		return !isNamedType(v) && c.allTerms(tp, func(term Type) bool { return c.assignableTo(v, term) })
	}
	if tp, ok := v.(*TypeParam); ok {
		if ti, ok := t.Underlying().(*Interface); ok {
			return c.missingMethod(v, ti) == ""
		}
		return !isNamedType(t) && c.allTerms(tp, func(term Type) bool { return c.assignableTo(term, t) })
	}
	vu, tu := v.Underlying(), t.Underlying()
	_, vNamed := v.(*Named)
	_, tNamed := t.(*Named)
	if Identical(vu, tu) && (!vNamed || !tNamed) {
		if _, ok := vu.(*Basic); !ok {
			return true
		}
	}
	if ti, ok := tu.(*Interface); ok {
		return c.missingMethod(v, ti) == ""
	}
	if vc, ok := vu.(*Chan); ok && vc.Dir == SendRecv {
		if tc, ok := tu.(*Chan); ok && Identical(vc.Elem, tc.Elem) && (!vNamed || !tNamed) {
			return true
		}
	}
	return false
}

// intRange returns the bit size and signedness of an integer type.
func (c *checker) intRange(b *Basic) (bits int, signed bool) {
	bits = b.size
	switch b.kind {
	case Int, Uint:
		bits = c.intBits
	case Usize:
		bits = c.addrBits
	}
	return bits, b.info&IsUnsigned == 0
}

// representable reports whether the constant val is representable in
// the basic type t, and returns the value converted to t's kind of
// constant (rounded for floating-point types).
func (c *checker) representable(val constant.Value, t *Basic) (constant.Value, bool) {
	if val.Kind() == constant.Unknown {
		return val, true
	}
	switch {
	case t.info&IsInteger != 0:
		x := constant.ToInt(val)
		if x.Kind() != constant.Int {
			return val, false
		}
		if t.info&IsUntyped != 0 {
			return x, true
		}
		bits, signed := c.intRange(t)
		if signed {
			lo := constant.Shift(constant.MakeInt64(-1), tokenShl, uint(bits-1))
			hi := constant.BinaryOp(constant.UnaryOp(tokenSub, lo, 0), tokenSub, constant.MakeInt64(1))
			return x, constant.Compare(x, tokenGeq, lo) && constant.Compare(x, tokenLeq, hi)
		}
		hi := constant.BinaryOp(constant.Shift(constant.MakeInt64(1), tokenShl, uint(bits)), tokenSub, constant.MakeInt64(1))
		return x, constant.Sign(x) >= 0 && constant.Compare(x, tokenLeq, hi)
	case t.info&IsFloat != 0:
		x := constant.ToFloat(val)
		if x.Kind() != constant.Float {
			return val, false
		}
		switch t.kind {
		case F32:
			f, _ := constant.Float32Val(x)
			if math.IsInf(float64(f), 0) {
				return val, false
			}
			return constant.MakeFloat64(float64(f)), true
		case F64:
			f, _ := constant.Float64Val(x)
			if math.IsInf(f, 0) {
				return val, false
			}
			return constant.MakeFloat64(f), true
		}
		return x, true
	case t.info&IsBoolean != 0:
		return val, val.Kind() == constant.Bool
	case t.info&IsString != 0:
		return val, val.Kind() == constant.String
	case t.info&IsTerminated != 0:
		return val, val.Kind() == constant.String
	}
	return val, false
}

// defaultType returns the default type of an untyped constant or value
// (lex.literal.default-type): untyped runes default to i32.
func defaultType(t Type) Type {
	if b, ok := t.(*Basic); ok {
		switch b.kind {
		case UntypedBool:
			return Typ[Bool]
		case UntypedInt:
			return Typ[Int]
		case UntypedRune:
			return Typ[I32]
		case UntypedFloat:
			return Typ[F64]
		case UntypedString:
			return Typ[String]
		}
	}
	return t
}
