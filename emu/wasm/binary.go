package wasm

import (
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// MalformedError reports a module that does not decode: its binary or its
// text form is not well formed.
type MalformedError struct{ Msg string }

func (e *MalformedError) Error() string { return "malformed: " + e.Msg }

func malformed(format string, args ...any) error {
	return &MalformedError{Msg: fmt.Sprintf(format, args...)}
}

// IsMalformed reports whether err reports a malformed module.
func IsMalformed(err error) bool {
	var m *MalformedError
	return errors.As(err, &m)
}

// decoder reads the binary format.
type decoder struct {
	b   []byte
	pos int
	err error
}

func (d *decoder) fail(format string, args ...any) {
	if d.err == nil {
		d.err = malformed(format, args...)
	}
	d.pos = len(d.b)
}

func (d *decoder) eof() bool { return d.pos >= len(d.b) }

func (d *decoder) byte() byte {
	if d.pos >= len(d.b) {
		d.fail("unexpected end")
		return 0
	}
	c := d.b[d.pos]
	d.pos++
	return c
}

func (d *decoder) bytes(n int) []byte {
	if n < 0 || d.pos+n > len(d.b) {
		d.fail("unexpected end")
		return nil
	}
	s := d.b[d.pos : d.pos+n]
	d.pos += n
	return s
}

// uleb reads an unsigned LEB128 number of at most bits bits.
func (d *decoder) uleb(bits int) uint64 {
	var v uint64
	for shift := 0; ; shift += 7 {
		if shift >= bits {
			d.fail("integer representation too long")
			return 0
		}
		c := d.byte()
		if d.err != nil {
			return 0
		}
		if rem := bits - shift; rem < 7 && c&0x7F>>uint(rem) != 0 {
			d.fail("integer too large")
			return 0
		}
		v |= uint64(c&0x7F) << uint(shift)
		if c&0x80 == 0 {
			return v
		}
	}
}

// sleb reads a signed LEB128 number of at most bits bits.
func (d *decoder) sleb(bits int) int64 {
	var v int64
	for shift := 0; ; shift += 7 {
		if shift >= bits {
			d.fail("integer representation too long")
			return 0
		}
		c := d.byte()
		if d.err != nil {
			return 0
		}
		if rem := bits - shift; rem < 7 {
			// The unused bits are the sign extension of the last used one.
			hi := int8(c<<1) >> uint(rem) // bits rem-1..6, sign-extended
			if c&0x80 != 0 || hi != 0 && hi != -1 {
				d.fail("integer too large")
				return 0
			}
		}
		v |= int64(c&0x7F) << uint(shift)
		if c&0x80 == 0 {
			if shift+7 < 64 && c&0x40 != 0 {
				v |= -1 << uint(shift+7)
			}
			return v
		}
	}
}

func (d *decoder) u32() uint32 { return uint32(d.uleb(32)) }

func (d *decoder) name() string {
	n := d.u32()
	b := d.bytes(int(n))
	if d.err == nil && !utf8.Valid(b) {
		d.fail("malformed UTF-8 encoding")
	}
	return string(b)
}

func (d *decoder) valType() ValType {
	c := d.byte()
	if d.err == nil && !validValType(c) {
		d.fail("malformed value type")
	}
	return ValType(c)
}

func (d *decoder) refType() ValType {
	c := d.byte()
	if d.err == nil && c != byte(FuncRef) && c != byte(ExternRef) {
		d.fail("malformed reference type")
	}
	return ValType(c)
}

func (d *decoder) limits() Limits {
	var l Limits
	switch d.byte() {
	case 0:
		l.Min = d.u32()
	case 1:
		l.Min = d.u32()
		l.Max = d.u32()
		l.HasMax = true
	default:
		d.fail("integer too large")
	}
	return l
}

func (d *decoder) tableType() TableType {
	t := TableType{Elem: d.refType()}
	t.Limits = d.limits()
	return t
}

func (d *decoder) globalType() GlobalType {
	g := GlobalType{Type: d.valType()}
	switch d.byte() {
	case 0:
	case 1:
		g.Mutable = true
	default:
		d.fail("malformed mutability")
	}
	return g
}

// count reads the length of a vector whose elements take at least min
// bytes each, so that a huge count fails before it is allocated.
func (d *decoder) count(min int) int {
	n := d.u32()
	if min > 0 && uint64(n)*uint64(min) > uint64(len(d.b)-d.pos) {
		d.fail("unexpected end")
		return 0
	}
	return int(n)
}

// Decode decodes a module in the binary format.
func Decode(b []byte) (*Module, error) {
	d := &decoder{b: b}
	m := &Module{Start: -1, DataCount: -1}
	if len(b) < 4 || string(b[:4]) != "\x00asm" {
		return nil, malformed("magic header not detected")
	}
	if len(b) < 8 {
		return nil, malformed("unexpected end")
	}
	if string(b[4:8]) != "\x01\x00\x00\x00" {
		return nil, malformed("unknown binary version")
	}
	d.pos = 8
	// The order of the sections other than custom ones.
	order := map[byte]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6, 7: 7, 8: 8, 9: 9, 12: 10, 10: 11, 11: 12}
	last := 0
	var funcTypes []uint32
	hasCode := false
	for !d.eof() && d.err == nil {
		id := d.byte()
		size := d.u32()
		if d.err != nil {
			break
		}
		if uint64(size) > uint64(len(b)-d.pos) {
			d.fail("unexpected end of section or function")
			break
		}
		end := d.pos + int(size)
		sec := &decoder{b: b[:end], pos: d.pos}
		if id != 0 {
			k, ok := order[id]
			if !ok {
				return nil, malformed("malformed section id")
			}
			if k <= last {
				return nil, malformed("unexpected content after last section")
			}
			last = k
		}
		switch id {
		case 0:
			sec.name()
			sec.pos = end
		case 1:
			n := sec.count(1)
			for i := 0; i < n && sec.err == nil; i++ {
				if sec.byte() != 0x60 {
					sec.fail("integer representation too long")
					break
				}
				var t FuncType
				np := sec.count(1)
				for j := 0; j < np && sec.err == nil; j++ {
					t.Params = append(t.Params, sec.valType())
				}
				nr := sec.count(1)
				for j := 0; j < nr && sec.err == nil; j++ {
					t.Results = append(t.Results, sec.valType())
				}
				m.Types = append(m.Types, t)
			}
		case 2:
			n := sec.count(3)
			for i := 0; i < n && sec.err == nil; i++ {
				im := Import{Module: sec.name(), Name: sec.name()}
				im.Kind = sec.byte()
				switch im.Kind {
				case KindFunc:
					im.Func = sec.u32()
				case KindTable:
					im.Table = sec.tableType()
				case KindMemory:
					im.Memory = sec.limits()
				case KindGlobal:
					im.Global = sec.globalType()
				default:
					sec.fail("malformed import kind")
				}
				m.Imports = append(m.Imports, im)
			}
		case 3:
			n := sec.count(1)
			for i := 0; i < n && sec.err == nil; i++ {
				funcTypes = append(funcTypes, sec.u32())
			}
		case 4:
			n := sec.count(2)
			for i := 0; i < n && sec.err == nil; i++ {
				m.Tables = append(m.Tables, sec.tableType())
			}
		case 5:
			n := sec.count(2)
			for i := 0; i < n && sec.err == nil; i++ {
				m.Memories = append(m.Memories, sec.limits())
			}
		case 6:
			n := sec.count(3)
			for i := 0; i < n && sec.err == nil; i++ {
				g := Global{Type: sec.globalType()}
				g.Init = sec.expr()
				m.Globals = append(m.Globals, g)
			}
		case 7:
			n := sec.count(3)
			for i := 0; i < n && sec.err == nil; i++ {
				e := Export{Name: sec.name()}
				e.Kind = sec.byte()
				if e.Kind > KindGlobal {
					sec.fail("malformed export kind")
				}
				e.Index = sec.u32()
				m.Exports = append(m.Exports, e)
			}
		case 8:
			m.Start = int(sec.u32())
		case 9:
			n := sec.count(1)
			for i := 0; i < n && sec.err == nil; i++ {
				m.Elems = append(m.Elems, sec.elem())
			}
		case 12:
			m.DataCount = int(sec.u32())
		case 10:
			hasCode = true
			n := sec.count(1)
			if sec.err == nil && n != len(funcTypes) {
				return nil, malformed("function and code section have inconsistent lengths")
			}
			for i := 0; i < n && sec.err == nil; i++ {
				size := sec.u32()
				if uint64(size) > uint64(len(sec.b)-sec.pos) {
					sec.fail("unexpected end of section or function")
					break
				}
				body := &decoder{b: sec.b[:sec.pos+int(size)], pos: sec.pos}
				f := Func{Type: funcTypes[i]}
				var total uint64
				nl := body.count(2)
				for j := 0; j < nl && body.err == nil; j++ {
					c := body.u32()
					t := body.valType()
					total += uint64(c)
					if total > math.MaxUint32 {
						body.fail("too many locals")
						break
					}
					if total > 1<<20 {
						body.fail("too many locals") // a limit of the implementation
						break
					}
					for k := uint32(0); k < c; k++ {
						f.Locals = append(f.Locals, t)
					}
				}
				f.Body = body.instrs(true)
				if body.err == nil && body.pos != len(body.b) {
					body.fail("section size mismatch")
				}
				if body.err != nil {
					sec.err = body.err
					break
				}
				sec.pos = body.pos
				m.Funcs = append(m.Funcs, f)
			}
		case 11:
			n := sec.count(1)
			if sec.err == nil && m.DataCount >= 0 && n != m.DataCount {
				return nil, malformed("data count and data section have inconsistent lengths")
			}
			for i := 0; i < n && sec.err == nil; i++ {
				var dt Data
				switch flags := sec.u32(); flags {
				case 0:
					dt.Offset = sec.expr()
				case 1:
					dt.Mode = ModePassive
				case 2:
					dt.Memory = sec.u32()
					dt.Offset = sec.expr()
				default:
					sec.fail("malformed data segment kind")
				}
				nb := sec.count(0)
				dt.Bytes = sec.bytes(nb)
				m.Datas = append(m.Datas, dt)
			}
		}
		if sec.err != nil {
			return nil, sec.err
		}
		if sec.pos != end {
			return nil, malformed("section size mismatch")
		}
		d.pos = end
	}
	if d.err != nil {
		return nil, d.err
	}
	if len(funcTypes) > 0 && !hasCode {
		return nil, malformed("function and code section have inconsistent lengths")
	}
	if m.DataCount >= 0 && len(m.Datas) != m.DataCount {
		return nil, malformed("data count and data section have inconsistent lengths")
	}
	if m.DataCount < 0 && usesDataIndex(m) {
		return nil, malformed("data count section required")
	}
	return m, nil
}

// usesDataIndex reports whether a function of m uses memory.init or
// data.drop, which need the data count section.
func usesDataIndex(m *Module) bool {
	for _, f := range m.Funcs {
		for _, in := range f.Body {
			if in.Op == prefixMisc+0x08 || in.Op == prefixMisc+0x09 {
				return true
			}
		}
	}
	return false
}

func (d *decoder) elem() Elem {
	var e Elem
	flags := d.u32()
	if flags > 7 {
		d.fail("malformed elements segment kind")
		return e
	}
	switch {
	case flags&1 == 0:
		e.Mode = ModeActive
	case flags&2 == 0:
		e.Mode = ModePassive
	default:
		e.Mode = ModeDeclarative
	}
	if flags&1 == 0 {
		if flags&2 != 0 {
			e.Table = d.u32()
		}
		e.Offset = d.expr()
	}
	e.Type = FuncRef
	exprs := flags&4 != 0
	if flags&3 != 0 {
		if exprs {
			e.Type = d.refType()
		} else if d.byte() != 0 {
			d.fail("malformed element kind")
		}
	}
	n := d.count(1)
	for i := 0; i < n && d.err == nil; i++ {
		if exprs {
			e.Init = append(e.Init, d.expr())
		} else {
			f := d.u32()
			e.Init = append(e.Init, []Instr{{Op: opRefFunc, A: f}, {Op: opEnd}})
		}
	}
	return e
}

// expr reads a constant expression.
func (d *decoder) expr() []Instr { return d.instrs(false) }

// instrs reads instructions up to the end that closes the expression.
func (d *decoder) instrs(body bool) []Instr {
	var out []Instr
	depth := 0
	for d.err == nil {
		if d.eof() {
			if body {
				d.fail("unexpected end of section or function")
			} else {
				d.fail("unexpected end")
			}
			break
		}
		in := d.instr()
		if d.err != nil {
			break
		}
		out = append(out, in)
		switch in.Op {
		case opBlock, opLoop, opIf:
			depth++
		case opEnd:
			if depth == 0 {
				return out
			}
			depth--
		}
	}
	return nil
}

func (d *decoder) instr() Instr {
	op := int(d.byte())
	switch op {
	case 0xFC:
		op = prefixMisc + int(d.u32())
	case 0xFD:
		op = prefixSIMD + int(d.u32())
	}
	if d.err != nil {
		return Instr{}
	}
	info, ok := ops[op]
	if !ok {
		d.fail("illegal opcode %#x", op)
		return Instr{}
	}
	in := Instr{Op: op}
	switch info.imm {
	case immBlock:
		if d.eof() {
			d.fail("unexpected end")
			break
		}
		switch c := d.b[d.pos]; {
		case c == 0x40:
			d.pos++
		case validValType(c):
			d.pos++
			in.A, in.B = 1, uint32(c)
		default:
			v := d.sleb(33)
			if v < 0 {
				d.fail("malformed block type")
			}
			in.A, in.B = 2, uint32(v)
		}
	case immLabel, immFunc, immLocal, immGlobal, immTable, immData, immElem:
		in.A = d.u32()
	case immBrTable:
		n := d.count(1)
		for i := 0; i <= n && d.err == nil; i++ {
			in.Ext = append(in.Ext, d.u32())
		}
	case immCallInd:
		in.A = d.u32()
		in.B = d.u32()
	case immMem:
		in.A = d.u32()
		if in.A >= 32 {
			d.fail("malformed memop flags")
		}
		in.C = uint64(d.u32())
	case immMemLane:
		in.A = d.u32()
		if in.A >= 32 {
			d.fail("malformed memop flags")
		}
		in.C = uint64(d.u32())
		in.B = uint32(d.byte())
	case immZero:
		if d.byte() != 0 {
			d.fail("zero byte expected")
		}
	case immZero2:
		if d.byte() != 0 || d.byte() != 0 {
			d.fail("zero byte expected")
		}
	case immI32:
		in.C = uint64(uint32(d.sleb(32)))
	case immI64:
		in.C = uint64(d.sleb(64))
	case immF32:
		b := d.bytes(4)
		if b != nil {
			in.C = uint64(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24)
		}
	case immF64:
		b := d.bytes(8)
		for i := range b {
			in.C |= uint64(b[i]) << (8 * uint(i))
		}
	case immSelectT:
		n := d.count(1)
		for i := 0; i < n && d.err == nil; i++ {
			in.Ext = append(in.Ext, uint32(d.valType()))
		}
	case immRefType:
		in.A = uint32(d.refType())
	case immElemTable:
		in.A = d.u32() // the element segment
		in.B = d.u32() // the table
	case immTable2:
		in.A = d.u32()
		in.B = d.u32()
	case immDataZero:
		in.A = d.u32()
		if d.byte() != 0 {
			d.fail("zero byte expected")
		}
	case immLane:
		in.A = uint32(d.byte())
	case immV128, immShuffle:
		b := d.bytes(16)
		for i := range b {
			if i < 8 {
				in.C |= uint64(b[i]) << (8 * uint(i))
			} else {
				in.D |= uint64(b[i]) << (8 * uint(i-8))
			}
		}
	}
	return in
}
