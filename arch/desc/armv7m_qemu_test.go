package desc

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The comparison of the ARMv7-M interpreter with QEMU on random
// instructions (spec/24-roadmap.md, step 15). A raw image for the
// mps2-an385 board (a Cortex-M3) holds a program, assembled with the
// assembler derived from the description, that fills a data area, then
// for each random instruction sets the registers and the flags, executes
// the instruction and writes the registers through semihosting, and at
// the end writes the data area. QEMU runs the image; the interpreter runs
// the same program, with semihosting modeled by the test; their outputs
// must agree.
//
// The interpreter executes each candidate instruction while the program
// is generated, and the program keeps only instructions that neither
// change the program counter other than by a short forward branch, nor
// change SP, nor access memory outside the data area: the dump of the
// registers is addressed relative to SP. Instructions with unpredictable
// operands, such as SP or PC as an operand of most 32-bit forms, and
// those that wait or that change the stack in use are left out.

const (
	armDataBase  = 0x20000000
	armDataSize  = 0x10000
	armConstBase = 0x3F0000 // the name ":tt", the open block and the handle
	armCodeSize  = 0x400000
	armHandle    = armConstBase + 0x20
)

// armProg is a program being generated and executed by the shadow
// interpreter.
type armProg struct {
	t    *testing.T
	a    *Arch
	m    *Machine
	code []byte // the image from address 0
	out  bytes.Buffer
	done bool
}

func (p *armProg) pc() int { return len(p.code) }

// emit assembles lines at the end of the program.
func (p *armProg) emit(lines ...string) {
	p.t.Helper()
	for _, l := range lines {
		b, err := p.a.AsmAt(l, uint64(p.pc()))
		if err != nil {
			p.t.Fatal(err)
		}
		p.code = append(p.code, b...)
	}
}

// load32 loads a constant into a register.
func (p *armProg) load32(reg string, v uint32) {
	p.emit(fmt.Sprintf("MOVW %s, 0x%X", reg, v&0xFFFF))
	if v>>16 != 0 {
		p.emit(fmt.Sprintf("MOVT %s, 0x%X", reg, v>>16))
	}
}

// sync copies the program into the memory of the shadow machine.
func (p *armProg) sync() {
	copy(p.m.regions[0].data, p.code)
}

// run executes the shadow machine until PC is at address end.
func (p *armProg) run(end int) {
	p.t.Helper()
	p.sync()
	for steps := 0; !p.done && p.m.Reg("PC") != uint64(end); steps++ {
		if err := p.step(); err != nil {
			p.t.Fatalf("at %#x: %v", p.m.Reg("PC"), err)
		}
		if steps > 1_000_000 {
			p.t.Fatalf("the program does not reach %#x", end)
		}
	}
}

// step executes one instruction, or a semihosting call at BKPT 0xAB.
func (p *armProg) step() error {
	m := p.m
	pc := m.Reg("PC")
	if m.Read(pc, 16) != 0xBEAB {
		return m.Step()
	}
	r1 := m.Reg("R1")
	switch m.Reg("R0") {
	case 0x01: // SYS_OPEN
		m.SetReg("R0", 1)
	case 0x05: // SYS_WRITE
		buf, n := m.Read(r1+4, 32), m.Read(r1+8, 32)
		for i := uint64(0); i < n; i++ {
			p.out.WriteByte(byte(m.Read(buf+i, 8)))
		}
		m.SetReg("R0", 0)
	case 0x18: // SYS_EXIT
		p.done = true
	default:
		return fmt.Errorf("semihosting call %#x", m.Reg("R0"))
	}
	m.SetReg("PC", pc+2)
	return nil
}

// armSnapshot is the state of the shadow machine.
type armSnapshot struct {
	regs []uint64
	data []byte
	out  int
}

func (p *armProg) snapshot() armSnapshot {
	return armSnapshot{append([]uint64(nil), p.m.regs...), append([]byte(nil), p.m.regions[1].data...), p.out.Len()}
}

func (p *armProg) restore(s armSnapshot) {
	copy(p.m.regs, s.regs)
	copy(p.m.regions[1].data, s.data)
	p.out.Truncate(s.out)
}

// armDumpRegs are the registers that each record holds, in order.
var armDumpRegs = []string{"R0", "R1", "R2", "R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10", "R11", "R12", "LR", "APSR", "SP"}

// dump writes the registers, addressed below SP, through semihosting.
func (p *armProg) dump() {
	for i, r := range armDumpRegs[:14] {
		p.emit(fmt.Sprintf("STR %s, dword [SP-0x%X]", r, 0x60-4*i))
	}
	p.emit("MRS R0, APSR", "STR R0, dword [SP-0x28]", "MOV R0, SP", "STR R0, dword [SP-0x24]")
	p.load32("R0", armHandle)
	p.emit("LDR R0, dword [R0]", "STR R0, dword [SP-0x10]", "SUB R0, SP, 0x60", "STR R0, dword [SP-0xC]",
		"MOVS R0, 0x40", "STR R0, dword [SP-0x8]", "SUB R1, SP, 0x10", "MOVS R0, 0x5", "BKPT 0xAB")
}

// armRegName returns the name of register number n.
func armRegName(a *Arch, n int) string { return a.classes["r"][n].Name }

// armAllowed reports whether a decoded instruction may be tested: its
// operands are predictable, it does not stop or wait, and it does not
// load code that follows it.
func armAllowed(a *Arch, d *Decoded, code []byte) bool {
	in := d.Inst
	switch in.Mnemonic {
	case "BKPT", "SVC", "UDF", "WFI", "WFE", "TBB", "TBH":
		return false
	case "MSR":
		if in.Operands[0] == "SP" || in.Operands[0] == "PSP" || in.Operands[0] == "CONTROL" {
			return false
		}
	}
	thumb2 := d.Length == 4
	var regs []int
	for i, o := range in.ops {
		name := opName(i)
		switch {
		case o.List:
			list := d.Fields[name]
			if list&(1<<13) != 0 || list&(1<<15) != 0 && in.Mnemonic != "POP" && in.Mnemonic != "LDM" {
				return false
			}
			if list == 0 || thumb2 && list&(list-1) == 0 {
				return false // an empty list, or a single register in a 32-bit form
			}
			// A base register in the list of a form that writes it back.
			if len(regs) > 0 && list&(1<<uint(regs[0])) != 0 {
				return false
			}
		case o.Class == "r" && o.Memory == 0:
			n := int(d.Fields[name])
			if o.Fragment != "" {
				continue
			}
			regs = append(regs, n)
			if (n == 13 || n == 15) && (thumb2 || in.Mnemonic != "ADD" && in.Mnemonic != "MOV" && in.Mnemonic != "CMP") {
				return false
			}
		}
		if alt := d.Alts[name]; alt != nil {
			// A load from PC forward reads code not yet generated.
			if sh := a.shape(alt); alt.Register == "" && !sh.neg && strings.Contains(strings.Join(sh.regs, " "), "PC") {
				return false
			}
			// The bound fields of a fragment: Rt and Rt2.
			for _, f := range []string{"rt", "rt2"} {
				if v, ok := d.Fields[name+"."+f]; ok && (v == 13 || v == 15) {
					return false
				}
			}
		}
	}
	switch in.Mnemonic {
	case "SMULL", "UMULL", "SMLAL", "UMLAL", "LDRD":
		if regs[0] == regs[1] {
			return false
		}
	case "UBFX", "SBFX":
		if d.Fields["c"]+d.Fields["d"]+1 > 32 {
			return false
		}
	case "BFI":
		if d.Fields["d"] < d.Fields["c"] {
			return false
		}
	case "BFC":
		if d.Fields["c"] < d.Fields["b"] {
			return false
		}
	}
	return true
}

// armAddressRegs returns the registers that the decoded instruction uses
// as addresses: the base and index of its memory operands, and the base
// of its multiple loads and stores.
func armAddressRegs(a *Arch, d *Decoded) []string {
	var regs []string
	for i, o := range d.Inst.ops {
		if alt := d.Alts[opName(i)]; alt != nil && alt.Register == "" {
			regs = append(regs, a.shape(alt).order...)
		}
		if o.List && i > 0 && d.Inst.ops[0].Class == "r" {
			regs = append(regs, armRegName(a, int(d.Fields["a"])))
		}
		if o.List && i > 0 && d.Inst.ops[0].Register != "" {
			regs = append(regs, d.Inst.ops[0].Register)
		}
	}
	return regs
}

// armTested are the instruction forms that the comparison executed.
var armTested = map[*Instruction]bool{}

func TestARMv7MQEMU(t *testing.T) {
	root := filepath.Join("..", "..", "external", "qemu", "root")
	qemu := filepath.Join(root, "usr", "bin", "qemu-system-arm")
	if _, err := os.Stat(qemu); err != nil {
		t.Skip("QEMU is not downloaded: run scripts/fetch-qemu.sh")
	}
	a := loadARMv7M(t)
	rng := rand.New(rand.NewSource(7))
	images := 2
	if testing.Short() {
		images = 1
	}
	total := 0
	for i := 0; i < images; i++ {
		total += armQEMUImage(t, a, rng, qemu)
	}
	t.Logf("%d instructions compared, of %d of the %d instruction forms", total, len(armTested), len(a.Instructions))
	if testing.Verbose() {
		var missing []string
		for i := range a.Instructions {
			if !armTested[&a.Instructions[i]] {
				missing = append(missing, a.Instructions[i].String())
			}
		}
		t.Logf("not executed: %s", strings.Join(missing, "; "))
	}
}

func armQEMUImage(t *testing.T, a *Arch, rng *rand.Rand, qemu string) int {
	m := NewMachine(a)
	m.Map(0, armCodeSize)
	m.Map(armDataBase, armDataSize)
	p := &armProg{t: t, a: a, m: m}
	// The vector table: the initial SP, the reset vector, then the fault
	// vectors, which end the program with an error.
	p.code = make([]byte, 0x40)
	fault := 0x40
	binary.LittleEndian.PutUint32(p.code[0:], armDataBase+armDataSize)
	for v := 2; v < 16; v++ {
		binary.LittleEndian.PutUint32(p.code[4*v:], uint32(fault)|1)
	}
	p.emit("MOVS R0, 0x18")
	p.load32("R1", 0x20024) // ADP_Stopped_RunTimeErrorUnknown
	p.emit("BKPT 0xAB")
	start := p.pc()
	binary.LittleEndian.PutUint32(p.code[4:], uint32(start)|1)
	// The name and the block of SYS_OPEN.
	consts := make([]byte, 0x24)
	copy(consts, ":tt\x00")
	binary.LittleEndian.PutUint32(consts[0x10:], armConstBase)
	binary.LittleEndian.PutUint32(consts[0x14:], 4) // "w"
	binary.LittleEndian.PutUint32(consts[0x18:], 3)
	m.SetReg("SP", armDataBase+armDataSize)
	m.SetReg("PC", uint64(start))
	// Fill the data area with a pattern, and open the console.
	p.load32("R0", armDataBase)
	p.load32("R1", armDataBase+armDataSize)
	p.load32("R2", 0x9E3779B9)
	p.load32("R4", 0x41C64E6D)
	p.load32("R5", 0x3039)
	loop := p.pc()
	p.emit("STR R2, dword [R0]", "ADDS R0, 0x4", "MUL R2, R2, R4", "ADD R2, R2, R5", "CMP R0, R1", fmt.Sprintf("BNE 0x%X", loop))
	p.load32("R1", armConstBase+0x10)
	p.emit("MOVS R0, 0x1", "BKPT 0xAB")
	p.load32("R1", armHandle)
	p.emit("STR R0, dword [R1]")
	copy(m.regions[0].data[armConstBase:], consts)
	p.run(p.pc())

	type testCase struct{ text string }
	var cases []testCase
	value := func() uint32 {
		switch rng.Intn(6) {
		case 0:
			return []uint32{0, 1, 0x7FFFFFFF, 0x80000000, 0xFFFFFFFF, 0xFFFF, 0x8000, 0xFF, 31, 32}[rng.Intn(10)]
		case 1:
			return uint32(rng.Intn(64))
		}
		return rng.Uint32()
	}
	for p.pc() < armConstBase-0x200 && len(cases) < 3000 {
		caseStart := p.pc()
		snap := p.snapshot()
		var d *Decoded
		var code []byte
		for {
			if rng.Intn(4) != 0 {
				// A random encoding of a random instruction form.
				code = armRandomEncoding(a, &a.Instructions[rng.Intn(len(a.Instructions))], rng)
				if code == nil {
					continue
				}
				code = append(code, 0, 0)
			} else {
				code = make([]byte, 4)
				rng.Read(code)
			}
			var amb []*Instruction
			d, amb = a.Decode(code)
			if d != nil && len(amb) == 0 && armAllowed(a, d, code) {
				break
			}
		}
		code = code[:d.Length]
		text := a.Disasm(d)
		// The registers: random values, addresses in the data area for the
		// registers that form addresses, and SP within the data area. The
		// register of BX and BLX holds a short forward target, set last.
		vals := map[string]uint32{}
		for _, r := range armDumpRegs[:14] {
			vals[r] = value()
		}
		for k, r := range armAddressRegs(a, d) {
			if r == "SP" || r == "PC" {
				continue
			}
			if k == 0 {
				vals[r] = armDataBase + 0x1000 + uint32(rng.Intn(0x6000))&^3
			} else {
				vals[r] = uint32(rng.Intn(0x400)) &^ 3
			}
		}
		branchReg := ""
		if d.Inst.Mnemonic == "BX" || d.Inst.Mnemonic == "BLX" {
			branchReg = armRegName(a, int(d.Fields["a"]))
		}
		sp := armDataBase + 0x4000 + uint32(rng.Intn(0x6000))&^3
		apsr := rng.Uint32() & 0xF8000000
		p.load32("R0", apsr)
		p.emit("MSR APSR, R0")
		p.load32("R0", sp)
		p.emit("MOV SP, R0")
		for _, r := range armDumpRegs[:14] {
			if r != branchReg {
				p.load32(r, vals[r])
			}
		}
		patch := p.pc()
		if branchReg != "" {
			p.emit("MOVW "+branchReg+", 0x0", "MOVT "+branchReg+", 0x0")
		}
		at := p.pc()
		taken := at + len(code) + 2*rng.Intn(8)
		switch {
		case hasRelative(d):
			// A short forward branch over marker instructions.
			text = relativeText(a, d, taken)
			b, err := a.AsmAt(text, uint64(at))
			if err != nil || at+len(b) > taken {
				p.code = p.code[:caseStart]
				p.restore(snap)
				continue
			}
			code = b
		case branchReg != "":
			p.code = p.code[:patch]
			p.emit(fmt.Sprintf("MOVW %s, 0x%X", branchReg, (taken|1)&0xFFFF), fmt.Sprintf("MOVT %s, 0x%X", branchReg, taken>>16))
		}
		p.code = append(p.code, code...)
		p.run(at)
		inner := p.snapshot()
		err := p.m.Step()
		next := p.m.Reg("PC")
		newSP := p.m.Reg("SP")
		ok := err == nil && newSP+0x400 >= uint64(sp) && newSP <= uint64(sp)+0x400 && bytes.Equal(m.regions[0].data[:len(p.code)], p.code)
		branch := hasRelative(d) || branchReg != ""
		if ok && branch {
			// The branch target, if taken, lies after the markers.
			ok = next == uint64(at+len(code)) || next == uint64(taken)
		} else if ok {
			ok = next == uint64(at+len(code))
		}
		if !ok {
			n := len(p.code)
			p.code = p.code[:caseStart]
			p.restore(snap)
			copy(m.regions[0].data[caseStart:], make([]byte, n-caseStart))
			continue
		}
		p.restore(inner)
		p.m.SetReg("PC", uint64(at))
		if branch {
			// Markers that a taken branch skips: each adds to R7.
			for p.pc() < taken {
				p.emit("ADDS R7, 0x1")
			}
		}
		p.dump()
		p.run(p.pc())
		cases = append(cases, testCase{text})
		armTested[d.Inst] = true
	}
	// The data area, then the end.
	p.load32("R1", armDataBase+armDataSize-0x10)
	p.load32("R0", armHandle)
	p.emit("LDR R0, dword [R0]", "STR R0, dword [R1]")
	p.load32("R0", armDataBase)
	p.emit("STR R0, dword [R1+0x4]")
	p.load32("R0", armDataSize-0x10)
	p.emit("STR R0, dword [R1+0x8]", "MOVS R0, 0x5", "BKPT 0xAB", "MOVS R0, 0x18")
	p.load32("R1", 0x20026) // ADP_Stopped_ApplicationExit
	p.emit("BKPT 0xAB")
	p.run(-1)
	got := p.out.Bytes()

	image := make([]byte, armCodeSize)
	copy(image, p.code)
	copy(image[armConstBase:], consts)
	dir := t.TempDir()
	imgFile := filepath.Join(dir, "test.bin")
	if err := os.WriteFile(imgFile, image, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, qemu, "-M", "mps2-an385", "-nographic", "-monitor", "none", "-serial", "none",
		"-semihosting-config", "enable=on,target=native", "-kernel", imgFile)
	var want bytes.Buffer
	cmd.Stdout = &want
	cmd.Run()
	if ctx.Err() != nil {
		if d := os.Getenv("ANLANG_QEMU_KEEP"); d != "" {
			os.WriteFile(filepath.Join(d, "test.bin"), image, 0o644)
		}
		t.Fatalf("QEMU did not finish; it wrote %d bytes", want.Len())
	}
	w := want.Bytes()
	recSize := 4 * len(armDumpRegs)
	if len(w) != len(got) {
		t.Errorf("QEMU wrote %d bytes, the interpreter %d", len(w), len(got))
	}
	failures := 0
	for i, c := range cases {
		if (i+1)*recSize > len(w) {
			t.Errorf("QEMU stopped before %s (record %d)", c.text, i)
			break
		}
		rg, rw := got[i*recSize:(i+1)*recSize], w[i*recSize:(i+1)*recSize]
		var diffs []string
		for k, name := range armDumpRegs {
			x, y := binary.LittleEndian.Uint32(rg[4*k:]), binary.LittleEndian.Uint32(rw[4*k:])
			if x != y {
				diffs = append(diffs, fmt.Sprintf("%s = %#x, QEMU %#x", name, x, y))
			}
		}
		if len(diffs) > 0 {
			failures++
			if failures <= 15 {
				t.Errorf("%s: %s", c.text, strings.Join(diffs, ", "))
			}
		}
	}
	tail := len(cases) * recSize
	if failures == 0 && len(w) == len(got) && !bytes.Equal(got[tail:], w[tail:]) {
		for i := tail; i < len(got); i++ {
			if got[i] != w[i] {
				t.Errorf("the data area differs first at %#x: %#x, QEMU %#x", armDataBase+i-tail, got[i], w[i])
				break
			}
		}
	}
	if failures > 15 {
		t.Errorf("%d of %d instructions differ", failures, len(cases))
	}
	return len(cases)
}

// armRandomEncoding returns a random encoding of instruction form in:
// random fields, and a random alternative of each fragment.
func armRandomEncoding(a *Arch, in *Instruction, rng *rand.Rand) []byte {
	vals := map[string]uint64{}
	alts := map[string]*Alternative{}
	altFields := map[string]map[string]uint64{}
	for i, o := range in.ops {
		name := opName(i)
		vals[name] = rng.Uint64()
		if o.Fragment != "" {
			f := a.fragments[o.Fragment]
			alt := &f.Alternatives[rng.Intn(len(f.Alternatives))]
			alts[name] = alt
			fields := map[string]uint64{}
			for _, it := range alt.pattern {
				if it.Kind == Field {
					fields[it.Name] = rng.Uint64()
				}
			}
			altFields[name] = fields
		}
	}
	code, err := a.encode(in.pattern, vals, alts, altFields)
	if err != nil {
		return nil
	}
	return code
}

func hasRelative(d *Decoded) bool {
	for _, o := range d.Inst.ops {
		if o.Relative {
			return true
		}
	}
	return false
}

// relativeText returns the text of a decoded branch with the target.
func relativeText(a *Arch, d *Decoded, target int) string {
	var ops []string
	for i, o := range d.Inst.ops {
		if o.Relative {
			ops = append(ops, fmt.Sprintf("0x%X", target))
			continue
		}
		ops = append(ops, a.operandText(d, i, o))
	}
	return d.Inst.Mnemonic + " " + strings.Join(ops, ", ")
}
