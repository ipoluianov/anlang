package types

import (
	"fmt"
	"go/constant"
	"math/big"
	"sort"
	"strings"

	"an/syntax"
	"an/verify"
	verifycheck "an/verify/check"
)

// Proving that a value fits an integer type (type.conv.int-narrow-facts)
// with the solver of stage 1: the expression becomes a linear term over
// variables with the facts of their values, and the solver proves that
// the term lies in the range of the type.

// prover translates the integer expressions of one conversion.
type prover struct {
	c *checker
	// vars maps a variable, or an expression that has no linear term, to
	// the number of its solver variable; names are the variables.
	vars  map[any]int
	names []string
	facts []verify.Formula
	// shared is set when the expression has no call, so that every use of
	// a variable has the same value.
	shared bool
}

// typeBounds returns the range of integer type t, or false.
func (c *checker) typeBounds(t Type) (lo, hi *big.Int, ok bool) {
	b, ok := core(t).(*Basic)
	if !ok || b.info&IsInteger == 0 || isUntyped(b) {
		return nil, nil, false
	}
	bits, signed := c.intRange(b)
	if signed {
		lo = new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), uint(bits-1)))
		return lo, new(big.Int).Sub(new(big.Int).Neg(lo), big.NewInt(1)), true
	}
	return new(big.Int), new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits)), big.NewInt(1)), true
}

func between(t verify.Term, lo, hi *big.Int) verify.Formula {
	return verify.AndOf(verify.Cmp(verify.ConstBig(lo), verify.LE, t), verify.Cmp(t, verify.LE, verify.ConstBig(hi)))
}

// inRange adds the fact that term t of expression e lies in the range of
// the type of e: a value of a checked integer type that is computed lies
// in it, or the operation panics before the conversion.
func (pr *prover) inRange(t verify.Term, e syntax.Expr) {
	if lo, hi, ok := pr.c.typeBounds(pr.c.info.Types[e].Type); ok {
		pr.facts = append(pr.facts, between(t, lo, hi))
	}
}

// fresh returns the variable of key, a new one with the range of the
// type of e the first time.
func (pr *prover) fresh(key any, name string, e syntax.Expr) verify.Term {
	if v, ok := pr.vars[key]; ok {
		return verify.V(v)
	}
	v := len(pr.names)
	pr.vars[key] = v
	pr.names = append(pr.names, name)
	t := verify.V(v)
	pr.inRange(t, e)
	return t
}

func (pr *prover) constant(e syntax.Expr) (*big.Int, bool) {
	tv, ok := pr.c.info.Types[e]
	if !ok || tv.Value == nil {
		return nil, false
	}
	v := constant.ToInt(tv.Value)
	if v.Kind() != constant.Int {
		return nil, false
	}
	b, ok := new(big.Int).SetString(v.ExactString(), 10)
	return b, ok
}

// interval adds a variable for e with the bounds lo and hi.
func (pr *prover) interval(e syntax.Expr, lo, hi *big.Int) verify.Term {
	t := pr.fresh(e, exprString(e), e)
	pr.facts = append(pr.facts, between(t, lo, hi))
	return t
}

// term returns the linear term of integer expression e.
func (pr *prover) term(e syntax.Expr) verify.Term {
	c := pr.c
	if k, ok := pr.constant(e); ok {
		return verify.ConstBig(k)
	}
	typ := c.info.Types[e].Type
	checked := !isWrapping(typ)
	one := big.NewInt(1)
	switch e := e.(type) {
	case *syntax.ParenExpr:
		return pr.term(e.X)
	case *syntax.Name:
		if v, ok := c.info.Uses[e].(*Var); ok {
			key := any(e)
			if pr.shared {
				key = v
			}
			t := pr.fresh(key, v.name, e)
			if n, ok := c.rangeKeys[v]; ok && n >= 1 {
				// A range key over n elements (fact 6).
				pr.facts = append(pr.facts, between(t, new(big.Int), big.NewInt(n-1)))
			}
			return t
		}
	case *syntax.UnaryExpr:
		switch {
		case e.Op == syntax.Add:
			return pr.term(e.X)
		case e.Op == syntax.Sub && checked:
			t := pr.term(e.X).Scale(big.NewInt(-1))
			pr.inRange(t, e)
			return t
		}
	case *syntax.BinaryExpr:
		switch e.Op {
		case syntax.Add, syntax.Sub:
			if !checked {
				break
			}
			// Checked arithmetic is exact: the operation panics otherwise.
			x, y := pr.term(e.X), pr.term(e.Y)
			t := x.Add(y)
			if e.Op == syntax.Sub {
				t = x.Sub(y)
			}
			pr.inRange(t, e)
			return t
		case syntax.Mul:
			if !checked {
				break
			}
			for _, ops := range [][2]syntax.Expr{{e.X, e.Y}, {e.Y, e.X}} {
				if k, ok := pr.constant(ops[0]); ok {
					t := pr.term(ops[1]).Scale(k)
					pr.inRange(t, e)
					return t
				}
			}
		case syntax.And:
			// x & c with c ≥ 0 (fact 3).
			for _, x := range []syntax.Expr{e.Y, e.X} {
				if k, ok := pr.constant(x); ok && k.Sign() >= 0 {
					return pr.interval(e, new(big.Int), k)
				}
			}
		case syntax.Rem:
			// x % c with c > 0 (fact 4).
			if k, ok := pr.constant(e.Y); ok && k.Sign() > 0 {
				top := new(big.Int).Sub(k, one)
				if isUnsigned(c.info.Types[e.X].Type) {
					return pr.interval(e, new(big.Int), top)
				}
				return pr.interval(e, new(big.Int).Neg(top), top)
			}
		case syntax.Shr:
			// x >> c with x unsigned (fact 5).
			b, isBasic := core(c.info.Types[e.X].Type).(*Basic)
			if k, ok := pr.constant(e.Y); ok && isBasic && b.info&IsUnsigned != 0 && k.IsInt64() && k.Sign() >= 0 {
				bits, _ := c.intRange(b)
				s := k.Int64()
				if s >= int64(bits) {
					return pr.interval(e, new(big.Int), new(big.Int))
				}
				return pr.interval(e, new(big.Int), new(big.Int).Sub(new(big.Int).Lsh(one, uint(int64(bits)-s)), one))
			}
		}
	case *syntax.CallExpr:
		// A conversion between integer types that the checker accepted keeps
		// the value (fact 7); one to a wrap type does not.
		if len(e.Args) == 1 && c.isConversion(e) && checked {
			if _, _, ok := c.typeBounds(c.info.Types[e.Args[0]].Type); ok && !isWrapping(c.info.Types[e.Args[0]].Type) {
				t := pr.term(e.Args[0])
				pr.inRange(t, e)
				return t
			}
		}
	}
	return pr.fresh(e, exprString(e), e)
}

// provenFits reports whether the value of integer expression e is proven
// to be representable in integer type t (type.conv.int-narrow-facts); if
// not, it returns a counterexample, values for which it does not fit.
func (c *checker) provenFits(e syntax.Expr, t Type) (bool, string) {
	lo, hi, ok := c.typeBounds(t)
	if !ok {
		return false, ""
	}
	pr := &prover{c: c, vars: map[any]int{}, shared: !hasCallOrRecv(e)}
	term := pr.term(e)
	p := &verify.Problem{Vars: pr.names, Facts: pr.facts, Goal: between(term, lo, hi)}
	r := verify.Solve(p, verify.DefaultSteps)
	switch r.Status {
	case verify.Proved:
		if err := verifycheck.Check(p, r.Proof); err != nil {
			c.errorf(e.Pos(), "contract.solver.certificate", "internal error: the checker rejects the certificate of the solver for the conversion of %s to %s: %v", exprString(e), t, err)
			return false, ""
		}
		return true, ""
	case verify.Refuted:
		var parts []string
		for i, name := range pr.names {
			if i < len(r.Model) {
				parts = append(parts, fmt.Sprintf("%s = %s", name, r.Model[i]))
			}
		}
		sort.Strings(parts)
		if len(parts) == 0 {
			return false, ""
		}
		return false, strings.Join(parts, ", ")
	}
	return false, ""
}
