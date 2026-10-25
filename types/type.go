package types

import (
	"fmt"
	"strings"
)

// Type is an Anlang type (spec/02-types.md).
type Type interface {
	// Underlying returns the underlying type: the type itself for every
	// type except *Named.
	Underlying() Type
	String() string
}

// BasicKind identifies a predeclared type.
type BasicKind int

const (
	Invalid BasicKind = iota

	Bool

	// Fixed-size integers (type.int.fixed).
	I8
	I16
	I32
	I64
	U8
	U16
	U32
	U64

	// Target-dependent integers (type.int.word, type.int.usize).
	Int
	Uint
	Usize

	// Byte-order integers (type.endian).
	I16le
	I16be
	I32le
	I32be
	I64le
	I64be
	U16le
	U16be
	U32le
	U32be
	U64le
	U64be

	F32
	F64

	String
	Cstring
	Dstring

	// Types of untyped constants and values.
	UntypedBool
	UntypedInt
	UntypedRune
	UntypedFloat
	UntypedString
	UntypedNil
)

// BasicInfo is a set of properties of a basic type.
type BasicInfo int

const (
	IsBoolean BasicInfo = 1 << iota
	IsInteger
	IsUnsigned
	IsFloat
	IsString
	IsTerminated // cstring and dstring
	IsUntyped

	IsOrdered   = IsInteger | IsFloat | IsString
	IsNumeric   = IsInteger | IsFloat
	IsConstType = IsBoolean | IsNumeric | IsString
)

// Basic is a predeclared type.
type Basic struct {
	kind BasicKind
	info BasicInfo
	size int // in bits for fixed-size numeric types; 0 if target-dependent or not numeric
	name string
}

func (b *Basic) Kind() BasicKind  { return b.kind }
func (b *Basic) Info() BasicInfo  { return b.info }
func (b *Basic) Name() string     { return b.name }
func (b *Basic) Underlying() Type { return b }
func (b *Basic) String() string   { return b.name }

// Typ holds the basic types, indexed by kind.
var Typ = [...]*Basic{
	Invalid: {Invalid, 0, 0, "invalid type"},

	Bool: {Bool, IsBoolean, 0, "bool"},

	I8:  {I8, IsInteger, 8, "i8"},
	I16: {I16, IsInteger, 16, "i16"},
	I32: {I32, IsInteger, 32, "i32"},
	I64: {I64, IsInteger, 64, "i64"},
	U8:  {U8, IsInteger | IsUnsigned, 8, "u8"},
	U16: {U16, IsInteger | IsUnsigned, 16, "u16"},
	U32: {U32, IsInteger | IsUnsigned, 32, "u32"},
	U64: {U64, IsInteger | IsUnsigned, 64, "u64"},

	Int:   {Int, IsInteger, 0, "int"},
	Uint:  {Uint, IsInteger | IsUnsigned, 0, "uint"},
	Usize: {Usize, IsInteger | IsUnsigned, 0, "usize"},

	I16le: {I16le, IsInteger, 16, "i16le"},
	I16be: {I16be, IsInteger, 16, "i16be"},
	I32le: {I32le, IsInteger, 32, "i32le"},
	I32be: {I32be, IsInteger, 32, "i32be"},
	I64le: {I64le, IsInteger, 64, "i64le"},
	I64be: {I64be, IsInteger, 64, "i64be"},
	U16le: {U16le, IsInteger | IsUnsigned, 16, "u16le"},
	U16be: {U16be, IsInteger | IsUnsigned, 16, "u16be"},
	U32le: {U32le, IsInteger | IsUnsigned, 32, "u32le"},
	U32be: {U32be, IsInteger | IsUnsigned, 32, "u32be"},
	U64le: {U64le, IsInteger | IsUnsigned, 64, "u64le"},
	U64be: {U64be, IsInteger | IsUnsigned, 64, "u64be"},

	F32: {F32, IsFloat, 32, "f32"},
	F64: {F64, IsFloat, 64, "f64"},

	String:  {String, IsString, 0, "string"},
	Cstring: {Cstring, IsTerminated, 0, "cstring"},
	Dstring: {Dstring, IsTerminated, 0, "dstring"},

	UntypedBool:   {UntypedBool, IsBoolean | IsUntyped, 0, "untyped bool"},
	UntypedInt:    {UntypedInt, IsInteger | IsUntyped, 0, "untyped int"},
	UntypedRune:   {UntypedRune, IsInteger | IsUntyped, 0, "untyped rune"},
	UntypedFloat:  {UntypedFloat, IsFloat | IsUntyped, 0, "untyped float"},
	UntypedString: {UntypedString, IsString | IsUntyped, 0, "untyped string"},
	UntypedNil:    {UntypedNil, IsUntyped, 0, "untyped nil"},
}

// Array is [Len]Elem.
type Array struct {
	Len  int64
	Elem Type
}

// Slice is []Elem.
type Slice struct {
	Elem Type
}

// Map is map[Key]Elem.
type Map struct {
	Key, Elem Type
}

// ChanDir is the direction of a channel type.
type ChanDir int

const (
	SendRecv ChanDir = iota
	SendOnly
	RecvOnly
)

// Chan is a channel type, a handle type (type.chan.handle).
type Chan struct {
	Dir  ChanDir
	Elem Type
}

// Struct is a struct type.
type Struct struct {
	Fields []*Var
	Tags   []string // "" for a field without a tag
	Layout string   // "", "c" or "packed" (type.layout.c, type.layout.packed)
}

// Tuple is an ordered list of variables: the parameters or results of a
// signature, or the values of a multi-value expression.
type Tuple struct {
	Vars []*Var
}

// Len returns the number of variables of t; t may be nil.
func (t *Tuple) Len() int {
	if t == nil {
		return 0
	}
	return len(t.Vars)
}

// At returns the i'th variable of t.
func (t *Tuple) At(i int) *Var { return t.Vars[i] }

// Signature is a function type. If Variadic is set, the last parameter
// has a slice type []T and is written ...T.
type Signature struct {
	Params   *Tuple
	Results  *Tuple
	Variadic bool
}

// Interface is an interface type.
type Interface struct {
	Methods []*Func
}

// Empty reports whether the interface has an empty method set
// (type.interface.empty-value).
func (t *Interface) Empty() bool { return len(t.Methods) == 0 }

// Named is a defined type.
type Named struct {
	Obj        *TypeName
	underlying Type // nil while the declaration is being checked
}

func (t *Array) Underlying() Type     { return t }
func (t *Slice) Underlying() Type     { return t }
func (t *Map) Underlying() Type       { return t }
func (t *Chan) Underlying() Type      { return t }
func (t *Struct) Underlying() Type    { return t }
func (t *Tuple) Underlying() Type     { return t }
func (t *Signature) Underlying() Type { return t }
func (t *Interface) Underlying() Type { return t }

func (t *Named) Underlying() Type {
	if t.underlying == nil {
		return Typ[Invalid]
	}
	return t.underlying
}

func (t *Array) String() string     { return typeString(t) }
func (t *Slice) String() string     { return typeString(t) }
func (t *Map) String() string       { return typeString(t) }
func (t *Chan) String() string      { return typeString(t) }
func (t *Struct) String() string    { return typeString(t) }
func (t *Tuple) String() string     { return typeString(t) }
func (t *Signature) String() string { return typeString(t) }
func (t *Interface) String() string { return typeString(t) }
func (t *Named) String() string     { return typeString(t) }

func typeString(t Type) string {
	var b strings.Builder
	writeType(&b, t)
	return b.String()
}

func writeType(b *strings.Builder, t Type) {
	switch t := t.(type) {
	case nil:
		b.WriteString("<nil>")
	case *Basic:
		b.WriteString(t.name)
	case *Array:
		fmt.Fprintf(b, "[%d]", t.Len)
		writeType(b, t.Elem)
	case *Slice:
		b.WriteString("[]")
		writeType(b, t.Elem)
	case *Map:
		b.WriteString("map[")
		writeType(b, t.Key)
		b.WriteString("]")
		writeType(b, t.Elem)
	case *Chan:
		switch t.Dir {
		case SendOnly:
			b.WriteString("chan<- ")
		case RecvOnly:
			b.WriteString("<-chan ")
		default:
			b.WriteString("chan ")
		}
		writeType(b, t.Elem)
	case *Struct:
		b.WriteString("struct{")
		for i, f := range t.Fields {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(f.name)
			b.WriteString(" ")
			writeType(b, f.typ)
		}
		b.WriteString("}")
	case *Tuple:
		writeTuple(b, t, false)
	case *Signature:
		b.WriteString("func")
		writeSignature(b, t)
	case *Interface:
		if t.Empty() {
			b.WriteString("interface{}")
			return
		}
		b.WriteString("interface{")
		for i, m := range t.Methods {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(m.name)
			writeSignature(b, m.typ.(*Signature))
		}
		b.WriteString("}")
	case *Named:
		b.WriteString(t.Obj.name)
	default:
		fmt.Fprintf(b, "%T", t)
	}
}

func writeTuple(b *strings.Builder, t *Tuple, variadic bool) {
	b.WriteString("(")
	if t == nil {
		t = &Tuple{}
	}
	for i, v := range t.Vars {
		if i > 0 {
			b.WriteString(", ")
		}
		if v.inout {
			b.WriteString("inout ")
		}
		if variadic && i == len(t.Vars)-1 {
			b.WriteString("...")
			writeType(b, v.typ.(*Slice).Elem)
			continue
		}
		writeType(b, v.typ)
	}
	b.WriteString(")")
}

func writeSignature(b *strings.Builder, s *Signature) {
	writeTuple(b, s.Params, s.Variadic)
	switch n := s.Results.Len(); {
	case n == 0:
	case n == 1:
		b.WriteString(" ")
		writeType(b, s.Results.Vars[0].typ)
	default:
		b.WriteString(" ")
		writeTuple(b, s.Results, false)
	}
}
