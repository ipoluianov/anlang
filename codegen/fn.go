package codegen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"an/ir"
	"an/types"
)

// fnGen generates the code of one function.
type fnGen struct {
	g     *gen
	f     *ir.Func
	name  string
	frame int // bytes of slots below the frame base
	args  int // bytes of the argument area
	vals  map[ir.Value]*loc
	free  map[int][]int // free temporary slots by size
	temps []operand     // temporary slots of the current instruction
	code  []item

	blocks   map[*ir.Block]string
	traps    map[string]string // trap labels by runtime function
	results  int               // offset of the results from the frame base
	resTypes []types.Type      // result types
	deferAt  int               // slot of the defer chain at entry, 0 if none
	cur      *ir.Block
	escape   bool // a slice of an array in the frame was made
	// fmtOnly are the conversions to interfaces used only as arguments
	// of fmt.Print and fmt.Println, which print the values directly.
	fmtOnly map[*ir.MakeInterface]bool
	// handler marks an interrupt handler that the operations enter.handler
	// and leave.handler enter and leave, saving the registers that the
	// templates may use.
	handler bool
	label   string // the label of the routine, for the stack analysis
	indKey  string // the signature of the indirect call being made
}

// enterOp and leaveOp return the operations that enter and leave the
// function.
func (fn *fnGen) enterOp() string {
	if fn.handler {
		return "enter.handler"
	}
	return "enter"
}

func (fn *fnGen) leaveOp() string {
	if fn.handler {
		return "leave.handler"
	}
	return "leave"
}

// loc is where the value of an IR value is: a constant, the zero value,
// a value in memory, or the address of memory (comp.gen.values).
type loc struct {
	t     types.Type
	parts []operand // a constant: its word parts, or one byte part
	zero  bool
	at    *mref
	addr  *mref
}

type mrefKind int

const (
	mFrame  mrefKind = iota // off from the frame base
	mArg                    // off from the bottom of the frame: the argument area
	mStatic                 // sym+off
	mPtr                    // the address in ptr, plus off
	mFar                    // the far address in ptr, plus off: memory of a hardware mapping
)

// mref is a place in memory.
type mref struct {
	kind mrefKind
	off  int
	sym  string
	ptr  operand
}

func (m mref) plus(n int) mref { m.off += n; return m }

func at(m mref, t types.Type) *loc     { return &loc{t: t, at: &m} }
func addrOf(m mref, t types.Type) *loc { return &loc{t: t, addr: &m} }

// slotRef returns the frame place of a slot operand.
func slotRef(o operand) mref {
	if o.kind == opArg {
		return mref{kind: mArg, off: o.off}
	}
	return mref{kind: mFrame, off: o.off}
}

func (fn *fnGen) errorf(format string, args ...any) {
	pos := fn.f.Pos.String()
	fn.g.errs = append(fn.g.errs, fmt.Errorf("%s: %s: %s", pos, fn.name, fmt.Sprintf(format, args...)))
}

func (fn *fnGen) unsupported(step int, what string) {
	fn.errorf("%s: not supported yet by the code generator (spec/24-roadmap.md, step %d)", what, step)
}

// ---------------------------------------------------------------------
// Emitting code.

func (fn *fnGen) emitLines(lines []string) {
	for _, l := range lines {
		if strings.HasSuffix(l, ":") {
			fn.code = append(fn.code, item{label: strings.TrimSuffix(l, ":"), fn: fn.name})
			continue
		}
		fn.code = append(fn.code, item{text: l, fn: fn.name})
	}
}

// emitOp emits the lines of operation name; control does not continue
// after a jump, a return or the end of the program.
func (fn *fnGen) emitOp(name string, lines []string) {
	fn.emitLines(lines)
	switch name {
	case "jump", "leave", "leave.handler", "exit":
		if n := len(fn.code); n > 0 && fn.code[n-1].text != "" {
			fn.code[n-1].term = true
		}
	}
}

func (fn *fnGen) defLabel(l string) {
	fn.code = append(fn.code, item{label: l, fn: fn.name})
}

// op emits machine operation name (target.arch.ops). Operands that are
// constants are moved into slots if no template assembles with them.
func (fn *fnGen) op(name string, width, from int, ops map[string]operand, labels map[string]string) {
	g := fn.g
	if s := fn.stackInfo(); s != nil {
		switch name {
		case "call":
			s.calls = append(s.calls, labels["f"])
		case "callind":
			s.ind = append(s.ind, fn.indKey)
		}
	}
	if lines, ok := g.selectRule(name, width, from, ops, labels); ok {
		fn.emitOp(name, lines)
		return
	}
	changed := false
	for _, k := range []string{"x", "y", "p", "q"} {
		o, ok := ops[k]
		if !ok || o.isMem() || o.kind == opFrame || name == "move" {
			continue
		}
		w := width
		switch k {
		case "p", "q":
			w = g.ptr * 8
		case "x":
			if from != 0 {
				w = from
			}
			if name == "branch" {
				w = 8 // the condition is a byte
			}
		case "y":
			if name == "shl" || name == "shr.u" || name == "shr.s" {
				w = g.word * 8 // a count of the word width
			}
		}
		if w == 0 {
			w = g.word * 8
		}
		t := fn.temp(w / 8)
		fn.store(slotRef(t), o, w/8)
		ops[k] = t
		changed = true
	}
	if changed {
		if lines, ok := g.selectRule(name, width, from, ops, labels); ok {
			fn.emitOp(name, lines)
			return
		}
	}
	fn.errorf("architecture %s has no instruction selection rule for operation %s of width %d (target.arch.select)", g.arch.Name, name, width)
}

// ---------------------------------------------------------------------
// Slots.

// slot reserves a slot of size bytes in the frame and returns its offset
// from the frame base.
func (fn *fnGen) slot(size int) int {
	a := 1
	for a < size && a < fn.g.align {
		a *= 2
	}
	fn.frame = roundUp(fn.frame+max(size, 1), a)
	return -fn.frame
}

// temp returns a temporary slot of size bytes for the current
// instruction.
func (fn *fnGen) temp(size int) operand {
	var off int
	if l := fn.free[size]; len(l) > 0 {
		off = l[len(l)-1]
		fn.free[size] = l[:len(l)-1]
	} else {
		off = fn.slot(size)
	}
	o := operand{kind: opSlot, off: off, width: widthOf(size)}
	fn.temps = append(fn.temps, o)
	return o
}

func widthOf(size int) int {
	if size <= 8 {
		return size * 8
	}
	return 0
}

// releaseLoopTemps is a no-op hook for copies word by word, whose
// temporaries live until the end of the instruction.
func (fn *fnGen) releaseLoopTemps() {}

// partOf returns word part i of an operand of size bytes.
func partOf(o operand, i, word int) operand {
	switch o.kind {
	case opImm:
		return imm(o.imm>>uint(i*word*8), word*8)
	case opSlot, opArg:
		o.off += i * word
		o.width = word * 8
		return o
	}
	if i > 0 {
		return imm(0, word*8)
	}
	return o
}

func (fn *fnGen) releaseTemps() {
	for _, t := range fn.temps {
		size := t.width / 8
		fn.free[size] = append(fn.free[size], t.off)
	}
	fn.temps = fn.temps[:0]
}

// result reserves the slot of the value of v.
func (fn *fnGen) result(v ir.Value) *loc {
	t := v.Type()
	l := at(mref{kind: mFrame, off: fn.slot(fn.g.sizeof(t))}, t)
	fn.vals[v] = l
	return l
}

// slotOperand returns the operand of a value of size bytes at frame
// place m.
func slotOperand(m mref, size int) operand {
	k := opSlot
	if m.kind == mArg {
		k = opArg
	}
	return operand{kind: k, off: m.off, width: widthOf(size)}
}

// ---------------------------------------------------------------------
// Values.

// loc returns the location of an IR value.
func (fn *fnGen) loc(v ir.Value) *loc {
	if l, ok := fn.vals[v]; ok {
		return l
	}
	switch v := v.(type) {
	case *ir.Const:
		return fn.g.constLoc(v)
	case *ir.FuncValue:
		return fn.g.funcValue(v.Type(), v.Func)
	case *ir.Global:
		if v.Addr == nil {
			return addrOf(mref{kind: mStatic, sym: fn.g.staticData(v)}, v.Type())
		}
		a := fn.g.constLoc(v.Addr)
		if v.Addr.Typ == types.Type(types.UnsafeKinds["Far"]) {
			return addrOf(mref{kind: mFar, ptr: fn.scalar(a)}, v.Type())
		}
		return addrOf(mref{kind: mPtr, ptr: fn.scalar(a)}, v.Type())
	}
	fn.errorf("codegen: no location for %s (%T)", v.Name(), v)
	return &loc{t: v.Type(), zero: true}
}

// scalar returns an operand of a value of at most 8 bytes, for a
// template.
func (fn *fnGen) scalar(l *loc) operand {
	size := fn.g.sizeof(l.t)
	switch {
	case l.parts != nil:
		if len(l.parts) == 1 || size <= fn.g.word {
			p := l.parts[0]
			p.width = size * 8
			return p
		}
		var v uint64
		for i := len(l.parts) - 1; i >= 0; i-- {
			if l.parts[i].kind != opImm {
				fn.errorf("codegen: a constant with an address part used as a number")
			}
			v = v<<uint(fn.g.word*8) | l.parts[i].imm
		}
		return imm(v, size*8)
	case l.zero:
		return imm(0, size*8)
	case l.addr != nil:
		return fn.addrOf(*l.addr)
	case l.at.kind == mFrame || l.at.kind == mArg:
		return slotOperand(*l.at, size)
	}
	t := fn.temp(size)
	fn.copyMem(slotRef(t), *l.at, size)
	return t
}

// value returns the operand of an IR value of at most 8 bytes.
func (fn *fnGen) value(v ir.Value) operand { return fn.scalar(fn.loc(v)) }

// part returns word part i of a value, or its byte if it has one byte.
func (fn *fnGen) part(l *loc, i int) operand {
	g := fn.g
	size := g.sizeof(l.t)
	w := g.word
	if size < g.word {
		w = size
	}
	switch {
	case l.parts != nil:
		if i < len(l.parts) {
			p := l.parts[i]
			p.width = w * 8
			return p
		}
		return imm(0, w*8)
	case l.zero:
		return imm(0, w*8)
	case l.addr != nil:
		return fn.addrOf(*l.addr)
	}
	return fn.readWord(l.at.plus(i*g.word), w)
}

// readWord returns an operand of the value of size bytes (1 or a word)
// at place m.
func (fn *fnGen) readWord(m mref, size int) operand {
	switch m.kind {
	case mFrame, mArg:
		return slotOperand(m, size)
	}
	t := fn.temp(size)
	if m.kind == mFar {
		fn.op("load.far", size*8, 0, map[string]operand{"r": t, "p": m.ptr, "k": imm(uint64(m.off), 0)}, nil)
		return t
	}
	p, k := fn.base(m)
	fn.op("load", size*8, 0, map[string]operand{"r": t, "p": p, "k": imm(uint64(k), 0)}, nil)
	return t
}

// base returns an address operand and an offset for place m in memory
// that is not in the frame.
func (fn *fnGen) base(m mref) (operand, int) {
	switch m.kind {
	case mStatic:
		return operand{kind: opSym, sym: m.sym, width: fn.g.ptr * 8}, m.off
	case mPtr:
		return m.ptr, m.off
	}
	return fn.addrOf(m), 0
}

// store writes the value x of size bytes (1 or a word, or wider for a
// constant split into words) at place m.
func (fn *fnGen) store(m mref, x operand, size int) {
	g := fn.g
	if size > g.word {
		for i := 0; i < size/g.word; i++ {
			var p operand
			switch x.kind {
			case opImm:
				p = imm(x.imm>>uint(i*g.word*8), g.word*8)
			case opSlot, opArg:
				p = x
				p.off += i * g.word
				p.width = g.word * 8
			default:
				p = x
			}
			fn.writeWord(m.plus(i*g.word), p, g.word)
		}
		return
	}
	fn.writeWord(m, x, size)
}

// writeWord writes x, of size bytes (1 or a word), at place m.
func (fn *fnGen) writeWord(m mref, x operand, size int) {
	x.width = size * 8
	switch m.kind {
	case mFrame, mArg:
		fn.op("move", size*8, 0, map[string]operand{"r": slotOperand(m, size), "x": x}, nil)
		return
	}
	if m.kind == mFar {
		fn.op("store.far", size*8, 0, map[string]operand{"p": m.ptr, "k": imm(uint64(m.off), 0), "x": x}, nil)
		return
	}
	p, k := fn.base(m)
	fn.op("store", size*8, 0, map[string]operand{"p": p, "k": imm(uint64(k), 0), "x": x}, nil)
}

// addrOf returns an operand of the address of place m: a far address
// for the memory of a hardware mapping.
func (fn *fnGen) addrOf(m mref) operand {
	g := fn.g
	switch m.kind {
	case mFar:
		if m.off == 0 {
			return m.ptr
		}
		t := fn.temp(4)
		fn.op("add", g.word*8, 0, map[string]operand{"r": slotOperand(slotRef(t), g.word), "x": partOf(m.ptr, 0, g.word), "y": imm(uint64(m.off), g.word*8)}, nil)
		fn.writeWord(slotRef(t).plus(g.word), partOf(m.ptr, 1, g.word), g.word)
		return t
	case mStatic:
		return operand{kind: opSym, sym: m.sym, add: m.off, width: g.ptr * 8}
	case mPtr:
		if m.off == 0 {
			return m.ptr
		}
		t := fn.temp(g.ptr)
		fn.op("add", g.ptr*8, 0, map[string]operand{"r": t, "x": m.ptr, "y": imm(uint64(m.off), g.ptr*8)}, nil)
		return t
	}
	t := fn.temp(g.ptr)
	x := slotOperand(m, 0)
	x.width = 0
	fn.op("addr", 0, 0, map[string]operand{"r": t, "x": x}, nil)
	return t
}

// copyTo copies the value l of n bytes to place dst.
func (fn *fnGen) copyTo(dst mref, l *loc, n int) {
	g := fn.g
	switch {
	case n == 0:
	case l.zero:
		fn.zeroMem(dst, n)
	case l.parts != nil, l.addr != nil:
		if n < g.word {
			fn.writeWord(dst, fn.part(l, 0), n)
			return
		}
		for i := 0; i*g.word < n; i++ {
			fn.writeWord(dst.plus(i*g.word), fn.part(l, i), g.word)
		}
	default:
		fn.copyMem(dst, *l.at, n)
	}
}

// copyMem copies n bytes from place src to place dst.
func (fn *fnGen) copyMem(dst, src mref, n int) {
	g := fn.g
	if dst == src || n == 0 {
		return
	}
	if n <= 4*g.word || dst.kind == mFar || src.kind == mFar {
		off := 0
		for ; off+g.word <= n; off += g.word {
			fn.writeWord(dst.plus(off), fn.readWord(src.plus(off), g.word), g.word)
			fn.releaseLoopTemps()
		}
		for ; off < n; off++ {
			fn.writeWord(dst.plus(off), fn.readWord(src.plus(off), 1), 1)
		}
		return
	}
	p := fn.addrOf(dst)
	q := fn.addrOf(src)
	fn.op("copy", 0, 0, map[string]operand{"p": p, "q": q, "n": imm(uint64(n), 0)}, nil)
}

// zeroMem sets n bytes at place dst to 0.
func (fn *fnGen) zeroMem(dst mref, n int) {
	g := fn.g
	if n <= 4*g.word || dst.kind == mFar {
		off := 0
		for ; off+g.word <= n; off += g.word {
			fn.writeWord(dst.plus(off), imm(0, g.word*8), g.word)
		}
		for ; off < n; off++ {
			fn.writeWord(dst.plus(off), imm(0, 8), 1)
		}
		return
	}
	fn.op("zero", 0, 0, map[string]operand{"p": fn.addrOf(dst), "n": imm(uint64(n), 0)}, nil)
}

// place returns the memory place that an address value points to.
func (fn *fnGen) place(addr ir.Value) mref {
	l := fn.loc(addr)
	if l.addr != nil {
		return *l.addr
	}
	return mref{kind: mPtr, ptr: fn.scalar(l)}
}

// ---------------------------------------------------------------------
// Functions.

// argLayout returns the offsets of the arguments and results in an
// argument area and its size (comp.gen.frame).
func (g *gen) argLayout(params, results []types.Type) (poffs, roffs []int, size int) {
	off := 0
	for _, t := range params {
		off = roundUp(off, g.align)
		poffs = append(poffs, off)
		off += g.sizeof(t)
	}
	for _, t := range results {
		off = roundUp(off, g.align)
		roffs = append(roffs, off)
		off += g.sizeof(t)
	}
	return poffs, roffs, roundUp(off, g.align)
}

// bindingLayout returns the offsets of the free variables in a closure
// record, after the address of the code.
func (g *gen) bindingLayout(fvs []*ir.FreeVar) []int {
	var ts []types.Type
	for _, v := range fvs {
		ts = append(ts, v.Typ)
	}
	offs, _, _ := g.argLayout(ts, nil)
	for k := range offs {
		offs[k] += roundUp(g.ptr, g.align)
	}
	return offs
}

// funcValue returns the location of the value of function f: the
// address of a record in the constant data that holds the address of its
// code. A function value is the address of a record whose first word is
// the address of the code; a closure record holds the values of its free
// variables after it.
func (g *gen) funcValue(t types.Type, f *ir.Func) *loc {
	code := g.funcLabel(f)
	g.addressTaken(code, sigKey(paramTypes(f), resultTypes(f.Sig)))
	return &loc{t: t, parts: []operand{{kind: opSym, sym: g.funcRecord(code), width: g.ptr * 8}}}
}

// funcRecord returns the label of the constant record of the code at
// label code.
func (g *gen) funcRecord(code string) string {
	l := "C" + code
	if g.funcsByLabel[l] {
		return l
	}
	g.funcsByLabel[l] = true
	g.data = append(g.data, &dataItem{label: l, bytes: make([]byte, g.ptr), align: g.scalarAlign(g.ptr), relocs: []reloc{{off: 0, sym: code}}})
	return l
}

// paramTypes returns the types of the parameters of f as they are
// passed: an inout parameter is an address.
func paramTypes(f *ir.Func) []types.Type {
	var ts []types.Type
	for _, p := range f.Params {
		t := p.Typ
		if p.Inout {
			t = &ir.Pointer{Elem: p.Typ}
		}
		ts = append(ts, t)
	}
	return ts
}

func resultTypes(sig *types.Signature) []types.Type {
	var ts []types.Type
	if sig.Results != nil {
		for _, v := range sig.Results.Vars {
			ts = append(ts, v.Type())
		}
	}
	return ts
}

// function generates the code of f.
func (g *gen) function(f *ir.Func) {
	fn := &fnGen{g: g, f: f, name: f.Name, vals: map[ir.Value]*loc{}, free: map[int][]int{}, blocks: map[*ir.Block]string{}, traps: map[string]string{}}
	pts := paramTypes(f)
	fn.resTypes = resultTypes(f.Sig)
	poffs, roffs, size := g.argLayout(pts, fn.resTypes)
	for i, p := range f.Params {
		fn.vals[p] = at(mref{kind: mFrame, off: g.arch.ABI.Params + poffs[i]}, pts[i])
	}
	if len(f.FreeVars) > 0 {
		// The closure record: its address follows the results.
		ctxp := operand{kind: opSlot, off: g.arch.ABI.Params + size, width: g.ptr * 8}
		boffs := g.bindingLayout(f.FreeVars)
		for k, v := range f.FreeVars {
			fn.vals[v] = at(mref{kind: mPtr, ptr: ctxp, off: boffs[k]}, v.Typ)
		}
	}
	if len(roffs) > 0 {
		fn.results = g.arch.ABI.Params + roffs[0]
	}
	for _, b := range f.Blocks {
		fn.blocks[b] = g.label()
	}
	fn.fmtOnly = fmtOnlyConversions(f)
	fn.defLabel(g.funcs[f])
	fn.label = g.funcs[f]
	fn.handler = g.handlers[f] && g.hasRule("enter.handler", 0, 0)
	fn.op(fn.enterOp(), 0, 0, map[string]operand{"n": {kind: opFrame}}, nil)
	if hasDefer(f) {
		g.usesDefer = true
		fn.deferAt = fn.slot(g.ptr)
		fn.writeWord(mref{kind: mFrame, off: fn.deferAt}, fn.readWord(mref{kind: mStatic, sym: "Ldefers"}, g.ptr), g.ptr)
		fn.releaseTemps()
	}
	for i, b := range f.Blocks {
		fn.cur = b
		if i > 0 || len(b.Preds) > 0 {
			fn.defLabel(fn.blocks[b])
		}
		for _, in := range b.Instrs {
			fn.instr(in, i)
			fn.releaseTemps()
		}
	}
	names := make([]string, 0, len(fn.traps))
	for name := range fn.traps {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fn.defLabel(fn.traps[name])
		fn.callRuntime(name, nil)
	}
	fn.finish()
}

// fmtOnlyConversions returns the conversions to interfaces of f that
// are used only as direct arguments of the printing functions of fmt.
func fmtOnlyConversions(f *ir.Func) map[*ir.MakeInterface]bool {
	only := map[*ir.MakeInterface]bool{}
	other := map[*ir.MakeInterface]bool{}
	for _, b := range f.Blocks {
		for _, in := range b.Instrs {
			if mi, ok := in.(*ir.MakeInterface); ok {
				only[mi] = true
			}
		}
	}
	for _, b := range f.Blocks {
		for _, in := range b.Instrs {
			fmtCall := false
			if c, ok := in.(*ir.Call); ok {
				if bi, ok := c.Callee.(*ir.Builtin); ok && (bi.Bname == "fmt.Print" || bi.Bname == "fmt.Println" || bi.Bname == "fmt.Printf") {
					fmtCall = true
				}
			}
			for _, op := range in.Operands() {
				if mi, ok := op.(*ir.MakeInterface); ok && !fmtCall {
					other[mi] = true
				}
			}
		}
	}
	for mi := range other {
		delete(only, mi)
	}
	return only
}

func hasDefer(f *ir.Func) bool {
	for _, b := range f.Blocks {
		for _, in := range b.Instrs {
			if _, ok := in.(*ir.Defer); ok {
				return true
			}
		}
	}
	return false
}

// finish resolves the offsets of the argument area, of the slots
// addressed from the bottom of the frame and the frame size, and adds
// the code of the function to the program.
func (fn *fnGen) finish() {
	frame := roundUp(fn.frame+fn.args, fn.g.align)
	if s := fn.stackInfo(); s != nil {
		s.frame = frame + fn.g.arch.ABI.Params
		if fn.handler {
			s.frame = frame + fn.g.arch.ABI.InterruptFrame
			s.handler = true
		}
	}
	for i := range fn.code {
		t := fn.code[i].text
		if t == "" {
			continue
		}
		t = argRef.ReplaceAllStringFunc(t, func(m string) string {
			k, _ := strconv.Atoi(argRef.FindStringSubmatch(m)[1])
			return fmt.Sprintf("%+d", k-frame)
		})
		t = slotTok.ReplaceAllStringFunc(t, func(m string) string {
			k, _ := strconv.Atoi(slotTok.FindStringSubmatch(m)[1])
			return strconv.Itoa(frame + k) // from the bottom of the frame
		})
		fn.code[i].text = strings.ReplaceAll(t, "\x01F\x01", strconv.Itoa(frame))
	}
	if fn.g.structured {
		fn.code = fn.structure(fn.code)
	}
	fn.g.code = append(fn.g.code, fn.code...)
}

// trap returns the label of a call of the runtime function name, for the
// trap label of checked operations.
func (fn *fnGen) trap(name string) string {
	if l, ok := fn.traps[name]; ok {
		return l
	}
	l := fn.g.label()
	fn.traps[name] = l
	return l
}
