package codegen

import (
	"go/constant"
	"strings"

	"an/ir"
	"an/types"
)

// builtin generates a call of a built-in function.
func (fn *fnGen) builtin(i *ir.Call, b *ir.Builtin) {
	if b.Bname == "fmt.Printf" {
		fn.printf(i.Args)
		return
	}
	var args []*loc
	for _, a := range i.Args {
		// fmt prints the dynamic values of its interface arguments; when
		// they are converted here, their static types are known.
		if mi, ok := a.(*ir.MakeInterface); ok && strings.HasPrefix(b.Bname, "fmt.") {
			args = append(args, fn.loc(mi.X))
			continue
		}
		args = append(args, fn.loc(a))
	}
	if r := fn.builtinArgs(b.Bname, i, args); r != nil {
		r.t = i.Type()
		fn.vals[i] = r
	}
}

// builtinArgs generates built-in function name with argument locations.
func (fn *fnGen) builtinArgs(name string, c *ir.Call, args []*loc) *loc {
	g := fn.g
	switch name {
	case "len", "cap":
		x := args[0]
		switch u := x.t.Underlying().(type) {
		case *types.Array:
			return fn.intLoc(int(u.Len))
		case *types.Slice:
			if name == "cap" {
				return fn.sub(types.Typ[types.Int], x, g.ptr+g.intBits/8)
			}
		}
		return fn.sub(types.Typ[types.Int], x, g.ptr)
	case "min", "max":
		return fn.minMax(name, c.Type(), args)
	case "print", "println":
		fn.printArgs(2, args, false, name == "println")
		return nil
	case "fmt.Print", "fmt.Println":
		fn.printArgs(1, args, true, name == "fmt.Println")
		return nil
	case "effect.Mark", "effect.Defer":
		return nil
	case "Option.Get":
		return fn.optionGet(c.Type().(*types.Tuple), args[0])
	case "decoderune":
		return fn.callRuntime("DecodeRune", args)
	case "console.ReadByte":
		return fn.callRuntime("ReadByte", nil)
	case "unsafe.Data":
		r := at(mref{kind: mFrame, off: fn.slot(g.ptr)}, types.Typ[types.Usize])
		fn.writeWord(*r.at, fn.part(args[0], 0), g.ptr)
		return r
	}
	if op, ok := strings.CutPrefix(name, "bits."); ok {
		return fn.bitsOp(op, c.Type(), args)
	}
	fn.unsupported(25, "built-in function "+name)
	return nil
}

// printf generates fmt.Printf: the format, a constant that the checker
// has checked (ct.reject.precondition), is parsed here, and each
// directive prints its argument with a function of the runtime package.
func (fn *fnGen) printf(args []ir.Value) {
	g := fn.g
	peel := func(v ir.Value) *loc {
		if mi, ok := v.(*ir.MakeInterface); ok {
			return fn.loc(mi.X)
		}
		return fn.loc(v)
	}
	fv, ok := args[0].(*ir.MakeInterface)
	var c *ir.Const
	if ok {
		c, _ = fv.X.(*ir.Const)
	} else {
		c, _ = args[0].(*ir.Const)
	}
	if c == nil || c.Val == nil || c.Val.Kind() != constant.String {
		fn.errorf("the format of fmt.Printf is not constant")
		return
	}
	format := constant.StringVal(c.Val)
	rest := args[1:]
	fd := fn.intLoc(1)
	lit := func(s string) {
		if s != "" {
			fn.printString(1, s)
		}
	}
	start := 0
	for i := 0; i < len(format); {
		if format[i] != '%' {
			i++
			continue
		}
		lit(format[start:i])
		i++
		var flags u8f
		for i < len(format) && strings.IndexByte("+-# 0", format[i]) >= 0 {
			flags |= map[byte]u8f{'-': 1, '+': 2, ' ': 4, '0': 8, '#': 16}[format[i]]
			i++
		}
		num := func() (int, bool) {
			n, any := 0, false
			for i < len(format) && '0' <= format[i] && format[i] <= '9' {
				n, any = n*10+int(format[i]-'0'), true
				i++
			}
			return n, any
		}
		if i < len(format) && format[i] == '*' {
			fn.unsupported(25, "fmt.Printf widths and precisions from arguments")
			return
		}
		width, _ := num()
		prec := -1
		if i < len(format) && format[i] == '.' {
			i++
			if i < len(format) && format[i] == '*' {
				fn.unsupported(25, "fmt.Printf widths and precisions from arguments")
				return
			}
			prec, _ = num()
		}
		verb := format[i]
		i++
		start = i
		if verb == '%' {
			lit("%")
			continue
		}
		if len(rest) == 0 {
			fn.errorf("fmt.Printf has too few arguments")
			return
		}
		a := peel(rest[0])
		rest = rest[1:]
		fn.directive(fd, verb, width, prec, flags, a)
	}
	lit(format[start:])
	_ = g
}

// u8f is a set of the flags of a directive of fmt.Printf, as the runtime
// package numbers them.
type u8f = uint8

// directive prints argument a of a directive of fmt.Printf.
func (fn *fnGen) directive(fd *loc, verb byte, width, prec int, flags u8f, a *loc) {
	g := fn.g
	t := a.t
	plain := width == 0 && prec < 0 && flags == 0
	wl, pl := fn.intLoc(width), fn.intLoc(prec)
	fl := &loc{t: types.Typ[types.U8], parts: []operand{imm(uint64(flags), 8)}}
	vl := &loc{t: types.Typ[types.U8], parts: []operand{imm(uint64(verb), 8)}}
	if ms := g.prog.Methods(t); ms != nil && strings.IndexByte("vsqxX", verb) >= 0 {
		for _, name := range []string{"Error", "String"} {
			if f := ms.Methods[name]; f != nil && f.Sig.Params.Len() == 0 && f.Sig.Results.Len() == 1 {
				s := fn.call(g.funcLabel(f), nil, []*loc{a}, resultTypes(f.Sig))
				if verb != 's' && verb != 'v' {
					break
				}
				fn.callRuntime("PrintStringF", []*loc{fd, s, wl, pl, fl})
				return
			}
		}
	}
	if bits, signed, _, ok := g.intInfo(t); ok {
		switch verb {
		case 'v', 'd', 'x', 'X', 'o', 'b':
			if verb == 'v' {
				vl.parts[0] = imm('d', 8)
			}
			wide := types.Typ[types.U64]
			name := "PrintUintF"
			if signed {
				wide, name = types.Typ[types.I64], "PrintIntF"
			}
			x := a
			if bits < 64 {
				x = fn.extend(&tempValue{wide}, a, signed)
			}
			fn.callRuntime(name, []*loc{fd, x, vl, wl, pl, fl})
			return
		case 'c':
			x := a
			if bits != 32 {
				x = fn.extend(&tempValue{types.Typ[types.I32]}, a, signed)
			}
			fn.callRuntime("PrintRune", []*loc{fd, x})
			return
		}
	}
	if isString(t) && (verb == 's' || verb == 'v') {
		fn.callRuntime("PrintStringF", []*loc{fd, a, wl, pl, fl})
		return
	}
	if isBool(t) && (verb == 't' || verb == 'v') {
		if plain {
			fn.printValue(1, a, true)
			return
		}
		yes, done := g.label(), g.label()
		fn.branchIf(fn.scalar(a), yes)
		fn.callRuntime("PrintStringF", []*loc{fd, g.stringLoc("false"), wl, pl, fl})
		fn.op("jump", 0, 0, nil, map[string]string{"L": done})
		fn.defLabel(yes)
		fn.callRuntime("PrintStringF", []*loc{fd, g.stringLoc("true"), wl, pl, fl})
		fn.defLabel(done)
		return
	}
	if bits, ok := floatBits(t); ok && strings.IndexByte("eEfFgGv", verb) >= 0 && flags&16 == 0 {
		fn.callRuntime("PrintFloatF", []*loc{fd, fn.floatAsF64(a), fn.intLoc(bits), vl, wl, pl, fl})
		return
	}
	if verb == 'v' && plain {
		fn.printValue(1, a, true)
		return
	}
	fn.unsupported(25, "fmt.Printf directive %"+string(verb)+" for values of type "+t.String())
}

// minMax generates min and max of integers and strings.
func (fn *fnGen) minMax(name string, t types.Type, args []*loc) *loc {
	g := fn.g
	size := g.sizeof(t)
	r := at(mref{kind: mFrame, off: fn.slot(size)}, t)
	fn.copyTo(*r.at, args[0], size)
	bits, signed, _, isInt := g.intInfo(t)
	for _, a := range args[1:] {
		skip := g.label()
		var less operand
		x, y := a, r // min: replace if a < r
		if name == "max" {
			x, y = r, a
		}
		switch {
		case isInt:
			op := "lt.u"
			if signed {
				op = "lt.s"
			}
			less = fn.test(op, bits, fn.scalar(x), fn.scalar(y))
		case isString(t):
			c := fn.callRuntime("CompareString", []*loc{x, y})
			less = fn.test("lt.s", g.intBits, fn.scalar(c), imm(0, g.intBits))
		default:
			// Floats: the runtime handles NaN and the signs of zeros.
			bits, _ := floatBits(t)
			m := fn.floatRuntime(nil, "M"+name[1:], bits, t, a, r)
			fn.copyTo(*r.at, m, size)
			continue
		}
		fn.branchIf(fn.test("eq", 8, less, imm(0, 8)), skip)
		fn.copyTo(*r.at, a, size)
		fn.defLabel(skip)
	}
	return r
}

// optionGet generates Option.Get: the value and true for Some, the zero
// value and false for None.
func (fn *fnGen) optionGet(tt *types.Tuple, o *loc) *loc {
	g := fn.g
	e := o.t.Underlying().(*types.Enum)
	r := at(mref{kind: mFrame, off: fn.slot(g.sizeof(tt))}, tt)
	fn.zeroMem(*r.at, g.sizeof(tt))
	none := g.label()
	tag := fn.sub(types.Typ[types.U8], o, 0)
	if g.tagSize(e) == 2 {
		tag.t = types.Typ[types.U16]
	}
	fn.branchIf(fn.test("ne", g.tagSize(e)*8, fn.scalar(tag), imm(1, g.tagSize(e)*8)), none)
	vt := tt.At(0).Type()
	fn.copyTo(*r.at, fn.sub(vt, o, g.variantFieldOffset(e, 1, 0)), g.sizeof(vt))
	fn.writeWord(r.at.plus(g.tupleOffset(tt, 1)), imm(1, 8), 1)
	fn.defLabel(none)
	return r
}

// bitsOp generates the functions of an/bits (expr.bits.functions).
func (fn *fnGen) bitsOp(name string, t types.Type, args []*loc) *loc {
	g := fn.g
	vt := t
	if tu, ok := t.(*types.Tuple); ok {
		vt = tu.At(0).Type()
	}
	bits, signed, _, _ := g.intInfo(vt)
	op := strings.ToLower(name[len(name)-3:])
	x, y := args[0], args[1]
	switch {
	case strings.HasPrefix(name, "Wrapping"):
		return fn.arith(&tempValue{vt}, op, bits, x, y, "")
	case strings.HasPrefix(name, "Checked"):
		tt := t.(*types.Tuple)
		r := at(mref{kind: mFrame, off: fn.slot(g.sizeof(tt))}, tt)
		fn.zeroMem(*r.at, g.sizeof(tt))
		over := g.label()
		fn.overflowing(op, bits, signed, x, y, slotOperand(*r.at, bits/8), over)
		fn.writeWord(r.at.plus(g.tupleOffset(tt, 1)), imm(1, 8), 1)
		fn.defLabel(over)
		return r
	}
	// Saturating: on overflow, the bound in the direction of the exact
	// result.
	r := at(mref{kind: mFrame, off: fn.slot(bits / 8)}, vt)
	over, done := g.label(), g.label()
	fn.overflowing(op, bits, signed, x, y, slotOperand(*r.at, bits/8), over)
	fn.op("jump", 0, 0, nil, map[string]string{"L": done})
	fn.defLabel(over)
	max := uint64(1)<<uint(bits) - 1
	min := uint64(0)
	if signed {
		max >>= 1
		min = max + 1
	}
	if !signed {
		bound := max
		if op == "sub" {
			bound = min
		}
		fn.store(*r.at, imm(bound, bits), bits/8)
	} else {
		// Negative exact results: add and sub follow the sign of x; mul
		// is negative if the signs differ.
		neg := fn.test("lt.s", bits, fn.scalar(x), imm(0, bits))
		if op == "mul" {
			ny := fn.test("lt.s", bits, fn.scalar(y), imm(0, bits))
			t := fn.temp(1)
			fn.op("xor", 8, 0, map[string]operand{"r": t, "x": neg, "y": ny}, nil)
			neg = t
		}
		lo := g.label()
		fn.branchIf(neg, lo)
		fn.store(*r.at, imm(max, bits), bits/8)
		fn.op("jump", 0, 0, nil, map[string]string{"L": done})
		fn.defLabel(lo)
		fn.store(*r.at, imm(min, bits), bits/8)
	}
	fn.defLabel(done)
	return r
}

// overflowing computes op of x and y into r, going to label over if the
// exact result does not fit.
func (fn *fnGen) overflowing(op string, bits int, signed bool, x, y *loc, r operand, over string) {
	g := fn.g
	name := op + ".u"
	if signed {
		name = op + ".s"
	}
	if !g.hasRule(name, bits, 0) {
		// The runtime function returns the result and whether it fits.
		vt := x.t
		res := fn.callRuntime("Checked"+runtimeName(name, bits), []*loc{x, y})
		if res == nil {
			return
		}
		tt := types.NewTuple(vt, types.Typ[types.Bool])
		res.t = tt
		fn.copyTo(slotRef(r), fn.sub(vt, res, 0), bits/8)
		fn.branchIf(fn.test("eq", 8, fn.scalar(fn.sub(types.Typ[types.Bool], res, g.tupleOffset(tt, 1))), imm(0, 8)), over)
		return
	}
	fn.op(name, bits, 0, map[string]operand{"r": r, "x": fn.scalar(x), "y": fn.scalar(y)}, map[string]string{"trap": over})
}

// ---------------------------------------------------------------------
// Printing (comp.gen.runtime): the formats of fmt.Print and of print
// (spec/22-compiler.md §22.10.3).

// printArgs prints the arguments of fmt.Print and fmt.Println (fmt set)
// or print and println to stream fd.
func (fn *fnGen) printArgs(fd int, args []*loc, fmtMode, ln bool) {
	for k, a := range args {
		if k > 0 {
			space := ln
			if !ln && fmtMode {
				// fmt.Print adds a space between operands when neither is a
				// string.
				space = !isString(args[k-1].t) && !isString(a.t)
			}
			if space {
				fn.printString(fd, " ")
			}
		}
		fn.printValue(fd, a, fmtMode)
	}
	if ln {
		fn.printString(fd, "\n")
	}
}

func (fn *fnGen) printString(fd int, s string) {
	fn.callRuntime("PrintString", []*loc{fn.intLoc(fd), fn.g.stringLoc(s)})
}

// printValue prints the value l.
func (fn *fnGen) printValue(fd int, l *loc, fmtMode bool) {
	g := fn.g
	t := l.t
	fdl := fn.intLoc(fd)
	if fmtMode {
		if ms := g.prog.Methods(t); ms != nil {
			for _, name := range []string{"Error", "String"} {
				if f := ms.Methods[name]; f != nil && f.Sig.Params.Len() == 0 && f.Sig.Results.Len() == 1 {
					s := fn.call(g.funcLabel(f), nil, []*loc{l}, resultTypes(f.Sig))
					fn.callRuntime("PrintString", []*loc{fdl, s})
					return
				}
			}
		}
	}
	if bits, signed, _, ok := g.intInfo(t); ok {
		x := l
		if bits < 16 {
			wt := types.Typ[types.U16]
			if signed {
				wt = types.Typ[types.I16]
			}
			x = fn.extend(&tempValue{wt}, l, signed)
			bits = 16
		}
		name := "PrintU"
		if signed {
			name = "PrintI"
		}
		fn.callRuntime(name+itoa(bits), []*loc{fdl, x})
		return
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			fn.callRuntime("PrintBool", []*loc{fdl, l})
			return
		case u.Info()&types.IsString != 0:
			fn.callRuntime("PrintString", []*loc{fdl, l})
			return
		case u.Info()&types.IsFloat != 0:
			fn.printFloat(fdl, l, fmtMode)
			return
		}
	case *types.Struct:
		fn.printString(fd, "{")
		for k, f := range u.Fields {
			if k > 0 {
				fn.printString(fd, " ")
			}
			fn.printValue(fd, fn.sub(f.Type(), l, g.fieldOffset(u, k)), fmtMode)
		}
		fn.printString(fd, "}")
		return
	case *types.Array:
		fn.printString(fd, "[")
		es := g.sizeof(u.Elem)
		if u.Len <= 8 || l.at == nil {
			for k := 0; k < int(u.Len); k++ {
				if k > 0 {
					fn.printString(fd, " ")
				}
				fn.printValue(fd, fn.sub(u.Elem, l, k*es), fmtMode)
			}
		} else {
			fn.printElems(fd, fn.addrOf(*l.at), fn.intLoc(int(u.Len)), u.Elem, fmtMode)
		}
		fn.printString(fd, "]")
		return
	case *types.Slice:
		if !fmtMode {
			break
		}
		fn.printString(fd, "[")
		fn.printElems(fd, fn.part(l, 0), fn.sub(types.Typ[types.Int], l, g.ptr), u.Elem, fmtMode)
		fn.printString(fd, "]")
		return
	case *types.Enum:
		fn.forVariant(l, u, func(k int) {
			v := u.Variants[k]
			fn.printString(fd, v.Name())
			if v.HasData() {
				fn.printString(fd, "{")
				for j, f := range v.Fields() {
					if j > 0 {
						fn.printString(fd, " ")
					}
					fn.printValue(fd, fn.sub(f.Type(), l, g.variantFieldOffset(u, k, j)), fmtMode)
				}
				fn.printString(fd, "}")
			}
		})
		return
	case *types.Interface:
		if !fmtMode {
			done, null := g.label(), g.label()
			fn.branchIf(fn.test("eq", g.word*8, fn.part(l, 0), imm(0, g.word*8)), null)
			fn.printString(fd, "(0x1,0x1)")
			fn.op("jump", 0, 0, nil, map[string]string{"L": done})
			fn.defLabel(null)
			fn.printString(fd, "(0x0,0x0)")
			fn.defLabel(done)
			return
		}
		fn.forDynamic(l, func(v *loc) {
			if v == nil {
				fn.printString(fd, "<nil>")
				return
			}
			fn.printValue(fd, v, fmtMode)
		})
		return
	case *types.Signature:
		done, null := g.label(), g.label()
		fn.branchIf(fn.test("eq", g.ptr*8, fn.scalar(l), imm(0, g.ptr*8)), null)
		fn.printString(fd, "0x1")
		fn.op("jump", 0, 0, nil, map[string]string{"L": done})
		fn.defLabel(null)
		if fmtMode {
			fn.printString(fd, "<nil>")
		} else {
			fn.printString(fd, "0x0")
		}
		fn.defLabel(done)
		return
	}
	fn.unsupported(25, "printing of values of type "+t.String())
}

// printElems prints n elements of type et at address base, separated by
// spaces.
func (fn *fnGen) printElems(fd int, base operand, n *loc, et types.Type, fmtMode bool) {
	g := fn.g
	es := g.sizeof(et)
	ib := g.intBits / 8
	idx := operand{kind: opSlot, off: fn.slot(ib), width: g.intBits}
	ptr := operand{kind: opSlot, off: fn.slot(g.ptr), width: g.ptr * 8}
	fn.store(slotRef(idx), imm(0, g.intBits), ib)
	fn.writeWord(slotRef(ptr), base, g.ptr)
	loop, body, done, first := g.label(), g.label(), g.label(), g.label()
	fn.defLabel(loop)
	fn.branchIf(fn.test("lt.s", g.intBits, idx, fn.scalar(n)), body)
	fn.op("jump", 0, 0, nil, map[string]string{"L": done})
	fn.defLabel(body)
	fn.branchIf(fn.test("eq", g.intBits, idx, imm(0, g.intBits)), first)
	fn.printString(fd, " ")
	fn.defLabel(first)
	fn.printValue(fd, at(mref{kind: mPtr, ptr: ptr}, et), fmtMode)
	fn.op("add", g.intBits, 0, map[string]operand{"r": idx, "x": idx, "y": imm(1, g.intBits)}, nil)
	fn.op("add", g.ptr*8, 0, map[string]operand{"r": ptr, "x": ptr, "y": imm(uint64(es), g.ptr*8)}, nil)
	fn.op("jump", 0, 0, nil, map[string]string{"L": loop})
	fn.defLabel(done)
}

// forVariant generates body for each variant of the enum value l, for the
// variant that l has.
func (fn *fnGen) forVariant(l *loc, e *types.Enum, body func(k int)) {
	g := fn.g
	ts := g.tagSize(e)
	tt := types.Typ[types.U8]
	if ts == 2 {
		tt = types.Typ[types.U16]
	}
	tag := fn.scalar(fn.sub(tt, l, 0))
	done := g.label()
	for k := range e.Variants {
		next := g.label()
		fn.branchIf(fn.test("ne", ts*8, tag, imm(uint64(k), ts*8)), next)
		body(k)
		fn.op("jump", 0, 0, nil, map[string]string{"L": done})
		fn.defLabel(next)
	}
	fn.defLabel(done)
}

// forDynamic generates body for each dynamic type an interface value l
// may have, with the dynamic value, and with nil for a nil interface.
func (fn *fnGen) forDynamic(l *loc, body func(v *loc)) {
	g := fn.g
	tag := fn.part(l, 0)
	done := g.label()
	for k := 0; k <= len(g.typeNums); k++ {
		next := g.label()
		fn.branchIf(fn.test("ne", g.word*8, tag, imm(uint64(k), g.word*8)), next)
		if k == 0 {
			body(nil)
		} else {
			body(fn.dynValue(l, g.typeNums[k-1]))
		}
		fn.op("jump", 0, 0, nil, map[string]string{"L": done})
		fn.defLabel(next)
	}
	fn.defLabel(done)
}

// dynValue returns the dynamic value of type t of the interface value l.
func (fn *fnGen) dynValue(l *loc, t types.Type) *loc {
	g := fn.g
	if g.sizeof(t) <= 8 {
		return fn.sub(t, l, g.word)
	}
	p := fn.part(fn.sub(types.Typ[types.Usize], l, g.word), 0)
	ptr := operand{kind: opSlot, off: fn.slot(g.ptr), width: g.ptr * 8}
	fn.writeWord(slotRef(ptr), p, g.ptr)
	return at(mref{kind: mPtr, ptr: ptr}, t)
}

// ---------------------------------------------------------------------
// Panics.

// panicValue generates panic(x): the deferred calls, the message in the
// format of the IR interpreter, and the exit (comp.gen.panic).
func (fn *fnGen) panicValue(x ir.Value) {
	fn.callRuntime("PanicStart", nil)
	if mi, ok := x.(*ir.MakeInterface); ok {
		fn.panicMessage(fn.loc(mi.X))
	} else {
		fn.forDynamic(fn.loc(x), func(v *loc) {
			if v == nil {
				fn.printString(2, "nil")
				return
			}
			fn.panicMessage(v)
		})
	}
	fn.callRuntime("PanicEnd", nil)
}

func (fn *fnGen) panicMessage(v *loc) {
	g := fn.g
	if ms := g.prog.Methods(v.t); ms != nil {
		if f := ms.Methods["Error"]; f != nil && f.Sig.Params.Len() == 0 {
			s := fn.call(g.funcLabel(f), nil, []*loc{v}, resultTypes(f.Sig))
			fn.callRuntime("PrintString", []*loc{fn.intLoc(2), s})
			return
		}
	}
	fn.printValue(2, v, true)
}

// ---------------------------------------------------------------------
// Interfaces (comp.gen.layout).

// typeNum returns the number of a dynamic type of interface values.
func (g *gen) typeNum(t types.Type) int {
	for k, u := range g.typeNums {
		if types.Identical(t, u) {
			return k + 1
		}
	}
	g.typeNums = append(g.typeNums, t)
	return len(g.typeNums)
}

// scanTypes numbers the dynamic types of the program before any code is
// generated, so that every dispatch knows all of them.
func (g *gen) scanTypes() {
	for _, f := range g.prog.Funcs {
		for _, b := range f.Blocks {
			for _, in := range b.Instrs {
				switch i := in.(type) {
				case *ir.MakeInterface:
					g.typeNum(i.X.Type())
				case *ir.TypeAssert:
					if !types.IsInterface(i.Asserted) {
						g.typeNum(i.Asserted)
					}
				}
			}
		}
	}
}

func (fn *fnGen) makeInterface(i *ir.MakeInterface) {
	g := fn.g
	r := fn.result(i)
	x := fn.loc(i.X)
	fn.zeroMem(*r.at, g.sizeof(i.Type()))
	fn.writeWord(*r.at, imm(uint64(g.typeNum(i.X.Type())), g.word*8), g.word)
	size := g.sizeof(i.X.Type())
	switch {
	case size <= 8:
		fn.copyTo(r.at.plus(g.word), x, size)
	case x.at != nil && x.at.kind == mStatic:
		fn.writeWord(r.at.plus(g.word), fn.addrOf(*x.at), g.ptr)
	default:
		fn.unsupported(25, "conversion of values larger than 8 bytes to interfaces")
	}
}

// implementers returns the numbers of the dynamic types that implement
// interface it.
func (g *gen) implementers(it *types.Interface) []int {
	var nums []int
	for k, t := range g.typeNums {
		ok := true
		ms := g.prog.Methods(t)
		for _, m := range it.MethodSet() {
			if ms == nil || ms.Methods[m.Name()] == nil {
				ok = false
				break
			}
		}
		if ok {
			nums = append(nums, k+1)
		}
	}
	return nums
}

func (fn *fnGen) typeAssert(i *ir.TypeAssert) {
	g := fn.g
	x := fn.loc(i.X)
	tag := fn.part(x, 0)
	w := g.word * 8
	yes, no := g.label(), g.label()
	if it, ok := i.Asserted.Underlying().(*types.Interface); ok {
		for _, n := range g.implementers(it) {
			fn.branchIf(fn.test("eq", w, tag, imm(uint64(n), w)), yes)
		}
	} else {
		fn.branchIf(fn.test("eq", w, tag, imm(uint64(g.typeNum(i.Asserted)), w)), yes)
	}
	fn.op("jump", 0, 0, nil, map[string]string{"L": no})
	vsize := g.sizeof(i.Asserted)
	value := func() *loc {
		if types.IsInterface(i.Asserted) {
			return &loc{t: i.Asserted, parts: x.parts, zero: x.zero, at: x.at}
		}
		return fn.dynValue(x, i.Asserted)
	}
	if i.CommaOk {
		tt := i.Type().(*types.Tuple)
		r := fn.result(i)
		done := g.label()
		fn.defLabel(no)
		fn.zeroMem(*r.at, g.sizeof(tt))
		fn.op("jump", 0, 0, nil, map[string]string{"L": done})
		fn.defLabel(yes)
		fn.copyTo(*r.at, value(), vsize)
		fn.writeWord(r.at.plus(g.tupleOffset(tt, 1)), imm(1, 8), 1)
		fn.defLabel(done)
		return
	}
	fn.defLabel(no)
	fn.callRuntime("PanicStart", nil)
	fn.printString(2, "runtime error: interface conversion: interface is ")
	fn.forDynamic(x, func(v *loc) {
		if v == nil {
			fn.printString(2, "nil")
			return
		}
		fn.printString(2, v.t.String())
	})
	fn.printString(2, ", not "+i.Asserted.String())
	fn.callRuntime("PanicEnd", nil)
	fn.defLabel(yes)
	r := fn.result(i)
	fn.copyTo(*r.at, value(), vsize)
}

// invoke generates a dynamic method call.
func (fn *fnGen) invoke(i *ir.Call) {
	var args []*loc
	for _, a := range i.Args {
		args = append(args, fn.loc(a))
	}
	if r := fn.dispatch(fn.loc(i.Callee), i.Method, args, callTypes(i.Type())); r != nil {
		r.t = i.Type()
		fn.vals[i] = r
	}
}

// dispatch calls method name of the dynamic type of interface value recv
// with args, and returns the location of the results.
func (fn *fnGen) dispatch(recv *loc, name string, args []*loc, results []types.Type) *loc {
	g := fn.g
	var r *loc
	var rsize int
	if len(results) == 1 {
		rsize = g.sizeof(results[0])
		r = at(mref{kind: mFrame, off: fn.slot(rsize)}, results[0])
	} else if len(results) > 1 {
		tt := types.NewTuple(results...)
		rsize = g.sizeof(tt)
		r = at(mref{kind: mFrame, off: fn.slot(rsize)}, tt)
	}
	tag := fn.part(recv, 0)
	w := g.word * 8
	done := g.label()
	for k, t := range g.typeNums {
		ms := g.prog.Methods(t)
		if ms == nil || ms.Methods[name] == nil {
			continue
		}
		next := g.label()
		fn.branchIf(fn.test("ne", w, tag, imm(uint64(k+1), w)), next)
		res := fn.call(g.funcLabel(ms.Methods[name]), nil, append([]*loc{fn.dynValue(recv, t)}, args...), results)
		if r != nil {
			fn.copyTo(*r.at, res, rsize)
		}
		fn.op("jump", 0, 0, nil, map[string]string{"L": done})
		fn.defLabel(next)
	}
	fn.defLabel(done)
	return r
}

// ---------------------------------------------------------------------
// Equality of composite values.

func (fn *fnGen) equality(i *ir.BinOp, x, y *loc, t types.Type) {
	g := fn.g
	r := fn.result(i)
	ne, done := g.label(), g.label()
	eqv, nev := uint64(1), uint64(0)
	if i.Op.String() == "!=" {
		eqv, nev = 0, 1
	}
	fn.equal(x, y, t, ne)
	fn.writeWord(*r.at, imm(eqv, 8), 1)
	fn.op("jump", 0, 0, nil, map[string]string{"L": done})
	fn.defLabel(ne)
	fn.writeWord(*r.at, imm(nev, 8), 1)
	fn.defLabel(done)
}

// equal goes to label ne if the values x and y of type t differ.
func (fn *fnGen) equal(x, y *loc, t types.Type, ne string) {
	g := fn.g
	x = &loc{t: t, parts: x.parts, zero: x.zero, at: x.at, addr: x.addr}
	y = &loc{t: t, parts: y.parts, zero: y.zero, at: y.at, addr: y.addr}
	if bits, _, _, ok := g.intInfo(t); ok {
		fn.branchIf(fn.test("ne", bits, fn.scalar(x), fn.scalar(y)), ne)
		return
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			fn.branchIf(fn.test("ne", 8, fn.scalar(x), fn.scalar(y)), ne)
			return
		case u.Info()&types.IsString != 0:
			eq := fn.callRuntime("EqualString", []*loc{x, y})
			fn.branchIf(fn.test("eq", 8, fn.scalar(eq), imm(0, 8)), ne)
			return
		case u.Info()&types.IsFloat != 0:
			bits, _ := floatBits(t)
			eq := fn.floatRuntime(nil, "Eq", bits, types.Typ[types.Bool], x, y)
			fn.branchIf(fn.test("eq", 8, fn.scalar(eq), imm(0, 8)), ne)
			return
		}
	case *types.Struct:
		for k, f := range u.Fields {
			off := g.fieldOffset(u, k)
			fn.equal(fn.sub(f.Type(), x, off), fn.sub(f.Type(), y, off), f.Type(), ne)
		}
		return
	case *types.Array:
		es := g.sizeof(u.Elem)
		for k := 0; k < int(u.Len); k++ {
			fn.equal(fn.sub(u.Elem, x, k*es), fn.sub(u.Elem, y, k*es), u.Elem, ne)
		}
		return
	case *types.Enum:
		ts := g.tagSize(u)
		tt := types.Typ[types.U8]
		if ts == 2 {
			tt = types.Typ[types.U16]
		}
		fn.equal(fn.sub(tt, x, 0), fn.sub(tt, y, 0), tt, ne)
		fn.forVariant(x, u, func(k int) {
			for j, f := range u.Variants[k].Fields() {
				off := g.variantFieldOffset(u, k, j)
				fn.equal(fn.sub(f.Type(), x, off), fn.sub(f.Type(), y, off), f.Type(), ne)
			}
		})
		return
	case *types.Interface:
		ut := types.Typ[types.Uint]
		if g.word == 2 {
			ut = types.Typ[types.U16]
		}
		fn.equal(fn.sub(ut, x, 0), fn.sub(ut, y, 0), ut, ne)
		fn.forDynamic(x, func(v *loc) {
			if v == nil {
				return
			}
			fn.equal(v, fn.dynValue(y, v.t), v.t, ne)
		})
		return
	case *types.Signature:
		fn.branchIf(fn.test("ne", g.ptr*8, fn.scalar(x), fn.scalar(y)), ne)
		return
	}
	fn.unsupported(25, "comparison of values of type "+t.String())
}
