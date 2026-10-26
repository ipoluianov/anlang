package wasm

import (
	"fmt"
	"io"
)

// WASI runs a module with the functions of the WebAssembly System
// Interface that the platform web imports (tool.emu.wasm): fd_write,
// fd_read and proc_exit of module wasi_snapshot_preview1. File descriptor
// 0 is Stdin, 1 Stdout and 2 Stderr.
type WASI struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// MaxSteps, if not 0, limits the branches and calls of the program.
	MaxSteps int64
}

// exit is the end of the program by proc_exit.
type exit struct{ code int }

func (e exit) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// errno values of WASI.
const (
	errnoSuccess = 0
	errnoBadf    = 8
	errnoFault   = 21
)

// Run decodes, validates and instantiates the module and calls its
// export _start; it returns the exit status.
func (w *WASI) Run(module []byte) (int, error) {
	m, err := Decode(module)
	if err != nil {
		return 0, err
	}
	if err := m.Validate(); err != nil {
		return 0, err
	}
	s := NewStore()
	s.Fuel = w.MaxSteps
	var inst *Instance
	mem := func() []byte {
		if inst == nil || inst.Memory() == nil {
			return nil
		}
		return inst.Memory().Data
	}
	// iovecs reads the vector of (address, length) pairs at iovs.
	iovecs := func(iovs, n uint32) ([][2]uint32, bool) {
		d := mem()
		var v [][2]uint32
		for k := uint32(0); k < n; k++ {
			a := uint64(iovs) + 8*uint64(k)
			if a+8 > uint64(len(d)) {
				return nil, false
			}
			p, l := le32(d[a:]), le32(d[a+4:])
			if uint64(p)+uint64(l) > uint64(len(d)) {
				return nil, false
			}
			v = append(v, [2]uint32{p, l})
		}
		return v, true
	}
	i32 := []ValType{I32}
	funcs := map[string]*Function{
		"fd_write": s.HostFunc(FuncType{Params: []ValType{I32, I32, I32, I32}, Results: i32}, func(a []uint64) ([]uint64, error) {
			var out io.Writer
			switch uint32(a[0]) {
			case 1:
				out = w.Stdout
			case 2:
				out = w.Stderr
			default:
				return []uint64{errnoBadf}, nil
			}
			v, ok := iovecs(uint32(a[1]), uint32(a[2]))
			if !ok {
				return []uint64{errnoFault}, nil
			}
			d := mem()
			total := uint32(0)
			for _, iov := range v {
				if out != nil {
					out.Write(d[iov[0] : iov[0]+iov[1]])
				}
				total += iov[1]
			}
			if p := uint64(uint32(a[3])); p+4 <= uint64(len(d)) {
				put32(d[p:], total)
			} else {
				return []uint64{errnoFault}, nil
			}
			return []uint64{errnoSuccess}, nil
		}),
		"fd_read": s.HostFunc(FuncType{Params: []ValType{I32, I32, I32, I32}, Results: i32}, func(a []uint64) ([]uint64, error) {
			if uint32(a[0]) != 0 {
				return []uint64{errnoBadf}, nil
			}
			v, ok := iovecs(uint32(a[1]), uint32(a[2]))
			if !ok {
				return []uint64{errnoFault}, nil
			}
			d := mem()
			total := uint32(0)
			for _, iov := range v {
				if w.Stdin == nil || iov[1] == 0 {
					continue
				}
				n, _ := w.Stdin.Read(d[iov[0] : iov[0]+iov[1]])
				total += uint32(n)
				if n < int(iov[1]) {
					break
				}
			}
			if p := uint64(uint32(a[3])); p+4 <= uint64(len(d)) {
				put32(d[p:], total)
			} else {
				return []uint64{errnoFault}, nil
			}
			return []uint64{errnoSuccess}, nil
		}),
		"proc_exit": s.HostFunc(FuncType{Params: i32}, func(a []uint64) ([]uint64, error) {
			return nil, exit{int(int32(a[0]))}
		}),
	}
	inst, err = s.Instantiate(m, func(module, name string) (Extern, bool) {
		if module != "wasi_snapshot_preview1" {
			return Extern{}, false
		}
		f, ok := funcs[name]
		return Extern{Kind: KindFunc, Func: f}, ok
	})
	if err != nil {
		return 0, err
	}
	start, ok := inst.Exports["_start"]
	if !ok || start.Kind != KindFunc {
		return 0, fmt.Errorf("the module exports no function _start")
	}
	_, err = s.Call(start.Func, nil)
	if e, ok := err.(exit); ok {
		return e.code, nil
	}
	return 0, err
}
