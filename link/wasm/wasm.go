// Package wasm writes WebAssembly modules (target.format.wasm): the
// functions of a structured image, the imports of the platform, a table
// of all the functions, the memory with the data, and the globals of the
// stack.
package wasm

// Import is an imported function: its module, name and the widths in
// bits of its parameters and results.
type Import struct {
	Module, Name    string
	Params, Results []int
}

// Module is the contents of a module.
type Module struct {
	Imports []Import
	// Funcs are the bodies of the functions, of type [] → [], without
	// their locals and their final end.
	Funcs [][]byte
	// Entry is the index of the entry function in Funcs, exported as
	// _start.
	Entry int
	// Data are the bytes of memory from address Origin; Memory is the
	// number of bytes of memory from address 0.
	Data   []byte
	Origin int
	Memory int
	// Globals is the number of mutable i32 globals, the registers of the
	// architecture, which start as 0.
	Globals int
}

const pageSize = 65536

func uleb(b []byte, v uint64) []byte {
	for {
		c := byte(v & 0x7F)
		v >>= 7
		if v == 0 {
			return append(b, c)
		}
		b = append(b, c|0x80)
	}
}

func valType(width int) byte {
	if width == 64 {
		return 0x7E // i64
	}
	return 0x7F // i32
}

func name(b []byte, s string) []byte { return append(uleb(b, uint64(len(s))), s...) }

// Bytes returns the binary format of the module.
func (m *Module) Bytes() []byte {
	out := []byte("\x00asm\x01\x00\x00\x00")
	section := func(id byte, body []byte) {
		out = append(out, id)
		out = uleb(out, uint64(len(body)))
		out = append(out, body...)
	}
	// Type 0 is the type of the functions of the image; the imports
	// have their own types after it.
	types := [][]byte{{0x60, 0, 0}}
	var importTypes []int
	for _, im := range m.Imports {
		t := []byte{0x60}
		t = uleb(t, uint64(len(im.Params)))
		for _, w := range im.Params {
			t = append(t, valType(w))
		}
		t = uleb(t, uint64(len(im.Results)))
		for _, w := range im.Results {
			t = append(t, valType(w))
		}
		idx := -1
		for k, u := range types {
			if string(u) == string(t) {
				idx = k
			}
		}
		if idx < 0 {
			idx = len(types)
			types = append(types, t)
		}
		importTypes = append(importTypes, idx)
	}
	var b []byte
	b = uleb(b, uint64(len(types)))
	for _, t := range types {
		b = append(b, t...)
	}
	section(1, b)
	if len(m.Imports) > 0 {
		b = uleb(nil, uint64(len(m.Imports)))
		for k, im := range m.Imports {
			b = name(name(b, im.Module), im.Name)
			b = uleb(append(b, 0x00), uint64(importTypes[k]))
		}
		section(2, b)
	}
	b = uleb(nil, uint64(len(m.Funcs)))
	for range m.Funcs {
		b = append(b, 0)
	}
	section(3, b)
	// The table holds every function at its index, so that the address
	// of a function is its index.
	n := uint64(len(m.Imports) + len(m.Funcs))
	section(4, uleb(uleb([]byte{1, 0x70, 0x01}, n), n))
	pages := uint64((m.Memory + pageSize - 1) / pageSize)
	section(5, uleb([]byte{1, 0x00}, pages))
	if m.Globals > 0 {
		b = uleb(nil, uint64(m.Globals))
		for k := 0; k < m.Globals; k++ {
			b = append(b, 0x7F, 0x01, 0x41, 0x00, 0x0B)
		}
		section(6, b)
	}
	b = uleb(nil, 2)
	b = append(name(b, "memory"), 0x02, 0x00)
	b = uleb(append(name(b, "_start"), 0x00), uint64(len(m.Imports)+m.Entry))
	section(7, b)
	b = append(uleb(nil, 1), 0x00, 0x41, 0x00, 0x0B)
	b = uleb(b, n)
	for k := uint64(0); k < n; k++ {
		b = uleb(b, k)
	}
	section(9, b)
	b = uleb(nil, uint64(len(m.Funcs)))
	for _, f := range m.Funcs {
		body := append(append([]byte{0}, f...), 0x0B)
		b = append(uleb(b, uint64(len(body))), body...)
	}
	section(10, b)
	data := m.Data
	for len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}
	if len(data) > 0 {
		b = append(uleb(nil, 1), 0x00, 0x41)
		b = sleb(b, int64(m.Origin))
		b = append(b, 0x0B)
		b = uleb(b, uint64(len(data)))
		b = append(b, data...)
		section(11, b)
	}
	return out
}

func sleb(b []byte, v int64) []byte {
	for {
		c := byte(v & 0x7F)
		v >>= 7
		if v == 0 && c&0x40 == 0 || v == -1 && c&0x40 != 0 {
			return append(b, c)
		}
		b = append(b, c|0x80)
	}
}
