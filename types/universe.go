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
	_Assert

	// Functions of an/fmt implemented by the compiler.
	_FmtPrint
	_FmtPrintln
	_FmtSprint
	_FmtSprintln
	_FmtPrintf
	_FmtSprintf

	// Functions of an/bits implemented by the compiler (expr.bits.functions).
	_BitsWrappingAdd
	_BitsWrappingSub
	_BitsWrappingMul
	_BitsSaturatingAdd
	_BitsSaturatingSub
	_BitsSaturatingMul
	_BitsCheckedAdd
	_BitsCheckedSub
	_BitsCheckedMul

	// Compiler-known functions of an/effect (spec/07-effects.md).
	_EffectMark
	_EffectDefer
	_EffectDeclare
	_EffectWithout

	// Compile-time functions of an/meta (spec/10-compile-time.md §10.4).
	_MetaName
	_MetaFields
	_MetaVariants
	_MetaReject

	// Functions of an/unsafe (spec/06-memory.md §11).
	_UnsafeMap
	_UnsafeData

	// Compile-time functions of an/encoding (spec/10-compile-time.md §10.7).
	_EncodingCP866

	// Functions of an/gen with effect ReadInput (spec/10-compile-time.md
	// §10.1).
	_GenReadFile

	// Functions of an/console (spec/18-stdlib.md).
	_ConsoleReadByte

	// Contract functions of an/math (std.math.contract).
	_MathSum
	_MathForAll
	_MathExists
)

var builtinNames = map[builtinID]string{
	_Append: "append", _Cap: "cap", _Clear: "clear", _Close: "close", _Copy: "copy",
	_Delete: "delete", _Len: "len", _Make: "make", _Max: "max", _Min: "min",
	_Panic: "panic", _Print: "print", _Println: "println", _Assert: "assert",
	_FmtPrint: "Print", _FmtPrintln: "Println", _FmtSprint: "Sprint", _FmtSprintln: "Sprintln",
	_FmtPrintf: "Printf", _FmtSprintf: "Sprintf",
	_BitsWrappingAdd: "WrappingAdd", _BitsWrappingSub: "WrappingSub", _BitsWrappingMul: "WrappingMul",
	_BitsSaturatingAdd: "SaturatingAdd", _BitsSaturatingSub: "SaturatingSub", _BitsSaturatingMul: "SaturatingMul",
	_BitsCheckedAdd: "CheckedAdd", _BitsCheckedSub: "CheckedSub", _BitsCheckedMul: "CheckedMul",
	_EffectMark: "Mark", _EffectDefer: "Defer", _EffectDeclare: "Declare", _EffectWithout: "Without",
	_MetaName: "Name", _MetaFields: "Fields", _MetaVariants: "Variants", _MetaReject: "Reject",
	_UnsafeMap: "Map", _UnsafeData: "Data", _EncodingCP866: "CP866",
	_GenReadFile: "ReadFile", _ConsoleReadByte: "ReadByte", _MathSum: "Sum", _MathForAll: "ForAll", _MathExists: "Exists",
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
		if t.kind == Invalid || t.info&(IsUntyped|IsWrapping) != 0 {
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

	Universe.Insert(&Const{object: object{name: "true", typ: Typ[UntypedBool]}, Val: constant.MakeBool(true)})
	Universe.Insert(&Const{object: object{name: "false", typ: Typ[UntypedBool]}, Val: constant.MakeBool(false)})
	universeIota = &Const{object: object{name: "iota", typ: Typ[UntypedInt]}, Val: constant.MakeInt64(0)}
	Universe.Insert(universeIota)
	Universe.Insert(&Nil{object{name: "nil", typ: Typ[UntypedNil]}})

	for id := _Append; id <= _Assert; id++ {
		Universe.Insert(&Builtin{object: object{name: builtinNames[id], typ: Typ[Invalid]}, id: id})
	}
}

// stdPackages are the standard library packages this compiler can
// compile uses of.
//
// Temporary: an/fmt is built into the compiler until the standard library
// is compiled from Anlang source (roadmap steps 9–10).
var stdPackages = map[string]*Package{
	"an/fmt":    newStdPackage("an/fmt", _FmtPrint, _FmtPrintln, _FmtSprint, _FmtSprintln, _FmtPrintf, _FmtSprintf),
	"an/wrap":   newWrapPackage(),
	"an/effect": newEffectPackage(),
	"an/meta":   newMetaPackage(),
	"an/unsafe": newUnsafePackage(),
	// Temporary: the functions of an/encoding are built into the compiler
	// until compile-time evaluation can run functions that allocate in
	// packages of the core profile.
	"an/encoding": newStdPackage("an/encoding", _EncodingCP866),
	// Temporary: an/gen is built into the compiler until files can be read
	// from Anlang source.
	"an/gen":     newStdPackage("an/gen", _GenReadFile),
	"an/console": newStdPackage("an/console", _ConsoleReadByte),
	// Temporary: an/math has only its contract functions until the
	// standard library is compiled from Anlang source.
	"an/math": newStdPackage("an/math", _MathSum, _MathForAll, _MathExists),
	"an/bits": newStdPackage("an/bits", _BitsWrappingAdd, _BitsWrappingSub, _BitsWrappingMul,
		_BitsSaturatingAdd, _BitsSaturatingSub, _BitsSaturatingMul, _BitsCheckedAdd, _BitsCheckedSub, _BitsCheckedMul),
}

// BaseEffects are the base effects of package an/effect (eff.base.set),
// with the hardware classes known before device descriptions (step 16).
var BaseEffects = []string{
	"Alloc", "StaticAlloc", "Console", "File", "Network", "Video", "Keyboard",
	"Panic", "Block", "Tasks", "Interrupts", "Gpio", "Uart", "Timers",
	"Clock", "Random", "Global", "Foreign",
}

// EffectType is the type effect.Effect. The value of an effect constant
// is a string "IDENTITY#NAME": the identity is the qualified name of the
// declaring constant (eff.model.identity), NAME the bare name
// (eff.model.names).
var EffectType *Named

// newEffectPackage returns package an/effect: the type Effect, the base
// effects and the compiler-known functions.
func newEffectPackage() *Package {
	pkg := newStdPackage("an/effect", _EffectMark, _EffectDefer, _EffectDeclare, _EffectWithout)
	obj := &TypeName{object{name: "Effect", pkg: pkg}}
	EffectType = &Named{Obj: obj, underlying: Typ[String]}
	obj.typ = EffectType
	pkg.Scope.Insert(obj)
	for _, name := range BaseEffects {
		pkg.Scope.Insert(&Const{object: object{name: name, typ: EffectType, pkg: pkg}, Val: constant.MakeString("an/effect." + name + "#" + name)})
	}
	return pkg
}

// UnsafeKinds are the pointer kind types of package an/unsafe
// (mem.unsafe.kinds): Flat and Near have the width of usize, Far and
// Program 32 bits.
var UnsafeKinds = map[string]*Named{}

// newUnsafePackage returns package an/unsafe (spec/06-memory.md §11):
// the pointer kinds, Map and Data. Only packages of the core profile
// import it (decl.import.unsafe).
func newUnsafePackage() *Package {
	pkg := newStdPackage("an/unsafe", _UnsafeMap, _UnsafeData)
	pkg.Profiles = []string{"core"}
	for _, k := range []struct {
		name string
		u    BasicKind
	}{{"Flat", Usize}, {"Near", Usize}, {"Far", U32}, {"Program", U32}} {
		obj := &TypeName{object{name: k.name, pkg: pkg}}
		t := &Named{Obj: obj, underlying: Typ[k.u]}
		obj.typ = t
		pkg.Scope.Insert(obj)
		UnsafeKinds[k.name] = t
	}
	return pkg
}

// IsPointerKind reports whether t is a pointer kind of an/unsafe.
func IsPointerKind(t Type) bool {
	for _, k := range UnsafeKinds {
		if t == Type(k) {
			return true
		}
	}
	return false
}

// newWrapPackage returns package an/wrap with its wrapping integer types
// (type.known.wrap).
func newWrapPackage() *Package {
	pkg := newStdPackage("an/wrap")
	for k := WrapU8; k <= WrapI64; k++ {
		t := Typ[k]
		pkg.Scope.Insert(&TypeName{object{name: t.name[len("wrap."):], typ: t, pkg: pkg}})
	}
	return pkg
}

func newStdPackage(path string, funcs ...builtinID) *Package {
	pkg := &Package{Path: path, Name: path[len("an/"):], Scope: NewScope(nil), Profiles: []string{"core", "app", "ui"}}
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
