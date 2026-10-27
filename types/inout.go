package types

import (
	"go/constant"

	"an/syntax"
)

// accessPath is the storage location an addressable operand denotes: a
// root variable and a sequence of fields, indices and subranges
// (mem.inout.overlap).
type accessPath struct {
	root  *Var
	elems []accessStep
}

// accessStep is one step of an access path.
type accessStep struct {
	field  string         // a struct field; "" for an index or a subrange
	index  constant.Value // a constant index; nil if unknown or a subrange
	slice  bool           // a subrange lo:hi
	lo, hi constant.Value // constant bounds of a subrange; nil if unknown
}

// pathOf returns the access path of the addressable operand e, using the
// recorded uses and constant values; ok is false if e is not a path.
func (c *checker) pathOf(e syntax.Expr) (accessPath, bool) {
	switch e := unparen(e).(type) {
	case *syntax.Name:
		v, ok := c.info.Uses[e].(*Var)
		if !ok || v.field {
			return accessPath{}, false
		}
		return accessPath{root: v}, true
	case *syntax.SelectorExpr:
		p, ok := c.pathOf(e.X)
		if !ok {
			return p, false
		}
		f, ok := c.info.Uses[e.Sel].(*Var)
		if !ok || !f.field {
			return accessPath{}, false
		}
		return p.with(accessStep{field: f.name}), true
	case *syntax.IndexExpr:
		if tv, ok := c.info.Types[e.X]; ok {
			if _, isMap := core(tv.Type).(*Map); isMap {
				return accessPath{}, false
			}
		}
		p, ok := c.pathOf(e.X)
		if !ok || len(e.Indices) != 1 {
			return p, false
		}
		return p.with(accessStep{index: c.constValue(e.Indices[0])}), true
	case *syntax.SliceExpr:
		p, ok := c.pathOf(e.X)
		if !ok {
			return p, false
		}
		el := accessStep{slice: true, lo: constant.MakeInt64(0)}
		if e.Low != nil {
			el.lo = c.constValue(e.Low)
		}
		if e.High != nil {
			el.hi = c.constValue(e.High)
		}
		return p.with(el), true
	}
	return accessPath{}, false
}

func (p accessPath) with(e accessStep) accessPath {
	elems := append(append([]accessStep(nil), p.elems...), e)
	return accessPath{root: p.root, elems: elems}
}

func (c *checker) constValue(e syntax.Expr) constant.Value {
	if tv, ok := c.info.Types[e]; ok && tv.Value != nil {
		return constant.ToInt(tv.Value)
	}
	return nil
}

// overlaps reports whether two access paths may denote overlapping
// storage: they do unless they are proven disjoint (mem.inout.overlap).
// Without the solver, only constant indices and bounds are compared.
func overlaps(a, b accessPath) bool {
	if a.root != b.root {
		return false
	}
	for i := 0; i < len(a.elems) && i < len(b.elems); i++ {
		if disjoint(a.elems[i], b.elems[i]) {
			return false
		}
		if a.elems[i] != b.elems[i] {
			// Different but overlapping steps: the rest is not comparable.
			return true
		}
	}
	return true // one path is a prefix of the other
}

func disjoint(x, y accessStep) bool {
	switch {
	case x.field != "" || y.field != "":
		return x.field != y.field
	case !x.slice && !y.slice:
		return x.index != nil && y.index != nil && constant.Compare(x.index, tokenNeq, y.index)
	case x.slice && y.slice:
		return before(x.hi, y.lo) || before(y.hi, x.lo)
	case x.slice:
		return x.index == nil && y.index != nil && outside(y.index, x.lo, x.hi)
	default:
		return y.index == nil && x.index != nil && outside(x.index, y.lo, y.hi)
	}
}

// before reports whether the subrange ending at hi ends at or before lo.
func before(hi, lo constant.Value) bool {
	return hi != nil && lo != nil && constant.Compare(hi, tokenLeq, lo)
}

func outside(i, lo, hi constant.Value) bool {
	return lo != nil && constant.Compare(i, tokenLss, lo) || hi != nil && constant.Compare(i, tokenGeq, hi)
}

// inoutArg checks an argument for an inout parameter
// (expr.call.inout-arg, mem.inout.addressable).
func (c *checker) inoutArg(x *operand, param *Var, context string) {
	if x.mode == invalid {
		return
	}
	switch {
	case x.mode == mapindex:
		c.errorf(x.expr.Pos(), "expr.addr.map", "cannot pass %s as an inout argument: a map element is not addressable", exprString(x.expr))
		return
	case x.mode != variable && c.computedRoot(x.expr) != nil:
		c.errorf(x.expr.Pos(), "ct.const.immutable", "cannot pass %s as an inout argument: the constant %s cannot be modified", exprString(x.expr), c.computedRoot(x.expr).name)
		return
	case x.mode != variable:
		c.errorf(x.expr.Pos(), "expr.call.inout-arg", "cannot pass %s as an inout argument in %s: it is not addressable", x, context)
		return
	case !Identical(x.typ, param.typ):
		c.errorf(x.expr.Pos(), "expr.call.inout-arg", "cannot pass %s as an inout argument of type %s in %s: the types must be identical", x, param.typ, context)
		return
	}
	c.capturedInout(x.expr)
}

// builtinInout checks the first operand of copy, delete and clear, which
// the built-in function modifies (mem.inout.builtin).
func (c *checker) builtinInout(x *operand, name string) {
	switch x.mode {
	case invalid:
	case mapindex:
		c.errorf(x.expr.Pos(), "expr.addr.map", "cannot modify %s with %s: a map element is not addressable; read it, modify the copy and store it back", exprString(x.expr), name)
	case variable:
		c.capturedInout(x.expr)
	default:
		c.errorf(x.expr.Pos(), "mem.inout.builtin", "cannot modify %s with %s: it is not addressable, so the change would be lost", x, name)
	}
}

// capturedInout reports an inout access to a variable that a function
// literal captures by copy (expr.closure.captured-readonly). Captured
// inout parameters and loop variables are captured by reference and may
// be modified (expr.closure.capture-inout).
func (c *checker) capturedInout(e syntax.Expr) {
	p, ok := c.pathOf(e)
	if !ok || p.root.owner == nil || p.root.owner == c.fn || p.root.inout {
		return
	}
	c.errorf(e.Pos(), "expr.closure.captured-readonly", "cannot take inout access to %s: %s is captured by the function literal, which holds a read-only copy", exprString(e), p.root.name)
}

// inoutOverlap reports overlapping inout accesses of one call
// (expr.call.inout-overlap).
func (c *checker) inoutOverlap(exprs []syntax.Expr) {
	var paths []accessPath
	for _, e := range exprs {
		p, ok := c.pathOf(e)
		if !ok {
			paths = append(paths, accessPath{})
			continue
		}
		for i, q := range paths {
			if q.root != nil && overlaps(p, q) {
				c.errorf(e.Pos(), "expr.call.inout-overlap", "inout access to %s overlaps the inout access to %s in the same call", exprString(e), exprString(exprs[i]))
				break
			}
		}
		paths = append(paths, p)
	}
}

// useInoutVar records the use of an inout parameter or loop variable of
// an enclosing function by a function literal: the literals between are
// second-class (expr.closure.capture-inout).
func (c *checker) useInoutVar(v *Var) {
	for fn := c.fn; fn != nil && fn != v.owner; fn = fn.parent {
		fn.captures = true
		fn.captured = append(fn.captured, v)
	}
}

// secondClassError reports a use of a second-class closure other than a
// direct call or the initialization of a new local variable
// (expr.closure.capture-inout, mem.ref.second-class).
func (c *checker) secondClassError(x *operand, context string) {
	c.errorf(x.expr.Pos(), "expr.closure.capture-inout", "cannot use %s %s: it captures an inout variable and may only be called directly", exprString(x.expr), context)
	x.invalidate()
}

// heldPath is an access path that an inout loop holds while its body
// runs (stmt.range.inout-exclusive).
type heldPath struct {
	path accessPath
	expr syntax.Expr
}

// heldPaths returns the paths that the range expression x of a loop over
// an iterator yielding inout access holds: the inout receiver and the
// inout arguments of the call that produced the iterator.
func (c *checker) heldPaths(x syntax.Expr) []heldPath {
	var exprs []syntax.Expr
	call, ok := unparen(x).(*syntax.CallExpr)
	if !ok {
		// A method value with an inout receiver, called by the loop.
		if sel, ok := unparen(x).(*syntax.SelectorExpr); ok {
			if m, ok := c.info.Uses[sel.Sel].(*Func); ok {
				if sig, ok := m.typ.(*Signature); ok && sig.Recv != nil && sig.Recv.inout {
					if p, ok := c.pathOf(sel.X); ok {
						return []heldPath{{p, sel.X}}
					}
				}
			}
		}
		return nil
	}
	if sel, ok := unparen(call.Fun).(*syntax.SelectorExpr); ok {
		if m, ok := c.info.Uses[sel.Sel].(*Func); ok {
			if sig, ok := m.typ.(*Signature); ok && sig.Recv != nil && sig.Recv.inout {
				exprs = append(exprs, sel.X)
			}
		}
	}
	if tv, ok := c.info.Types[call.Fun]; ok {
		if sig, ok := core(tv.Type).(*Signature); ok {
			for i, p := range sig.Params.Vars {
				if p.inout && i < len(call.Args) {
					exprs = append(exprs, call.Args[i])
				}
			}
		}
	}
	var held []heldPath
	for _, e := range exprs {
		if p, ok := c.pathOf(e); ok {
			held = append(held, heldPath{p, e})
		}
	}
	return held
}

// checkHeld reports an access, in the body of an inout loop, to a path
// that the loop holds (stmt.range.inout-exclusive). e is a maximal path
// expression: not the operand of a selector, index or slice.
func (c *checker) checkHeld(e syntax.Expr) {
	if len(c.held) == 0 {
		return
	}
	// The longest prefix of e that is an access path: x.M() and m[k]
	// access x and m.
	var p accessPath
	for cur := e; ; {
		var ok bool
		if p, ok = c.pathOf(cur); ok {
			break
		}
		switch x := unparen(cur).(type) {
		case *syntax.SelectorExpr:
			cur = x.X
		case *syntax.IndexExpr:
			cur = x.X
		case *syntax.SliceExpr:
			cur = x.X
		default:
			return
		}
	}
	for _, h := range c.held {
		if overlaps(p, h.path) {
			c.errorf(e.Pos(), "stmt.range.inout-exclusive", "%s overlaps %s, which the loop holds while the iterator yields inout access", exprString(e), exprString(h.expr))
			return
		}
	}
}
