package wasm

import (
	"errors"
	"fmt"
	"strings"
)

// InvalidError reports a module that decodes but is not valid.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return "invalid: " + e.Msg }

func invalid(format string, args ...any) error {
	return &InvalidError{Msg: fmt.Sprintf(format, args...)}
}

// IsInvalid reports whether err reports an invalid module.
func IsInvalid(err error) bool {
	var v *InvalidError
	return errors.As(err, &v)
}

// context is the validation context of a module.
type context struct {
	m       *Module
	types   []FuncType
	funcs   []FuncType
	tables  []TableType
	mems    []Limits
	globals []GlobalType
	elems   []ValType
	datas   int
	refs    map[uint32]bool
	nimport int // the number of imported globals
}

// Validate validates the module and compiles its functions.
func (m *Module) Validate() error {
	if m.validated {
		return nil
	}
	c := &context{m: m, types: m.Types, refs: map[uint32]bool{}}
	for _, im := range m.Imports {
		switch im.Kind {
		case KindFunc:
			if int(im.Func) >= len(m.Types) {
				return invalid("unknown type %d", im.Func)
			}
			c.funcs = append(c.funcs, m.Types[im.Func])
		case KindTable:
			if err := checkLimits(im.Table.Limits, 1<<32-1, "table size"); err != nil {
				return err
			}
			c.tables = append(c.tables, im.Table)
		case KindMemory:
			if err := checkMemory(im.Memory); err != nil {
				return err
			}
			c.mems = append(c.mems, im.Memory)
		case KindGlobal:
			c.globals = append(c.globals, im.Global)
			c.nimport++
		}
	}
	for _, f := range m.Funcs {
		if int(f.Type) >= len(m.Types) {
			return invalid("unknown type %d", f.Type)
		}
		c.funcs = append(c.funcs, m.Types[f.Type])
	}
	for _, t := range m.Tables {
		if err := checkLimits(t.Limits, 1<<32-1, "table size"); err != nil {
			return err
		}
		c.tables = append(c.tables, t)
	}
	for _, l := range m.Memories {
		if err := checkMemory(l); err != nil {
			return err
		}
		c.mems = append(c.mems, l)
	}
	if len(c.mems) > 1 {
		return invalid("multiple memories")
	}
	for _, e := range m.Elems {
		c.elems = append(c.elems, e.Type)
	}
	c.datas = len(m.Datas)
	// The functions that may be referenced by ref.func in function
	// bodies: those referenced outside them.
	addRefs := func(code []Instr) {
		for _, in := range code {
			if in.Op == opRefFunc {
				c.refs[in.A] = true
			}
		}
	}
	for _, g := range m.Globals {
		addRefs(g.Init)
	}
	for _, e := range m.Elems {
		for _, x := range e.Init {
			addRefs(x)
		}
	}
	for _, e := range m.Exports {
		if e.Kind == KindFunc {
			c.refs[e.Index] = true
		}
	}
	// Globals: their initializers see the imported globals only.
	for _, g := range m.Globals {
		if err := c.constExpr(g.Init, g.Type.Type, c.nimport); err != nil {
			return err
		}
		c.globals = append(c.globals, g.Type)
	}
	for _, e := range m.Elems {
		for _, x := range e.Init {
			if err := c.constExpr(x, e.Type, c.nimport); err != nil {
				return err
			}
		}
		if e.Mode == ModeActive {
			if int(e.Table) >= len(c.tables) {
				return invalid("unknown table %d", e.Table)
			}
			if c.tables[e.Table].Elem != e.Type {
				return invalid("type mismatch")
			}
			if err := c.constExpr(e.Offset, I32, c.nimport); err != nil {
				return err
			}
		}
	}
	for _, d := range m.Datas {
		if d.Mode == ModeActive {
			if int(d.Memory) >= len(c.mems) {
				return invalid("unknown memory %d", d.Memory)
			}
			if err := c.constExpr(d.Offset, I32, c.nimport); err != nil {
				return err
			}
		}
	}
	if m.Start >= 0 {
		if m.Start >= len(c.funcs) {
			return invalid("unknown function %d", m.Start)
		}
		if t := c.funcs[m.Start]; len(t.Params) != 0 || len(t.Results) != 0 {
			return invalid("start function")
		}
	}
	names := map[string]bool{}
	for _, e := range m.Exports {
		if names[e.Name] {
			return invalid("duplicate export name")
		}
		names[e.Name] = true
		var n int
		switch e.Kind {
		case KindFunc:
			n = len(c.funcs)
		case KindTable:
			n = len(c.tables)
		case KindMemory:
			n = len(c.mems)
		case KindGlobal:
			n = len(c.globals)
		}
		if int(e.Index) >= n {
			return invalid("unknown %s %d", kindName(e.Kind), e.Index)
		}
	}
	for i := range m.Funcs {
		f := &m.Funcs[i]
		code, err := c.function(f)
		if err != nil {
			return err
		}
		f.code = code
	}
	m.validated = true
	return nil
}

func kindName(k byte) string {
	return [...]string{"function", "table", "memory", "global"}[k]
}

func checkLimits(l Limits, max uint64, what string) error {
	if l.HasMax && l.Min > l.Max {
		return invalid("size minimum must not be greater than maximum")
	}
	if uint64(l.Min) > max || l.HasMax && uint64(l.Max) > max {
		return invalid("%s must be at most %d", what, max)
	}
	return nil
}

func checkMemory(l Limits) error {
	if uint64(l.Min) > 65536 || l.HasMax && uint64(l.Max) > 65536 {
		return invalid("memory size must be at most 65536 pages (4GiB)")
	}
	return checkLimits(l, 65536, "memory size")
}

// constExpr validates a constant expression of type t, in which global.get
// may refer to the first nglobals globals.
func (c *context) constExpr(code []Instr, t ValType, nglobals int) error {
	var stack []ValType
	for _, in := range code {
		switch in.Op {
		case opI32Const:
			stack = append(stack, I32)
		case opI64Const:
			stack = append(stack, I64)
		case opF32Const:
			stack = append(stack, F32)
		case opF64Const:
			stack = append(stack, F64)
		case opV128Const:
			stack = append(stack, V128)
		case opRefNull:
			stack = append(stack, ValType(in.A))
		case opRefFunc:
			if int(in.A) >= len(c.funcs) {
				return invalid("unknown function %d", in.A)
			}
			stack = append(stack, FuncRef)
		case opGlobalGet:
			if int(in.A) >= nglobals {
				if int(in.A) < len(c.globals) {
					// WebAssembly 3.0 allows this.
					return invalid("unknown global %d: a constant expression may refer only to imported globals", in.A)
				}
				return invalid("unknown global %d", in.A)
			}
			g := c.globals[in.A]
			if g.Mutable {
				return invalid("constant expression required")
			}
			stack = append(stack, g.Type)
		case opEnd:
		default:
			return invalid("constant expression required")
		}
	}
	if len(stack) != 1 || stack[0] != t {
		return invalid("type mismatch")
	}
	return nil
}

// ---------------------------------------------------------------------
// Function bodies.

// Internal operations of compiled code, after the opcodes.
const (
	iJump       = 0x400 + iota // a: the target
	iJumpIfZero                // a: the target
	iBr                        // a: the target; b: the slots kept; c: the height
	iBrIf                      // as iBr
	iBrTable                   // a: the index in brTables
	iReturn                    // the end of the function
	iLocalGet2                 // the v128 forms: a is the slot
	iLocalSet2
	iLocalTee2
	iGlobalGet2
	iGlobalSet2
	iDrop    // a: the slots
	iSelect2 // select of v128 values
)

// cinstr is a compiled instruction.
type cinstr struct {
	op   uint16
	a, b uint32
	c, d uint64
}

type brTarget struct {
	pc, keep, height uint32
}

// compiled is the compiled code of a function.
type compiled struct {
	code     []cinstr
	brTables [][]brTarget
	params   int // the slots of the parameters
	locals   int // the slots of the parameters and the locals
	results  int // the slots of the results
	maxStack int // the most slots of the operand stack
}

type ctrlFrame struct {
	op          int
	params      []ValType
	results     []ValType
	height      int // the operand types below the frame
	slots       int // the slots of those operands
	unreachable bool
	live        bool // code is emitted for the frame
	start       int  // the pc of a loop
	patches     []int
	elseJump    int // the jump of an if to its else branch, or -1
	brPatches   [][2]int
}

type funcValidator struct {
	c      *context
	f      *Func
	locals []ValType
	slotOf []int // the slot of each local
	vals   []ValType
	slots  int
	ctrls  []*ctrlFrame
	out    *compiled
	err    error
}

func (v *funcValidator) fail(format string, args ...any) {
	if v.err == nil {
		v.err = invalid(format, args...)
	}
}

func (v *funcValidator) top() *ctrlFrame { return v.ctrls[len(v.ctrls)-1] }

func (v *funcValidator) emitting() bool {
	f := v.top()
	return f.live && !f.unreachable
}

func (v *funcValidator) emit(in cinstr) int {
	if !v.emitting() {
		return -1
	}
	v.out.code = append(v.out.code, in)
	return len(v.out.code) - 1
}

func (v *funcValidator) push(t ValType) {
	v.vals = append(v.vals, t)
	v.slots += slotsOf(t)
	if v.slots > v.out.maxStack {
		v.out.maxStack = v.slots
	}
}

func slotsOf(t ValType) int {
	if t == V128 {
		return 2
	}
	return 1
}

func (v *funcValidator) pushAll(ts []ValType) {
	for _, t := range ts {
		v.push(t)
	}
}

func (v *funcValidator) pop() ValType {
	f := v.top()
	if len(v.vals) == f.height {
		if f.unreachable {
			return unknown
		}
		v.fail("type mismatch")
		return unknown
	}
	t := v.vals[len(v.vals)-1]
	v.vals = v.vals[:len(v.vals)-1]
	v.slots -= slotsOf(t)
	return t
}

func (v *funcValidator) popExpect(want ValType) ValType {
	t := v.pop()
	if t != want && t != unknown && want != unknown {
		v.fail("type mismatch")
	}
	if t == unknown {
		return want
	}
	return t
}

func (v *funcValidator) popAll(ts []ValType) {
	for i := len(ts) - 1; i >= 0; i-- {
		v.popExpect(ts[i])
	}
}

func (v *funcValidator) pushCtrl(op int, params, results []ValType) *ctrlFrame {
	live := len(v.ctrls) == 0 || v.emitting()
	f := &ctrlFrame{op: op, params: params, results: results, height: len(v.vals), slots: v.slots, live: live, elseJump: -1}
	v.ctrls = append(v.ctrls, f)
	v.pushAll(params)
	return f
}

func (v *funcValidator) popCtrl() *ctrlFrame {
	if len(v.ctrls) == 0 {
		v.fail("unexpected end")
		return &ctrlFrame{}
	}
	f := v.top()
	v.popAll(f.results)
	if len(v.vals) != f.height {
		v.fail("type mismatch")
	}
	v.ctrls = v.ctrls[:len(v.ctrls)-1]
	return f
}

func (f *ctrlFrame) labelTypes() []ValType {
	if f.op == opLoop {
		return f.params
	}
	return f.results
}

func (v *funcValidator) setUnreachable() {
	f := v.top()
	for len(v.vals) > f.height {
		v.pop()
	}
	v.slots = f.slots
	f.unreachable = true
}

// label returns the frame of label l.
func (v *funcValidator) label(l uint32) *ctrlFrame {
	if int(l) >= len(v.ctrls) {
		v.fail("unknown label %d", l)
		return nil
	}
	return v.ctrls[len(v.ctrls)-1-int(l)]
}

// branch emits a branch of operation op to the label of frame f.
func (v *funcValidator) branch(op uint16, f *ctrlFrame) {
	keep := slotCount(f.labelTypes())
	in := cinstr{op: op, b: uint32(keep), c: uint64(v.out.locals + f.slots)}
	if f.op == opLoop {
		in.a = uint32(f.start)
		v.emit(in)
		return
	}
	if i := v.emit(in); i >= 0 {
		f.patches = append(f.patches, i)
	}
}

func (v *funcValidator) blockType(in Instr) (params, results []ValType) {
	switch in.A {
	case 1:
		return nil, []ValType{ValType(in.B)}
	case 2:
		if int(in.B) >= len(v.c.types) {
			v.fail("unknown type %d", in.B)
			return nil, nil
		}
		t := v.c.types[in.B]
		return t.Params, t.Results
	}
	return nil, nil
}

// function validates and compiles the body of a function.
func (c *context) function(f *Func) (*compiled, error) {
	t := c.types[f.Type]
	v := &funcValidator{c: c, f: f, out: &compiled{}}
	v.locals = append(append([]ValType(nil), t.Params...), f.Locals...)
	n := 0
	for _, l := range v.locals {
		v.slotOf = append(v.slotOf, n)
		n += slotsOf(l)
	}
	v.out.params = slotCount(t.Params)
	v.out.locals = n
	v.out.results = slotCount(t.Results)
	v.pushCtrl(-1, nil, t.Results)
	for i, in := range f.Body {
		if v.err != nil {
			break
		}
		if len(v.ctrls) == 0 {
			v.fail("operators remaining after end of function")
			break
		}
		v.instr(in)
		if v.err != nil {
			v.err = invalid("%s, at instruction %d (%s)", v.err.(*InvalidError).Msg, i, ops[in.Op].name)
			break
		}
		if len(v.ctrls) == 0 && i != len(f.Body)-1 {
			v.fail("operators remaining after end of function")
		}
	}
	if v.err == nil && len(v.ctrls) != 0 {
		v.fail("unexpected end")
	}
	return v.out, v.err
}

func (v *funcValidator) localType(i uint32) ValType {
	if int(i) >= len(v.locals) {
		v.fail("unknown local %d", i)
		return unknown
	}
	return v.locals[i]
}

func (v *funcValidator) globalType(i uint32) GlobalType {
	if int(i) >= len(v.c.globals) {
		v.fail("unknown global %d", i)
		return GlobalType{Type: unknown}
	}
	return v.c.globals[i]
}

func (v *funcValidator) table(i uint32) TableType {
	if int(i) >= len(v.c.tables) {
		v.fail("unknown table %d", i)
		return TableType{Elem: unknown}
	}
	return v.c.tables[i]
}

func (v *funcValidator) memory() {
	if len(v.c.mems) == 0 {
		v.fail("unknown memory 0")
	}
}

func (v *funcValidator) instr(in Instr) {
	op := in.Op
	switch op {
	case opUnreachable:
		v.emit(cinstr{op: opUnreachable})
		v.setUnreachable()
	case opNop:
	case opBlock, opLoop:
		params, results := v.blockType(in)
		v.popAll(params)
		f := v.pushCtrl(op, params, results)
		f.start = len(v.out.code)
	case opIf:
		params, results := v.blockType(in)
		v.popExpect(I32)
		v.popAll(params)
		live := v.emitting()
		f := v.pushCtrl(op, params, results)
		if live {
			v.out.code = append(v.out.code, cinstr{op: iJumpIfZero})
			f.elseJump = len(v.out.code) - 1
		}
	case opElse:
		f := v.top()
		if f.op != opIf {
			v.fail("else without if")
			return
		}
		v.popAll(f.results)
		if len(v.vals) != f.height {
			v.fail("type mismatch")
		}
		if i := v.emit(cinstr{op: iJump}); i >= 0 {
			f.patches = append(f.patches, i)
		}
		if f.elseJump >= 0 {
			v.out.code[f.elseJump].a = uint32(len(v.out.code))
			f.elseJump = -1
		}
		f.op = opElse
		f.unreachable = false
		v.slots = f.slots
		v.vals = v.vals[:f.height]
		v.pushAll(f.params)
	case opEnd:
		f := v.top()
		if f.op == opIf && !equalTypes(f.params, f.results) {
			v.fail("type mismatch")
		}
		v.popCtrl()
		end := uint32(len(v.out.code))
		if f.op == -1 {
			v.out.code = append(v.out.code, cinstr{op: iReturn})
		}
		for _, p := range f.patches {
			v.out.code[p].a = end
		}
		for _, p := range f.brPatches {
			v.out.brTables[p[0]][p[1]].pc = end
		}
		if f.elseJump >= 0 {
			v.out.code[f.elseJump].a = end
		}
		v.slots = f.slots
		v.vals = v.vals[:f.height]
		v.pushAll(f.results)
	case opBr:
		if f := v.label(in.A); f != nil {
			v.popAll(f.labelTypes())
			v.branch(iBr, f)
		}
		v.setUnreachable()
	case opBrIf:
		v.popExpect(I32)
		if f := v.label(in.A); f != nil {
			ts := f.labelTypes()
			v.popAll(ts)
			v.branch(iBrIf, f)
			v.pushAll(ts)
		}
	case opBrTable:
		v.popExpect(I32)
		def := v.label(in.Ext[len(in.Ext)-1])
		if def == nil {
			return
		}
		arity := len(def.labelTypes())
		idx := len(v.out.brTables)
		var targets []brTarget
		for k, l := range in.Ext {
			f := v.label(l)
			if f == nil {
				return
			}
			ts := f.labelTypes()
			if len(ts) != arity {
				v.fail("type mismatch")
				return
			}
			// Each label's types must accept the operands.
			saved, savedSlots := append([]ValType(nil), v.vals...), v.slots
			v.popAll(ts)
			v.vals, v.slots = saved, savedSlots
			t := brTarget{keep: uint32(slotCount(ts)), height: uint32(v.out.locals + f.slots)}
			if f.op == opLoop {
				t.pc = uint32(f.start)
			} else if v.emitting() {
				f.brPatches = append(f.brPatches, [2]int{idx, k})
			}
			targets = append(targets, t)
		}
		if v.emitting() {
			v.out.brTables = append(v.out.brTables, targets)
			v.emit(cinstr{op: iBrTable, a: uint32(idx)})
		}
		v.popAll(def.labelTypes())
		v.setUnreachable()
	case opReturn:
		f := v.ctrls[0]
		v.popAll(f.results)
		v.branch(iBr, f)
		v.setUnreachable()
	case opCall:
		if int(in.A) >= len(v.c.funcs) {
			v.fail("unknown function %d", in.A)
			return
		}
		t := v.c.funcs[in.A]
		v.popAll(t.Params)
		v.emit(cinstr{op: opCall, a: in.A})
		v.pushAll(t.Results)
	case opCallIndirect:
		tt := v.table(in.B)
		if tt.Elem != FuncRef && tt.Elem != unknown {
			v.fail("type mismatch")
		}
		if int(in.A) >= len(v.c.types) {
			v.fail("unknown type %d", in.A)
			return
		}
		t := v.c.types[in.A]
		v.popExpect(I32)
		v.popAll(t.Params)
		v.emit(cinstr{op: opCallIndirect, a: in.A, b: in.B})
		v.pushAll(t.Results)
	case opDrop:
		t := v.pop()
		v.emit(cinstr{op: iDrop, a: uint32(slotsOf(t))})
	case opSelect, opSelectT:
		var t ValType
		if op == opSelectT {
			if len(in.Ext) != 1 {
				v.fail("invalid result arity")
				return
			}
			t = ValType(in.Ext[0])
			v.popExpect(I32)
			v.popExpect(t)
			v.popExpect(t)
		} else {
			v.popExpect(I32)
			t1 := v.pop()
			t2 := v.pop()
			if t1.isRef() || t2.isRef() {
				v.fail("type mismatch")
			}
			if t1 != t2 && t1 != unknown && t2 != unknown {
				v.fail("type mismatch")
			}
			t = t1
			if t == unknown {
				t = t2
			}
		}
		if t == V128 {
			v.emit(cinstr{op: iSelect2})
		} else {
			v.emit(cinstr{op: opSelect})
		}
		v.push(t)
	case opLocalGet, opLocalSet, opLocalTee:
		t := v.localType(in.A)
		if t == unknown {
			return
		}
		slot := uint32(v.slotOf[in.A])
		code := uint16(op)
		if t == V128 {
			code = uint16(iLocalGet2 + op - opLocalGet)
		}
		switch op {
		case opLocalGet:
			v.emit(cinstr{op: code, a: slot})
			v.push(t)
		case opLocalSet:
			v.popExpect(t)
			v.emit(cinstr{op: code, a: slot})
		case opLocalTee:
			v.popExpect(t)
			v.emit(cinstr{op: code, a: slot})
			v.push(t)
		}
	case opGlobalGet:
		g := v.globalType(in.A)
		code := uint16(op)
		if g.Type == V128 {
			code = iGlobalGet2
		}
		v.emit(cinstr{op: code, a: in.A})
		v.push(g.Type)
	case opGlobalSet:
		g := v.globalType(in.A)
		if !g.Mutable && g.Type != unknown {
			v.fail("global is immutable")
		}
		v.popExpect(g.Type)
		code := uint16(op)
		if g.Type == V128 {
			code = iGlobalSet2
		}
		v.emit(cinstr{op: code, a: in.A})
	case opTableGet:
		t := v.table(in.A)
		v.popExpect(I32)
		v.emit(cinstr{op: uint16(op), a: in.A})
		v.push(t.Elem)
	case opTableSet:
		t := v.table(in.A)
		v.popExpect(t.Elem)
		v.popExpect(I32)
		v.emit(cinstr{op: uint16(op), a: in.A})
	case opRefNull:
		v.emit(cinstr{op: uint16(op)})
		v.push(ValType(in.A))
	case opRefIsNull:
		t := v.pop()
		if t != unknown && !t.isRef() {
			v.fail("type mismatch")
		}
		v.emit(cinstr{op: uint16(op)})
		v.push(I32)
	case opRefFunc:
		if int(in.A) >= len(v.c.funcs) {
			v.fail("unknown function %d", in.A)
			return
		}
		if !v.c.refs[in.A] {
			v.fail("undeclared function reference")
			return
		}
		v.emit(cinstr{op: uint16(op), a: in.A})
		v.push(FuncRef)
	case opI32Const, opI64Const, opF32Const, opF64Const:
		v.emit(cinstr{op: uint16(op), c: in.C})
		v.push([...]ValType{I32, I64, F32, F64}[op-opI32Const])
	case opV128Const:
		v.emit(cinstr{op: uint16(op), c: in.C, d: in.D})
		v.push(V128)
	default:
		v.other(in)
	}
}

// other validates the instructions with a fixed signature, memory and
// table instructions and the vector instructions.
func (v *funcValidator) other(in Instr) {
	op := in.Op
	info := ops[op]
	name := info.name
	ci := cinstr{op: uint16(op), a: in.A, b: in.B, c: in.C, d: in.D}
	if info.imm == immMem || info.imm == immMemLane {
		v.memory()
		size := accessSize(name)
		if 1<<in.A > size {
			v.fail("alignment must not be larger than natural")
		}
		if info.imm == immMemLane && int(in.B) >= info.lane {
			v.fail("invalid lane index")
		}
		ci.a = in.B // the lane
	}
	if info.imm == immLane && int(in.A) >= info.lane {
		v.fail("invalid lane index")
	}
	if info.imm == immShuffle {
		for k := 0; k < 16; k++ {
			b := in.C >> (8 * uint(k))
			if k >= 8 {
				b = in.D >> (8 * uint(k-8))
			}
			if b&0xFF >= 32 {
				v.fail("invalid lane index")
			}
		}
	}
	sig, ok := signature(op)
	switch {
	case ok:
	case op == 0x3F: // memory.size
		v.memory()
		sig = fsig{nil, []ValType{I32}}
	case op == 0x40: // memory.grow
		v.memory()
		sig = fsig{[]ValType{I32}, []ValType{I32}}
	case op == prefixMisc+0x08: // memory.init
		v.memory()
		v.dataIndex(in.A)
		sig = fsig{[]ValType{I32, I32, I32}, nil}
	case op == prefixMisc+0x09: // data.drop
		v.dataIndex(in.A)
	case op == prefixMisc+0x0A: // memory.copy
		v.memory()
		sig = fsig{[]ValType{I32, I32, I32}, nil}
	case op == prefixMisc+0x0B: // memory.fill
		v.memory()
		sig = fsig{[]ValType{I32, I32, I32}, nil}
	case op == prefixMisc+0x0C: // table.init
		t := v.table(in.B)
		if int(in.A) >= len(v.c.elems) {
			v.fail("unknown elem segment %d", in.A)
		} else if t.Elem != v.c.elems[in.A] && t.Elem != unknown {
			v.fail("type mismatch")
		}
		sig = fsig{[]ValType{I32, I32, I32}, nil}
	case op == prefixMisc+0x0D: // elem.drop
		if int(in.A) >= len(v.c.elems) {
			v.fail("unknown elem segment %d", in.A)
		}
	case op == prefixMisc+0x0E: // table.copy
		d, s := v.table(in.A), v.table(in.B)
		if d.Elem != s.Elem && d.Elem != unknown && s.Elem != unknown {
			v.fail("type mismatch")
		}
		sig = fsig{[]ValType{I32, I32, I32}, nil}
	case op == prefixMisc+0x0F: // table.grow
		t := v.table(in.A)
		sig = fsig{[]ValType{t.Elem, I32}, []ValType{I32}}
	case op == prefixMisc+0x10: // table.size
		v.table(in.A)
		sig = fsig{nil, []ValType{I32}}
	case op == prefixMisc+0x11: // table.fill
		t := v.table(in.A)
		sig = fsig{[]ValType{I32, t.Elem, I32}, nil}
	default:
		v.fail("unknown operator %s", name)
		return
	}
	if v.err != nil {
		return
	}
	v.popAll(sig.params)
	v.emit(ci)
	v.pushAll(sig.results)
}

func (v *funcValidator) dataIndex(i uint32) {
	if int(i) >= v.c.datas {
		v.fail("unknown data segment %d", i)
	}
}

// accessSize returns the bytes accessed by a memory instruction.
func accessSize(name string) int {
	_, op, _ := strings.Cut(name, ".")
	switch {
	case strings.HasPrefix(op, "load8x8"), strings.HasPrefix(op, "load16x4"), strings.HasPrefix(op, "load32x2"):
		return 8
	case strings.Contains(op, "8"):
		return 1
	case strings.Contains(op, "16"):
		return 2
	case strings.Contains(op, "32"):
		return 4
	case strings.Contains(op, "64"):
		return 8
	case strings.HasPrefix(name, "v128"):
		return 16
	case strings.HasPrefix(name, "i32"), strings.HasPrefix(name, "f32"):
		return 4
	}
	return 8
}

type fsig struct {
	params, results []ValType
}

var sigs = map[int]fsig{}

func typeOfName(s string) ValType {
	switch s {
	case "i32":
		return I32
	case "i64":
		return I64
	case "f32":
		return F32
	case "f64":
		return F64
	}
	return V128
}

// signature returns the signature of an instruction whose operands and
// results have fixed types.
func signature(op int) (fsig, bool) {
	s, ok := sigs[op]
	return s, ok
}

func init() {
	v := V128
	for op, info := range ops {
		name := info.name
		ty, rest, _ := strings.Cut(name, ".")
		t := typeOfName(ty)
		un := fsig{[]ValType{t}, []ValType{t}}
		bin := fsig{[]ValType{t, t}, []ValType{t}}
		cmp := fsig{[]ValType{t, t}, []ValType{I32}}
		switch {
		case op >= 0x28 && op <= 0x35:
			sigs[op] = fsig{[]ValType{I32}, []ValType{t}}
		case op >= 0x36 && op <= 0x3E:
			sigs[op] = fsig{[]ValType{I32, t}, nil}
		case op >= 0x45 && op <= 0xC4 || op >= prefixMisc && op <= prefixMisc+7:
			switch {
			case rest == "eqz":
				sigs[op] = fsig{[]ValType{t}, []ValType{I32}}
			case op >= 0x46 && op <= 0x66:
				sigs[op] = cmp
			case rest == "clz" || rest == "ctz" || rest == "popcnt" || strings.HasPrefix(rest, "extend") && !strings.Contains(rest, "_i") ||
				(t == F32 || t == F64) && (rest == "abs" || rest == "neg" || rest == "ceil" || rest == "floor" || rest == "trunc" || rest == "nearest" || rest == "sqrt"):
				sigs[op] = un
			case strings.Contains(rest, "_i3") || strings.Contains(rest, "_i6") || strings.Contains(rest, "_f3") || strings.Contains(rest, "_f6"):
				// A conversion: the source type follows the operation.
				parts := strings.Split(rest, "_")
				from := parts[len(parts)-1]
				if from == "s" || from == "u" {
					from = parts[len(parts)-2]
				}
				sigs[op] = fsig{[]ValType{typeOfName(from)}, []ValType{t}}
			default:
				sigs[op] = bin
			}
		case op >= prefixSIMD:
			switch {
			case info.imm == immMem && strings.Contains(rest, "store"):
				sigs[op] = fsig{[]ValType{I32, v}, nil}
			case info.imm == immMem:
				sigs[op] = fsig{[]ValType{I32}, []ValType{v}}
			case info.imm == immMemLane && strings.Contains(rest, "store"):
				sigs[op] = fsig{[]ValType{I32, v}, nil}
			case info.imm == immMemLane:
				sigs[op] = fsig{[]ValType{I32, v}, []ValType{v}}
			case info.imm == immV128:
			case rest == "splat":
				sigs[op] = fsig{[]ValType{lane(ty)}, []ValType{v}}
			case strings.HasPrefix(rest, "extract_lane"):
				sigs[op] = fsig{[]ValType{v}, []ValType{lane(ty)}}
			case rest == "replace_lane":
				sigs[op] = fsig{[]ValType{v, lane(ty)}, []ValType{v}}
			case rest == "shl" || rest == "shr_s" || rest == "shr_u":
				sigs[op] = fsig{[]ValType{v, I32}, []ValType{v}}
			case rest == "bitselect":
				sigs[op] = fsig{[]ValType{v, v, v}, []ValType{v}}
			case rest == "any_true" || rest == "all_true" || rest == "bitmask":
				sigs[op] = fsig{[]ValType{v}, []ValType{I32}}
			case simdUnary(rest):
				sigs[op] = fsig{[]ValType{v}, []ValType{v}}
			default:
				sigs[op] = fsig{[]ValType{v, v}, []ValType{v}}
			}
		}
	}
}

// lane returns the type of a lane of a vector shape such as i8x16.
func lane(shape string) ValType {
	switch shape[:3] {
	case "i64":
		return I64
	case "f32":
		return F32
	case "f64":
		return F64
	}
	return I32
}

func simdUnary(op string) bool {
	switch op {
	case "not", "abs", "neg", "popcnt", "ceil", "floor", "trunc", "nearest", "sqrt":
		return true
	}
	for _, p := range []string{"extend_", "extadd_pairwise", "convert", "trunc_sat", "demote", "promote"} {
		if strings.HasPrefix(op, p) {
			return true
		}
	}
	return false
}
