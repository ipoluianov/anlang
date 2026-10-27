package verify

import (
	"math/big"
	"sort"
)

// DefaultSteps is the default step limit of the solver.
const DefaultSteps = 200000

// The normal form (contract.solver.normal-form). The checker of package
// verify/check implements the same definition on its own.

// ineq returns the inequality t ≤ 0 shifted by d: Σ a·x ≤ -c + d for
// t = Σ a·x + c.
func ineq(t Term, d int64) Ineq {
	c := new(big.Int).Neg(t.constant())
	c.Add(c, big.NewInt(d))
	return Ineq{Coefs: t.Coefs, C: c}
}

// normalize returns the normal form of f if positive, of its negation
// otherwise.
func normalize(f Formula, positive bool) NF {
	switch f.Kind {
	case True, False:
		if (f.Kind == True) == positive {
			return NF{Kind: NTrue}
		}
		return NF{Kind: NFalse}
	case Not:
		return normalize(f.Args[0], !positive)
	case And, Or:
		k := NAnd
		if (f.Kind == Or) == positive {
			k = NOr
		}
		n := NF{Kind: k}
		for _, a := range f.Args {
			n.Args = append(n.Args, normalize(a, positive))
		}
		return n
	}
	rel := f.Rel
	if !positive {
		rel = [...]Rel{GT, GE, NE, EQ, LT, LE}[rel]
	}
	lr, rl := f.L.Sub(f.R), f.R.Sub(f.L)
	le := func(t Term, d int64) NF { return NF{Kind: NIneq, Ineq: ineq(t, d)} }
	switch rel {
	case LE:
		return le(lr, 0)
	case LT:
		return le(lr, -1)
	case GE:
		return le(rl, 0)
	case GT:
		return le(rl, -1)
	case EQ:
		return NF{Kind: NAnd, Args: []NF{le(lr, 0), le(rl, 0)}}
	}
	return NF{Kind: NOr, Args: []NF{le(lr, -1), le(rl, -1)}}
}

// Normalize returns the conjunction that a proof of p refutes: the normal
// forms of the facts and of the negation of the goal.
func Normalize(p *Problem) NF {
	n := NF{Kind: NAnd}
	for _, f := range p.Facts {
		n.Args = append(n.Args, normalize(f, true))
	}
	n.Args = append(n.Args, normalize(p.Goal, false))
	return n
}

// flatten appends the conjuncts of n to the inequalities and the other
// formulas of a conjunction.
func flatten(n NF, ineqs []Ineq, other []NF) ([]Ineq, []NF) {
	switch n.Kind {
	case NTrue:
	case NAnd:
		for _, a := range n.Args {
			ineqs, other = flatten(a, ineqs, other)
		}
	case NIneq:
		ineqs = append(ineqs, n.Ineq)
	default:
		other = append(other, n)
	}
	return ineqs, other
}

// ---------------------------------------------------------------------
// Search.

type solver struct {
	nvars  int
	limit  int
	steps  int
	branch int // the depth of the branches on variables
}

// maxBranch bounds the depth of the branches on the value of a variable:
// on unbounded integers they may not end.
const maxBranch = 64

// outcome is the result of a search: unknown ends the whole search at
// the step limit, deep only the branch that is too deep, after which the
// other branches may still find a model.
type outcome int

const (
	unsat outcome = iota
	sat
	unknown
	deep
)

// Solve decides problem p within the step limit.
func Solve(p *Problem, limit int) Result {
	s := &solver{nvars: len(p.Vars), limit: limit}
	for _, f := range append(append([]Formula(nil), p.Facts...), p.Goal) {
		s.countVars(f)
	}
	ineqs, other := flatten(Normalize(p), nil, nil)
	proof, model, out := s.search(ineqs, other)
	r := Result{Steps: s.steps}
	switch out {
	case unsat:
		r.Status, r.Proof = Proved, proof
	case sat:
		r.Status = Refuted
		for _, v := range model {
			r.Model = append(r.Model, new(big.Int).Set(v.Num()))
		}
	default:
		r.Status = Unknown
	}
	return r
}

// cases searches the cases of a split or a branch: a model of one of them
// is a model, and all of them must be refuted for a proof.
//
// intro returns the keys of the inequalities and disjunctions that case i
// adds: a refutation of the case that uses none of them refutes the
// conjunction without the case, and the other cases are not searched.
func (s *solver) cases(p *Proof, n int, intro func(i int) map[string]bool, search func(i int) (*Proof, []*big.Rat, outcome)) (*Proof, []*big.Rat, outcome) {
	result := unsat
	for i := 0; i < n; i++ {
		c, m, out := search(i)
		switch out {
		case sat, unknown:
			return nil, m, out
		case deep:
			result = deep
		case unsat:
			if !c.uses(intro(i)) {
				return c, nil, unsat
			}
		}
		p.Cases = append(p.Cases, c)
	}
	if result != unsat {
		return nil, nil, result
	}
	return p, nil, unsat
}

func (s *solver) countVars(f Formula) {
	for _, t := range []Term{f.L, f.R} {
		for _, c := range t.Coefs {
			if c.Var >= s.nvars {
				s.nvars = c.Var + 1
			}
		}
	}
	for _, a := range f.Args {
		s.countVars(a)
	}
}

// search refutes the conjunction of ineqs and other, or finds a model of
// it.
func (s *solver) search(ineqs []Ineq, other []NF) (*Proof, []*big.Rat, outcome) {
	s.steps++
	if s.steps > s.limit {
		return nil, nil, unknown
	}
	for _, o := range other {
		if o.Kind == NFalse {
			return &Proof{Kind: Refute}, nil, unsat
		}
	}
	steps, model, out := s.theory(ineqs)
	switch out {
	case unknown:
		return nil, nil, unknown
	case unsat:
		return &Proof{Kind: Refute, Steps: steps}, nil, unsat
	}
	// A model that violates an inequality would be a false
	// counterexample: the obligation stays unknown.
	for _, q := range ineqs {
		if !holds(NF{Kind: NIneq, Ineq: q}, model) {
			return nil, nil, unknown
		}
	}
	// A disjunction that one of the inequalities satisfies is satisfied by
	// every model. Of the others, the one with the fewest disjuncts that
	// the model of the inequalities violates is split; if the model
	// satisfies them all, only its values that are not integers remain.
	have := map[string]bool{}
	for _, q := range ineqs {
		have[q.Key()] = true
	}
	pick := -1
	var keep []int
	seen := map[string]bool{}
	for i := range other {
		o := other[i]
		if k := o.Key(); seen[k] {
			continue // the same disjunction again
		} else {
			seen[k] = true
		}
		satisfied := false
		for _, a := range o.Args {
			if a.Kind == NTrue || a.Kind == NIneq && have[a.Ineq.Key()] {
				satisfied = true
			}
		}
		if satisfied {
			continue
		}
		keep = append(keep, i)
	}
	// Of the disjunctions that the model violates, the one with the
	// fewest disjuncts that bounds propagation does not refute is split:
	// one without such disjuncts closes the search, one with a single one
	// adds it to every branch.
	live := -1
	for _, i := range keep {
		o := other[i]
		if holds(o, model) {
			continue
		}
		n := 0
		for _, a := range o.Args {
			if !s.refutedByBounds(ineqs, a) {
				n++
			}
		}
		if s.steps > s.limit {
			return nil, nil, unknown
		}
		if pick < 0 || n < live || n == live && len(o.Args) < len(other[pick].Args) {
			pick, live = i, n
		}
		if live == 0 {
			break
		}
	}
	var or *NF
	var rest []NF
	for _, i := range keep {
		if i == pick {
			or = &other[i]
		} else {
			rest = append(rest, other[i])
		}
	}
	if or == nil {
		other = rest
		// The inequalities alone: a model with integer values, or a branch
		// on a variable whose value is not an integer.
		for v, x := range model {
			if x.IsInt() {
				continue
			}
			k := new(big.Int).Div(x.Num(), x.Denom()) // ⌊x⌋
			left := Ineq{Coefs: []Coef{{v, big.NewInt(1)}}, C: k}
			right := Ineq{Coefs: []Coef{{v, big.NewInt(-1)}}, C: new(big.Int).Neg(new(big.Int).Add(k, one))}
			if s.branch >= maxBranch {
				return nil, nil, deep
			}
			s.branch++
			defer func() { s.branch-- }()
			qs := []Ineq{left, right}
			intro := func(i int) map[string]bool { return map[string]bool{qs[i].Key(): true} }
			return s.cases(&Proof{Kind: Branch, Term: left.Coefs, K: k}, 2, intro, func(i int) (*Proof, []*big.Rat, outcome) {
				return s.search(append(append([]Ineq(nil), ineqs...), qs[i]), other)
			})
		}
		return nil, model, sat
	}
	intro := func(i int) map[string]bool {
		keys := map[string]bool{}
		qs, os := flatten(or.Args[i], nil, nil)
		for _, q := range qs {
			keys[q.Key()] = true
		}
		for _, o := range os {
			keys[o.Key()] = true
		}
		return keys
	}
	return s.cases(&Proof{Kind: Split, Of: *or}, len(or.Args), intro, func(i int) (*Proof, []*big.Rat, outcome) {
		qs, os := flatten(or.Args[i], append([]Ineq(nil), ineqs...), append([]NF(nil), rest...))
		return s.search(qs, os)
	})
}

// refutedByBounds reports whether bounds propagation refutes the
// inequalities with the formula d; its steps count.
func (s *solver) refutedByBounds(ineqs []Ineq, d NF) bool {
	if size(d) > maxCheck {
		return false
	}
	qs, os := flatten(d, append([]Ineq(nil), ineqs...), nil)
	for _, o := range os {
		if o.Kind == NFalse {
			return true
		}
	}
	t := &solver{nvars: s.nvars, limit: boundsSteps}
	var rows []row
	var derivs []derivation
	found, contradiction := false, 0
	for i, q := range qs {
		r := row{a: q.Coefs, c: new(big.Int).Set(q.C), src: -1 - i}
		g := new(big.Int)
		for _, x := range r.a {
			g.GCD(nil, nil, g, new(big.Int).Abs(x.C))
		}
		switch {
		case g.Sign() == 0:
			if r.c.Sign() < 0 {
				return true
			}
			continue
		case g.Cmp(one) > 0:
			a := make([]Coef, len(r.a))
			for j, x := range r.a {
				a[j] = Coef{x.Var, new(big.Int).Quo(x.C, g)}
			}
			r.a, r.c = a, new(big.Int).Div(r.c, g)
		}
		rows = append(rows, r)
	}
	t.propagate(&rows, &derivs, &found, &contradiction)
	s.steps += t.steps
	return found
}

// maxCheck bounds the formulas that refutedByBounds checks.
const maxCheck = 4

// size returns the number of inequalities of n.
func size(n NF) int {
	if n.Kind == NIneq {
		return 1
	}
	k := 0
	for _, a := range n.Args {
		k += size(a)
	}
	return k
}

// boundsSteps bounds the steps of refutedByBounds.
const boundsSteps = 2000

// uses reports whether proof p uses one of the inequalities or the
// disjunctions of keys, or refutes a conjunction by its false.
func (p *Proof) uses(keys map[string]bool) bool {
	switch p.Kind {
	case Refute:
		if len(p.Steps) == 0 {
			return true
		}
		for _, st := range p.Steps {
			for _, u := range st.Uses {
				if u.Derived < 0 && keys[u.Ineq.Key()] {
					return true
				}
			}
		}
	case Split:
		if keys[p.Of.Key()] {
			return true
		}
	}
	for _, c := range p.Cases {
		if c.uses(keys) {
			return true
		}
	}
	return false
}

// holds reports whether the rational model m satisfies n.
func holds(n NF, m []*big.Rat) bool {
	switch n.Kind {
	case NTrue:
		return true
	case NFalse:
		return false
	case NIneq:
		sum := new(big.Rat)
		for _, c := range n.Ineq.Coefs {
			if c.Var < len(m) && m[c.Var] != nil {
				sum.Add(sum, new(big.Rat).Mul(new(big.Rat).SetInt(c.C), m[c.Var]))
			}
		}
		return sum.Cmp(new(big.Rat).SetInt(n.Ineq.C)) <= 0
	case NAnd:
		for _, a := range n.Args {
			if !holds(a, m) {
				return false
			}
		}
		return true
	case NOr:
		for _, a := range n.Args {
			if holds(a, m) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------
// Fourier-Motzkin elimination over the integers, with bounds propagation.

// row is an inequality Σ a·x ≤ c, with its coefficients sorted by
// variable, and the step that derives it: an index of steps, or -1-i for
// inequality i of the conjunction.
type row struct {
	a   []Coef
	c   *big.Int
	src int
}

// coef returns the coefficient of v in r, or nil.
func (r row) coef(v int) *big.Int {
	i := sort.Search(len(r.a), func(i int) bool { return r.a[i].Var >= v })
	if i < len(r.a) && r.a[i].Var == v {
		return r.a[i].C
	}
	return nil
}

// sign returns the sign of the coefficient of v in r.
func (r row) sign(v int) int {
	if k := r.coef(v); k != nil {
		return k.Sign()
	}
	return 0
}

// sum returns mp·p + mn·q.
func sum(mp *big.Int, p []Coef, mn *big.Int, q []Coef) []Coef {
	var out []Coef
	i, j := 0, 0
	for i < len(p) || j < len(q) {
		switch {
		case j >= len(q) || i < len(p) && p[i].Var < q[j].Var:
			out = append(out, Coef{p[i].Var, new(big.Int).Mul(mp, p[i].C)})
			i++
		case i >= len(p) || q[j].Var < p[i].Var:
			out = append(out, Coef{q[j].Var, new(big.Int).Mul(mn, q[j].C)})
			j++
		default:
			x := new(big.Int).Add(new(big.Int).Mul(mp, p[i].C), new(big.Int).Mul(mn, q[j].C))
			if x.Sign() != 0 {
				out = append(out, Coef{p[i].Var, x})
			}
			i++
			j++
		}
	}
	return out
}

type stage struct {
	v    int
	rows []row // the rows that contain v before its elimination
}

type derivation struct {
	uses    [][2]any // source int, multiple *big.Int
	tighten bool
}

// theory decides the conjunction of inequalities: it returns the steps of
// a refutation, or a rational model.
func (s *solver) theory(ineqs []Ineq) ([]Step, []*big.Rat, outcome) {
	var derivs []derivation
	var rows []row
	found, contradiction := false, 0
	add := func(r row) {
		// Tighten: divide by the greatest common divisor of the
		// coefficients, rounding the constant down.
		g := new(big.Int)
		for _, x := range r.a {
			g.GCD(nil, nil, g, new(big.Int).Abs(x.C))
		}
		if g.Cmp(one) > 0 {
			a := make([]Coef, len(r.a))
			for i, x := range r.a {
				a[i] = Coef{x.Var, new(big.Int).Quo(x.C, g)}
			}
			r.a = a
			r.c = new(big.Int).Div(r.c, g) // Euclidean: ⌊c/g⌋ for g > 0
			derivs = append(derivs, derivation{uses: [][2]any{{r.src, big.NewInt(1)}}, tighten: true})
			r.src = len(derivs) - 1
		}
		if g.Sign() == 0 {
			if r.c.Sign() < 0 && !found {
				found, contradiction = true, r.src
			}
			return // 0 ≤ c
		}
		rows = append(rows, r)
	}
	for i, q := range ineqs {
		add(row{a: q.Coefs, c: new(big.Int).Set(q.C), src: -1 - i})
	}
	var stages []stage
	eliminated := make([]bool, s.nvars)
	if !found {
		// Bounds propagation: the bounds of a variable from each row and the
		// bounds of the others, which fixes many variables quickly.
		fixed, ok := s.propagate(&rows, &derivs, &found, &contradiction)
		if !ok {
			return nil, nil, unknown
		}
		// A variable fixed by its bounds is substituted in the other rows.
		for _, fx := range fixed {
			if found {
				break
			}
			v := fx.v
			eliminated[v] = true
			stages = append(stages, stage{v: v, rows: []row{fx.hi, fx.lo}})
			old := rows
			rows = nil
			for _, r := range old {
				switch k := r.coef(v); {
				case found:
				case k == nil:
					rows = append(rows, r)
				case isBound(r, v) && fx.satisfies(r):
					// A bound that the value of v satisfies. One that it
					// does not, which the substitution of a variable before
					// may derive, gives a contradiction below.
				default:
					s.steps++
					if s.steps > s.limit {
						return nil, nil, unknown
					}
					// Add |a|·(the bound) that cancels v.
					bnd := fx.lo
					if k.Sign() < 0 {
						bnd = fx.hi
					}
					m := new(big.Int).Abs(k)
					c := new(big.Int).Add(r.c, new(big.Int).Mul(m, bnd.c))
					derivs = append(derivs, derivation{uses: [][2]any{{r.src, big.NewInt(1)}, {bnd.src, m}}})
					add(row{a: sum(one, r.a, m, bnd.a), c: c, src: len(derivs) - 1})
				}
			}
		}
	}
	for !found {
		rows = dedupe(rows)
		// A variable with the coefficient ±1 in an equality, the pair of
		// rows e ≤ c and -e ≤ -c, is eliminated by the equality alone:
		// each other row is combined with the half of the equality that
		// cancels the variable, which substitutes it.
		if v, ep, en, ok := s.equality(rows, eliminated); ok {
			eliminated[v] = true
			stages = append(stages, stage{v: v, rows: []row{rows[ep], rows[en]}})
			old := rows
			rows = nil
			for i, r := range old {
				k := r.coef(v)
				switch {
				case i == ep || i == en:
					continue
				case k == nil:
					rows = append(rows, r)
					continue
				}
				s.steps++
				if s.steps > s.limit {
					return nil, nil, unknown
				}
				e := old[en] // -v + … for a row with v positive
				if k.Sign() < 0 {
					e = old[ep]
				}
				m := new(big.Int).Abs(k)
				c := new(big.Int).Add(r.c, new(big.Int).Mul(m, e.c))
				derivs = append(derivs, derivation{uses: [][2]any{{r.src, big.NewInt(1)}, {e.src, m}}})
				add(row{a: sum(one, r.a, m, e.a), c: c, src: len(derivs) - 1})
				if found {
					break
				}
			}
			continue
		}
		// The variable whose elimination makes the fewest rows.
		pos := map[int]int{}
		neg := map[int]int{}
		for _, r := range rows {
			for _, x := range r.a {
				if x.C.Sign() > 0 {
					pos[x.Var]++
				} else {
					neg[x.Var]++
				}
			}
		}
		best, bestCost := -1, 0
		for v := 0; v < s.nvars; v++ {
			if eliminated[v] || pos[v]+neg[v] == 0 {
				continue
			}
			if cost := pos[v] * neg[v]; best < 0 || cost < bestCost {
				best, bestCost = v, cost
			}
		}
		if best < 0 {
			break
		}
		v := best
		eliminated[v] = true
		var ps, ns, rest []row
		for _, r := range rows {
			switch r.sign(v) {
			case 1:
				ps = append(ps, r)
			case -1:
				ns = append(ns, r)
			default:
				rest = append(rest, r)
			}
		}
		stages = append(stages, stage{v: v, rows: append(append([]row(nil), ps...), ns...)})
		rows = rest
		for _, p := range ps {
			for _, n := range ns {
				s.steps++
				if s.steps > s.limit {
					return nil, nil, unknown
				}
				mp := new(big.Int).Neg(n.coef(v)) // > 0
				mn := new(big.Int).Set(p.coef(v))
				c := new(big.Int).Add(new(big.Int).Mul(mp, p.c), new(big.Int).Mul(mn, n.c))
				derivs = append(derivs, derivation{uses: [][2]any{{p.src, mp}, {n.src, mn}}})
				add(row{a: sum(mp, p.a, mn, n.a), c: c, src: len(derivs) - 1})
				if found {
					break
				}
			}
			if found {
				break
			}
		}
	}
	if found {
		return s.certificate(ineqs, derivs, contradiction), nil, unsat
	}
	return nil, s.model(stages), sat
}

// equality returns a variable that is not eliminated with the
// coefficient ±1 in an equality of rows, and the indices of the halves of
// the equality where its coefficient is 1 and -1.
func (s *solver) equality(rows []row, eliminated []bool) (int, int, int, bool) {
	// A hash of the coefficients, and of their negations; equal hashes
	// are compared exactly.
	hash := func(a []Coef, neg bool) uint64 {
		h := uint64(14695981039346656037)
		for _, x := range a {
			c := x.C.Int64()
			if !x.C.IsInt64() {
				c = int64(x.C.BitLen())
			}
			if neg {
				c = -c
			}
			h = (h ^ uint64(x.Var)) * 1099511628211
			h = (h ^ uint64(c)) * 1099511628211
		}
		return h
	}
	byHash := map[uint64][]int{}
	for i, r := range rows {
		k := hash(r.a, false)
		byHash[k] = append(byHash[k], i)
	}
	opposite := func(p, q row) bool {
		if len(p.a) != len(q.a) || new(big.Int).Add(p.c, q.c).Sign() != 0 {
			return false
		}
		for k := range p.a {
			if p.a[k].Var != q.a[k].Var || new(big.Int).Add(p.a[k].C, q.a[k].C).Sign() != 0 {
				return false
			}
		}
		return true
	}
	for i, r := range rows {
		for _, j := range byHash[hash(r.a, true)] {
			if j == i || !opposite(r, rows[j]) {
				continue
			}
			for _, x := range r.a {
				if eliminated[x.Var] || x.C.CmpAbs(one) != 0 {
					continue
				}
				if x.C.Sign() > 0 {
					return x.Var, i, j, true
				}
				return x.Var, j, i, true
			}
		}
	}
	return 0, 0, 0, false
}

// isBound reports whether row r is a bound of variable v alone: v ≤ c or
// -v ≤ c.
func isBound(r row, v int) bool {
	return len(r.a) == 1 && r.a[0].Var == v && r.a[0].C.CmpAbs(one) == 0
}

// fixedVar is a variable that its bounds fix, with its bound rows v ≤ k
// and -v ≤ -k.
type fixedVar struct {
	v      int
	hi, lo row
}

// satisfies reports whether the value of the variable satisfies its bound
// r.
func (fx fixedVar) satisfies(r row) bool {
	if r.a[0].C.Sign() > 0 {
		return r.c.Cmp(fx.hi.c) >= 0
	}
	return r.c.Cmp(fx.lo.c) >= 0
}

// propagate adds the bounds of the variables that the rows imply: for a
// row Σ a·x ≤ c, the sum of the row and of the bounds of the other
// variables, tightened, bounds one variable. It returns the variables
// that their bounds fix, and false at the step limit.
func (s *solver) propagate(rows *[]row, derivs *[]derivation, found *bool, contradiction *int) ([]fixedVar, bool) {
	type bound struct {
		k *big.Int
		r row
	}
	hi := map[int]*bound{}
	lo := map[int]*bound{}
	setBound := func(r row) {
		if len(r.a) != 1 || r.a[0].C.CmpAbs(one) != 0 {
			return
		}
		v := r.a[0].Var
		if r.a[0].C.Sign() > 0 {
			if hi[v] == nil || r.c.Cmp(hi[v].k) < 0 {
				hi[v] = &bound{new(big.Int).Set(r.c), r}
			}
		} else if k := new(big.Int).Neg(r.c); lo[v] == nil || k.Cmp(lo[v].k) > 0 {
			lo[v] = &bound{k, r}
		}
	}
	for _, r := range *rows {
		setBound(r)
	}
	rounds := 4*s.nvars + 8
	if rounds > 64 {
		rounds = 64
	}
	for round := 0; round < rounds; round++ {
		changed := false
		for ri := 0; ri < len(*rows); ri++ {
			r := (*rows)[ri]
			if len(r.a) < 2 {
				continue
			}
			for _, xj := range r.a {
				j, aj := xj.Var, xj.C
				// The bounds of the others: lower ones for positive
				// coefficients, upper ones for negative.
				c := new(big.Int).Set(r.c)
				uses := [][2]any{{r.src, big.NewInt(1)}}
				ok := true
				for _, xi := range r.a {
					if xi.Var == j {
						continue
					}
					var b *bound
					if xi.C.Sign() > 0 {
						b = lo[xi.Var]
					} else {
						b = hi[xi.Var]
					}
					if b == nil {
						ok = false
						break
					}
					m := new(big.Int).Abs(xi.C)
					if xi.C.Sign() > 0 {
						c.Sub(c, new(big.Int).Mul(xi.C, b.k)) // + |a|·(-x ≤ -lo)
					} else {
						c.Add(c, new(big.Int).Mul(m, b.k)) // + |a|·(x ≤ hi)
					}
					uses = append(uses, [2]any{b.r.src, m})
				}
				if !ok {
					continue
				}
				// a_j·x_j ≤ c, divided by |a_j|.
				m := new(big.Int).Abs(aj)
				k := new(big.Int).Div(c, m) // ⌊c/|a|⌋
				var better bool
				if aj.Sign() > 0 {
					better = hi[j] == nil || k.Cmp(hi[j].k) < 0
				} else {
					better = lo[j] == nil || new(big.Int).Neg(k).Cmp(lo[j].k) > 0
				}
				if !better {
					continue
				}
				s.steps++
				if s.steps > s.limit {
					return nil, false
				}
				*derivs = append(*derivs, derivation{uses: uses, tighten: m.Cmp(one) > 0})
				nr := row{a: []Coef{{j, big.NewInt(int64(aj.Sign()))}}, c: k, src: len(*derivs) - 1}
				if m.Cmp(one) == 0 {
					nr.c = c
				}
				*rows = append(*rows, nr)
				setBound(nr)
				changed = true
				if lo[j] != nil && hi[j] != nil && lo[j].k.Cmp(hi[j].k) > 0 {
					// x ≤ hi and -x ≤ -lo give 0 ≤ hi - lo < 0.
					*derivs = append(*derivs, derivation{uses: [][2]any{{hi[j].r.src, big.NewInt(1)}, {lo[j].r.src, big.NewInt(1)}}})
					*found, *contradiction = true, len(*derivs)-1
					return nil, true
				}
			}
		}
		if !changed {
			break
		}
	}
	var fixed []fixedVar
	for v := 0; v < s.nvars; v++ {
		if hi[v] != nil && lo[v] != nil && hi[v].k.Cmp(lo[v].k) == 0 {
			fixed = append(fixed, fixedVar{v: v, hi: hi[v].r, lo: lo[v].r})
		}
	}
	return fixed, true
}

// dedupe keeps of the rows with the same coefficients the one with the
// smallest constant.
func dedupe(rows []row) []row {
	seen := map[string]int{}
	var out []row
	var buf []byte
	for _, r := range rows {
		buf = buf[:0]
		for _, x := range r.a {
			v := x.Var
			buf = append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
			if x.C.IsInt64() {
				k := x.C.Int64()
				buf = append(buf, 'i', byte(k), byte(k>>8), byte(k>>16), byte(k>>24), byte(k>>32), byte(k>>40), byte(k>>48), byte(k>>56))
				continue
			}
			buf = append(buf, 'b', byte(x.C.Sign()+1))
			b := x.C.Bytes()
			buf = append(buf, byte(len(b)))
			buf = append(buf, b...)
		}
		k := string(buf)
		if i, ok := seen[k]; ok {
			if r.c.Cmp(out[i].c) < 0 {
				out[i] = r
			}
			continue
		}
		seen[k] = len(out)
		out = append(out, r)
	}
	return out
}

// certificate returns the steps that derive the contradiction from the
// inequalities, renumbered.
func (s *solver) certificate(ineqs []Ineq, derivs []derivation, last int) []Step {
	if last < 0 {
		// An inequality 0 ≤ c of the conjunction with c < 0.
		return []Step{{Uses: []Use{{Ineq: ineqs[-1-last], Derived: -1, Mult: big.NewInt(1)}}}}
	}
	need := map[int]bool{}
	var mark func(i int)
	mark = func(i int) {
		if i < 0 || need[i] {
			return
		}
		need[i] = true
		for _, u := range derivs[i].uses {
			mark(u[0].(int))
		}
	}
	mark(last)
	var order []int
	for i := range need {
		order = append(order, i)
	}
	sort.Ints(order)
	index := map[int]int{}
	var steps []Step
	for _, i := range order {
		d := derivs[i]
		st := Step{Tighten: d.tighten}
		for _, u := range d.uses {
			src, mult := u[0].(int), u[1].(*big.Int)
			if src < 0 {
				st.Uses = append(st.Uses, Use{Ineq: ineqs[-1-src], Derived: -1, Mult: new(big.Int).Set(mult)})
			} else {
				st.Uses = append(st.Uses, Use{Derived: index[src], Mult: new(big.Int).Set(mult)})
			}
		}
		index[i] = len(steps)
		steps = append(steps, st)
	}
	return steps
}

// model returns a rational solution of the rows of the stages, choosing
// for each variable in the reverse order of elimination an integer in its
// bounds where there is one.
func (s *solver) model(stages []stage) []*big.Rat {
	x := make([]*big.Rat, s.nvars)
	for i := range x {
		x[i] = new(big.Rat)
	}
	for k := len(stages) - 1; k >= 0; k-- {
		st := stages[k]
		var lo, hi *big.Rat
		for _, r := range st.rows {
			// a·v ≤ c - Σ others
			rest := new(big.Rat).SetInt(r.c)
			var av *big.Int
			for _, k := range r.a {
				if k.Var == st.v {
					av = k.C
					continue
				}
				rest.Sub(rest, new(big.Rat).Mul(new(big.Rat).SetInt(k.C), x[k.Var]))
			}
			if av == nil {
				continue
			}
			b := new(big.Rat).Quo(rest, new(big.Rat).SetInt(av))
			if av.Sign() > 0 {
				if hi == nil || b.Cmp(hi) < 0 {
					hi = b
				}
			} else if lo == nil || b.Cmp(lo) > 0 {
				lo = b
			}
		}
		x[st.v] = choose(lo, hi)
	}
	return x
}

// choose returns a value in [lo, hi], where a nil bound is unbounded: the
// integer closest to 0 if there is one, otherwise the midpoint.
func choose(lo, hi *big.Rat) *big.Rat {
	ceil := func(r *big.Rat) *big.Int {
		q := new(big.Int).Div(r.Num(), r.Denom()) // ⌊r⌋
		if !r.IsInt() {
			q.Add(q, one)
		}
		return q
	}
	floor := func(r *big.Rat) *big.Int { return new(big.Int).Div(r.Num(), r.Denom()) }
	var l, h *big.Int
	if lo != nil {
		l = ceil(lo)
	}
	if hi != nil {
		h = floor(hi)
	}
	switch {
	case l != nil && h != nil && l.Cmp(h) > 0:
		m := new(big.Rat).Add(lo, hi)
		return m.Quo(m, big.NewRat(2, 1))
	case l != nil && l.Sign() > 0:
		return new(big.Rat).SetInt(l)
	case h != nil && h.Sign() < 0:
		return new(big.Rat).SetInt(h)
	}
	return new(big.Rat)
}
