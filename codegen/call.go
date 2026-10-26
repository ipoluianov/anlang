package codegen

import (
	"strings"

	"an/build"
	"an/ir"
	"an/syntax"
	"an/types"
)

// callTypes returns the result types of a call of type t.
func callTypes(t types.Type) []types.Type {
	if tu, ok := t.(*types.Tuple); ok {
		var ts []types.Type
		for _, v := range tu.Vars {
			ts = append(ts, v.Type())
		}
		return ts
	}
	return []types.Type{t}
}

// call emits a call of the function at label target, or at the address
// ind if target is "", with the arguments args, and returns the location
// of the result: nil without results, a tuple with several
// (comp.gen.frame).
func (fn *fnGen) call(target string, ind *loc, args []*loc, results []types.Type) *loc {
	g := fn.g
	var pts []types.Type
	for _, a := range args {
		pts = append(pts, a.t)
	}
	poffs, roffs, size := g.argLayout(pts, results)
	fn.args = max(fn.args, size)
	for k, a := range args {
		if a.addr != nil && a.addr.kind == mFar {
			fn.unsupported(25, "inout access to a hardware mapping at a far address")
		}
		fn.copyTo(mref{kind: mArg, off: poffs[k]}, a, g.sizeof(a.t))
	}
	if target != "" {
		fn.op("call", 0, 0, nil, map[string]string{"f": target})
	} else {
		// A function value is the address of a record that starts with
		// the address of the code; the record is passed after the
		// results, for the free variables of a closure.
		fn.args = max(fn.args, size+g.ptr)
		rec := fn.scalar(ind)
		fn.writeWord(mref{kind: mArg, off: size}, rec, g.ptr)
		code := fn.readWord(mref{kind: mPtr, ptr: rec}, g.ptr)
		fn.op("callind", 0, 0, map[string]operand{"x": code}, nil)
	}
	switch len(results) {
	case 0:
		return nil
	case 1:
		r := at(mref{kind: mFrame, off: fn.slot(g.sizeof(results[0]))}, results[0])
		fn.copyMem(*r.at, mref{kind: mArg, off: roffs[0]}, g.sizeof(results[0]))
		return r
	}
	tt := types.NewTuple(results...)
	r := at(mref{kind: mFrame, off: fn.slot(g.sizeof(tt))}, tt)
	for k, t := range results {
		fn.copyMem(r.at.plus(g.tupleOffset(tt, k)), mref{kind: mArg, off: roffs[k]}, g.sizeof(t))
	}
	return r
}

// callRuntime calls function name of the runtime package and returns the
// location of its result.
func (fn *fnGen) callRuntime(name string, args []*loc) *loc {
	f, label := fn.g.rt(name)
	if f == nil {
		return nil
	}
	pts := paramTypes(f)
	for k := range args {
		if k < len(pts) && fn.g.sizeof(args[k].t) == fn.g.sizeof(pts[k]) {
			args[k] = &loc{t: pts[k], parts: args[k].parts, zero: args[k].zero, at: args[k].at, addr: args[k].addr}
		}
	}
	return fn.call(label, nil, args, resultTypes(f.Sig))
}

// callInstr generates an IR call.
func (fn *fnGen) callInstr(i *ir.Call) {
	results := callTypes(i.Type())
	switch c := i.Callee.(type) {
	case *ir.Builtin:
		fn.builtin(i, c)
		return
	case *ir.FuncValue:
		if name, ok := strings.CutPrefix(c.Func.Name, build.RuntimePath+"."); ok && fn.primitive(name, i.Args) {
			return
		}
		if name, ok := strings.CutPrefix(c.Func.Name, "an/arch/"+fn.g.arch.Name+"."); ok && name != "Far" {
			fn.instrCall(i, name)
			return
		}
		var args []*loc
		for _, a := range i.Args {
			args = append(args, fn.loc(a))
		}
		if r := fn.call(fn.g.funcLabel(c.Func), nil, args, results); r != nil {
			r.t = i.Type()
			fn.vals[i] = r
		}
		return
	}
	if i.Method != "" {
		fn.invoke(i)
		return
	}
	var args []*loc
	for _, a := range i.Args {
		args = append(args, fn.loc(a))
	}
	if r := fn.call("", fn.loc(i.Callee), args, results); r != nil {
		r.t = i.Type()
		fn.vals[i] = r
	}
}

// instrCall generates a call of a function of the instruction package:
// the instruction, with the registers loaded from the Regs argument and
// stored into the Regs result (target.arch.instr-package). The reserved
// registers of the ABI are neither loaded nor stored.
func (fn *fnGen) instrCall(i *ir.Call, name string) {
	g := fn.g
	n := len(i.Args)
	var imms []uint64
	for _, a := range i.Args[:n-1] {
		c, ok := fn.constInt(fn.loc(a))
		if !ok {
			fn.errorf("the operands of %s.%s other than the registers must be constants (target.arch.instr-package)", g.arch.Name, name)
			return
		}
		imms = append(imms, uint64(c))
	}
	text, ok := g.arch.PackageInstruction(name, imms)
	if !ok {
		fn.errorf("the instruction package of %s has no function %s", g.arch.Name, name)
		return
	}
	st := i.Type().Underlying().(*types.Struct)
	size := g.sizeof(i.Type())
	in := fn.loc(i.Args[n-1])
	if in.at == nil || in.at.kind != mFrame && in.at.kind != mArg {
		t := at(mref{kind: mFrame, off: fn.slot(size)}, i.Type())
		fn.copyTo(*t.at, in, size)
		in = t
	}
	r := fn.result(i)
	fn.copyTo(*r.at, in, size)
	reserved := map[string]bool{}
	for _, name := range g.arch.ABI.Reserved {
		reserved[name] = true
	}
	regs := g.arch.PackageRegisters()
	transfer := func(op string, k int) {
		reg := regs[k]
		if reserved[reg.Name] {
			return
		}
		f := st.Fields[k]
		fs := g.sizeof(f.Type())
		ops := map[string]operand{"reg": {kind: opReg, sym: reg.Name}}
		if reg.Name == g.arch.ABI.Flags {
			// The flags are only stored: loading them could change the
			// state of the processor, such as whether interrupts are on.
			if op == "regin" {
				return
			}
			op = "flagsout"
			delete(ops, "reg")
		}
		if op == "regin" {
			ops["x"] = fn.scalar(fn.sub(f.Type(), in, g.fieldOffset(st, k)))
		} else {
			ops["r"] = slotOperand(r.at.plus(g.fieldOffset(st, k)), fs)
		}
		// A register that no template can move, such as a flags
		// register, keeps its value.
		if lines, ok := g.selectRule(op, reg.Width, 0, ops, nil); ok {
			fn.emitLines(lines)
		}
	}
	// The flags are stored last: their template may use other registers,
	// and moves do not change them.
	order := make([]int, 0, len(regs))
	for k, reg := range regs {
		if reg.Name == g.arch.ABI.Flags {
			order = append([]int{k}, order...)
		} else {
			order = append(order, k)
		}
	}
	for _, k := range order {
		transfer("regin", k)
	}
	fn.emitLines([]string{text})
	for i := len(order) - 1; i >= 0; i-- {
		transfer("regout", order[i])
	}
}

// primitive generates a call of a primitive of the runtime package with
// the platform operations (comp.gen.runtime).
func (fn *fnGen) primitive(name string, args []ir.Value) bool {
	switch name {
	case "write", "writeString":
		p := fn.loc(args[1])
		n := fn.sub(types.Typ[types.Int], p, fn.g.ptr)
		fn.op("write", 0, 0, map[string]operand{"x": fn.value(args[0]), "p": fn.part(p, 0), "y": fn.scalar(n)}, nil)
	case "exit":
		fn.op("exit", 0, 0, map[string]operand{"x": fn.value(args[0])}, nil)
	case "runDefers":
		fn.g.usesDefer = true
		fn.op("call", 0, 0, nil, map[string]string{"f": fn.g.runDefersLabel()})
	default:
		return false
	}
	return true
}

// ret generates a return: the results are written to the argument area
// of the caller, the deferred calls of the frame run, and the function
// returns.
func (fn *fnGen) ret(results []ir.Value) {
	g := fn.g
	if fn.escape {
		for _, t := range fn.resTypes {
			if containsSlice(t) {
				fn.unsupported(25, "returning a slice of an array in the frame (comp.gen.escape)")
				break
			}
		}
	}
	if len(results) > 0 {
		_, roffs, _ := g.argLayout(paramTypes(fn.f), fn.resTypes)
		for k, v := range results {
			fn.copyTo(mref{kind: mFrame, off: g.arch.ABI.Params + roffs[k]}, fn.loc(v), g.sizeof(fn.resTypes[k]))
		}
	}
	if fn.deferAt != 0 {
		fn.runDefers(operand{kind: opSlot, off: fn.deferAt, width: g.ptr * 8})
	}
	fn.op("leave", 0, 0, nil, nil)
}

// ---------------------------------------------------------------------
// Deferred calls (stmt.defer.go).
//
// A deferred call is a record in the frame of the function: the address
// of the next record, the address of a thunk and the arguments. The
// static word Ldefers holds the newest record. The thunk takes the
// address of the record and makes the call.

// deferCall generates a defer statement.
func (fn *fnGen) deferCall(c *ir.Call) {
	g := fn.g
	if fn.inLoop(fn.cur) {
		fn.unsupported(25, "defer statements in loops")
		return
	}
	var args []*loc
	var ts []types.Type
	var callee *loc
	if _, ok := c.Callee.(*ir.FuncValue); !ok && c.Method == "" {
		if _, ok := c.Callee.(*ir.Builtin); !ok {
			callee = fn.loc(c.Callee)
			args = append(args, callee)
			ts = append(ts, callee.t)
		}
	}
	if c.Method != "" {
		recv := fn.loc(c.Callee)
		args = append(args, recv)
		ts = append(ts, recv.t)
	}
	for _, a := range c.Args {
		l := fn.loc(a)
		args = append(args, l)
		ts = append(ts, l.t)
	}
	// The record.
	hdr := 2 * g.ptr
	offs, _, size := g.argLayout(ts, nil)
	rec := mref{kind: mFrame, off: fn.slot(hdr + size)}
	for k, a := range args {
		fn.copyTo(rec.plus(hdr+offs[k]), a, g.sizeof(a.t))
	}
	head := mref{kind: mStatic, sym: "Ldefers"}
	fn.writeWord(rec, fn.readWord(head, g.ptr), g.ptr)
	thunk := g.label()
	fn.writeWord(rec.plus(g.ptr), operand{kind: opSym, sym: g.funcRecord(thunk), width: g.ptr * 8}, g.ptr)
	fn.writeWord(head, fn.addrOf(rec), g.ptr)
	pos := fn.f.Pos
	g.thunks = append(g.thunks, func() { g.thunk(thunk, c, ts, callee != nil, fn.name, pos) })
}

// inLoop reports whether block b is in a cycle of the control flow.
func (fn *fnGen) inLoop(b *ir.Block) bool {
	seen := map[*ir.Block]bool{}
	var walk func(x *ir.Block) bool
	walk = func(x *ir.Block) bool {
		for _, s := range x.Succs {
			if s == b {
				return true
			}
			if !seen[s] {
				seen[s] = true
				if walk(s) {
					return true
				}
			}
		}
		return false
	}
	return walk(b)
}

// thunk generates the thunk of a deferred call: a function whose
// parameter is the address of the record.
func (g *gen) thunk(label string, c *ir.Call, ts []types.Type, indirect bool, from string, pos syntax.Pos) {
	f := ir.NewFunc(from+"$defer", &types.Signature{Params: types.NewTuple(types.Typ[types.Usize]), Results: types.NewTuple()}, pos)
	fn := &fnGen{g: g, f: f, name: f.Name, vals: map[ir.Value]*loc{}, free: map[int][]int{}, blocks: map[*ir.Block]string{}, traps: map[string]string{}}
	fn.defLabel(label)
	fn.op("enter", 0, 0, map[string]operand{"n": {kind: opFrame}}, nil)
	recPtr := operand{kind: opSlot, off: g.arch.ABI.Params, width: g.ptr * 8}
	offs, _, _ := g.argLayout(ts, nil)
	var args []*loc
	for k, t := range ts {
		args = append(args, at(mref{kind: mPtr, ptr: recPtr, off: 2*g.ptr + offs[k]}, t))
	}
	fn.makeCall(c, args, indirect)
	fn.releaseTemps()
	fn.op("leave", 0, 0, nil, nil)
	fn.finish()
}

// makeCall generates call c with the given argument locations: the
// function value first if indirect, the receiver first for a method.
func (fn *fnGen) makeCall(c *ir.Call, args []*loc, indirect bool) {
	results := callTypes(c.Type())
	switch callee := c.Callee.(type) {
	case *ir.Builtin:
		fn.builtinArgs(callee.Bname, c, args)
		return
	case *ir.FuncValue:
		fn.call(fn.g.funcLabel(callee.Func), nil, args, results)
		return
	}
	if c.Method != "" {
		fn.dispatch(args[0], c.Method, args[1:], results)
		return
	}
	if indirect {
		fn.call("", args[0], args[1:], results)
	}
}

// runDefers runs the records of the chain until the chain is base: a
// slot with the chain at the entry of the function.
func (fn *fnGen) runDefers(base operand) {
	g := fn.g
	loop, done := g.label(), g.label()
	head := mref{kind: mStatic, sym: "Ldefers"}
	fn.defLabel(loop)
	cur := fn.temp(g.ptr)
	fn.writeWord(slotRef(cur), fn.readWord(head, g.ptr), g.ptr)
	fn.branchIf(fn.test("eq", g.ptr*8, cur, base), done)
	next := fn.readWord(mref{kind: mPtr, ptr: cur}, g.ptr)
	fn.writeWord(head, next, g.ptr)
	thunk := fn.temp(g.ptr)
	fn.writeWord(slotRef(thunk), fn.readWord(mref{kind: mPtr, ptr: cur, off: g.ptr}, g.ptr), g.ptr)
	fn.call("", at(slotRef(thunk), types.Typ[types.Usize]), []*loc{at(slotRef(cur), types.Typ[types.Usize])}, nil)
	fn.op("jump", 0, 0, nil, map[string]string{"L": loop})
	fn.defLabel(done)
}

// runDefersLabel returns the label of the routine that runs all pending
// deferred calls, for the panic functions.
func (g *gen) runDefersLabel() string {
	const label = "LrunDefers"
	if g.funcsByLabel[label] {
		return label
	}
	g.funcsByLabel[label] = true
	g.thunks = append(g.thunks, func() {
		f := ir.NewFunc("runtime.runDefers", &types.Signature{Params: types.NewTuple(), Results: types.NewTuple()}, g.prog.Main.Pos)
		fn := &fnGen{g: g, f: f, name: f.Name, vals: map[ir.Value]*loc{}, free: map[int][]int{}, blocks: map[*ir.Block]string{}, traps: map[string]string{}}
		fn.defLabel(label)
		fn.op("enter", 0, 0, map[string]operand{"n": {kind: opFrame}}, nil)
		fn.runDefers(imm(0, g.ptr*8))
		fn.releaseTemps()
		fn.op("leave", 0, 0, nil, nil)
		fn.finish()
	})
	return label
}

// entry generates the entry code (target.platform.entry).
func (g *gen) entry() {
	f := ir.NewFunc("entry", &types.Signature{Params: types.NewTuple(), Results: types.NewTuple()}, g.prog.Main.Pos)
	fn := &fnGen{g: g, f: f, name: "entry", vals: map[ir.Value]*loc{}, free: map[int][]int{}, blocks: map[*ir.Block]string{}, traps: map[string]string{}}
	fn.defLabel("entry")
	if g.hasRule("start", 0, 0) {
		fn.op("start", 0, 0, nil, nil)
	}
	fn.op("call", 0, 0, nil, map[string]string{"f": g.funcLabel(g.prog.Main)})
	fn.op("exit", 0, 0, map[string]operand{"x": imm(0, g.word*8)}, nil)
	fn.finish()
}

// asmInstr generates an asm statement (stmt.asm.syntax): the registers
// named by the fields of the input struct are loaded, the instructions
// follow with their labels made unique, and the registers named by the
// fields of the result are stored.
func (fn *fnGen) asmInstr(i *ir.Asm) {
	g := fn.g
	reserved := map[string]bool{}
	for _, name := range g.arch.ABI.Reserved {
		reserved[name] = true
	}
	transfer := func(op, field string, t types.Type, value *loc, dst mref) {
		reg := g.arch.Register(field)
		if reg == nil {
			fn.errorf("asm: %s is not a register of %s", field, g.arch.Name)
			return
		}
		ops := map[string]operand{"reg": {kind: opReg, sym: reg.Name}}
		switch {
		case op == "regin" && (reg.Name == g.arch.ABI.Flags || reserved[reg.Name]):
			// As for the instruction package: the flags and the registers
			// that generated code relies on are not loaded.
			return
		case reg.Name == g.arch.ABI.Flags:
			op = "flagsout"
			delete(ops, "reg")
		}
		if op == "regin" {
			ops["x"] = fn.scalar(value)
		} else {
			ops["r"] = slotOperand(dst, g.sizeof(t))
		}
		if lines, ok := g.selectRule(op, reg.Width, 0, ops, nil); ok {
			fn.emitLines(lines)
		} else {
			fn.errorf("asm: architecture %s has no rule to transfer register %s", g.arch.Name, reg.Name)
		}
	}
	// The result is cleared before any register is loaded: the templates
	// that clear it may use registers.
	st, hasOut := i.Type().Underlying().(*types.Struct)
	var r *loc
	if hasOut {
		r = fn.result(i)
		fn.zeroMem(*r.at, g.sizeof(i.Type()))
	}
	if i.In != nil {
		in := fn.loc(i.In)
		st := i.In.Type().Underlying().(*types.Struct)
		if in.at == nil || in.at.kind != mFrame && in.at.kind != mArg {
			t := at(mref{kind: mFrame, off: fn.slot(g.sizeof(i.In.Type()))}, i.In.Type())
			fn.copyTo(*t.at, in, g.sizeof(i.In.Type()))
			in = t
		}
		for k, f := range st.Fields {
			transfer("regin", f.Name(), f.Type(), fn.sub(f.Type(), in, g.fieldOffset(st, k)), mref{})
		}
	}
	// The instructions, with labels local to this statement.
	labels := map[string]string{}
	var lines []string
	for _, line := range strings.Split(i.Text, "\n") {
		if k := strings.Index(line, ";"); k >= 0 {
			line = line[:k]
		}
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ":") {
			labels[strings.TrimSuffix(line, ":")] = g.label()
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	for _, line := range lines {
		if name, ok := strings.CutSuffix(line, ":"); ok {
			fn.defLabel(labels[name])
			continue
		}
		fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
		for _, f := range fields {
			if l, ok := labels[f]; ok {
				line = replaceWord(line, f, "<"+l+"+0>")
			}
		}
		fn.emitLines([]string{line})
	}
	if !hasOut {
		return
	}
	// The flags last: their template may use other registers.
	var flags int = -1
	for k, f := range st.Fields {
		if reg := g.arch.Register(f.Name()); reg != nil && reg.Name == g.arch.ABI.Flags {
			flags = k
			continue
		}
		transfer("regout", f.Name(), f.Type(), nil, r.at.plus(g.fieldOffset(st, k)))
	}
	if flags >= 0 {
		f := st.Fields[flags]
		transfer("regout", f.Name(), f.Type(), nil, r.at.plus(g.fieldOffset(st, flags)))
	}
}

// replaceWord replaces the whole-word occurrences of w in s by r.
func replaceWord(s, w, r string) string {
	var b strings.Builder
	isWord := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], w) && (i == 0 || !isWord(s[i-1])) && (i+len(w) == len(s) || !isWord(s[i+len(w)])) {
			b.WriteString(r)
			i += len(w)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
