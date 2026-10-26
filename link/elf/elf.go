// Package elf writes ELF executables (spec/13-targets.md, §13.9).
package elf

import (
	"bytes"
	"encoding/binary"
)

// Machine is an ELF e_machine value.
type Machine uint16

const MachineX86_64 Machine = 62

const (
	headerSize   = 64
	phdrSize     = 56
	textOffset   = headerSize + phdrSize
	segmentAlign = 0x1000
)

// Exec describes a static executable with one loadable segment that
// holds the ELF headers, followed by Text. The segment is readable and
// executable; the program has no writable data yet.
type Exec struct {
	Machine Machine
	Base    uint64 // virtual address of the start of the file
	Text    []byte // code and read-only data
	Entry   int    // offset of the entry point in Text
}

// TextAddr returns the virtual address at which Text is loaded.
func (e *Exec) TextAddr() uint64 { return e.Base + textOffset }

// Bytes encodes the executable. The output depends only on the input
// (spec/22-compiler.md: reproducible builds).
func (e *Exec) Bytes() []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	total := uint64(textOffset + len(e.Text))

	// ELF header.
	b.Write([]byte{0x7F, 'E', 'L', 'F', 2 /* 64-bit */, 1 /* little endian */, 1 /* version */, 0 /* System V ABI */})
	b.Write(make([]byte, 8))                           // ABI version and padding
	binary.Write(&b, le, uint16(2))                    // e_type: ET_EXEC
	binary.Write(&b, le, uint16(e.Machine))            // e_machine
	binary.Write(&b, le, uint32(1))                    // e_version
	binary.Write(&b, le, e.TextAddr()+uint64(e.Entry)) // e_entry
	binary.Write(&b, le, uint64(headerSize))           // e_phoff
	binary.Write(&b, le, uint64(0))                    // e_shoff: no section headers
	binary.Write(&b, le, uint32(0))                    // e_flags
	binary.Write(&b, le, uint16(headerSize))           // e_ehsize
	binary.Write(&b, le, uint16(phdrSize))             // e_phentsize
	binary.Write(&b, le, uint16(1))                    // e_phnum
	binary.Write(&b, le, uint16(0))                    // e_shentsize
	binary.Write(&b, le, uint16(0))                    // e_shnum
	binary.Write(&b, le, uint16(0))                    // e_shstrndx

	// Program header: PT_LOAD of the whole file, R+X.
	binary.Write(&b, le, uint32(1))            // p_type: PT_LOAD
	binary.Write(&b, le, uint32(4|1))          // p_flags: PF_R | PF_X
	binary.Write(&b, le, uint64(0))            // p_offset
	binary.Write(&b, le, e.Base)               // p_vaddr
	binary.Write(&b, le, e.Base)               // p_paddr
	binary.Write(&b, le, total)                // p_filesz
	binary.Write(&b, le, total)                // p_memsz
	binary.Write(&b, le, uint64(segmentAlign)) // p_align

	b.Write(e.Text)
	return b.Bytes()
}

// MachineARM is the e_machine value of 32-bit ARM.
const MachineARM Machine = 40

// Segment is a loadable segment of an Image.
type Segment struct {
	Addr  uint32
	Data  []byte
	Size  int // the size in memory: at least len(Data); the rest is zero
	Write bool
	Exec  bool
}

// Image describes a 32-bit little-endian executable for a bare target:
// segments at fixed addresses and the entry point, without sections.
type Image struct {
	Machine  Machine
	Flags    uint32
	Entry    uint32
	Segments []Segment
}

// Bytes encodes the image.
func (im *Image) Bytes() []byte {
	const ehsize, phsize = 52, 32
	var b bytes.Buffer
	le := binary.LittleEndian
	n := len(im.Segments)
	data := ehsize + phsize*n // the offset of the contents of the first segment
	data = (data + 15) &^ 15
	b.Write([]byte{0x7F, 'E', 'L', 'F', 1 /* 32-bit */, 1 /* little endian */, 1 /* version */, 0 /* System V ABI */})
	b.Write(make([]byte, 8))
	binary.Write(&b, le, uint16(2)) // e_type: ET_EXEC
	binary.Write(&b, le, uint16(im.Machine))
	binary.Write(&b, le, uint32(1)) // e_version
	binary.Write(&b, le, im.Entry)
	binary.Write(&b, le, uint32(ehsize)) // e_phoff
	binary.Write(&b, le, uint32(0))      // e_shoff: no section headers
	binary.Write(&b, le, im.Flags)
	binary.Write(&b, le, uint16(ehsize))
	binary.Write(&b, le, uint16(phsize))
	binary.Write(&b, le, uint16(n))
	binary.Write(&b, le, uint16(0)) // e_shentsize
	binary.Write(&b, le, uint16(0)) // e_shnum
	binary.Write(&b, le, uint16(0)) // e_shstrndx
	off := data
	for _, s := range im.Segments {
		flags := uint32(4) // PF_R
		if s.Write {
			flags |= 2
		}
		if s.Exec {
			flags |= 1
		}
		binary.Write(&b, le, uint32(1)) // p_type: PT_LOAD
		binary.Write(&b, le, uint32(off))
		binary.Write(&b, le, s.Addr) // p_vaddr
		binary.Write(&b, le, s.Addr) // p_paddr
		binary.Write(&b, le, uint32(len(s.Data)))
		binary.Write(&b, le, uint32(max(s.Size, len(s.Data))))
		binary.Write(&b, le, flags)
		binary.Write(&b, le, uint32(4)) // p_align
		off += (len(s.Data) + 3) &^ 3
	}
	b.Write(make([]byte, data-b.Len()))
	for _, s := range im.Segments {
		b.Write(s.Data)
		b.Write(make([]byte, (4-len(s.Data)%4)%4))
	}
	return b.Bytes()
}
