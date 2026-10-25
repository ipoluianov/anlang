package types

import (
	"go/constant"
)

// builtinID identifies a built-in function.
type builtinID int

const (
	_Append builtinID = iota
	_Cap
	_Clear
	_Close
	_Copy
	_Delete
	_Len
	_Make
	_Max
	_Min
	_Panic
	_Print
	_Println

	// Functions of an/fmt implemented by the compiler.
	_FmtPrint
	_FmtPrintln
	_FmtSprint
	_FmtSprintln
)

var builtinNames = map[builtinID]string{
	_Append: "append", _Cap: "cap", _Clear: "clear", _Close: "close", _Copy: "copy",
	_Delete: "delete", _Len: "len", _Make: "make", _Max: "max", _Min: "min",
	_Panic: "panic", _Print: "print", _Println: "println",
	_FmtPrint: "Print", _FmtPrintln: "Println", _FmtSprint: "Sprint", _FmtSprintln: "Sprintln",
}

// Universe is the universe block (type.universe).
var Universe = NewScope(nil)

// universe objects referred to by the checker.
var (
	universeAny   *TypeName
	universeError *Named
	universeIota  *Const

	universeOption    *Named
	universeOptionGet *Func
)

// notYet lists predeclared identifiers that this compiler does not
// support yet, with the roadmap step that adds them.
var notYet = map[string]int{}

// removed lists Go predeclared identifiers that do not exist in Anlang,
// with the rule that removes them and the replacement, if any.
var removed = map[string]struct{ rule, instead string }{
	"int8":       {"type.numeric.removed", "i8"},
	"int16":      {"type.numeric.removed", "i16"},
	"int32":      {"type.numeric.removed", "i32"},
	"int64":      {"type.numeric.removed", "i64"},
	"uint8":      {"type.numeric.removed", "u8"},
	"uint16":     {"type.numeric.removed", "u16"},
	"uint32":     {"type.numeric.removed", "u32"},
	"uint64":     {"type.numeric.removed", "u64"},
	"uintptr":    {"type.numeric.removed", "usize"},
	"float32":    {"type.numeric.removed", "f32"},
	"float64":    {"type.numeric.removed", "f64"},
	"complex64":  {"type.numeric.removed", ""},
	"complex128": {"type.numeric.removed", ""},
	"byte":       {"type.numeric.removed", "u8"},
	"rune":       {"type.numeric.removed", "i32"},
	"new":        {"type.universe.removed", ""},
	"complex":    {"type.universe.removed", ""},
	"real":       {"type.universe.removed", ""},
	"imag":       {"type.universe.removed", ""},
	"recover":    {"type.universe.removed", ""},
}

func init() {
	for _, t := range Typ {
		if t.kind == Invalid || t.info&IsUntyped != 0 {
			continue
		}
		Universe.Insert(&TypeName{object{name: t.name, typ: t}})
	}

	universeAny = &TypeName{object{name: "any", typ: &Interface{}}}
	Universe.Insert(universeAny)

	errObj := &TypeName{object{name: "error"}}
	universeError = &Named{Obj: errObj}
	errObj.typ = universeError
	errorMethod := &Func{object: object{name: "Error", typ: &Signature{
		Params:  &Tuple{},
		Results: &Tuple{Vars: []*Var{{object: object{typ: Typ[String]}, param: true}}},
	}}}
	universeError.underlying = &Interface{Methods: []*Func{errorMethod}}
	Universe.Insert(errObj)

	declareOption()

	cmpObj := &TypeName{object{name: "comparable"}}
	cmpObj.typ = &Named{Obj: cmpObj, underlying: &Interface{isComparable: true}}
	Universe.Insert(cmpObj)

	Universe.Insert(&Const{object{name: "true", typ: Typ[UntypedBool]}, constant.MakeBool(true)})
	Universe.Insert(&Const{object{name: "false", typ: Typ[UntypedBool]}, constant.MakeBool(false)})
	universeIota = &Const{object{name: "iota", typ: Typ[UntypedInt]}, constant.MakeInt64(0)}
	Universe.Insert(universeIota)
	Universe.Insert(&Nil{object{name: "nil", typ: Typ[UntypedNil]}})

	for id := _Append; id <= _Println; id++ {
		Universe.Insert(&Builtin{object: object{name: builtinNames[id], typ: Typ[Invalid]}, id: id})
	}
}

// stdPackages are the standard library packages this compiler can
// compile uses of.
//
// Temporary: an/fmt is built into the compiler until the standard library
// is compiled from Anlang source (roadmap steps 9–10).
var stdPackages = map[string]*Package{
	"an/fmt": newStdPackage("an/fmt", _FmtPrint, _FmtPrintln, _FmtSprint, _FmtSprintln),
}

func newStdPackage(path string, funcs ...builtinID) *Package {
	pkg := &Package{Path: path, Name: path[len("an/"):], Scope: NewScope(nil)}
	for _, id := range funcs {
		pkg.Scope.Insert(&Builtin{object: object{name: builtinNames[id], typ: Typ[Invalid]}, id: id, pkg: path})
	}
	return pkg
}

// declareOption declares the predeclared generic enum type Option, its
// variants None and Some, and its method Get (type.option.decl):
//
//	type Option[T any] enum {
//	    None
//	    Some{value T}
//	}
//
//	func (o Option[T]) Get() (T, bool)
func declareOption() {
	obj := &TypeName{object{name: "Option"}}
	tobj := &TypeName{object{name: "T"}}
	tp := &TypeParam{Obj: tobj, bound: universeAny.typ}
	tobj.typ = tp
	opt := &Named{Obj: obj, TypeParams: []*TypeParam{tp}}
	obj.typ = opt
	none := &Variant{object: object{name: "None", typ: opt}, enum: opt, index: 0}
	value := &Var{object: object{name: "value", typ: tp}, field: true}
	some := &Variant{object: object{name: "Some", typ: opt}, enum: opt, index: 1, data: true, fields: []*Var{value}}
	opt.underlying = &Enum{Variants: []*Variant{none, some}}

	get := &Func{object: object{name: "Get"}}
	get.typ = &Signature{
		Recv:   &Var{object: object{name: "o", typ: opt}, param: true},
		Params: &Tuple{},
		Results: &Tuple{Vars: []*Var{
			{object: object{typ: tp}, param: true},
			{object: object{typ: Typ[Bool]}, param: true},
		}},
		RecvTypeParams: []*TypeParam{tp},
	}
	opt.methods = []*Func{get}
	universeOption, universeOptionGet = opt, get

	Universe.Insert(obj)
	Universe.Insert(none)
	Universe.Insert(some)
}
