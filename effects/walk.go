package effects

import (
	"an/syntax"
	"an/types"
	"strconv"
	"strings"
)

// walker collects the facts of one function body.
type walker struct {
	a       *analyzer
	fn      *fnFacts
	callees map[syntax.Expr]bool // expressions in callee position
	comms   map[syntax.Node]bool // channel operations of select cases
	commaOk map[syntax.Expr]bool // type assertions with a comma-ok result
	// locked are the peripherals whose critical section the walk is in
	// (emb.ctx.multi).
	locked map[string]bool
}

func (a *analyzer) walkBody(fn *fnFacts, body *syntax.BlockStmt) {
	w := &walker{a: a, fn: fn, callees: map[syntax.Expr]bool{}, commaOk: map[syntax.Expr]bool{}, comms: map[syntax.Node]bool{}}
	fn.copied = a.copiedSlices(fn, body)
	a.borrowedLits(fn, body)
	for _, s := range body.List {
		if _, ok := s.(*syntax.ContractStmt); ok {
			continue
		}
		syntax.Walk(s, w.visit)
	}
}

// borrowedLits records the function literals that a borrowing function
// returns: their closure records are in the frame of the caller
// (mem.ref.borrowing).
func (a *analyzer) borrowedLits(fn *fnFacts, body *syntax.BlockStmt) {
	sig := fn.sig
	if fn.outer != nil || sig.Results.Len() != 1 || !types.IsIterator(sig.Results.At(0).Type()) {
		return
	}
	inout := sig.Recv != nil && sig.Recv.Inout()
	for i := range sig.Params.Len() {
		inout = inout || sig.Params.At(i).Inout()
	}
	if !inout {
		return
	}
	syntax.Walk(body, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.FuncLit:
			return false
		case *syntax.ReturnStmt:
			if len(n.Results) == 1 {
				if lit, ok := unparen(n.Results[0]).(*syntax.FuncLit); ok {
					a.borrowed[lit] = true
				}
			}
		}
		return true
	})
}

// sliceRoot returns the name at the root of a chain of slice expressions.
func sliceRoot(e syntax.Expr) *syntax.Name {
	switch x := unparen(e).(type) {
	case *syntax.Name:
		return x
	case *syntax.SliceExpr:
		return sliceRoot(x.X)
	}
	return nil
}

// copiedSlices returns the inout slice parameters of fn that body uses
// other than in the forms that copy no value: the operand of an index or
// slice expression that is so used, of len, cap, copy or clear, of a
// comparison with nil or of a range without a value variable, an inout
// argument or receiver, and the target of an assignment. A use in a
// function literal captures a copy (mem.inout.unique).
func (a *analyzer) copiedSlices(fn *fnFacts, body *syntax.BlockStmt) map[*types.Var]bool {
	param := func(id *syntax.Name) *types.Var {
		v, ok := a.info.Uses[id].(*types.Var)
		if !ok || !v.Inout() {
			return nil
		}
		if _, own := fn.params[v]; !own {
			return nil
		}
		if _, isSlice := coreOf(v.Type()).(*types.Slice); !isSlice {
			return nil
		}
		return v
	}
	keep := map[*syntax.Name]bool{}
	var chain func(e syntax.Expr)
	chain = func(e syntax.Expr) {
		switch x := unparen(e).(type) {
		case *syntax.Name:
			keep[x] = true
		case *syntax.SliceExpr:
			chain(x.X)
		}
	}
	isNil := func(e syntax.Expr) bool {
		id, ok := unparen(e).(*syntax.Name)
		if !ok {
			return false
		}
		_, ok = a.info.Uses[id].(*types.Nil)
		return ok
	}
	skip := func(n syntax.Node) bool {
		if _, ok := n.(*syntax.ContractStmt); ok {
			return true // contracts are not executed
		}
		c, ok := n.(*syntax.CallExpr)
		return ok && a.builtinName(c.Fun) == "assert"
	}
	syntax.Walk(body, func(n syntax.Node) bool {
		if skip(n) {
			return false
		}
		switch n := n.(type) {
		case *syntax.FuncLit:
			return false
		case *syntax.IndexExpr:
			chain(n.X)
		case *syntax.AssignStmt:
			if n.Tok == syntax.Assign {
				for i, e := range n.Lhs {
					id, ok := unparen(e).(*syntax.Name)
					if !ok {
						continue
					}
					keep[id] = true
					// xs = xs[lo:hi] replaces the only copy.
					if v := param(id); v != nil && len(n.Lhs) == len(n.Rhs) {
						if r := sliceRoot(n.Rhs[i]); r != nil && a.info.Uses[r] == v {
							chain(n.Rhs[i])
						}
					}
				}
			}
		case *syntax.RangeStmt:
			if n.Value == nil {
				chain(n.X)
			}
		case *syntax.BinaryExpr:
			if n.Op == syntax.Eql || n.Op == syntax.Neq {
				if isNil(n.X) {
					chain(n.Y)
				}
				if isNil(n.Y) {
					chain(n.X)
				}
			}
		case *syntax.CallExpr:
			switch a.builtinName(n.Fun) {
			case "len", "cap", "copy", "clear":
				for _, e := range n.Args {
					chain(e)
				}
				return true
			}
			sig, _ := coreOf(a.typeOf(n.Fun)).(*types.Signature)
			if sig == nil {
				return true
			}
			for i, e := range n.Args {
				if i < sig.Params.Len() && sig.Params.At(i).Inout() {
					chain(e)
				}
			}
			if sel, ok := unparen(n.Fun).(*syntax.SelectorExpr); ok && sig.Recv == nil {
				if s := a.info.Selections[sel]; s != nil {
					if m, ok := s.Obj.(*types.Func); ok && m.Signature().Recv != nil && m.Signature().Recv.Inout() {
						chain(sel.X)
					}
				}
			}
		}
		return true
	})
	copied := map[*types.Var]bool{}
	syntax.Walk(body, func(n syntax.Node) bool {
		if skip(n) {
			return false
		}
		if id, ok := n.(*syntax.Name); ok && !keep[id] {
			if v := param(id); v != nil {
				copied[v] = true
			}
		}
		return true
	})
	return copied
}

// AsmEffects returns the names of the effects of the instructions of an
// asm statement for an architecture, from its description
// (stmt.asm.effects). Package arch/desc sets it.
var AsmEffects func(arch, text string) []string

func (w *walker) visit(n syntax.Node) bool {
	a, fn := w.a, w.fn
	switch n := n.(type) {
	case *syntax.ContractStmt:
		return false // the invariant of a loop has no effects
	case *syntax.FuncLit:
		w.funcLit(n)
		return false

	case *syntax.Name:
		switch obj := a.info.Uses[n].(type) {
		case *types.Var:
			if isPackageVar(obj) {
				fn.source(Global, n.Pos(), "use of package-level variable "+n.Name)
				w.device(obj, n.Pos())
			}
		case *types.Func:
			if !w.callees[n] {
				w.funcValue(n, funcKey(obj), obj.Type())
			}
		}

	case *syntax.SelectorExpr:
		w.selector(n)

	case *syntax.AsmStmt:
		// The effects of the instructions (stmt.asm.effects).
		base := n.Pos().File
		base = base[strings.LastIndexAny(base, "/\\")+1:]
		arch := strings.TrimSuffix(base[strings.LastIndex(base, "_")+1:], ".an")
		text, _ := strconv.Unquote(n.Text.Value)
		if AsmEffects != nil {
			for _, name := range AsmEffects(arch, text) {
				fn.source(Base(name), n.Pos(), "asm statement")
			}
		}
		for _, x := range n.Lhs {
			syntax.Walk(x, w.visit)
		}
		if r := n.Regs(); r != nil {
			syntax.Walk(r, w.visit)
		}
		return false

	case *syntax.CallExpr:
		if a.builtinName(n.Fun) == "assert" {
			// A proof obligation, not evaluated (contract.assert.meaning).
			return false
		}
		w.callees[unparen(n.Fun)] = true
		if ix, ok := unparen(n.Fun).(*syntax.IndexExpr); ok {
			w.callees[unparen(ix.X)] = true // an explicit instantiation
		}
		w.call(n)

	case *syntax.BinaryExpr:
		w.binary(n)

	case *syntax.UnaryExpr:
		switch n.Op {
		case syntax.Sub:
			if !a.isConst(n) && checkedOrGeneric(a.typeOf(n)) {
				fn.source(Panic, n.OpPos, "integer negation")
			}
		case syntax.Arrow:
			if !w.comms[n] {
				fn.source(Block, n.OpPos, "channel receive")
			}
		}

	case *syntax.IndexExpr:
		w.index(n)

	case *syntax.SliceExpr:
		w.slice(n)

	case *syntax.TypeAssertExpr:
		if n.Type == nil {
			break // x.(type) of a type switch
		}
		if !w.commaOk[n] {
			fn.source(Panic, n.Lparen, "type assertion")
		}
		if t := a.typeOf(n.Type); t != nil && types.IsInterface(t) {
			w.flow(a.typeOf(n.X), t)
		}

	case *syntax.CompositeLit:
		w.compositeLit(n)

	case *syntax.AssignStmt:
		w.assign(n)

	case *syntax.IncDecStmt:
		w.opAssign(n.X, syntax.Add, nil, n.TokPos)

	case *syntax.DeclStmt:
		for _, spec := range n.Decl.Specs {
			vs, ok := spec.(*syntax.ValueSpec)
			if !ok {
				continue
			}
			if len(vs.Names) == 2 && len(vs.Values) == 1 {
				w.commaOk[unparen(vs.Values[0])] = true
			}
			if vs.Type != nil && len(vs.Values) == len(vs.Names) {
				for _, v := range vs.Values {
					w.conv(v, a.typeOf(vs.Type))
				}
			}
		}

	case *syntax.ReturnStmt:
		res := fn.sig.Results
		if len(n.Results) == res.Len() {
			for i, e := range n.Results {
				w.conv(e, res.At(i).Type())
			}
		}

	case *syntax.SendStmt:
		if !w.comms[n] {
			fn.source(Block, n.Arrow, "channel send")
		}
		fn.source(Panic, n.Arrow, "channel send")
		if ch, ok := coreOf(a.typeOf(n.Chan)).(*types.Chan); ok {
			w.conv(n.Value, ch.Elem)
		}

	case *syntax.GoStmt:
		fn.source(Tasks, n.Go, "go statement")

	case *syntax.SelectStmt:
		// The channel operations of the cases wait only as part of the
		// select statement (eff.source.wait).
		hasDefault := false
		for _, s := range n.Body.List {
			cc, ok := s.(*syntax.CommClause)
			if !ok {
				continue
			}
			switch c := cc.Comm.(type) {
			case nil:
				hasDefault = true
			case *syntax.SendStmt:
				w.comms[c] = true
			case *syntax.ExprStmt:
				w.comms[unparen(c.X)] = true
			case *syntax.AssignStmt:
				for _, e := range c.Rhs {
					w.comms[unparen(e)] = true
				}
			}
		}
		if !hasDefault {
			fn.source(Block, n.Select, "select statement")
		}

	case *syntax.RangeStmt:
		if n.Const {
			// The unrolled instances of the body (ct.for.unroll).
			for _, u := range a.info.Unrolled[n] {
				syntax.Walk(u.Body, w.visit)
			}
			return false
		}
		if _, ok := coreOf(a.typeOf(n.X)).(*types.Chan); ok {
			fn.source(Block, n.Range, "range over a channel")
		}
		if sel, ok := n.X.(*syntax.SelectorExpr); ok && a.info.Selections[sel] != nil && a.info.Selections[sel].Kind == types.MethodVal {
			// A method value ranged over is called, not kept: it binds its
			// receiver without allocating.
			w.callees[sel] = true
		}
		if unit := w.lockUnit(n.X); unit != "" {
			// The body is a critical section of the peripheral, which Lock
			// enters.
			saved := w.locked[unit]
			if w.locked == nil {
				w.locked = map[string]bool{}
			}
			w.locked[unit] = true
			syntax.Walk(n.X, w.visit)
			syntax.Walk(n.Body, w.visit)
			w.locked[unit] = saved
			return false
		}
		if n.Tok == syntax.Assign {
			for _, e := range []syntax.Expr{n.Key, n.Value} {
				if e != nil {
					w.write(e, n.TokPos)
				}
			}
		}

	case *syntax.TypeSwitchStmt:
		if idx, ok := a.info.StaticSwitch[n]; ok {
			// Only the chosen clause (ct.typeswitch.static).
			if idx >= 0 {
				for _, s := range n.Body.List[idx].(*syntax.CaseClause).Body {
					syntax.Walk(s, w.visit)
				}
			}
			return false
		}

	case *syntax.SwitchStmt:
		if n.Tag != nil && types.IsInterface(a.typeOf(n.Tag)) {
			for _, s := range n.Body.List {
				if cc, ok := s.(*syntax.CaseClause); ok {
					for _, e := range cc.List {
						w.conv(e, a.typeOf(n.Tag))
					}
				}
			}
		}
	}
	return true
}

// isPackageVar reports whether v is a package-level variable
// (eff.source.global).
// DeviceClasses are the hardware effect classes of the groups of
// peripherals (eff.base.set, group Hardware).
var DeviceClasses = map[string]string{"GPIO": "Gpio", "AFIO": "Gpio", "USART": "Uart", "UART": "Uart", "TIM": "Timers"}

// UnitEffect returns the instance effect of a peripheral of an/device
// (emb.periph.effect), or of its critical section if locked.
func UnitEffect(name string, locked bool) Effect {
	if locked {
		return Effect("an/device." + name + ".Lock#" + name + ".Lock")
	}
	return Effect("an/device." + name + "#" + name)
}

// device adds the effects of a use of a package-level variable that is a
// peripheral of an/device: its instance effect and the effect of its
// class.
func (w *walker) device(v *types.Var, pos syntax.Pos) {
	if v.Map == nil || v.Pkg() == nil || v.Pkg().Path != "an/device" {
		return
	}
	w.fn.source(UnitEffect(v.Name(), w.locked[v.Name()]), pos, "use of peripheral "+v.Name())
	if n, ok := v.Type().(*types.Named); ok {
		if class := DeviceClasses[strings.TrimSuffix(n.Obj.Name(), "_Regs")]; class != "" {
			w.fn.source(Base(class), pos, "use of peripheral "+v.Name())
		}
	}
}

// lockUnit returns the peripheral of a range expression device.U.Lock,
// whose body is a critical section of U (emb.lock.form).
func (w *walker) lockUnit(x syntax.Expr) string {
	sel, ok := x.(*syntax.SelectorExpr)
	if !ok || sel.Sel.Name != "Lock" {
		return ""
	}
	var id *syntax.Name
	switch e := sel.X.(type) {
	case *syntax.SelectorExpr:
		id = e.Sel
	case *syntax.Name:
		id = e
	}
	if id == nil {
		return ""
	}
	if v, ok := w.a.info.Uses[id].(*types.Var); ok && v.Map != nil && v.Pkg() != nil && v.Pkg().Path == "an/device" {
		return v.Name()
	}
	return ""
}

func isPackageVar(v *types.Var) bool {
	p := v.Parent()
	return p != nil && (p.Parent() == types.Universe || p.Parent() == nil) && !v.IsField()
}

// checkedOrGeneric reports whether arithmetic on type t has overflow
// checks: a checked integer type, or a type parameter.
func checkedOrGeneric(t types.Type) bool {
	if t == nil {
		return false
	}
	if types.IsTypeParam(t) {
		return true
	}
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsInteger != 0 && b.Info()&(types.IsWrapping|types.IsUntyped) == 0
}

func isInteger(t types.Type) bool {
	if t == nil {
		return false
	}
	if types.IsTypeParam(t) {
		return true
	}
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsInteger != 0
}

// hasInterface reports whether comparing or hashing values of type t may
// panic: t is or contains an interface type.
func hasInterface(t types.Type) bool {
	return containsIface(t, map[types.Type]bool{})
}

func containsIface(t types.Type, seen map[types.Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch u := t.Underlying().(type) {
	case *types.Interface:
		return true
	case *types.Struct:
		for _, f := range u.Fields {
			if containsIface(f.Type(), seen) {
				return true
			}
		}
	case *types.Array:
		return containsIface(u.Elem, seen)
	}
	return types.IsTypeParam(t)
}

func (w *walker) binary(n *syntax.BinaryExpr) {
	a, fn := w.a, w.fn
	if a.isConst(n) {
		return
	}
	xt := a.typeOf(n.X)
	switch n.Op {
	case syntax.Add:
		if b, ok := coreOf(xt).(*types.Basic); ok && b.Info()&types.IsString != 0 {
			fn.source(Alloc, n.OpPos, "string concatenation")
			return
		}
		fallthrough
	case syntax.Sub, syntax.Mul:
		if checkedOrGeneric(xt) {
			fn.source(Panic, n.OpPos, "integer "+opWord(n.Op))
		}
	case syntax.Quo, syntax.Rem:
		if isInteger(xt) && !w.safeDivisor(n.Y, xt) {
			fn.source(Panic, n.OpPos, "integer division")
		}
	case syntax.Shl, syntax.Shr:
		if yt := a.typeOf(n.Y); !a.isConst(n.Y) && yt != nil && isInteger(yt) && !isUnsigned(yt) {
			fn.source(Panic, n.OpPos, "shift by a signed count")
		}
	case syntax.Eql, syntax.Neq:
		yt := a.typeOf(n.Y)
		if !isNil(a, n.X) && !isNil(a, n.Y) && (hasInterface(xt) || hasInterface(yt)) {
			fn.source(Panic, n.OpPos, "comparison of interface values")
		}
		if xt != nil && yt != nil {
			switch {
			case types.IsInterface(xt) && !types.IsInterface(yt):
				w.conv(n.Y, xt)
			case types.IsInterface(yt) && !types.IsInterface(xt):
				w.conv(n.X, yt)
			}
		}
	}
}

func opWord(op syntax.Kind) string {
	switch op {
	case syntax.Add:
		return "addition"
	case syntax.Sub:
		return "subtraction"
	}
	return "multiplication"
}

func isUnsigned(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsUnsigned != 0
}

func isNil(a *analyzer, e syntax.Expr) bool {
	if id, ok := unparen(e).(*syntax.Name); ok {
		if _, ok := a.info.Uses[id].(*types.Nil); ok {
			return true
		}
	}
	t, ok := a.typeOf(e).(*types.Basic)
	return ok && t.Kind() == types.UntypedNil
}

// safeDivisor reports whether division by y cannot panic: y is a
// constant other than 0 and, for a checked signed type, other than -1
// (eff.source.panic-ops).
func (w *walker) safeDivisor(y syntax.Expr, t types.Type) bool {
	v := w.a.info.Types[y].Value
	if v == nil || types.IsTypeParam(t) {
		return false
	}
	s := v.String()
	if s == "0" {
		return false
	}
	return s != "-1" || isUnsigned(t) || !checkedOrGeneric(t)
}

func (w *walker) index(n *syntax.IndexExpr) {
	a, fn := w.a, w.fn
	xt := a.typeOf(n.X)
	if xt == nil || len(n.Indices) != 1 {
		return
	}
	if tv := a.info.Types[n.X]; tv.IsType() {
		return
	}
	if _, ok := xt.(*types.Signature); ok {
		return // an instantiation
	}
	switch u := coreOf(xt).(type) {
	case *types.Map:
		if hasInterface(u.Key) {
			fn.source(Panic, n.Lbrack, "map index with an interface key")
		}
		w.conv(n.Indices[0], u.Key)
	case *types.Array:
		if !a.isConst(n.Indices[0]) {
			fn.source(Panic, n.Lbrack, "index")
		}
	default:
		fn.source(Panic, n.Lbrack, "index")
	}
}

func (w *walker) slice(n *syntax.SliceExpr) {
	a := w.a
	idx := []syntax.Expr{n.Low, n.High, n.Max}
	_, isArray := coreOf(a.typeOf(n.X)).(*types.Array)
	for _, e := range idx {
		if e != nil && (!isArray || !a.isConst(e)) {
			w.fn.source(Panic, n.Lbrack, "slice expression")
			return
		}
	}
}

// write handles e as the target of an assignment: a write into a map
// may grow it, a write into the elements of a slice that is not unique
// may copy a shared buffer (eff.source.alloc-ops, mem.inout.unique).
func (w *walker) write(e syntax.Expr, pos syntax.Pos) {
	a, fn := w.a, w.fn
	for {
		switch x := unparen(e).(type) {
		case *syntax.IndexExpr:
			switch u := coreOf(a.typeOf(x.X)).(type) {
			case *types.Map:
				fn.source(Alloc, pos, "map assignment")
				_ = u
				return
			case *types.Slice:
				if !w.unique(x.X) {
					fn.source(Alloc, pos, "write to a slice element")
				}
				return
			}
			e = x.X
		case *syntax.SelectorExpr:
			e = x.X
		default:
			return
		}
	}
}

// unique reports whether the slice operand e is unique: its buffer is
// shared with no other copy, so that writing into its elements or passing
// it to an inout parameter copies nothing (mem.inout.unique).
func (w *walker) unique(e syntax.Expr) bool {
	a := w.a
	switch x := unparen(e).(type) {
	case *syntax.SliceExpr:
		if _, ok := coreOf(a.typeOf(x.X)).(*types.Array); ok {
			return true
		}
		return w.unique(x.X)
	case *syntax.Name:
		switch obj := a.info.Uses[x].(type) {
		case *types.Nil:
			return true
		case *types.Var:
			_, own := w.fn.params[obj]
			return own && obj.Inout() && !w.fn.copied[obj]
		}
	}
	return false
}

// inoutSliceParam reports whether e is an inout parameter or receiver of
// a slice type of the function.
func (w *walker) inoutSliceParam(e syntax.Expr) bool {
	id, ok := unparen(e).(*syntax.Name)
	if !ok {
		return false
	}
	v, ok := w.a.info.Uses[id].(*types.Var)
	if !ok || !v.Inout() {
		return false
	}
	if _, own := w.fn.params[v]; !own {
		return false
	}
	_, isSlice := coreOf(v.Type()).(*types.Slice)
	return isSlice
}

// inoutArg handles e passed to an inout parameter: a slice that is not
// unique is made unique first, which may copy its buffer
// (mem.inout.unique).
func (w *walker) inoutArg(e syntax.Expr, pos syntax.Pos) {
	if _, ok := coreOf(w.a.typeOf(e)).(*types.Slice); ok && !w.unique(e) {
		w.fn.source(Alloc, pos, "slice passed to an inout parameter, which may copy a shared buffer")
	}
}

func (w *walker) assign(n *syntax.AssignStmt) {
	a := w.a
	switch n.Tok {
	case syntax.Assign, syntax.Define:
		if len(n.Lhs) == 2 && len(n.Rhs) == 1 {
			w.commaOk[unparen(n.Rhs[0])] = true
		}
		if n.Tok == syntax.Assign {
			for i, e := range n.Lhs {
				w.write(e, n.TokPos)
				// A subslice passed to an inout slice parameter keeps
				// its length (mem.inout.subslice).
				if w.inoutSliceParam(e) {
					w.fn.source(Panic, n.TokPos, "assignment to an inout slice parameter, which panics if it changes the length of a subslice")
				}
				// An inout slice parameter stays unique
				// (mem.inout.unique).
				if w.unique(e) {
					if id, ok := unparen(e).(*syntax.Name); ok && id.Name != "_" && (len(n.Lhs) != len(n.Rhs) || !w.unique(n.Rhs[i])) {
						if _, ok := coreOf(a.typeOf(e)).(*types.Slice); ok {
							w.fn.source(Alloc, n.TokPos, "assignment to an inout slice parameter, which may copy a shared buffer")
						}
					}
				}
			}
		}
		if len(n.Lhs) == len(n.Rhs) {
			for i, e := range n.Rhs {
				if id, ok := unparen(n.Lhs[i]).(*syntax.Name); ok && id.Name == "_" {
					continue
				}
				w.conv(e, a.typeOf(n.Lhs[i]))
			}
		}
	default:
		w.opAssign(n.Lhs[0], assignOp(n.Tok), n.Rhs[0], n.TokPos)
	}
}

// opAssign handles x op= y and, with y nil, x++ and x--.
func (w *walker) opAssign(x syntax.Expr, op syntax.Kind, y syntax.Expr, pos syntax.Pos) {
	a, fn := w.a, w.fn
	w.write(x, pos)
	t := a.typeOf(x)
	switch op {
	case syntax.Add:
		if b, ok := coreOf(t).(*types.Basic); ok && b.Info()&types.IsString != 0 {
			fn.source(Alloc, pos, "string concatenation")
			return
		}
		fallthrough
	case syntax.Sub, syntax.Mul:
		if checkedOrGeneric(t) {
			fn.source(Panic, pos, "integer "+opWord(op))
		}
	case syntax.Quo, syntax.Rem:
		if isInteger(t) && !w.safeDivisor(y, t) {
			fn.source(Panic, pos, "integer division")
		}
	case syntax.Shl, syntax.Shr:
		if yt := a.typeOf(y); !a.isConst(y) && yt != nil && isInteger(yt) && !isUnsigned(yt) {
			fn.source(Panic, pos, "shift by a signed count")
		}
	}
}

func assignOp(tok syntax.Kind) syntax.Kind {
	switch tok {
	case syntax.AddAssign:
		return syntax.Add
	case syntax.SubAssign:
		return syntax.Sub
	case syntax.MulAssign:
		return syntax.Mul
	case syntax.QuoAssign:
		return syntax.Quo
	case syntax.RemAssign:
		return syntax.Rem
	case syntax.ShlAssign:
		return syntax.Shl
	case syntax.ShrAssign:
		return syntax.Shr
	}
	return syntax.Illegal
}

func (w *walker) compositeLit(n *syntax.CompositeLit) {
	a, fn := w.a, w.fn
	t := a.typeOf(n)
	if t == nil {
		return
	}
	switch u := coreOf(t).(type) {
	case *types.Slice:
		if len(n.Elts) > 0 {
			fn.source(Alloc, n.Lbrace, "slice literal")
		}
		for _, e := range n.Elts {
			if kv, ok := e.(*syntax.KeyValueExpr); ok {
				e = kv.Value
			}
			w.conv(e, u.Elem)
		}
	case *types.Array:
		for _, e := range n.Elts {
			if kv, ok := e.(*syntax.KeyValueExpr); ok {
				e = kv.Value
			}
			w.conv(e, u.Elem)
		}
	case *types.Map:
		if len(n.Elts) > 0 {
			fn.source(Alloc, n.Lbrace, "map literal")
		}
		for _, e := range n.Elts {
			if kv, ok := e.(*syntax.KeyValueExpr); ok {
				w.conv(kv.Key, u.Key)
				w.conv(kv.Value, u.Elem)
				if hasInterface(u.Key) {
					fn.source(Panic, n.Lbrace, "map literal with interface keys")
				}
			}
		}
	case *types.Struct:
		for i, e := range n.Elts {
			if kv, ok := e.(*syntax.KeyValueExpr); ok {
				if id, ok := kv.Key.(*syntax.Name); ok {
					for _, f := range u.Fields {
						if f.Name() == id.Name {
							w.conv(kv.Value, f.Type())
						}
					}
				}
				continue
			}
			if i < len(u.Fields) {
				w.conv(e, u.Fields[i].Type())
			}
		}
	}
}

// conv handles the use of e where a value of type target is expected:
// a conversion of a concrete value to an interface type adds the
// summaries of its methods and records them as implementations
// (eff.infer.rules 6).
func (w *walker) conv(e syntax.Expr, target types.Type) {
	a, fn := w.a, w.fn
	src := a.typeOf(e)
	if src == nil || target == nil || isNil(a, e) {
		return
	}
	switch {
	case types.IsInterface(target):
		switch {
		case types.IsInterface(src):
			w.flow(src, target)
		case types.IsTypeParam(src):
			if owner := fn.tparamOwner(src.(*types.TypeParam)); owner == fn {
				fn.sum.Converts = appendConvert(fn.sum.Converts, Convert{Index: fn.tparams[src.(*types.TypeParam)], Iface: typeKey(target), Methods: methods(target)})
			}
			fn.source(Alloc, e.Pos(), "conversion to an interface")
		default:
			for _, m := range methods(target) {
				if key := a.methodKey(src, m); key != "" {
					fn.adds = append(fn.adds, addFact{key, origin(e.Pos(), "conversion to "+target.String())})
					a.impl(Ref{Kind: OpenMethod, Method: m, Type: typeKey(target)}, key)
				}
			}
			if !a.isConst(e) && !smallType(src) {
				fn.source(Alloc, e.Pos(), "conversion to an interface")
			}
		}
	case isSignature(target) && isSignature(src) && sigKey(src) != sigKey(target):
		a.flowRef(Ref{Kind: OpenFunc, Type: sigKey(target)}, Ref{Kind: OpenFunc, Type: sigKey(src)})
	}
}

func appendConvert(cs []Convert, c Convert) []Convert {
	for _, x := range cs {
		if x.Index == c.Index && x.Iface == c.Iface {
			return cs
		}
	}
	return append(cs, c)
}

func isSignature(t types.Type) bool {
	_, ok := t.Underlying().(*types.Signature)
	return ok
}

// smallType reports whether a value of type t fits in an interface value
// without allocation: a boolean or numeric type, a struct type without
// fields, or an enum type whose variants carry no data
// (eff.source.alloc-ops).
func smallType(t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		return u.Info()&(types.IsBoolean|types.IsInteger|types.IsFloat) != 0
	case *types.Struct:
		return len(u.Fields) == 0
	case *types.Enum:
		for _, v := range u.Variants {
			if v.HasData() {
				return false
			}
		}
		return true
	}
	return false
}

// flow records that values of interface type src flow into interface
// type dst: the implementations of the methods of src implement those
// of dst.
func (w *walker) flow(src, dst types.Type) {
	if src == nil || dst == nil || !types.IsInterface(src) || typeKey(src) == typeKey(dst) {
		return
	}
	for _, m := range methods(dst) {
		w.a.flowRef(Ref{Kind: OpenMethod, Method: m, Type: typeKey(dst)}, Ref{Kind: OpenMethod, Method: m, Type: typeKey(src)})
	}
}

func (a *analyzer) flowRef(dst, src Ref) {
	if a.pkg.Flows[dst] == nil {
		a.pkg.Flows[dst] = map[Ref]bool{}
	}
	a.pkg.Flows[dst][src] = true
}

func (a *analyzer) impl(r Ref, key string) {
	if a.pkg.Impls[r] == nil {
		a.pkg.Impls[r] = map[string]bool{}
	}
	a.pkg.Impls[r][key] = true
}

// funcValue handles the creation of a function value from the function
// key (eff.infer.rules 7).
func (w *walker) funcValue(e syntax.Expr, key string, t types.Type) {
	w.fn.adds = append(w.fn.adds, addFact{key, origin(e.Pos(), "function value")})
	w.a.impl(Ref{Kind: OpenFunc, Type: sigKey(t)}, key)
}

func (w *walker) selector(n *syntax.SelectorExpr) {
	a, fn := w.a, w.fn
	sel := a.info.Selections[n]
	if sel == nil {
		// A qualified identifier.
		switch obj := a.info.Uses[n.Sel].(type) {
		case *types.Var:
			if isPackageVar(obj) {
				fn.source(Global, n.Sel.Pos(), "use of package-level variable "+n.Sel.Name)
				w.device(obj, n.Sel.Pos())
			}
		case *types.Func:
			if !w.callees[n] {
				w.funcValue(n, funcKey(obj), obj.Type())
			}
		}
		return
	}
	if w.callees[n] {
		return
	}
	m, ok := sel.Obj.(*types.Func)
	if !ok {
		return
	}
	switch sel.Kind {
	case types.MethodVal:
		// A method value binds its receiver (eff.source.alloc-ops).
		fn.source(Alloc, n.Sel.Pos(), "method value")
		recv := sel.Recv
		switch {
		case types.IsTypeParam(recv):
			fn.prefs = append(fn.prefs, prefFact{kind: TypeParamMethod, tp: recv.(*types.TypeParam), method: m.Name(), origin: origin(n.Pos(), "method value")})
		case types.IsInterface(recv):
			r := Ref{Kind: OpenMethod, Method: m.Name(), Type: typeKey(recv)}
			fn.open = append(fn.open, refFact{r, origin(n.Pos(), "method value")})
			a.flowRef(Ref{Kind: OpenFunc, Type: sigKey(a.typeOf(n))}, r)
		default:
			w.funcValue(n, funcKey(m), a.typeOf(n))
		}
	case types.MethodExpr:
		if t := a.typeOf(n); t != nil {
			w.funcValue(n, funcKey(m), t)
		}
	}
}

// funcLit analyzes a function literal as a function of its own, whose
// summary is added to the enclosing function (eff.infer.rules 7).
func (w *walker) funcLit(n *syntax.FuncLit) {
	a, fn := w.a, w.fn
	sig, ok := a.typeOf(n).(*types.Signature)
	if !ok {
		return
	}
	fn.nlit++
	key := fn.key + "$lit" + itoa(fn.nlit)
	lit := a.newFn(key, sig, fn)
	lit.sum.Pos = n.Pos()
	a.litKeys[n] = key
	fn.lits = append(fn.lits, lit)
	a.impl(Ref{Kind: OpenFunc, Type: sigKey(sig)}, key)
	if a.captures(n) && !a.borrowed[n] {
		fn.source(Alloc, n.Pos(), "function literal with captured variables")
	}
	a.walkBody(lit, n.Body)
}

// captures reports whether the literal uses local variables of enclosing
// functions.
func (a *analyzer) captures(n *syntax.FuncLit) bool {
	local := map[*types.Var]bool{}
	found := false
	syntax.Walk(n, func(m syntax.Node) bool {
		if f, ok := m.(*syntax.FuncLit); ok {
			for _, fl := range [][]*syntax.Field{f.Type.Params.List, resultsOf(f.Type)} {
				for _, fd := range fl {
					for _, id := range fd.Names {
						if v, ok := a.info.Defs[id].(*types.Var); ok {
							local[v] = true
						}
					}
				}
			}
		}
		if id, ok := m.(*syntax.Name); ok {
			if v, ok := a.info.Defs[id].(*types.Var); ok {
				local[v] = true
			}
		}
		return true
	})
	syntax.Walk(n, func(m syntax.Node) bool {
		if id, ok := m.(*syntax.Name); ok {
			if v, ok := a.info.Uses[id].(*types.Var); ok && !local[v] && !isPackageVar(v) && !v.IsField() {
				found = true
			}
		}
		return !found
	})
	return found
}

func resultsOf(t *syntax.FuncType) []*syntax.Field {
	if t.Results == nil {
		return nil
	}
	return t.Results.List
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
