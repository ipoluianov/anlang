package desc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The comparison of the 8086 interpreter with QEMU on random
// instructions (spec/24-roadmap.md, step 12). A disk image holds a
// program, assembled with the assembler derived from the description,
// that fills memory, then for each random instruction sets the
// registers, executes the instruction and writes all registers to the
// debug port 0xE9, and at the end writes checksums of memory. QEMU boots
// the image; the interpreter runs the same program; their outputs must
// agree, except for the flags that the 8086 leaves undefined.
// scripts/fetch-qemu.sh downloads QEMU into external/qemu; the test is
// skipped without it.
//
// QEMU emulates a later processor than the 8086, so the random
// instructions leave out what differs between them: control transfers,
// interrupts, input and output, encodings that only the 8086 accepts,
// the LOCK prefix (an invalid opcode on
// later processors with most instructions), division (whose errors call interrupt
// handlers), the decimal adjustments, PUSH SP, loads of CS and SS, shift
// counts above 7, words at offset FFFF, and bits 12 to 15 of FLAGS,
// which PUSHF stores.

const (
	qemuCodeSeg = 0x1000
	qemuDataSeg = 0x3000
	qemuStack   = 0x4000
	qemuExtra   = 0x5000
)

// asmAt assembles text for address pc: relative targets are absolute.
func asmAt(t *testing.T, a *Arch, text string, pc int) []byte {
	t.Helper()
	// The assembler works at address 0; shift a numeric target.
	fields := strings.Fields(text)
	if len(fields) == 2 && (strings.HasPrefix(fields[0], "J") || strings.HasPrefix(fields[0], "LOOP") || fields[0] == "CALL") && !strings.Contains(fields[1], ":") {
		var target int
		fmt.Sscanf(fields[1], "0x%x", &target)
		text = fmt.Sprintf("%s 0x%X", fields[0], (target-pc)&0xFFFF)
	}
	code, err := a.Asm(text)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

type qemuProg struct {
	t    *testing.T
	a    *Arch
	code []byte
}

func (p *qemuProg) asm(lines ...string) {
	for _, l := range lines {
		p.code = append(p.code, asmAt(p.t, p.a, l, len(p.code))...)
	}
}

func (p *qemuProg) pc() int { return len(p.code) }

var dumpRegs = []string{"BX", "CX", "DX", "SI", "DI", "BP", "SP", "DS", "ES", "SS"}

// qemuMasks returns the masks of the defined flags of the opcodes, from
// the metadata of the 8086 processor test suite.
func qemuMasks(t *testing.T) map[string]uint64 {
	data, err := os.ReadFile(filepath.Join("..", "..", "external", "8086", "metadata.json"))
	if err != nil {
		t.Skip("the metadata of the 8086 processor tests is missing: run scripts/fetch-8086-tests.sh")
	}
	var meta sstMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	masks := map[string]uint64{}
	for op, info := range meta.Opcodes {
		masks[op] = info.FlagsMask
		for r, ri := range info.Reg {
			masks[op+"."+r] = ri.FlagsMask
		}
	}
	return masks
}

// excludedMnemonics are the instructions that the random tests leave out.
var excludedMnemonics = map[string]bool{
	"JMP": true, "CALL": true, "RET": true, "RETF": true, "INT": true, "INTO": true, "IRET": true,
	"HLT": true, "PUSHF": true, "IN": true, "OUT": true, "STI": true, "POPF": true, "DIV": true, "IDIV": true,
	"AAM": true, "DAA": true, "DAS": true, "AAA": true, "AAS": true, "LOOP": true, "LOOPE": true,
	"LOOPNE": true, "JCXZ": true,
}

func allowed(a *Arch, d *Decoded, code []byte) bool {
	in := d.Inst
	if excludedMnemonics[in.Mnemonic] || strings.HasPrefix(in.Mnemonic, "J") {
		return false
	}
	text := a.Disasm(d)
	switch {
	case strings.Contains(text, "LOCK") || strings.Contains(text, "PUSH SP") || strings.Contains(text, "POP SS") || strings.Contains(text, "MOV SS") ||
		strings.Contains(text, "MOV CS") || strings.Contains(text, "POP CS"):
		return false
	}
	// The 8086 ignores the reg field of POP r/m and MOV r/m, imm, and
	// takes it modulo 4 in moves to and from segment registers; later
	// processors reject those encodings or name FS and GS. LDS and LES
	// with a register operand are undefined.
	op, modrm := code[len(d.Prefixes)], code[len(d.Prefixes)+1]
	switch {
	case (op == 0x8F || op == 0xC6 || op == 0xC7) && modrm>>3&7 != 0,
		(op == 0x8C || op == 0x8E) && modrm>>3&7 > 3,
		(op == 0xC4 || op == 0xC5) && modrm>>6 == 3:
		return false
	}
	// Displacements and addresses small enough that no word lies at
	// offset FFFF.
	for k, v := range d.Fields {
		if (strings.HasSuffix(k, ".disp") || strings.HasSuffix(k, ".addr")) && v > 0x7FFE && v > 0xFF {
			return false
		}
	}
	return true
}

func TestI8086QEMU(t *testing.T) {
	root := filepath.Join("..", "..", "external", "qemu", "root")
	qemu := filepath.Join(root, "usr", "bin", "qemu-system-i386")
	if _, err := os.Stat(qemu); err != nil {
		t.Skip("QEMU is not downloaded: run scripts/fetch-qemu.sh")
	}
	masks := qemuMasks(t)
	a := loadI8086(t)
	rng := rand.New(rand.NewSource(1))
	images := 4
	if testing.Short() {
		images = 1
	}
	total := 0
	for img := 0; img < images; img++ {
		total += qemuImage(t, a, rng, masks, qemu, root)
	}
	t.Logf("%d random instructions agree with QEMU", total)
}

// qemuImage builds one image, runs it in QEMU and in the interpreter, and
// compares the outputs. It returns the number of instructions tested.
func qemuImage(t *testing.T, a *Arch, rng *rand.Rand, masks map[string]uint64, qemu, root string) int {
	p := &qemuProg{t: t, a: a}
	// Fill the data, stack and extra segments with a pattern.
	p.asm("CLD")
	for _, seg := range []int{qemuDataSeg, qemuStack, qemuExtra} {
		p.asm(fmt.Sprintf("MOV AX, 0x%X", seg), "MOV ES, AX", "XOR DI, DI", "MOV CX, 0x8000", fmt.Sprintf("MOV BX, 0x%X", seg^0x9E37))
		loop := p.pc()
		p.asm("MOV AX, BX", "ADD BX, 0x6D2B", "ROL BX, 1", "XOR AX, BX", "STOSW", fmt.Sprintf("LOOP 0x%X", loop))
	}
	// The dump routine: AX first, then FLAGS and the other registers.
	jumpOver := p.pc()
	p.asm("JMP 0x8000") // a near jump, patched below
	dump := p.pc()
	out := func() { p.asm("OUT 0xE9, AL", "XCHG AL, AH", "OUT 0xE9, AL", "XCHG AL, AH") }
	out()
	p.asm("PUSHF", "POP AX")
	out()
	// The undefined flags differ between the processors: overwrite the
	// stack word that PUSHF wrote.
	p.asm("XOR AX, AX", "PUSH AX", "POP AX")
	for _, r := range dumpRegs {
		p.asm("MOV AX, " + r)
		out()
	}
	p.asm("RET")
	after := p.pc()
	patch := asmAt(t, a, fmt.Sprintf("JMP 0x%X", after), jumpOver)
	if len(patch) == 2 {
		patch = append(patch, 0x90) // NOP
	}
	copy(p.code[jumpOver:], patch)

	type testCase struct {
		text string
		mask uint64
	}
	var cases []testCase
	for p.pc() < 62000 {
		// A random allowed instruction.
		var code []byte
		var d *Decoded
		for {
			code = make([]byte, 8)
			rng.Read(code)
			if rng.Intn(5) == 0 {
				// Not CS:, through which an instruction could change the program.
				code = append([]byte{[]byte{0x26, 0x36, 0x3E}[rng.Intn(3)]}, code...)
			}
			var amb []*Instruction
			d, amb = a.Decode(code)
			if d != nil && len(amb) == 0 && allowed(a, d, code) {
				break
			}
		}
		code = code[:d.Length]
		op := fmt.Sprintf("%02X", code[len(d.Prefixes)])
		mask := masks[op]
		if d.Length > len(d.Prefixes)+1 {
			if r, ok := masks[fmt.Sprintf("%s.%d", op, code[len(d.Prefixes)+1]>>3&7)]; ok {
				mask = r
			}
		}
		if mask == 0 {
			mask = 0xFFFF
		}
		mask &= 0x0FD5 // bits 12 to 15 differ between the 8086 and later processors
		text := a.Disasm(d)
		isString := strings.HasSuffix(d.Inst.Mnemonic, "SB") || strings.HasSuffix(d.Inst.Mnemonic, "SW")
		reg := func() uint64 { return uint64(0x100 + rng.Intn(0x3E00)) }
		cx := uint64(rng.Intn(0x10000))
		if isString {
			cx &= 0x3F
		}
		if strings.Contains(text, ", CL") {
			cx = cx&0xFF00 | uint64(rng.Intn(8))
		}
		p.asm(fmt.Sprintf("MOV AX, 0x%X", qemuDataSeg), "MOV DS, AX",
			fmt.Sprintf("MOV AX, 0x%X", qemuExtra), "MOV ES, AX",
			fmt.Sprintf("MOV AX, 0x%X", qemuStack), "MOV SS, AX",
			fmt.Sprintf("MOV SP, 0x%X", 0x2000+2*rng.Intn(0x6000)),
			fmt.Sprintf("MOV AX, 0x%X", rng.Intn(0x10000)&0x0CD5), "PUSH AX", "POPF",
			fmt.Sprintf("MOV BX, 0x%X", reg()), fmt.Sprintf("MOV CX, 0x%X", cx),
			fmt.Sprintf("MOV DX, 0x%X", rng.Intn(0x10000)), fmt.Sprintf("MOV SI, 0x%X", reg()),
			fmt.Sprintf("MOV DI, 0x%X", reg()), fmt.Sprintf("MOV BP, 0x%X", reg()),
			fmt.Sprintf("MOV AX, 0x%X", rng.Intn(0x10000)))
		p.code = append(p.code, code...)
		p.code = append(p.code, asmAt(t, a, fmt.Sprintf("CALL 0x%X", dump), p.pc())...)
		cases = append(cases, testCase{text, mask})
	}
	// Checksums of the segments, then the end.
	for _, seg := range []int{qemuDataSeg, qemuStack, qemuExtra} {
		p.asm(fmt.Sprintf("MOV AX, 0x%X", seg), "MOV DS, AX", "XOR SI, SI", "XOR BX, BX", "MOV CX, 0x8000")
		loop := p.pc()
		p.asm("LODSW", "ADD BX, AX", "ROL BX, 1", fmt.Sprintf("LOOP 0x%X", loop), "MOV AX, BX")
		out()
	}
	p.asm("MOV AL, 0x0", "OUT 0xF4, AL", "HLT")
	if len(p.code) > 127*512 {
		t.Fatalf("the program is %d bytes", len(p.code))
	}

	// The boot sector loads the program to 1000:0000 and runs it. The
	// BIOS needs interrupts while it reads the disk; the program runs
	// with them disabled, so that no handler writes to its stack.
	nsect := (len(p.code) + 511) / 512
	boot := &qemuProg{t: t, a: a}
	boot.asm("CLI", "XOR AX, AX", "MOV DS, AX", "MOV SS, AX", "MOV SP, 0x7C00", "MOV SI, 0x7C40", "MOV AH, 0x42",
		"STI", "INT 0x13", "CLI", fmt.Sprintf("JMP 0x%X:0x0", qemuCodeSeg))
	sector := make([]byte, 512)
	copy(sector, boot.code)
	dap := []byte{0x10, 0, byte(nsect), 0, 0, 0, byte(qemuCodeSeg & 0xFF), byte(qemuCodeSeg >> 8), 1, 0, 0, 0, 0, 0, 0, 0}
	copy(sector[0x40:], dap)
	sector[510], sector[511] = 0x55, 0xAA
	image := append(sector, p.code...)
	image = append(image, make([]byte, nsect*512+512-len(image))...)
	dir := t.TempDir()
	imgFile := filepath.Join(dir, "test.img")
	outFile := filepath.Join(dir, "out.bin")
	if err := os.WriteFile(imgFile, image, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, qemu, "-L", filepath.Join(root, "usr", "share", "seabios"), "-L", filepath.Join(root, "usr", "share", "qemu"),
		"-nographic", "-display", "none", "-monitor", "none", "-serial", "none",
		"-debugcon", "file:"+outFile, "-device", "isa-debug-exit,iobase=0xf4,iosize=0x04",
		"-drive", "format=raw,file="+imgFile, "-no-reboot")
	cmd.Run() // isa-debug-exit makes QEMU exit with status 1
	if ctx.Err() != nil {
		out, _ := os.ReadFile(outFile)
		if d := os.Getenv("ANLANG_QEMU_KEEP"); d != "" {
			os.WriteFile(filepath.Join(d, "test.img"), image, 0o644)
		}
		t.Fatalf("QEMU did not finish; it wrote %d bytes", len(out))
	}
	want, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}

	// The interpreter, from the state after the boot sector.
	m := NewMachine(a)
	copy(m.Mem[qemuCodeSeg<<4:], p.code)
	m.SetReg("CS", qemuCodeSeg)
	m.SetReg("IP", 0)
	var got bytes.Buffer
	done := false
	m.Out = func(port uint64, width int, v uint64) {
		switch port {
		case 0xE9:
			got.WriteByte(byte(v))
		case 0xF4:
			done = true
		}
	}
	var stepErr error
	for steps := 0; !done && stepErr == nil; steps++ {
		stepErr = m.Step()
		if steps > 20_000_000 {
			stepErr = fmt.Errorf("the program does not end in the interpreter")
		}
	}
	// On an error, the records written so far show where the two differ.
	recSize := 2 * (2 + len(dumpRegs))
	g, w := got.Bytes(), want
	if stepErr != nil {
		t.Errorf("after %d records: %v", len(g)/recSize, stepErr)
		n := min(len(g), len(w)) / recSize * recSize
		g, w, cases = g[:n], w[:n], cases[:n/recSize]
	} else if len(w) != len(g) {
		t.Fatalf("QEMU wrote %d bytes, the interpreter %d", len(w), len(g))
	}
	failures := 0
	for i, c := range cases {
		rg, rw := g[i*recSize:(i+1)*recSize], w[i*recSize:(i+1)*recSize]
		word := func(b []byte, k int) uint64 { return uint64(b[2*k]) | uint64(b[2*k+1])<<8 }
		names := append([]string{"AX", "FLAGS"}, dumpRegs...)
		var diffs []string
		for k, name := range names {
			x, y := word(rg, k), word(rw, k)
			if name == "FLAGS" {
				x, y = x&c.mask, y&c.mask
			}
			if x != y {
				diffs = append(diffs, fmt.Sprintf("%s = %#x, QEMU %#x", name, x, y))
			}
		}
		if len(diffs) > 0 {
			failures++
			if failures <= 10 {
				t.Errorf("%s: %s", c.text, strings.Join(diffs, ", "))
			}
		}
	}
	tail := len(cases) * recSize
	if stepErr == nil && !bytes.Equal(g[tail:], w[tail:]) {
		t.Errorf("memory checksums differ: % X, QEMU % X", g[tail:], w[tail:])
	}
	if failures > 10 {
		t.Errorf("%d of %d instructions differ", failures, len(cases))
	}
	return len(cases)
}
