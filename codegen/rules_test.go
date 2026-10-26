package codegen

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"

	"an/arch/desc"
	"an/types"
)

// TestRulesI8086 checks the instruction selection rules of the 8086
// against the formal semantics (target.arch.rules-test): each rule of a
// data operation runs in the interpreter generated from the description,
// exhaustively for 8-bit operands and on boundary and random values for
// wider ones, with its operands in frame slots and, where its template
// assembles with them, as constants.
func TestRulesI8086(t *testing.T) {
	a, errs := desc.ForName("i8086")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	rng := rand.New(rand.NewSource(1))
	checked := 0
	for _, r := range a.Rules {
		switch r.Op {
		case "jump", "branch", "call", "callind", "enter", "leave", "regin", "regout", "flagsout":
			continue
		}
		r := r
		t.Run(fmt.Sprintf("%s/%d/%d", r.Op, r.Width, r.From), func(t *testing.T) {
			h := newHarness(t, a)
			checked++
			switch r.Op {
			case "addr":
				h.checkAddr(r)
			case "load", "store", "load.far", "store.far":
				h.checkMemory(r, rng)
			case "copy", "zero":
				h.checkBlock(r, rng)
			default:
				h.checkData(r, rng)
			}
		})
	}
	if checked == 0 {
		t.Fatal("no rules")
	}
}

// harness runs instruction templates on the 8086 interpreter: code at
// 0000:0100, the frame base BP at 0x8000.
type harness struct {
	t     *testing.T
	a     *desc.Arch
	g     *gen
	m     *desc.Machine
	cache map[string]*prepared
}

// prepared is an assembled template.
type prepared struct {
	image     []byte
	end, trap uint64
	err       error
}

const (
	frameBase = 0x8000
	offX      = -16 // the slots of x, y and r
	offY      = -32
	offR      = -48
)

func newHarness(t *testing.T, a *desc.Arch) *harness {
	g := &gen{arch: a, word: a.Word / 8, ptr: a.Pointers.Near / 8, align: a.ABI.Align, intBits: a.Word, frame: a.ABI.Frame,
		layouts: map[types.Type][2]int{}, funcsByLabel: map[string]bool{}, selected: map[string]int{}, rules: map[string][]desc.Rule{}}
	return &harness{t: t, a: a, g: g, m: desc.NewMachine(a), cache: map[string]*prepared{}}
}

// prepare assembles template code with the operands.
func (h *harness) prepare(code string, ops map[string]operand) *prepared {
	key := fmt.Sprint(code, ops)
	if p, ok := h.cache[key]; ok {
		return p
	}
	p := &prepared{}
	if len(h.cache) < 10000 {
		h.cache[key] = p
	}
	g := h.g
	lines, err := g.expand(code, ops, map[string]string{"trap": "TRAP"})
	if err != nil {
		p.err = err
		return p
	}
	if !g.assembles(lines) {
		p.err = errNoAssemble
		return p
	}
	g.code = nil
	for _, l := range lines {
		if len(l) > 0 && l[len(l)-1] == ':' {
			g.code = append(g.code, item{label: l[:len(l)-1]})
		} else {
			g.code = append(g.code, item{text: l})
		}
	}
	g.code = append(g.code, item{label: "END"}, item{text: "NOP"}, item{label: "TRAP"}, item{text: "NOP"})
	g.data = nil
	p.image, p.err = g.link(0x100)
	p.end, p.trap = uint64(g.addrs["END"]), uint64(g.addrs["TRAP"])
	return p
}

// run runs template code with the operands, with memory set by setup,
// and returns the machine and whether control reached the trap label.
func (h *harness) run(code string, ops map[string]operand, setup func(m *desc.Machine)) (*desc.Machine, bool, error) {
	p := h.prepare(code, ops)
	if p.err != nil {
		return nil, false, p.err
	}
	m := h.m
	copy(m.Mem[0x100:], p.image)
	for _, r := range []string{"CS", "DS", "ES", "SS"} {
		m.SetReg(r, 0)
	}
	m.SetReg("IP", 0x100)
	m.SetReg("SP", 0x7000)
	m.SetReg("BP", frameBase)
	setup(m)
	for steps := 0; ; steps++ {
		pc := m.FetchAddr()
		if pc == p.end {
			return m, false, nil
		}
		if pc == p.trap {
			return m, true, nil
		}
		if steps > 10000 {
			return nil, false, fmt.Errorf("the template does not end")
		}
		if err := m.Step(); err != nil {
			return nil, false, err
		}
	}
}

var errNoAssemble = fmt.Errorf("the template does not assemble with these operands")

func slot(off, width int) operand {
	return operand{kind: opSlot, off: off, width: widthOf(width / 8)}
}

func put(m *desc.Machine, off int, width int, v uint64) {
	for i := 0; i < width/8; i++ {
		m.Mem[frameBase+off+i] = byte(v >> uint(8*i))
	}
}

func get(m *desc.Machine, off int, width int) uint64 {
	var v uint64
	for i := 0; i < width/8; i++ {
		v |= uint64(m.Mem[frameBase+off+i]) << uint(8*i)
	}
	return v
}

// values returns the operand values to test for a width.
func values(width int, rng *rand.Rand) []uint64 {
	if width == 8 {
		var vs []uint64
		for v := range 256 {
			vs = append(vs, uint64(v))
		}
		return vs
	}
	m := uint64(1)<<uint(width) - 1
	if width == 64 {
		m = ^uint64(0)
	}
	half := uint64(1) << uint(width-1)
	vs := []uint64{0, 1, 2, 3, 7, 10, m, m - 1, half, half - 1, half + 1, 0xFF, 0x100, 0xFFFF & m, 0x10000 & m, 0x12345678 & m}
	for range 24 {
		vs = append(vs, rng.Uint64()&m)
	}
	return vs
}

// semantics computes the meaning of operation op of width w on x and y
// (target.arch.ops): the result, whether control goes to the trap label,
// and whether the operands meet the precondition.
func semantics(op string, w, from int, x, y uint64) (r uint64, trap, ok bool) {
	m := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(w)), big.NewInt(1))
	mask := func(b *big.Int) uint64 { return new(big.Int).And(b, m).Uint64() }
	u := func(v uint64) *big.Int { return new(big.Int).SetUint64(v) }
	s := func(v uint64, w int) *big.Int {
		b := u(v)
		if w < 64 && v>>uint(w-1)&1 != 0 || w == 64 && int64(v) < 0 {
			b.Sub(b, new(big.Int).Lsh(big.NewInt(1), uint(w)))
		}
		return b
	}
	fitsS := func(b *big.Int) bool {
		lim := new(big.Int).Lsh(big.NewInt(1), uint(w-1))
		return b.Cmp(new(big.Int).Neg(lim)) >= 0 && b.Cmp(lim) < 0
	}
	fitsU := func(b *big.Int) bool { return b.Sign() >= 0 && b.Cmp(m) <= 0 }
	b2u := func(b bool) uint64 {
		if b {
			return 1
		}
		return 0
	}
	var res *big.Int
	switch op {
	case "move":
		return x, false, true
	case "add", "add.s", "add.u":
		res = new(big.Int).Add(u(x), u(y))
		if op == "add.s" {
			return mask(res), !fitsS(new(big.Int).Add(s(x, w), s(y, w))), true
		}
		return mask(res), op == "add.u" && !fitsU(res), true
	case "sub", "sub.s", "sub.u":
		res = new(big.Int).Sub(u(x), u(y))
		if op == "sub.s" {
			return mask(res), !fitsS(new(big.Int).Sub(s(x, w), s(y, w))), true
		}
		return mask(res), op == "sub.u" && !fitsU(res), true
	case "mul", "mul.s", "mul.u":
		res = new(big.Int).Mul(u(x), u(y))
		if op == "mul.s" {
			return mask(res), !fitsS(new(big.Int).Mul(s(x, w), s(y, w))), true
		}
		return mask(res), op == "mul.u" && !fitsU(res), true
	case "and":
		return x & y, false, true
	case "or":
		return x | y, false, true
	case "xor":
		return x ^ y, false, true
	case "not":
		return mask(new(big.Int).Not(u(x))), false, true
	case "neg", "neg.s":
		n := new(big.Int).Neg(s(x, w))
		return mask(n), op == "neg.s" && !fitsS(n), true
	case "div.u", "rem.u":
		if y == 0 {
			return 0, false, false
		}
		if op == "div.u" {
			return x / y, false, true
		}
		return x % y, false, true
	case "div.s", "rem.s":
		sx, sy := s(x, w), s(y, w)
		if sy.Sign() == 0 || sy.Cmp(big.NewInt(-1)) == 0 && !fitsS(new(big.Int).Neg(sx)) {
			return 0, false, false
		}
		q, r := new(big.Int).QuoRem(sx, sy, new(big.Int))
		if op == "div.s" {
			return mask(q), false, true
		}
		return mask(r), false, true
	case "shl", "shr.u", "shr.s":
		c := y & 0xFFFF
		switch {
		case op == "shl" && c >= uint64(w), op == "shr.u" && c >= uint64(w):
			return 0, false, true
		case op == "shl":
			return mask(new(big.Int).Lsh(u(x), uint(c))), false, true
		case op == "shr.u":
			return x >> c, false, true
		}
		if c >= uint64(w) {
			c = uint64(w - 1)
		}
		return mask(new(big.Int).Rsh(s(x, w), uint(c))), false, true
	case "eq":
		return b2u(x == y), false, true
	case "ne":
		return b2u(x != y), false, true
	case "lt.u":
		return b2u(x < y), false, true
	case "le.u":
		return b2u(x <= y), false, true
	case "lt.s":
		return b2u(s(x, w).Cmp(s(y, w)) < 0), false, true
	case "le.s":
		return b2u(s(x, w).Cmp(s(y, w)) <= 0), false, true
	case "ext.u":
		return x, false, true
	case "ext.s":
		return mask(s(x, from)), false, true
	}
	panic("no semantics for " + op)
}

// checkData checks a rule of an arithmetic, comparison or extension
// operation.
func (h *harness) checkData(r desc.Rule, rng *rand.Rand) {
	w := r.Width
	xw, yw, rw := w, w, w
	unary := false
	switch r.Op {
	case "move", "not", "neg", "neg.s":
		unary = true
	case "ext.s", "ext.u":
		unary, xw = true, r.From
	case "eq", "ne", "lt.s", "lt.u", "le.s", "le.u":
		rw = 8
	case "shl", "shr.u", "shr.s":
		yw = 16
	}
	xs := values(xw, rng)
	ys := values(yw, rng)
	if r.Op == "shl" || r.Op == "shr.u" || r.Op == "shr.s" {
		ys = nil
		for c := 0; c <= w+2; c++ {
			ys = append(ys, uint64(c))
		}
		ys = append(ys, 255, 256, 0xFFFF)
	}
	if unary {
		ys = []uint64{0}
	}
	if w == 8 && !unary && r.Op != "shl" && r.Op != "shr.u" && r.Op != "shr.s" {
		// Exhaustive over both operands, with the operands in slots.
		h.sweep(r, xs, ys, xw, yw, rw, unary, false, false)
		return
	}
	h.sweep(r, xs, ys, xw, yw, rw, unary, false, false)
	h.sweep(r, xs[:min(len(xs), 12)], ys, xw, yw, rw, unary, false, true)
	h.sweep(r, xs, ys[:min(len(ys), 12)], xw, yw, rw, unary, true, false)
}

// sweep runs the rule for each pair of values, with x or y as a constant
// if xImm or yImm is set.
func (h *harness) sweep(r desc.Rule, xs, ys []uint64, xw, yw, rw int, unary, xImm, yImm bool) {
	t := h.t
	fails := 0
	for _, x := range xs {
		for _, y := range ys {
			want, wantTrap, ok := semantics(r.Op, r.Width, r.From, x, y)
			if !ok {
				continue
			}
			ops := map[string]operand{"r": slot(offR, rw), "x": slot(offX, xw)}
			if xImm {
				ops["x"] = imm(x, xw)
			}
			if !unary {
				ops["y"] = slot(offY, yw)
				if yImm {
					ops["y"] = imm(y, yw)
				}
			}
			m, trap, err := h.run(r.Code, ops, func(m *desc.Machine) {
				put(m, offX, xw, x)
				put(m, offY, yw, y)
				put(m, offR, rw, 0x5A5A5A5A5A5A5A5A)
			})
			if err == errNoAssemble {
				return // the template takes no constant here
			}
			if err != nil {
				t.Fatalf("x=%#x y=%#x: %v", x, y, err)
			}
			got := get(m, offR, rw)
			untouched := uint64(0x5A5A5A5A5A5A5A5A) & (1<<uint(rw) - 1)
			if rw == 64 {
				untouched = 0x5A5A5A5A5A5A5A5A
			}
			if trap != wantTrap || !trap && got != want || trap && got != untouched {
				fails++
				if fails <= 5 {
					t.Errorf("x=%#x y=%#x (imm %v %v): got %#x trap %v, want %#x trap %v", x, y, xImm, yImm, got, trap, want, wantTrap)
				}
			}
		}
	}
}

// checkAddr checks the rule of addr: the address of a slot.
func (h *harness) checkAddr(r desc.Rule) {
	x := slot(offX, 0)
	x.width = 0
	m, _, err := h.run(r.Code, map[string]operand{"r": slot(offR, 16), "x": x}, func(*desc.Machine) {})
	if err != nil {
		h.t.Fatal(err)
	}
	if got := get(m, offR, 16); got != uint64(frameBase+offX) {
		h.t.Errorf("addr: got %#x, want %#x", got, frameBase+offX)
	}
}

// checkMemory checks the rules of load and store through an address.
func (h *harness) checkMemory(r desc.Rule, rng *rand.Rand) {
	const area = 0x4000
	w := r.Width
	ptr, pw := uint64(area), 16
	if r.Op == "load.far" || r.Op == "store.far" {
		ptr, pw = 0x0300<<16|0x1000, 32 // 0300:1000 is the physical address 0x4000
	}
	for _, k := range []uint64{0, 1, 6, 255} {
		for _, v := range values(w, rng)[:20] {
			var ops map[string]operand
			load := r.Op == "load" || r.Op == "load.far"
			if load {
				ops = map[string]operand{"r": slot(offR, w), "p": slot(offX, pw), "k": imm(k, 0)}
			} else {
				ops = map[string]operand{"p": slot(offX, pw), "k": imm(k, 0), "x": slot(offY, w)}
			}
			m, _, err := h.run(r.Code, ops, func(m *desc.Machine) {
				put(m, offX, pw, ptr)
				put(m, offY, w, v)
				for i := 0; i < w/8; i++ {
					m.Mem[area+int(k)+i] = byte(v >> uint(8*i))
				}
				if !load {
					for i := 0; i < w/8; i++ {
						m.Mem[area+int(k)+i] = 0xEE
					}
				}
			})
			if err != nil {
				h.t.Fatal(err)
			}
			var got uint64
			if load {
				got = get(m, offR, w)
			} else {
				for i := 0; i < w/8; i++ {
					got |= uint64(m.Mem[area+int(k)+i]) << uint(8*i)
				}
			}
			if got != v {
				h.t.Errorf("%s k=%d: got %#x, want %#x", r.Op, k, got, v)
			}
		}
	}
}

// checkBlock checks the rules of copy and zero.
func (h *harness) checkBlock(r desc.Rule, rng *rand.Rand) {
	const src, dst = 0x4000, 0x5000
	for _, n := range []int{1, 2, 3, 17, 100} {
		data := make([]byte, n)
		rng.Read(data)
		ops := map[string]operand{"p": slot(offX, 16), "n": imm(uint64(n), 0)}
		if r.Op == "copy" {
			ops["q"] = slot(offY, 16)
		}
		m, _, err := h.run(r.Code, ops, func(m *desc.Machine) {
			put(m, offX, 16, dst)
			put(m, offY, 16, src)
			copy(m.Mem[src:], data)
			for i := 0; i < n+2; i++ {
				m.Mem[dst+i] = 0xEE
			}
		})
		if err != nil {
			h.t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			want := data[i]
			if r.Op == "zero" {
				want = 0
			}
			if m.Mem[dst+i] != want {
				h.t.Fatalf("%s n=%d: byte %d is %#x, want %#x", r.Op, n, i, m.Mem[dst+i], want)
			}
		}
		if m.Mem[dst+n] != 0xEE {
			h.t.Errorf("%s n=%d writes past the end", r.Op, n)
		}
	}
}
