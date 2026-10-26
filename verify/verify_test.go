package verify_test

import (
	"math/big"
	"math/rand"
	"testing"

	"an/verify"
	"an/verify/check"
)

func c(k int64) verify.Term { return verify.Const(k) }

func x(v int) verify.Term { return verify.V(v) }

func scale(k int64, t verify.Term) verify.Term { return t.Scale(big.NewInt(k)) }

func between(t verify.Term, lo, hi int64) verify.Formula {
	return verify.AndOf(verify.Cmp(c(lo), verify.LE, t), verify.Cmp(t, verify.LE, c(hi)))
}

// eval evaluates a formula for the values of the variables.
func eval(f verify.Formula, vals []int64) bool {
	term := func(t verify.Term) int64 {
		s := int64(0)
		if t.Const != nil {
			s = t.Const.Int64()
		}
		for _, k := range t.Coefs {
			s += k.C.Int64() * vals[k.Var]
		}
		return s
	}
	switch f.Kind {
	case verify.True:
		return true
	case verify.False:
		return false
	case verify.Not:
		return !eval(f.Args[0], vals)
	case verify.And:
		for _, a := range f.Args {
			if !eval(a, vals) {
				return false
			}
		}
		return true
	case verify.Or:
		for _, a := range f.Args {
			if eval(a, vals) {
				return true
			}
		}
		return false
	}
	l, r := term(f.L), term(f.R)
	switch f.Rel {
	case verify.LE:
		return l <= r
	case verify.LT:
		return l < r
	case verify.EQ:
		return l == r
	case verify.NE:
		return l != r
	case verify.GE:
		return l >= r
	}
	return l > r
}

func solve(t *testing.T, p *verify.Problem) verify.Result {
	t.Helper()
	r := verify.Solve(p, verify.DefaultSteps)
	if r.Status == verify.Proved {
		if err := check.Check(p, r.Proof); err != nil {
			t.Fatalf("the checker rejects the certificate of %v ⊢ %v: %v", p.Facts, p.Goal, err)
		}
	}
	if r.Status == verify.Refuted {
		vals := make([]int64, len(p.Vars))
		for i := range vals {
			if i < len(r.Model) {
				vals[i] = r.Model[i].Int64()
			}
		}
		for _, f := range p.Facts {
			if !eval(f, vals) {
				t.Fatalf("the counterexample %v of %v ⊢ %v violates the fact %v", vals, p.Facts, p.Goal, f)
			}
		}
		if eval(p.Goal, vals) {
			t.Fatalf("the counterexample %v of %v ⊢ %v satisfies the goal", vals, p.Facts, p.Goal)
		}
	}
	return r
}

func TestExamples(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts []verify.Formula
		goal  verify.Formula
		want  verify.Status
	}{
		{"range sum", []verify.Formula{between(x(0), 0, 4)}, between(x(0).Add(c(10)), 0, 65535), verify.Proved},
		{"range sum too large", []verify.Formula{between(x(0), 0, 65535)}, between(x(0).Add(c(10)), 0, 65535), verify.Refuted},
		{"lo <= hi", []verify.Formula{verify.Cmp(x(0), verify.LE, x(1)), verify.Cmp(x(1), verify.LE, x(2))}, verify.Cmp(x(0), verify.LE, x(2)), verify.Proved},
		{"parity", nil, verify.Cmp(scale(2, x(0)), verify.NE, scale(2, x(1)).Add(c(1))), verify.Proved},
		{"integers between", []verify.Formula{verify.Cmp(scale(3, x(0)), verify.GE, c(1)), verify.Cmp(scale(3, x(0)), verify.LE, c(2))}, verify.Formula{Kind: verify.False}, verify.Proved},
		{"cases", []verify.Formula{verify.OrOf(verify.Cmp(x(0), verify.EQ, c(1)), verify.Cmp(x(0), verify.EQ, c(2)))}, verify.Cmp(x(0), verify.GT, c(0)), verify.Proved},
		{"cases refuted", []verify.Formula{verify.OrOf(verify.Cmp(x(0), verify.EQ, c(1)), verify.Cmp(x(0), verify.EQ, c(-2)))}, verify.Cmp(x(0), verify.GT, c(0)), verify.Refuted},
		{"index", []verify.Formula{between(x(0), 0, 9), verify.Cmp(x(1), verify.EQ, scale(2, x(0)).Add(c(1)))}, verify.Cmp(x(1), verify.LT, c(20)), verify.Proved},
		{"branch", []verify.Formula{verify.Cmp(scale(2, x(0)).Add(scale(2, x(1))), verify.EQ, c(3))}, verify.Formula{Kind: verify.False}, verify.Proved},
		{"no integer in a thin strip", []verify.Formula{verify.Cmp(scale(3, x(0)).Sub(scale(3, x(1))), verify.GE, c(1)), verify.Cmp(scale(3, x(0)).Sub(scale(3, x(1))), verify.LE, c(2)), between(x(0), -5, 5)}, verify.Formula{Kind: verify.False}, verify.Proved},
	} {
		p := &verify.Problem{Vars: []string{"x", "y", "z"}, Facts: tc.facts, Goal: tc.goal}
		if r := solve(t, p); r.Status != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, r.Status, tc.want)
		}
	}
}

// randFormula returns a random formula over nv variables.
func randFormula(rng *rand.Rand, nv, depth int) verify.Formula {
	if depth == 0 || rng.Intn(3) == 0 {
		t := c(int64(rng.Intn(13) - 6))
		for v := 0; v < nv; v++ {
			if k := int64(rng.Intn(7) - 3); k != 0 && rng.Intn(2) == 0 {
				t = t.Add(scale(k, x(v)))
			}
		}
		return verify.Cmp(t, verify.Rel(rng.Intn(6)), c(int64(rng.Intn(5)-2)))
	}
	switch rng.Intn(3) {
	case 0:
		return verify.NotOf(randFormula(rng, nv, depth-1))
	case 1:
		return verify.AndOf(randFormula(rng, nv, depth-1), randFormula(rng, nv, depth-1))
	}
	return verify.OrOf(randFormula(rng, nv, depth-1), randFormula(rng, nv, depth-1))
}

// TestExhaustive compares the solver with the enumeration of the values
// of small domains: the obligations whose facts bound every variable to
// [-3, 3].
func TestExhaustive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	counts := map[verify.Status]int{}
	n := 3000
	if testing.Short() {
		n = 600
	}
	for i := 0; i < n; i++ {
		nv := 1 + rng.Intn(3)
		var facts []verify.Formula
		for v := 0; v < nv; v++ {
			facts = append(facts, between(x(v), -3, 3))
		}
		if rng.Intn(2) == 0 {
			facts = append(facts, randFormula(rng, nv, 2))
		}
		goal := randFormula(rng, nv, 3)
		p := &verify.Problem{Vars: []string{"x", "y", "z"}[:nv], Facts: facts, Goal: goal}
		valid := true
		vals := make([]int64, nv)
		var enumerate func(k int)
		enumerate = func(k int) {
			if !valid {
				return
			}
			if k == nv {
				all := true
				for _, f := range facts {
					all = all && eval(f, vals)
				}
				if all && !eval(goal, vals) {
					valid = false
				}
				return
			}
			for v := int64(-3); v <= 3; v++ {
				vals[k] = v
				enumerate(k + 1)
			}
		}
		enumerate(0)
		r := solve(t, p)
		counts[r.Status]++
		want := verify.Refuted
		if valid {
			want = verify.Proved
		}
		if r.Status != want {
			t.Fatalf("%v ⊢ %v: %v, want %v", facts, goal, r.Status, want)
		}
	}
	t.Logf("%d obligations: %d proved, %d refuted, %d unknown", n, counts[verify.Proved], counts[verify.Refuted], counts[verify.Unknown])
}

// TestCheckerRejects checks that the checker rejects wrong certificates.
func TestCheckerRejects(t *testing.T) {
	p := &verify.Problem{Vars: []string{"x"}, Facts: []verify.Formula{between(x(0), 0, 4)}, Goal: verify.Cmp(x(0), verify.LE, c(4))}
	r := solve(t, p)
	if r.Status != verify.Proved {
		t.Fatalf("%v", r.Status)
	}
	one := big.NewInt(1)
	// x ≤ 4 and -x ≤ -5 (the negation of the goal) give 0 ≤ -1.
	good := &verify.Proof{Kind: verify.Refute, Steps: []verify.Step{{Uses: []verify.Use{
		{Ineq: verify.Ineq{Coefs: []verify.Coef{{Var: 0, C: big.NewInt(1)}}, C: big.NewInt(4)}, Derived: -1, Mult: one},
		{Ineq: verify.Ineq{Coefs: []verify.Coef{{Var: 0, C: big.NewInt(-1)}}, C: big.NewInt(-5)}, Derived: -1, Mult: one},
	}}}}
	if err := check.Check(p, good); err != nil {
		t.Fatalf("the checker rejects a correct certificate: %v", err)
	}
	bad := []*verify.Proof{
		// 0 ≤ 0 is no contradiction.
		{Kind: verify.Refute, Steps: []verify.Step{{Uses: []verify.Use{
			{Ineq: verify.Ineq{Coefs: []verify.Coef{{Var: 0, C: big.NewInt(1)}}, C: big.NewInt(4)}, Derived: -1, Mult: one},
			{Ineq: verify.Ineq{Coefs: []verify.Coef{{Var: 0, C: big.NewInt(-1)}}, C: big.NewInt(-4)}, Derived: -1, Mult: one},
		}}}},
		// A negative multiple.
		{Kind: verify.Refute, Steps: []verify.Step{{Uses: []verify.Use{
			{Ineq: verify.Ineq{Coefs: []verify.Coef{{Var: 0, C: big.NewInt(1)}}, C: big.NewInt(4)}, Derived: -1, Mult: big.NewInt(-1)},
		}}}},
		// An inequality that is not in the conjunction.
		{Kind: verify.Refute, Steps: []verify.Step{{Uses: []verify.Use{
			{Ineq: verify.Ineq{C: big.NewInt(-1)}, Derived: -1, Mult: one},
		}}}},
		// No steps.
		{Kind: verify.Refute},
		// A split of a formula that is not there.
		{Kind: verify.Split, Of: verify.NF{Kind: verify.NOr}},
		// A branch with one case.
		{Kind: verify.Branch, Term: []verify.Coef{{Var: 0, C: one}}, K: big.NewInt(2), Cases: []*verify.Proof{good}},
	}
	for i, b := range bad {
		if err := check.Check(p, b); err == nil {
			t.Errorf("the checker accepts wrong certificate %d", i)
		}
	}
	// A certificate of another problem.
	q := &verify.Problem{Vars: []string{"x"}, Facts: []verify.Formula{between(x(0), 0, 5)}, Goal: verify.Cmp(x(0), verify.LE, c(4))}
	if err := check.Check(q, r.Proof); err == nil {
		t.Errorf("the checker accepts the certificate of another problem")
	}
}

// TestExhaustiveWide does the same for four variables in [-2, 2].
func TestExhaustiveWide(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	n := 1500
	if testing.Short() {
		n = 300
	}
	for i := 0; i < n; i++ {
		const nv = 4
		var facts []verify.Formula
		for v := 0; v < nv; v++ {
			facts = append(facts, between(x(v), -2, 2))
		}
		facts = append(facts, randFormula(rng, nv, 2))
		goal := randFormula(rng, nv, 3)
		p := &verify.Problem{Vars: []string{"a", "b", "c", "d"}, Facts: facts, Goal: goal}
		valid := true
		vals := make([]int64, nv)
		for code := 0; code < 625 && valid; code++ {
			k := code
			for v := 0; v < nv; v++ {
				vals[v] = int64(k%5 - 2)
				k /= 5
			}
			all := true
			for _, f := range facts {
				all = all && eval(f, vals)
			}
			if all && !eval(goal, vals) {
				valid = false
			}
		}
		r := solve(t, p)
		if want := map[bool]verify.Status{true: verify.Proved, false: verify.Refuted}[valid]; r.Status != want {
			t.Fatalf("%v ⊢ %v: %v, want %v", facts, goal, r.Status, want)
		}
	}
}

// TestUnbounded solves obligations over all the integers: a proof must
// pass the checker and a counterexample must violate the goal.
func TestUnbounded(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	counts := map[verify.Status]int{}
	for i := 0; i < 2000; i++ {
		nv := 1 + rng.Intn(3)
		p := &verify.Problem{Vars: []string{"x", "y", "z"}[:nv], Goal: randFormula(rng, nv, 3)}
		if rng.Intn(2) == 0 {
			p.Facts = []verify.Formula{randFormula(rng, nv, 2)}
		}
		counts[solve(t, p).Status]++
	}
	t.Logf("%d proved, %d refuted, %d unknown", counts[verify.Proved], counts[verify.Refuted], counts[verify.Unknown])
}

// TestDeterminism checks that the result depends only on the problem
// and the limit (contract.solver.determinism).
func TestDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 200; i++ {
		p := &verify.Problem{Vars: []string{"x", "y", "z"}, Facts: []verify.Formula{randFormula(rng, 3, 2)}, Goal: randFormula(rng, 3, 3)}
		a, b := verify.Solve(p, verify.DefaultSteps), verify.Solve(p, verify.DefaultSteps)
		if a.Status != b.Status || a.Steps != b.Steps {
			t.Fatalf("%v ⊢ %v: %v in %d steps, then %v in %d", p.Facts, p.Goal, a.Status, a.Steps, b.Status, b.Steps)
		}
		if lim := verify.Solve(p, 1); lim.Status != verify.Unknown && lim.Steps > 1 {
			t.Fatalf("the step limit 1 is exceeded: %d steps", lim.Steps)
		}
	}
}
