// Package codegen is the code generator of every architecture
// (spec/22-compiler.md §22.11). It translates the IR of a program and the
// runtime package into machine operations, replaces each operation by the
// instruction template of a rule of the architecture or the platform
// (spec/13-targets.md §13.5.7), and links the result into one image.
// Nothing in it is specific to an architecture
// (target.arch.no-compiler-change).
package codegen

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"an/arch/desc"
	"an/build"
	"an/ir"
	"an/types"
)

func init() {
	build.Backend = func(prog *ir.Program, d *build.TargetDesc) ([]byte, []error) {
		a, errs := desc.ForName(d.Arch.Name)
		if len(errs) > 0 {
			return nil, errs
		}
		return Generate(prog, a, d)
	}
}

// gen generates the image of one program.
type gen struct {
	prog    *ir.Program
	arch    *desc.Arch
	desc    *build.TargetDesc
	rules   map[string][]desc.Rule // by operation, the architecture's first
	word    int                    // bytes
	ptr     int                    // bytes of the default pointer kind
	align   int
	intBits int
	frame   string // the frame register

	layouts  map[types.Type][2]int
	funcs    map[*ir.Func]string // labels of the functions to generate
	queue    []*ir.Func
	runtime  map[string]*ir.Func // the functions of the runtime package by name
	thunks   []func()
	code     []item
	data     []*dataItem
	strs     map[string]string // labels of string constants
	consts   map[*ir.Const]string
	typeNums []types.Type // the dynamic types of interface values; number i+1
	selected map[string]int
	nlabel   int
	errs     []error

	usesDefer    bool
	funcsByLabel map[string]bool // generated routines
	addrs        map[string]int  // the addresses of the labels, after linking
}

// item is a line of generated code: a label or an instruction whose text
// may refer to labels as <label+offset>.
type item struct {
	label string
	text  string
	fn    string // the function, for diagnostics
}

// dataItem is constant data: bytes with the addresses of labels written
// into them.
type dataItem struct {
	label  string
	bytes  []byte
	align  int
	relocs []reloc
}

type reloc struct {
	off int
	sym string
	add int
}

// Generate generates the image of prog for the target: the entry code,
// the functions and the constant data, linked at the origin of the first
// format of the target.
func Generate(prog *ir.Program, a *desc.Arch, d *build.TargetDesc) ([]byte, []error) {
	if a.Endian != 0 {
		return nil, []error{fmt.Errorf("codegen: big-endian architectures are not supported yet")}
	}
	g := &gen{
		prog: prog, arch: a, desc: d,
		rules:    map[string][]desc.Rule{},
		word:     a.Word / 8,
		ptr:      d.PointerBits() / 8,
		align:    a.ABI.Align,
		intBits:  d.Word,
		frame:    a.ABI.Frame,
		layouts:  map[types.Type][2]int{},
		funcs:    map[*ir.Func]string{},
		runtime:  map[string]*ir.Func{},
		strs:     map[string]string{},
		consts:   map[*ir.Const]string{},
		selected: map[string]int{},

		funcsByLabel: map[string]bool{},
	}
	if g.align == 0 {
		g.align = g.word
	}
	for _, r := range a.Rules {
		g.rules[r.Op] = append(g.rules[r.Op], r)
	}
	for _, r := range d.Platform.Rules {
		g.rules[r.Op] = append(g.rules[r.Op], desc.Rule{Op: r.Op, Width: r.Width, From: r.From, Code: r.Code})
	}
	for _, f := range prog.Funcs {
		if name, ok := strings.CutPrefix(f.Name, build.RuntimePath+"."); ok {
			g.runtime[name] = f
		}
	}
	g.scanTypes()

	g.entry()
	for len(g.queue) > 0 || len(g.thunks) > 0 {
		if len(g.queue) > 0 {
			f := g.queue[0]
			g.queue = g.queue[1:]
			g.function(f)
			continue
		}
		t := g.thunks[0]
		g.thunks = g.thunks[1:]
		t()
	}
	if g.usesDefer {
		g.data = append(g.data, &dataItem{label: "Ldefers", bytes: make([]byte, g.ptr), align: g.scalarAlign(g.ptr)})
	}
	if len(g.errs) > 0 {
		return nil, g.errs
	}
	origin, limit := 0, 0
	if len(d.Formats) > 0 {
		origin, limit = d.Formats[0].Origin, d.Formats[0].MaxSize
	}
	image, err := g.link(origin)
	if err != nil {
		return nil, []error{err}
	}
	if limit > 0 && len(image) > limit {
		return nil, []error{fmt.Errorf("the image is %d bytes, more than the %d bytes of format %s (target.format.size)", len(image), limit, d.Formats[0].Type)}
	}
	return image, nil
}

// label returns a new label.
func (g *gen) label() string {
	g.nlabel++
	return "L" + strconv.Itoa(g.nlabel)
}

// funcLabel returns the label of function f and queues it for
// generation.
func (g *gen) funcLabel(f *ir.Func) string {
	if l, ok := g.funcs[f]; ok {
		return l
	}
	l := "F" + strconv.Itoa(len(g.funcs))
	g.funcs[f] = l
	g.queue = append(g.queue, f)
	return l
}

// rt returns the label of function name of the runtime package.
func (g *gen) rt(name string) (*ir.Func, string) {
	f := g.runtime[name]
	if f == nil {
		g.fail("codegen: the runtime package has no function %s", name)
		return nil, "F0"
	}
	return f, g.funcLabel(f)
}

// ---------------------------------------------------------------------
// Operands and templates.

type operandKind int

const (
	opSlot  operandKind = iota // a frame slot: off relative to the frame base
	opArg                      // a slot of the argument area: off from the bottom of the frame
	opImm                      // a constant
	opSym                      // the address of a label plus add
	opFrame                    // the size of the frame of the function
	opReg                      // a register named sym, for regin and regout
)

// operand is an operand of a machine operation.
type operand struct {
	kind  operandKind
	off   int
	width int // bits: the size of a memory operand; 0 for an address only
	imm   uint64
	sym   string
	add   int
}

func imm(v uint64, width int) operand { return operand{kind: opImm, imm: v, width: width} }

func (o operand) isMem() bool { return o.kind == opSlot || o.kind == opArg }

// text returns the assembler text of part of an operand: the whole
// operand if part < 0, otherwise the part of the word width.
func (g *gen) text(o operand, part int) string {
	width, off := o.width, o.off
	if part >= 0 {
		width = g.word * 8
		off += part * g.word
	}
	size := sizeKeyword(width)
	if size != "" {
		size += " "
	}
	switch o.kind {
	case opSlot:
		return fmt.Sprintf("%s[%s%+d]", size, g.frame, off)
	case opArg:
		return fmt.Sprintf("%s[%s\x01A%d\x01]", size, g.frame, off)
	case opImm:
		v := o.imm
		if part >= 0 {
			v >>= uint(part * g.word * 8)
			width = g.word * 8
		} else if o.width == 0 {
			width = 64
		}
		if width < 64 {
			v &= 1<<uint(width) - 1
		}
		return strconv.FormatUint(v, 10)
	case opSym:
		if part > 0 {
			return "0"
		}
		return fmt.Sprintf("<%s%+d>", o.sym, o.add)
	case opFrame:
		return "\x01F\x01"
	case opReg:
		return o.sym
	}
	panic("codegen: bad operand")
}

func sizeKeyword(width int) string {
	switch width {
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

var placeholder = regexp.MustCompile(`\{([A-Za-z]+)(?:\.(\d+))?\}|@[A-Za-z0-9_]+`)

// expand substitutes the operands and labels into template code and
// returns its lines.
func (g *gen) expand(code string, ops map[string]operand, labels map[string]string) ([]string, error) {
	local := map[string]string{}
	var err error
	s := placeholder.ReplaceAllStringFunc(code, func(m string) string {
		if m[0] == '@' {
			if local[m] == "" {
				local[m] = g.label()
			}
			return "\x02" + local[m] + "\x02"
		}
		sm := placeholder.FindStringSubmatch(m)
		name := sm[1]
		if l, ok := labels[name]; ok {
			return "<" + l + "+0>"
		}
		o, ok := ops[name]
		if !ok {
			err = fmt.Errorf("the template uses {%s}, which operation has no", name)
			return m
		}
		part := -1
		if sm[2] != "" {
			part, _ = strconv.Atoi(sm[2])
		}
		return g.text(o, part)
	})
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if i := strings.Index(l, ";"); i >= 0 {
			l = l[:i]
		}
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "\x02") && strings.HasSuffix(l, "\x02:") {
			l = strings.Trim(l[:len(l)-1], "\x02") + ":" // a local label
		} else {
			l = localRef.ReplaceAllString(l, "<$1+0>")
		}
		lines = append(lines, l)
	}
	return lines, nil
}

var (
	symRef   = regexp.MustCompile(`<([A-Za-z0-9_.]+)([+-]\d+)>`)
	argRef   = regexp.MustCompile("\x01A(-?\\d+)\x01")
	localRef = regexp.MustCompile("\x02(L\\d+)\x02")
)

// assembles reports whether the instruction lines of an expanded
// template assemble, with labels at an arbitrary address.
func (g *gen) assembles(lines []string) bool {
	for _, l := range lines {
		if strings.HasSuffix(l, ":") {
			continue
		}
		l = symRef.ReplaceAllString(l, "4096")
		l = argRef.ReplaceAllString(l, "-100")
		l = strings.ReplaceAll(l, "\x01F\x01", "100")
		if _, err := g.arch.AsmAt(l, 0x1000); err != nil {
			return false
		}
	}
	return true
}

// selectRule returns the lines of the first rule for the operation whose
// template assembles with the operands (target.arch.select).
func (g *gen) selectRule(op string, width, from int, ops map[string]operand, labels map[string]string) ([]string, bool) {
	var key strings.Builder
	fmt.Fprintf(&key, "%s/%d/%d", op, width, from)
	names := make([]string, 0, len(ops))
	for n := range ops {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&key, " %s%d", n, ops[n].kind)
		if ops[n].kind == opReg {
			key.WriteString(ops[n].sym) // templates differ in what registers they take
		}
	}
	rules := g.rules[op]
	if i, ok := g.selected[key.String()]; ok {
		if i < 0 {
			return nil, false
		}
		lines, _ := g.expand(rules[i].Code, ops, labels)
		return lines, true
	}
	for i, r := range rules {
		if r.Width != width || r.From != from {
			continue
		}
		lines, err := g.expand(r.Code, ops, labels)
		if err != nil {
			g.fail("rule %s of width %d of %s: %v", op, width, g.arch.Name, err)
			continue
		}
		if g.assembles(lines) {
			g.selected[key.String()] = i
			return lines, true
		}
	}
	g.selected[key.String()] = -1
	return nil, false
}

// hasRule reports whether there is a rule for the operation.
func (g *gen) hasRule(op string, width, from int) bool {
	for _, r := range g.rules[op] {
		if r.Width == width && r.From == from {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// Linking (comp.gen.link).

// link places the code and the data at origin and assembles the code
// until the lengths of the instructions do not change.
func (g *gen) link(origin int) ([]byte, error) {
	sizes := make([]int, len(g.code))
	addrs := map[string]int{}
	resolve := func(text string) (string, error) {
		var err error
		s := symRef.ReplaceAllStringFunc(text, func(m string) string {
			sm := symRef.FindStringSubmatch(m)
			a, ok := addrs[sm[1]]
			if !ok {
				err = fmt.Errorf("codegen: undefined label %s", sm[1])
			}
			add, _ := strconv.Atoi(sm[2])
			return strconv.Itoa(a + add)
		})
		return s, err
	}
	var codes [][]byte
	for pass := 0; ; pass++ {
		if pass > 30 {
			return nil, fmt.Errorf("codegen: the lengths of the instructions do not settle")
		}
		// Place.
		pc := origin
		for i, it := range g.code {
			if it.label != "" {
				addrs[it.label] = pc
			}
			pc += sizes[i]
		}
		for _, d := range g.data {
			pc = roundUp(pc, d.align)
			addrs[d.label] = pc
			pc += len(d.bytes)
		}
		if pass == 0 {
			// Lengths with the label addresses of a first placement.
			for i, it := range g.code {
				if it.text == "" {
					continue
				}
				text, _ := resolve(it.text)
				code, err := g.arch.AsmAt(text, uint64(origin))
				if err != nil {
					code, err = g.arch.AsmAt(symRef.ReplaceAllString(it.text, "4660"), uint64(origin))
				}
				if err != nil {
					return nil, fmt.Errorf("%s: %q: %v", it.fn, it.text, err)
				}
				sizes[i] = len(code)
			}
			continue
		}
		// Assemble at the addresses of this placement. An error may be
		// due to lengths that are still changing; it counts only when no
		// length changes.
		codes = codes[:0]
		changed := false
		var firstErr error
		pc = origin
		for i, it := range g.code {
			if it.text == "" {
				codes = append(codes, nil)
				continue
			}
			text, err := resolve(it.text)
			if err != nil {
				return nil, err
			}
			code, err := g.arch.AsmAt(text, uint64(pc))
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %q: %v", it.fn, text, err)
				}
				code = make([]byte, sizes[i])
			}
			if len(code) != sizes[i] {
				sizes[i] = len(code)
				changed = true
			}
			codes = append(codes, code)
			pc += sizes[i]
		}
		if !changed {
			if firstErr != nil {
				return nil, firstErr
			}
			break
		}
	}
	g.addrs = addrs
	var image []byte
	for _, c := range codes {
		image = append(image, c...)
	}
	for _, d := range g.data {
		for len(image)+origin < addrs[d.label] {
			image = append(image, 0)
		}
		b := append([]byte(nil), d.bytes...)
		for _, r := range d.relocs {
			v := addrs[r.sym] + r.add
			for k := 0; k < g.ptr; k++ {
				b[r.off+k] = byte(v >> uint(8*k))
			}
		}
		image = append(image, b...)
	}
	return image, nil
}
