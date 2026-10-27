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
	"an/syntax"
	"an/types"
)

func init() {
	build.Backend = func(prog *ir.Program, d *build.TargetDesc) (*build.Image, []error) {
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
	bottom  bool   // the frame register holds the bottom of the frame
	// structured is set for an architecture with structured control flow
	// (target.arch.structured).
	structured bool

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
	handlers     map[*ir.Func]bool // the interrupt handlers
	stackInfos   map[string]*stackInfo
	addrTaken    map[string]string // the labels of routines whose address is a value, with their signatures
	stackNeed    int
	funcsByLabel map[string]bool // generated routines
	addrs        map[string]int  // the addresses of the labels, after linking
	sizes        map[string]int  // the bytes of code of each function, after linking
}

// item is a line of generated code: a label or an instruction whose text
// may refer to labels as <label+offset>.
type item struct {
	label string
	text  string
	fn    string // the function, for diagnostics
	term  bool   // control does not continue after the instruction
	unit  bool   // the label of a routine of a structured image
}

// dataItem is constant data: bytes with the addresses of labels written
// into them; or writable data, which starts as zero bytes.
type dataItem struct {
	label    string
	bytes    []byte
	align    int
	relocs   []reloc
	writable bool
}

type reloc struct {
	off int
	sym string
	add int
}

// Generate generates the image of prog for the target: the entry code,
// the functions and the constant data, linked at the origin of the first
// format of the target.
func Generate(prog *ir.Program, a *desc.Arch, d *build.TargetDesc) (*build.Image, []error) {
	image, _, errs := GenerateMap(prog, a, d)
	return image, errs
}

// GenerateMap is Generate that also returns the memory map: the address
// of the code of each function, by its name.
func GenerateMap(prog *ir.Program, a *desc.Arch, d *build.TargetDesc) (*build.Image, map[string]int, []error) {
	g, image, errs := generate(prog, a, d)
	if len(errs) > 0 {
		return nil, nil, errs
	}
	funcs := map[string]int{}
	for _, it := range g.code {
		if it.label != "" {
			if _, ok := funcs[it.fn]; !ok {
				funcs[it.fn] = g.addrs[it.label]
			}
		}
	}
	return image, funcs, nil
}

func generate(prog *ir.Program, a *desc.Arch, d *build.TargetDesc) (*gen, *build.Image, []error) {
	if a.Endian != 0 {
		return nil, nil, []error{fmt.Errorf("codegen: big-endian architectures are not supported yet")}
	}
	g := &gen{
		prog: prog, arch: a, desc: d,
		rules:   map[string][]desc.Rule{},
		word:    a.Word / 8,
		ptr:     d.PointerBits() / 8,
		align:   a.ABI.Align,
		intBits: d.Word,
		frame:   a.ABI.Frame,
		bottom:  a.ABI.FrameBottom,

		structured: a.ABI.Structured.End != "",
		layouts:    map[types.Type][2]int{},
		funcs:      map[*ir.Func]string{},
		runtime:    map[string]*ir.Func{},
		strs:       map[string]string{},
		consts:     map[*ir.Const]string{},
		selected:   map[string]int{},

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

	g.funcLabel(g.prog.Main)
	g.handlers = map[*ir.Func]bool{}
	for _, h := range g.prog.Handlers {
		g.handlers[h] = true
		g.funcLabel(h)
	}
	g.drain()
	// The entry code, which clears the writable data, comes first; the
	// default interrupt handler it generates may use the defer chain.
	body := g.code
	g.code = nil
	if g.hasRule("vectors", 0, 0) {
		g.defaultHandler()
		g.drain()
	}
	if g.usesDefer {
		g.data = append(g.data, &dataItem{label: "Ldefers", bytes: make([]byte, g.ptr), align: g.scalarAlign(g.ptr), writable: true})
	}
	if _, _, ok := d.Regions(); !ok && d.Stack > 0 {
		// The stack of main follows the writable data.
		g.data = append(g.data, &dataItem{label: "Lstack", bytes: make([]byte, d.Stack), align: 16, writable: true})
	}
	body = append(body, g.code...)
	g.code = nil
	g.entry()
	g.drain()
	g.code = append(g.code, body...)
	g.checkStack()
	if len(g.errs) > 0 {
		return nil, nil, g.errs
	}
	origin, limit := 0, 0
	if len(d.Formats) > 0 {
		origin, limit = d.Formats[0].Origin, d.Formats[0].MaxSize
	}
	if g.structured {
		im, err := g.linkStructured(origin)
		if err != nil {
			return nil, nil, []error{err}
		}
		return g, im, nil
	}
	ram := -1
	if code, data, ok := d.Regions(); ok {
		origin, limit, ram = code.At, code.Size, data.At
	}
	image, err := g.link(origin, ram)
	if limit > 0 && g.imageSize() > limit {
		// An image too large may fail to link first, with addresses that
		// do not fit their instructions.
		what := "format " + d.Formats[0].Type
		if ram >= 0 {
			what = "the Exec region"
		}
		return nil, nil, []error{fmt.Errorf("the image is %d bytes, more than the %d bytes of %s (target.format.size, target.memory.overflow); %s", g.imageSize(), limit, what, g.breakdown())}
	}
	if err != nil {
		return nil, nil, []error{err}
	}
	if d.Stack == 0 && g.prog.Core && limit > 0 && ram < 0 {
		// Without a Stack, the stack of a flat image grows down from the
		// end of its format into the memory after the image
		// (emb.stack.compute).
		if free := limit - len(image) - g.ptr; g.stackNeed > free {
			return nil, nil, []error{&syntax.Error{Pos: g.prog.Main.Pos, Rule: "emb.stack.limit", Msg: fmt.Sprintf("the stack needs %d bytes, more than the %d bytes between the image and the end of format %s", g.stackNeed, free, d.Formats[0].Type)}}
		}
	}
	return g, &build.Image{Bytes: image}, nil
}

// drain generates the queued functions and thunks.
func (g *gen) drain() {
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
}

// imageSize returns the size of the code and constant data after the
// last pass of linking.
func (g *gen) imageSize() int {
	n := 0
	for _, v := range g.sizes {
		n += v
	}
	for _, d := range g.data {
		if !d.writable {
			n += len(d.bytes)
		}
	}
	return n
}

// breakdown describes the contents of the image: the code by package,
// the constant data and the writable data.
func (g *gen) breakdown() string {
	code := map[string]int{}
	for fn, n := range g.sizes {
		pkg := fn
		if i := strings.LastIndexByte(pkg, '/'); i >= 0 {
			if j := strings.IndexByte(pkg[i:], '.'); j >= 0 {
				pkg = pkg[:i+j]
			}
		} else if j := strings.IndexByte(pkg, '.'); j >= 0 {
			pkg = pkg[:j]
		}
		code[pkg] += n
	}
	pkgs := make([]string, 0, len(code))
	for p := range code {
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool { return code[pkgs[i]] > code[pkgs[j]] })
	var parts []string
	for _, p := range pkgs {
		parts = append(parts, fmt.Sprintf("%s %d", p, code[p]))
	}
	consts, static := 0, 0
	for _, d := range g.data {
		if d.writable {
			static += len(d.bytes)
		} else {
			consts += len(d.bytes)
		}
	}
	return fmt.Sprintf("code by package: %s; constants %d; static data %d", strings.Join(parts, ", "), consts, static)
}

// writableSize returns the size of the writable data, which the entry
// code clears.
func (g *gen) writableSize() int {
	n := 0
	for _, d := range g.data {
		if d.writable {
			n = roundUp(n, d.align) + len(d.bytes)
		}
	}
	return n
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
	case opSlot, opArg:
		return fmt.Sprintf("%s[%s%s]", size, g.frame, g.offset(o.kind, off))
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

// offset returns the text of the offset of a slot from the frame
// register, with its sign: a slot of the frame is at off from the frame
// base, a slot of the argument area at off from the bottom of the frame.
// Offsets that depend on the frame size are tokens that finish replaces.
func (g *gen) offset(kind operandKind, off int) string {
	switch {
	case kind == opSlot && g.bottom:
		return fmt.Sprintf("+\x01S%d\x01", off)
	case kind == opSlot:
		return fmt.Sprintf("%+d", off)
	case g.bottom:
		return fmt.Sprintf("%+d", off)
	}
	return fmt.Sprintf("\x01A%d\x01", off)
}

// half returns the text of 16-bit half h of an operand: of a constant,
// the bits; of a label, a reference to the half of its address. Halves of
// slots do not assemble in the templates that use them.
func (g *gen) half(o operand, h int) string {
	switch o.kind {
	case opImm:
		return strconv.FormatUint(o.imm>>uint(16*h)&0xFFFF, 10)
	case opSym:
		return fmt.Sprintf("<%s%+d:h%d>", o.sym, o.add, h)
	}
	return g.text(o, -1)
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

var placeholder = regexp.MustCompile(`\{([A-Za-z]+)(?:\.(\d+|h\d|off))?\}|@[A-Za-z0-9_]+`)

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
		if !ok && sm[2] == "" && g.arch.Register(name) != nil {
			return m // a register list of one register, such as {LR}
		}
		if !ok {
			err = fmt.Errorf("the template uses {%s}, which operation has no", name)
			return m
		}
		switch {
		case strings.HasPrefix(sm[2], "h"):
			return g.half(o, int(sm[2][1]-'0'))
		case sm[2] == "off":
			if !o.isMem() {
				return "?"
			}
			return strings.TrimPrefix(g.offset(o.kind, o.off), "+")
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
	symRef   = regexp.MustCompile(`<([A-Za-z0-9_.]+)([+-]\d+)(?::h(\d))?>`)
	argRef   = regexp.MustCompile("\x01A(-?\\d+)\x01")
	slotTok  = regexp.MustCompile("\x01S(-?\\d+)\x01")
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
		l = slotTok.ReplaceAllString(l, "100")
		l = strings.ReplaceAll(l, "\x01F\x01", "100")
		if _, err := g.assemble(l, 0x1000); err != nil {
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

// assemble assembles one line of code at address pc: an instruction, or
// the directive ".word v", which stores v in a word, where v is a sum of
// numbers.
func (g *gen) assemble(text string, pc uint64) ([]byte, error) {
	arg, ok := strings.CutPrefix(text, ".word ")
	if !ok {
		return g.arch.AsmAt(text, pc)
	}
	var v int64
	for _, t := range strings.Split(strings.ReplaceAll(arg, " ", ""), "+") {
		n, err := strconv.ParseInt(t, 0, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid .word %q", arg)
		}
		v += n
	}
	b := make([]byte, g.word)
	for k := range b {
		b[k] = byte(v >> uint(8*k))
	}
	return b, nil
}

// link places the code and the constant data at origin, and the writable
// data after them or, if ram is not negative, at address ram; it
// assembles the code until the lengths of the instructions do not change.
// The image holds the code and the constant data, and the writable data
// unless it is placed at ram.
func (g *gen) link(origin, ram int) ([]byte, error) {
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
			v := a + add
			if sm[3] != "" {
				v = v >> (16 * int(sm[3][0]-'0')) & 0xFFFF
			}
			return strconv.Itoa(v)
		})
		return s, err
	}
	// The data in the order placed: the constant data, then the writable.
	var data []*dataItem
	for _, w := range []bool{false, true} {
		for _, d := range g.data {
			if d.writable == w {
				data = append(data, d)
			}
		}
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
		for _, d := range data {
			if d.writable && ram >= 0 && pc < ram {
				pc = ram
			}
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
				code, err := g.assemble(text, uint64(origin))
				if err != nil {
					code, err = g.assemble(symRef.ReplaceAllString(it.text, "4660"), uint64(origin))
				}
				// An instruction that assembles at no address of this
				// placement, such as a short branch forward, gets its length
				// in the next passes.
				if err == nil {
					sizes[i] = len(code)
				}
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
			code, err := g.assemble(text, uint64(pc))
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
		g.sizes = map[string]int{}
		for i, it := range g.code {
			g.sizes[it.fn] += sizes[i]
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
	for _, d := range data {
		if d.writable && ram >= 0 {
			break
		}
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
