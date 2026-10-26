package desc

import (
	"fmt"
	"sort"
	"strings"
)

// The widths of the semantics notation: every value is a bit vector of
// a width from 1 to 64; literals and the results of width() are unsized
// and take the width of the other operand. Functions of the shared
// definitions have no declared widths: each call checks the function for
// the widths of its arguments.

// wval is the width of a value, 0 if unsized, with its value if it is a
// constant known when the description is checked.
type wval struct {
	w     int
	konst bool
	val   uint64
}

type wvar struct {
	w     int
	konst bool
	val   uint64
}

type wenv struct {
	ctx     string
	vars    map[string]*wvar
	inst    *Instruction
	fn      *FuncDecl
	returns []wval
	loops   int
	stack   []string // functions being checked, for recursion
	src     string   // the text being checked, for positions
}

func (c *checker) env(ctx string) *wenv {
	return &wenv{ctx: ctx, vars: map[string]*wvar{}}
}

// builtins are the built-in functions: the number of arguments, or -1.
var builtins = map[string]*struct{ args int }{
	"zx": {2}, "sx": {2}, "tr": {2}, "width": {1}, "msb": {1},
	"lts": {2}, "les": {2}, "gts": {2}, "ges": {2},
	"divu": {2}, "remu": {2}, "divs": {2}, "rems": {2},
	"in8": {1}, "in16": {1}, "out8": {2}, "out16": {2},
	"halt": {0}, "ea": {1}, "segof": {1},
}

type semFail struct{ msg string }

func (e *wenv) failf(format string, args ...any) {
	panic(semFail{fmt.Sprintf(format, args...)})
}

// exprIn checks expression x and reports an error in the context of env.
func (c *checker) exprIn(env *wenv, x Expr) (v wval, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			f, isFail := r.(semFail)
			if !isFail {
				panic(r)
			}
			c.errorf("%s: %s", env.ctx, f.msg)
			ok = false
		}
	}()
	return c.expr(env, x), true
}

func (c *checker) stmts(env *wenv, list []Stmt) {
	defer func() {
		if r := recover(); r != nil {
			f, isFail := r.(semFail)
			if !isFail {
				panic(r)
			}
			c.errorf("%s: semantics: %s", env.ctx, f.msg)
		}
	}()
	c.block(env, list)
}

func (c *checker) block(env *wenv, list []Stmt) {
	saved := map[string]*wvar{}
	for k, v := range env.vars {
		saved[k] = v
	}
	for _, s := range list {
		c.stmt(env, s)
	}
	// Variables declared in the block end with it.
	env.vars = saved
}

func unify(env *wenv, x, y wval, op string) int {
	switch {
	case x.w == 0:
		return y.w
	case y.w == 0 || x.w == y.w:
		return x.w
	}
	env.failf("operator %s on widths %d and %d", op, x.w, y.w)
	return 0
}

func (c *checker) stmt(env *wenv, s Stmt) {
	switch s := s.(type) {
	case *LetStmt:
		v := c.expr(env, s.X)
		if v.w < 0 {
			env.failf("let %s: the call has no value", s.Name)
		}
		env.vars[s.Name] = &wvar{w: v.w, konst: v.konst, val: v.val}
	case *AssignStmt:
		v := c.expr(env, s.X)
		if v.w < 0 {
			env.failf("the assigned call has no value")
		}
		switch l := s.LHS.(type) {
		case *Ident:
			if lv := env.vars[l.Name]; lv != nil {
				if lv.w != 0 && v.w != 0 && lv.w != v.w {
					env.failf("assignment of a value of width %d to %s of width %d", v.w, l.Name, lv.w)
				}
				if lv.w == 0 {
					lv.w = v.w
				}
				lv.konst = false
				return
			}
			w := c.lvalueWidth(env, l.Name)
			if v.w != 0 && v.w != w {
				env.failf("assignment of a value of width %d to %s of width %d", v.w, l.Name, w)
			}
		case *MemExpr:
			c.expr(env, l.Addr)
			if v.w != 0 && v.w != l.Size {
				env.failf("assignment of a value of width %d to mem%d", v.w, l.Size)
			}
		}
	case *IfStmt:
		c.cond(env, s.Cond)
		c.block(env, s.Then)
		c.block(env, s.Else)
	case *WhileStmt:
		c.cond(env, s.Cond)
		env.loops++
		c.block(env, s.Body)
		env.loops--
	case *BreakStmt:
		if env.loops == 0 {
			env.failf("break outside a loop")
		}
	case *ReturnStmt:
		if env.fn == nil {
			env.failf("return outside a function")
		}
		if s.X == nil {
			env.returns = append(env.returns, wval{w: -1})
		} else {
			env.returns = append(env.returns, c.expr(env, s.X))
		}
	case *ExprStmt:
		c.expr(env, s.X)
	}
}

func (c *checker) cond(env *wenv, x Expr) {
	v := c.expr(env, x)
	if v.w != 1 && v.w != 0 {
		env.failf("a condition must have width 1, not %d", v.w)
	}
}

// lvalueWidth returns the width of an assignable name: a register or an
// operand that is a register or memory.
func (c *checker) lvalueWidth(env *wenv, name string) int {
	if r := c.a.regs[name]; r != nil {
		return r.Width
	}
	if o := c.operand(env, name); o != nil {
		if o.Imm != 0 || o.Constant {
			env.failf("cannot assign to the immediate operand %s", name)
		}
		w := o.ValueWidth(c.a)
		if w == 0 {
			env.failf("cannot assign to the address operand %s", name)
		}
		return w
	}
	env.failf("cannot assign to %s: not a register, an operand or a variable", name)
	return 0
}

// operand returns the operand named by a, b, c or d, or nil.
func (c *checker) operand(env *wenv, name string) *Operand {
	if env.inst == nil || len(name) != 1 {
		return nil
	}
	i := strings.IndexByte("abcd", name[0])
	if i < 0 || i >= len(env.inst.ops) {
		return nil
	}
	return env.inst.ops[i]
}

func (c *checker) konst(env *wenv, x Expr, what string) uint64 {
	v := c.expr(env, x)
	if !v.konst {
		env.failf("%s must be a constant", what)
	}
	return v.val
}

func mask(w int) uint64 {
	if w >= 64 || w == 0 {
		return ^uint64(0)
	}
	return 1<<uint(w) - 1
}

func (c *checker) expr(env *wenv, x Expr) wval {
	switch x := x.(type) {
	case *Num:
		return wval{konst: true, val: x.Val}
	case *Ident:
		if v := env.vars[x.Name]; v != nil {
			return wval{w: v.w, konst: v.konst, val: v.val}
		}
		if r := c.a.regs[x.Name]; r != nil {
			return wval{w: r.Width}
		}
		if o := c.operand(env, x.Name); o != nil {
			if o.Constant {
				return wval{konst: true, val: uint64(o.Value)}
			}
			w := o.ValueWidth(c.a)
			if w == 0 {
				env.failf("operand %s of kind %s is an address: use ea(%s)", x.Name, o.Name, x.Name)
			}
			return wval{w: w}
		}
		env.failf("undefined: %s", x.Name)
	case *Unary:
		v := c.expr(env, x.X)
		switch x.Op {
		case "!":
			if v.w > 1 {
				env.failf("operator ! on width %d", v.w)
			}
			return wval{w: 1}
		case "-":
			if v.konst {
				return wval{w: v.w, konst: true, val: -v.val & mask(v.w)}
			}
		case "~":
			if v.konst && v.w != 0 {
				return wval{w: v.w, konst: true, val: ^v.val & mask(v.w)}
			}
		}
		return wval{w: v.w}
	case *Binary:
		xv, yv := c.expr(env, x.X), c.expr(env, x.Y)
		if xv.w < 0 || yv.w < 0 {
			env.failf("operator %s on a call without a value", x.Op)
		}
		switch x.Op {
		case "&&", "||":
			if xv.w > 1 || yv.w > 1 {
				env.failf("operator %s on widths %d and %d", x.Op, xv.w, yv.w)
			}
			return wval{w: 1}
		case "==", "!=", "<", "<=", ">", ">=":
			unify(env, xv, yv, x.Op)
			if xv.konst && yv.konst {
				var r bool
				switch x.Op {
				case "==":
					r = xv.val == yv.val
				case "!=":
					r = xv.val != yv.val
				case "<":
					r = xv.val < yv.val
				case "<=":
					r = xv.val <= yv.val
				case ">":
					r = xv.val > yv.val
				case ">=":
					r = xv.val >= yv.val
				}
				v := uint64(0)
				if r {
					v = 1
				}
				return wval{w: 1, konst: true, val: v}
			}
			return wval{w: 1}
		case "<<", ">>", ">>>":
			if xv.w == 0 {
				env.failf("operator %s on an unsized operand", x.Op)
			}
			return wval{w: xv.w}
		}
		w := unify(env, xv, yv, x.Op)
		if xv.konst && yv.konst {
			var r uint64
			switch x.Op {
			case "+":
				r = xv.val + yv.val
			case "-":
				r = xv.val - yv.val
			case "*":
				r = xv.val * yv.val
			case "&":
				r = xv.val & yv.val
			case "|":
				r = xv.val | yv.val
			case "^":
				r = xv.val ^ yv.val
			}
			return wval{w: w, konst: true, val: r & mask(w)}
		}
		return wval{w: w}
	case *IndexExpr:
		v := c.expr(env, x.X)
		if v.w <= 0 {
			env.failf("index of an unsized value")
		}
		hi := c.konst(env, x.Hi, "an index")
		lo := hi
		if x.Lo != nil {
			lo = c.konst(env, x.Lo, "an index")
		}
		if hi >= uint64(v.w) || lo > hi {
			env.failf("bits [%d:%d] of a value of width %d", hi, lo, v.w)
		}
		return wval{w: int(hi-lo) + 1}
	case *MemExpr:
		if v := c.expr(env, x.Addr); v.w != 0 && v.w > 64 {
			env.failf("address of width %d", v.w)
		}
		return wval{w: x.Size}
	case *CallExpr:
		return c.call(env, x)
	}
	env.failf("invalid expression")
	return wval{}
}

func (c *checker) call(env *wenv, x *CallExpr) wval {
	if b := builtins[x.Fun]; b != nil {
		if b.args >= 0 && len(x.Args) != b.args {
			env.failf("%s takes %d arguments", x.Fun, b.args)
		}
		arg := func(i int) wval { return c.expr(env, x.Args[i]) }
		switch x.Fun {
		case "zx", "sx", "tr":
			v := arg(0)
			n := int(c.konst(env, x.Args[1], "the width of "+x.Fun))
			if n <= 0 || n > 64 {
				env.failf("%s to width %d", x.Fun, n)
			}
			if x.Fun == "sx" && v.w == 0 {
				env.failf("sx of an unsized value: give it a width with zx")
			}
			if x.Fun == "tr" && v.w != 0 && v.w < n || x.Fun != "tr" && v.w > n {
				env.failf("%s of a value of width %d to width %d", x.Fun, v.w, n)
			}
			if v.konst && x.Fun != "sx" {
				return wval{w: n, konst: true, val: v.val & mask(n)}
			}
			return wval{w: n}
		case "width":
			v := arg(0)
			if v.w <= 0 {
				env.failf("width of an unsized value")
			}
			return wval{konst: true, val: uint64(v.w)}
		case "msb":
			if arg(0).w <= 0 {
				env.failf("msb of an unsized value")
			}
			return wval{w: 1}
		case "lts", "les", "gts", "ges":
			unify(env, arg(0), arg(1), x.Fun)
			return wval{w: 1}
		case "divu", "remu", "divs", "rems":
			return wval{w: unify(env, arg(0), arg(1), x.Fun)}
		case "in8", "in16":
			arg(0)
			return wval{w: map[string]int{"in8": 8, "in16": 16}[x.Fun]}
		case "out8", "out16":
			arg(0)
			if v := arg(1); v.w != 0 && v.w != map[string]int{"out8": 8, "out16": 16}[x.Fun] {
				env.failf("%s of a value of width %d", x.Fun, v.w)
			}
			return wval{w: -1}
		case "halt":
			return wval{w: -1}
		case "ea", "segof":
			id, ok := x.Args[0].(*Ident)
			o := (*Operand)(nil)
			if ok {
				o = c.operand(env, id.Name)
			}
			if o == nil || o.Memory == 0 {
				env.failf("%s needs a memory operand", x.Fun)
			}
			if x.Fun == "ea" {
				return wval{w: c.a.Word}
			}
			return wval{w: 16}
		}
	}
	f := c.a.funcs[x.Fun]
	if f == nil {
		env.failf("undefined function %s", x.Fun)
	}
	if len(x.Args) != len(f.Params) {
		env.failf("%s takes %d arguments, not %d", x.Fun, len(f.Params), len(x.Args))
	}
	for _, s := range env.stack {
		if s == f.Name {
			env.failf("recursive call of %s", f.Name)
		}
	}
	fenv := &wenv{ctx: env.ctx, vars: map[string]*wvar{}, inst: env.inst, fn: f, stack: append(append([]string(nil), env.stack...), f.Name)}
	for i, p := range f.Params {
		v := c.expr(env, x.Args[i])
		if v.w < 0 {
			env.failf("argument of %s has no value", x.Fun)
		}
		fenv.vars[p] = &wvar{w: v.w, konst: v.konst, val: v.val}
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if f, ok := r.(semFail); ok {
					env.failf("in %s: %s", x.Fun, f.msg)
				}
				panic(r)
			}
		}()
		c.block(fenv, f.Body)
	}()
	if len(fenv.returns) == 0 {
		return wval{w: -1}
	}
	widths := map[int]bool{}
	for _, r := range fenv.returns {
		widths[r.w] = true
	}
	delete(widths, 0)
	if len(widths) > 1 {
		var ws []int
		for w := range widths {
			ws = append(ws, w)
		}
		sort.Ints(ws)
		env.failf("%s returns values of widths %v", x.Fun, ws)
	}
	for w := range widths {
		return wval{w: w}
	}
	return wval{}
}
