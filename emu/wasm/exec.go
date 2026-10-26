package wasm

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
)

// Trap is a trap of execution.
type Trap struct{ Msg string }

func (t *Trap) Error() string { return "trap: " + t.Msg }

func trap(msg string) { panic(&Trap{Msg: msg}) }

// LinkError reports imports that do not match the module.
type LinkError struct{ Msg string }

func (e *LinkError) Error() string { return "link: " + e.Msg }

// IsLink reports whether err is a link error.
func IsLink(err error) bool {
	var l *LinkError
	return errors.As(err, &l)
}

// IsTrap reports whether err is a trap.
func IsTrap(err error) bool {
	var t *Trap
	return errors.As(err, &t)
}

const pageSize = 65536

// Limits of the implementation.
const (
	maxFrames = 50000
	maxSlots  = 1 << 24
)

// Value is a value of a value type: a number in Lo, the halves of a
// vector, or a reference: 0 for null, otherwise the reference of a
// function of the store or an external value plus 1.
type Value struct {
	Type   ValType
	Lo, Hi uint64
}

// Store holds the instances that may share functions, tables, memories
// and globals.
type Store struct {
	funcs []*Function
	m     *machine
	// Fuel, if not 0, limits the branches and calls that the functions
	// may execute; when it runs out, execution traps.
	Fuel int64
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{} }

// Function is a function instance: a function of a module instance or a
// host function.
type Function struct {
	Type FuncType
	inst *Instance
	code *compiled
	// Host implements a host function: it receives the parameters and
	// returns the results as stack slots.
	Host func(args []uint64) ([]uint64, error)
	ref  uint64
}

// Memory is a memory instance.
type Memory struct {
	Data   []byte
	Max    uint32
	HasMax bool
}

// Pages returns the size of the memory in pages.
func (m *Memory) Pages() uint32 { return uint32(len(m.Data) / pageSize) }

// Grow grows the memory by n pages and returns the old size, or -1.
func (m *Memory) Grow(n uint32) int32 {
	old := m.Pages()
	max := uint64(65536)
	if m.HasMax {
		max = uint64(m.Max)
	}
	if uint64(old)+uint64(n) > max {
		return -1
	}
	m.Data = append(m.Data, make([]byte, int(n)*pageSize)...)
	return int32(old)
}

// Table is a table instance.
type Table struct {
	Elem   ValType
	Elems  []uint64
	Max    uint32
	HasMax bool
}

// Global is a global instance.
type GlobalInst struct {
	Type   GlobalType
	Lo, Hi uint64
}

// Extern is an external value: an export or an import.
type Extern struct {
	Kind   byte
	Func   *Function
	Table  *Table
	Memory *Memory
	Global *GlobalInst
}

// Instance is a module instance.
type Instance struct {
	store   *Store
	module  *Module
	types   []FuncType
	funcs   []*Function
	tables  []*Table
	mems    []*Memory
	globals []*GlobalInst
	elems   [][]uint64
	datas   [][]byte
	Exports map[string]Extern
}

// Memory returns memory 0 of the instance, or nil.
func (inst *Instance) Memory() *Memory {
	if len(inst.mems) == 0 {
		return nil
	}
	return inst.mems[0]
}

// HostFunc returns a host function of type t.
func (s *Store) HostFunc(t FuncType, f func(args []uint64) ([]uint64, error)) *Function {
	fn := &Function{Type: t, Host: f}
	s.addFunc(fn)
	return fn
}

func (s *Store) addFunc(f *Function) {
	s.funcs = append(s.funcs, f)
	f.ref = uint64(len(s.funcs))
}

// Func returns the function of a reference, or nil.
func (s *Store) Func(ref uint64) *Function {
	if ref == 0 || ref > uint64(len(s.funcs)) {
		return nil
	}
	return s.funcs[ref-1]
}

// Ref returns the reference of a function.
func (f *Function) Ref() uint64 { return f.ref }

// Instantiate instantiates a validated module; imports resolves its
// imports. An error is a *LinkError, or a *Trap of the initialization
// of the segments or of the start function, after which the instance is
// partly initialized.
func (s *Store) Instantiate(m *Module, imports func(module, name string) (Extern, bool)) (*Instance, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	inst := &Instance{store: s, module: m, types: m.Types, Exports: map[string]Extern{}}
	for _, im := range m.Imports {
		ext, ok := Extern{}, false
		if imports != nil {
			ext, ok = imports(im.Module, im.Name)
		}
		if !ok {
			return nil, &LinkError{Msg: fmt.Sprintf("unknown import %q %q", im.Module, im.Name)}
		}
		if ext.Kind != im.Kind {
			return nil, &LinkError{Msg: fmt.Sprintf("incompatible import type for %q %q", im.Module, im.Name)}
		}
		bad := false
		switch im.Kind {
		case KindFunc:
			bad = !ext.Func.Type.equal(m.Types[im.Func])
			inst.funcs = append(inst.funcs, ext.Func)
		case KindTable:
			t := ext.Table
			bad = t.Elem != im.Table.Elem || !limitsMatch(uint64(len(t.Elems)), t.Max, t.HasMax, im.Table.Limits)
			inst.tables = append(inst.tables, t)
		case KindMemory:
			mem := ext.Memory
			bad = !limitsMatch(uint64(mem.Pages()), mem.Max, mem.HasMax, im.Memory)
			inst.mems = append(inst.mems, mem)
		case KindGlobal:
			bad = ext.Global.Type != im.Global
			inst.globals = append(inst.globals, ext.Global)
		}
		if bad {
			return nil, &LinkError{Msg: fmt.Sprintf("incompatible import type for %q %q", im.Module, im.Name)}
		}
	}
	for i := range m.Funcs {
		f := &Function{Type: m.Types[m.Funcs[i].Type], inst: inst, code: m.Funcs[i].code}
		s.addFunc(f)
		inst.funcs = append(inst.funcs, f)
	}
	for _, t := range m.Tables {
		inst.tables = append(inst.tables, &Table{Elem: t.Elem, Elems: make([]uint64, t.Limits.Min), Max: t.Limits.Max, HasMax: t.Limits.HasMax})
	}
	for _, l := range m.Memories {
		inst.mems = append(inst.mems, &Memory{Data: make([]byte, int(l.Min)*pageSize), Max: l.Max, HasMax: l.HasMax})
	}
	for _, g := range m.Globals {
		lo, hi := inst.constExpr(g.Init)
		inst.globals = append(inst.globals, &GlobalInst{Type: g.Type, Lo: lo, Hi: hi})
	}
	for _, e := range m.Exports {
		ext := Extern{Kind: e.Kind}
		switch e.Kind {
		case KindFunc:
			ext.Func = inst.funcs[e.Index]
		case KindTable:
			ext.Table = inst.tables[e.Index]
		case KindMemory:
			ext.Memory = inst.mems[e.Index]
		case KindGlobal:
			ext.Global = inst.globals[e.Index]
		}
		inst.Exports[e.Name] = ext
	}
	for _, e := range m.Elems {
		var refs []uint64
		for _, x := range e.Init {
			v, _ := inst.constExpr(x)
			refs = append(refs, v)
		}
		inst.elems = append(inst.elems, refs)
	}
	for _, d := range m.Datas {
		inst.datas = append(inst.datas, d.Bytes)
	}
	err := s.protect(func() {
		for i, e := range m.Elems {
			switch e.Mode {
			case ModeActive:
				off, _ := inst.constExpr(e.Offset)
				inst.tableInit(inst.tables[e.Table], inst.elems[i], uint32(off), 0, uint32(len(inst.elems[i])))
				inst.elems[i] = nil
			case ModeDeclarative:
				inst.elems[i] = nil
			}
		}
		for i, d := range m.Datas {
			if d.Mode == ModeActive {
				off, _ := inst.constExpr(d.Offset)
				inst.memoryInit(inst.mems[d.Memory], inst.datas[i], uint32(off), 0, uint32(len(inst.datas[i])))
				inst.datas[i] = nil
			}
		}
	})
	if err != nil {
		return inst, err
	}
	if m.Start >= 0 {
		if _, err := s.Call(inst.funcs[m.Start], nil); err != nil {
			return inst, err
		}
	}
	return inst, nil
}

func limitsMatch(size uint64, max uint32, hasMax bool, want Limits) bool {
	if size < uint64(want.Min) {
		return false
	}
	if want.HasMax {
		return hasMax && max <= want.Max
	}
	return true
}

// constExpr evaluates a constant expression.
func (inst *Instance) constExpr(code []Instr) (lo, hi uint64) {
	for _, in := range code {
		switch in.Op {
		case opI32Const, opI64Const, opF32Const, opF64Const:
			lo = in.C
		case opV128Const:
			lo, hi = in.C, in.D
		case opRefNull:
			lo = 0
		case opRefFunc:
			lo = inst.funcs[in.A].ref
		case opGlobalGet:
			g := inst.globals[in.A]
			lo, hi = g.Lo, g.Hi
		}
	}
	return lo, hi
}

func (inst *Instance) tableInit(t *Table, elems []uint64, d, s, n uint32) {
	if uint64(s)+uint64(n) > uint64(len(elems)) || uint64(d)+uint64(n) > uint64(len(t.Elems)) {
		trap("out of bounds table access")
	}
	copy(t.Elems[d:d+n], elems[s:s+n])
}

func (inst *Instance) memoryInit(mem *Memory, data []byte, d, s, n uint32) {
	if uint64(s)+uint64(n) > uint64(len(data)) || uint64(d)+uint64(n) > uint64(len(mem.Data)) {
		trap("out of bounds memory access")
	}
	copy(mem.Data[d:d+n], data[s:s+n])
}

// hostError is an error of a host function that is not a trap, such as
// the end of the program; it ends the execution.
type hostError struct{ err error }

// protect runs f and returns the trap it raises, or the error of a host
// function.
func (s *Store) protect(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			switch r := r.(type) {
			case *Trap:
				err = r
			case hostError:
				err = r.err
			default:
				panic(r)
			}
		}
	}()
	f()
	return nil
}

// Call calls function f with the arguments as stack slots and returns
// the results as stack slots.
func (s *Store) Call(f *Function, args []uint64) (results []uint64, err error) {
	m := s.m
	if m == nil || m.busy {
		m = &machine{store: s, stack: make([]uint64, 1<<16)}
		if s.m == nil {
			s.m = m
		}
	}
	m.busy = true
	defer func() { m.busy = false }()
	err = s.protect(func() {
		sp := m.sp
		base := sp
		m.ensure(sp + len(args) + 16)
		copy(m.stack[sp:], args)
		sp += len(args)
		sp = m.invoke(f, sp)
		results = append([]uint64(nil), m.stack[base:sp]...)
	})
	m.sp, m.frames = 0, m.frames[:0]
	return results, err
}

// Invoke calls f with values and returns its results as values.
func (s *Store) Invoke(f *Function, args []Value) ([]Value, error) {
	if len(args) != len(f.Type.Params) {
		return nil, fmt.Errorf("wrong number of arguments")
	}
	var slots []uint64
	for _, a := range args {
		slots = append(slots, a.Lo)
		if a.Type == V128 {
			slots = append(slots, a.Hi)
		}
	}
	res, err := s.Call(f, slots)
	if err != nil {
		return nil, err
	}
	var out []Value
	for _, t := range f.Type.Results {
		v := Value{Type: t, Lo: res[0]}
		res = res[1:]
		if t == V128 {
			v.Hi = res[0]
			res = res[1:]
		}
		out = append(out, v)
	}
	return out, nil
}

type frame struct {
	code *compiled
	pc   int
	fp   int
	inst *Instance
}

type machine struct {
	store  *Store
	stack  []uint64
	sp     int
	frames []frame
	busy   bool
}

// ensure makes the stack hold at least n slots.
func (m *machine) ensure(n int) {
	if n <= len(m.stack) {
		return
	}
	if n > maxSlots {
		trap("call stack exhausted")
	}
	size := len(m.stack)
	for size < n {
		size *= 2
	}
	s := make([]uint64, size)
	copy(s, m.stack)
	m.stack = s
}

// invoke calls f with its arguments on the stack below sp and returns the
// stack pointer above its results.
func (m *machine) invoke(f *Function, sp int) int {
	if f.Host != nil {
		return m.callHost(f, sp)
	}
	depth := len(m.frames)
	m.frames = append(m.frames, frame{code: nil}) // the frame to return to
	sp = m.enter(f, sp)
	return m.run(depth, sp)
}

func (m *machine) callHost(f *Function, sp int) int {
	n := slotCount(f.Type.Params)
	args := append([]uint64(nil), m.stack[sp-n:sp]...)
	res, err := f.Host(args)
	if err != nil {
		var t *Trap
		if errors.As(err, &t) {
			panic(t)
		}
		panic(hostError{err})
	}
	sp -= n
	m.ensure(sp + len(res))
	copy(m.stack[sp:], res)
	return sp + len(res)
}

// burn uses one unit of the fuel of the store.
func (m *machine) burn() {
	if s := m.store; s.Fuel != 0 {
		s.Fuel--
		if s.Fuel == 0 {
			trap("out of fuel")
		}
	}
}

// enter makes the frame of wasm function f, whose arguments are below sp,
// the current frame and returns the stack pointer of its operand stack.
func (m *machine) enter(f *Function, sp int) int {
	m.burn()
	c := f.code
	if len(m.frames) >= maxFrames {
		trap("call stack exhausted")
	}
	fp := sp - c.params
	m.ensure(fp + c.locals + c.maxStack + 8)
	clear(m.stack[sp : fp+c.locals])
	m.frames = append(m.frames, frame{code: c, fp: fp, inst: f.inst})
	return fp + c.locals
}

func b2i(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// run executes the frames above depth and returns the stack pointer
// after the results of the first of them.
func (m *machine) run(depth, sp int) int {
	fr := &m.frames[len(m.frames)-1]
	code, pc, fp, inst := fr.code.code, 0, fr.fp, fr.inst
	cc := fr.code
	s := m.stack
	var mem *Memory
	if len(inst.mems) > 0 {
		mem = inst.mems[0]
	}
	reload := func() {
		fr := &m.frames[len(m.frames)-1]
		cc = fr.code
		code, pc, fp, inst = cc.code, fr.pc, fr.fp, fr.inst
		s = m.stack
		mem = nil
		if len(inst.mems) > 0 {
			mem = inst.mems[0]
		}
	}
	addr := func(a uint64, off uint64, n uint64) uint64 {
		ea := uint64(uint32(a)) + off
		if mem == nil || ea+n > uint64(len(mem.Data)) {
			trap("out of bounds memory access")
		}
		return ea
	}
	for {
		in := &code[pc]
		pc++
		switch in.op {
		case opUnreachable:
			trap("unreachable")
		case iJump:
			pc = int(in.a)
			m.burn()
		case iJumpIfZero:
			sp--
			if uint32(s[sp]) == 0 {
				pc = int(in.a)
			}
		case iBr:
			keep, h := int(in.b), fp+int(in.c)
			if sp-keep != h {
				copy(s[h:h+keep], s[sp-keep:sp])
			}
			sp = h + keep
			pc = int(in.a)
			m.burn()
		case iBrIf:
			sp--
			if uint32(s[sp]) != 0 {
				keep, h := int(in.b), fp+int(in.c)
				if sp-keep != h {
					copy(s[h:h+keep], s[sp-keep:sp])
				}
				sp = h + keep
				pc = int(in.a)
				m.burn()
			}
		case iBrTable:
			sp--
			i := uint32(s[sp])
			ts := cc.brTables[in.a]
			if int(i) >= len(ts)-1 {
				i = uint32(len(ts) - 1)
			}
			t := ts[i]
			keep, h := int(t.keep), fp+int(t.height)
			if sp-keep != h {
				copy(s[h:h+keep], s[sp-keep:sp])
			}
			sp = h + keep
			pc = int(t.pc)
			m.burn()
		case iReturn:
			n := cc.results
			copy(s[fp:fp+n], s[sp-n:sp])
			sp = fp + n
			m.frames = m.frames[:len(m.frames)-1]
			if len(m.frames) == depth+1 {
				m.frames = m.frames[:depth]
				return sp
			}
			reload()
		case opCall:
			f := inst.funcs[in.a]
			m.frames[len(m.frames)-1].pc = pc
			if f.Host != nil {
				sp = m.callHost(f, sp)
				s = m.stack
				continue
			}
			sp = m.enter(f, sp)
			reload()
		case opCallIndirect:
			t := inst.tables[in.b]
			sp--
			i := uint32(s[sp])
			if int(i) >= len(t.Elems) {
				trap("undefined element")
			}
			f := m.store.Func(t.Elems[i])
			if f == nil {
				trap("uninitialized element")
			}
			if !f.Type.equal(inst.types[in.a]) {
				trap("indirect call type mismatch")
			}
			m.frames[len(m.frames)-1].pc = pc
			if f.Host != nil {
				sp = m.callHost(f, sp)
				s = m.stack
				continue
			}
			sp = m.enter(f, sp)
			reload()
		case iDrop:
			sp -= int(in.a)
		case opSelect:
			sp -= 2
			if uint32(s[sp+1]) == 0 {
				s[sp-1] = s[sp]
			}
		case iSelect2:
			sp -= 3
			if uint32(s[sp+2]) == 0 {
				s[sp-2], s[sp-1] = s[sp], s[sp+1]
			}
		case opLocalGet:
			s[sp] = s[fp+int(in.a)]
			sp++
		case opLocalSet:
			sp--
			s[fp+int(in.a)] = s[sp]
		case opLocalTee:
			s[fp+int(in.a)] = s[sp-1]
		case iLocalGet2:
			s[sp], s[sp+1] = s[fp+int(in.a)], s[fp+int(in.a)+1]
			sp += 2
		case iLocalSet2:
			sp -= 2
			s[fp+int(in.a)], s[fp+int(in.a)+1] = s[sp], s[sp+1]
		case iLocalTee2:
			s[fp+int(in.a)], s[fp+int(in.a)+1] = s[sp-2], s[sp-1]
		case opGlobalGet:
			s[sp] = inst.globals[in.a].Lo
			sp++
		case opGlobalSet:
			sp--
			inst.globals[in.a].Lo = s[sp]
		case iGlobalGet2:
			g := inst.globals[in.a]
			s[sp], s[sp+1] = g.Lo, g.Hi
			sp += 2
		case iGlobalSet2:
			sp -= 2
			g := inst.globals[in.a]
			g.Lo, g.Hi = s[sp], s[sp+1]
		case opTableGet:
			t := inst.tables[in.a]
			i := uint32(s[sp-1])
			if int(i) >= len(t.Elems) {
				trap("out of bounds table access")
			}
			s[sp-1] = t.Elems[i]
		case opTableSet:
			t := inst.tables[in.a]
			sp -= 2
			i := uint32(s[sp])
			if int(i) >= len(t.Elems) {
				trap("out of bounds table access")
			}
			t.Elems[i] = s[sp+1]
		case opRefNull:
			s[sp] = 0
			sp++
		case opRefIsNull:
			s[sp-1] = b2i(s[sp-1] == 0)
		case opRefFunc:
			s[sp] = inst.funcs[in.a].ref
			sp++
		case opI32Const, opI64Const, opF32Const, opF64Const:
			s[sp] = in.c
			sp++
		case opV128Const:
			s[sp], s[sp+1] = in.c, in.d
			sp += 2

		// Memory.
		case 0x28: // i32.load
			ea := addr(s[sp-1], in.c, 4)
			s[sp-1] = uint64(le32(mem.Data[ea:]))
		case 0x29: // i64.load
			ea := addr(s[sp-1], in.c, 8)
			s[sp-1] = le64(mem.Data[ea:])
		case 0x2A:
			ea := addr(s[sp-1], in.c, 4)
			s[sp-1] = uint64(le32(mem.Data[ea:]))
		case 0x2B:
			ea := addr(s[sp-1], in.c, 8)
			s[sp-1] = le64(mem.Data[ea:])
		case 0x2C: // i32.load8_s
			ea := addr(s[sp-1], in.c, 1)
			s[sp-1] = uint64(uint32(int32(int8(mem.Data[ea]))))
		case 0x2D:
			ea := addr(s[sp-1], in.c, 1)
			s[sp-1] = uint64(mem.Data[ea])
		case 0x2E:
			ea := addr(s[sp-1], in.c, 2)
			s[sp-1] = uint64(uint32(int32(int16(le16(mem.Data[ea:])))))
		case 0x2F:
			ea := addr(s[sp-1], in.c, 2)
			s[sp-1] = uint64(le16(mem.Data[ea:]))
		case 0x30: // i64.load8_s
			ea := addr(s[sp-1], in.c, 1)
			s[sp-1] = uint64(int64(int8(mem.Data[ea])))
		case 0x31:
			ea := addr(s[sp-1], in.c, 1)
			s[sp-1] = uint64(mem.Data[ea])
		case 0x32:
			ea := addr(s[sp-1], in.c, 2)
			s[sp-1] = uint64(int64(int16(le16(mem.Data[ea:]))))
		case 0x33:
			ea := addr(s[sp-1], in.c, 2)
			s[sp-1] = uint64(le16(mem.Data[ea:]))
		case 0x34:
			ea := addr(s[sp-1], in.c, 4)
			s[sp-1] = uint64(int64(int32(le32(mem.Data[ea:]))))
		case 0x35:
			ea := addr(s[sp-1], in.c, 4)
			s[sp-1] = uint64(le32(mem.Data[ea:]))
		case 0x36, 0x38, 0x3E: // i32.store, f32.store, i64.store32
			sp -= 2
			ea := addr(s[sp], in.c, 4)
			put32(mem.Data[ea:], uint32(s[sp+1]))
		case 0x37, 0x39: // i64.store, f64.store
			sp -= 2
			ea := addr(s[sp], in.c, 8)
			put64(mem.Data[ea:], s[sp+1])
		case 0x3A, 0x3C: // store8
			sp -= 2
			ea := addr(s[sp], in.c, 1)
			mem.Data[ea] = byte(s[sp+1])
		case 0x3B, 0x3D: // store16
			sp -= 2
			ea := addr(s[sp], in.c, 2)
			put16(mem.Data[ea:], uint16(s[sp+1]))
		case 0x3F: // memory.size
			s[sp] = uint64(mem.Pages())
			sp++
		case 0x40: // memory.grow
			s[sp-1] = uint64(uint32(mem.Grow(uint32(s[sp-1]))))

		// Integer comparisons and arithmetic.
		case 0x45:
			s[sp-1] = b2i(uint32(s[sp-1]) == 0)
		case 0x46:
			sp--
			s[sp-1] = b2i(uint32(s[sp-1]) == uint32(s[sp]))
		case 0x47:
			sp--
			s[sp-1] = b2i(uint32(s[sp-1]) != uint32(s[sp]))
		case 0x48:
			sp--
			s[sp-1] = b2i(int32(s[sp-1]) < int32(s[sp]))
		case 0x49:
			sp--
			s[sp-1] = b2i(uint32(s[sp-1]) < uint32(s[sp]))
		case 0x4A:
			sp--
			s[sp-1] = b2i(int32(s[sp-1]) > int32(s[sp]))
		case 0x4B:
			sp--
			s[sp-1] = b2i(uint32(s[sp-1]) > uint32(s[sp]))
		case 0x4C:
			sp--
			s[sp-1] = b2i(int32(s[sp-1]) <= int32(s[sp]))
		case 0x4D:
			sp--
			s[sp-1] = b2i(uint32(s[sp-1]) <= uint32(s[sp]))
		case 0x4E:
			sp--
			s[sp-1] = b2i(int32(s[sp-1]) >= int32(s[sp]))
		case 0x4F:
			sp--
			s[sp-1] = b2i(uint32(s[sp-1]) >= uint32(s[sp]))
		case 0x50:
			s[sp-1] = b2i(s[sp-1] == 0)
		case 0x51:
			sp--
			s[sp-1] = b2i(s[sp-1] == s[sp])
		case 0x52:
			sp--
			s[sp-1] = b2i(s[sp-1] != s[sp])
		case 0x53:
			sp--
			s[sp-1] = b2i(int64(s[sp-1]) < int64(s[sp]))
		case 0x54:
			sp--
			s[sp-1] = b2i(s[sp-1] < s[sp])
		case 0x55:
			sp--
			s[sp-1] = b2i(int64(s[sp-1]) > int64(s[sp]))
		case 0x56:
			sp--
			s[sp-1] = b2i(s[sp-1] > s[sp])
		case 0x57:
			sp--
			s[sp-1] = b2i(int64(s[sp-1]) <= int64(s[sp]))
		case 0x58:
			sp--
			s[sp-1] = b2i(s[sp-1] <= s[sp])
		case 0x59:
			sp--
			s[sp-1] = b2i(int64(s[sp-1]) >= int64(s[sp]))
		case 0x5A:
			sp--
			s[sp-1] = b2i(s[sp-1] >= s[sp])
		case 0x5B, 0x5C, 0x5D, 0x5E, 0x5F, 0x60:
			sp--
			a, b := f32(s[sp-1]), f32(s[sp])
			s[sp-1] = b2i(fcmp(in.op-0x5B, float64(a), float64(b)))
		case 0x61, 0x62, 0x63, 0x64, 0x65, 0x66:
			sp--
			s[sp-1] = b2i(fcmp(in.op-0x61, f64(s[sp-1]), f64(s[sp])))
		case 0x67:
			s[sp-1] = uint64(bits.LeadingZeros32(uint32(s[sp-1])))
		case 0x68:
			s[sp-1] = uint64(bits.TrailingZeros32(uint32(s[sp-1])))
		case 0x69:
			s[sp-1] = uint64(bits.OnesCount32(uint32(s[sp-1])))
		case 0x6A:
			sp--
			s[sp-1] = uint64(uint32(s[sp-1]) + uint32(s[sp]))
		case 0x6B:
			sp--
			s[sp-1] = uint64(uint32(s[sp-1]) - uint32(s[sp]))
		case 0x6C:
			sp--
			s[sp-1] = uint64(uint32(s[sp-1]) * uint32(s[sp]))
		case 0x6D:
			sp--
			a, b := int32(s[sp-1]), int32(s[sp])
			if b == 0 {
				trap("integer divide by zero")
			}
			if a == math.MinInt32 && b == -1 {
				trap("integer overflow")
			}
			s[sp-1] = uint64(uint32(a / b))
		case 0x6E:
			sp--
			a, b := uint32(s[sp-1]), uint32(s[sp])
			if b == 0 {
				trap("integer divide by zero")
			}
			s[sp-1] = uint64(a / b)
		case 0x6F:
			sp--
			a, b := int32(s[sp-1]), int32(s[sp])
			if b == 0 {
				trap("integer divide by zero")
			}
			if b == -1 {
				s[sp-1] = 0
			} else {
				s[sp-1] = uint64(uint32(a % b))
			}
		case 0x70:
			sp--
			a, b := uint32(s[sp-1]), uint32(s[sp])
			if b == 0 {
				trap("integer divide by zero")
			}
			s[sp-1] = uint64(a % b)
		case 0x71:
			sp--
			s[sp-1] = uint64(uint32(s[sp-1] & s[sp]))
		case 0x72:
			sp--
			s[sp-1] = uint64(uint32(s[sp-1] | s[sp]))
		case 0x73:
			sp--
			s[sp-1] = uint64(uint32(s[sp-1] ^ s[sp]))
		case 0x74:
			sp--
			s[sp-1] = uint64(uint32(s[sp-1]) << (s[sp] & 31))
		case 0x75:
			sp--
			s[sp-1] = uint64(uint32(int32(s[sp-1]) >> (s[sp] & 31)))
		case 0x76:
			sp--
			s[sp-1] = uint64(uint32(s[sp-1]) >> (s[sp] & 31))
		case 0x77:
			sp--
			s[sp-1] = uint64(bits.RotateLeft32(uint32(s[sp-1]), int(s[sp]&31)))
		case 0x78:
			sp--
			s[sp-1] = uint64(bits.RotateLeft32(uint32(s[sp-1]), -int(s[sp]&31)))
		case 0x79:
			s[sp-1] = uint64(bits.LeadingZeros64(s[sp-1]))
		case 0x7A:
			s[sp-1] = uint64(bits.TrailingZeros64(s[sp-1]))
		case 0x7B:
			s[sp-1] = uint64(bits.OnesCount64(s[sp-1]))
		case 0x7C:
			sp--
			s[sp-1] += s[sp]
		case 0x7D:
			sp--
			s[sp-1] -= s[sp]
		case 0x7E:
			sp--
			s[sp-1] *= s[sp]
		case 0x7F:
			sp--
			a, b := int64(s[sp-1]), int64(s[sp])
			if b == 0 {
				trap("integer divide by zero")
			}
			if a == math.MinInt64 && b == -1 {
				trap("integer overflow")
			}
			s[sp-1] = uint64(a / b)
		case 0x80:
			sp--
			if s[sp] == 0 {
				trap("integer divide by zero")
			}
			s[sp-1] /= s[sp]
		case 0x81:
			sp--
			a, b := int64(s[sp-1]), int64(s[sp])
			if b == 0 {
				trap("integer divide by zero")
			}
			if b == -1 {
				s[sp-1] = 0
			} else {
				s[sp-1] = uint64(a % b)
			}
		case 0x82:
			sp--
			if s[sp] == 0 {
				trap("integer divide by zero")
			}
			s[sp-1] %= s[sp]
		case 0x83:
			sp--
			s[sp-1] &= s[sp]
		case 0x84:
			sp--
			s[sp-1] |= s[sp]
		case 0x85:
			sp--
			s[sp-1] ^= s[sp]
		case 0x86:
			sp--
			s[sp-1] <<= s[sp] & 63
		case 0x87:
			sp--
			s[sp-1] = uint64(int64(s[sp-1]) >> (s[sp] & 63))
		case 0x88:
			sp--
			s[sp-1] >>= s[sp] & 63
		case 0x89:
			sp--
			s[sp-1] = bits.RotateLeft64(s[sp-1], int(s[sp]&63))
		case 0x8A:
			sp--
			s[sp-1] = bits.RotateLeft64(s[sp-1], -int(s[sp]&63))

		// Floating point.
		case 0x8B:
			s[sp-1] = uint64(uint32(s[sp-1]) &^ (1 << 31))
		case 0x8C:
			s[sp-1] = uint64(uint32(s[sp-1]) ^ (1 << 31))
		case 0x8D:
			s[sp-1] = fr32(float32(math.Ceil(float64(f32(s[sp-1])))))
		case 0x8E:
			s[sp-1] = fr32(float32(math.Floor(float64(f32(s[sp-1])))))
		case 0x8F:
			s[sp-1] = fr32(float32(math.Trunc(float64(f32(s[sp-1])))))
		case 0x90:
			s[sp-1] = fr32(float32(math.RoundToEven(float64(f32(s[sp-1])))))
		case 0x91:
			s[sp-1] = fr32(float32(math.Sqrt(float64(f32(s[sp-1])))))
		case 0x92:
			sp--
			s[sp-1] = fr32(f32(s[sp-1]) + f32(s[sp]))
		case 0x93:
			sp--
			s[sp-1] = fr32(f32(s[sp-1]) - f32(s[sp]))
		case 0x94:
			sp--
			s[sp-1] = fr32(f32(s[sp-1]) * f32(s[sp]))
		case 0x95:
			sp--
			s[sp-1] = fr32(f32(s[sp-1]) / f32(s[sp]))
		case 0x96:
			sp--
			s[sp-1] = fr32(float32(fmin(float64(f32(s[sp-1])), float64(f32(s[sp])))))
		case 0x97:
			sp--
			s[sp-1] = fr32(float32(fmax(float64(f32(s[sp-1])), float64(f32(s[sp])))))
		case 0x98:
			sp--
			s[sp-1] = uint64(uint32(s[sp-1])&^(1<<31) | uint32(s[sp])&(1<<31))
		case 0x99:
			s[sp-1] &^= 1 << 63
		case 0x9A:
			s[sp-1] ^= 1 << 63
		case 0x9B:
			s[sp-1] = fr64(math.Ceil(f64(s[sp-1])))
		case 0x9C:
			s[sp-1] = fr64(math.Floor(f64(s[sp-1])))
		case 0x9D:
			s[sp-1] = fr64(math.Trunc(f64(s[sp-1])))
		case 0x9E:
			s[sp-1] = fr64(math.RoundToEven(f64(s[sp-1])))
		case 0x9F:
			s[sp-1] = fr64(math.Sqrt(f64(s[sp-1])))
		case 0xA0:
			sp--
			s[sp-1] = fr64(f64(s[sp-1]) + f64(s[sp]))
		case 0xA1:
			sp--
			s[sp-1] = fr64(f64(s[sp-1]) - f64(s[sp]))
		case 0xA2:
			sp--
			s[sp-1] = fr64(f64(s[sp-1]) * f64(s[sp]))
		case 0xA3:
			sp--
			s[sp-1] = fr64(f64(s[sp-1]) / f64(s[sp]))
		case 0xA4:
			sp--
			s[sp-1] = fr64(fmin(f64(s[sp-1]), f64(s[sp])))
		case 0xA5:
			sp--
			s[sp-1] = fr64(fmax(f64(s[sp-1]), f64(s[sp])))
		case 0xA6:
			sp--
			s[sp-1] = s[sp-1]&^(1<<63) | s[sp]&(1<<63)

		// Conversions.
		case 0xA7:
			s[sp-1] = uint64(uint32(s[sp-1]))
		case 0xA8:
			s[sp-1] = uint64(uint32(truncS(float64(f32(s[sp-1])), 32)))
		case 0xA9:
			s[sp-1] = uint64(uint32(truncU(float64(f32(s[sp-1])), 32)))
		case 0xAA:
			s[sp-1] = uint64(uint32(truncS(f64(s[sp-1]), 32)))
		case 0xAB:
			s[sp-1] = uint64(uint32(truncU(f64(s[sp-1]), 32)))
		case 0xAC:
			s[sp-1] = uint64(int64(int32(s[sp-1])))
		case 0xAD:
			s[sp-1] = uint64(uint32(s[sp-1]))
		case 0xAE:
			s[sp-1] = uint64(truncS(float64(f32(s[sp-1])), 64))
		case 0xAF:
			s[sp-1] = truncU(float64(f32(s[sp-1])), 64)
		case 0xB0:
			s[sp-1] = uint64(truncS(f64(s[sp-1]), 64))
		case 0xB1:
			s[sp-1] = truncU(f64(s[sp-1]), 64)
		case 0xB2:
			s[sp-1] = fr32(float32(int32(s[sp-1])))
		case 0xB3:
			s[sp-1] = fr32(float32(uint32(s[sp-1])))
		case 0xB4:
			s[sp-1] = fr32(float32(int64(s[sp-1])))
		case 0xB5:
			s[sp-1] = fr32(float32(s[sp-1]))
		case 0xB6:
			s[sp-1] = fr32(float32(f64(s[sp-1])))
		case 0xB7:
			s[sp-1] = fr64(float64(int32(s[sp-1])))
		case 0xB8:
			s[sp-1] = fr64(float64(uint32(s[sp-1])))
		case 0xB9:
			s[sp-1] = fr64(float64(int64(s[sp-1])))
		case 0xBA:
			s[sp-1] = fr64(float64(s[sp-1]))
		case 0xBB:
			s[sp-1] = fr64(float64(f32(s[sp-1])))
		case 0xBC, 0xBE:
			s[sp-1] = uint64(uint32(s[sp-1]))
		case 0xBD, 0xBF:
		case 0xC0:
			s[sp-1] = uint64(uint32(int32(int8(s[sp-1]))))
		case 0xC1:
			s[sp-1] = uint64(uint32(int32(int16(s[sp-1]))))
		case 0xC2:
			s[sp-1] = uint64(int64(int8(s[sp-1])))
		case 0xC3:
			s[sp-1] = uint64(int64(int16(s[sp-1])))
		case 0xC4:
			s[sp-1] = uint64(int64(int32(s[sp-1])))
		case prefixMisc + 0:
			s[sp-1] = uint64(uint32(satS(float64(f32(s[sp-1])), 32)))
		case prefixMisc + 1:
			s[sp-1] = uint64(uint32(satU(float64(f32(s[sp-1])), 32)))
		case prefixMisc + 2:
			s[sp-1] = uint64(uint32(satS(f64(s[sp-1]), 32)))
		case prefixMisc + 3:
			s[sp-1] = uint64(uint32(satU(f64(s[sp-1]), 32)))
		case prefixMisc + 4:
			s[sp-1] = uint64(satS(float64(f32(s[sp-1])), 64))
		case prefixMisc + 5:
			s[sp-1] = satU(float64(f32(s[sp-1])), 64)
		case prefixMisc + 6:
			s[sp-1] = uint64(satS(f64(s[sp-1]), 64))
		case prefixMisc + 7:
			s[sp-1] = satU(f64(s[sp-1]), 64)

		// Bulk memory and tables.
		case prefixMisc + 0x08: // memory.init
			sp -= 3
			inst.memoryInit(mem, inst.datas[in.a], uint32(s[sp]), uint32(s[sp+1]), uint32(s[sp+2]))
		case prefixMisc + 0x09:
			inst.datas[in.a] = nil
		case prefixMisc + 0x0A: // memory.copy
			sp -= 3
			d, src, n := uint64(uint32(s[sp])), uint64(uint32(s[sp+1])), uint64(uint32(s[sp+2]))
			if src+n > uint64(len(mem.Data)) || d+n > uint64(len(mem.Data)) {
				trap("out of bounds memory access")
			}
			copy(mem.Data[d:d+n], mem.Data[src:src+n])
		case prefixMisc + 0x0B: // memory.fill
			sp -= 3
			d, v, n := uint64(uint32(s[sp])), byte(s[sp+1]), uint64(uint32(s[sp+2]))
			if d+n > uint64(len(mem.Data)) {
				trap("out of bounds memory access")
			}
			b := mem.Data[d : d+n]
			for i := range b {
				b[i] = v
			}
		case prefixMisc + 0x0C: // table.init
			sp -= 3
			inst.tableInit(inst.tables[in.b], inst.elems[in.a], uint32(s[sp]), uint32(s[sp+1]), uint32(s[sp+2]))
		case prefixMisc + 0x0D:
			inst.elems[in.a] = nil
		case prefixMisc + 0x0E: // table.copy
			sp -= 3
			dt, st := inst.tables[in.a], inst.tables[in.b]
			d, src, n := uint64(uint32(s[sp])), uint64(uint32(s[sp+1])), uint64(uint32(s[sp+2]))
			if src+n > uint64(len(st.Elems)) || d+n > uint64(len(dt.Elems)) {
				trap("out of bounds table access")
			}
			copy(dt.Elems[d:d+n], st.Elems[src:src+n])
		case prefixMisc + 0x0F: // table.grow
			sp--
			t := inst.tables[in.a]
			n := uint64(uint32(s[sp]))
			old := uint64(len(t.Elems))
			max := uint64(1<<32 - 1)
			if t.HasMax {
				max = uint64(t.Max)
			}
			if old+n > max || old+n > 1<<24 {
				s[sp-1] = uint64(uint32(0xFFFFFFFF))
			} else {
				v := s[sp-1]
				for i := uint64(0); i < n; i++ {
					t.Elems = append(t.Elems, v)
				}
				s[sp-1] = old
			}
		case prefixMisc + 0x10: // table.size
			s[sp] = uint64(len(inst.tables[in.a].Elems))
			sp++
		case prefixMisc + 0x11: // table.fill
			sp -= 3
			t := inst.tables[in.a]
			i, v, n := uint64(uint32(s[sp])), s[sp+1], uint64(uint32(s[sp+2]))
			if i+n > uint64(len(t.Elems)) {
				trap("out of bounds table access")
			}
			for k := uint64(0); k < n; k++ {
				t.Elems[i+k] = v
			}
		default:
			if in.op >= prefixSIMD && in.op < prefixSIMD+0x100 {
				sp = m.simd(in, s, sp, addr, mem)
				continue
			}
			panic(fmt.Sprintf("wasm: no implementation of %s", ops[int(in.op)].name))
		}
	}
}

func f32(v uint64) float32  { return math.Float32frombits(uint32(v)) }
func f64(v uint64) float64  { return math.Float64frombits(v) }
func fr32(f float32) uint64 { return uint64(math.Float32bits(f)) }
func fr64(f float64) uint64 { return math.Float64bits(f) }
func le16(b []byte) uint16  { return uint16(b[0]) | uint16(b[1])<<8 }
func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
func le64(b []byte) uint64     { return uint64(le32(b)) | uint64(le32(b[4:]))<<32 }
func put16(b []byte, v uint16) { b[0], b[1] = byte(v), byte(v>>8) }
func put32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
func put64(b []byte, v uint64) { put32(b, uint32(v)); put32(b[4:], uint32(v>>32)) }

// fcmp compares by the operation of index k: eq, ne, lt, gt, le, ge.
func fcmp(k uint16, a, b float64) bool {
	switch k {
	case 0:
		return a == b
	case 1:
		return a != b
	case 2:
		return a < b
	case 3:
		return a > b
	case 4:
		return a <= b
	}
	return a >= b
}

// nan is the canonical NaN.
var nan = math.Float64frombits(0x7FF8000000000000)

func fmin(a, b float64) float64 {
	switch {
	case a != a || b != b:
		return nan
	case a == 0 && b == 0:
		if math.Signbit(a) {
			return a
		}
		return b
	case a < b:
		return a
	}
	return b
}

func fmax(a, b float64) float64 {
	switch {
	case a != a || b != b:
		return nan
	case a == 0 && b == 0:
		if math.Signbit(a) {
			return b
		}
		return a
	case a > b:
		return a
	}
	return b
}

// truncS truncates x to a signed integer of n bits, trapping if it does
// not fit.
func truncS(x float64, n int) int64 {
	if x != x {
		trap("invalid conversion to integer")
	}
	lim := math.Ldexp(1, n-1)
	if !(x >= -lim && x < lim) {
		if n == 32 && x > -lim-1 && x < lim {
			return int64(x)
		}
		trap("integer overflow")
	}
	return int64(x)
}

func truncU(x float64, n int) uint64 {
	if x != x {
		trap("invalid conversion to integer")
	}
	if !(x > -1 && x < math.Ldexp(1, n)) {
		trap("integer overflow")
	}
	return uint64(x)
}

func satS(x float64, n int) int64 {
	lim := math.Ldexp(1, n-1)
	switch {
	case x != x:
		return 0
	case x <= -lim:
		return -1 << uint(n-1)
	case x >= lim:
		return 1<<uint(n-1) - 1
	}
	return int64(x)
}

func satU(x float64, n int) uint64 {
	switch {
	case x != x || x <= -1:
		return 0
	case x >= math.Ldexp(1, n):
		if n == 64 {
			return math.MaxUint64
		}
		return 1<<uint(n) - 1
	}
	return uint64(x)
}
