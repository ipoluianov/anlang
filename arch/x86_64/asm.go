// Package x86_64 generates x86-64 machine code (spec/13-targets.md).
//
// For now the instruction encodings are written by hand. From roadmap
// step 26 they are generated from the architecture description.
package x86_64

import (
	"encoding/binary"
	"fmt"
)

// Asm assembles one section of code and read-only data. Labels may be
// used before they are defined; rel32 references are patched by Finish.
type Asm struct {
	buf    []byte
	labels map[string]int
	fixups []fixup
}

type fixup struct {
	at    int // offset of the rel32 field
	end   int // offset of the end of the instruction
	label string
}

// NewAsm returns an empty assembler.
func NewAsm() *Asm { return &Asm{labels: map[string]int{}} }

// Len returns the current offset.
func (a *Asm) Len() int { return len(a.buf) }

// Label defines label at the current offset.
func (a *Asm) Label(name string) {
	if _, dup := a.labels[name]; dup {
		panic("x86_64: duplicate label " + name)
	}
	a.labels[name] = len(a.buf)
}

// Bytes appends raw bytes.
func (a *Asm) Bytes(b ...byte) { a.buf = append(a.buf, b...) }

func (a *Asm) imm32(v uint32) { a.buf = binary.LittleEndian.AppendUint32(a.buf, v) }

// rel32 appends a 32-bit displacement to label, relative to the end of
// the instruction, which ends tail bytes after the field.
func (a *Asm) rel32(label string, tail int) {
	at := len(a.buf)
	a.imm32(0)
	a.fixups = append(a.fixups, fixup{at: at, end: at + 4 + tail, label: label})
}

// Reg is a general-purpose register number as used in ModRM encoding.
type Reg byte

const (
	RAX Reg = 0
	RCX Reg = 1
	RDX Reg = 2
	RBX Reg = 3
	RSP Reg = 4
	RBP Reg = 5
	RSI Reg = 6
	RDI Reg = 7
)

// MovImm32 emits mov r32, imm32; the upper half of the register is zeroed.
func (a *Asm) MovImm32(r Reg, v uint32) {
	a.Bytes(0xB8 + byte(r))
	a.imm32(v)
}

// LeaRIP emits lea r64, [rip+label].
func (a *Asm) LeaRIP(r Reg, label string) {
	a.Bytes(0x48, 0x8D, 0x05|byte(r)<<3)
	a.rel32(label, 0)
}

// Test64 emits test r64, r64 with the same register twice.
func (a *Asm) Test64(r Reg) { a.Bytes(0x48, 0x85, 0xC0|byte(r)<<3|byte(r)) }

// CmpImm8 emits cmp r64, imm8 (sign-extended).
func (a *Asm) CmpImm8(r Reg, v int8) { a.Bytes(0x48, 0x83, 0xF8|byte(r), byte(v)) }

// Add64 emits add dst, src.
func (a *Asm) Add64(dst, src Reg) { a.Bytes(0x48, 0x01, 0xC0|byte(src)<<3|byte(dst)) }

// Sub64 emits sub dst, src.
func (a *Asm) Sub64(dst, src Reg) { a.Bytes(0x48, 0x29, 0xC0|byte(src)<<3|byte(dst)) }

// Syscall emits syscall.
func (a *Asm) Syscall() { a.Bytes(0x0F, 0x05) }

// Ret emits ret.
func (a *Asm) Ret() { a.Bytes(0xC3) }

// Call emits call rel32 to label.
func (a *Asm) Call(label string) {
	a.Bytes(0xE8)
	a.rel32(label, 0)
}

// Jmp emits jmp rel32 to label.
func (a *Asm) Jmp(label string) {
	a.Bytes(0xE9)
	a.rel32(label, 0)
}

// Cond is a condition code for Jcc.
type Cond byte

const (
	CondE  Cond = 0x4 // equal / zero
	CondNE Cond = 0x5
	CondLE Cond = 0xE // signed less or equal
)

// Jcc emits a conditional jump rel32 to label.
func (a *Asm) Jcc(c Cond, label string) {
	a.Bytes(0x0F, 0x80|byte(c))
	a.rel32(label, 0)
}

// Finish resolves all label references and returns the section.
func (a *Asm) Finish() ([]byte, error) {
	for _, f := range a.fixups {
		target, ok := a.labels[f.label]
		if !ok {
			return nil, fmt.Errorf("x86_64: undefined label %s", f.label)
		}
		binary.LittleEndian.PutUint32(a.buf[f.at:], uint32(int32(target-f.end)))
	}
	return a.buf, nil
}

// Offset returns the offset of a defined label.
func (a *Asm) Offset(label string) (int, bool) {
	off, ok := a.labels[label]
	return off, ok
}
