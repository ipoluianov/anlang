package desc

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The assembler and the disassembler of an architecture, derived from its
// description (target.arch.generated). The syntax is the one of the Intel
// manuals in a uniform form: prefixes, the mnemonic and the operands
// separated by commas. A register is its name; an immediate is a number;
// a relative operand is the target address; a memory operand is
// "byte [address]", "word [address]" or "dword [address]" by its width, or
// "[address]" for an operand whose address only is used, where the
// address is a sum of registers and a displacement, as the alternatives
// of its fragment allow; a far pointer is "segment:offset".

// Disasm returns the assembler text of a decoded instruction at address
// pc.
func (a *Arch) Disasm(d *Decoded) string {
	var parts []string
	for _, p := range d.Prefixes {
		parts = append(parts, p.Name)
	}
	parts = append(parts, d.Inst.Mnemonic)
	text := strings.Join(parts, " ")
	var ops []string
	for i, o := range d.Inst.ops {
		ops = append(ops, a.operandText(d, i, o))
	}
	if len(ops) > 0 {
		text += " " + strings.Join(ops, ", ")
	}
	return text
}

func hexNum(v uint64) string { return "0x" + strings.ToUpper(strconv.FormatUint(v, 16)) }

func sizeWord(w int) string {
	switch w {
	case 8:
		return "byte"
	case 16:
		return "word"
	case 32:
		return "dword"
	case 64:
		return "qword"
	}
	return ""
}

func (a *Arch) operandText(d *Decoded, i int, o *Operand) string {
	name := opName(i)
	switch {
	case o.Register != "":
		return o.Register
	case o.Constant:
		return strconv.Itoa(o.Value)
	case o.Imm != 0:
		w := o.ValueWidth(a)
		v := o.bits(a, d.Fields[name]) & mask(w)
		if o.Relative {
			// The target, from the base of an instruction at address 0.
			base := uint64(d.Length)
			if a.PCRead != 0 {
				base = uint64(a.PCRead)
			}
			v = (base + v) & mask(w)
		}
		if o.Imm == 32 && o.Name == "ptr16:16" {
			return hexNum(v>>16) + ":" + hexNum(v&0xFFFF)
		}
		return hexNum(v)
	}
	if alt := d.Alts[name]; alt != nil {
		if alt.Register != "" {
			return a.classReg(o.Class, d.Fields[name+"."+alt.Register]).Name
		}
		addr := a.addressText(alt.address, func(field string) (uint64, int) {
			for _, it := range alt.pattern {
				if it.Kind == Field && it.Name == field {
					return d.Fields[name+"."+field], it.Width
				}
			}
			return 0, 0
		})
		if s := sizeWord(o.Memory); s != "" {
			return s + " [" + addr + "]"
		}
		return "[" + addr + "]"
	}
	return a.classReg(o.Class, d.Fields[name]).Name
}

// addressText writes an address expression with the values of its fields.
func (a *Arch) addressText(e Expr, field func(string) (uint64, int)) string {
	var terms []string
	var walk func(Expr, bool)
	walk = func(e Expr, neg bool) {
		switch e := e.(type) {
		case *Binary:
			if e.Op == "+" || e.Op == "-" {
				walk(e.X, neg)
				walk(e.Y, neg != (e.Op == "-"))
				return
			}
		case *Ident:
			if a.regs[e.Name] != nil {
				terms = append(terms, e.Name)
				return
			}
			v, _ := field(e.Name)
			terms = append(terms, hexNum(v))
			return
		case *CallExpr:
			if e.Fun == "sx" && len(e.Args) == 2 {
				if id, ok := e.Args[0].(*Ident); ok {
					v, w := field(id.Name)
					if s := int64(signExtend(v, w)); s < 0 {
						terms = append(terms, "-"+hexNum(uint64(-s)))
					} else {
						terms = append(terms, hexNum(uint64(s)))
					}
					return
				}
			}
		}
		terms = append(terms, "?")
	}
	walk(e, false)
	s := strings.Join(terms, "+")
	return strings.ReplaceAll(s, "+-", "-")
}

// AsmError is an error in assembler text.
type AsmError struct {
	Text string
	Msg  string
}

func (e *AsmError) Error() string { return fmt.Sprintf("%q: %s", e.Text, e.Msg) }

// asmOperand is a parsed operand.
type asmOperand struct {
	text   string
	reg    *Register
	num    uint64
	isNum  bool
	far    bool // segment:offset
	seg    uint64
	mem    bool
	size   int // the width of a memory operand; 0 if not given
	regs   []string
	disp   int64
	hasNum bool // the address has a displacement
}

// Asm assembles one instruction at address 0.
func (a *Arch) Asm(text string) ([]byte, error) {
	return a.AsmAt(text, 0)
}

// AsmAt assembles one instruction at address pc: a relative operand is
// the target address.
func (a *Arch) AsmAt(text string, pc uint64) ([]byte, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return nil, &AsmError{text, "no instruction"}
	}
	// Prefixes, then the mnemonic.
	var prefixes []*Prefix
	i := 0
	for ; i < len(fields); i++ {
		p := a.prefix(fields[i])
		if p == nil || i == len(fields)-1 && a.isMnemonic(fields[i]) {
			break
		}
		prefixes = append(prefixes, p)
	}
	if i >= len(fields) {
		return nil, &AsmError{text, "no mnemonic"}
	}
	mnemonic := strings.ToUpper(fields[i])
	rest := strings.TrimSpace(strings.Join(fields[i+1:], " "))
	var ops []asmOperand
	if rest != "" {
		for _, s := range strings.Split(rest, ",") {
			op, err := a.parseOperand(strings.TrimSpace(s))
			if err != nil {
				return nil, &AsmError{text, err.Error()}
			}
			ops = append(ops, op)
		}
	}
	var out []byte
	for _, p := range prefixes {
		b, err := a.encode(p.pattern, nil, nil, nil)
		if err != nil {
			return nil, &AsmError{text, err.Error()}
		}
		out = append(out, b...)
	}
	type candidate struct {
		code []byte
		in   *Instruction
	}
	var cands []candidate
	var lastErr error
	for j := range a.Instructions {
		in := &a.Instructions[j]
		if in.Mnemonic != mnemonic || len(in.ops) != len(ops) {
			continue
		}
		code, err := a.encodeInst(in, ops, int(pc)+len(out))
		if err != nil {
			lastErr = err
			continue
		}
		cands = append(cands, candidate{code, in})
	}
	if len(cands) == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("no instruction form %s with %d operands", mnemonic, len(ops))
		}
		return nil, &AsmError{text, lastErr.Error()}
	}
	// The shortest encoding; of equal ones the first form described.
	sort.SliceStable(cands, func(x, y int) bool { return len(cands[x].code) < len(cands[y].code) })
	return append(out, cands[0].code...), nil
}

func (a *Arch) prefix(name string) *Prefix {
	for i := range a.Prefixes {
		if strings.EqualFold(a.Prefixes[i].Name, name) {
			return &a.Prefixes[i]
		}
	}
	return nil
}

func (a *Arch) isMnemonic(name string) bool {
	for i := range a.Instructions {
		if strings.EqualFold(a.Instructions[i].Mnemonic, name) {
			return true
		}
	}
	return false
}

func parseNum(s string) (int64, bool) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	v, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		return -int64(v), true
	}
	return int64(v), true
}

func (a *Arch) parseOperand(s string) (asmOperand, error) {
	op := asmOperand{text: s}
	if r := a.regs[strings.ToUpper(s)]; r != nil {
		op.reg = r
		return op, nil
	}
	if seg, off, ok := strings.Cut(s, ":"); ok && !strings.Contains(s, "[") {
		sv, ok1 := parseNum(seg)
		ov, ok2 := parseNum(off)
		if !ok1 || !ok2 {
			return op, fmt.Errorf("invalid far pointer %q", s)
		}
		op.far, op.seg, op.num = true, uint64(sv), uint64(ov)
		return op, nil
	}
	if v, ok := parseNum(s); ok {
		op.isNum, op.num = true, uint64(v)
		return op, nil
	}
	lb := strings.IndexByte(s, '[')
	if lb < 0 || !strings.HasSuffix(s, "]") {
		return op, fmt.Errorf("invalid operand %q", s)
	}
	op.mem = true
	switch strings.ToLower(strings.TrimSpace(s[:lb])) {
	case "":
	case "byte":
		op.size = 8
	case "word":
		op.size = 16
	case "dword":
		op.size = 32
	case "qword":
		op.size = 64
	default:
		return op, fmt.Errorf("invalid size in %q", s)
	}
	inner := strings.ReplaceAll(s[lb+1:len(s)-1], " ", "")
	inner = strings.ReplaceAll(inner, "-", "+-")
	for _, t := range strings.Split(inner, "+") {
		if t == "" {
			continue
		}
		if r := a.regs[strings.ToUpper(t)]; r != nil {
			op.regs = append(op.regs, r.Name)
			continue
		}
		v, ok := parseNum(t)
		if !ok {
			return op, fmt.Errorf("invalid address term %q", t)
		}
		op.disp += v
		op.hasNum = true
	}
	sort.Strings(op.regs)
	return op, nil
}

// addrShape is the shape of the address of a fragment alternative: its
// registers and its displacement field.
type addrShape struct {
	regs   []string
	field  string
	width  int
	signed bool
	ok     bool
	// unsigned marks zx(field): the displacement is not negative; neg
	// marks a field that is subtracted; scale multiplies the field.
	unsigned bool
	neg      bool
	scale    int64
}

func (a *Arch) shape(alt *Alternative) addrShape {
	sh := addrShape{ok: true, scale: 1}
	widths := map[string]int{}
	for _, it := range alt.pattern {
		if it.Kind == Field {
			widths[it.Name] = it.Width
		}
	}
	// field recognizes a displacement: a field, sx(field), zx(field, n),
	// each optionally scaled by a constant factor.
	var field func(Expr, bool) bool
	field = func(e Expr, neg bool) bool {
		switch e := e.(type) {
		case *Ident:
			if w, ok := widths[e.Name]; ok && sh.field == "" {
				sh.field, sh.width, sh.neg = e.Name, w, neg
				return true
			}
		case *CallExpr:
			if id, ok := e.Args[0].(*Ident); ok && (e.Fun == "sx" || e.Fun == "zx") {
				if w, ok := widths[id.Name]; ok && sh.field == "" {
					sh.field, sh.width, sh.neg = id.Name, w, neg
					sh.signed, sh.unsigned = e.Fun == "sx", e.Fun == "zx"
					return true
				}
			}
		case *Binary:
			if n, ok := e.Y.(*Num); ok && (e.Op == "*" || e.Op == "<<") {
				if field(e.X, neg) {
					if e.Op == "*" {
						sh.scale = int64(n.Val)
					} else {
						sh.scale = 1 << n.Val
					}
					return true
				}
			}
		}
		return false
	}
	var walk func(Expr)
	walk = func(e Expr) {
		switch e := e.(type) {
		case *Binary:
			if e.Op == "+" {
				walk(e.X)
				walk(e.Y)
				return
			}
			if e.Op == "-" {
				walk(e.X)
				if !field(e.Y, true) {
					sh.ok = false
				}
				return
			}
		case *Ident:
			if a.regs[e.Name] != nil {
				sh.regs = append(sh.regs, e.Name)
				return
			}
		}
		if !field(e, false) {
			sh.ok = false
		}
	}
	walk(alt.address)
	sort.Strings(sh.regs)
	return sh
}

// encodeInst encodes instruction form in with the operands, at offset
// start of the encoding; it fails if an operand does not fit.
func (a *Arch) encodeInst(in *Instruction, ops []asmOperand, start int) ([]byte, error) {
	vals := map[string]uint64{}
	alts := map[string]*Alternative{}
	altFields := map[string]map[string]uint64{}
	var relOps []int
	for i, o := range in.ops {
		op := ops[i]
		name := opName(i)
		switch {
		case o.Register != "":
			if op.reg == nil || op.reg.Name != o.Register {
				return nil, fmt.Errorf("operand %d is not %s", i+1, o.Register)
			}
		case o.Constant:
			if !op.isNum || op.num != uint64(o.Value) {
				return nil, fmt.Errorf("operand %d is not %d", i+1, o.Value)
			}
		case o.Imm != 0 && o.Name == "ptr16:16":
			if !op.far {
				return nil, fmt.Errorf("operand %d is not a far pointer", i+1)
			}
			vals[name] = op.seg<<16 | op.num
		case o.Imm != 0:
			if !op.isNum {
				return nil, fmt.Errorf("operand %d is not a number", i+1)
			}
			if o.Relative {
				relOps = append(relOps, i)
				continue
			}
			if o.Scale != 0 || o.Transform != "" {
				w := o.ValueWidth(a)
				v := op.num & mask(w)
				s := int64(v)
				if o.Signed {
					s = int64(signExtend(v, w))
				}
				f, ok := o.field(a, s)
				if !ok {
					return nil, fmt.Errorf("operand %d cannot be encoded in %d bits scaled by %d", i+1, o.Imm, o.Scale)
				}
				vals[name] = f
				continue
			}
			v := op.num & mask(o.ValueWidth(a))
			if o.Signed {
				s := int64(signExtend(v, o.ValueWidth(a)))
				if s < -(1<<(o.Imm-1)) || s >= 1<<(o.Imm-1) {
					return nil, fmt.Errorf("operand %d does not fit in %d signed bits", i+1, o.Imm)
				}
				v &= mask(o.Imm)
			} else if int64(op.num) < -(1<<(o.Imm-1)) || op.num > mask(o.Imm) && int64(op.num) >= 0 {
				return nil, fmt.Errorf("operand %d does not fit in %d bits", i+1, o.Imm)
			}
			vals[name] = v & mask(o.Imm)
		case op.reg != nil && o.Class != "" && op.reg.Class == o.Class && (o.Registers == 0 || op.reg.Number < o.Registers):
			if o.Fragment != "" {
				for j := range a.fragments[o.Fragment].Alternatives {
					alt := &a.fragments[o.Fragment].Alternatives[j]
					if alt.Register != "" {
						alts[name] = alt
						altFields[name] = map[string]uint64{alt.Register: uint64(op.reg.Number)}
						break
					}
				}
			}
			vals[name] = uint64(op.reg.Number)
		case op.mem && o.Memory != 0:
			if op.size != 0 && op.size != o.Memory || o.Memory < 0 && op.size != 0 {
				return nil, fmt.Errorf("operand %d has the wrong size", i+1)
			}
			alt, fv, err := a.chooseAlt(o.Fragment, op)
			if err != nil {
				return nil, err
			}
			alts[name], altFields[name] = alt, fv
		default:
			return nil, fmt.Errorf("operand %d does not fit %s", i+1, o.Name)
		}
	}
	// A relative operand depends on the length, which does not depend on
	// it: encode with zero, then with the displacement.
	for _, i := range relOps {
		vals[opName(i)] = 0
	}
	code, err := a.encode(in.pattern, vals, alts, altFields)
	if err != nil {
		return nil, err
	}
	if len(relOps) > 0 {
		for _, i := range relOps {
			o := in.ops[i]
			target := int64(ops[i].num)
			w := o.ValueWidth(a)
			base := int64(start + len(code))
			if a.PCRead != 0 {
				base = int64(start + a.PCRead)
			}
			// The displacement wraps around with the program counter.
			disp := int64(signExtend(uint64(target-base)&mask(w), w))
			if o.Scale != 0 || o.Transform != "" {
				f, ok := o.field(a, disp)
				if !ok {
					return nil, fmt.Errorf("target %#x is out of range or misaligned", target)
				}
				vals[opName(i)] = f
				continue
			}
			if o.Signed || o.Imm < w {
				if disp < -(1<<(o.Imm-1)) || disp >= 1<<(o.Imm-1) {
					return nil, fmt.Errorf("target %#x is out of range", target)
				}
			}
			vals[opName(i)] = uint64(disp) & mask(o.Imm)
		}
		code, err = a.encode(in.pattern, vals, alts, altFields)
	}
	return code, err
}

// chooseAlt chooses the shortest alternative of a fragment that encodes a
// memory operand.
func (a *Arch) chooseAlt(frag string, op asmOperand) (*Alternative, map[string]uint64, error) {
	f := a.fragments[frag]
	var best *Alternative
	var bestFields map[string]uint64
	bestLen := 1 << 30
	for j := range f.Alternatives {
		alt := &f.Alternatives[j]
		if alt.Register != "" {
			continue
		}
		sh := a.shape(alt)
		if !sh.ok || strings.Join(sh.regs, "+") != strings.Join(op.regs, "+") {
			continue
		}
		fields := map[string]uint64{}
		if sh.field == "" {
			if op.disp != 0 {
				continue
			}
		} else {
			d := op.disp
			if sh.neg {
				d = -d
			}
			if d%sh.scale != 0 {
				continue
			}
			d /= sh.scale
			switch {
			case sh.signed:
				if d < -(1<<(sh.width-1)) || d >= 1<<(sh.width-1) {
					continue
				}
			case sh.unsigned || sh.neg || sh.scale != 1:
				if d < 0 || d > int64(mask(sh.width)) {
					continue
				}
			default:
				if d < -(1<<(sh.width-1)) || d > int64(mask(sh.width)) {
					continue
				}
			}
			fields[sh.field] = uint64(d) & mask(sh.width)
		}
		n := 0
		for _, it := range alt.pattern {
			n += it.Width
		}
		if n < bestLen {
			best, bestFields, bestLen = alt, fields, n
		}
	}
	if best == nil {
		return nil, nil, fmt.Errorf("no addressing mode for %q", op.text)
	}
	return best, bestFields, nil
}

// writer writes the bits of an encoding (see reader).
type writer struct {
	a    *Arch
	bits []byte // one bit per element, in order
}

func (w *writer) put(v uint64, width int) {
	unit := w.a.Unit
	if width%unit == 0 && len(w.bits)%unit == 0 && width > unit {
		units := width / unit
		for i := 0; i < units; i++ {
			var u uint64
			if w.a.Endian == 0 {
				u = v >> uint(i*unit)
			} else {
				u = v >> uint((units-1-i)*unit)
			}
			w.putBits(u&mask(unit), unit)
		}
		return
	}
	w.putBits(v, width)
}

func (w *writer) putBits(v uint64, width int) {
	for i := width - 1; i >= 0; i-- {
		w.bits = append(w.bits, byte(v>>uint(i)&1))
	}
}

func (w *writer) bytes() []byte {
	out := make([]byte, (len(w.bits)+7)/8)
	for i, b := range w.bits {
		byteIdx, bit := bitPos(w.a, i)
		out[byteIdx] |= b << uint(bit)
	}
	return out
}

func (a *Arch) encode(items []Item, vals map[string]uint64, alts map[string]*Alternative, altFields map[string]map[string]uint64) ([]byte, error) {
	w := &writer{a: a}
	if err := a.encodeItems(w, items, vals, alts, altFields, nil); err != nil {
		return nil, err
	}
	if len(w.bits)%8 != 0 {
		return nil, fmt.Errorf("the encoding is %d bits", len(w.bits))
	}
	return w.bytes(), nil
}

func (a *Arch) encodeItems(w *writer, items []Item, vals map[string]uint64, alts map[string]*Alternative, altFields map[string]map[string]uint64, local map[string]uint64) error {
	for _, it := range items {
		switch it.Kind {
		case Fixed:
			w.put(it.Bits, it.Width)
		case Field:
			v, ok := local[it.Name]
			if local == nil {
				v, ok = vals[it.Name]
			}
			if !ok {
				return fmt.Errorf("no value for field %s", it.Name)
			}
			if it.Slice {
				v >>= uint(it.Lo)
			}
			w.put(v&mask(it.Width), it.Width)
		case Ref:
			alt := alts[it.Operand]
			if alt == nil {
				return fmt.Errorf("operand %s needs fragment %s", it.Operand, it.Name)
			}
			fields := map[string]uint64{}
			for k, v := range altFields[it.Operand] {
				fields[k] = v
			}
			for k, b := range it.Binds {
				switch {
				case b.Const:
					fields[k] = b.Value
				case b.Any:
					fields[k] = 0
				default:
					fields[k] = vals[b.Operand]
				}
			}
			if err := a.encodeItems(w, alt.pattern, vals, alts, altFields, fields); err != nil {
				return err
			}
		}
	}
	return nil
}

// AsmEffects returns the effects of the instructions of assembler text,
// one instruction or label per line (stmt.asm.effects): the effects of
// every instruction form of each mnemonic used.
func (a *Arch) AsmEffects(text string) []string {
	seen := map[string]bool{}
	var effs []string
	for _, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, ";"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		for len(fields) > 0 && a.prefix(fields[0]) != nil && len(fields) > 1 {
			fields = fields[1:]
		}
		if len(fields) == 0 || strings.HasSuffix(fields[0], ":") {
			continue
		}
		m := strings.ToUpper(fields[0])
		for i := range a.Instructions {
			if a.Instructions[i].Mnemonic == m {
				for _, e := range a.Instructions[i].Effects {
					if !seen[e] {
						seen[e] = true
						effs = append(effs, e)
					}
				}
			}
		}
	}
	return effs
}
