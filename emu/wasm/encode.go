package wasm

// Encode returns the binary format of a module.
func (m *Module) Encode() []byte {
	out := []byte("\x00asm\x01\x00\x00\x00")
	section := func(id byte, body []byte) {
		out = append(out, id)
		out = appendU(out, uint64(len(body)))
		out = append(out, body...)
	}
	vec := func(n int, f func(b []byte, i int) []byte) []byte {
		b := appendU(nil, uint64(n))
		for i := 0; i < n; i++ {
			b = f(b, i)
		}
		return b
	}
	if len(m.Types) > 0 {
		section(1, vec(len(m.Types), func(b []byte, i int) []byte {
			t := m.Types[i]
			b = append(b, 0x60)
			b = appendTypes(b, t.Params)
			return appendTypes(b, t.Results)
		}))
	}
	if len(m.Imports) > 0 {
		section(2, vec(len(m.Imports), func(b []byte, i int) []byte {
			im := m.Imports[i]
			b = appendName(appendName(b, im.Module), im.Name)
			b = append(b, im.Kind)
			switch im.Kind {
			case KindFunc:
				b = appendU(b, uint64(im.Func))
			case KindTable:
				b = appendLimits(append(b, byte(im.Table.Elem)), im.Table.Limits)
			case KindMemory:
				b = appendLimits(b, im.Memory)
			case KindGlobal:
				b = append(b, byte(im.Global.Type), b2b(im.Global.Mutable))
			}
			return b
		}))
	}
	if len(m.Funcs) > 0 {
		section(3, vec(len(m.Funcs), func(b []byte, i int) []byte { return appendU(b, uint64(m.Funcs[i].Type)) }))
	}
	if len(m.Tables) > 0 {
		section(4, vec(len(m.Tables), func(b []byte, i int) []byte {
			return appendLimits(append(b, byte(m.Tables[i].Elem)), m.Tables[i].Limits)
		}))
	}
	if len(m.Memories) > 0 {
		section(5, vec(len(m.Memories), func(b []byte, i int) []byte { return appendLimits(b, m.Memories[i]) }))
	}
	if len(m.Globals) > 0 {
		section(6, vec(len(m.Globals), func(b []byte, i int) []byte {
			g := m.Globals[i]
			return appendInstrs(append(b, byte(g.Type.Type), b2b(g.Type.Mutable)), g.Init)
		}))
	}
	if len(m.Exports) > 0 {
		section(7, vec(len(m.Exports), func(b []byte, i int) []byte {
			e := m.Exports[i]
			return appendU(append(appendName(b, e.Name), e.Kind), uint64(e.Index))
		}))
	}
	if m.Start >= 0 {
		section(8, appendU(nil, uint64(m.Start)))
	}
	if len(m.Elems) > 0 {
		section(9, vec(len(m.Elems), func(b []byte, i int) []byte {
			e := m.Elems[i]
			// Always the form with expressions and an explicit type.
			switch e.Mode {
			case ModeActive:
				b = appendU(b, 6)
				b = appendU(b, uint64(e.Table))
				b = appendInstrs(b, e.Offset)
			case ModePassive:
				b = appendU(b, 5)
			case ModeDeclarative:
				b = appendU(b, 7)
			}
			b = append(b, byte(e.Type))
			b = appendU(b, uint64(len(e.Init)))
			for _, x := range e.Init {
				b = appendInstrs(b, x)
			}
			return b
		}))
	}
	if m.DataCount >= 0 {
		section(12, appendU(nil, uint64(m.DataCount)))
	}
	if len(m.Funcs) > 0 {
		section(10, vec(len(m.Funcs), func(b []byte, i int) []byte {
			f := m.Funcs[i]
			var body []byte
			var groups [][2]int // the count and the type of each run of locals
			for _, t := range f.Locals {
				if n := len(groups); n > 0 && groups[n-1][1] == int(t) {
					groups[n-1][0]++
				} else {
					groups = append(groups, [2]int{1, int(t)})
				}
			}
			body = appendU(body, uint64(len(groups)))
			for _, g := range groups {
				body = append(appendU(body, uint64(g[0])), byte(g[1]))
			}
			body = appendInstrs(body, f.Body)
			return append(appendU(b, uint64(len(body))), body...)
		}))
	}
	if len(m.Datas) > 0 {
		section(11, vec(len(m.Datas), func(b []byte, i int) []byte {
			d := m.Datas[i]
			switch d.Mode {
			case ModeActive:
				b = appendU(b, 2)
				b = appendU(b, uint64(d.Memory))
				b = appendInstrs(b, d.Offset)
			default:
				b = appendU(b, 1)
			}
			b = appendU(b, uint64(len(d.Bytes)))
			return append(b, d.Bytes...)
		}))
	}
	return out
}

func b2b(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func appendU(b []byte, v uint64) []byte {
	for {
		c := byte(v & 0x7F)
		v >>= 7
		if v == 0 {
			return append(b, c)
		}
		b = append(b, c|0x80)
	}
}

func appendS(b []byte, v int64) []byte {
	for {
		c := byte(v & 0x7F)
		v >>= 7
		if v == 0 && c&0x40 == 0 || v == -1 && c&0x40 != 0 {
			return append(b, c)
		}
		b = append(b, c|0x80)
	}
}

func appendName(b []byte, s string) []byte {
	return append(appendU(b, uint64(len(s))), s...)
}

func appendTypes(b []byte, ts []ValType) []byte {
	b = appendU(b, uint64(len(ts)))
	for _, t := range ts {
		b = append(b, byte(t))
	}
	return b
}

func appendLimits(b []byte, l Limits) []byte {
	if l.HasMax {
		return appendU(appendU(append(b, 1), uint64(l.Min)), uint64(l.Max))
	}
	return appendU(append(b, 0), uint64(l.Min))
}

func appendInstrs(b []byte, code []Instr) []byte {
	for _, in := range code {
		b = AppendInstr(b, in)
	}
	return b
}

// AppendInstr appends the binary encoding of an instruction.
func AppendInstr(b []byte, in Instr) []byte {
	switch {
	case in.Op >= prefixSIMD:
		b = appendU(append(b, 0xFD), uint64(in.Op-prefixSIMD))
	case in.Op >= prefixMisc:
		b = appendU(append(b, 0xFC), uint64(in.Op-prefixMisc))
	default:
		b = append(b, byte(in.Op))
	}
	switch ops[in.Op].imm {
	case immBlock:
		switch in.A {
		case 0:
			b = append(b, 0x40)
		case 1:
			b = append(b, byte(in.B))
		default:
			b = appendS(b, int64(in.B))
		}
	case immLabel, immFunc, immLocal, immGlobal, immTable, immData, immElem:
		b = appendU(b, uint64(in.A))
	case immBrTable:
		b = appendU(b, uint64(len(in.Ext)-1))
		for _, l := range in.Ext {
			b = appendU(b, uint64(l))
		}
	case immCallInd, immElemTable, immTable2:
		b = appendU(appendU(b, uint64(in.A)), uint64(in.B))
	case immMem:
		b = appendU(appendU(b, uint64(in.A)), in.C)
	case immMemLane:
		b = append(appendU(appendU(b, uint64(in.A)), in.C), byte(in.B))
	case immZero:
		b = append(b, 0)
	case immZero2:
		b = append(b, 0, 0)
	case immI32:
		b = appendS(b, int64(int32(in.C)))
	case immI64:
		b = appendS(b, int64(in.C))
	case immF32:
		b = append(b, byte(in.C), byte(in.C>>8), byte(in.C>>16), byte(in.C>>24))
	case immF64:
		for k := 0; k < 8; k++ {
			b = append(b, byte(in.C>>(8*uint(k))))
		}
	case immSelectT:
		b = appendU(b, uint64(len(in.Ext)))
		for _, t := range in.Ext {
			b = append(b, byte(t))
		}
	case immRefType:
		b = append(b, byte(in.A))
	case immDataZero:
		b = append(appendU(b, uint64(in.A)), 0)
	case immLane:
		b = append(b, byte(in.A))
	case immV128, immShuffle:
		v := vec(in.C, in.D)
		b = append(b, v[:]...)
	}
	return b
}
