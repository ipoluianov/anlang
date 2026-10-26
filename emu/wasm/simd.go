package wasm

import (
	"math"
	"math/bits"
	"strings"
)

// v128 is a vector value as its 16 bytes, lane 0 first.
type v128 [16]byte

func vec(lo, hi uint64) v128 {
	var v v128
	put64(v[:], lo)
	put64(v[8:], hi)
	return v
}

func (v v128) halves() (uint64, uint64) { return le64(v[:]), le64(v[8:]) }

// lane returns lane k of v as a shape of n-bit lanes, zero-extended.
func (v *v128) lane(n, k int) uint64 {
	switch n {
	case 8:
		return uint64(v[k])
	case 16:
		return uint64(le16(v[2*k:]))
	case 32:
		return uint64(le32(v[4*k:]))
	}
	return le64(v[8*k:])
}

func (v *v128) setLane(n, k int, x uint64) {
	switch n {
	case 8:
		v[k] = byte(x)
	case 16:
		put16(v[2*k:], uint16(x))
	case 32:
		put32(v[4*k:], uint32(x))
	default:
		put64(v[8*k:], x)
	}
}

func sext(x uint64, n int) int64 { return int64(x<<uint(64-n)) >> uint(64-n) }

func mask(n int) uint64 {
	if n == 64 {
		return math.MaxUint64
	}
	return 1<<uint(n) - 1
}

// shape returns the lane width and count of a shape such as i16x8.
func shape(s string) (width, count int, float bool) {
	switch s {
	case "i8x16":
		return 8, 16, false
	case "i16x8":
		return 16, 8, false
	case "i32x4":
		return 32, 4, false
	case "i64x2":
		return 64, 2, false
	case "f32x4":
		return 32, 4, true
	case "f64x2":
		return 64, 2, true
	}
	return 0, 0, false
}

func satSigned(x int64, n int) uint64 {
	lo, hi := -int64(1)<<uint(n-1), int64(1)<<uint(n-1)-1
	if x < lo {
		x = lo
	} else if x > hi {
		x = hi
	}
	return uint64(x) & mask(n)
}

func satUnsigned(x int64, n int) uint64 {
	if x < 0 {
		x = 0
	} else if x > int64(mask(n)) {
		x = int64(mask(n))
	}
	return uint64(x)
}

// fl returns lane k of a float shape as float64, and fset stores one.
func (v *v128) fl(n, k int) float64 {
	if n == 32 {
		return float64(math.Float32frombits(uint32(v.lane(32, k))))
	}
	return math.Float64frombits(v.lane(64, k))
}

func (v *v128) fset(n, k int, x float64) {
	if n == 32 {
		v.setLane(32, k, uint64(math.Float32bits(float32(x))))
	} else {
		v.setLane(64, k, math.Float64bits(x))
	}
}

// simd executes a vector instruction and returns the stack pointer.
func (m *machine) simd(in *cinstr, s []uint64, sp int, addr func(a, off, n uint64) uint64, mem *Memory) int {
	name := ops[int(in.op)].name
	sh, op, _ := strings.Cut(name, ".")
	pop := func() v128 {
		sp -= 2
		return vec(s[sp], s[sp+1])
	}
	push := func(v v128) {
		s[sp], s[sp+1] = v.halves()
		sp += 2
	}
	if sh == "v128" && strings.Contains(op, "load") || sh == "v128" && strings.Contains(op, "store") {
		return m.simdMemory(in, op, s, sp, addr, mem)
	}
	w, n, float := shape(sh)
	switch {
	case name == "i8x16.shuffle":
		b, a := pop(), pop()
		var r v128
		idx := vec(in.c, in.d)
		for k := 0; k < 16; k++ {
			l := idx[k]
			if l < 16 {
				r[k] = a[l]
			} else {
				r[k] = b[l-16]
			}
		}
		push(r)
	case name == "i8x16.swizzle":
		b, a := pop(), pop()
		var r v128
		for k := 0; k < 16; k++ {
			if b[k] < 16 {
				r[k] = a[b[k]]
			}
		}
		push(r)
	case op == "splat":
		sp--
		x := s[sp]
		var r v128
		for k := 0; k < n; k++ {
			r.setLane(w, k, x)
		}
		push(r)
	case strings.HasPrefix(op, "extract_lane"):
		a := pop()
		x := a.lane(w, int(in.a))
		if op == "extract_lane_s" {
			x = uint64(uint32(sext(x, w)))
		}
		s[sp] = x
		sp++
	case op == "replace_lane":
		sp--
		x := s[sp]
		a := pop()
		a.setLane(w, int(in.a), x)
		push(a)
	case sh == "v128":
		switch op {
		case "not":
			a := pop()
			for k := range a {
				a[k] = ^a[k]
			}
			push(a)
		case "and", "andnot", "or", "xor":
			b, a := pop(), pop()
			for k := range a {
				switch op {
				case "and":
					a[k] &= b[k]
				case "andnot":
					a[k] &^= b[k]
				case "or":
					a[k] |= b[k]
				case "xor":
					a[k] ^= b[k]
				}
			}
			push(a)
		case "bitselect":
			c, b, a := pop(), pop(), pop()
			for k := range a {
				a[k] = a[k]&c[k] | b[k]&^c[k]
			}
			push(a)
		case "any_true":
			a := pop()
			lo, hi := a.halves()
			s[sp] = b2i(lo|hi != 0)
			sp++
		}
	case op == "all_true":
		a := pop()
		r := uint64(1)
		for k := 0; k < n; k++ {
			if a.lane(w, k) == 0 {
				r = 0
			}
		}
		s[sp] = r
		sp++
	case op == "bitmask":
		a := pop()
		var r uint64
		for k := 0; k < n; k++ {
			r |= a.lane(w, k) >> uint(w-1) << uint(k)
		}
		s[sp] = r
		sp++
	case op == "shl" || op == "shr_s" || op == "shr_u":
		sp--
		c := uint(s[sp]) % uint(w)
		a := pop()
		for k := 0; k < n; k++ {
			x := a.lane(w, k)
			switch op {
			case "shl":
				x <<= c
			case "shr_s":
				x = uint64(sext(x, w) >> c)
			default:
				x >>= c
			}
			a.setLane(w, k, x&mask(w))
		}
		push(a)
	case float:
		push(simdFloat(op, w, n, pop))
	default:
		push(simdInt(op, w, n, pop))
	}
	return sp
}

func (m *machine) simdMemory(in *cinstr, op string, s []uint64, sp int, addr func(a, off, n uint64) uint64, mem *Memory) int {
	var r v128
	switch op {
	case "load":
		ea := addr(s[sp-1], in.c, 16)
		copy(r[:], mem.Data[ea:])
	case "load8x8_s", "load8x8_u", "load16x4_s", "load16x4_u", "load32x2_s", "load32x2_u":
		w := map[byte]int{'8': 8, '1': 16, '3': 32}[op[4]]
		ea := addr(s[sp-1], in.c, 8)
		for k := 0; k < 64/w; k++ {
			var src v128
			copy(src[:8], mem.Data[ea:ea+8])
			x := src.lane(w, k)
			if op[len(op)-1] == 's' {
				x = uint64(sext(x, w))
			}
			r.setLane(2*w, k, x&mask(2*w))
		}
	case "load8_splat", "load16_splat", "load32_splat", "load64_splat":
		w := map[byte]int{'8': 8, '1': 16, '3': 32, '6': 64}[op[4]]
		ea := addr(s[sp-1], in.c, uint64(w/8))
		var src v128
		copy(src[:w/8], mem.Data[ea:])
		for k := 0; k < 128/w; k++ {
			r.setLane(w, k, src.lane(w, 0))
		}
	case "load32_zero", "load64_zero":
		n := uint64(4)
		if op == "load64_zero" {
			n = 8
		}
		ea := addr(s[sp-1], in.c, n)
		copy(r[:n], mem.Data[ea:ea+n])
	case "store":
		sp -= 3
		ea := addr(s[sp], in.c, 16)
		v := vec(s[sp+1], s[sp+2])
		copy(mem.Data[ea:], v[:])
		return sp
	case "load8_lane", "load16_lane", "load32_lane", "load64_lane", "store8_lane", "store16_lane", "store32_lane", "store64_lane":
		w := 8
		switch {
		case strings.Contains(op, "16"):
			w = 16
		case strings.Contains(op, "32"):
			w = 32
		case strings.Contains(op, "64"):
			w = 64
		}
		sp -= 3
		ea := addr(s[sp], in.c, uint64(w/8))
		v := vec(s[sp+1], s[sp+2])
		if strings.HasPrefix(op, "store") {
			var b v128
			b.setLane(w, 0, v.lane(w, int(in.a)))
			copy(mem.Data[ea:ea+uint64(w/8)], b[:w/8])
			return sp
		}
		var b v128
		copy(b[:w/8], mem.Data[ea:])
		v.setLane(w, int(in.a), b.lane(w, 0))
		s[sp], s[sp+1] = v.halves()
		return sp + 2
	}
	sp--
	s[sp], s[sp+1] = r.halves()
	return sp + 2
}

func simdInt(op string, w, n int, pop func() v128) v128 {
	var r v128
	switch op {
	case "abs", "neg", "popcnt":
		a := pop()
		for k := 0; k < n; k++ {
			x := sext(a.lane(w, k), w)
			switch op {
			case "abs":
				if x < 0 {
					x = -x
				}
			case "neg":
				x = -x
			case "popcnt":
				x = int64(bits.OnesCount64(a.lane(w, k)))
			}
			r.setLane(w, k, uint64(x)&mask(w))
		}
		return r
	}
	if strings.HasPrefix(op, "extend_") || strings.HasPrefix(op, "extadd_pairwise") {
		a := pop()
		h := w / 2
		signed := strings.HasSuffix(op, "_s")
		get := func(k int) int64 {
			x := a.lane(h, k)
			if signed {
				return sext(x, h)
			}
			return int64(x)
		}
		for k := 0; k < n; k++ {
			var x int64
			switch {
			case strings.HasPrefix(op, "extend_low"):
				x = get(k)
			case strings.HasPrefix(op, "extend_high"):
				x = get(k + n)
			default:
				x = get(2*k) + get(2*k+1)
			}
			r.setLane(w, k, uint64(x)&mask(w))
		}
		return r
	}
	if strings.HasPrefix(op, "trunc_sat") || strings.HasPrefix(op, "convert") || strings.HasPrefix(op, "demote") || strings.HasPrefix(op, "promote") {
		return simdConvert(op, w, n, pop())
	}
	b, a := pop(), pop()
	if strings.HasPrefix(op, "narrow") {
		h := 2 * w
		for k := 0; k < n; k++ {
			src, j := a, k
			if k >= n/2 {
				src, j = b, k-n/2
			}
			x := sext(src.lane(h, j), h)
			if strings.HasSuffix(op, "_s") {
				r.setLane(w, k, satSigned(x, w))
			} else {
				r.setLane(w, k, satUnsigned(x, w))
			}
		}
		return r
	}
	if strings.HasPrefix(op, "extmul") {
		h := w / 2
		off := 0
		if strings.Contains(op, "high") {
			off = n
		}
		for k := 0; k < n; k++ {
			x, y := a.lane(h, k+off), b.lane(h, k+off)
			var p uint64
			if strings.HasSuffix(op, "_s") {
				p = uint64(sext(x, h) * sext(y, h))
			} else {
				p = x * y
			}
			r.setLane(w, k, p&mask(w))
		}
		return r
	}
	if op == "dot_i16x8_s" {
		for k := 0; k < n; k++ {
			p := sext(a.lane(16, 2*k), 16)*sext(b.lane(16, 2*k), 16) + sext(a.lane(16, 2*k+1), 16)*sext(b.lane(16, 2*k+1), 16)
			r.setLane(32, k, uint64(p)&mask(32))
		}
		return r
	}
	for k := 0; k < n; k++ {
		ux, uy := a.lane(w, k), b.lane(w, k)
		x, y := sext(ux, w), sext(uy, w)
		var z uint64
		switch op {
		case "eq":
			z = mask(w) * b2i(ux == uy)
		case "ne":
			z = mask(w) * b2i(ux != uy)
		case "lt_s":
			z = mask(w) * b2i(x < y)
		case "lt_u":
			z = mask(w) * b2i(ux < uy)
		case "gt_s":
			z = mask(w) * b2i(x > y)
		case "gt_u":
			z = mask(w) * b2i(ux > uy)
		case "le_s":
			z = mask(w) * b2i(x <= y)
		case "le_u":
			z = mask(w) * b2i(ux <= uy)
		case "ge_s":
			z = mask(w) * b2i(x >= y)
		case "ge_u":
			z = mask(w) * b2i(ux >= uy)
		case "add":
			z = ux + uy
		case "sub":
			z = ux - uy
		case "mul":
			z = ux * uy
		case "add_sat_s":
			z = satSigned(x+y, w)
		case "add_sat_u":
			z = satUnsigned(int64(ux+uy), w)
		case "sub_sat_s":
			z = satSigned(x-y, w)
		case "sub_sat_u":
			z = satUnsigned(int64(ux)-int64(uy), w)
		case "min_s":
			z = uint64(min(x, y))
		case "min_u":
			z = min(ux, uy)
		case "max_s":
			z = uint64(max(x, y))
		case "max_u":
			z = max(ux, uy)
		case "avgr_u":
			z = (ux + uy + 1) >> 1
		case "q15mulr_sat_s":
			z = satSigned((x*y+0x4000)>>15, 16)
		}
		r.setLane(w, k, z&mask(w))
	}
	return r
}

func simdFloat(op string, w, n int, pop func() v128) v128 {
	var r v128
	round := func(x float64) float64 {
		if w == 32 {
			return float64(float32(x))
		}
		return x
	}
	switch op {
	case "abs", "neg":
		a := pop()
		for k := 0; k < n; k++ {
			x := a.lane(w, k)
			if op == "abs" {
				x &^= 1 << uint(w-1)
			} else {
				x ^= 1 << uint(w-1)
			}
			r.setLane(w, k, x)
		}
		return r
	case "sqrt", "ceil", "floor", "trunc", "nearest":
		a := pop()
		for k := 0; k < n; k++ {
			x := a.fl(w, k)
			switch op {
			case "sqrt":
				x = math.Sqrt(x)
			case "ceil":
				x = math.Ceil(x)
			case "floor":
				x = math.Floor(x)
			case "trunc":
				x = math.Trunc(x)
			case "nearest":
				x = math.RoundToEven(x)
			}
			r.fset(w, k, round(x))
		}
		return r
	}
	if strings.HasPrefix(op, "convert") || strings.HasPrefix(op, "demote") || strings.HasPrefix(op, "promote") {
		return simdConvert(op, w, n, pop())
	}
	b, a := pop(), pop()
	for k := 0; k < n; k++ {
		x, y := a.fl(w, k), b.fl(w, k)
		var z float64
		cmp := -1
		switch op {
		case "eq":
			cmp = int(b2i(x == y))
		case "ne":
			cmp = int(b2i(x != y))
		case "lt":
			cmp = int(b2i(x < y))
		case "gt":
			cmp = int(b2i(x > y))
		case "le":
			cmp = int(b2i(x <= y))
		case "ge":
			cmp = int(b2i(x >= y))
		case "add":
			z = round(x + y)
		case "sub":
			z = round(x - y)
		case "mul":
			z = round(x * y)
		case "div":
			z = round(x / y)
		case "min":
			z = fmin(x, y)
		case "max":
			z = fmax(x, y)
		case "pmin", "pmax":
			pick := y < x
			if op == "pmax" {
				pick = x < y
			}
			if pick {
				r.setLane(w, k, b.lane(w, k))
			} else {
				r.setLane(w, k, a.lane(w, k))
			}
			continue
		}
		if cmp >= 0 {
			r.setLane(w, k, mask(w)*uint64(cmp))
		} else {
			r.fset(w, k, z)
		}
	}
	return r
}

// simdConvert converts between integer and floating-point vectors; w
// and n are of the result shape.
func simdConvert(op string, w, n int, a v128) v128 {
	var r v128
	switch op {
	case "trunc_sat_f32x4_s", "trunc_sat_f32x4_u":
		for k := 0; k < 4; k++ {
			x := a.fl(32, k)
			if strings.HasSuffix(op, "_s") {
				r.setLane(32, k, uint64(satS(x, 32))&mask(32))
			} else {
				r.setLane(32, k, satU(x, 32))
			}
		}
	case "trunc_sat_f64x2_s_zero", "trunc_sat_f64x2_u_zero":
		for k := 0; k < 2; k++ {
			x := a.fl(64, k)
			if strings.Contains(op, "_s_") {
				r.setLane(32, k, uint64(satS(x, 32))&mask(32))
			} else {
				r.setLane(32, k, satU(x, 32))
			}
		}
	case "convert_i32x4_s", "convert_i32x4_u":
		for k := 0; k < 4; k++ {
			x := a.lane(32, k)
			var f float32
			if strings.HasSuffix(op, "_s") {
				f = float32(int32(x))
			} else {
				f = float32(uint32(x))
			}
			r.setLane(32, k, uint64(math.Float32bits(f)))
		}
	case "convert_low_i32x4_s", "convert_low_i32x4_u":
		for k := 0; k < 2; k++ {
			x := a.lane(32, k)
			f := float64(uint32(x))
			if strings.HasSuffix(op, "_s") {
				f = float64(int32(x))
			}
			r.fset(64, k, f)
		}
	case "demote_f64x2_zero":
		for k := 0; k < 2; k++ {
			r.setLane(32, k, uint64(math.Float32bits(float32(a.fl(64, k)))))
		}
	case "promote_low_f32x4":
		for k := 0; k < 2; k++ {
			r.fset(64, k, a.fl(32, k))
		}
	}
	return r
}
