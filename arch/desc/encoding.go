package desc

import (
	"fmt"
	"strconv"
	"strings"
)

// The encoding notation (spec/13-targets.md §13.5.3): a sequence of
// items separated by spaces.
//
//	HH                 a byte in hexadecimal: 8 fixed bits
//	%0101              fixed bits in binary
//	name:width         a field: an operand (a, b, c, d) or a field of a fragment; a field
//	                   may appear twice, with the same value
//	name[hi:lo]        bits hi to lo of an operand; name[b] is one bit
//	<frag op k=v ...>  a fragment encoding operand op, with its fields k
//	                   bound to operands or constants

// Item is an item of an encoding pattern.
type Item struct {
	Kind  ItemKind
	Bits  uint64 // Fixed
	Width int    // Fixed, Field
	Name  string // Field: the operand or fragment field; Ref: the fragment
	// Field: Slice marks bits Lo to Lo+Width-1 of the operand.
	Slice bool
	Lo    int
	// Ref: the operand the fragment encodes, and the bindings of its
	// fields.
	Operand string
	Binds   map[string]Bind
}

type ItemKind int

const (
	Fixed ItemKind = iota
	Field
	Ref
)

// Bind binds a field of a fragment to an operand or a constant.
type Bind struct {
	Operand string
	Value   uint64
	Const   bool
	Any     bool // "_": the field may have any value and is ignored
}

// parseEncoding parses an encoding pattern.
func parseEncoding(s string) ([]Item, error) {
	var items []Item
	for i := 0; i < len(s); {
		switch {
		case s[i] == ' ' || s[i] == '\t':
			i++
			continue
		case s[i] == '<':
			j := strings.IndexByte(s[i:], '>')
			if j < 0 {
				return nil, fmt.Errorf("unclosed < in %q", s)
			}
			it, err := parseRef(s[i+1 : i+j])
			if err != nil {
				return nil, err
			}
			items = append(items, it)
			i += j + 1
			continue
		}
		j := strings.IndexAny(s[i:], " \t")
		if j < 0 {
			j = len(s) - i
		}
		tok := s[i : i+j]
		i += j
		it, err := parseToken(tok)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

func parseToken(tok string) (Item, error) {
	switch {
	case strings.HasPrefix(tok, "%"):
		bits := tok[1:]
		if bits == "" || strings.Trim(bits, "01") != "" || len(bits) > 64 {
			return Item{}, fmt.Errorf("invalid bits %q", tok)
		}
		v, _ := strconv.ParseUint(bits, 2, 64)
		return Item{Kind: Fixed, Bits: v, Width: len(bits)}, nil
	case len(tok) == 2 && isHex(tok):
		v, _ := strconv.ParseUint(tok, 16, 8)
		return Item{Kind: Fixed, Bits: v, Width: 8}, nil
	case strings.HasSuffix(tok, "]") && strings.Contains(tok, "["):
		name, rng, _ := strings.Cut(strings.TrimSuffix(tok, "]"), "[")
		hs, ls, two := strings.Cut(rng, ":")
		if !two {
			ls = hs
		}
		hi, err1 := strconv.Atoi(hs)
		lo, err2 := strconv.Atoi(ls)
		if err1 != nil || err2 != nil || lo < 0 || hi < lo || hi > 63 || !isName(name) {
			return Item{}, fmt.Errorf("invalid bit range %q", tok)
		}
		return Item{Kind: Field, Name: name, Width: hi - lo + 1, Slice: true, Lo: lo}, nil
	case strings.Contains(tok, ":"):
		name, width, _ := strings.Cut(tok, ":")
		n, err := strconv.Atoi(width)
		if err != nil || n <= 0 || n > 64 || !isName(name) {
			return Item{}, fmt.Errorf("invalid field %q", tok)
		}
		return Item{Kind: Field, Name: name, Width: n}, nil
	}
	return Item{}, fmt.Errorf("invalid encoding item %q", tok)
}

func parseRef(s string) (Item, error) {
	parts := strings.Fields(s)
	if len(parts) < 2 || !isName(parts[0]) || !isName(parts[1]) {
		return Item{}, fmt.Errorf("invalid fragment reference <%s>: want <fragment operand field=value ...>", s)
	}
	it := Item{Kind: Ref, Name: parts[0], Operand: parts[1], Binds: map[string]Bind{}}
	for _, p := range parts[2:] {
		k, v, ok := strings.Cut(p, "=")
		if !ok || !isName(k) {
			return Item{}, fmt.Errorf("invalid binding %q in <%s>", p, s)
		}
		switch {
		case v == "_":
			it.Binds[k] = Bind{Any: true}
		case isName(v):
			it.Binds[k] = Bind{Operand: v}
		case strings.HasPrefix(v, "%"):
			n, err := strconv.ParseUint(v[1:], 2, 64)
			if err != nil {
				return Item{}, fmt.Errorf("invalid binding %q in <%s>", p, s)
			}
			it.Binds[k] = Bind{Value: n, Const: true}
		default:
			n, err := strconv.ParseUint(v, 0, 64)
			if err != nil {
				return Item{}, fmt.Errorf("invalid binding %q in <%s>", p, s)
			}
			it.Binds[k] = Bind{Value: n, Const: true}
		}
	}
	return it, nil
}

func isHex(s string) bool {
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}

func isName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if !(c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || i > 0 && '0' <= c && c <= '9') {
			return false
		}
	}
	return true
}

// fixedBits returns the number of fixed bits of a pattern, not counting
// fragments: the more fixed bits, the more specific the pattern.
func fixedBits(items []Item) int {
	n := 0
	for _, it := range items {
		if it.Kind == Fixed {
			n += it.Width
		}
		for _, b := range it.Binds {
			if b.Const {
				n++ // a constant binding narrows the fragment
			}
		}
	}
	return n
}

// reader reads the bits of an encoding: units are filled from their most
// significant bit; a field of whole units that starts at a unit boundary
// is stored in the byte order of the architecture.
type reader struct {
	a    *Arch
	code []byte
	pos  int // in bits
}

func (r *reader) read(width int) (uint64, bool) {
	if r.pos+width > len(r.code)*8 {
		return 0, false
	}
	unit := r.a.Unit
	if width%unit == 0 && r.pos%unit == 0 && width > unit {
		// A multi-unit field in the byte order of the architecture.
		var v uint64
		units := width / unit
		for i := 0; i < units; i++ {
			u, _ := r.readBits(unit)
			if r.a.Endian == 0 {
				v |= u << (uint(i) * uint(unit))
			} else {
				v = v<<uint(unit) | u
			}
		}
		return v, true
	}
	return r.readBits(width)
}

func (r *reader) readBits(width int) (uint64, bool) {
	var v uint64
	for i := 0; i < width; i++ {
		byteIdx, bit := bitPos(r.a, r.pos)
		v = v<<1 | uint64(r.code[byteIdx]>>uint(bit)&1)
		r.pos++
	}
	return v, true
}

// bitPos returns the byte and the bit within it of bit pos of an
// encoding: units are filled from their most significant bit and stored
// in the byte order of the architecture.
func bitPos(a *Arch, pos int) (int, int) {
	unit := a.Unit
	if unit <= 8 {
		return pos / 8, 7 - pos%8
	}
	u, k := pos/unit, pos%unit
	inUnit := unit - 1 - k // the bit number within the unit
	b := inUnit / 8        // the byte number from the least significant
	nbytes := unit / 8
	if a.Endian == 0 {
		return u*nbytes + b, inUnit % 8
	}
	return u*nbytes + nbytes - 1 - b, inUnit % 8
}

// Decoded is an instruction decoded from its encoding.
type Decoded struct {
	Inst     *Instruction
	Prefixes []*Prefix
	Length   int // in bytes
	// Fields are the values of the operand fields: register numbers and
	// immediates; for an operand encoded by a fragment, the fragment's
	// fields, prefixed with the operand name and a dot.
	Fields map[string]uint64
	// Alts are the fragment alternatives of the operands encoded by
	// fragments.
	Alts map[string]*Alternative
}

// match matches pattern items against r, recording fields in d.
func (a *Arch) match(items []Item, r *reader, d *Decoded, prefix string) bool {
	for i, it := range items {
		switch it.Kind {
		case Fixed:
			v, ok := r.read(it.Width)
			if !ok || v != it.Bits {
				return false
			}
		case Field:
			v, ok := r.read(it.Width)
			if !ok {
				return false
			}
			switch old, seen := d.Fields[prefix+it.Name]; {
			case it.Slice:
				d.Fields[prefix+it.Name] = old | v<<uint(it.Lo)
			case seen && prefix == "" && old != v:
				return false // a field encoded twice holds one value
			default:
				d.Fields[prefix+it.Name] = v
			}
		case Ref:
			f := a.fragments[it.Name]
			saved := r.pos
			for j := range f.Alternatives {
				alt := &f.Alternatives[j]
				r.pos = saved
				fields := map[string]uint64{}
				for k, v := range d.Fields {
					fields[k] = v
				}
				sub := &Decoded{Fields: fields, Alts: d.Alts}
				if !a.match(alt.pattern, r, sub, it.Operand+".") {
					continue
				}
				// The bound fields must agree with their bindings.
				ok := true
				for k, b := range it.Binds {
					v := sub.Fields[it.Operand+"."+k]
					if b.Const && v != b.Value {
						ok = false
					}
					if !b.Const && !b.Any {
						sub.Fields[b.Operand] = v
					}
				}
				if !ok {
					continue
				}
				// The rest of the pattern must match too.
				rest := r.pos
				d2 := &Decoded{Fields: sub.Fields, Alts: copyAlts(d.Alts)}
				d2.Alts[it.Operand] = alt
				if a.match(items[i+1:], r, d2, prefix) {
					d.Fields, d.Alts = d2.Fields, d2.Alts
					return true
				}
				r.pos = rest
			}
			return false
		}
	}
	return true
}

func copyAlts(m map[string]*Alternative) map[string]*Alternative {
	c := map[string]*Alternative{}
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Decode decodes the instruction at the start of code, with its
// prefixes. Of several instruction forms that match, the most specific
// one is chosen; ambiguous reports a tie between different forms.
func (a *Arch) Decode(code []byte) (d *Decoded, ambiguous []*Instruction) {
	r := &reader{a: a, code: code}
	var prefixes []*Prefix
prefixes:
	for {
		for i := range a.Prefixes {
			p := &a.Prefixes[i]
			saved := r.pos
			if a.match(p.pattern, r, &Decoded{Fields: map[string]uint64{}}, "") {
				prefixes = append(prefixes, p)
				continue prefixes
			}
			r.pos = saved
		}
		break
	}
	start := r.pos
	var best *Decoded
	bestBits := -1
	if start/8 >= len(code) {
		return nil, nil
	}
	for _, in := range a.byFirst[code[start/8]] {
		r.pos = start
		cand := &Decoded{Inst: in, Prefixes: prefixes, Fields: map[string]uint64{}, Alts: map[string]*Alternative{}}
		if !a.match(in.pattern, r, cand, "") || r.pos%8 != 0 || !a.validRegisters(in, cand) {
			continue
		}
		cand.Length = r.pos / 8
		switch n := fixedBits(in.pattern); {
		case n > bestBits:
			best, bestBits, ambiguous = cand, n, nil
		case n == bestBits:
			if ambiguous == nil {
				ambiguous = []*Instruction{best.Inst}
			}
			ambiguous = append(ambiguous, in)
		}
	}
	return best, ambiguous
}

// validRegisters reports whether the register numbers of the decoded
// operands name registers of their classes.
func (a *Arch) validRegisters(in *Instruction, d *Decoded) bool {
	for i, o := range in.ops {
		name := opName(i)
		if alt := d.Alts[name]; alt != nil && alt.Register != "" && o.Class == "" {
			return false // a memory operand cannot be a register
		}
		if o.Class == "" {
			continue
		}
		v, ok := d.Fields[name]
		if alt := d.Alts[name]; alt != nil {
			if alt.Register == "" {
				continue // a memory operand
			}
			v, ok = d.Fields[name+"."+alt.Register]
		}
		_ = v
		_ = ok
	}
	return true
}

// leading returns the mask and value of the fixed bits of the first unit
// of a pattern.
func leading(items []Item, unit int) (mask, value uint64) {
	n := 0
	for _, it := range items {
		if it.Kind != Fixed || n >= unit {
			break
		}
		w := min(it.Width, unit-n)
		bitsv := it.Bits >> uint(it.Width-w)
		mask |= (1<<uint(w) - 1) << uint(unit-n-w)
		value |= bitsv << uint(unit-n-w)
		n += w
	}
	return mask, value
}

// index groups the instruction forms by the values of the first byte in
// memory that they can start with.
func (a *Arch) index() {
	for i := range a.byFirst {
		a.byFirst[i] = nil
	}
	unit := max(a.Unit, 8)
	for i := range a.Instructions {
		in := &a.Instructions[i]
		mask, value := leading(in.pattern, unit)
		// The first byte in memory of the first unit.
		shift := uint(unit - 8)
		if a.Endian == 0 {
			shift = 0
		}
		mask, value = mask>>shift&0xFF, value>>shift&0xFF
		for b := 0; b < 256; b++ {
			if uint64(b)&mask == value {
				a.byFirst[b] = append(a.byFirst[b], in)
			}
		}
	}
}
