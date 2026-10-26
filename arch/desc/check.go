package desc

import (
	"fmt"
	"math/bits"
	"strings"
)

// Check checks the description: its registers, operand kinds,
// fragments, prefixes and instructions, the encodings, and the widths in
// the semantics (target.arch.value). It prepares the description for
// decoding and interpretation.
func (a *Arch) Check() []error {
	c := &checker{a: a}
	c.registers()
	c.operands()
	c.funcs()
	c.fragments()
	if e, err := parseExpr(a.Address); err != nil {
		c.errorf("Address: %v", err)
	} else {
		a.address = e
		env := c.env("Address")
		segWidth := 0
		for _, f := range a.Fragments {
			for _, alt := range f.Alternatives {
				if alt.segment != nil {
					if v, ok := c.exprIn(c.env("segment"), alt.segment); ok {
						segWidth = v.w
					}
				}
			}
		}
		env.vars["seg"] = &wvar{w: segWidth}
		env.vars["off"] = &wvar{w: a.Word}
		if v, ok := c.exprIn(env, e); ok && v.w != a.AddressBits {
			c.errorf("Address: the width is %d, want AddressBits %d", v.w, a.AddressBits)
		}
	}
	if a.pc = a.regs[a.PC]; a.pc == nil {
		c.errorf("PC: unknown register %q", a.PC)
	}
	if e, err := parseExpr(a.Fetch); err != nil {
		c.errorf("Fetch: %v", err)
	} else if v, ok := c.exprIn(c.env("Fetch"), e); ok {
		a.fetch = e
		if v.w != a.AddressBits {
			c.errorf("Fetch: the width is %d, want AddressBits %d", v.w, a.AddressBits)
		}
	}
	c.prefixes()
	c.instructions()
	a.index()
	return c.errs
}

type checker struct {
	a    *Arch
	errs []error
}

func (c *checker) errorf(format string, args ...any) {
	c.errs = append(c.errs, fmt.Errorf(c.a.Name+": "+format, args...))
}

func (c *checker) registers() {
	a := c.a
	a.regs = map[string]*Register{}
	a.classes = map[string][]*Register{}
	if a.Unit <= 0 || a.Unit%8 != 0 {
		c.errorf("Unit %d is not a multiple of 8", a.Unit)
		a.Unit = 8
	}
	for i := range a.Registers {
		r := &a.Registers[i]
		if a.regs[r.Name] != nil {
			c.errorf("register %s declared twice", r.Name)
		}
		if r.Width <= 0 || r.Width > 64 {
			c.errorf("register %s: invalid width %d", r.Name, r.Width)
		}
		a.regs[r.Name] = r
		if r.Class != "" {
			r.Number = len(a.classes[r.Class])
			a.classes[r.Class] = append(a.classes[r.Class], r)
		}
	}
	for i := range a.Registers {
		r := &a.Registers[i]
		if r.Parent == "" {
			continue
		}
		p := a.regs[r.Parent]
		switch {
		case p == nil:
			c.errorf("register %s: unknown parent %s", r.Name, r.Parent)
		case p.Parent != "":
			c.errorf("register %s: the parent %s is itself a part", r.Name, r.Parent)
		case r.Offset < 0 || r.Offset+r.Width > p.Width:
			c.errorf("register %s: bits %d to %d are outside %s", r.Name, r.Offset, r.Offset+r.Width-1, p.Name)
		}
	}
	for name, regs := range a.classes {
		if name == "prefix" {
			continue // the prefix state is not named by number
		}
		for _, r := range regs {
			if r.Width != regs[0].Width {
				c.errorf("register class %s mixes widths", name)
				break
			}
		}
	}
}

// regBits returns the number of bits that encode a register operand.
func (o *Operand) regBits(a *Arch) int {
	if o.Registers == 0 {
		return a.classBits(o.Class)
	}
	n := 0
	for 1<<uint(n) < o.Registers {
		n++
	}
	return n
}

// classBits returns the number of bits that encode a register of class
// name.
func (a *Arch) classBits(name string) int {
	return bits.Len(uint(len(a.classes[name]) - 1))
}

func (c *checker) operands() {
	a := c.a
	a.operands = map[string]*Operand{}
	for i := range a.Operands {
		o := &a.Operands[i]
		if a.operands[o.Name] != nil {
			c.errorf("operand kind %s declared twice", o.Name)
		}
		a.operands[o.Name] = o
		kinds := 0
		if o.Register != "" {
			kinds++
			if a.regs[o.Register] == nil {
				c.errorf("operand kind %s: unknown register %s", o.Name, o.Register)
			}
		}
		if o.Class != "" || o.Memory != 0 {
			kinds++
			if o.Class != "" && a.classes[o.Class] == nil {
				c.errorf("operand kind %s: unknown register class %s", o.Name, o.Class)
			}
		}
		if o.Memory != 0 && o.Fragment == "" {
			c.errorf("operand kind %s: a memory operand needs a fragment", o.Name)
		}
		if o.Imm != 0 {
			kinds++
		}
		if o.Constant {
			kinds++
		}
		if kinds != 1 {
			c.errorf("operand kind %s: want exactly one of Register, Class or Memory, Imm and Constant", o.Name)
		}
		if o.Transform != "" {
			e, err := parseExpr(o.Transform)
			if err != nil {
				c.errorf("operand kind %s: Transform: %v", o.Name, err)
				continue
			}
			o.tparsed = e
			o.transform, _ = a.compileExpr(e, "v")
			env := c.env("operand kind " + o.Name)
			env.vars["v"] = &wvar{w: o.Imm}
			if v, ok := c.exprIn(env, e); ok && v.w != 0 && v.w != o.Imm {
				c.errorf("operand kind %s: Transform has width %d, want %d", o.Name, v.w, o.Imm)
			}
		}
	}
}

func (c *checker) funcs() {
	a := c.a
	a.funcs = map[string]*FuncDecl{}
	fns, err := parseFuncs(a.Semantics)
	if err != nil {
		c.errorf("Semantics: %v", err)
		return
	}
	for _, f := range fns {
		if a.funcs[f.Name] != nil || builtins[f.Name] != nil {
			c.errorf("Semantics: function %s declared twice or named as a built-in function", f.Name)
		}
		a.funcs[f.Name] = f
	}
}

func (c *checker) fragments() {
	a := c.a
	a.fragments = map[string]*Fragment{}
	for i := range a.Fragments {
		f := &a.Fragments[i]
		a.fragments[f.Name] = f
	}
	for i := range a.Fragments {
		f := &a.Fragments[i]
		for j := range f.Alternatives {
			alt := &f.Alternatives[j]
			ctx := fmt.Sprintf("fragment %s, alternative %q", f.Name, alt.Encoding)
			p, err := parseEncoding(alt.Encoding)
			if err != nil {
				c.errorf("%s: %v", ctx, err)
				continue
			}
			alt.pattern = p
			fields := map[string]int{}
			total := 0
			for _, it := range p {
				switch it.Kind {
				case Ref:
					c.errorf("%s: a fragment cannot refer to a fragment", ctx)
				case Field:
					fields[it.Name] = it.Width
				}
				total += it.Width
			}
			if total%a.Unit != 0 && a.Unit == 8 {
				// With units wider than a byte, a fragment may be part of a
				// unit; the instruction forms that use it must be whole.
				c.errorf("%s: %d bits is not a whole number of units", ctx, total)
			}
			if alt.Register != "" {
				if _, ok := fields[alt.Register]; !ok {
					c.errorf("%s: no field %s for the register", ctx, alt.Register)
				}
				continue
			}
			env := c.env(ctx)
			for name, w := range fields {
				env.vars[name] = &wvar{w: w}
			}
			if e, err := parseExpr(alt.Address); err != nil {
				c.errorf("%s: Address: %v", ctx, err)
			} else {
				alt.address = e
				c.exprIn(env, e)
			}
			if alt.Segment != "" {
				if e, err := parseExpr(alt.Segment); err != nil {
					c.errorf("%s: Segment: %v", ctx, err)
				} else {
					alt.segment = e
					c.exprIn(env, e)
				}
			}
		}
	}
}

func (c *checker) prefixes() {
	a := c.a
	for i := range a.Prefixes {
		p := &a.Prefixes[i]
		ctx := "prefix " + p.Name
		pat, err := parseEncoding(p.Encoding)
		if err != nil {
			c.errorf("%s: %v", ctx, err)
			continue
		}
		p.pattern = pat
		body, serr := parseStmts(p.Semantics)
		if serr != nil {
			c.errorf("%s: semantics %v", ctx, serr)
			continue
		}
		p.body = body
		c.stmts(c.env(ctx), body)
	}
}

func (c *checker) instructions() {
	a := c.a
	for i := range a.Instructions {
		in := &a.Instructions[i]
		ctx := "instruction " + in.String()
		pat, err := parseEncoding(in.Encoding)
		if err != nil {
			c.errorf("%s: %v", ctx, err)
			continue
		}
		in.pattern = pat
		in.ops = nil
		for _, name := range in.Operands {
			o := a.operands[name]
			if o == nil {
				c.errorf("%s: unknown operand kind %s", ctx, name)
				o = &Operand{Name: name}
			}
			in.ops = append(in.ops, o)
		}
		c.encoding(ctx, in)
		body, serr := parseStmts(in.Semantics)
		if serr != nil {
			c.errorf("%s: semantics %v", ctx, serr)
			continue
		}
		in.body = body
		env := c.env(ctx)
		env.inst = in
		c.stmts(env, body)
	}
}

// opName returns the semantics name of operand i: a, b or c.
func opName(i int) string { return string(rune('a' + i)) }

// encoding checks that the encoding of an instruction encodes each
// operand that needs it, with fields of the right widths.
func (c *checker) encoding(ctx string, in *Instruction) {
	a := c.a
	encoded := map[string]bool{}
	covered := map[string]uint64{} // the bits of operands encoded by slices
	sliceWant := map[string]int{}
	total := 0
	for _, it := range in.pattern {
		switch it.Kind {
		case Fixed:
			total += it.Width
		case Field:
			total += it.Width
			i := strings.IndexByte("abc", it.Name[0])
			if len(it.Name) != 1 || i < 0 || i >= len(in.ops) {
				c.errorf("%s: field %s does not name an operand", ctx, it.Name)
				continue
			}
			o := in.ops[i]
			want := 0
			switch {
			case o.Class != "" && o.Memory == 0:
				want = o.regBits(a)
			case o.Imm != 0:
				want = o.Imm
			default:
				c.errorf("%s: operand %s of kind %s is not encoded by a field", ctx, it.Name, o.Name)
			}
			switch {
			case it.Slice:
				covered[it.Name] |= mask(it.Width) << uint(it.Lo)
				sliceWant[it.Name] = want
			case want != 0 && it.Width != want:
				c.errorf("%s: field %s has %d bits, want %d", ctx, it.Name, it.Width, want)
			}
			encoded[it.Name] = true
		case Ref:
			f := a.fragments[it.Name]
			if f == nil {
				c.errorf("%s: unknown fragment %s", ctx, it.Name)
				continue
			}
			i := strings.IndexByte("abc", it.Operand[0])
			if len(it.Operand) != 1 || i < 0 || i >= len(in.ops) {
				c.errorf("%s: <%s %s> does not name an operand", ctx, it.Name, it.Operand)
				continue
			}
			if o := in.ops[i]; o.Fragment != it.Name {
				c.errorf("%s: operand %s of kind %s is not encoded by fragment %s", ctx, it.Operand, o.Name, it.Name)
			}
			encoded[it.Operand] = true
			if len(f.Alternatives) > 0 {
				for _, item := range f.Alternatives[0].pattern {
					total += item.Width
				}
			}
			for k, b := range it.Binds {
				w := 0
				for _, alt := range f.Alternatives {
					for _, item := range alt.pattern {
						if item.Kind == Field && item.Name == k {
							w = item.Width
						}
					}
				}
				if w == 0 {
					c.errorf("%s: fragment %s has no field %s", ctx, it.Name, k)
					continue
				}
				if b.Any {
					continue
				}
				if b.Const {
					if b.Value >= 1<<uint(w) {
						c.errorf("%s: %d does not fit in the field %s of %d bits", ctx, b.Value, k, w)
					}
					continue
				}
				j := strings.IndexByte("abc", b.Operand[0])
				if len(b.Operand) != 1 || j < 0 || j >= len(in.ops) {
					c.errorf("%s: %s=%s does not name an operand", ctx, k, b.Operand)
					continue
				}
				if o := in.ops[j]; o.Class == "" || o.regBits(a) > w {
					c.errorf("%s: operand %s of kind %s does not fit the field %s of %d bits", ctx, b.Operand, o.Name, k, w)
				}
				encoded[b.Operand] = true
			}
		}
	}
	for name, bits := range covered {
		if w := sliceWant[name]; w != 0 && bits != mask(w) {
			c.errorf("%s: the bit ranges of %s cover %#x, want the %d bits of the operand", ctx, name, bits, w)
		}
	}
	if total%a.Unit != 0 {
		c.errorf("%s: the fixed part has %d bits, not a whole number of units", ctx, total)
	}
	for i, o := range in.ops {
		needs := o.Register == "" && !o.Constant
		if needs && !encoded[opName(i)] {
			c.errorf("%s: operand %s of kind %s is not encoded", ctx, opName(i), o.Name)
		}
	}
}
