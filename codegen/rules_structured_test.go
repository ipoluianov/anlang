package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"an/emu/wasm"
)

// wasmTemplate is a template of an architecture with structured control
// flow as the only function of a module: it runs the template in a block
// that the trap label leaves and returns 1 if control left it.
type wasmTemplate struct {
	store *wasm.Store
	inst  *wasm.Instance
	f     *wasm.Function
}

// wasmMem is the memory of the instance.
type wasmMem struct{ m *wasm.Memory }

func (w wasmMem) Read(addr uint64, width int) uint64 {
	var v uint64
	for i := 0; i < width/8; i++ {
		v |= uint64(w.m.Data[addr+uint64(i)]) << uint(8*i)
	}
	return v
}

func (w wasmMem) Write(addr uint64, width int, v uint64) {
	for i := 0; i < width/8; i++ {
		w.m.Data[addr+uint64(i)] = byte(v >> uint(8*i))
	}
}

func leb(b []byte, v uint64) []byte {
	for {
		c := byte(v & 0x7F)
		v >>= 7
		if v == 0 {
			return append(b, c)
		}
		b = append(b, c|0x80)
	}
}

// prepareWasm assembles the lines of a template in the function of a
// module with one page of memory and the registers of the architecture as
// mutable globals: the frame register holds the frame base less
// harnessFrame.
func (h *harness) prepareWasm(lines []string) (*wasmTemplate, error) {
	g := h.g
	open := map[string]bool{}
	for _, m := range h.a.ABI.Structured.Open {
		open[strings.ToLower(m)] = true
	}
	end := strings.ToLower(h.a.ABI.Structured.End)
	body := []byte{0x02, 0x40} // block
	extra := 0
	for _, l := range lines {
		l = slotTok.ReplaceAllStringFunc(l, func(m string) string {
			off, _ := strconv.Atoi(slotTok.FindStringSubmatch(m)[1])
			return strconv.Itoa(harnessFrame + off)
		})
		l = strings.ReplaceAll(l, "<TRAP+0>", strconv.Itoa(extra))
		code, err := g.assemble(l, 0)
		if err != nil {
			return nil, fmt.Errorf("%q: %v", l, err)
		}
		body = append(body, code...)
		switch mn := strings.ToLower(strings.Fields(l)[0]); {
		case open[mn]:
			extra++
		case mn == end:
			extra--
		}
	}
	body = append(body, 0x41, 0x00, 0x0F, 0x0B, 0x41, 0x01, 0x0B) // i32.const 0; return; end; i32.const 1; end
	mod := []byte("\x00asm\x01\x00\x00\x00")
	section := func(id byte, b []byte) {
		mod = append(leb(append(mod, id), uint64(len(b))), b...)
	}
	section(1, []byte{1, 0x60, 0, 1, 0x7F})
	section(3, []byte{1, 0})
	section(5, []byte{1, 0, 1})
	var globals []byte
	n := 0
	for _, r := range h.a.Registers {
		if r.Class == "" {
			continue
		}
		v := uint64(0)
		if r.Name == h.a.ABI.Frame {
			v = frameBase - harnessFrame
		}
		globals = append(globals, 0x7F, 0x01, 0x41)
		globals = append(leb(globals, v), 0x0B) // the values are small and positive
		n++
	}
	section(6, append(leb(nil, uint64(n)), globals...))
	section(7, []byte{1, 1, 'f', 0, 0})
	section(10, append(leb([]byte{1}, uint64(len(body)+1)), append([]byte{0}, body...)...))
	m, err := wasm.Decode(mod)
	if err != nil {
		return nil, err
	}
	s := wasm.NewStore()
	inst, err := s.Instantiate(m, nil)
	if err != nil {
		return nil, err
	}
	return &wasmTemplate{store: s, inst: inst, f: inst.Exports["f"].Func}, nil
}

func (w *wasmTemplate) run(setup func(m mem)) (mem, bool, error) {
	m := wasmMem{w.inst.Memory()}
	setup(m)
	w.store.Fuel = 100000
	res, err := w.store.Call(w.f, nil)
	if err != nil {
		return nil, false, err
	}
	return m, res[0] == 1, nil
}
