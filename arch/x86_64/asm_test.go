package x86_64

import (
	"bytes"
	"testing"
)

// The expected encodings are from the Intel SDM and agree with GNU as.
func TestEncodings(t *testing.T) {
	tests := []struct {
		name string
		emit func(a *Asm)
		want []byte
	}{
		{"mov eax, 1", func(a *Asm) { a.MovImm32(RAX, 1) }, []byte{0xB8, 1, 0, 0, 0}},
		{"mov edi, 2", func(a *Asm) { a.MovImm32(RDI, 2) }, []byte{0xBF, 2, 0, 0, 0}},
		{"mov edx, 0x1234", func(a *Asm) { a.MovImm32(RDX, 0x1234) }, []byte{0xBA, 0x34, 0x12, 0, 0}},
		{"test rax, rax", func(a *Asm) { a.Test64(RAX) }, []byte{0x48, 0x85, 0xC0}},
		{"test rdx, rdx", func(a *Asm) { a.Test64(RDX) }, []byte{0x48, 0x85, 0xD2}},
		{"cmp rax, -4", func(a *Asm) { a.CmpImm8(RAX, -4) }, []byte{0x48, 0x83, 0xF8, 0xFC}},
		{"add rsi, rax", func(a *Asm) { a.Add64(RSI, RAX) }, []byte{0x48, 0x01, 0xC6}},
		{"sub rdx, rax", func(a *Asm) { a.Sub64(RDX, RAX) }, []byte{0x48, 0x29, 0xC2}},
		{"syscall", func(a *Asm) { a.Syscall() }, []byte{0x0F, 0x05}},
		{"ret", func(a *Asm) { a.Ret() }, []byte{0xC3}},
	}
	for _, tt := range tests {
		a := NewAsm()
		tt.emit(a)
		got, err := a.Finish()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, tt.want) {
			t.Errorf("%s: got % X, want % X", tt.name, got, tt.want)
		}
	}
}

func TestRelocations(t *testing.T) {
	a := NewAsm()
	a.Label("start")
	a.Call("target")      // E8 rel32, 5 bytes
	a.Jcc(CondE, "start") // 0F 84 rel32, 6 bytes
	a.LeaRIP(RSI, "data") // 48 8D 35 rel32, 7 bytes
	a.Label("target")
	a.Ret()
	a.Label("data")
	a.Bytes('x')
	got, err := a.Finish()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0xE8, 13, 0, 0, 0, // target at 18, next instruction at 5
		0x0F, 0x84, 0xF5, 0xFF, 0xFF, 0xFF, // start at 0, next at 11: -11
		0x48, 0x8D, 0x35, 1, 0, 0, 0, // data at 19, next at 18
		0xC3,
		'x',
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got  % X\nwant % X", got, want)
	}
}

func TestUndefinedLabel(t *testing.T) {
	a := NewAsm()
	a.Call("nowhere")
	if _, err := a.Finish(); err == nil {
		t.Error("no error for undefined label")
	}
}
