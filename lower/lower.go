// Package lower translates checked packages to IR (spec/22-compiler.md
// §22.3, step 5).
//
// Generic functions and methods of generic types are instantiated per
// type argument list. Constructs that the IR does not support yet are
// reported with the roadmap step that adds them.
package lower

import (
	"fmt"
	"go/constant"
	"strings"

	"an/ir"
	"an/syntax"
	"an/types"
)

// builder translates a program.
type builder struct {
	prog    *ir.Program
	infos   []*types.Info
	declOf  map[*syntax.FuncDecl]*types.Info // the package of a declaration
	origins map[*syntax.FuncDecl]*types.Func // declared functions and methods by declaration
	funcs   map[string]*ir.Func              // instances by key
	ctors   map[string]*ir.Func              // variant constructors by key
	queue   []func()
	errors  []*syntax.Error
	nlits   int
	globals map[*types.Var]*ir.Global
	ready   func(*types.Const) bool // for Constant: whether a computed constant is known
	missing []*types.Const          // computed constants used before they are known
}

// Program translates the checked packages of a program to IR: the
// packages it imports, in dependency order, and package main last.
func Program(infos ...*types.Info) (*ir.Program, []*syntax.Error) {
	b := newBuilder(infos)
	main := infos[len(infos)-1]
	if main.Main == nil {
		return nil, []*syntax.Error{{Msg: "package main has no func main"}}
	}
	b.prog.Main = b.function(main.Main, nil)
	b.handlers(infos)
	return b.run()
}

// handlers translates the interrupt handlers of the packages.
func (b *builder) handlers(infos []*types.Info) {
	for _, info := range infos {
		for _, h := range info.Handlers {
			fn := b.function(h, nil)
			fn.Handler = h.Handler
			b.prog.Handlers = append(b.prog.Handlers, fn)
		}
	}
}

// ProgramWith translates the program as Program does, and the functions
// extra too, which generated code calls without the program calling
// them: the functions of the runtime package (comp.gen.runtime).
func ProgramWith(extra []*types.Func, infos ...*types.Info) (*ir.Program, []*syntax.Error) {
	b := newBuilder(infos)
	main := infos[len(infos)-1]
	if main.Main == nil {
		return nil, []*syntax.Error{{Msg: "package main has no func main"}}
	}
	b.prog.Main = b.function(main.Main, nil)
	for _, f := range extra {
		b.function(f, nil)
	}
	b.handlers(infos)
	return b.run()
}

// Constant translates the initializer of the computed constant k of the
// package described by info to a program whose Main returns the value
// of the constant (ct.const.call). infos are the packages of the program.
//
// ready reports whether the value of a computed constant is known; the
// constants used before their values are known are returned in missing,
// and the program must be built again after evaluating them.
func Constant(k *types.Const, info *types.Info, ready func(*types.Const) bool, infos ...*types.Info) (prog *ir.Program, missing []*types.Const, errs []*syntax.Error) {
	b := newBuilder(infos)
	b.ready = ready
	sig := &types.Signature{Params: types.NewTuple(), Results: types.NewTuple(k.Type())}
	fn := ir.NewFunc(info.Pkg.Path+".const$"+k.Name(), sig, k.Pos())
	b.prog.Funcs = append(b.prog.Funcs, fn)
	b.prog.Main = fn
	fb := b.newFuncBuilder(info, fn, nil, nil)
	v := fb.convert(fb.copy(fb.expr(k.Init)), k.Type())
	fb.ret([]ir.Value{v})
	prog, errs = b.run()
	return prog, b.missing, errs
}

func newBuilder(infos []*types.Info) *builder {
	b := &builder{
		prog:    &ir.Program{IntBits: 64},
		infos:   infos,
		declOf:  map[*syntax.FuncDecl]*types.Info{},
		origins: map[*syntax.FuncDecl]*types.Func{},
		funcs:   map[string]*ir.Func{},
		ctors:   map[string]*ir.Func{},
		globals: map[*types.Var]*ir.Global{},
	}
	for _, info := range infos {
		for _, f := range append(append([]*types.Func(nil), info.Funcs...), info.Methods...) {
			b.declOf[f.Decl] = info
			b.origins[f.Decl] = f
		}
	}
	return b
}

// run builds the queued functions.
func (b *builder) run() (*ir.Program, []*syntax.Error) {
	for len(b.queue) > 0 {
		job := b.queue[0]
		b.queue = b.queue[1:]
		job()
	}
	if len(b.errors) > 0 {
		return nil, b.errors
	}
	return b.prog, nil
}

func (b *builder) errorf(pos syntax.Pos, step int, format string, args ...any) {
	b.errors = append(b.errors, &syntax.Error{Pos: pos, Msg: fmt.Sprintf(format, args...) + fmt.Sprintf(": not supported yet by this compiler (spec/24-roadmap.md, step %d)", step)})
}

// typeKey identifies a list of type arguments in the name of an instance.
func typeKey(targs []types.Type) string {
	if len(targs) == 0 {
		return ""
	}
	var s []string
	for _, t := range targs {
		s = append(s, t.String())
	}
	return "[" + strings.Join(s, ",") + "]"
}

// function returns the IR function of a declared function or method with
// the given type arguments (of the function, or of the receiver type of a
// method), building it if needed.
func (b *builder) function(f *types.Func, targs []types.Type) *ir.Func {
	origin := f
	if o, ok := b.origins[f.Decl]; ok {
		origin = o
	}
	sig := origin.Signature()
	name := origin.Name()
	if sig.Recv != nil {
		name = recvName(sig.Recv.Type()) + "." + name
	}
	if p := origin.Pkg(); p != nil {
		name = p.Path + "." + name
	}
	key := name + typeKey(targs)
	if fn, ok := b.funcs[key]; ok {
		return fn
	}
	info := b.declOf[origin.Decl]
	tps := sig.TypeParams
	if sig.Recv != nil {
		tps = sig.RecvTypeParamList()
	}
	fn := ir.NewFunc(key, b.substSig(info, sig, tps, targs), origin.Pos())
	if info != nil {
		fn.RecursionDepth = info.RecursionDepth[origin]
	}
	b.funcs[key] = fn
	b.prog.Funcs = append(b.prog.Funcs, fn)
	b.queue = append(b.queue, func() {
		fb := b.newFuncBuilder(info, fn, tps, targs)
		fb.declBody(origin.Decl, sig)
	})
	return fn
}

func recvName(t types.Type) string {
	if n, ok := t.(*types.Named); ok {
		return n.Obj.Name()
	}
	return t.String()
}

// substSig returns the signature of an instance: with the type arguments
// substituted and the receiver as the first parameter.
func (b *builder) substSig(info *types.Info, sig *types.Signature, tps []*types.TypeParam, targs []types.Type) *types.Signature {
	s := sig
	if len(tps) > 0 {
		s = info.Subst(sig, tps, targs).(*types.Signature)
	}
	return s
}

// funcBuilder translates one function body.
type funcBuilder struct {
	b        *builder
	info     *types.Info
	fn       *ir.Func
	cur      *ir.Block
	tps      []*types.TypeParam
	targs    []types.Type
	vars     map[*types.Var]ir.Value // the address of the cell of each local variable
	parent   *funcBuilder
	free     map[*types.Var]*ir.FreeVar
	captured []*types.Var // variables of enclosing functions, in the order of the free variables
	byRef    bool         // captures by address: the body of a range-over-func loop

	fallthroughTo *ir.Block // the next clause of an expression switch

	targets []*target
	labels  map[string]*ir.Block
	results []ir.Value // cells of the named results
	defers  bool       // the body has defer statements

	// For the body of a range-over-func loop: what break, continue and
	// return do inside it.
	yield *yieldContext

	tuple []ir.Value // the parameters of the tuple of an instance (ct.any.instance)
}

// target is the destination of break and continue.
type target struct {
	label     string
	brk, cont *ir.Block
	yield     bool // the loop is a range-over-func loop translated to a closure
}

func (b *builder) newFuncBuilder(info *types.Info, fn *ir.Func, tps []*types.TypeParam, targs []types.Type) *funcBuilder {
	fb := &funcBuilder{b: b, info: info, fn: fn, tps: tps, targs: targs, vars: map[*types.Var]ir.Value{}, labels: map[string]*ir.Block{}}
	fb.cur = fn.NewBlock("entry")
	return fb
}

// typ substitutes the type arguments of the instance in t.
func (fb *funcBuilder) typ(t types.Type) types.Type {
	if t == nil || len(fb.tps) == 0 {
		return t
	}
	return fb.info.Subst(t, fb.tps, fb.targs)
}

// typeOf returns the type of expression e in the instance.
func (fb *funcBuilder) typeOf(e syntax.Expr) types.Type {
	tv, ok := fb.info.Types[e]
	if !ok {
		return types.Typ[types.Invalid]
	}
	t := tv.Type
	if types.Untyped(t) {
		t = types.Default(t)
	}
	return fb.typ(t)
}

func (fb *funcBuilder) emit(i ir.Instr, t types.Type) ir.Value {
	fb.cur.Emit(i, t)
	if v, ok := i.(ir.Value); ok {
		return v
	}
	return nil
}

func (fb *funcBuilder) emitStmt(i ir.Instr) { fb.cur.Emit(i, nil) }

func (fb *funcBuilder) newBlock(comment string) *ir.Block { return fb.fn.NewBlock(comment) }

// jump ends the current block with a jump to target.
func (fb *funcBuilder) jump(target *ir.Block) {
	if !fb.cur.Terminated() {
		fb.emitStmt(&ir.Jump{})
		ir.AddEdge(fb.cur, target)
	}
}

// branch ends the current block with a conditional jump.
func (fb *funcBuilder) branch(cond ir.Value, then, els *ir.Block) {
	fb.emitStmt(&ir.If{Cond: cond})
	ir.AddEdge(fb.cur, then)
	ir.AddEdge(fb.cur, els)
}

// declBody translates the body of a declared function or method.
func (fb *funcBuilder) declBody(d *syntax.FuncDecl, sig *types.Signature) {
	isig := fb.fn.Sig
	if sig.Recv != nil {
		fb.param(sig.Recv, fb.typ(sig.Recv.Type()))
	}
	for i, v := range sig.Params.Vars {
		fb.param(v, isig.Params.At(i).Type())
	}
	for i, v := range sig.Results.Vars {
		if v.Name() != "" {
			cell := fb.alloc(isig.Results.At(i).Type(), v.Name())
			fb.vars[v] = cell
			fb.results = append(fb.results, cell)
		}
	}
	fb.body(d.Body.List)
	fb.finish()
}

// body translates the statements of a function body.
func (fb *funcBuilder) body(list []syntax.Stmt) {
	for _, s := range list {
		syntax.Walk(s, func(n syntax.Node) bool {
			switch n.(type) {
			case *syntax.FuncLit:
				return false
			case *syntax.DeferStmt:
				fb.defers = true
			}
			return true
		})
	}
	fb.stmts(list)
}

// param declares a parameter: an inout parameter is the address of the
// caller's value; a value parameter is copied into a cell.
func (fb *funcBuilder) param(v *types.Var, t types.Type) {
	name := v.Name()
	if name == "" || name == "_" {
		name = fmt.Sprintf("p%d", len(fb.fn.Params))
	}
	if v.Inout() {
		p := &ir.Param{Pname: name, Typ: &ir.Pointer{Elem: t}, Inout: true}
		fb.fn.Params = append(fb.fn.Params, p)
		fb.vars[v] = p
		return
	}
	p := &ir.Param{Pname: name, Typ: t}
	fb.fn.Params = append(fb.fn.Params, p)
	cell := fb.alloc(t, name)
	fb.emitStmt(&ir.Store{Addr: cell, Val: p})
	fb.vars[v] = cell
}

// finish ends the function: a body that falls off its end returns.
func (fb *funcBuilder) finish() {
	if !fb.cur.Terminated() {
		fb.ret(nil)
	}
}

func (fb *funcBuilder) alloc(t types.Type, comment string) ir.Value {
	return fb.emit(&ir.Alloc{Comment: comment}, &ir.Pointer{Elem: t})
}

// ret returns from the function with results, or the named results.
func (fb *funcBuilder) ret(results []ir.Value) {
	if results == nil && len(fb.results) > 0 {
		for _, cell := range fb.results {
			results = append(results, fb.copy(fb.load(cell)))
		}
	}
	fb.emitStmt(&ir.Return{Results: results})
}

func (fb *funcBuilder) load(addr ir.Value) ir.Value {
	return fb.emit(&ir.Load{Addr: addr}, addr.Type().(*ir.Pointer).Elem)
}

func (fb *funcBuilder) store(addr, v ir.Value) {
	fb.emitStmt(&ir.Store{Addr: addr, Val: v})
}

// copy marks a logical copy of v (mem.value.copy). Values that are not
// shared through storage need none.
func (fb *funcBuilder) copy(v ir.Value) ir.Value {
	switch v.(type) {
	case *ir.Const, *ir.FuncValue, *ir.MakeStruct, *ir.MakeArray, *ir.MakeVariant, *ir.Call, *ir.BinOp, *ir.MakeClosure:
		return v
	}
	if !needsCopy(v.Type()) {
		return v
	}
	return fb.emit(&ir.Copy{X: v}, v.Type())
}

// needsCopy reports whether values of t may share mutable storage.
func needsCopy(t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Basic, *types.Signature, *types.Chan:
		return false
	case *types.Interface:
		return true
	case *types.Struct, *types.Array, *types.Slice, *types.Map, *types.Enum:
		return true
	default:
		_ = u
	}
	return false
}

// local returns the address of the cell of local variable v.
func (fb *funcBuilder) local(v *types.Var) ir.Value {
	if a, ok := fb.vars[v]; ok {
		return a
	}
	if v.Map != nil || v.Static {
		return fb.b.global(v)
	}
	// A variable of an enclosing function, captured by a function literal.
	if fb.parent != nil {
		return fb.capture(v)
	}
	return nil
}

// global returns the address of a hardware mapping or of a package-level
// variable with static storage.
func (b *builder) global(v *types.Var) *ir.Global {
	if g, ok := b.globals[v]; ok {
		return g
	}
	if v.Static {
		g := &ir.Global{Var: v, Typ: &ir.Pointer{Elem: v.Type()}}
		b.globals[v] = g
		return g
	}
	addr := &ir.Const{Typ: v.Map.Type(), Val: v.Map.Val}
	if v.Map.Computed {
		addr = &ir.Const{Typ: v.Map.Type(), Computed: v.Map.Value}
		if c, ok := v.Map.Value.(constant.Value); ok {
			addr = &ir.Const{Typ: v.Map.Type(), Val: c}
		}
	}
	g := &ir.Global{Var: v, Typ: &ir.Pointer{Elem: v.Type()}, Addr: addr}
	b.globals[v] = g
	return g
}

// capture returns the address of a captured variable inside a function
// literal: a copy of the value in a cell of the literal, or the address
// of an inout variable (expr.closure.capture-copy,
// expr.closure.capture-inout).
func (fb *funcBuilder) capture(v *types.Var) ir.Value {
	fv := fb.free[v]
	if fv == nil {
		t := fb.typ(v.Type())
		if v.Inout() || fb.byRef {
			fv = &ir.FreeVar{Vname: v.Name(), Typ: &ir.Pointer{Elem: t}}
		} else {
			fv = &ir.FreeVar{Vname: v.Name(), Typ: t}
		}
		fb.free[v] = fv
		fb.fn.FreeVars = append(fb.fn.FreeVars, fv)
		fb.captured = append(fb.captured, v)
	}
	if v.Inout() || fb.byRef {
		fb.vars[v] = fv
		return fv
	}
	// The cell is created in the entry block, so the captured value is
	// stored before any use.
	entry := fb.fn.Blocks[0]
	saved := fb.cur
	fb.cur = entry
	cell := &ir.Alloc{Comment: v.Name()}
	store := &ir.Store{Val: fv}
	fb.cur = saved
	entry.Emit(cell, &ir.Pointer{Elem: fb.typ(v.Type())})
	store.Addr = cell
	entry.Emit(store, nil)
	// Keep the terminator of the entry block last.
	moveTerminatorLast(entry)
	fb.vars[v] = cell
	return cell
}

func moveTerminatorLast(b *ir.Block) {
	n := len(b.Instrs)
	for i := 0; i < n-2; i++ {
		switch b.Instrs[i].(type) {
		case *ir.Jump, *ir.If, *ir.Return, *ir.Panic:
			t := b.Instrs[i]
			copy(b.Instrs[i:], b.Instrs[i+1:])
			b.Instrs[n-1] = t
			return
		}
	}
}
