// Package vc creates and proves the proof obligations of a program
// (spec/08-contracts.md): the requires and ensures clauses of functions
// and the assertions, which reject the program if they are not proven
// (contract.obligation.unproven), and the run-time checks of index and
// slice bounds, integer overflow and division, which are removed from the
// program when they are proven (contract.check.runtime).
//
// The obligations come from symbolic execution of each function body:
// an integer value is a linear term over the variables of the solver,
// with facts about them; a boolean value is a formula; a slice, an array
// or a string is the term of its length. The facts of a point are its
// path condition and what the checks before it imply. Branches join with
// the disjunction of their facts; a loop runs its body once from a state
// in which the variables it assigns have unknown values.
package vc

import (
	"crypto/sha256"
	"fmt"
	"go/constant"
	"math/big"
	"sort"
	"strings"
	"sync"

	"an/syntax"
	"an/types"
	"an/verify"
	"an/verify/check"
)

// Check proves the obligations of the packages and records the run-time
// checks proven not to fail in Info.Proven. infos are in dependency order.
func Check(infos []*types.Info) []*syntax.Error { return CheckCached(infos, nil) }

// CheckCached is Check with the results of a package reused while its
// source and those of the packages before it are the same: keys are the
// hashes of the sources of infos, or nil.
func CheckCached(infos []*types.Info, keys []string) []*syntax.Error {
	vc := &checker{funcInfo: map[*syntax.FuncDecl]*types.Info{}, pkgInfo: map[*types.Package]*types.Info{}, hasInv: map[types.Type]bool{}, noZero: map[types.Type]syntax.Expr{}}
	for _, info := range infos {
		vc.pkgInfo[info.Pkg] = info
		for _, f := range append(append([]*types.Func(nil), info.Funcs...), info.Methods...) {
			if f.Decl != nil {
				vc.funcInfo[f.Decl] = info
			}
		}
	}
	var errs []*syntax.Error
	key := ""
	for i, info := range infos {
		if keys != nil {
			h := sha256.Sum256([]byte(key + "\x00" + keys[i] + "\x00" + fmt.Sprint(info.IntBounds(types.Typ[types.Int]))))
			key = string(h[:])
			if r := cached(key); r != nil {
				errs = append(errs, r.apply(info)...)
				continue
			}
		}
		perrs := vc.zeroValues(info)
		for _, f := range append(append([]*types.Func(nil), info.Funcs...), info.Methods...) {
			if f.Decl == nil || f.Decl.Body == nil {
				continue
			}
			perrs = append(perrs, indirect(info, f.Decl.Body)...)
			perrs = append(perrs, ifaceMethods(info, f.Decl.Body, f.Signature())...)
			fv := newFunc(vc, info, f.Signature(), f.Decl.Body, f.Name())
			fv.self = f
			if len(f.Signature().TypeParams) > 0 || f.Signature().Recv != nil && len(f.Signature().RecvTypeParams) > 0 || info.Templates[f] {
				fv.generic = true
			}
			fv.run()
			perrs = append(perrs, fv.errs...)
		}
		if keys != nil {
			store(key, info, perrs)
		}
		errs = append(errs, perrs...)
	}
	sort.SliceStable(errs, func(i, j int) bool {
		a, b := errs[i].Pos, errs[j].Pos
		return a.File < b.File || a.File == b.File && a.Offset < b.Offset
	})
	return errs
}

// indirect reports the functions with preconditions that body uses as
// values (contract.obligation.indirect).
func indirect(info *types.Info, body *syntax.BlockStmt) []*syntax.Error {
	var errs []*syntax.Error
	callees := map[syntax.Expr]bool{}
	syntax.Walk(body, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CallExpr:
			fun := unparen(n.Fun)
			if ix, ok := fun.(*syntax.IndexExpr); ok {
				fun = unparen(ix.X)
			}
			callees[fun] = true
		case *syntax.Name, *syntax.SelectorExpr:
			e := n.(syntax.Expr)
			if callees[e] {
				return true
			}
			id, _ := e.(*syntax.Name)
			if sel, ok := e.(*syntax.SelectorExpr); ok {
				if s := info.Selections[sel]; s != nil {
					return true // a method value: methods of interfaces carry no preconditions either
				}
				id = sel.Sel
			}
			g, ok := info.Uses[id].(*types.Func)
			if !ok {
				return true
			}
			req, _ := contracts(g)
			for _, r := range req {
				if types.ExprString(unparen(r.X)) == "true" {
					continue // requires true
				}
				errs = append(errs, &syntax.Error{Pos: e.Pos(), Rule: "contract.obligation.indirect", Msg: fmt.Sprintf("%s has preconditions and cannot be used as a function value: calls through function values carry none", g.Name())})
				break
			}
			if _, ok := e.(*syntax.SelectorExpr); ok {
				return false
			}
		}
		return true
	})
	return errs
}

// ifaceMethods reports the conversions in body of values whose methods
// with preconditions satisfy methods of the interface they are converted
// to (contract.obligation.indirect): in assignments, arguments, results,
// elements of composite literals and explicit conversions.
func ifaceMethods(info *types.Info, body *syntax.BlockStmt, sig *types.Signature) []*syntax.Error {
	var errs []*syntax.Error
	conv := func(e syntax.Expr, to types.Type) {
		if e == nil || to == nil || !types.IsInterface(to) {
			return
		}
		from := info.Types[e].Type
		if from == nil || types.IsInterface(from) {
			return
		}
		iface, ok := core(to).(*types.Interface)
		if !ok {
			return
		}
		want := map[string]bool{}
		for _, m := range iface.MethodSet() {
			want[m.Name()] = true
		}
		for _, m := range info.MethodSet(from) {
			if !want[m.Name()] {
				continue
			}
			req, _ := contracts(m)
			for _, r := range req {
				if types.ExprString(unparen(r.X)) == "true" {
					continue
				}
				errs = append(errs, &syntax.Error{Pos: e.Pos(), Rule: "contract.obligation.indirect", Msg: fmt.Sprintf("the method %s of %s has preconditions and cannot satisfy the interface %s: calls through interfaces carry none", m.Name(), from, to)})
				break
			}
		}
	}
	syntax.Walk(body, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.FuncLit:
			return true
		case *syntax.AssignStmt:
			if n.Tok == syntax.Assign && len(n.Lhs) == len(n.Rhs) {
				for i := range n.Lhs {
					conv(n.Rhs[i], info.Types[n.Lhs[i]].Type)
				}
			}
		case *syntax.DeclStmt:
			for _, spec := range n.Decl.Specs {
				if vs, ok := spec.(*syntax.ValueSpec); ok && vs.Type != nil && len(vs.Values) == len(vs.Names) {
					for _, v := range vs.Values {
						conv(v, info.Types[vs.Type].Type)
					}
				}
			}
		case *syntax.CallExpr:
			tv := info.Types[n.Fun]
			if tv.IsType() && len(n.Args) == 1 {
				conv(n.Args[0], tv.Type)
				return true
			}
			if s, ok := core(tv.Type).(*types.Signature); ok {
				for i, a := range n.Args {
					if i < s.Params.Len() {
						conv(a, s.Params.At(i).Type())
					}
				}
			}
		case *syntax.ReturnStmt:
			if sig.Results != nil && len(n.Results) == sig.Results.Len() {
				for i, r := range n.Results {
					conv(r, sig.Results.At(i).Type())
				}
			}
		case *syntax.CompositeLit:
			t := core(info.Types[n].Type)
			for _, el := range n.Elts {
				v := el
				if kv, ok := el.(*syntax.KeyValueExpr); ok {
					v = kv.Value
				}
				switch u := t.(type) {
				case *types.Slice:
					conv(v, u.Elem)
				case *types.Array:
					conv(v, u.Elem)
				}
			}
		}
		return true
	})
	return errs
}

// result is the result of a package in the cache: the positions of the
// proven checks and the diagnostics.
type result struct {
	proven map[string]bool
	depth  map[string]int // the bounds on recursion by function
	errs   []*syntax.Error
}

var cache = struct {
	sync.Mutex
	m map[string]*result
}{m: map[string]*result{}}

func cached(key string) *result {
	cache.Lock()
	defer cache.Unlock()
	return cache.m[key]
}

// nodeKey identifies a node of a check by its position and kind.
func nodeKey(n syntax.Node) string {
	p := n.Pos()
	end := 0
	if e, ok := n.(interface{ End() syntax.Pos }); ok {
		end = e.End().Offset
	}
	return fmt.Sprintf("%s:%d:%d:%T", p.File, p.Offset, end, n)
}

func store(key string, info *types.Info, errs []*syntax.Error) {
	r := &result{proven: map[string]bool{}, depth: map[string]int{}, errs: errs}
	for n := range info.Proven {
		r.proven[nodeKey(n)] = true
	}
	for f, d := range info.RecursionDepth {
		if f.Decl != nil {
			r.depth[nodeKey(f.Decl)] = d
		}
	}
	cache.Lock()
	cache.m[key] = r
	cache.Unlock()
}

// apply records the proven checks and the bounds on recursion of the
// cached result in info.
func (r *result) apply(info *types.Info) []*syntax.Error {
	for _, f := range append(append([]*types.Func(nil), info.Funcs...), info.Methods...) {
		if f.Decl == nil || f.Decl.Body == nil {
			continue
		}
		if d, ok := r.depth[nodeKey(f.Decl)]; ok {
			info.RecursionDepth[f] = d
		}
		syntax.Walk(f.Decl.Body, func(n syntax.Node) bool {
			switch n.(type) {
			case *syntax.IndexExpr, *syntax.SliceExpr, *syntax.BinaryExpr, *syntax.UnaryExpr, *syntax.AssignStmt, *syntax.IncDecStmt:
				if r.proven[nodeKey(n)] {
					info.Proven[n] = true
				}
			}
			return true
		})
	}
	return r.errs
}

// checker holds what the verification of the functions shares.
type checker struct {
	funcInfo map[*syntax.FuncDecl]*types.Info
	pkgInfo  map[*types.Package]*types.Info
	hasInv   map[types.Type]bool
	noZero   map[types.Type]syntax.Expr // the invariant that the zero value violates, nil if it has one
}

// infoOf returns the information of the package that declares g.
func (c *checker) infoOf(g *types.Func) *types.Info {
	if g.Decl != nil {
		if info := c.funcInfo[g.Decl]; info != nil {
			return info
		}
	}
	return nil
}

// vkind is the kind of a symbolic value.
type vkind int

const (
	vNone   vkind = iota // not tracked
	vInt                 // an integer: t
	vBool                // a boolean: f
	vSeq                 // a slice, an array or a string: t is its length
	vStruct              // a struct: fields
	vEnum                // an enum: t is the index of its variant, cases the fields of each variant
)

type val struct {
	kind vkind
	t    verify.Term
	f    verify.Formula
	// seq identifies the elements of a sequence, 0 if they are not
	// tracked, and off is the offset of its first element among them:
	// element j of the sequence is element off+j of seq.
	seq int
	off verify.Term
	// fields are the values of the fields of a struct, in order.
	fields []val
	// cases are the values of the fields of each variant of an enum.
	cases [][]val
}

// state is the symbolic state at a point of a function.
type state struct {
	vars  map[*types.Var]val
	facts []verify.Formula
	fvars [][]int // the variables of each fact
	dead  bool
	// defs holds the variables defined by the facts as functions of a
	// term, such as its bits: the same term gets the same variables, so
	// that the solver needs not prove them equal.
	defs map[string][]verify.Term
	// apps holds, for each sequence of elements, the applications of its
	// prefix sum: the arguments and the values.
	apps map[int][][2]verify.Term
	// quants are the quantifiers of the facts that proofs instantiate.
	quants []quant
	// reads are the positions of the elements read, for the instances.
	reads []verify.Term
}

func (s *state) clone() *state {
	c := &state{vars: make(map[*types.Var]val, len(s.vars)), facts: append([]verify.Formula(nil), s.facts...), fvars: append([][]int(nil), s.fvars...), dead: s.dead, defs: s.cloneDefs(), apps: s.cloneApps(), quants: s.quants[:len(s.quants):len(s.quants)], reads: s.reads[:len(s.reads):len(s.reads)]}
	for k, v := range s.vars {
		c.vars[k] = v
	}
	return c
}

func (s *state) cloneDefs() map[string][]verify.Term {
	d := make(map[string][]verify.Term, len(s.defs))
	for k, v := range s.defs {
		d[k] = v
	}
	return d
}

func (s *state) cloneApps() map[int][][2]verify.Term {
	a := make(map[int][][2]verify.Term, len(s.apps))
	for k, v := range s.apps {
		a[k] = v[:len(v):len(v)] // an append copies
	}
	return a
}

func (s *state) assume(f verify.Formula) {
	vs := termVars(f)
	if len(vs) == 0 {
		if v, known, _ := evalFormula(f, nil); known {
			if !v {
				s.dead = true // a path that cannot be taken
			}
			return
		}
	}
	s.facts = append(s.facts, f)
	s.fvars = append(s.fvars, vs)
}

// termVars returns the variables of formula g.
func termVars(g verify.Formula) []int {
	seen := map[int]bool{}
	var out []int
	var walk func(g verify.Formula)
	walk = func(g verify.Formula) {
		for _, t := range []verify.Term{g.L, g.R} {
			for _, c := range t.Coefs {
				if !seen[c.Var] {
					seen[c.Var] = true
					out = append(out, c.Var)
				}
			}
		}
		for _, a := range g.Args {
			walk(a)
		}
	}
	walk(g)
	return out
}

// def returns the variables that the facts of s define as function kind
// of term x, calling make to define them the first time.
func (s *state) def(kind string, x verify.Term, make func() []verify.Term) []verify.Term {
	key := kind + " " + x.String()
	if d, ok := s.defs[key]; ok {
		return d
	}
	d := make()
	if s.defs == nil {
		s.defs = map[string][]verify.Term{}
	}
	s.defs[key] = d
	return d
}

// jump is the target of break and continue: a loop, or for break a
// switch.
type jump struct {
	label     string
	loop      bool
	breaks    []*state
	continues []*state
}

// fn is the verification of one function body.
type fn struct {
	vc      *checker
	info    *types.Info
	sig     *types.Signature
	body    *syntax.BlockStmt
	name    string
	generic bool

	// inoutLoops are the loop variables of the enclosing loops over
	// iterators that yield inout access (contract.type.boundaries).
	inoutLoops []*types.Var

	names    []string // the variables of the solver
	captured map[*types.Var]bool
	locals   map[*types.Var]bool
	entry    map[*types.Var]val // the values of the parameters at entry
	jumps    []*jump
	errs     []*syntax.Error
	proven   map[syntax.Node]bool // false once a check of the node is not proven
	reported map[syntax.Expr]bool // the narrowing conversions reported
	lits     []*syntax.FuncLit
	giveUp   string // a construct that the verification does not handle
	ensures  []*syntax.ContractStmt
	results  []*types.Var
	contract bool // the function has contracts or assertions
	self     *types.Func
	recCalls []recCall           // the calls of self in the body
	deferred []syntax.Expr       // the inout arguments of deferred calls
	nseq     int                 // the sequences of elements so far
	zeroSeq  map[int]verify.Term // the sequences of zero elements, with their lengths
	updates  map[int]update
	congr    []verify.Formula                  // the congruences of the prefix sums, true in every state
	known    map[int][]*big.Int                // the prefix sums of sequences of constant elements
	indexAt  map[*syntax.IndexExpr]verify.Term // the last index of each index expression
}

// update is a sequence equal to sequence base but for element at, which
// is delta more.
type update struct {
	base      int
	at, delta verify.Term
}

// recCall is a call of a function in its own body: the state before the
// call and the values of the arguments.
type recCall struct {
	st   *state
	args []val
}

func newFunc(vc *checker, info *types.Info, sig *types.Signature, body *syntax.BlockStmt, name string) *fn {
	return &fn{vc: vc, info: info, sig: sig, body: body, name: name, captured: map[*types.Var]bool{}, locals: map[*types.Var]bool{}, entry: map[*types.Var]val{}, proven: map[syntax.Node]bool{}, reported: map[syntax.Expr]bool{}}
}

func (f *fn) errorf(pos syntax.Pos, rule, format string, args ...any) {
	f.errs = append(f.errs, &syntax.Error{Pos: pos, Rule: rule, Msg: fmt.Sprintf(format, args...)})
}

// run verifies the body.
func (f *fn) run() {
	f.scan()
	if f.giveUp != "" || f.generic {
		// The narrowing conversions are not proven with facts here.
		syntax.Walk(f.body, func(n syntax.Node) bool {
			if e, ok := n.(syntax.Expr); ok {
				if t, ok := f.info.Narrowings[e]; ok && !f.reported[e] {
					f.reported[e] = true
					f.errorf(e.Pos(), "type.conv.int-narrow", "cannot convert %s (value of type %s) to type %s: the value may not fit, and it is not proven to (use conv.Narrow[%s] to get (%s, bool))", types.ExprString(e), f.typeOf(e), t, t, t)
				}
			}
			return true
		})
	}
	if f.giveUp != "" {
		if f.contract {
			f.errorf(f.body.Lbrace, "contract.obligation.unproven", "the contracts of %s are not checked in a function with %s: not supported yet (spec/24-roadmap.md, step 23)", f.name, f.giveUp)
		}
		return
	}
	if f.generic {
		if f.contract {
			f.errorf(f.body.Lbrace, "contract.obligation.unproven", "the contracts of the generic function %s are not checked: not supported yet (spec/24-roadmap.md, step 23)", f.name)
		}
		return
	}
	st := &state{vars: map[*types.Var]val{}}
	params := []*types.Var{}
	if f.sig.Recv != nil {
		params = append(params, f.sig.Recv)
	}
	if f.sig.Params != nil {
		params = append(params, f.sig.Params.Vars...)
	}
	for _, p := range params {
		f.locals[p] = true
		v := f.received(st, p.Type(), p.Name())
		f.write(st, p, v)
		f.entry[p] = v
	}
	if f.sig.Results != nil {
		for _, r := range f.sig.Results.Vars {
			f.results = append(f.results, r)
			if r.Name() != "" {
				f.locals[r] = true
				f.write(st, r, f.zero(r.Type()))
			}
		}
	}
	// The preconditions hold at entry (contract.obligation.assume).
	for _, s := range f.body.List {
		c, ok := s.(*syntax.ContractStmt)
		if !ok {
			break
		}
		switch c.Word {
		case "requires":
			if g, ok := f.contractIn(f.info, st, c.X, nil, true); ok {
				st.assume(g)
			}
		case "ensures":
			if !isConstraint(f.info, c.X) {
				f.ensures = append(f.ensures, c)
			}
		}
	}
	f.stmts(st, f.body.List)
	if !st.dead {
		f.exit(st, f.body.Rbrace, nil)
	}
	for n, ok := range f.proven {
		if ok {
			f.info.Proven[n] = true
		}
	}
	if d := f.recursionDepth(params); d > 0 {
		f.info.RecursionDepth[f.self] = d
	}
	for _, lit := range f.lits {
		sig, _ := core(f.info.Types[lit].Type).(*types.Signature)
		if sig == nil {
			continue
		}
		g := newFunc(f.vc, f.info, sig, lit.Body, f.name+" (function literal)")
		g.run()
		f.errs = append(f.errs, g.errs...)
	}
}

// isConstraint reports whether a contract expression is a constraint form
// (contract.constraint.forms).
func isConstraint(info *types.Info, e syntax.Expr) bool {
	call, ok := unparen(e).(*syntax.CallExpr)
	if !ok {
		return false
	}
	sel, ok := unparen(call.Fun).(*syntax.SelectorExpr)
	if !ok {
		return false
	}
	if id, ok := unparen(sel.X).(*syntax.Name); ok {
		if pkg, ok := info.Uses[id].(*types.PkgName); ok {
			p := pkg.Imported.Path
			return p == "an/effect" || p == "an/secret" || p == "an/wcet"
		}
	}
	return false
}

func unparen(e syntax.Expr) syntax.Expr {
	for {
		p, ok := e.(*syntax.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// scan finds the variables captured by function literals, the
// constructs that the verification does not handle, and whether the
// function has contracts.
func (f *fn) scan() {
	syntax.Walk(f.body, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.FuncLit:
			syntax.Walk(n.Body, func(m syntax.Node) bool {
				if id, ok := m.(*syntax.Name); ok {
					if v, ok := f.info.Uses[id].(*types.Var); ok {
						f.captured[v] = true
					}
				}
				return true
			})
			return false
		case *syntax.BranchStmt:
			if n.Tok == syntax.Goto {
				f.giveUp = "goto"
			}
		case *syntax.ContractStmt:
			if n.Word == "requires" || n.Word == "invariant" || n.Word == "ensures" && !isConstraint(f.info, n.X) {
				f.contract = true
			}
			return false // the predicates of quantifiers capture nothing
		case *syntax.CallExpr:
			if f.isAssert(n) {
				f.contract = true
				return false
			}
		}
		return true
	})
	// Variables captured by literals are not tracked; nor are the ones
	// declared in them.
}

func (f *fn) isAssert(c *syntax.CallExpr) bool {
	id, ok := unparen(c.Fun).(*syntax.Name)
	if !ok {
		return false
	}
	b, ok := f.info.Uses[id].(*types.Builtin)
	return ok && b.Name() == "assert" && b.Pkg() == ""
}

// ---------------------------------------------------------------------
// Values.

func basicOf(t types.Type) *types.Basic {
	if t == nil {
		return nil
	}
	b, _ := core(t).(*types.Basic)
	return b
}

func isInt(t types.Type) bool {
	b := basicOf(t)
	return b != nil && b.Info()&types.IsInteger != 0
}

func isBool(t types.Type) bool {
	b := basicOf(t)
	return b != nil && b.Info()&types.IsBoolean != 0
}

func isString(t types.Type) bool {
	b := basicOf(t)
	return b != nil && b.Info()&types.IsString != 0 && b.Info()&types.IsTerminated == 0
}

// seqLen returns the length of an array type, or -1 for a slice or a
// string, or -2 for other types.
func seqLen(t types.Type) int64 {
	if t == nil {
		return -2
	}
	switch u := core(t).(type) {
	case *types.Array:
		return u.Len
	case *types.Slice:
		return -1
	}
	if isString(t) {
		return -1
	}
	return -2
}

// newVar returns a new variable of the solver.
func (f *fn) newVar(name string) verify.Term {
	f.names = append(f.names, name)
	return verify.V(len(f.names) - 1)
}

func between(t verify.Term, lo, hi *big.Int) verify.Formula {
	return verify.AndOf(verify.Cmp(verify.ConstBig(lo), verify.LE, t), verify.Cmp(t, verify.LE, verify.ConstBig(hi)))
}

// inRange adds the fact that t lies in the range of integer type typ.
func (f *fn) inRange(st *state, t verify.Term, typ types.Type) {
	if lo, hi, ok := f.info.IntBounds(typ); ok {
		st.assume(between(t, lo, hi))
	}
}

// maxLen is the largest length of a slice or a string: the largest int.
func (f *fn) maxLen() *big.Int {
	_, hi, _ := f.info.IntBounds(types.Typ[types.Int])
	return hi
}

// fresh returns an unknown value of type t.
func (f *fn) fresh(st *state, t types.Type, name string) val {
	switch {
	case isInt(t):
		v := f.newVar(name)
		f.inRange(st, v, t)
		return val{kind: vInt, t: v}
	case isBool(t):
		v := f.newVar(name)
		st.assume(between(v, big.NewInt(0), big.NewInt(1)))
		return val{kind: vBool, f: verify.Cmp(v, verify.EQ, verify.Const(1))}
	}
	if s := structOf(t); s != nil {
		if leaves(t, 0) > maxLeaves {
			return val{}
		}
		x := val{kind: vStruct, fields: make([]val, len(s.Fields))}
		for i, fl := range s.Fields {
			x.fields[i] = f.fresh(st, fl.Type(), name+"."+fl.Name())
		}
		return x
	}
	if en := enumOf(t); en != nil {
		if leaves(t, 0) > maxLeaves {
			return val{}
		}
		tag := f.newVar("variant(" + name + ")")
		st.assume(between(tag, zeroInt, big.NewInt(int64(len(en.Variants)-1))))
		x := val{kind: vEnum, t: tag, cases: make([][]val, len(en.Variants))}
		for i, v := range en.Variants {
			for _, fl := range v.Fields() {
				x.cases[i] = append(x.cases[i], f.fresh(st, fl.Type(), name+"."+v.Name()+"."+fl.Name()))
			}
		}
		return x
	}
	switch n := seqLen(t); {
	case n >= 0:
		return val{kind: vSeq, t: verify.Const(n), seq: f.newSeq(), off: cint(0)}
	case n == -1:
		v := f.newVar("len(" + name + ")")
		st.assume(between(v, big.NewInt(0), f.maxLen()))
		return val{kind: vSeq, t: v, seq: f.newSeq(), off: cint(0)}
	}
	return val{}
}

// maxKnown bounds the sequences of constant elements whose prefix sums
// at a variable argument are a disjunction of the known ones.
const maxKnown = 64

// maxLeaves bounds the fields of the structs whose values are tracked,
// with the fields of their fields.
const maxLeaves = 64

// leaves returns the number of fields of a value of type t that are not
// structs, counting into the fields that are, beyond n once it exceeds
// maxLeaves.
func leaves(t types.Type, n int) int {
	if en := enumOf(t); en != nil {
		n++
		for _, v := range en.Variants {
			for _, fl := range v.Fields() {
				if n = leaves(fl.Type(), n); n > maxLeaves {
					return n
				}
			}
		}
		return n
	}
	s := structOf(t)
	if s == nil {
		return n + 1
	}
	for _, fl := range s.Fields {
		if n = leaves(fl.Type(), n); n > maxLeaves {
			break
		}
	}
	return n
}

// variant returns the value of variant k of enum type t with the values
// of its fields; the fields of the other variants are zero.
func (f *fn) variant(t types.Type, k int, args []val) val {
	en := enumOf(t)
	x := val{kind: vEnum, t: cint(int64(k)), cases: make([][]val, len(en.Variants))}
	for i, v := range en.Variants {
		for j, fl := range v.Fields() {
			y := f.zero(fl.Type())
			if i == k && j < len(args) {
				y = args[j]
			}
			x.cases[i] = append(x.cases[i], y)
		}
	}
	return x
}

// variantOf returns the variant that e names: a variant, a qualified
// variant or an instance of one.
func variantOf(info *types.Info, e syntax.Expr) *types.Variant {
	switch e := unparen(e).(type) {
	case *syntax.Name:
		v, _ := info.Uses[e].(*types.Variant)
		return v
	case *syntax.SelectorExpr:
		v, _ := info.Uses[e.Sel].(*types.Variant)
		return v
	case *syntax.IndexExpr:
		return variantOf(info, e.X)
	}
	return nil
}

// sameVariant returns the fact that enum values x and y are equal: the
// same variant with equal fields, if the fields are integers, booleans
// or structs of them, and otherwise only the same variant (false then
// says nothing).
func sameVariant(x, y val) (verify.Formula, bool) {
	same := []verify.Formula{verify.Cmp(x.t, verify.EQ, y.t)}
	exact := true
	for k := range x.cases {
		if k >= len(y.cases) {
			return verify.Formula{}, false
		}
		var eq []verify.Formula
		for j := range x.cases[k] {
			if j >= len(y.cases[k]) || !exactKind(x.cases[k][j]) {
				exact = false
				continue
			}
			eq = append(eq, equal(x.cases[k][j], y.cases[k][j])...)
		}
		if len(eq) > 0 {
			same = append(same, verify.OrOf(verify.Cmp(x.t, verify.NE, cint(int64(k))), verify.AndOf(eq...)))
		}
	}
	return verify.AndOf(same...), exact
}

// exactKind reports whether equal gives the equality of value x.
func exactKind(x val) bool {
	switch x.kind {
	case vInt, vBool:
		return true
	case vStruct:
		for _, y := range x.fields {
			if !exactKind(y) {
				return false
			}
		}
		return true
	case vEnum:
		for _, c := range x.cases {
			for _, y := range c {
				if !exactKind(y) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// enumOf returns the enum type of t, or nil.
func enumOf(t types.Type) *types.Enum {
	if t == nil {
		return nil
	}
	e, _ := t.Underlying().(*types.Enum)
	return e
}

// structOf returns the struct type of t, or nil.
func structOf(t types.Type) *types.Struct {
	s, _ := core(t).(*types.Struct)
	return s
}

// newSeq returns a new sequence of unknown elements.
func (f *fn) newSeq() int {
	f.nseq++
	return f.nseq
}

// prefix returns the sum of the elements of seq before element t: a
// function P with P(0) = 0 that the elements define, P(j+1) - P(j) being
// element j. An application gets a variable per term, and the facts that
// it equals the other applications at equal arguments.
func (f *fn) prefix(st *state, seq int, t verify.Term) verify.Term {
	return st.def(fmt.Sprintf("prefix%d", seq), t, func() []verify.Term {
		p := f.newVar("sum")
		k, isConst := isConstTerm(t)
		if ps := f.known[seq]; isConst && k.Sign() >= 0 && k.Cmp(big.NewInt(int64(len(ps)))) < 0 {
			p = verify.ConstBig(ps[k.Int64()])
		} else if ps := f.known[seq]; ps != nil && !isConst && len(ps) <= maxKnown {
			// One of the known sums, or outside them.
			alts := []verify.Formula{verify.Cmp(t, verify.LT, cint(0)), verify.Cmp(t, verify.GE, cint(int64(len(ps))))}
			for j, v := range ps {
				alts = append(alts, verify.AndOf(verify.Cmp(t, verify.EQ, cint(int64(j))), verify.Cmp(p, verify.EQ, verify.ConstBig(v))))
			}
			st.assume(verify.OrOf(alts...))
		} else if isConst && k.Sign() == 0 {
			p = cint(0)
		} else if n, ok := f.zeroSeq[seq]; ok {
			// Zero within the length; unknown outside (contract.expr.elements).
			st.assume(verify.OrOf(verify.Cmp(t, verify.LT, cint(0)), verify.Cmp(t, verify.GT, n), verify.Cmp(p, verify.EQ, cint(0))))
		} else if u, ok := f.updates[seq]; ok {
			b := f.prefix(st, u.base, t)
			st.assume(verify.OrOf(
				verify.AndOf(verify.Cmp(t, verify.LE, u.at), verify.Cmp(p, verify.EQ, b)),
				verify.AndOf(verify.Cmp(t, verify.GT, u.at), verify.Cmp(p, verify.EQ, b.Add(u.delta)))))
		}
		for _, a := range st.apps[seq] {
			if _, ok := isConstTerm(t.Sub(a[0])); ok {
				continue // different arguments
			}
			f.congr = append(f.congr, verify.OrOf(verify.Cmp(t, verify.NE, a[0]), verify.Cmp(p, verify.EQ, a[1])))
		}
		if st.apps == nil {
			st.apps = map[int][][2]verify.Term{}
		}
		st.apps[seq] = append(st.apps[seq], [2]verify.Term{t, p})
		return []verify.Term{p}
	})[0]
}

// store returns sequence x with element j, of integer type t, set to v.
func (f *fn) store(st *state, x val, j, v verify.Term, t types.Type) val {
	s := f.newSeq()
	if f.updates == nil {
		f.updates = map[int]update{}
	}
	f.updates[s] = update{base: x.seq, at: x.off.Add(j), delta: v.Sub(f.element(st, x, j, t))}
	x.seq = s
	return x
}

// element returns element j of sequence x, of integer type t.
func (f *fn) element(st *state, x val, j verify.Term, t types.Type) verify.Term {
	at := x.off.Add(j)
	if len(st.reads) < 4*maxInstances {
		st.reads = append(st.reads[:len(st.reads):len(st.reads)], at)
	}
	e := f.prefix(st, x.seq, at.Add(cint(1))).Sub(f.prefix(st, x.seq, at))
	f.inRange(st, e, t)
	return e
}

// sum returns the sum of the elements of sequence x.
func (f *fn) sum(st *state, x val) verify.Term {
	return f.prefix(st, x.seq, x.off.Add(x.t)).Sub(f.prefix(st, x.seq, x.off))
}

// zero returns the zero value of type t.
func (f *fn) zero(t types.Type) val {
	switch {
	case isInt(t):
		return val{kind: vInt, t: verify.Const(0)}
	case isBool(t):
		return val{kind: vBool, f: verify.Formula{Kind: verify.False}}
	}
	if s := structOf(t); s != nil {
		x := val{kind: vStruct, fields: make([]val, len(s.Fields))}
		for i, fl := range s.Fields {
			x.fields[i] = f.zero(fl.Type())
		}
		return x
	}
	if en := enumOf(t); en != nil {
		return f.variant(t, 0, nil) // the first variant (type.enum.zero)
	}
	if n := seqLen(t); n >= -1 {
		if n < 0 {
			n = 0
		}
		s := f.newSeq()
		if f.zeroSeq == nil {
			f.zeroSeq = map[int]verify.Term{}
		}
		f.zeroSeq[s] = verify.Const(n)
		return val{kind: vSeq, t: verify.Const(n), seq: s, off: cint(0)}
	}
	return val{}
}

// tracked reports whether variable v has a value in the states: a
// parameter, a result or a local variable of the function that no
// function literal captures.
func (f *fn) tracked(v *types.Var) bool {
	return f.locals[v] && !f.captured[v] && (isInt(v.Type()) || isBool(v.Type()) || seqLen(v.Type()) >= -1 || (structOf(v.Type()) != nil || enumOf(v.Type()) != nil) && leaves(v.Type(), 0) <= maxLeaves)
}

func (f *fn) read(st *state, v *types.Var) val {
	if x, ok := st.vars[v]; ok {
		return x
	}
	var x val
	if v.IsPackageLevel() {
		x = f.received(st, v.Type(), v.Name())
	} else {
		x = f.fresh(st, v.Type(), v.Name())
	}
	if f.tracked(v) {
		st.vars[v] = x
	}
	return x
}

func (f *fn) write(st *state, v *types.Var, x val) {
	if !f.tracked(v) {
		return
	}
	if x.kind == vNone {
		delete(st.vars, v)
		return
	}
	st.vars[v] = x
}

// constant returns the value of a constant integer expression.
func (f *fn) constant(e syntax.Expr) (*big.Int, bool) {
	tv, ok := f.info.Types[e]
	if !ok || tv.Value == nil {
		return nil, false
	}
	v := constant.ToInt(tv.Value)
	if v.Kind() != constant.Int {
		return nil, false
	}
	return new(big.Int).SetString(v.ExactString(), 10)
}

func (f *fn) typeOf(e syntax.Expr) types.Type { return f.info.Types[e].Type }

// ---------------------------------------------------------------------
// Obligations.

// relevant returns the facts that share variables with the goal, directly
// or through other facts; vars are the variables of each fact.
func relevant(facts []verify.Formula, vars [][]int, goal verify.Formula) []verify.Formula {
	byVar := map[int][]int{}
	for i, vs := range vars {
		for _, v := range vs {
			byVar[v] = append(byVar[v], i)
		}
	}
	used := make([]bool, len(facts))
	seen := map[int]bool{}
	queue := termVars(goal)
	for _, v := range queue {
		seen[v] = true
	}
	for len(queue) > 0 {
		v := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, i := range byVar[v] {
			if used[i] {
				continue
			}
			used[i] = true
			for _, w := range vars[i] {
				if !seen[w] {
					seen[w] = true
					queue = append(queue, w)
				}
			}
		}
	}
	var out []verify.Formula
	for i, g := range facts {
		if used[i] {
			out = append(out, g)
		}
	}
	return out
}

// prove proves goal from the facts of st within limit steps and returns
// the counterexample of a refutation. The problem has only the variables
// of the goal and of the facts that are relevant to it. The congruences
// of the prefix sums and the instances of the quantifiers are added as
// the models of the solver need them: a fact that a model violates, or
// does not decide, with a variable of the model, joins the problem, until
// the solver proves the goal or a model satisfies them all.
func (f *fn) prove(st *state, goal verify.Formula, pos syntax.Pos, limit int) (bool, []*big.Int) {
	if st.dead {
		return true, nil // the point cannot be reached
	}
	base, vars := st.facts, st.fvars
	lazy := f.congr
	if len(st.quants) > 0 {
		side, insts := f.instantiate(st)
		base = append(base[:len(base):len(base)], side...)
		for _, g := range side {
			vars = append(vars[:len(vars):len(vars)], termVars(g))
		}
		lazy = append(lazy[:len(lazy):len(lazy)], insts...)
	}
	chosen := make([]bool, len(lazy))
	var facts []verify.Formula
	for round := 0; ; round++ {
		all, allVars := base[:len(base):len(base)], vars[:len(vars):len(vars)]
		for i, c := range chosen {
			if c {
				all = append(all, lazy[i])
				allVars = append(allVars, termVars(lazy[i]))
			}
		}
		facts = relevant(all, allVars, goal)
		ok, model := f.solve(facts, goal, pos, limit)
		if ok || model == nil || round == maxRounds || limit <= checkSteps && round == checkRounds {
			return ok, model
		}
		added := false
		for i, g := range lazy {
			if chosen[i] {
				continue
			}
			if v, known, touches := evalFormula(g, model); touches && (!known || !v) {
				chosen[i], added = true, true
			}
		}
		if !added {
			return false, model
		}
	}
}

// maxRounds bounds the rounds of prove that add facts, and checkRounds
// those of a run-time check, which stays in the program if it is not
// proven.
const (
	maxRounds   = 16
	checkRounds = 2
)

// solve proves goal from facts within limit steps and returns the
// counterexample of a refutation: the values of the variables of the
// problem, nil for the others.
func (f *fn) solve(facts []verify.Formula, goal verify.Formula, pos syntax.Pos, limit int) (bool, []*big.Int) {
	index := map[int]int{}
	var names []string
	var renumber func(g verify.Formula) verify.Formula
	term := func(t verify.Term) verify.Term {
		r := verify.Term{Const: t.Const}
		for _, c := range t.Coefs {
			k, ok := index[c.Var]
			if !ok {
				k = len(names)
				index[c.Var] = k
				names = append(names, f.names[c.Var])
			}
			r.Coefs = append(r.Coefs, verify.Coef{Var: k, C: c.C})
		}
		sort.Slice(r.Coefs, func(i, j int) bool { return r.Coefs[i].Var < r.Coefs[j].Var })
		return r
	}
	renumber = func(g verify.Formula) verify.Formula {
		r := verify.Formula{Kind: g.Kind, Rel: g.Rel}
		if g.Kind == verify.Atom {
			r.L, r.R = term(g.L), term(g.R)
		}
		for _, a := range g.Args {
			r.Args = append(r.Args, renumber(a))
		}
		return r
	}
	p := &verify.Problem{Goal: renumber(goal)}
	for _, g := range facts {
		p.Facts = append(p.Facts, renumber(g))
	}
	p.Vars = names
	r := verify.Solve(p, limit)
	switch r.Status {
	case verify.Proved:
		if err := check.Check(p, r.Proof); err != nil {
			f.errorf(pos, "contract.solver.certificate", "internal error: the checker rejects the certificate of the solver: %v", err)
			return false, nil
		}
		return true, nil
	case verify.Refuted:
		model := make([]*big.Int, len(f.names))
		for v, k := range index {
			if k < len(r.Model) {
				model[v] = r.Model[k]
			}
		}
		return false, model
	}
	return false, nil
}

// evalFormula evaluates g with the values of model, nil for a variable
// without one: it returns the value, whether the values decide it, and
// whether g has a variable with a value.
func evalFormula(g verify.Formula, model []*big.Int) (value, known, touches bool) {
	switch g.Kind {
	case verify.True:
		return true, true, false
	case verify.False:
		return false, true, false
	case verify.Not:
		v, k, t := evalFormula(g.Args[0], model)
		return !v, k, t
	case verify.And, verify.Or:
		and := g.Kind == verify.And
		value, known = and, true
		for _, a := range g.Args {
			v, k, t := evalFormula(a, model)
			touches = touches || t
			switch {
			case k && v != and:
				// One argument decides: false for And, true for Or.
				value, known = v, true
				for _, b := range g.Args {
					_, _, t := evalFormula(b, model)
					touches = touches || t
				}
				return value, known, touches
			case !k:
				known = false
			}
		}
		return value, known, touches
	}
	l, kl, tl := evalKnown(g.L, model)
	r, kr, tr := evalKnown(g.R, model)
	if !kl || !kr {
		return false, false, tl || tr
	}
	c := l.Cmp(r)
	switch g.Rel {
	case verify.LE:
		value = c <= 0
	case verify.LT:
		value = c < 0
	case verify.EQ:
		value = c == 0
	case verify.NE:
		value = c != 0
	case verify.GE:
		value = c >= 0
	case verify.GT:
		value = c > 0
	}
	return value, true, tl || tr
}

// evalKnown evaluates t with the values of model, nil for a variable
// without one: it returns whether they decide t and whether t has one.
func evalKnown(t verify.Term, model []*big.Int) (*big.Int, bool, bool) {
	v := new(big.Int)
	if t.Const != nil {
		v.Set(t.Const)
	}
	known, touches := true, false
	for _, c := range t.Coefs {
		if c.Var >= len(model) || model[c.Var] == nil {
			known = false
			continue
		}
		touches = true
		v.Add(v, new(big.Int).Mul(c.C, model[c.Var]))
	}
	return v, known, touches
}

// checkSteps is the step limit of a run-time check: a check that is not
// proven stays in the program.
const checkSteps = 5000

// runtimeCheck proves a run-time check of node n; the check holds after
// the node in any case.
func (f *fn) runtimeCheck(st *state, n syntax.Node, cond verify.Formula) {
	ok, _ := f.prove(st, cond, n.Pos(), checkSteps)
	if old, seen := f.proven[n]; !seen || old {
		f.proven[n] = ok
	}
	st.assume(cond)
}

// unprovable records that the check of node n is not proven.
func (f *fn) unprovable(n syntax.Node) { f.proven[n] = false }

// evalTerm returns the value of term t under a model, with 0 for a
// variable without a value.
func evalTerm(t verify.Term, model []*big.Int) *big.Int {
	v := new(big.Int)
	if t.Const != nil {
		v.Set(t.Const)
	}
	for _, c := range t.Coefs {
		if c.Var < len(model) && model[c.Var] != nil {
			v.Add(v, new(big.Int).Mul(c.C, model[c.Var]))
		}
	}
	return v
}

// counterexample describes the values of the variables named in e under
// a model, with env for the variables of a contract.
func (f *fn) counterexample(st *state, e syntax.Expr, env map[*types.Var]val, model []*big.Int) string {
	if model == nil {
		return ""
	}
	seen := map[string]bool{}
	var parts []string
	syntax.Walk(e, func(n syntax.Node) bool {
		id, ok := n.(*syntax.Name)
		if !ok {
			return true
		}
		v, ok := f.info.Uses[id].(*types.Var)
		if !ok || seen[v.Name()] {
			return true
		}
		x, ok := env[v]
		if !ok {
			x, ok = st.vars[v]
		}
		if !ok {
			return true
		}
		seen[v.Name()] = true
		switch x.kind {
		case vInt:
			parts = append(parts, fmt.Sprintf("%s = %s", v.Name(), evalTerm(x.t, model)))
		case vSeq:
			parts = append(parts, fmt.Sprintf("len(%s) = %s", v.Name(), evalTerm(x.t, model)))
		}
		return true
	})
	if len(parts) == 0 {
		return ""
	}
	return ", for example when " + strings.Join(parts, ", ")
}

// obligation proves a contract obligation and reports it if it is not
// proven.
func (f *fn) obligation(st *state, goal verify.Formula, pos syntax.Pos, e syntax.Expr, env map[*types.Var]val, what string) {
	ok, model := f.prove(st, goal, pos, verify.DefaultSteps)
	if !ok {
		f.errorf(pos, "contract.obligation.unproven", "%s is not proven%s", what, f.counterexample(st, e, env, model))
	}
}

// checkEnsures proves the postconditions at a return with the values of
// the results.
func (f *fn) checkEnsures(st *state, pos syntax.Pos, results []val) {
	for _, c := range f.ensures {
		env := map[*types.Var]val{}
		for p, v := range f.entry {
			if !p.Inout() {
				env[p] = v
			}
		}
		for i, r := range f.results {
			if i < len(results) {
				env[r] = results[i]
			}
		}
		if rv := f.info.ContractResults[c]; rv != nil && len(results) == 1 {
			env[rv] = results[0]
		}
		if g, ok := f.contractGoal(f.info, st, c.X, env, true); ok {
			f.obligation(st, g, pos, c.X, env, fmt.Sprintf("the postcondition %s of %s", exprString(c.X), f.name))
		}
	}
}

func exprString(e syntax.Expr) string { return types.ExprString(e) }

// core returns the core type of t, or nil.
func core(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	return types.CoreType(t)
}

// maxRecursionDepth is the largest bound on the depth of a recursion: a
// parameter with a larger range bounds nothing (rt.stack.core-recursion).
const maxRecursionDepth = 1 << 20

// recursionDepth returns a bound on the depth of the recursion of a
// function that calls itself, or 0 (rt.stack.core-recursion): an integer
// parameter that every call of the function in its body passes a smaller
// value than it has at entry, and that lies in [lo, hi] at entry, bounds
// the depth by hi - lo + 1. params are the receiver and the parameters.
func (f *fn) recursionDepth(params []*types.Var) int {
	if len(f.recCalls) == 0 {
		return 0
	}
	best := 0
	for i, p := range params {
		j := i // the index of p among the values of a call
		if f.sig.Recv == nil {
			j++
		}
		if !isInt(p.Type()) || f.sig.Variadic && p == params[len(params)-1] {
			continue
		}
		lo, hi, ok := f.info.IntBounds(p.Type())
		if !ok {
			continue
		}
		lo, hi = f.requiredBounds(p, lo, hi)
		decreases := true
		for _, c := range f.recCalls {
			if j >= len(c.args) || c.args[j].kind != vInt {
				decreases = false
				break
			}
			if ok, _ := f.prove(c.st, verify.Cmp(c.args[j].t, verify.LT, f.entry[p].t), f.body.Lbrace, checkSteps); !ok {
				decreases = false
				break
			}
		}
		if !decreases {
			continue
		}
		n := new(big.Int).Sub(hi, lo)
		n.Add(n, oneInt)
		if !n.IsInt64() || n.Int64() > maxRecursionDepth {
			continue
		}
		d := int(n.Int64())
		if best == 0 || d < best {
			best = d
		}
	}
	return best
}

// requiredBounds narrows the range [lo, hi] of parameter p by the
// preconditions of the form p op c and c op p with a constant c.
func (f *fn) requiredBounds(p *types.Var, lo, hi *big.Int) (*big.Int, *big.Int) {
	lo, hi = new(big.Int).Set(lo), new(big.Int).Set(hi)
	var conj func(e syntax.Expr)
	conj = func(e syntax.Expr) {
		b, ok := unparen(e).(*syntax.BinaryExpr)
		if !ok {
			return
		}
		if b.Op == syntax.LAnd {
			conj(b.X)
			conj(b.Y)
			return
		}
		op, x, y := b.Op, unparen(b.X), unparen(b.Y)
		if id, ok := y.(*syntax.Name); ok && f.info.Uses[id] == p {
			x, y = y, x
			op = map[syntax.Kind]syntax.Kind{syntax.Lss: syntax.Gtr, syntax.Leq: syntax.Geq, syntax.Gtr: syntax.Lss, syntax.Geq: syntax.Leq, syntax.Eql: syntax.Eql}[op]
		}
		id, ok := x.(*syntax.Name)
		if !ok || f.info.Uses[id] != p {
			return
		}
		k, ok := f.constant(y)
		if !ok {
			return
		}
		switch op {
		case syntax.Lss:
			k.Sub(k, oneInt)
			fallthrough
		case syntax.Leq:
			if k.Cmp(hi) < 0 {
				hi = k
			}
		case syntax.Gtr:
			k.Add(k, oneInt)
			fallthrough
		case syntax.Geq:
			if k.Cmp(lo) > 0 {
				lo = k
			}
		case syntax.Eql:
			lo, hi = new(big.Int).Set(k), k
		}
	}
	for _, s := range f.body.List {
		c, ok := s.(*syntax.ContractStmt)
		if !ok {
			break
		}
		if c.Word == "requires" {
			conj(c.X)
		}
	}
	return lo, hi
}
