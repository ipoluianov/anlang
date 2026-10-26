// Package wasm is the WebAssembly interpreter of the toolchain
// (tool.emu.wasm): it decodes, validates and executes modules of
// WebAssembly 2.0, including the fixed-width SIMD instructions, and reads
// the text format and the scripts of the specification test suite, which
// is its oracle (conf.oracle.use).
//
// The interpreter compiles each function body once, when the module is
// validated, into an array of instructions over a stack of 64-bit slots:
// a value of type v128 takes two slots, every other value one. Branches
// carry their targets and the number of slots they keep.
package wasm

// ValType is a value type, by its binary encoding.
type ValType byte

const (
	I32       ValType = 0x7F
	I64       ValType = 0x7E
	F32       ValType = 0x7D
	F64       ValType = 0x7C
	V128      ValType = 0x7B
	FuncRef   ValType = 0x70
	ExternRef ValType = 0x6F

	unknown ValType = 0 // the type of an operand of unreachable code
)

func (t ValType) String() string {
	switch t {
	case I32:
		return "i32"
	case I64:
		return "i64"
	case F32:
		return "f32"
	case F64:
		return "f64"
	case V128:
		return "v128"
	case FuncRef:
		return "funcref"
	case ExternRef:
		return "externref"
	}
	return "?"
}

// slots returns the number of stack slots of a value of type t.
func (t ValType) slots() int {
	if t == V128 {
		return 2
	}
	return 1
}

func (t ValType) isRef() bool { return t == FuncRef || t == ExternRef }

func validValType(b byte) bool {
	switch ValType(b) {
	case I32, I64, F32, F64, V128, FuncRef, ExternRef:
		return true
	}
	return false
}

// FuncType is a function type.
type FuncType struct {
	Params, Results []ValType
}

func (f FuncType) equal(g FuncType) bool {
	return equalTypes(f.Params, g.Params) && equalTypes(f.Results, g.Results)
}

func equalTypes(a, b []ValType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func slotCount(ts []ValType) int {
	n := 0
	for _, t := range ts {
		n += t.slots()
	}
	return n
}

// Limits are the limits of a memory, in pages, or of a table.
type Limits struct {
	Min    uint32
	Max    uint32
	HasMax bool
}

// TableType is a table type.
type TableType struct {
	Elem   ValType
	Limits Limits
}

// GlobalType is a global type.
type GlobalType struct {
	Type    ValType
	Mutable bool
}

// The kinds of imports and exports.
const (
	KindFunc   = 0
	KindTable  = 1
	KindMemory = 2
	KindGlobal = 3
)

// Import is an import of a module.
type Import struct {
	Module, Name string
	Kind         byte
	Func         uint32 // the type index
	Table        TableType
	Memory       Limits
	Global       GlobalType
}

// Export is an export of a module.
type Export struct {
	Name  string
	Kind  byte
	Index uint32
}

// Func is a function defined by a module.
type Func struct {
	Type   uint32
	Locals []ValType
	Body   []Instr // the instructions, ending with end

	code *compiled
}

// Instr is a decoded instruction with its immediates.
type Instr struct {
	Op int
	// A and B are indices: of a label, a function, a type, a table, a
	// local, a global, a segment or a lane, or the alignment of a memarg;
	// for a block type, A is 0 (no type), 1 (the value type B) or 2 (the
	// type index B).
	A, B uint32
	// C is a constant, the offset of a memarg or the low half of a v128;
	// D is the high half of a v128.
	C, D uint64
	// Ext are the labels of br_table, the default last, or the value
	// types of select.
	Ext []uint32
}

// Global is a global defined by a module.
type Global struct {
	Type GlobalType
	Init []Instr // a constant expression, ending with end
}

// The modes of element and data segments.
const (
	ModeActive = iota
	ModePassive
	ModeDeclarative
)

// Elem is an element segment.
type Elem struct {
	Mode   int
	Table  uint32
	Offset []Instr
	Type   ValType
	Init   [][]Instr // constant expressions
}

// Data is a data segment.
type Data struct {
	Mode   int
	Memory uint32
	Offset []Instr
	Bytes  []byte
}

// Module is a decoded module.
type Module struct {
	Types     []FuncType
	Imports   []Import
	Funcs     []Func
	Tables    []TableType
	Memories  []Limits
	Globals   []Global
	Exports   []Export
	Start     int // the start function, or -1
	Elems     []Elem
	Datas     []Data
	DataCount int // the count of the data count section, or -1

	validated bool
}

// funcType returns the type of function i of the module, in the index
// space where the imported functions come first.
func (m *Module) funcType(i uint32) (FuncType, bool) {
	for _, im := range m.Imports {
		if im.Kind != KindFunc {
			continue
		}
		if i == 0 {
			if int(im.Func) >= len(m.Types) {
				return FuncType{}, false
			}
			return m.Types[im.Func], true
		}
		i--
	}
	if int(i) >= len(m.Funcs) || int(m.Funcs[i].Type) >= len(m.Types) {
		return FuncType{}, false
	}
	return m.Types[m.Funcs[i].Type], true
}

// imported returns the number of imports of kind k.
func (m *Module) imported(k byte) int {
	n := 0
	for _, im := range m.Imports {
		if im.Kind == k {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------
// Instructions.

// imm is the kind of the immediates of an instruction.
type imm int

const (
	immNone      imm = iota
	immBlock         // a block type
	immLabel         // a label index
	immBrTable       // a vector of label indices and a default
	immFunc          // a function index
	immCallInd       // a type index and a table index
	immLocal         // a local index
	immGlobal        // a global index
	immTable         // a table index
	immMem           // a memarg: alignment and offset
	immZero          // a reserved zero byte (a memory index)
	immZero2         // two reserved zero bytes
	immI32           // a signed LEB128 i32
	immI64           // a signed LEB128 i64
	immF32           // 4 bytes
	immF64           // 8 bytes
	immSelectT       // a vector of value types
	immRefType       // a reference type
	immElemTable     // an element index and a table index
	immTable2        // two table indices
	immDataZero      // a data index and a reserved zero byte
	immData          // a data index
	immElem          // an element index
	immMemLane       // a memarg and a lane index
	immLane          // a lane index
	immV128          // 16 bytes
	immShuffle       // 16 lane indices
)

// Opcodes are numbered by their encoding: one byte, 0x100 plus the
// operation of prefix 0xFC, or 0x200 plus that of prefix 0xFD.
const (
	prefixMisc = 0x100
	prefixSIMD = 0x200
)

type opInfo struct {
	name string
	imm  imm
	lane int // the number of lanes of a lane index
}

var ops = map[int]opInfo{}
var opByName = map[string]int{}

func defOps(base int, imm imm, names ...string) {
	for i, n := range names {
		if n == "" {
			continue
		}
		def(base+i, n, imm)
	}
}

func def(code int, name string, imm imm) {
	ops[code] = opInfo{name: name, imm: imm}
	opByName[name] = code
}

// Opcodes used by the validator and the compiler.
const (
	opUnreachable  = 0x00
	opNop          = 0x01
	opBlock        = 0x02
	opLoop         = 0x03
	opIf           = 0x04
	opElse         = 0x05
	opEnd          = 0x0B
	opBr           = 0x0C
	opBrIf         = 0x0D
	opBrTable      = 0x0E
	opReturn       = 0x0F
	opCall         = 0x10
	opCallIndirect = 0x11
	opDrop         = 0x1A
	opSelect       = 0x1B
	opSelectT      = 0x1C
	opLocalGet     = 0x20
	opLocalSet     = 0x21
	opLocalTee     = 0x22
	opGlobalGet    = 0x23
	opGlobalSet    = 0x24
	opTableGet     = 0x25
	opTableSet     = 0x26
	opI32Const     = 0x41
	opI64Const     = 0x42
	opF32Const     = 0x43
	opF64Const     = 0x44
	opRefNull      = 0xD0
	opRefIsNull    = 0xD1
	opRefFunc      = 0xD2
	opV128Const    = prefixSIMD + 0x0C
)

func init() {
	def(0x00, "unreachable", immNone)
	def(0x01, "nop", immNone)
	def(0x02, "block", immBlock)
	def(0x03, "loop", immBlock)
	def(0x04, "if", immBlock)
	def(0x05, "else", immNone)
	def(0x0B, "end", immNone)
	def(0x0C, "br", immLabel)
	def(0x0D, "br_if", immLabel)
	def(0x0E, "br_table", immBrTable)
	def(0x0F, "return", immNone)
	def(0x10, "call", immFunc)
	def(0x11, "call_indirect", immCallInd)
	def(0x1A, "drop", immNone)
	def(0x1C, "select", immSelectT)
	def(0x1B, "select", immNone) // the name is the untyped form
	def(0x20, "local.get", immLocal)
	def(0x21, "local.set", immLocal)
	def(0x22, "local.tee", immLocal)
	def(0x23, "global.get", immGlobal)
	def(0x24, "global.set", immGlobal)
	def(0x25, "table.get", immTable)
	def(0x26, "table.set", immTable)
	defOps(0x28, immMem, "i32.load", "i64.load", "f32.load", "f64.load",
		"i32.load8_s", "i32.load8_u", "i32.load16_s", "i32.load16_u",
		"i64.load8_s", "i64.load8_u", "i64.load16_s", "i64.load16_u", "i64.load32_s", "i64.load32_u",
		"i32.store", "i64.store", "f32.store", "f64.store",
		"i32.store8", "i32.store16", "i64.store8", "i64.store16", "i64.store32")
	def(0x3F, "memory.size", immZero)
	def(0x40, "memory.grow", immZero)
	def(0x41, "i32.const", immI32)
	def(0x42, "i64.const", immI64)
	def(0x43, "f32.const", immF32)
	def(0x44, "f64.const", immF64)
	defOps(0x45, immNone,
		"i32.eqz", "i32.eq", "i32.ne", "i32.lt_s", "i32.lt_u", "i32.gt_s", "i32.gt_u", "i32.le_s", "i32.le_u", "i32.ge_s", "i32.ge_u",
		"i64.eqz", "i64.eq", "i64.ne", "i64.lt_s", "i64.lt_u", "i64.gt_s", "i64.gt_u", "i64.le_s", "i64.le_u", "i64.ge_s", "i64.ge_u",
		"f32.eq", "f32.ne", "f32.lt", "f32.gt", "f32.le", "f32.ge",
		"f64.eq", "f64.ne", "f64.lt", "f64.gt", "f64.le", "f64.ge",
		"i32.clz", "i32.ctz", "i32.popcnt", "i32.add", "i32.sub", "i32.mul", "i32.div_s", "i32.div_u", "i32.rem_s", "i32.rem_u",
		"i32.and", "i32.or", "i32.xor", "i32.shl", "i32.shr_s", "i32.shr_u", "i32.rotl", "i32.rotr",
		"i64.clz", "i64.ctz", "i64.popcnt", "i64.add", "i64.sub", "i64.mul", "i64.div_s", "i64.div_u", "i64.rem_s", "i64.rem_u",
		"i64.and", "i64.or", "i64.xor", "i64.shl", "i64.shr_s", "i64.shr_u", "i64.rotl", "i64.rotr",
		"f32.abs", "f32.neg", "f32.ceil", "f32.floor", "f32.trunc", "f32.nearest", "f32.sqrt",
		"f32.add", "f32.sub", "f32.mul", "f32.div", "f32.min", "f32.max", "f32.copysign",
		"f64.abs", "f64.neg", "f64.ceil", "f64.floor", "f64.trunc", "f64.nearest", "f64.sqrt",
		"f64.add", "f64.sub", "f64.mul", "f64.div", "f64.min", "f64.max", "f64.copysign",
		"i32.wrap_i64", "i32.trunc_f32_s", "i32.trunc_f32_u", "i32.trunc_f64_s", "i32.trunc_f64_u",
		"i64.extend_i32_s", "i64.extend_i32_u", "i64.trunc_f32_s", "i64.trunc_f32_u", "i64.trunc_f64_s", "i64.trunc_f64_u",
		"f32.convert_i32_s", "f32.convert_i32_u", "f32.convert_i64_s", "f32.convert_i64_u", "f32.demote_f64",
		"f64.convert_i32_s", "f64.convert_i32_u", "f64.convert_i64_s", "f64.convert_i64_u", "f64.promote_f32",
		"i32.reinterpret_f32", "i64.reinterpret_f64", "f32.reinterpret_i32", "f64.reinterpret_i64",
		"i32.extend8_s", "i32.extend16_s", "i64.extend8_s", "i64.extend16_s", "i64.extend32_s")
	def(0xD0, "ref.null", immRefType)
	def(0xD1, "ref.is_null", immNone)
	def(0xD2, "ref.func", immFunc)

	defOps(prefixMisc, immNone,
		"i32.trunc_sat_f32_s", "i32.trunc_sat_f32_u", "i32.trunc_sat_f64_s", "i32.trunc_sat_f64_u",
		"i64.trunc_sat_f32_s", "i64.trunc_sat_f32_u", "i64.trunc_sat_f64_s", "i64.trunc_sat_f64_u")
	def(prefixMisc+0x08, "memory.init", immDataZero)
	def(prefixMisc+0x09, "data.drop", immData)
	def(prefixMisc+0x0A, "memory.copy", immZero2)
	def(prefixMisc+0x0B, "memory.fill", immZero)
	def(prefixMisc+0x0C, "table.init", immElemTable)
	def(prefixMisc+0x0D, "elem.drop", immElem)
	def(prefixMisc+0x0E, "table.copy", immTable2)
	def(prefixMisc+0x0F, "table.grow", immTable)
	def(prefixMisc+0x10, "table.size", immTable)
	def(prefixMisc+0x11, "table.fill", immTable)

	s := prefixSIMD
	defOps(s, immMem, "v128.load", "v128.load8x8_s", "v128.load8x8_u", "v128.load16x4_s", "v128.load16x4_u",
		"v128.load32x2_s", "v128.load32x2_u", "v128.load8_splat", "v128.load16_splat", "v128.load32_splat",
		"v128.load64_splat", "v128.store")
	def(s+0x0C, "v128.const", immV128)
	def(s+0x0D, "i8x16.shuffle", immShuffle)
	defOps(s+0x0E, immNone, "i8x16.swizzle", "i8x16.splat", "i16x8.splat", "i32x4.splat", "i64x2.splat",
		"f32x4.splat", "f64x2.splat")
	lanes := []struct {
		name string
		n    int
	}{
		{"i8x16.extract_lane_s", 16}, {"i8x16.extract_lane_u", 16}, {"i8x16.replace_lane", 16},
		{"i16x8.extract_lane_s", 8}, {"i16x8.extract_lane_u", 8}, {"i16x8.replace_lane", 8},
		{"i32x4.extract_lane", 4}, {"i32x4.replace_lane", 4},
		{"i64x2.extract_lane", 2}, {"i64x2.replace_lane", 2},
		{"f32x4.extract_lane", 4}, {"f32x4.replace_lane", 4},
		{"f64x2.extract_lane", 2}, {"f64x2.replace_lane", 2},
	}
	for i, l := range lanes {
		ops[s+0x15+i] = opInfo{name: l.name, imm: immLane, lane: l.n}
		opByName[l.name] = s + 0x15 + i
	}
	defOps(s+0x23, immNone,
		"i8x16.eq", "i8x16.ne", "i8x16.lt_s", "i8x16.lt_u", "i8x16.gt_s", "i8x16.gt_u", "i8x16.le_s", "i8x16.le_u", "i8x16.ge_s", "i8x16.ge_u",
		"i16x8.eq", "i16x8.ne", "i16x8.lt_s", "i16x8.lt_u", "i16x8.gt_s", "i16x8.gt_u", "i16x8.le_s", "i16x8.le_u", "i16x8.ge_s", "i16x8.ge_u",
		"i32x4.eq", "i32x4.ne", "i32x4.lt_s", "i32x4.lt_u", "i32x4.gt_s", "i32x4.gt_u", "i32x4.le_s", "i32x4.le_u", "i32x4.ge_s", "i32x4.ge_u",
		"f32x4.eq", "f32x4.ne", "f32x4.lt", "f32x4.gt", "f32x4.le", "f32x4.ge",
		"f64x2.eq", "f64x2.ne", "f64x2.lt", "f64x2.gt", "f64x2.le", "f64x2.ge",
		"v128.not", "v128.and", "v128.andnot", "v128.or", "v128.xor", "v128.bitselect", "v128.any_true")
	memLanes := []struct {
		name string
		n    int
	}{
		{"v128.load8_lane", 16}, {"v128.load16_lane", 8}, {"v128.load32_lane", 4}, {"v128.load64_lane", 2},
		{"v128.store8_lane", 16}, {"v128.store16_lane", 8}, {"v128.store32_lane", 4}, {"v128.store64_lane", 2},
	}
	for i, l := range memLanes {
		ops[s+0x54+i] = opInfo{name: l.name, imm: immMemLane, lane: l.n}
		opByName[l.name] = s + 0x54 + i
	}
	def(s+0x5C, "v128.load32_zero", immMem)
	def(s+0x5D, "v128.load64_zero", immMem)
	defOps(s+0x5E, immNone,
		"f32x4.demote_f64x2_zero", "f64x2.promote_low_f32x4",
		"i8x16.abs", "i8x16.neg", "i8x16.popcnt", "i8x16.all_true", "i8x16.bitmask",
		"i8x16.narrow_i16x8_s", "i8x16.narrow_i16x8_u",
		"f32x4.ceil", "f32x4.floor", "f32x4.trunc", "f32x4.nearest",
		"i8x16.shl", "i8x16.shr_s", "i8x16.shr_u",
		"i8x16.add", "i8x16.add_sat_s", "i8x16.add_sat_u", "i8x16.sub", "i8x16.sub_sat_s", "i8x16.sub_sat_u",
		"f64x2.ceil", "f64x2.floor",
		"i8x16.min_s", "i8x16.min_u", "i8x16.max_s", "i8x16.max_u",
		"f64x2.trunc", "i8x16.avgr_u",
		"i16x8.extadd_pairwise_i8x16_s", "i16x8.extadd_pairwise_i8x16_u",
		"i32x4.extadd_pairwise_i16x8_s", "i32x4.extadd_pairwise_i16x8_u",
		// 0x80
		"i16x8.abs", "i16x8.neg", "i16x8.q15mulr_sat_s", "i16x8.all_true", "i16x8.bitmask",
		"i16x8.narrow_i32x4_s", "i16x8.narrow_i32x4_u",
		"i16x8.extend_low_i8x16_s", "i16x8.extend_high_i8x16_s", "i16x8.extend_low_i8x16_u", "i16x8.extend_high_i8x16_u",
		"i16x8.shl", "i16x8.shr_s", "i16x8.shr_u",
		"i16x8.add", "i16x8.add_sat_s", "i16x8.add_sat_u", "i16x8.sub", "i16x8.sub_sat_s", "i16x8.sub_sat_u",
		"f64x2.nearest", "i16x8.mul",
		"i16x8.min_s", "i16x8.min_u", "i16x8.max_s", "i16x8.max_u", "",
		"i16x8.avgr_u",
		"i16x8.extmul_low_i8x16_s", "i16x8.extmul_high_i8x16_s", "i16x8.extmul_low_i8x16_u", "i16x8.extmul_high_i8x16_u",
		// 0xA0
		"i32x4.abs", "i32x4.neg", "", "i32x4.all_true", "i32x4.bitmask", "", "",
		"i32x4.extend_low_i16x8_s", "i32x4.extend_high_i16x8_s", "i32x4.extend_low_i16x8_u", "i32x4.extend_high_i16x8_u",
		"i32x4.shl", "i32x4.shr_s", "i32x4.shr_u", "i32x4.add", "", "", "i32x4.sub", "", "", "",
		"i32x4.mul", "i32x4.min_s", "i32x4.min_u", "i32x4.max_s", "i32x4.max_u", "i32x4.dot_i16x8_s", "",
		"i32x4.extmul_low_i16x8_s", "i32x4.extmul_high_i16x8_s", "i32x4.extmul_low_i16x8_u", "i32x4.extmul_high_i16x8_u",
		// 0xC0
		"i64x2.abs", "i64x2.neg", "", "i64x2.all_true", "i64x2.bitmask", "", "",
		"i64x2.extend_low_i32x4_s", "i64x2.extend_high_i32x4_s", "i64x2.extend_low_i32x4_u", "i64x2.extend_high_i32x4_u",
		"i64x2.shl", "i64x2.shr_s", "i64x2.shr_u", "i64x2.add", "", "", "i64x2.sub", "", "", "",
		"i64x2.mul", "i64x2.eq", "i64x2.ne", "i64x2.lt_s", "i64x2.gt_s", "i64x2.le_s", "i64x2.ge_s",
		"i64x2.extmul_low_i32x4_s", "i64x2.extmul_high_i32x4_s", "i64x2.extmul_low_i32x4_u", "i64x2.extmul_high_i32x4_u",
		// 0xE0
		"f32x4.abs", "f32x4.neg", "", "f32x4.sqrt", "f32x4.add", "f32x4.sub", "f32x4.mul", "f32x4.div",
		"f32x4.min", "f32x4.max", "f32x4.pmin", "f32x4.pmax",
		"f64x2.abs", "f64x2.neg", "", "f64x2.sqrt", "f64x2.add", "f64x2.sub", "f64x2.mul", "f64x2.div",
		"f64x2.min", "f64x2.max", "f64x2.pmin", "f64x2.pmax",
		"i32x4.trunc_sat_f32x4_s", "i32x4.trunc_sat_f32x4_u", "f32x4.convert_i32x4_s", "f32x4.convert_i32x4_u",
		"i32x4.trunc_sat_f64x2_s_zero", "i32x4.trunc_sat_f64x2_u_zero", "f64x2.convert_low_i32x4_s", "f64x2.convert_low_i32x4_u")
	if ops[s+0xFF].name != "f64x2.convert_low_i32x4_u" || ops[s+0x80].name != "i16x8.abs" || ops[s+0xA0].name != "i32x4.abs" || ops[s+0xC0].name != "i64x2.abs" || ops[s+0xE0].name != "f32x4.abs" {
		panic("wasm: the SIMD opcode table is misnumbered")
	}
}
