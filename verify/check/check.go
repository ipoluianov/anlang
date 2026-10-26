// Package check is the independent checker of the certificates of the
// solver (contract.solver.certificate). It shares only the data types of
// package verify: it computes the normal form of the obligation itself
// and checks every step of a certificate with exact integer arithmetic.
package check

import (
	"fmt"
	"math/big"
	"sort"

	"an/verify"
)

// Check verifies that proof refutes the facts of p together with the
// negation of its goal, so that the goal holds under the facts for every
// integer value of the variables.
func Check(p *verify.Problem, proof *verify.Proof) error {
	st := &state{ineqs: map[string]verify.Ineq{}, other: map[string]verify.NF{}}
	for _, f := range p.Facts {
		st.add(nf(f, true))
	}
	st.add(nf(p.Goal, false))
	return check(st, proof, 0)
}

// The normal form (contract.solver.normal-form).

func sub(l, r verify.Term) ([]verify.Coef, *big.Int) {
	m := map[int]*big.Int{}
	for _, c := range l.Coefs {
		m[c.Var] = new(big.Int).Set(c.C)
	}
	for _, c := range r.Coefs {
		x, ok := m[c.Var]
		if !ok {
			x = new(big.Int)
		}
		m[c.Var] = x.Sub(x, c.C)
	}
	k := new(big.Int)
	if l.Const != nil {
		k.Add(k, l.Const)
	}
	if r.Const != nil {
		k.Sub(k, r.Const)
	}
	return canonical(m), k
}

func canonical(m map[int]*big.Int) []verify.Coef {
	var cs []verify.Coef
	for v, c := range m {
		if c.Sign() != 0 {
			cs = append(cs, verify.Coef{Var: v, C: c})
		}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Var < cs[j].Var })
	return cs
}

// le returns the normal form of t ≤ d for t = cs + k: cs ≤ d - k.
func le(cs []verify.Coef, k *big.Int, d int64) verify.NF {
	c := new(big.Int).Sub(big.NewInt(d), k)
	return verify.NF{Kind: verify.NIneq, Ineq: verify.Ineq{Coefs: cs, C: c}}
}

func nf(f verify.Formula, pos bool) verify.NF {
	switch f.Kind {
	case verify.True:
		if pos {
			return verify.NF{Kind: verify.NTrue}
		}
		return verify.NF{Kind: verify.NFalse}
	case verify.False:
		if pos {
			return verify.NF{Kind: verify.NFalse}
		}
		return verify.NF{Kind: verify.NTrue}
	case verify.Not:
		return nf(f.Args[0], !pos)
	case verify.And, verify.Or:
		conj := f.Kind == verify.And
		if !pos {
			conj = !conj
		}
		n := verify.NF{Kind: verify.NOr}
		if conj {
			n.Kind = verify.NAnd
		}
		for _, a := range f.Args {
			n.Args = append(n.Args, nf(a, pos))
		}
		return n
	}
	rel := f.Rel
	if !pos {
		switch rel {
		case verify.LE:
			rel = verify.GT
		case verify.LT:
			rel = verify.GE
		case verify.EQ:
			rel = verify.NE
		case verify.NE:
			rel = verify.EQ
		case verify.GE:
			rel = verify.LT
		case verify.GT:
			rel = verify.LE
		}
	}
	a, ka := sub(f.L, f.R) // L - R
	b, kb := sub(f.R, f.L) // R - L
	switch rel {
	case verify.LE:
		return le(a, ka, 0)
	case verify.LT:
		return le(a, ka, -1)
	case verify.GE:
		return le(b, kb, 0)
	case verify.GT:
		return le(b, kb, -1)
	case verify.EQ:
		return verify.NF{Kind: verify.NAnd, Args: []verify.NF{le(a, ka, 0), le(b, kb, 0)}}
	}
	return verify.NF{Kind: verify.NOr, Args: []verify.NF{le(a, ka, -1), le(b, kb, -1)}}
}

// state is a conjunction: inequalities and disjunctions by their keys,
// and whether it contains false.
type state struct {
	ineqs map[string]verify.Ineq
	other map[string]verify.NF
	false bool
}

func (s *state) add(n verify.NF) {
	switch n.Kind {
	case verify.NTrue:
	case verify.NFalse:
		s.false = true
	case verify.NIneq:
		s.ineqs[n.Ineq.Key()] = n.Ineq
	case verify.NAnd:
		for _, a := range n.Args {
			s.add(a)
		}
	case verify.NOr:
		s.other[n.Key()] = n
	}
}

func (s *state) clone() *state {
	c := &state{ineqs: map[string]verify.Ineq{}, other: map[string]verify.NF{}, false: s.false}
	for k, v := range s.ineqs {
		c.ineqs[k] = v
	}
	for k, v := range s.other {
		c.other[k] = v
	}
	return c
}

const maxDepth = 100000

func check(s *state, p *verify.Proof, depth int) error {
	if p == nil {
		return fmt.Errorf("missing proof")
	}
	if depth > maxDepth {
		return fmt.Errorf("the proof is too deep")
	}
	switch p.Kind {
	case verify.Split:
		key := p.Of.Key()
		or, ok := s.other[key]
		if !ok || or.Kind != verify.NOr {
			return fmt.Errorf("split of %s, which is not a disjunction of the conjunction", key)
		}
		if len(p.Cases) != len(or.Args) {
			return fmt.Errorf("split of %s with %d cases for %d disjuncts", key, len(p.Cases), len(or.Args))
		}
		for i, d := range or.Args {
			// The disjunction stays in the conjunction, which still
			// contains it.
			c := s.clone()
			c.add(d)
			if err := check(c, p.Cases[i], depth+1); err != nil {
				return err
			}
		}
		return nil
	case verify.Branch:
		if len(p.Cases) != 2 || p.K == nil {
			return fmt.Errorf("a branch needs two cases and a bound")
		}
		m := map[int]*big.Int{}
		for _, c := range p.Term {
			x, ok := m[c.Var]
			if !ok {
				x = new(big.Int)
			}
			m[c.Var] = x.Add(x, c.C)
		}
		term := canonical(m)
		var neg []verify.Coef
		for _, c := range term {
			neg = append(neg, verify.Coef{Var: c.Var, C: new(big.Int).Neg(c.C)})
		}
		cases := []verify.Ineq{
			{Coefs: term, C: new(big.Int).Set(p.K)},                                 // term ≤ K
			{Coefs: neg, C: new(big.Int).Neg(new(big.Int).Add(p.K, big.NewInt(1)))}, // term ≥ K+1
		}
		for i, q := range cases {
			c := s.clone()
			c.ineqs[q.Key()] = q
			if err := check(c, p.Cases[i], depth+1); err != nil {
				return err
			}
		}
		return nil
	case verify.Refute:
		return refute(s, p.Steps)
	}
	return fmt.Errorf("unknown proof kind %d", p.Kind)
}

// refute checks that the steps derive 0 ≤ c with c < 0 from the
// inequalities of the conjunction.
func refute(s *state, steps []verify.Step) error {
	if len(steps) == 0 {
		if s.false {
			return nil
		}
		return fmt.Errorf("a refutation without steps of a conjunction without false")
	}
	var derived []verify.Ineq
	for i, st := range steps {
		if len(st.Uses) == 0 {
			return fmt.Errorf("step %d uses nothing", i)
		}
		sum := map[int]*big.Int{}
		c := new(big.Int)
		for _, u := range st.Uses {
			if u.Mult == nil || u.Mult.Sign() <= 0 {
				return fmt.Errorf("step %d: a multiple that is not positive", i)
			}
			var q verify.Ineq
			switch {
			case u.Derived >= 0 && u.Derived < i:
				q = derived[u.Derived]
			case u.Derived >= 0:
				return fmt.Errorf("step %d uses step %d", i, u.Derived)
			default:
				have, ok := s.ineqs[u.Ineq.Key()]
				if !ok {
					return fmt.Errorf("step %d uses %s, which is not in the conjunction", i, u.Ineq.Key())
				}
				q = have
			}
			for _, k := range q.Coefs {
				x, ok := sum[k.Var]
				if !ok {
					x = new(big.Int)
				}
				sum[k.Var] = x.Add(x, new(big.Int).Mul(u.Mult, k.C))
			}
			c.Add(c, new(big.Int).Mul(u.Mult, q.C))
		}
		cs := canonical(sum)
		if st.Tighten {
			g := new(big.Int)
			for _, k := range cs {
				g.GCD(nil, nil, g, new(big.Int).Abs(k.C))
			}
			if g.Sign() == 0 {
				return fmt.Errorf("step %d tightens an inequality without variables", i)
			}
			for j := range cs {
				cs[j].C = new(big.Int).Quo(cs[j].C, g)
			}
			c.Div(c, g) // ⌊c/g⌋, g > 0
		}
		derived = append(derived, verify.Ineq{Coefs: cs, C: c})
	}
	last := derived[len(derived)-1]
	if len(last.Coefs) != 0 || last.C.Sign() >= 0 {
		return fmt.Errorf("the last step derives %s, not 0 ≤ c with c < 0", last.Key())
	}
	return nil
}
