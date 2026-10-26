package desc

// The semantics of a description are compiled once into Go closures:
// names are resolved to local slots, registers and operands when the
// description is loaded, so that executing an instruction does not look
// up names or allocate (tool.emu.generated).

// cexpr evaluates an expression; cstmt executes a statement.
type (
	cexpr func(*ctx) val
	cstmt func(*ctx)
)

// ctx is the state of one execution of compiled semantics.
type ctx struct {
	m    *Machine
	vars []val
	ops  []*operandVal // the operands a, b, c and d of the instruction
	ret  val
	done bool
	brk  bool
}

// cfunc is a compiled function of the shared definitions.
type cfunc struct {
	nslots int
	body   cstmt
}

// compiled is compiled semantics with the number of local slots it uses.
type compiled struct {
	nslots int
	body   cstmt
}

// compiler compiles statements and expressions of one function or
// instruction.
type compiler struct {
	a      *Arch
	scopes []map[string]int
	nslots int
}

func newCompiler(a *Arch, locals ...string) *compiler {
	cp := &compiler{a: a, scopes: []map[string]int{{}}}
	for _, name := range locals {
		cp.declare(name)
	}
	return cp
}

func (cp *compiler) lookup(name string) (int, bool) {
	for i := len(cp.scopes) - 1; i >= 0; i-- {
		if s, ok := cp.scopes[i][name]; ok {
			return s, true
		}
	}
	return 0, false
}

func (cp *compiler) declare(name string) int {
	if s, ok := cp.lookup(name); ok {
		return s
	}
	s := cp.nslots
	cp.nslots++
	cp.scopes[len(cp.scopes)-1][name] = s
	return s
}

var opIndex = map[string]int{"a": 0, "b": 1, "c": 2, "d": 3}

// compileFuncs compiles the functions of the shared definitions.
func (a *Arch) compileFuncs() {
	a.instrPC = true // the functions are called from instruction semantics
	defer func() { a.instrPC = false }()
	a.cfuncs = map[string]*cfunc{}
	for name := range a.funcs {
		a.cfuncs[name] = &cfunc{}
	}
	for name, f := range a.funcs {
		cp := newCompiler(a, f.Params...)
		body := cp.list(f.Body)
		a.cfuncs[name].body, a.cfuncs[name].nslots = body, cp.nslots
	}
}

// compileBody compiles a statement list with the given predeclared
// locals.
func (a *Arch) compileBody(list []Stmt, locals ...string) *compiled {
	cp := newCompiler(a, locals...)
	body := cp.list(list)
	return &compiled{nslots: cp.nslots, body: body}
}

// compileExpr compiles an expression with the given predeclared locals.
func (a *Arch) compileExpr(x Expr, locals ...string) (cexpr, int) {
	cp := newCompiler(a, locals...)
	e := cp.expr(x)
	return e, cp.nslots
}

func (cp *compiler) list(list []Stmt) cstmt {
	var ss []cstmt
	for _, s := range list {
		ss = append(ss, cp.stmt(s))
	}
	return func(c *ctx) {
		for _, s := range ss {
			if c.done || c.brk {
				return
			}
			s(c)
		}
	}
}

func (cp *compiler) block(list []Stmt) cstmt {
	cp.scopes = append(cp.scopes, map[string]int{})
	s := cp.list(list)
	cp.scopes = cp.scopes[:len(cp.scopes)-1]
	return s
}

func (cp *compiler) stmt(s Stmt) cstmt {
	switch s := s.(type) {
	case *LetStmt:
		x := cp.expr(s.X)
		slot := cp.declare(s.Name)
		return func(c *ctx) { c.vars[slot] = x(c) }
	case *AssignStmt:
		x := cp.expr(s.X)
		switch l := s.LHS.(type) {
		case *Ident:
			if slot, ok := cp.lookup(l.Name); ok {
				return func(c *ctx) {
					v := x(c)
					w := c.vars[slot].w
					if w == 0 {
						w = v.w
					}
					c.vars[slot] = val{v.v & mask(w), w}
				}
			}
			if r := cp.a.regs[l.Name]; r != nil {
				return func(c *ctx) { c.m.writeReg(r, x(c).v) }
			}
			if i, ok := opIndex[l.Name]; ok {
				return func(c *ctx) {
					v := x(c).v
					switch o := c.ops[i]; {
					case o.reg != nil:
						c.m.writeReg(o.reg, v)
					case o.mem:
						c.m.writeOperand(o, v)
					default:
						failf("assignment to an immediate operand")
					}
				}
			}
			name := l.Name
			return func(c *ctx) { failf("cannot assign to %s", name) }
		case *MemExpr:
			addr := cp.expr(l.Addr)
			size := l.Size
			return func(c *ctx) {
				v := x(c)
				c.m.Write(addr(c).v&c.m.addrMask(), size, v.v)
			}
		}
	case *IfStmt:
		cond := cp.expr(s.Cond)
		then, els := cp.block(s.Then), cp.block(s.Else)
		return func(c *ctx) {
			if cond(c).v != 0 {
				then(c)
			} else {
				els(c)
			}
		}
	case *WhileStmt:
		cond := cp.expr(s.Cond)
		body := cp.block(s.Body)
		return func(c *ctx) {
			for !c.done && cond(c).v != 0 {
				body(c)
				if c.brk {
					c.brk = false
					break
				}
			}
		}
	case *BreakStmt:
		return func(c *ctx) { c.brk = true }
	case *ReturnStmt:
		if s.X == nil {
			return func(c *ctx) { c.done = true }
		}
		x := cp.expr(s.X)
		return func(c *ctx) {
			c.ret = x(c)
			c.done = true
		}
	case *ExprStmt:
		x := cp.expr(s.X)
		return func(c *ctx) { x(c) }
	}
	return func(c *ctx) { failf("invalid statement") }
}

func (cp *compiler) expr(x Expr) cexpr {
	switch x := x.(type) {
	case *Num:
		v := val{x.Val, 0}
		return func(*ctx) val { return v }
	case *Ident:
		if slot, ok := cp.lookup(x.Name); ok {
			return func(c *ctx) val { return c.vars[slot] }
		}
		if r := cp.a.regs[x.Name]; r != nil {
			w := r.Width
			if r == cp.a.pc && cp.a.PCRead != 0 && cp.a.instrPC {
				// The program counter reads as the address of the
				// instruction plus PCRead.
				return func(c *ctx) val { return val{(c.m.istart + uint64(cp.a.PCRead)) & mask(w), w} }
			}
			return func(c *ctx) val { return val{c.m.readReg(r), w} }
		}
		if i, ok := opIndex[x.Name]; ok {
			a := cp.a
			return func(c *ctx) val {
				o := c.ops[i]
				switch {
				case o.reg == a.pc && a.PCRead != 0:
					// A program counter operand reads as the program
					// counter does.
					return val{(c.m.istart + uint64(a.PCRead)) & mask(o.reg.Width), o.reg.Width}
				case o.reg != nil:
					return val{c.m.readReg(o.reg), o.reg.Width}
				case o.mem:
					return val{c.m.readOperand(o), o.width}
				}
				return val{o.value, o.width}
			}
		}
		name := x.Name
		return func(*ctx) val { failf("undefined: %s", name); return val{} }
	case *Unary:
		v := cp.expr(x.X)
		switch x.Op {
		case "!":
			return func(c *ctx) val { return boolVal(v(c).v == 0) }
		case "-":
			return func(c *ctx) val { y := v(c); return val{-y.v & mask(y.w), y.w} }
		case "~":
			return func(c *ctx) val { y := v(c); return val{^y.v & mask(y.w), y.w} }
		}
	case *Binary:
		return cp.binary(x)
	case *IndexExpr:
		v, hi := cp.expr(x.X), cp.expr(x.Hi)
		if x.Lo == nil {
			return func(c *ctx) val {
				h := hi(c).v
				return val{(v(c).v >> h) & 1, 1}
			}
		}
		lo := cp.expr(x.Lo)
		return func(c *ctx) val {
			y := v(c)
			h, l := hi(c).v, lo(c).v
			n := int(h-l) + 1
			return val{(y.v >> l) & mask(n), n}
		}
	case *MemExpr:
		addr := cp.expr(x.Addr)
		size := x.Size
		return func(c *ctx) val {
			return val{c.m.Read(addr(c).v&c.m.addrMask(), size), size}
		}
	case *CallExpr:
		return cp.call(x)
	}
	return func(*ctx) val { failf("invalid expression"); return val{} }
}

func (cp *compiler) binary(x *Binary) cexpr {
	l, r := cp.expr(x.X), cp.expr(x.Y)
	switch x.Op {
	case "&&":
		return func(c *ctx) val {
			if l(c).v == 0 {
				return boolVal(false)
			}
			return boolVal(r(c).v != 0)
		}
	case "||":
		return func(c *ctx) val {
			if l(c).v != 0 {
				return boolVal(true)
			}
			return boolVal(r(c).v != 0)
		}
	case "==", "!=", "<", "<=", ">", ">=":
		op := x.Op
		return func(c *ctx) val {
			a, b := l(c), r(c)
			m := mask(width2(a, b))
			p, q := a.v&m, b.v&m
			switch op {
			case "==":
				return boolVal(p == q)
			case "!=":
				return boolVal(p != q)
			case "<":
				return boolVal(p < q)
			case "<=":
				return boolVal(p <= q)
			case ">":
				return boolVal(p > q)
			}
			return boolVal(p >= q)
		}
	case "<<":
		return func(c *ctx) val {
			a, b := l(c), r(c)
			if b.v >= 64 {
				return val{0, a.w}
			}
			return val{(a.v << b.v) & mask(a.w), a.w}
		}
	case ">>":
		return func(c *ctx) val {
			a, b := l(c), r(c)
			if b.v >= 64 {
				return val{0, a.w}
			}
			return val{(a.v & mask(a.w)) >> b.v, a.w}
		}
	case ">>>":
		return func(c *ctx) val {
			a, b := l(c), r(c)
			s := b.v
			if s >= 64 {
				s = 63
			}
			return val{uint64(signed(a)>>s) & mask(a.w), a.w}
		}
	case "+":
		return func(c *ctx) val { a, b := l(c), r(c); w := width2(a, b); return val{(a.v + b.v) & mask(w), w} }
	case "-":
		return func(c *ctx) val { a, b := l(c), r(c); w := width2(a, b); return val{(a.v - b.v) & mask(w), w} }
	case "*":
		return func(c *ctx) val { a, b := l(c), r(c); w := width2(a, b); return val{(a.v * b.v) & mask(w), w} }
	case "&":
		return func(c *ctx) val { a, b := l(c), r(c); w := width2(a, b); return val{a.v & b.v & mask(w), w} }
	case "|":
		return func(c *ctx) val { a, b := l(c), r(c); w := width2(a, b); return val{(a.v | b.v) & mask(w), w} }
	case "^":
		return func(c *ctx) val { a, b := l(c), r(c); w := width2(a, b); return val{(a.v ^ b.v) & mask(w), w} }
	}
	op := x.Op
	return func(*ctx) val { failf("invalid operator %s", op); return val{} }
}

func (cp *compiler) call(x *CallExpr) cexpr {
	var args []cexpr
	for _, a := range x.Args {
		args = append(args, cp.expr(a))
	}
	arg := func(c *ctx, i int) val { return args[i](c) }
	switch x.Fun {
	case "zx", "tr":
		return func(c *ctx) val { v, n := arg(c, 0), int(arg(c, 1).v); return val{v.v & mask(n), n} }
	case "sx":
		return func(c *ctx) val {
			v, n := arg(c, 0), int(arg(c, 1).v)
			if v.w == 0 {
				return val{v.v & mask(n), n}
			}
			return val{signExtend(v.v&mask(v.w), v.w) & mask(n), n}
		}
	case "width":
		return func(c *ctx) val { return val{uint64(arg(c, 0).w), 0} }
	case "msb":
		return func(c *ctx) val { v := arg(c, 0); return val{(v.v >> uint(v.w-1)) & 1, 1} }
	case "lts", "les", "gts", "ges":
		fun := x.Fun
		return func(c *ctx) val {
			l, r := arg(c, 0), arg(c, 1)
			w := width2(l, r)
			a, b := signed(val{l.v, w}), signed(val{r.v, w})
			switch fun {
			case "lts":
				return boolVal(a < b)
			case "les":
				return boolVal(a <= b)
			case "gts":
				return boolVal(a > b)
			}
			return boolVal(a >= b)
		}
	case "divu", "remu", "divs", "rems":
		fun := x.Fun
		return func(c *ctx) val {
			l, r := arg(c, 0), arg(c, 1)
			w := width2(l, r)
			if r.v&mask(w) == 0 {
				failf("division by zero in the semantics")
			}
			switch fun {
			case "divu":
				return val{(l.v & mask(w)) / (r.v & mask(w)), w}
			case "remu":
				return val{(l.v & mask(w)) % (r.v & mask(w)), w}
			}
			a, b := signed(val{l.v, w}), signed(val{r.v, w})
			if fun == "divs" {
				return val{uint64(a/b) & mask(w), w}
			}
			return val{uint64(a%b) & mask(w), w}
		}
	case "in8", "in16":
		w := 8
		if x.Fun == "in16" {
			w = 16
		}
		return func(c *ctx) val {
			port := arg(c, 0).v
			if c.m.In == nil {
				return val{0, w}
			}
			return val{c.m.In(port, w) & mask(w), w}
		}
	case "out8", "out16":
		w := 8
		if x.Fun == "out16" {
			w = 16
		}
		return func(c *ctx) val {
			port, v := arg(c, 0).v, arg(c, 1).v
			if c.m.Out != nil {
				c.m.Out(port, w, v&mask(w))
			}
			return val{}
		}
	case "halt":
		return func(c *ctx) val { c.m.Halted = true; return val{} }
	case "ea", "segof":
		i := opIndex[x.Args[0].(*Ident).Name]
		word := cp.a.Word
		if x.Fun == "ea" {
			return func(c *ctx) val { return val{c.ops[i].off, word} }
		}
		return func(c *ctx) val { return val{c.ops[i].seg, 16} }
	}
	f := cp.a.cfuncs[x.Fun]
	if f == nil {
		name := x.Fun
		return func(*ctx) val { failf("undefined function %s", name); return val{} }
	}
	return func(c *ctx) val {
		m := c.m
		base := m.sp
		m.sp += f.nslots
		if m.sp > len(m.stack) {
			m.stack = append(m.stack, make([]val, m.sp-len(m.stack)+64)...)
		}
		vars := m.stack[base:m.sp]
		for i := range vars {
			vars[i] = val{}
		}
		for i := range args {
			vars[i] = args[i](c)
		}
		nc := ctx{m: m, vars: vars, ops: c.ops}
		f.body(&nc)
		m.sp = base
		return nc.ret
	}
}

// run executes compiled semantics with fresh local slots.
func (m *Machine) run(cs *compiled, ops []*operandVal) {
	base := m.sp
	m.sp += cs.nslots
	if m.sp > len(m.stack) {
		m.stack = append(m.stack, make([]val, m.sp-len(m.stack)+64)...)
	}
	vars := m.stack[base:m.sp]
	for i := range vars {
		vars[i] = val{}
	}
	c := ctx{m: m, vars: vars, ops: ops}
	cs.body(&c)
	m.sp = base
}

// eval evaluates a compiled expression with the given local values.
func (m *Machine) eval(e cexpr, locals ...val) val {
	base := m.sp
	m.sp += len(locals)
	if m.sp > len(m.stack) {
		m.stack = append(m.stack, make([]val, m.sp-len(m.stack)+64)...)
	}
	vars := m.stack[base:m.sp]
	copy(vars, locals)
	c := ctx{m: m, vars: vars}
	v := e(&c)
	m.sp = base
	return v
}
