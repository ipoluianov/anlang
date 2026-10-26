package desc

import "fmt"

// Machine executes instructions by their formal semantics: the
// interpreter of an architecture, which the built-in emulators use
// (target.arch.generated, tool.emu.generated). Nothing in it depends on
// a particular architecture.
type Machine struct {
	A *Arch
	// Mem is the memory from physical address 0: all 2^AddressBits bytes
	// if AddressBits is at most 24, otherwise none. Map adds memory
	// elsewhere; an access to an address without memory fails.
	Mem []byte
	// In and Out are the input and output ports; nil ports read 0.
	In  func(port uint64, width int) uint64
	Out func(port uint64, width int, v uint64)
	// Halted is set by the built-in function halt.
	Halted bool
	// Cycles counts the clock cycles of the executed instructions.
	Cycles int64

	regions []region // the memory added by Map
	ios     []ioRegion
	regs    []uint64 // the whole registers, by index in A.Registers
	stack   []val    // local slots of compiled semantics
	sp      int
	cache   map[cacheKey]*decodedEntry
	// istart is the program counter value of the executing instruction.
	istart uint64
	opbuf  [4]operandVal
	ops    [4]*operandVal
}

// NewMachine returns a machine with zero registers and memory.
func NewMachine(a *Arch) *Machine {
	a.compileOnce.Do(a.compile)
	return newMachine(a, true)
}

// newMachine returns a machine of the compiled description a, with
// memory from address 0 if mem is set and AddressBits allows it.
func newMachine(a *Arch, mem bool) *Machine {
	m := &Machine{A: a, regs: make([]uint64, len(a.Registers)), cache: map[cacheKey]*decodedEntry{}}
	if mem && a.AddressBits <= 24 {
		m.Mem = make([]byte, 1<<uint(a.AddressBits))
	}
	for i := range a.Registers {
		if r := &a.Registers[i]; r.Parent == "" {
			m.regs[i] = uint64(r.Fixed)
		}
	}
	for i := range m.ops {
		m.ops[i] = &m.opbuf[i]
	}
	return m
}

// region is memory at a physical address.
type region struct {
	base uint64
	data []byte
}

// Map adds size bytes of zero memory at physical address base and
// returns them.
func (m *Machine) Map(base uint64, size int) []byte {
	data := make([]byte, size)
	m.regions = append(m.regions, region{base, data})
	return data
}

// IO is a device at a range of physical addresses, such as the registers
// of a peripheral: the machine reads and writes it with the width of each
// access, at the offset from the start of the range.
type IO interface {
	Read(off uint64, width int) uint64
	Write(off uint64, width int, v uint64)
}

type ioRegion struct {
	base, size uint64
	io         IO
}

// MapIO maps device io at size bytes from physical address base.
func (m *Machine) MapIO(base, size uint64, io IO) {
	m.ios = append(m.ios, ioRegion{base, size, io})
}

// ioAt returns the device at physical address addr and the offset into
// it, if any.
func (m *Machine) ioAt(addr uint64) (IO, uint64) {
	for i := range m.ios {
		r := &m.ios[i]
		if addr-r.base < r.size {
			return r.io, addr - r.base
		}
	}
	return nil, 0
}

// Fault fails the executing instruction with a message, for devices.
func Fault(format string, args ...any) { failf(format, args...) }

// byteRef returns the byte at physical address addr; an address without
// memory fails the executing instruction.
func (m *Machine) byteRef(addr uint64) *byte {
	if addr < uint64(len(m.Mem)) {
		return &m.Mem[addr]
	}
	for i := range m.regions {
		r := &m.regions[i]
		if addr-r.base < uint64(len(r.data)) {
			return &r.data[addr-r.base]
		}
	}
	failf("no memory at address %#x", addr)
	return nil
}

// peek returns the byte at physical address addr, and whether there is
// memory there.
func (m *Machine) peek(addr uint64) (byte, bool) {
	if addr < uint64(len(m.Mem)) {
		return m.Mem[addr], true
	}
	for i := range m.regions {
		r := &m.regions[i]
		if addr-r.base < uint64(len(r.data)) {
			return r.data[addr-r.base], true
		}
	}
	return 0, false
}

// compile compiles the semantics of the description.
func (a *Arch) compile() {
	for i := range a.Registers {
		r := &a.Registers[i]
		r.index = i
		if r.Parent != "" {
			r.parent = a.regs[r.Parent]
		}
	}
	a.compileFuncs()
	for i := range a.Operands {
		if o := &a.Operands[i]; o.sparsed != nil {
			o.csyntax, _ = a.compileExpr(o.sparsed, "v")
		}
	}
	a.cfetch, _ = a.compileExpr(a.fetch)
	a.caddress, _ = a.compileExpr(a.address, "seg", "off")
	a.instrPC = true // addresses of operands read the program counter as instructions do
	for i := range a.Fragments {
		for j := range a.Fragments[i].Alternatives {
			alt := &a.Fragments[i].Alternatives[j]
			alt.fields = nil
			for _, it := range alt.pattern {
				if it.Kind == Field {
					alt.fields = append(alt.fields, it.Name)
				}
			}
			if alt.address != nil {
				alt.caddress, _ = a.compileExpr(alt.address, alt.fields...)
			}
			if alt.segment != nil {
				alt.csegment, _ = a.compileExpr(alt.segment, alt.fields...)
			}
		}
	}
	a.instrPC = false
	for i := range a.Prefixes {
		a.Prefixes[i].cbody = a.compileBody(a.Prefixes[i].body)
	}
	a.instrPC = true
	for i := range a.Instructions {
		a.Instructions[i].cbody = a.compileBody(a.Instructions[i].body)
	}
	a.instrPC = false
}

// Reg returns the value of a register or a register part.
func (m *Machine) Reg(name string) uint64 {
	r := m.A.regs[name]
	if r == nil {
		panic("desc: unknown register " + name)
	}
	return m.readReg(r)
}

// SetReg sets a register or a register part.
func (m *Machine) SetReg(name string, v uint64) {
	r := m.A.regs[name]
	if r == nil {
		panic("desc: unknown register " + name)
	}
	m.writeReg(r, v)
}

func (m *Machine) readReg(r *Register) uint64 {
	if r.parent == nil {
		return (m.regs[r.index] | uint64(r.Fixed)) & mask(r.Width)
	}
	return (m.readReg(r.parent) >> uint(r.Offset)) & mask(r.Width)
}

func (m *Machine) writeReg(r *Register, v uint64) {
	v &= mask(r.Width)
	if r.parent == nil {
		m.regs[r.index] = (v | uint64(r.Fixed)) &^ uint64(r.Zero) & mask(r.Width)
		return
	}
	p := r.parent
	old := m.readReg(p)
	m.writeReg(p, old&^(mask(r.Width)<<uint(r.Offset))|v<<uint(r.Offset))
}

func (m *Machine) addrMask() uint64 { return mask(m.A.AddressBits) }

// Read returns the value of width bits at physical address addr.
func (m *Machine) Read(addr uint64, width int) uint64 {
	if len(m.ios) > 0 {
		if io, off := m.ioAt(addr & m.addrMask()); io != nil {
			return io.Read(off, width) & mask(width)
		}
	}
	n := width / 8
	var v uint64
	for i := 0; i < n; i++ {
		b := uint64(*m.byteRef((addr + uint64(i)) & m.addrMask()))
		if m.A.Endian == 0 {
			v |= b << uint(8*i)
		} else {
			v = v<<8 | b
		}
	}
	return v
}

// Write stores width bits of v at physical address addr.
func (m *Machine) Write(addr uint64, width int, v uint64) {
	if len(m.ios) > 0 {
		if io, off := m.ioAt(addr & m.addrMask()); io != nil {
			io.Write(off, width, v&mask(width))
			return
		}
	}
	n := width / 8
	for i := 0; i < n; i++ {
		var b byte
		if m.A.Endian == 0 {
			b = byte(v >> uint(8*i))
		} else {
			b = byte(v >> uint(8*(n-1-i)))
		}
		*m.byteRef((addr + uint64(i)) & m.addrMask()) = b
	}
}

// ExecError is an error in executing an instruction.
type ExecError struct {
	Addr uint64
	Msg  string
}

func (e *ExecError) Error() string { return fmt.Sprintf("at %#x: %s", e.Addr, e.Msg) }

// FetchAddr returns the physical address of the next instruction.
func (m *Machine) FetchAddr() uint64 {
	return m.eval(m.A.cfetch).v & m.addrMask()
}

// cacheKey identifies a decoded instruction: the value of the program
// counter and the physical address it denotes.
type cacheKey struct{ pc, addr uint64 }

// decodedEntry is a decoded instruction with the addresses and values of
// its bytes, which must still be in memory for the entry to be used.
type decodedEntry struct {
	d     *Decoded
	addrs []uint64
	bytes []byte
	plans []opPlan
}

// opPlan is what can be computed of an operand when it is decoded.
type opPlan struct {
	reg      *Register
	imm      bool
	value    uint64
	relative bool
	width    int
	alt      *Alternative // a memory operand
	fields   []val
}

// relBase returns the base of the relative operands of the executing
// instruction: the address of the instruction plus PCRead, or the
// program counter advanced past the instruction.
func (m *Machine) relBase() uint64 {
	if m.A.PCRead != 0 {
		return m.istart + uint64(m.A.PCRead)
	}
	return m.readReg(m.A.pc)
}

// Step decodes and executes one instruction with its prefixes.
func (m *Machine) Step() (err error) {
	a := m.A
	pcVal := m.readReg(a.pc)
	fetch := m.eval(a.cfetch).v & m.addrMask()
	key := cacheKey{pcVal, fetch}
	e := m.cache[key]
	if e == nil || !e.valid(m) {
		e, err = m.decode(pcVal, fetch)
		if err != nil {
			return err
		}
		m.cache[key] = e
	}
	d := e.d
	m.istart = pcVal
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(execFail)
			if !ok {
				panic(r)
			}
			m.sp = 0
			err = &ExecError{fetch, fmt.Sprintf("%s: %s", d.Inst, f.msg)}
		}
	}()
	m.writeReg(a.pc, pcVal+uint64(d.Length))
	for _, p := range d.Prefixes {
		m.run(p.cbody, nil)
	}
	// The operands, with the addresses of memory operands computed
	// after the prefixes.
	for i := range e.plans {
		m.operand(&e.plans[i], &m.opbuf[i])
	}
	m.Cycles += int64(d.Inst.Cycles)
	m.run(d.Inst.cbody, m.ops[:len(e.plans)])
	for _, r := range a.classes["prefix"] {
		m.writeReg(r, 0)
	}
	return nil
}

func (e *decodedEntry) valid(m *Machine) bool {
	for i, addr := range e.addrs {
		if b, _ := m.peek(addr); b != e.bytes[i] {
			return false
		}
	}
	return true
}

// decode decodes the instruction at the program counter value pcVal,
// whose physical address is fetch.
func (m *Machine) decode(pcVal, fetch uint64) (*decodedEntry, error) {
	a := m.A
	// The bytes of the instruction follow the program counter, which
	// wraps around within its range.
	var addrs [16]uint64
	code := make([]byte, 16)
	for i := range code {
		// Set directly: the bits of the program counter that read as 0,
		// such as bit 0 for halfword instructions, still count here.
		if a.pc.parent == nil {
			m.regs[a.pc.index] = (pcVal + uint64(i)) & mask(a.pc.Width)
		} else {
			m.writeReg(a.pc, pcVal+uint64(i))
		}
		addrs[i] = m.eval(a.cfetch).v & m.addrMask()
		code[i], _ = m.peek(addrs[i])
	}
	m.writeReg(a.pc, pcVal)
	d, amb := a.Decode(code)
	if d == nil || len(amb) > 0 {
		return nil, &ExecError{fetch, fmt.Sprintf("invalid instruction % X", code[:4])}
	}
	e := &decodedEntry{d: d, addrs: append([]uint64(nil), addrs[:d.Length]...), bytes: code[:d.Length]}
	for i, o := range d.Inst.ops {
		e.plans = append(e.plans, a.plan(d, i, o))
	}
	return e, nil
}

// plan computes what can be known of operand i when it is decoded.
func (a *Arch) plan(d *Decoded, i int, o *Operand) opPlan {
	name := opName(i)
	w := o.ValueWidth(a)
	switch {
	case o.Register != "":
		return opPlan{reg: a.regs[o.Register], width: w}
	case o.Constant:
		return opPlan{imm: true, value: uint64(o.Value)}
	case o.List || o.Syntax != "":
		return opPlan{imm: true, value: d.Fields[name], width: w}
	case o.Imm != 0:
		v := o.bits(a, d.Fields[name]) & mask(w)
		return opPlan{imm: true, value: v, relative: o.Relative, width: w}
	}
	if alt := d.Alts[name]; alt != nil {
		if alt.Register != "" {
			return opPlan{reg: a.classReg(o.Class, d.Fields[name+"."+alt.Register]), width: w}
		}
		p := opPlan{alt: alt, width: w}
		for _, it := range alt.pattern {
			if it.Kind == Field {
				p.fields = append(p.fields, val{d.Fields[name+"."+it.Name], it.Width})
			}
		}
		return p
	}
	// A register of a class encoded by a field.
	return opPlan{reg: a.classReg(o.Class, d.Fields[name]), width: w}
}

// operand evaluates an operand of the executing instruction into o.
func (m *Machine) operand(p *opPlan, o *operandVal) {
	*o = operandVal{width: p.width}
	switch {
	case p.reg != nil:
		o.reg = p.reg
	case p.imm:
		o.imm, o.value = true, p.value
		if p.relative {
			o.value = (m.relBase() + p.value) & mask(p.width)
		}
	case p.alt != nil:
		off := m.eval(p.alt.caddress, p.fields...)
		var seg val
		if p.alt.csegment != nil {
			seg = m.eval(p.alt.csegment, p.fields...)
		}
		m.Cycles += int64(p.alt.Cycles)
		o.mem, o.off, o.seg, o.segW = true, off.v&mask(m.A.Word), seg.v, seg.w
	}
}

// operandVal is an operand of the executing instruction.
type operandVal struct {
	reg   *Register // a register operand
	mem   bool      // a memory operand
	off   uint64    // the offset and segment value of a memory operand
	seg   uint64
	segW  int
	width int
	value uint64 // an immediate or constant
	imm   bool
}

func signExtend(v uint64, w int) uint64 {
	if w < 64 && v&(1<<uint(w-1)) != 0 {
		return v | ^mask(w)
	}
	return v
}

// val is a value with its width; width 0 is unsized.
type val struct {
	v uint64
	w int
}

type execFail struct{ msg string }

func failf(format string, args ...any) { panic(execFail{fmt.Sprintf(format, args...)}) }

func width2(x, y val) int {
	if x.w != 0 {
		return x.w
	}
	return y.w
}

func boolVal(b bool) val {
	if b {
		return val{1, 1}
	}
	return val{0, 1}
}

func signed(v val) int64 {
	if v.w == 0 || v.w >= 64 {
		return int64(v.v)
	}
	return int64(signExtend(v.v, v.w))
}

// byteAddr returns the physical address of byte i of memory operand o:
// the address of offset off+i in its segment, the offset wrapping around
// within its range (target.arch.execution).
func (m *Machine) byteAddr(o *operandVal, i int) uint64 {
	a := m.A
	return m.eval(a.caddress, val{o.seg, o.segW}, val{(o.off + uint64(i)) & mask(a.Word), a.Word}).v & m.addrMask()
}

func (m *Machine) readOperand(o *operandVal) uint64 {
	if len(m.ios) > 0 {
		if io, off := m.ioAt(m.byteAddr(o, 0)); io != nil {
			return io.Read(off, o.width) & mask(o.width)
		}
	}
	n := o.width / 8
	var v uint64
	for i := 0; i < n; i++ {
		b := uint64(*m.byteRef(m.byteAddr(o, i)))
		if m.A.Endian == 0 {
			v |= b << uint(8*i)
		} else {
			v = v<<8 | b
		}
	}
	return v
}

func (m *Machine) writeOperand(o *operandVal, v uint64) {
	if len(m.ios) > 0 {
		if io, off := m.ioAt(m.byteAddr(o, 0)); io != nil {
			io.Write(off, o.width, v&mask(o.width))
			return
		}
	}
	n := o.width / 8
	for i := 0; i < n; i++ {
		var b byte
		if m.A.Endian == 0 {
			b = byte(v >> uint(8*i))
		} else {
			b = byte(v >> uint(8*(n-1-i)))
		}
		*m.byteRef(m.byteAddr(o, i)) = b
	}
}

// classReg returns register number n of a class. A field wider than the
// class needs selects the register by the low bits (target.arch.encoding).
func (a *Arch) classReg(class string, n uint64) *Register {
	regs := a.classes[class]
	return regs[n%uint64(len(regs))]
}
