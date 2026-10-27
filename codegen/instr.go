package codegen

import (
	"slices"

	"an/ir"
	"an/types"
)

// instr generates the code of one IR instruction (comp.gen.lower). bi is
// the index of its block.
func (fn *fnGen) instr(in ir.Instr, bi int) {
	g := fn.g
	switch i := in.(type) {
	case *ir.Alloc:
		elem := i.Type().(*ir.Pointer).Elem
		size := g.sizeof(elem)
		m := mref{kind: mFrame, off: fn.slot(size)}
		fn.vals[i] = addrOf(m, i.Type())
		if !fn.storedNext(i) {
			fn.zeroMem(m, size)
		}
	case *ir.Load:
		r := fn.result(i)
		fn.copyTo(*r.at, at(fn.place(i.Addr), i.Type()), g.sizeof(i.Type()))
	case *ir.Store:
		fn.copyTo(fn.place(i.Addr), fn.loc(i.Val), g.sizeof(i.Val.Type()))
	case *ir.Copy:
		fn.vals[i] = fn.loc(i.X)
	case *ir.FieldAddr:
		st := i.X.Type().(*ir.Pointer).Elem.Underlying().(*types.Struct)
		fn.vals[i] = fn.offsetAddr(i, fn.loc(i.X), g.fieldOffset(st, i.Field))
	case *ir.Field:
		x := fn.loc(i.X)
		var off int
		switch u := i.X.Type().Underlying().(type) {
		case *types.Struct:
			off = g.fieldOffset(u, i.Field)
		case *types.Tuple:
			off = g.tupleOffset(u, i.Field)
		}
		fn.vals[i] = fn.sub(i.Type(), x, off)
	case *ir.Extract:
		fn.vals[i] = fn.sub(i.Type(), fn.loc(i.Tuple), g.tupleOffset(i.Tuple.Type().(*types.Tuple), i.Index))
	case *ir.IndexAddr:
		fn.indexAddr(i)
	case *ir.Index:
		fn.index(i)
	case *ir.Slice:
		fn.slice(i)
	case *ir.SubsliceAddr:
		fn.unsupported(25, "inout subslices")
	case *ir.BinOp:
		fn.binOp(i)
	case *ir.UnOp:
		fn.unOp(i)
	case *ir.Convert:
		fn.convert(i)
	case *ir.MakeInterface:
		if !fn.fmtOnly[i] {
			fn.makeInterface(i)
		}
	case *ir.TypeAssert:
		fn.typeAssert(i)
	case *ir.MakeStruct:
		r := fn.result(i)
		st := i.Type().Underlying().(*types.Struct)
		for k, f := range i.Fields {
			fn.copyTo(r.at.plus(g.fieldOffset(st, k)), fn.loc(f), g.sizeof(f.Type()))
		}
	case *ir.MakeArray:
		a, ok := i.Type().Underlying().(*types.Array)
		if !ok {
			fn.unsupported(25, "slice literals that are not constant")
			return
		}
		r := fn.result(i)
		es := g.sizeof(a.Elem)
		for k, e := range i.Elems {
			fn.copyTo(r.at.plus(k*es), fn.loc(e), es)
		}
	case *ir.MakeVariant:
		r := fn.result(i)
		e, ok := i.Type().Underlying().(*types.Enum)
		if !ok {
			fn.errorf("codegen: a variant of %s, which is not an enum", i.Type())
			return
		}
		size := g.sizeof(i.Type())
		fn.zeroMem(*r.at, size)
		fn.writeWord(*r.at, imm(uint64(i.Variant), g.tagSize(e)*8), g.tagSize(e))
		for k, f := range i.Fields {
			fn.copyTo(r.at.plus(g.variantFieldOffset(e, i.Variant, k)), fn.loc(f), g.sizeof(f.Type()))
		}
	case *ir.VariantIndex:
		e := i.X.Type().Underlying().(*types.Enum)
		tt := types.Typ[types.U8]
		if g.tagSize(e) == 2 {
			tt = types.Typ[types.U16]
		}
		tag := fn.sub(tt, fn.loc(i.X), 0)
		fn.vals[i] = fn.extend(i, tag, false)
	case *ir.VariantField:
		e := i.X.Type().Underlying().(*types.Enum)
		fn.vals[i] = fn.sub(i.Type(), fn.loc(i.X), g.variantFieldOffset(e, i.Variant, i.Field))
	case *ir.MakeClosure:
		if len(i.Bindings) == 0 {
			fn.vals[i] = g.funcValue(i.Type(), i.Fn)
			return
		}
		// A closure record in the frame: the closures of the core profile
		// do not outlive the frame that makes them (eff.source.alloc-ops).
		rec := mref{kind: mFrame}
		if slices.Contains(fn.f.Borrows, i.Fn) {
			rec = mref{kind: mPtr, ptr: fn.borrowRec}
		} else {
			rec.off = fn.slot(g.recordSize(i.Fn))
		}
		boffs := g.bindingLayout(i.Fn.FreeVars)
		code := g.funcLabel(i.Fn)
		g.addressTaken(code, sigKey(paramTypes(i.Fn), resultTypes(i.Fn.Sig)))
		fn.writeWord(rec, operand{kind: opSym, sym: code, width: g.ptr * 8}, g.ptr)
		for k, b := range i.Bindings {
			fn.copyTo(rec.plus(boffs[k]), fn.loc(b), g.sizeof(i.Fn.FreeVars[k].Typ))
		}
		fn.vals[i] = addrOf(rec, i.Type())
	case *ir.Call:
		fn.callInstr(i)
	case *ir.Asm:
		fn.asmInstr(i)
	case *ir.Jump:
		fn.jump(fn.cur.Succs[0], bi)
	case *ir.If:
		b := fn.cur
		fn.op("branch", 0, 0, map[string]operand{"x": fn.value(i.Cond)}, map[string]string{"L": fn.blocks[b.Succs[0]]})
		fn.jump(b.Succs[1], bi)
	case *ir.Return:
		fn.ret(i.Results)
	case *ir.Panic:
		fn.panicValue(i.X)
	case *ir.Defer:
		fn.deferCall(i.Call)
	case *ir.RunDefers:
		if fn.deferAt != 0 {
			fn.runDefers(operand{kind: opSlot, off: fn.deferAt, width: fn.g.ptr * 8})
		}
	case *ir.MakeSlice:
		fn.unsupported(25, "make")
	case *ir.MakeMap, *ir.Lookup, *ir.MapUpdate:
		fn.unsupported(25, "maps")
	default:
		fn.errorf("codegen: instruction %T is not supported", in)
	}
}

// storedNext reports whether the instruction after the alloc a stores a
// whole value into it, so that the cell need not be zeroed first.
func (fn *fnGen) storedNext(a *ir.Alloc) bool {
	instrs := fn.cur.Instrs
	for k, in := range instrs {
		if in == ir.Instr(a) && k+1 < len(instrs) {
			s, ok := instrs[k+1].(*ir.Store)
			return ok && s.Addr == ir.Value(a)
		}
	}
	return false
}

// jump continues at block b, which follows the current block bi if it is
// the next block.
func (fn *fnGen) jump(b *ir.Block, bi int) {
	if bi+1 < len(fn.f.Blocks) && fn.f.Blocks[bi+1] == b {
		return
	}
	fn.op("jump", 0, 0, nil, map[string]string{"L": fn.blocks[b]})
}

// sub returns the location of the part of type t at offset off of the
// value x.
func (fn *fnGen) sub(t types.Type, x *loc, off int) *loc {
	switch {
	case x.zero:
		return &loc{t: t, zero: true}
	case x.at != nil:
		return at(x.at.plus(off), t)
	case x.parts != nil:
		w := fn.g.word
		if off%w == 0 && off/w < len(x.parts) {
			return &loc{t: t, parts: x.parts[off/w:]}
		}
	}
	fn.errorf("codegen: a part of a constant at offset %d", off)
	return &loc{t: t, zero: true}
}

// offsetAddr returns the address at offset off from the address x, as
// the value of v.
func (fn *fnGen) offsetAddr(v ir.Value, x *loc, off int) *loc {
	if x.addr != nil {
		return addrOf(x.addr.plus(off), v.Type())
	}
	if off == 0 {
		return x
	}
	if x.parts != nil && x.parts[0].kind == opSym {
		p := x.parts[0]
		p.add += off
		return &loc{t: v.Type(), parts: []operand{p}}
	}
	r := fn.result(v)
	g := fn.g
	fn.op("add", g.ptr*8, 0, map[string]operand{"r": slotOperand(*r.at, g.ptr), "x": fn.scalar(x), "y": imm(uint64(off), g.ptr*8)}, nil)
	return r
}

// extend returns the integer value x zero- or sign-extended to the type
// of v.
func (fn *fnGen) extend(v ir.Value, x *loc, signed bool) *loc {
	g := fn.g
	from, to := g.sizeof(x.t), g.sizeof(v.Type())
	if from == to {
		return &loc{t: v.Type(), parts: x.parts, zero: x.zero, at: x.at, addr: x.addr}
	}
	if from > to {
		// Truncation: the low part (little-endian).
		switch {
		case x.at != nil:
			return at(*x.at, v.Type())
		case x.zero:
			return &loc{t: v.Type(), zero: true}
		}
		o := fn.scalar(x)
		if o.kind == opImm {
			return &loc{t: v.Type(), parts: fn.g.constParts(o.imm, to)}
		}
		r := fn.result(v)
		fn.copyTo(*r.at, x, to)
		return r
	}
	r := fn.result(v)
	op := "ext.u"
	if signed {
		op = "ext.s"
	}
	fn.op(op, to*8, from*8, map[string]operand{"r": slotOperand(*r.at, to), "x": fn.scalar(x)}, nil)
	return r
}

// constParts returns the parts of an integer constant of size bytes.
func (g *gen) constParts(v uint64, size int) []operand {
	if size < g.word {
		return []operand{imm(v&(1<<uint(size*8)-1), size*8)}
	}
	var parts []operand
	for i := 0; i < size/g.word; i++ {
		parts = append(parts, imm(v>>uint(i*g.word*8)&(1<<uint(g.word*8)-1), g.word*8))
	}
	return parts
}

// boundsCheck tests 0 <= i < n and calls PanicIndex otherwise
// (expr.bounds.check). i and n are ints.
func (fn *fnGen) boundsCheck(i, n *loc) {
	g := fn.g
	ok := g.label()
	t := fn.temp(1)
	bits, signed, _, _ := g.intInfo(i.t)
	switch {
	case bits < g.intBits:
		// A narrower index is extended to an int.
		i = fn.extend(&tempValue{types.Typ[types.Int]}, i, signed)
		fn.op("lt.u", g.intBits, 0, map[string]operand{"r": t, "x": fn.scalar(i), "y": fn.scalar(n)}, nil)
	case bits > g.intBits:
		// A wider index is compared with the length extended to its
		// width; the message shows the low part.
		wide := fn.extend(&tempValue{i.t}, n, false)
		fn.op("lt.u", bits, 0, map[string]operand{"r": t, "x": fn.scalar(i), "y": fn.scalar(wide)}, nil)
		i = fn.extend(&tempValue{types.Typ[types.Int]}, i, false)
	default:
		fn.op("lt.u", g.intBits, 0, map[string]operand{"r": t, "x": fn.scalar(i), "y": fn.scalar(n)}, nil)
	}
	fn.op("branch", 0, 0, map[string]operand{"x": t}, map[string]string{"L": ok})
	fn.callRuntime("PanicIndex", []*loc{i, n})
	fn.defLabel(ok)
}

// intLoc returns a location of an int constant.
func (fn *fnGen) intLoc(v int) *loc {
	return &loc{t: types.Typ[types.Int], parts: fn.g.constParts(uint64(v), fn.g.intBits/8)}
}

// toPtr returns an operand of the int value i as an offset of the
// pointer width, multiplied by scale.
func (fn *fnGen) scaled(i *loc, scale int) operand {
	g := fn.g
	bits, signed, _, _ := g.intInfo(i.t)
	var x operand
	switch {
	case bits > g.ptr*8:
		x = fn.part(i, 0) // the bounds are checked: the low part
	case bits < g.ptr*8:
		x = fn.temp(g.ptr)
		op := "ext.u"
		if signed {
			op = "ext.s"
		}
		fn.op(op, g.ptr*8, bits, map[string]operand{"r": x, "x": fn.scalar(i)}, nil)
	default:
		x = fn.scalar(i)
	}
	if scale == 1 {
		return x
	}
	if x.kind == opImm {
		return imm(x.imm*uint64(scale), g.ptr*8)
	}
	t := fn.temp(g.ptr)
	fn.op("mul", g.ptr*8, 0, map[string]operand{"r": t, "x": x, "y": imm(uint64(scale), g.ptr*8)}, nil)
	return t
}

// elemPtr computes the address base + i*scale into a slot of the
// function and returns the place it points to.
func (fn *fnGen) elemPtr(base operand, i *loc, scale int) mref {
	g := fn.g
	off := fn.scaled(i, scale)
	dst := operand{kind: opSlot, off: fn.slot(g.ptr), width: g.ptr * 8}
	fn.op("add", g.ptr*8, 0, map[string]operand{"r": dst, "x": base, "y": off}, nil)
	return mref{kind: mPtr, ptr: dst}
}

// indexAddr generates the address of an element of the array or slice at
// an address.
func (fn *fnGen) indexAddr(i *ir.IndexAddr) {
	g := fn.g
	x := fn.place(i.X)
	idx := fn.loc(i.Index)
	switch u := i.X.Type().(*ir.Pointer).Elem.Underlying().(type) {
	case *types.Array:
		es := g.sizeof(u.Elem)
		if c, ok := fn.constInt(idx); ok && c >= 0 && c < u.Len {
			fn.vals[i] = addrOf(x.plus(int(c)*es), i.Type())
			return
		}
		if !i.Proven {
			fn.boundsCheck(idx, fn.intLoc(int(u.Len)))
		}
		if x.kind == mFar {
			// A far address: the element offset is added to the offset
			// part.
			base := fn.addrOf(x)
			dst := operand{kind: opSlot, off: fn.slot(4), width: 32}
			fn.op("add", g.word*8, 0, map[string]operand{"r": slotOperand(slotRef(dst), g.word), "x": partOf(base, 0, g.word), "y": fn.scaled(idx, es)}, nil)
			fn.writeWord(slotRef(dst).plus(g.word), partOf(base, 1, g.word), g.word)
			fn.vals[i] = addrOf(mref{kind: mFar, ptr: dst}, i.Type())
			return
		}
		m := fn.elemPtr(fn.addrOf(x), idx, es)
		fn.vals[i] = addrOf(m, i.Type())
	case *types.Slice:
		es := g.sizeof(u.Elem)
		hdr := at(x, i.X.Type().(*ir.Pointer).Elem)
		n := fn.sub(types.Typ[types.Int], hdr, g.ptr)
		if !i.Proven {
			fn.boundsCheck(idx, n)
		}
		m := fn.elemPtr(fn.part(hdr, 0), idx, es)
		fn.vals[i] = addrOf(m, i.Type())
	default:
		fn.errorf("codegen: indexaddr of %s", i.X.Type())
	}
}

// constInt returns the value of an integer constant location.
func (fn *fnGen) constInt(l *loc) (int64, bool) {
	if l.zero {
		return 0, true
	}
	if l.parts == nil {
		return 0, false
	}
	var v uint64
	for k := len(l.parts) - 1; k >= 0; k-- {
		if l.parts[k].kind != opImm {
			return 0, false
		}
		v = v<<uint(fn.g.word*8) | l.parts[k].imm
	}
	size := fn.g.sizeof(l.t)
	if size < 8 {
		v &= 1<<uint(size*8) - 1
		if bits, signed, _, ok := fn.g.intInfo(l.t); ok && signed && v>>uint(bits-1) != 0 {
			return int64(v) - 1<<uint(bits), true
		}
	}
	return int64(v), true
}

// index generates an element of an array or string value.
func (fn *fnGen) index(i *ir.Index) {
	g := fn.g
	x := fn.loc(i.X)
	idx := fn.loc(i.Index)
	if isString(i.X.Type()) {
		n := fn.sub(types.Typ[types.Int], x, g.ptr)
		if !i.Proven {
			fn.boundsCheck(idx, n)
		}
		r := fn.result(i)
		ptr := fn.part(x, 0)
		if c, ok := fn.constInt(idx); ok {
			fn.writeWord(*r.at, fn.readWord(mref{kind: mPtr, ptr: ptr, off: int(c)}, 1), 1)
			return
		}
		t := fn.temp(g.ptr)
		fn.op("add", g.ptr*8, 0, map[string]operand{"r": t, "x": ptr, "y": fn.scaled(idx, 1)}, nil)
		fn.writeWord(*r.at, fn.readWord(mref{kind: mPtr, ptr: t}, 1), 1)
		return
	}
	if st, ok := i.X.Type().Underlying().(*types.Slice); ok {
		es := g.sizeof(st.Elem)
		if !i.Proven {
			fn.boundsCheck(idx, fn.sub(types.Typ[types.Int], x, g.ptr))
		}
		m := fn.elemPtr(fn.part(x, 0), idx, es)
		r := fn.result(i)
		fn.copyTo(*r.at, at(m, i.Type()), es)
		return
	}
	a := i.X.Type().Underlying().(*types.Array)
	es := g.sizeof(a.Elem)
	if c, ok := fn.constInt(idx); ok && c >= 0 && c < a.Len {
		fn.vals[i] = fn.sub(i.Type(), x, int(c)*es)
		return
	}
	if !i.Proven {
		fn.boundsCheck(idx, fn.intLoc(int(a.Len)))
	}
	if x.zero {
		fn.vals[i] = &loc{t: i.Type(), zero: true}
		return
	}
	var base operand
	if x.at != nil {
		base = fn.addrOf(*x.at)
	} else {
		fn.errorf("codegen: index of a constant array")
		return
	}
	off := fn.scaled(idx, es)
	t := fn.temp(g.ptr)
	fn.op("add", g.ptr*8, 0, map[string]operand{"r": t, "x": base, "y": off}, nil)
	r := fn.result(i)
	fn.copyTo(*r.at, at(mref{kind: mPtr, ptr: t}, i.Type()), es)
}
