package verify_test

import (
	"fmt"
	"math/rand"
	"testing"

	"an/verify"
)

// The bounds of the guaranteed fragment of edition 1.0
// (contract.solver.guaranteed).
const (
	fragmentVars   = 16
	fragmentIneqs  = 64
	fragmentDisjns = 2
)

// fragmentProblem returns an obligation of the guaranteed fragment with
// nv variables and at most ni inequalities: each inequality has at most
// two variables with coefficients ±1, the facts hold for a hidden value of
// the variables, and the goal follows from a chain of the facts over the
// rationals, through a case split where a link of the chain is a
// disjunction. unary gives facts that bound one variable.
func fragmentProblem(r *rand.Rand, nv, ni int, unary bool) *verify.Problem {
	val := make([]int64, nv)
	for i := range val {
		val[i] = r.Int63n(201) - 100
	}
	// lin is a·x + b·y, with n variables, and its value.
	type lin struct {
		v    [2]int
		k    [2]int64
		n    int
		eval int64
	}
	sign := func() int64 { return int64(1 - 2*r.Intn(2)) }
	mk := func(vs ...int) lin {
		l := lin{n: len(vs)}
		for i, v := range vs {
			l.v[i], l.k[i] = v, sign()
			l.eval += l.k[i] * val[v]
		}
		return l
	}
	random := func() lin {
		a, b := r.Intn(nv), r.Intn(nv)
		if unary && r.Intn(3) == 0 || a == b {
			return mk(a)
		}
		return mk(a, b)
	}
	term := func(l lin) verify.Term {
		t := c(0)
		for i := 0; i < l.n; i++ {
			t = t.Add(scale(l.k[i], x(l.v[i])))
		}
		return t
	}
	le := func(l lin, k int64) verify.Formula { return verify.Cmp(term(l), verify.LE, c(k)) }
	var facts []verify.Formula
	ineqs, disjns := 0, 0
	goalNE := r.Intn(3) == 0
	if goalNE {
		ineqs, disjns = 2, 1
	} else {
		ineqs = 1
	}
	// link adds l ≤ k, as a disjunction where the budget allows: l ≤ k,
	// or l ≤ k - 1 with another fact, so that both cases give l ≤ k.
	link := func(l lin, k int64) {
		if disjns < fragmentDisjns && ineqs+3 <= ni && r.Intn(4) == 0 {
			o := random()
			facts = append(facts, verify.OrOf(le(l, k), verify.AndOf(le(l, k-1), le(o, o.eval+r.Int63n(20)))))
			ineqs, disjns = ineqs+3, disjns+1
			return
		}
		facts = append(facts, le(l, k))
		ineqs++
	}
	// The chain: x₀ … x_k with each fact cancelling the variable before.
	k := 1 + r.Intn(min(nv-1, ni-ineqs))
	perm := r.Perm(nv)[:k+1]
	sum := mk(perm[0], perm[1])
	cst := sum.eval
	link(sum, sum.eval)
	for i := 1; i < k; i++ {
		l := mk(perm[i], perm[i+1])
		l.k[0] = -sum.k[1]
		l.eval = l.k[0]*val[perm[i]] + l.k[1]*val[perm[i+1]]
		slack := r.Int63n(3)
		link(l, l.eval+slack)
		cst += l.eval + slack
		sum.v[1], sum.k[1] = perm[i+1], l.k[1]
	}
	// Noise: other facts that the hidden value satisfies, and
	// disjunctions of which one disjunct holds for it.
	for ineqs < ni {
		a := random()
		f := le(a, a.eval+r.Int63n(20))
		if disjns < fragmentDisjns && ineqs+2 <= ni && r.Intn(8) == 0 {
			b := random()
			f = verify.OrOf(f, le(b, b.eval-1-r.Int63n(20)))
			ineqs, disjns = ineqs+1, disjns+1
		}
		facts = append(facts, f)
		ineqs++
	}
	r.Shuffle(len(facts), func(i, j int) { facts[i], facts[j] = facts[j], facts[i] })
	var goal verify.Formula
	t := term(sum)
	switch {
	case goalNE:
		// The negation is a disjunction of two inequalities.
		goal = verify.NotOf(verify.Cmp(t, verify.EQ, c(cst+1+r.Int63n(5))))
	case r.Intn(2) == 0:
		goal = verify.Cmp(t, verify.LE, c(cst))
	default:
		goal = verify.Cmp(t, verify.LT, c(cst+1))
	}
	names := make([]string, nv)
	for i := range names {
		names[i] = fmt.Sprintf("x%d", i)
	}
	return &verify.Problem{Vars: names, Facts: facts, Goal: goal}
}

// TestGuaranteedFragment checks that the solver proves the obligations of
// the guaranteed fragment within the step limit of the edition
// (contract.solver.guaranteed), half of them at the bounds of the
// fragment.
func TestGuaranteedFragment(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	n := 2000
	if testing.Short() {
		n = 200
	}
	most := 0
	for i := 0; i < n; i++ {
		nv, ni := fragmentVars, fragmentIneqs
		if i%2 == 1 {
			nv, ni = 2+r.Intn(fragmentVars-1), 1+r.Intn(fragmentIneqs)
		}
		p := fragmentProblem(r, nv, max(ni, 4), r.Intn(2) == 0)
		res := verify.Solve(p, verify.DefaultSteps)
		if res.Status != verify.Proved {
			t.Fatalf("problem %d: %s after %d steps:\nfacts %v\ngoal %v", i, res.Status, res.Steps, p.Facts, p.Goal)
		}
		most = max(most, res.Steps)
	}
	t.Logf("at most %d steps", most)
}

// TestFixedBounds keeps a fault of the substitution of the variables
// that bounds propagation fixes: a bound that the substitution of another
// variable derived was dropped, though the value of its variable violated
// it, and the solver returned a false counterexample.
func TestFixedBounds(t *testing.T) {
	le := func(t verify.Term, k int64) verify.Formula { return verify.Cmp(t, verify.LE, c(k)) }
	facts := []verify.Formula{
		le(scale(-1, x(1)).Sub(x(2)), -117),
		le(x(0).Add(x(4)), 73),
		le(scale(-1, x(2)), -4),
		le(scale(-1, x(0)).Add(x(2)), 35),
		le(scale(-1, x(0)).Sub(x(3)), 110),
		le(x(2), 31),
		le(scale(-1, x(2)).Sub(x(4)), -87),
		le(scale(-1, x(0)), 32),
		le(x(0), -2),
		le(scale(-1, x(1)).Sub(x(4)), -171),
	}
	goal := verify.Cmp(x(4).Sub(x(1)), verify.LT, c(-8))
	p := &verify.Problem{Vars: []string{"x0", "x1", "x2", "x3", "x4"}, Facts: facts, Goal: goal}
	if res := verify.Solve(p, verify.DefaultSteps); res.Status != verify.Proved {
		t.Errorf("%s %v, want proved", res.Status, res.Model)
	}
}
