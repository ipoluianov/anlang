package elf

import (
	"bytes"
	"debug/elf"
	"testing"
)

// The Go standard library's ELF reader is the oracle for the header.
func TestExecHeaders(t *testing.T) {
	text := []byte{0x90, 0x90, 0xC3}
	e := &Exec{Machine: MachineX86_64, Base: 0x400000, Text: text, Entry: 1}
	f, err := elf.NewFile(bytes.NewReader(e.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Type != elf.ET_EXEC || f.Machine != elf.EM_X86_64 {
		t.Errorf("header %+v", f.FileHeader)
	}
	if f.Entry != 0x400000+textOffset+1 {
		t.Errorf("entry %#x", f.Entry)
	}
	if len(f.Progs) != 1 {
		t.Fatalf("%d program headers, want 1", len(f.Progs))
	}
	p := f.Progs[0]
	if p.Type != elf.PT_LOAD || p.Flags != elf.PF_R|elf.PF_X || p.Vaddr != 0x400000 || p.Filesz != uint64(textOffset+len(text)) {
		t.Errorf("program header %+v", p.ProgHeader)
	}
	data := make([]byte, len(text))
	if _, err := p.ReadAt(data, textOffset); err != nil || !bytes.Equal(data, text) {
		t.Errorf("text %X, err %v", data, err)
	}
}
