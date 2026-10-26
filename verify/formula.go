// Package verify is the built-in solver of the toolchain
// (spec/08-contracts.md §8.8, spec/22-compiler.md §22.8). Stage 1 decides
// linear integer arithmetic: formulas built with conjunction, disjunction
// and negation from comparisons of linear terms over integer variables.
//
// An obligation is proved when its facts together with the negation of
// its goal have no integer solution; the proof is a certificate that the
// independent checker of package verify/check verifies. An obligation is
// refuted with a counterexample, or the result is unknown when the step
// limit is reached. The work depends only on the obligation and the
// limit (contract.solver.determinism).
package verify

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Coef is the coefficient of a variable in a linear term.
type Coef struct {
	Var int
	C   *big.Int
}

// Term is a linear term: the sum of the coefficients times their
// variables, sorted by variable, and a constant.
type Term struct {
	Coefs []Coef
	Const *big.Int
}

// Const returns the constant term c.
func Const(c int64) Term { return Term{Const: big.NewInt(c)} }

// ConstBig returns the constant term c.
func ConstBig(c *big.Int) Term { return Term{Const: new(big.Int).Set(c)} }

// V returns the term of variable v.
func V(v int) Term { return Term{Coefs: []Coef{{v, big.NewInt(1)}}, Const: new(big.Int)} }

func (t Term) constant() *big.Int {
	if t.Const == nil {
		return new(big.Int)
	}
	return t.Const
}

// combine returns a·t + b·u.
func combine(a *big.Int, t Term, b *big.Int, u Term) Term {
	m := map[int]*big.Int{}
	for _, c := range t.Coefs {
		m[c.Var] = new(big.Int).Mul(a, c.C)
	}
	for _, c := range u.Coefs {
		x := new(big.Int).Mul(b, c.C)
		if old, ok := m[c.Var]; ok {
			x.Add(x, old)
		}
		m[c.Var] = x
	}
	r := Term{Const: new(big.Int).Add(new(big.Int).Mul(a, t.constant()), new(big.Int).Mul(b, u.constant()))}
	for v, c := range m {
		if c.Sign() != 0 {
			r.Coefs = append(r.Coefs, Coef{v, c})
		}
	}
	sort.Slice(r.Coefs, func(i, j int) bool { return r.Coefs[i].Var < r.Coefs[j].Var })
	return r
}

var one = big.NewInt(1)

// Add returns t + u.
func (t Term) Add(u Term) Term { return combine(one, t, one, u) }

// Sub returns t - u.
func (t Term) Sub(u Term) Term { return combine(one, t, big.NewInt(-1), u) }

// Scale returns k·t.
func (t Term) Scale(k *big.Int) Term { return combine(k, t, new(big.Int), Term{}) }

func (t Term) String() string {
	var parts []string
	for _, c := range t.Coefs {
		parts = append(parts, fmt.Sprintf("%s·x%d", c.C, c.Var))
	}
	if t.constant().Sign() != 0 || len(parts) == 0 {
		parts = append(parts, t.constant().String())
	}
	return strings.Join(parts, " + ")
}

// Rel is a comparison.
type Rel int

const (
	LE Rel = iota
	LT
	EQ
	NE
	GE
	GT
)

var relNames = [...]string{"<=", "<", "==", "!=", ">=", ">"}

// Kind is the kind of a formula.
type Kind int

const (
	True Kind = iota
	False
	Atom // L Rel R
	Not
	And
	Or
)

// Formula is a formula of linear integer arithmetic.
type Formula struct {
	Kind Kind
	Rel  Rel
	L, R Term
	Args []Formula
}

// Cmp returns the atom l rel r.
func Cmp(l Term, rel Rel, r Term) Formula { return Formula{Kind: Atom, Rel: rel, L: l, R: r} }

// AndOf returns the conjunction of fs.
func AndOf(fs ...Formula) Formula { return Formula{Kind: And, Args: fs} }

// OrOf returns the disjunction of fs.
func OrOf(fs ...Formula) Formula { return Formula{Kind: Or, Args: fs} }

// NotOf returns the negation of f.
func NotOf(f Formula) Formula { return Formula{Kind: Not, Args: []Formula{f}} }

func (f Formula) String() string {
	switch f.Kind {
	case True:
		return "true"
	case False:
		return "false"
	case Atom:
		return f.L.String() + " " + relNames[f.Rel] + " " + f.R.String()
	case Not:
		return "!(" + f.Args[0].String() + ")"
	}
	op := " && "
	if f.Kind == Or {
		op = " || "
	}
	var parts []string
	for _, a := range f.Args {
		parts = append(parts, "("+a.String()+")")
	}
	return strings.Join(parts, op)
}

// Problem is a proof obligation: the goal under the facts. Vars are the
// names of the variables, by number, for counterexamples.
type Problem struct {
	Vars  []string
	Facts []Formula
	Goal  Formula
}

// ---------------------------------------------------------------------
// Normal form and certificates.

// Ineq is the inequality Σ Coefs·x ≤ C.
type Ineq struct {
	Coefs []Coef
	C     *big.Int
}

// Key returns a canonical text of the inequality.
func (q Ineq) Key() string {
	var b strings.Builder
	for _, c := range q.Coefs {
		fmt.Fprintf(&b, "%s*x%d+", c.C, c.Var)
	}
	fmt.Fprintf(&b, "<=%s", q.C)
	return b.String()
}

func (q Ineq) String() string {
	if len(q.Coefs) == 0 {
		return "0 <= " + q.C.String()
	}
	return Term{Coefs: q.Coefs}.String() + " <= " + q.C.String()
}

// NKind is the kind of a formula in normal form.
type NKind int

const (
	NTrue NKind = iota
	NFalse
	NIneq
	NAnd
	NOr
)

// NF is a formula in normal form (contract.solver.normal-form): negations
// are pushed into the atoms, and every atom is an inequality Σ a·x ≤ c.
type NF struct {
	Kind NKind
	Ineq Ineq
	Args []NF
}

// Key returns a canonical text of the formula.
func (n NF) Key() string {
	switch n.Kind {
	case NTrue:
		return "T"
	case NFalse:
		return "F"
	case NIneq:
		return n.Ineq.Key()
	}
	var parts []string
	for _, a := range n.Args {
		parts = append(parts, a.Key())
	}
	op := "&"
	if n.Kind == NOr {
		op = "|"
	}
	return op + "(" + strings.Join(parts, ",") + ")"
}

// ProofKind is the kind of a node of a certificate.
type ProofKind int

const (
	// Split proves the conjunction for each disjunct of the disjunction
	// Of of the conjunction.
	Split ProofKind = iota
	// Branch proves it with Term ≤ K and with Term ≥ K+1, which cover the
	// integers.
	Branch
	// Refute derives 0 ≤ c with c < 0 from the inequalities of the
	// conjunction.
	Refute
)

// Proof is a node of a certificate: it refutes the current conjunction,
// which is first the facts and the negation of the goal in normal form.
type Proof struct {
	Kind  ProofKind
	Of    NF     // Split
	Term  []Coef // Branch
	K     *big.Int
	Cases []*Proof // Split: one per disjunct; Branch: two
	Steps []Step   // Refute
}

// Step derives an inequality: the sum of the multiples of the inequalities
// it uses, divided, if Tighten is set, by the greatest common divisor g
// of its coefficients, with the constant rounded down: Σ (a/g)·x ≤ ⌊c/g⌋,
// which holds for integers.
type Step struct {
	Uses    []Use
	Tighten bool
}

// Use is an inequality of the conjunction, or with Derived ≥ 0 the result
// of an earlier step, with a positive multiple.
type Use struct {
	Ineq    Ineq
	Derived int
	Mult    *big.Int
}

// Status is the result of the solver.
type Status int

const (
	Proved Status = iota
	Refuted
	Unknown
)

func (s Status) String() string { return [...]string{"proved", "refuted", "unknown"}[s] }

// Result is the result of the solver for a problem.
type Result struct {
	Status Status
	// Proof is the certificate of a proved problem.
	Proof *Proof
	// Model is the counterexample of a refuted problem: a value for each
	// variable.
	Model []*big.Int
	Steps int
}
