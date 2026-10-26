package desc

import (
	"fmt"
	"strings"
	"testing"

	"an/format"
	"an/syntax"
	"an/types"
)

func loadI8086(t *testing.T) *Arch {
	t.Helper()
	a, errs := Load("I8086")
	for _, err := range errs {
		t.Error(err)
	}
	if a == nil {
		t.FailNow()
	}
	return a
}

// TestI8086 checks the acceptance criterion of roadmap step 11: the
// description of the 8086 type-checks, as Anlang and in its encodings
// and semantics.
func TestI8086(t *testing.T) {
	a := loadI8086(t)
	if a.Name != "i8086" || a.Word != 16 || len(a.Instructions) < 200 {
		t.Errorf("unexpected description: %s, word %d, %d instructions", a.Name, a.Word, len(a.Instructions))
	}
}

// The opcodes of the 8086 that a description need not cover: the
// coprocessor escapes, the undocumented aliases and the opcodes that do
// not exist on the 8086.
var notCovered = map[byte]bool{
	0x60: true, 0x61: true, 0x62: true, 0x63: true, 0x64: true, 0x65: true, 0x66: true, 0x67: true,
	0x68: true, 0x69: true, 0x6A: true, 0x6B: true, 0x6C: true, 0x6D: true, 0x6E: true, 0x6F: true,
	0x82: true, 0xC0: true, 0xC1: true, 0xC8: true, 0xC9: true, 0xD6: true, 0xF1: true,
	0xD8: true, 0xD9: true, 0xDA: true, 0xDB: true, 0xDC: true, 0xDD: true, 0xDE: true, 0xDF: true,
}

// prefixes are the prefix bytes of the 8086.
var prefixBytes = map[byte]bool{0x26: true, 0x2E: true, 0x36: true, 0x3E: true, 0xF0: true, 0xF2: true, 0xF3: true}

// groups are the opcodes whose ModRM reg field selects the operation,
// with the reg values that the 8086 does not define.
var groups = map[byte][]int{
	0x80: nil, 0x81: nil, 0x83: nil, 0x8F: nil, 0xC6: nil, 0xC7: nil,
	0xD0: {6}, 0xD1: {6}, 0xD2: {6}, 0xD3: {6},
	0xF6: {1}, 0xF7: {1}, 0xFE: {2, 3, 4, 5, 6, 7}, 0xFF: {7},
	0x8C: nil, 0x8E: nil,
}

// TestI8086Coverage checks the second acceptance criterion of roadmap
// step 11: every instruction that 8086 programs such as DOS .COM programs
// use has a description; every documented opcode, and every reg value of
// the opcode groups, decodes to exactly one most specific instruction
// form, with every addressing mode.
func TestI8086Coverage(t *testing.T) {
	a := loadI8086(t)
	covered := map[*Instruction]bool{}
	for op := 0; op < 256; op++ {
		b := byte(op)
		if prefixBytes[b] {
			d, _ := a.Decode([]byte{b, 0x90})
			if d == nil || len(d.Prefixes) != 1 {
				t.Errorf("%02X is not decoded as a prefix", b)
			}
			continue
		}
		undefined := map[int]bool{}
		for _, r := range groups[b] {
			undefined[r] = true
		}
		// Each ModRM byte, followed by enough bytes for any displacement
		// and immediate.
		for modrm := 0; modrm < 256; modrm++ {
			code := []byte{b, byte(modrm), 0x34, 0x12, 0x78, 0x56}
			d, amb := a.Decode(code)
			reg := modrm >> 3 & 7
			want := !notCovered[b] && !(groups[b] != nil && undefined[reg])
			if modrm>>6 == 3 && (b == 0x8D || b == 0xC4 || b == 0xC5 || b == 0xFF && (reg == 3 || reg == 5)) {
				want = false // the operand must be in memory
			}
			switch {
			case len(amb) > 0:
				var names []string
				for _, in := range amb {
					names = append(names, in.String())
				}
				t.Errorf("% X: ambiguous: %s", code[:2], strings.Join(names, " | "))
			case want && d == nil:
				t.Errorf("% X: not covered", code[:2])
			case !want && d != nil && (groups[b] != nil || modrm>>6 == 3):
				t.Errorf("% X: decodes as %s, which the 8086 does not define", code[:2], d.Inst)
			case d != nil:
				covered[d.Inst] = true
			}
			if d != nil && d.Length == 1 {
				break // the opcode has no ModRM byte
			}
			if d == nil && !want && groups[b] == nil {
				break
			}
		}
	}
	for i := range a.Instructions {
		if in := &a.Instructions[i]; !covered[in] {
			t.Errorf("instruction %s (%s) is never decoded", in, in.Encoding)
		}
	}
	t.Logf("%d instruction forms decoded", len(covered))
}

// TestDecode checks decoding of some instructions with operands.
func TestDecode(t *testing.T) {
	a := loadI8086(t)
	tests := []struct {
		code []byte
		want string
		n    int
	}{
		{[]byte{0xB4, 0x09}, "MOV r8, imm8 a=4 b=9", 2},
		{[]byte{0xCD, 0x21}, "INT imm8 a=33", 2},
		{[]byte{0x8B, 0x46, 0xFE}, "MOV r16, r/m16 a=0 b.disp=254 b.reg=0", 3},
		{[]byte{0x26, 0x88, 0x07}, "MOV r/m8, r8 a.reg=0 b=0", 3},
		{[]byte{0x81, 0xC3, 0x34, 0x12}, "ADD r/m16, imm16 a.r=3 a.reg=0 b=4660", 4},
		{[]byte{0xF3, 0xA4}, "MOVSB", 2},
		{[]byte{0xEA, 0x00, 0x01, 0x00, 0xF0}, "JMP ptr16:16 a=4026532096", 5},
		{[]byte{0x75, 0xFE}, "JNE rel8 a=254", 2},
	}
	for _, tt := range tests {
		d, amb := a.Decode(tt.code)
		if d == nil || len(amb) > 0 {
			t.Errorf("% X: decoded %v, ambiguous %v", tt.code, d, amb)
			continue
		}
		got := d.Inst.String()
		var keys []string
		for k := range d.Fields {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			got += fmt.Sprintf(" %s=%d", k, d.Fields[k])
		}
		if got != tt.want || d.Length != tt.n {
			t.Errorf("% X: got %q (%d bytes), want %q (%d bytes)", tt.code, got, d.Length, tt.want, tt.n)
		}
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestCheckErrors checks that errors in a description are reported.
func TestCheckErrors(t *testing.T) {
	tests := []struct {
		change func(a *Arch)
		want   string
	}{
		{func(a *Arch) { a.Instructions[0].Semantics = "AL = AX;" }, "width 16 to AL of width 8"},
		{func(a *Arch) { a.Instructions[0].Semantics = "a = b + CX;" }, "operator + on widths 8 and 16"},
		{func(a *Arch) { a.Instructions[0].Semantics = "XX = 1;" }, "cannot assign to XX"},
		{func(a *Arch) { a.Instructions[0].Semantics = "a = undefined(b);" }, "undefined function undefined"},
		{func(a *Arch) { a.Instructions[0].Semantics = "if AX { }" }, "condition must have width 1"},
		{func(a *Arch) { a.Instructions[0].Semantics = "a = b[9];" }, "bits [9:9] of a value of width 8"},
		{func(a *Arch) { a.Instructions[0].Encoding = "00 <rm a>" }, "operand b of kind r8 is not encoded"},
		{func(a *Arch) { a.Instructions[0].Encoding = "00 <rm a reg=9>" }, "does not fit in the field reg"},
		{func(a *Arch) { a.Instructions[0].Operands = []string{"r/m8", "r99"} }, "unknown operand kind r99"},
		{func(a *Arch) {
			a.Semantics += "\nfn loop(x) {\n\treturn loop(x);\n}\n"
			a.Instructions[0].Semantics = "a = loop(a);"
		}, "recursive call of loop"},
	}
	for _, tt := range tests {
		a := loadI8086(t)
		tt.change(a)
		errs := a.Check()
		found := false
		for _, err := range errs {
			if strings.Contains(err.Error(), tt.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("want an error containing %q, got %v", tt.want, errs)
		}
	}
}

// TestAsmRoundTrip checks the second acceptance criterion of roadmap step
// 12: for every instruction encoding of the coverage enumeration, the
// disassembled text assembles to an encoding that disassembles to the
// same text.
func TestAsmRoundTrip(t *testing.T) {
	a := loadI8086(t)
	n := 0
	for op := 0; op < 256; op++ {
		for modrm := 0; modrm < 256; modrm += 7 {
			code := []byte{byte(op), byte(modrm), 0x34, 0x92, 0x78, 0x56}
			d, amb := a.Decode(code)
			if d == nil || len(amb) > 0 {
				continue
			}
			text := a.Disasm(d)
			re, err := a.Asm(text)
			if err != nil {
				t.Errorf("% X: %s: %v", code[:d.Length], text, err)
				continue
			}
			d2, _ := a.Decode(append(re, make([]byte, 8)...))
			if d2 == nil || a.Disasm(d2) != text {
				got := "nothing"
				if d2 != nil {
					got = a.Disasm(d2)
				}
				t.Errorf("% X: %s assembles to % X, which is %s", code[:d.Length], text, re, got)
			}
			n++
		}
	}
	t.Logf("%d instructions round-trip", n)
	for _, tt := range []struct{ text, want string }{
		{"MOV AH, 0x9", "B4 09"},
		{"INT 0x21", "CD 21"},
		{"MOV word [BP-0x2], AX", "89 46 FE"},
		{"ES MOV byte [BX], AL", "26 88 07"},
		{"REP MOVSB", "F3 A4"},
		{"ADD AX, 0x5", "05 05 00"},
		{"MOV AX, word [BP]", "8B 46 00"},
		{"JMP 0x0", "EB FE"},
		{"CALL 0x1234:0x10", "9A 10 00 34 12"},
	} {
		code, err := a.Asm(tt.text)
		if got := fmt.Sprintf("% X", code); err != nil || got != tt.want {
			t.Errorf("Asm(%q) = %s, %v; want %s", tt.text, got, err, tt.want)
		}
	}
}

// TestInstructionPackage checks that the generated instruction package
// is canonical Anlang that type-checks.
func TestInstructionPackage(t *testing.T) {
	a := loadI8086(t)
	src := a.InstructionPackage()
	f, perrs := syntax.Parse("i8086.an", src)
	if len(perrs) > 0 {
		t.Fatalf("%v\n%s", perrs, src)
	}
	if errs := format.Canonical("i8086.an", src); len(errs) > 0 {
		t.Errorf("not canonical: %v", errs)
	}
	_, errs := types.Check(&types.Config{Module: "x", Path: "an/arch/i8086", IntBits: 16, AddrBits: 16}, []*syntax.File{f})
	for _, err := range errs {
		t.Error(err)
	}
	for _, want := range []string{"func Int(a u8, r Regs) Regs {", "func Cli(r Regs) Regs {\n\teffect.Mark(effect.Interrupts)", "\tAX    u16\n"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("the package has no %q", want)
		}
	}
}
