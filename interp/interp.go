package interp

import (
	"fmt"
	"go/constant"
	"io"
	"math"
	"strings"

	"an/ir"
	"an/syntax"
	"an/types"
)

// Panic is a panic that ended the program.
type Panic struct {
	Value string   // the printed panic value
	Stack []string // the functions being called, innermost last
}

// LimitError reports that a limit of Options was exceeded.
type LimitError struct {
	Limit string // "steps" or "memory"
	Max   int64
	Stack []string
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("the %s limit (%d) was exceeded", e.Limit, e.Max)
}

// NotExecutableError reports a call of a function of an instruction
// package, which only a target executes (tool.emu.ir).
type NotExecutableError struct {
	Func string
}

func (e *NotExecutableError) Error() string {
	return fmt.Sprintf("%s executes an instruction of its architecture: the IR interpreter cannot run it; run the program in the emulator of its target", e.Func)
}

func (p *Panic) Error() string { return "panic: " + p.Value }

// anPanic is a panic in progress.
type anPanic struct {
	v       value
	t       types.Type // type of v, for printing
	runtime bool       // a run-time error
}

func runtimeError(msg string) *anPanic {
	return &anPanic{v: "runtime error: " + msg, t: types.Typ[types.String], runtime: true}
}

// Options configure a run.
type Options struct {
	Stdout io.Writer // fmt.Print and fmt.Println
	Stderr io.Writer // print, println and panics
	// MaxSteps bounds the number of executed instructions; 0 means no
	// limit.
	MaxSteps int64
	// MaxMemory bounds the number of values the program creates in
	// slices, arrays, structs and maps; 0 means no limit.
	MaxMemory int64
	// ReadFile reads a file for gen.ReadFile; nil means no files can be
	// read (ct.eval.read-input).
	ReadFile func(name string) ([]byte, error)
	// Stdin is the standard input, for console.ReadByte; nil is empty.
	Stdin io.Reader
}

// machine runs one program.
type machine struct {
	prog    *ir.Program
	opts    Options
	funcs   map[*ir.Func]*funcInfo
	consts  map[*ir.Const]value
	steps   int64
	memory  int64
	intBits int
	stack   []*ir.Func // functions being called, innermost last
	statics map[*types.Var]*cell
}

// funcInfo is the prepared form of a function.
type funcInfo struct {
	fn     *ir.Func
	params map[*ir.Param]int
	free   map[*ir.FreeVar]int
}

// frame is the activation of a function.
type frame struct {
	fi     *funcInfo
	regs   []value
	args   []value
	env    []value
	defers []func()
}

// Run runs the program. It returns a *Panic if the program panicked.
func Run(p *ir.Program, opts Options) (err error) {
	m := newMachine(p, opts)
	defer m.recover(&err)
	m.call(m.info(p.Main), nil, nil)
	return nil
}

// Eval runs p.Main, which has no parameters and one result, and returns
// the result as the value of a computed constant (types.Composite and
// the other representations of types). It returns a *Panic or a
// *LimitError if the evaluation does not complete (ct.eval.panic,
// ct.limit.exceeded).
func Eval(p *ir.Program, opts Options) (result any, err error) {
	m := newMachine(p, opts)
	defer m.recover(&err)
	v := m.call(m.info(p.Main), nil, nil)
	return m.constOf(v, p.Main.Sig.Results.At(0).Type())
}

func newMachine(p *ir.Program, opts Options) *machine {
	m := &machine{prog: p, opts: opts, funcs: map[*ir.Func]*funcInfo{}, consts: map[*ir.Const]value{}, intBits: p.IntBits, statics: map[*types.Var]*cell{}}
	if m.intBits == 0 {
		m.intBits = 64
	}
	if opts.Stdout == nil {
		m.opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		m.opts.Stderr = io.Discard
	}
	return m
}

// recover turns a panic of the program into an error.
func (m *machine) recover(err *error) {
	r := recover()
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *anPanic:
		msg := m.panicString(r)
		fmt.Fprintf(m.opts.Stderr, "panic: %s\n", msg)
		*err = &Panic{Value: msg, Stack: m.stackNames()}
	case *LimitError:
		r.Stack = m.stackNames()
		fmt.Fprintf(m.opts.Stderr, "%s\n", r)
		*err = r
	case *NotExecutableError:
		*err = r
	default:
		panic(r)
	}
}

// alloc accounts for n new values (Options.MaxMemory).
func (m *machine) alloc(n int) {
	m.memory += int64(n)
	if m.opts.MaxMemory > 0 && m.memory > m.opts.MaxMemory {
		panic(&LimitError{Limit: "memory", Max: m.opts.MaxMemory})
	}
}

// stackNames returns the functions being called, innermost last.
func (m *machine) stackNames() []string {
	var names []string
	for _, fn := range m.stack {
		names = append(names, fmt.Sprintf("%s (%s)", fn.Name, fn.Pos))
	}
	return names
}

func (m *machine) info(fn *ir.Func) *funcInfo {
	if fi, ok := m.funcs[fn]; ok {
		return fi
	}
	fi := &funcInfo{fn: fn, params: map[*ir.Param]int{}, free: map[*ir.FreeVar]int{}}
	for i, p := range fn.Params {
		fi.params[p] = i
	}
	for i, v := range fn.FreeVars {
		fi.free[v] = i
	}
	m.funcs[fn] = fi
	return fi
}

// call runs a function and returns its result: nil, a value, or a tuple
// ([]value) for several results.
func (m *machine) call(fi *funcInfo, args, env []value) (result value) {
	if strings.HasPrefix(fi.fn.Name, "an/arch/") && !strings.HasSuffix(fi.fn.Name, ".Far") {
		panic(&NotExecutableError{Func: fi.fn.Name})
	}
	fr := &frame{fi: fi, regs: make([]value, fi.fn.NumValues()), args: args, env: env}
	n := len(m.stack)
	m.stack = append(m.stack, fi.fn)
	done := false
	defer func() {
		if done {
			m.stack = m.stack[:n] // while panicking, the stack stays for the report
		}
	}()
	defer func() {
		// Deferred calls run on return and while panicking.
		for i := len(fr.defers) - 1; i >= 0; i-- {
			fr.defers[i]()
		}
	}()
	b := fi.fn.Blocks[0]
	for {
		next, res, end := m.block(fr, b)
		if end {
			done = true
			return res
		}
		b = next
	}
}

// block runs a block and returns the next block, or the result.
func (m *machine) block(fr *frame, b *ir.Block) (*ir.Block, value, bool) {
	for _, in := range b.Instrs {
		m.steps++
		if m.opts.MaxSteps > 0 && m.steps > m.opts.MaxSteps {
			panic(&LimitError{Limit: "steps", Max: m.opts.MaxSteps})
		}
		switch i := in.(type) {
		case *ir.Jump:
			return b.Succs[0], nil, false
		case *ir.If:
			if m.get(fr, i.Cond).(bool) {
				return b.Succs[0], nil, false
			}
			return b.Succs[1], nil, false
		case *ir.Return:
			switch len(i.Results) {
			case 0:
				return nil, nil, true
			case 1:
				return nil, m.get(fr, i.Results[0]), true
			}
			vs := make([]value, len(i.Results))
			for j, r := range i.Results {
				vs[j] = m.get(fr, r)
			}
			return nil, vs, true
		case *ir.Panic:
			panic(&anPanic{v: m.get(fr, i.X), t: i.X.Type()})
		default:
			m.exec(fr, in)
		}
	}
	panic("interp: block without terminator in " + fr.fi.fn.Name)
}

// get returns the value of an operand.
func (m *machine) get(fr *frame, v ir.Value) value {
	switch v := v.(type) {
	case *ir.Const:
		if v.Computed != nil {
			c, ok := m.consts[v]
			if !ok {
				c = m.computedValue(v.Computed, v.Typ)
				pin(c)
				m.consts[v] = c
			}
			return c
		}
		c, ok := m.consts[v]
		if !ok {
			c = m.constValue(v)
			m.consts[v] = c
		}
		if a, ok := c.(*aggregate); ok {
			// A fresh copy of a constant composite value.
			_ = a
			return m.constValue(v)
		}
		return c
	case *ir.Param:
		return fr.args[fr.fi.params[v]]
	case *ir.FreeVar:
		return fr.env[fr.fi.free[v]]
	case *ir.FuncValue:
		return &closure{fn: m.info(v.Func)}
	case interface{ ID() int }:
		return fr.regs[v.ID()]
	case *ir.Global:
		if v.Addr == nil {
			// A variable with static storage, zero at the start.
			c := m.statics[v.Var]
			if c == nil {
				c = &cell{v: m.zero(v.Var.Type())}
				m.statics[v.Var] = c
			}
			return c
		}
		// A hardware mapping: the interpreter has no hardware.
		panic(&NotExecutableError{Func: fr.fi.fn.Name})
	}
	panic(fmt.Sprintf("interp: operand %T", v))
}

func (m *machine) set(fr *frame, in ir.Instr, v value) {
	fr.regs[in.(interface{ ID() int }).ID()] = v
}

func (m *machine) constValue(c *ir.Const) value {
	if c.Val == nil {
		return m.zero(c.Typ)
	}
	b, ok := c.Typ.Underlying().(*types.Basic)
	if !ok {
		if types.IsInterface(c.Typ) {
			// An untyped constant converted to an interface.
			d := types.Default(defaultTypeOf(c.Val))
			return iface{t: d, v: m.constValue(&ir.Const{Typ: d, Val: c.Val})}
		}
		return m.zero(c.Typ)
	}
	switch {
	case b.Info()&types.IsBoolean != 0:
		return constant.BoolVal(c.Val)
	case b.Info()&types.IsUnsigned != 0:
		v, _ := constant.Uint64Val(constant.ToInt(c.Val))
		return v
	case b.Info()&types.IsInteger != 0:
		v, _ := constant.Int64Val(constant.ToInt(c.Val))
		return v
	case b.Info()&types.IsFloat != 0:
		v, _ := constant.Float64Val(constant.ToFloat(c.Val))
		if b.Kind() == types.F32 {
			v = float64(float32(v))
		}
		return v
	case b.Info()&(types.IsString|types.IsTerminated) != 0:
		return constant.StringVal(c.Val)
	}
	return nil
}

func defaultTypeOf(v constant.Value) types.Type {
	switch v.Kind() {
	case constant.Bool:
		return types.Typ[types.Bool]
	case constant.String:
		return types.Typ[types.String]
	case constant.Float:
		return types.Typ[types.F64]
	}
	return types.Typ[types.Int]
}

// exec runs an instruction that does not transfer control.
func (m *machine) exec(fr *frame, in ir.Instr) {
	switch i := in.(type) {
	case *ir.Alloc:
		m.set(fr, i, &cell{v: m.zero(i.Type().(*ir.Pointer).Elem)})
	case *ir.Load:
		m.set(fr, i, m.get(fr, i.Addr).(addr).load())
	case *ir.Store:
		m.get(fr, i.Addr).(addr).store(m.get(fr, i.Val))
	case *ir.Copy:
		m.set(fr, i, share(m.get(fr, i.X)))
	case *ir.FieldAddr:
		m.set(fr, i, &fieldRef{base: m.get(fr, i.X).(addr), i: i.Field})
	case *ir.IndexAddr:
		base := m.get(fr, i.X).(addr)
		idx := int(m.intValue(m.get(fr, i.Index)))
		m.checkIndex(idx, m.length(base.load()))
		m.set(fr, i, &elemRef{base: base, i: idx})
	case *ir.SubsliceAddr:
		base := m.get(fr, i.X).(addr)
		n := m.length(base.load())
		lo, hi := 0, n
		if i.Low != nil {
			lo = int(m.intValue(m.get(fr, i.Low)))
		}
		if i.High != nil {
			hi = int(m.intValue(m.get(fr, i.High)))
		}
		checkSlice(lo, hi, hi, n, false, false)
		m.set(fr, i, &subRef{base: base, lo: lo, hi: hi})
	case *ir.Field:
		m.set(fr, i, m.get(fr, i.X).(*aggregate).elems[i.Field])
	case *ir.Index:
		x := m.get(fr, i.X)
		idx := int(m.intValue(m.get(fr, i.Index)))
		m.checkIndex(idx, m.length(x))
		switch x := x.(type) {
		case string:
			m.set(fr, i, uint64(x[idx]))
		case *aggregate:
			m.set(fr, i, x.elems[idx])
		case sliceVal:
			m.set(fr, i, x.buf.elems[x.off+idx])
		}
	case *ir.Lookup:
		mp, _ := m.get(fr, i.X).(*mapObj)
		v, ok := mp.lookup(m.get(fr, i.Key))
		if !ok {
			v = m.zero(i.X.Type().Underlying().(*types.Map).Elem)
		}
		if i.CommaOk {
			m.set(fr, i, []value{v, ok})
		} else {
			m.set(fr, i, v)
		}
	case *ir.MapUpdate:
		m.alloc(1)
		uniqueMap(m.get(fr, i.Map).(addr)).set(m.get(fr, i.Key), m.get(fr, i.Value))
	case *ir.Slice:
		m.set(fr, i, m.slice(fr, i))
	case *ir.BinOp:
		m.set(fr, i, m.binop(i.Op, i.X.Type(), m.get(fr, i.X), m.get(fr, i.Y)))
	case *ir.UnOp:
		m.set(fr, i, m.unop(i.Op, i.Type(), m.get(fr, i.X)))
	case *ir.Convert:
		m.set(fr, i, m.convert(m.get(fr, i.X), i.X.Type(), i.Type()))
	case *ir.MakeInterface:
		x := m.get(fr, i.X)
		t := i.X.Type()
		if c, ok := i.X.(*ir.Const); ok && types.Untyped(c.Typ) {
			t = types.Default(c.Typ)
		}
		m.set(fr, i, iface{t: t, v: share(x)})
	case *ir.TypeAssert:
		m.set(fr, i, m.typeAssert(m.get(fr, i.X).(iface), i))
	case *ir.MakeSlice:
		n := int(m.intValue(m.get(fr, i.Len)))
		c := int(m.intValue(m.get(fr, i.Cap)))
		if n < 0 || c < n {
			panic(runtimeError("makeslice: len out of range"))
		}
		et := i.Type().Underlying().(*types.Slice).Elem
		m.alloc(c)
		buf := &buffer{elems: make([]value, c), used: n, refs: 1}
		for j := range buf.elems {
			buf.elems[j] = m.zero(et)
		}
		m.set(fr, i, sliceVal{buf: buf, len: n, cap: c})
	case *ir.MakeMap:
		m.set(fr, i, &mapObj{index: map[string]int{}, refs: 1})
	case *ir.MakeStruct:
		m.alloc(len(i.Fields))
		a := &aggregate{elems: make([]value, len(i.Fields)), refs: 1}
		for j, f := range i.Fields {
			a.elems[j] = m.get(fr, f)
		}
		m.set(fr, i, a)
	case *ir.MakeArray:
		m.alloc(len(i.Elems))
		elems := make([]value, len(i.Elems))
		for j, e := range i.Elems {
			elems[j] = m.get(fr, e)
		}
		if _, ok := i.Type().Underlying().(*types.Slice); ok {
			m.set(fr, i, sliceVal{buf: newBuffer(elems), len: len(elems), cap: len(elems)})
		} else {
			m.set(fr, i, &aggregate{elems: elems, refs: 1})
		}
	case *ir.MakeVariant:
		a := &aggregate{elems: make([]value, len(i.Fields)), variant: i.Variant, refs: 1}
		for j, f := range i.Fields {
			a.elems[j] = m.get(fr, f)
		}
		m.set(fr, i, a)
	case *ir.VariantIndex:
		m.set(fr, i, int64(m.get(fr, i.X).(*aggregate).variant))
	case *ir.VariantField:
		m.set(fr, i, m.get(fr, i.X).(*aggregate).elems[i.Field])
	case *ir.MakeClosure:
		env := make([]value, len(i.Bindings))
		for j, b := range i.Bindings {
			env[j] = m.get(fr, b)
		}
		m.set(fr, i, &closure{fn: m.info(i.Fn), env: env})
	case *ir.Call:
		m.set(fr, i, m.doCall(fr, i))
	case *ir.Extract:
		m.set(fr, i, m.get(fr, i.Tuple).([]value)[i.Index])
	case *ir.Asm:
		panic(&NotExecutableError{Func: "asm"})
	case *ir.Defer:
		callee, args := m.callee(fr, i.Call), m.args(fr, i.Call)
		c := i.Call
		fr.defers = append(fr.defers, func() { m.invoke(c, callee, args) })
	default:
		panic(fmt.Sprintf("interp: instruction %T", in))
	}
}

func (m *machine) args(fr *frame, c *ir.Call) []value {
	args := make([]value, len(c.Args))
	for j, a := range c.Args {
		args[j] = m.get(fr, a)
	}
	return args
}

func (m *machine) callee(fr *frame, c *ir.Call) value {
	if b, ok := c.Callee.(*ir.Builtin); ok {
		return b
	}
	return m.get(fr, c.Callee)
}

func (m *machine) doCall(fr *frame, c *ir.Call) value {
	return m.invoke(c, m.callee(fr, c), m.args(fr, c))
}

// invoke calls the callee of a call instruction with evaluated
// arguments.
func (m *machine) invoke(c *ir.Call, callee value, args []value) value {
	switch f := callee.(type) {
	case *ir.Builtin:
		return m.builtin(f, c, args)
	case iface:
		if c.Method == "" {
			break
		}
		if f.t == nil {
			panic(runtimeError("invalid memory address or nil pointer dereference: method call on a nil interface value"))
		}
		return m.callMethod(f, c.Method, args)
	case *closure:
		if f == nil {
			panic(runtimeError("invalid memory address or nil pointer dereference: call of a nil function"))
		}
		return m.call(f.fn, args, f.env)
	}
	panic(fmt.Sprintf("interp: callee %T", callee))
}

// callMethod calls a method of the dynamic value of an interface.
func (m *machine) callMethod(x iface, name string, args []value) value {
	ms := m.prog.Methods(x.t)
	if ms == nil || ms.Methods[name] == nil {
		panic(fmt.Sprintf("interp: no method %s of %s", name, x.t))
	}
	return m.call(m.info(ms.Methods[name]), append([]value{share(x.v)}, args...), nil)
}

func (m *machine) typeAssert(x iface, i *ir.TypeAssert) value {
	ok := x.t != nil
	if ok {
		if ti, isIface := i.Asserted.Underlying().(*types.Interface); isIface {
			ok = m.implements(x.t, ti)
		} else {
			ok = types.Identical(x.t, i.Asserted)
		}
	}
	var v value
	if ok {
		if types.IsInterface(i.Asserted) {
			v = x
		} else {
			v = x.v
		}
	} else {
		if !i.CommaOk {
			have := "nil"
			if x.t != nil {
				have = x.t.String()
			}
			panic(runtimeError(fmt.Sprintf("interface conversion: interface is %s, not %s", have, i.Asserted)))
		}
		v = m.zero(i.Asserted)
	}
	if i.CommaOk {
		return []value{v, ok}
	}
	return v
}

// implements reports whether dynamic type t has the methods of iface.
func (m *machine) implements(t types.Type, iface *types.Interface) bool {
	ms := m.prog.Methods(t)
	for _, im := range iface.MethodSet() {
		if ms == nil {
			return false
		}
		f := ms.Methods[im.Name()]
		if f == nil {
			return false
		}
		want := im.Signature()
		have := f.Sig
		if !types.Identical(&types.Signature{Params: have.Params, Results: have.Results, Variadic: have.Variadic}, want) {
			return false
		}
	}
	return true
}

func (m *machine) length(x value) int {
	switch x := x.(type) {
	case string:
		return len(x)
	case *aggregate:
		return len(x.elems)
	case sliceVal:
		return x.len
	case *mapObj:
		return x.len()
	}
	return 0
}

func (m *machine) checkIndex(i, n int) {
	if i < 0 {
		panic(runtimeError(fmt.Sprintf("index out of range [%d]", i)))
	}
	if i >= n {
		panic(runtimeError(fmt.Sprintf("index out of range [%d] with length %d", i, n)))
	}
}

// checkSlice checks the indices of a slice expression in the order and
// with the messages of Go (expr.bounds.check): x[lo:hi] or x[lo:hi:max],
// where c is the capacity of x, which is a slice if isSlice is set.
func checkSlice(lo, hi, max, c int, three, isSlice bool) {
	fail := func(neg, format string, args ...any) {
		if args[0].(int) < 0 {
			format, args = neg, args[:1]
		}
		panic(runtimeError("slice bounds out of range " + fmt.Sprintf(format, args...)))
	}
	limit := "length"
	if isSlice {
		limit = "capacity"
	}
	if three {
		if max < 0 || max > c {
			fail("[::%d]", "[::%d] with "+limit+" %d", max, c)
		}
		if hi < 0 || hi > max {
			fail("[:%d:]", "[:%d:%d]", hi, max)
		}
		if lo < 0 || lo > hi {
			fail("[%d::]", "[%d:%d:]", lo, hi)
		}
		return
	}
	if hi < 0 || hi > c {
		fail("[:%d]", "[:%d] with "+limit+" %d", hi, c)
	}
	if lo < 0 || lo > hi {
		fail("[%d:]", "[%d:%d]", lo, hi)
	}
}

func (m *machine) slice(fr *frame, i *ir.Slice) value {
	x := m.get(fr, i.X)
	var n, c int
	switch v := x.(type) {
	case addr: // an array
		a := v.load().(*aggregate)
		n, c = len(a.elems), len(a.elems)
	case string:
		n, c = len(v), len(v)
	case sliceVal:
		n, c = v.len, v.cap
	}
	lo, hi, max := 0, n, c
	if i.Low != nil {
		lo = int(m.intValue(m.get(fr, i.Low)))
	}
	if i.High != nil {
		hi = int(m.intValue(m.get(fr, i.High)))
	}
	if i.Max != nil {
		max = int(m.intValue(m.get(fr, i.Max)))
	}
	if _, isString := x.(string); isString {
		c = n
	}
	_, isSlice := x.(sliceVal)
	checkSlice(lo, hi, max, c, i.Max != nil, isSlice)
	switch v := x.(type) {
	case addr:
		// The slice is a copy of the elements (mem.value.copy).
		a := v.load().(*aggregate)
		elems := make([]value, max-lo)
		for k := range elems {
			elems[k] = share(a.elems[lo+k])
		}
		return sliceVal{buf: &buffer{elems: elems, used: hi - lo, refs: 1}, len: hi - lo, cap: max - lo}
	case string:
		return v[lo:hi]
	case sliceVal:
		if v.buf == nil {
			return sliceVal{}
		}
		// Elements beyond the length become visible.
		v.buf.used = maxInt(v.buf.used, v.off+hi)
		return share(sliceVal{buf: v.buf, off: v.off + lo, len: hi - lo, cap: max - lo})
	}
	return nil
}

func (m *machine) intValue(v value) int64 {
	switch v := v.(type) {
	case int64:
		return v
	case uint64:
		return int64(v)
	}
	panic(fmt.Sprintf("interp: not an integer: %T", v))
}

// panicString prints a panic value as Go does.
func (m *machine) panicString(p *anPanic) string {
	if p.runtime {
		s := fmt.Sprint(p.v)
		return s
	}
	var b strings.Builder
	v, t := p.v, p.t
	if x, ok := v.(iface); ok {
		if x.t == nil {
			return "nil"
		}
		v, t = x.v, x.t
	}
	if ms := m.prog.Methods(t); ms != nil {
		if f := ms.Methods["Error"]; f != nil && f.Sig.Params.Len() == 0 {
			return fmt.Sprint(m.call(m.info(f), []value{v}, nil))
		}
	}
	switch v := v.(type) {
	case string:
		if t.Underlying() == types.Typ[types.String] && t != types.Typ[types.String] {
			fmt.Fprintf(&b, "%s(%q)", t, v)
		} else {
			b.WriteString(v)
		}
	default:
		m.formatPrint(&b, v, t)
	}
	return b.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ = math.Inf
var _ = syntax.Add
