package types

import (
	"fmt"
	"strings"

	"an/syntax"
)

// zeroReason records why a type parameter requires a zero value
// (type.zero.param-constraint): an operation in the body of its generic
// function, or a type argument for another type parameter that requires
// one.
type zeroReason struct {
	pos  syntax.Pos
	what string     // the operation, as "var z T"
	via  *TypeParam // the type parameter it is passed to, if any
	fn   string     // the generic function or type that requires it
}

// hasZero reports whether t has a zero value (type.zero.none). Type
// parameters have one; mark is called for each type parameter whose zero
// value t needs (type.zero.param-use).
func hasZero(t Type, mark func(*TypeParam)) bool {
	return hasZeroIn(t, mark, nil)
}

func hasZeroIn(t Type, mark func(*TypeParam), seen []*Named) bool {
	switch t := t.(type) {
	case *TypeParam:
		if mark != nil {
			mark(t)
		}
		return true
	case *Named:
		for _, n := range seen {
			if n == t {
				return true
			}
		}
		seen = append(seen, t)
	}
	switch u := t.Underlying().(type) {
	case *Array:
		return hasZeroIn(u.Elem, mark, seen)
	case *Struct:
		ok := true
		for _, f := range u.Fields {
			ok = hasZeroIn(f.typ, mark, seen) && ok
		}
		return ok
	case *Enum:
		// The zero value is the first variant if it carries no data
		// (type.enum.zero).
		return len(u.Variants) > 0 && !u.Variants[0].data
	}
	return true
}

// whyNoZero explains why t has no zero value.
func whyNoZero(t Type) string {
	return whyNoZeroIn(t, nil)
}

func whyNoZeroIn(t Type, seen []*Named) string {
	if n, ok := t.(*Named); ok {
		for _, s := range seen {
			if s == n {
				return ""
			}
		}
		seen = append(seen, n)
	}
	switch u := t.Underlying().(type) {
	case *Array:
		return fmt.Sprintf("its element type %s has no zero value", u.Elem)
	case *Struct:
		for _, f := range u.Fields {
			if !hasZero(f.typ, nil) {
				return fmt.Sprintf("field %s of type %s has no zero value", f.name, f.typ)
			}
		}
	case *Enum:
		if len(u.Variants) > 0 {
			return fmt.Sprintf("its first variant %s carries data", u.Variants[0].name)
		}
	}
	return ""
}

// zeroUse checks an operation that produces the zero value of t
// implicitly (type.zero.uses). For a type parameter, the operation gives
// it the inferred constraint "requires a zero value"; for a type without
// a zero value, it is an error with the given rule. It reports whether t
// has a zero value.
func (c *checker) zeroUse(pos syntax.Pos, t Type, rule, what string) bool {
	if !isValid(t) {
		return true
	}
	if hasZero(t, func(tp *TypeParam) { c.requireZero(tp, &zeroReason{pos: pos, what: what}) }) {
		return true
	}
	c.errorf(pos, rule, "%s: %s has no zero value (%s)", what, t, whyNoZero(t))
	return false
}

// requireZero gives tp the constraint "requires a zero value".
func (c *checker) requireZero(tp *TypeParam, r *zeroReason) {
	if tp.zero != nil {
		return
	}
	if r.fn == "" && c.fn != nil && c.fn.name != "" {
		r.fn = c.fn.name
	}
	tp.zero = r
	if tp.origParam != nil {
		// A receiver type parameter of a method stands for the type
		// parameter of the generic type.
		c.requireZero(tp.origParam, &zeroReason{pos: r.pos, what: r.what, fn: r.fn})
	}
}

// zeroEdge is a type argument given for a type parameter, recorded to
// check and propagate the zero value constraint once all bodies are
// checked (type.zero.param-instantiation).
type zeroEdge struct {
	tp   *TypeParam
	targ Type
	pos  syntax.Pos
	name string // the generic function or type
}

func (c *checker) recordZeroEdges(pos syntax.Pos, name string, tps []*TypeParam, targs []Type) {
	for i, tp := range tps {
		if i < len(targs) {
			c.zeroEdges = append(c.zeroEdges, zeroEdge{tp, targs[i], pos, name})
		}
	}
}

// checkZeroEdges propagates the zero value constraint from type
// parameters to the type parameters given for them, and reports type
// arguments without a zero value for type parameters that require one.
func (c *checker) checkZeroEdges() {
	for changed := true; changed; {
		changed = false
		for _, e := range c.zeroEdges {
			if e.tp.zero == nil {
				continue
			}
			hasZero(e.targ, func(tp *TypeParam) {
				if tp.zero == nil && tp != e.tp {
					tp.zero = &zeroReason{pos: e.pos, via: e.tp, fn: e.name, what: fmt.Sprintf("passed as %s of %s", e.tp, e.name)}
					changed = true
				}
			})
		}
	}
	reported := map[syntax.Pos]bool{}
	for _, e := range c.zeroEdges {
		if e.tp.zero == nil || reported[e.pos] || !isValid(e.targ) {
			continue
		}
		if !hasZero(e.targ, nil) {
			reported[e.pos] = true
			c.errorf(e.pos, "type.zero.param-instantiation", "%s has no zero value (%s), but %s requires a zero value of %s: %s", e.targ, whyNoZero(e.targ), e.name, e.tp, zeroChain(e.tp))
		}
	}
}

// zeroChain describes the chain from a type parameter to the operation
// that uses its zero value.
func zeroChain(tp *TypeParam) string {
	var parts []string
	for i := 0; tp != nil && tp.zero != nil && i < 16; i++ {
		r := tp.zero
		if r.via == nil {
			parts = append(parts, fmt.Sprintf("%s uses the zero value of %s (%s at %s)", orName(r.fn, "the function"), tp, r.what, r.pos))
			break
		}
		parts = append(parts, fmt.Sprintf("%s is passed as %s of %s at %s", tp, r.via, r.fn, r.pos))
		tp = r.via
	}
	return strings.Join(parts, "; ")
}

func orName(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// pendingZero is a map index or receive expression whose single-value
// form would produce the zero value of its type. It is checked when all
// bodies are checked, unless it was used in a comma-ok assignment
// (type.zero.map-index, type.zero.chan-receive), as the target of an
// assignment, or as a statement.
type pendingZero struct {
	pos  syntax.Pos
	typ  Type
	rule string
	what string
	fn   string
}

func (c *checker) addPendingZero(e syntax.Expr, t Type, rule, what string) {
	name := ""
	if c.fn != nil {
		name = c.fn.name
	}
	c.pendingZero[e] = pendingZero{e.Pos(), t, rule, what, name}
}

func (c *checker) dropPendingZero(e syntax.Expr) { delete(c.pendingZero, unparen(e)) }

func (c *checker) checkPendingZero() {
	for _, p := range c.pendingZero {
		saved := c.funcName
		c.fn = &funcContext{name: p.fn}
		c.zeroUse(p.pos, p.typ, p.rule, p.what)
		c.fn, c.funcName = nil, saved
	}
	clear(c.pendingZero)
}
