// Package desc reads architecture descriptions (spec/13-targets.md
// §13.5): the constants of type arch.Arch of package an/arch. It parses
// and checks their encodings and their formal semantics; the assembler,
// the disassembler, the instruction packages and the interpreters of the
// architectures are derived from the result.
package desc

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"an/build"
	"an/ct"
	"an/types"
)

// Arch is an architecture description: the value of a constant of type
// arch.Arch, with the same fields.
type Arch struct {
	Name         string
	Word         int
	Endian       int // 0 little, 1 big
	Unit         int
	AddressBits  int
	Pointers     Pointers
	Address      string
	PC           string
	PCRead       int
	Fetch        string
	Registers    []Register
	Operands     []Operand
	Fragments    []Fragment
	Prefixes     []Prefix
	Instructions []Instruction
	Semantics    string
	ABI          ABI
	Rules        []Rule

	// Derived by Check.
	regs      map[string]*Register
	classes   map[string][]*Register
	operands  map[string]*Operand
	fragments map[string]*Fragment
	funcs     map[string]*FuncDecl
	address   Expr
	fetch     Expr
	pc        *Register
	byFirst   [256][]*Instruction

	// The compiled semantics, made by compile.
	compileOnce sync.Once
	cfuncs      map[string]*cfunc
	cfetch      cexpr
	caddress    cexpr
	instrPC     bool // compiling instruction semantics, where PC reads as PCRead

	// The machine without memory that evaluates Syntax.
	syntaxMu sync.Mutex
	syntaxM  *Machine
}

// ABI is the machine model of the code generator (target.arch.abi).
type ABI struct {
	Frame       string
	FrameBottom bool
	Params      int
	Align       int
	Reserved    []string
	Flags       string
	// InterruptFrame is what the processor and enter.handler push for an
	// interrupt handler, for the stack analysis.
	InterruptFrame int
	// Structured describes structured control flow: the mnemonics that
	// open a block and the one that ends it.
	Structured struct {
		Open []string
		End  string
	}
}

// Rule is an instruction selection rule (target.arch.rule).
type Rule struct {
	Op    string
	Width int
	From  int
	Code  string
}

type Pointers struct {
	Flat, Near, Far, Program int
}

type Register struct {
	Name   string
	Width  int
	Class  string
	Parent string
	Offset int
	Fixed  int
	Zero   int
	Number int // the number within the class

	index  int       // the index in Registers
	parent *Register // the register of Parent
}

type Operand struct {
	Name      string
	Register  string
	Class     string
	Registers int
	Memory    int
	Fragment  string
	Imm       int
	Signed    bool
	Relative  bool
	Value     int
	Constant  bool
	Width     int
	Scale     int
	Transform string
	Syntax    string
	List      bool

	transform cexpr // compiled Transform
	tparsed   Expr
	sparsed   Expr  // parsed Syntax
	syntaxW   int   // the width of Syntax
	csyntax   cexpr // compiled Syntax, made by compile
	// syntaxInv maps the numbers of Syntax to their first fields.
	syntaxInv map[uint64]uint64
}

// bits returns the operand bits of the field value v: transformed,
// sign-extended and scaled.
func (o *Operand) bits(a *Arch, v uint64) uint64 {
	if o.transform != nil {
		v = o.transform(&ctx{vars: []val{{v, o.Imm}}}).v & mask(o.Imm)
	}
	if o.Signed {
		v = signExtend(v, o.Imm)
	}
	return v << uint(o.Scale)
}

// field returns the field value of operand bits v and whether they fit.
func (o *Operand) field(a *Arch, v int64) (uint64, bool) {
	if v&(1<<uint(o.Scale)-1) != 0 {
		return 0, false
	}
	v >>= uint(o.Scale)
	if o.Signed {
		if o.Imm < 64 && (v < -(1<<(o.Imm-1)) || v >= 1<<(o.Imm-1)) {
			return 0, false
		}
	} else if v < 0 || uint64(v) > mask(o.Imm) {
		return 0, false
	}
	f := uint64(v) & mask(o.Imm)
	if o.transform != nil {
		f = o.transform(&ctx{vars: []val{{f, o.Imm}}}).v & mask(o.Imm)
	}
	return f, true
}

// ValueWidth returns the width of the operand in the semantics.
func (o *Operand) ValueWidth(a *Arch) int {
	switch {
	case o.List:
		return len(a.classes[o.Class])
	case o.Width != 0:
		return o.Width
	case o.Register != "":
		return a.regs[o.Register].Width
	case o.Class != "":
		return a.classes[o.Class][0].Width
	case o.Memory > 0:
		return o.Memory
	case o.Memory < 0:
		return 0 // an address only
	case o.Imm != 0:
		return o.Imm
	}
	return 0 // a constant of the assembler syntax
}

type Fragment struct {
	Name         string
	Alternatives []Alternative
}

type Alternative struct {
	Encoding string
	Register string
	Address  string
	Segment  string
	Cycles   int

	pattern []Item
	address Expr
	segment Expr

	fields   []string // the names of the fields of pattern, in order
	caddress cexpr
	csegment cexpr
}

type Prefix struct {
	Name      string
	Encoding  string
	Semantics string

	pattern []Item
	body    []Stmt
	cbody   *compiled
}

type Instruction struct {
	Mnemonic  string
	Operands  []string
	Encoding  string
	Semantics string
	Cycles    int
	Effects   []string

	pattern []Item
	body    []Stmt
	ops     []*Operand
	cbody   *compiled
}

// String writes the instruction form as in assembler syntax.
func (in *Instruction) String() string {
	if len(in.Operands) == 0 {
		return in.Mnemonic
	}
	return in.Mnemonic + " " + strings.Join(in.Operands, ", ")
}

// Load evaluates the architecture description named by the constant
// name of package an/arch, such as "I8086", and checks it.
func Load(name string) (*Arch, []error) {
	infos, errs := build.Library("an/arch", "linux-x86_64")
	if len(errs) > 0 {
		return nil, errs
	}
	info := infos[len(infos)-1]
	k, ok := info.Scope.Lookup(name).(*types.Const)
	if !ok || !k.Computed {
		return nil, []error{fmt.Errorf("an/arch has no architecture description %s", name)}
	}
	a := &Arch{}
	if err := ct.Decode(k.Value, k.Type(), reflect.ValueOf(a).Elem()); err != nil {
		return nil, []error{fmt.Errorf("arch.%s: %v", name, err)}
	}
	if errs := a.Check(); len(errs) > 0 {
		return nil, errs
	}
	return a, nil
}
