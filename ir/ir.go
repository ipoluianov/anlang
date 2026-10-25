// Package ir defines the shared SSA representation of programs
// (spec/22-compiler.md §22.3, §22.10).
//
// A program is a set of functions; a function is a graph of basic
// blocks; a block is a sequence of instructions ending in a control
// instruction. Every value is defined once. Local variables are storage
// cells: Alloc yields the address of a cell, read by Load and written by
// Store. Addresses of parts of values come from FieldAddr and IndexAddr;
// an inout parameter is an address. Types are those of package types,
// with all type parameters substituted: generic functions are
// instantiated per type argument list (monomorphization).
//
// Value semantics (spec/06-memory.md §1) are explicit: Copy marks the
// points where a value is logically copied — assignment, argument
// passing, return, storing into a part — so that a backend can implement
// copy-on-write (spec/22-compiler.md §22.6).
package ir

import (
	"go/constant"

	"an/syntax"
	"an/types"
)

// Program is a whole program.
type Program struct {
	Funcs   []*Func
	Main    *Func
	IntBits int // size of int and uint in bits (type.int.word)

	// MethodSets lists the methods of the types that are converted to
	// interfaces, for dynamic calls and type assertions.
	MethodSets []*MethodSet
}

// MethodSet is the set of methods of a concrete type.
type MethodSet struct {
	Type    types.Type
	Methods map[string]*Func
}

// Methods returns the method set of type t, or nil.
func (p *Program) Methods(t types.Type) *MethodSet {
	for _, ms := range p.MethodSets {
		if types.Identical(ms.Type, t) {
			return ms
		}
	}
	return nil
}

// Func is a function, a method (its receiver is the first parameter) or
// a function literal.
type Func struct {
	Name     string
	Sig      *types.Signature
	Params   []*Param
	FreeVars []*FreeVar // captured values or addresses of a function literal
	Blocks   []*Block
	Pos      syntax.Pos

	nvalues int
}

// Block is a basic block.
type Block struct {
	Index   int
	Instrs  []Instr
	Preds   []*Block
	Succs   []*Block
	Comment string
	Func    *Func
}

// Value is an operand: a constant, a parameter, a free variable, a
// function, or the result of an instruction.
type Value interface {
	Type() types.Type
	Name() string
}

// Instr is an instruction.
type Instr interface {
	Operands() []Value
	block() *Block
	setBlock(*Block)
}

// Pointer is the type of an address in IR. It is not an Anlang type.
type Pointer struct {
	Elem types.Type
}

func (t *Pointer) Underlying() types.Type { return t }
func (t *Pointer) String() string         { return "*" + t.Elem.String() }

// Tuple is the type of the results of a call with several results.
type Tuple = types.Tuple

// ---------------------------------------------------------------------
// Values that are not instructions.

// Const is a constant. Val is nil for the zero value of Typ, including
// nil.
type Const struct {
	Typ types.Type
	Val constant.Value
}

func (c *Const) Type() types.Type { return c.Typ }
func (c *Const) Name() string {
	if c.Val == nil {
		return "zero"
	}
	return c.Val.ExactString()
}

// Param is a parameter of a function. The parameter of an inout
// parameter is the address of the caller's value.
type Param struct {
	Pname string
	Typ   types.Type
	Inout bool
}

func (p *Param) Type() types.Type { return p.Typ }
func (p *Param) Name() string     { return p.Pname }

// FreeVar is a value or an address captured by a function literal.
type FreeVar struct {
	Vname string
	Typ   types.Type
}

func (v *FreeVar) Type() types.Type { return v.Typ }
func (v *FreeVar) Name() string     { return v.Vname }

// FuncValue is a function used as a value or as a callee.
type FuncValue struct {
	Func *Func
}

func (f *FuncValue) Type() types.Type { return f.Func.Sig }
func (f *FuncValue) Name() string     { return f.Func.Name }

// Builtin is a built-in function or a function of the standard library
// implemented by the compiler.
type Builtin struct {
	Bname string // "len", "append", "fmt.Println", ...
	Typ   types.Type
}

func (b *Builtin) Type() types.Type { return b.Typ }
func (b *Builtin) Name() string     { return b.Bname }

// ---------------------------------------------------------------------
// Instructions.

type anInstr struct {
	b *Block
}

func (i *anInstr) block() *Block     { return i.b }
func (i *anInstr) setBlock(b *Block) { i.b = b }

// register is embedded in instructions that define a value.
type register struct {
	anInstr
	id  int
	typ types.Type
}

func (r *register) Type() types.Type { return r.typ }
func (r *register) Name() string     { return "t" + itoa(r.id) }

// SetType sets the type of the value defined by an instruction; it is
// used by the builder.
func (r *register) SetType(t types.Type) { r.typ = t }

func (r *register) reg() *register { return r }

// ID returns the number of the value in its function, from 0 to
// NumValues-1.
func (r *register) ID() int { return r.id }

// NumValues returns the number of values the instructions of f define.
func (f *Func) NumValues() int { return f.nvalues }

// Alloc allocates a storage cell for a value of type Elem and yields its
// address. The cell holds the zero value.
type Alloc struct {
	register
	Comment string // the variable
}

// Load reads the value at an address. The result shares the storage of
// the value; Copy makes a logical copy.
type Load struct {
	register
	Addr Value
}

// Store writes Val at Addr.
type Store struct {
	anInstr
	Addr Value
	Val  Value
}

// Copy is a logical copy of a value (mem.value.copy).
type Copy struct {
	register
	X Value
}

// FieldAddr yields the address of field Field of the struct at X.
type FieldAddr struct {
	register
	X     Value
	Field int
}

// IndexAddr yields the address of element Index of the array or slice
// at X.
type IndexAddr struct {
	register
	X     Value
	Index Value
}

// SubsliceAddr yields the address of the elements Low to High-1 of the
// slice or array at X, for an inout argument s[lo:hi]
// (mem.inout.subslice). Loading it yields a slice of those elements;
// storing a slice of the same length writes its elements back to X, and
// storing one of another length panics.
type SubsliceAddr struct {
	register
	X         Value
	Low, High Value // nil for 0 and the length of X
}

// Field yields field Field of struct value X.
type Field struct {
	register
	X     Value
	Field int
}

// Index yields element Index of array or string value X.
type Index struct {
	register
	X     Value
	Index Value
}

// Lookup yields the element of map X with key Key; with CommaOk, a pair
// of the element and whether it was present.
type Lookup struct {
	register
	X       Value
	Key     Value
	CommaOk bool
}

// MapUpdate sets the element of the map at address Map.
type MapUpdate struct {
	anInstr
	Map   Value
	Key   Value
	Value Value
}

// Slice yields X[Low:High:Max] of a slice, a string, or the array at
// address X. Low, High and Max may be nil.
type Slice struct {
	register
	X              Value
	Low, High, Max Value
}

// BinOp is a binary operation; the operands have the same type, except
// for shifts. Comparisons yield bool.
type BinOp struct {
	register
	Op   syntax.Kind
	X, Y Value
}

// UnOp is -X, ^X, !X or <-X.
type UnOp struct {
	register
	Op      syntax.Kind
	X       Value
	CommaOk bool // a receive with comma-ok
}

// Convert converts X to its type: numeric and string conversions,
// changes between types with the same underlying type, slice to array.
type Convert struct {
	register
	X Value
}

// MakeInterface converts the non-interface value X to an interface type.
type MakeInterface struct {
	register
	X Value
}

// TypeAssert asserts that interface value X holds a value of type
// Asserted, or implements it if Asserted is an interface.
type TypeAssert struct {
	register
	X        Value
	Asserted types.Type
	CommaOk  bool
}

// MakeSlice makes a slice of length Len and capacity Cap.
type MakeSlice struct {
	register
	Len, Cap Value
}

// MakeMap makes an empty map.
type MakeMap struct {
	register
}

// MakeStruct builds a struct value from its fields.
type MakeStruct struct {
	register
	Fields []Value
}

// MakeArray builds an array or slice value from its elements.
type MakeArray struct {
	register
	Elems []Value
}

// MakeVariant builds a value of an enum type: variant Variant with its
// fields.
type MakeVariant struct {
	register
	Variant int
	Fields  []Value
}

// VariantIndex yields the index of the variant of enum value X.
type VariantIndex struct {
	register
	X Value
}

// VariantField yields field Field of variant Variant of enum value X.
type VariantField struct {
	register
	X       Value
	Variant int
	Field   int
}

// MakeClosure makes a function value from a function literal and the
// values or addresses it captures.
type MakeClosure struct {
	register
	Fn       *Func
	Bindings []Value
}

// Call calls Callee with Args. If Method is set, Callee is an interface
// value and the call is dynamic. The type is the result, a tuple of the
// results, or the empty tuple.
type Call struct {
	register
	Callee Value
	Method string
	Args   []Value
}

// Extract yields result Index of a call with several results.
type Extract struct {
	register
	Tuple Value
	Index int
}

// Jump transfers control to the only successor.
type Jump struct {
	anInstr
}

// If transfers control to the first successor if Cond is true, to the
// second otherwise.
type If struct {
	anInstr
	Cond Value
}

// Return returns from the function, after running the deferred calls.
type Return struct {
	anInstr
	Results []Value
}

// Panic panics with X.
type Panic struct {
	anInstr
	X Value
}

// Defer schedules Call to run when the function returns. The callee and
// the arguments are evaluated when the Defer instruction runs.
type Defer struct {
	anInstr
	Call *Call
}

// Operands of the instructions.

func (i *Alloc) Operands() []Value         { return nil }
func (i *Load) Operands() []Value          { return []Value{i.Addr} }
func (i *Store) Operands() []Value         { return []Value{i.Addr, i.Val} }
func (i *Copy) Operands() []Value          { return []Value{i.X} }
func (i *FieldAddr) Operands() []Value     { return []Value{i.X} }
func (i *IndexAddr) Operands() []Value     { return []Value{i.X, i.Index} }
func (i *SubsliceAddr) Operands() []Value  { return []Value{i.X, i.Low, i.High} }
func (i *Field) Operands() []Value         { return []Value{i.X} }
func (i *Index) Operands() []Value         { return []Value{i.X, i.Index} }
func (i *Lookup) Operands() []Value        { return []Value{i.X, i.Key} }
func (i *MapUpdate) Operands() []Value     { return []Value{i.Map, i.Key, i.Value} }
func (i *Slice) Operands() []Value         { return []Value{i.X, i.Low, i.High, i.Max} }
func (i *BinOp) Operands() []Value         { return []Value{i.X, i.Y} }
func (i *UnOp) Operands() []Value          { return []Value{i.X} }
func (i *Convert) Operands() []Value       { return []Value{i.X} }
func (i *MakeInterface) Operands() []Value { return []Value{i.X} }
func (i *TypeAssert) Operands() []Value    { return []Value{i.X} }
func (i *MakeSlice) Operands() []Value     { return []Value{i.Len, i.Cap} }
func (i *MakeMap) Operands() []Value       { return nil }
func (i *MakeStruct) Operands() []Value    { return i.Fields }
func (i *MakeArray) Operands() []Value     { return i.Elems }
func (i *MakeVariant) Operands() []Value   { return i.Fields }
func (i *VariantIndex) Operands() []Value  { return []Value{i.X} }
func (i *VariantField) Operands() []Value  { return []Value{i.X} }
func (i *MakeClosure) Operands() []Value   { return i.Bindings }
func (i *Call) Operands() []Value          { return append([]Value{i.Callee}, i.Args...) }
func (i *Extract) Operands() []Value       { return []Value{i.Tuple} }
func (i *Jump) Operands() []Value          { return nil }
func (i *If) Operands() []Value            { return []Value{i.Cond} }
func (i *Return) Operands() []Value        { return i.Results }
func (i *Panic) Operands() []Value         { return []Value{i.X} }
func (i *Defer) Operands() []Value         { return i.Call.Operands() }

// ---------------------------------------------------------------------
// Construction.

// NewFunc returns an empty function.
func NewFunc(name string, sig *types.Signature, pos syntax.Pos) *Func {
	return &Func{Name: name, Sig: sig, Pos: pos}
}

// NewBlock adds a block to f.
func (f *Func) NewBlock(comment string) *Block {
	b := &Block{Index: len(f.Blocks), Comment: comment, Func: f}
	f.Blocks = append(f.Blocks, b)
	return b
}

// Emit appends instruction i to block b. If i defines a value, its type
// is t and it gets a number.
func (b *Block) Emit(i Instr, t types.Type) Instr {
	i.setBlock(b)
	if r, ok := i.(interface{ reg() *register }); ok {
		reg := r.reg()
		reg.id = b.Func.nvalues
		reg.typ = t
		b.Func.nvalues++
	}
	b.Instrs = append(b.Instrs, i)
	return i
}

// AddEdge records a control flow edge from b to c.
func AddEdge(b, c *Block) {
	b.Succs = append(b.Succs, c)
	c.Preds = append(c.Preds, b)
}

// Terminated reports whether b ends in a control instruction.
func (b *Block) Terminated() bool {
	if len(b.Instrs) == 0 {
		return false
	}
	switch b.Instrs[len(b.Instrs)-1].(type) {
	case *Jump, *If, *Return, *Panic:
		return true
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
