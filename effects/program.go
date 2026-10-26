package effects

import (
	"sort"
	"strings"

	"an/syntax"
	"an/types"
)

// Program joins the analyzed packages of a program for whole-program
// substitution (spec/22-compiler.md §22.5, step 2).
type Program struct {
	Packages []*Package
	funcs    map[string]*Summary
	impls    map[Ref]map[string]bool
	flows    map[Ref]map[Ref]bool
	exec     map[string]map[Effect]Origin
	resolved map[Ref]map[Effect]Origin
}

// NewProgram joins the packages and computes the execution effects of
// every function (eff.attr.resolve, eff.attr.exec).
func NewProgram(pkgs []*Package) *Program {
	p := &Program{
		Packages: pkgs,
		funcs:    map[string]*Summary{},
		impls:    map[Ref]map[string]bool{},
		flows:    map[Ref]map[Ref]bool{},
		exec:     map[string]map[Effect]Origin{},
		resolved: map[Ref]map[Effect]Origin{},
	}
	for _, pkg := range pkgs {
		for k, s := range pkg.Funcs {
			p.funcs[k] = s
		}
		for r, ks := range pkg.Impls {
			if p.impls[r] == nil {
				p.impls[r] = map[string]bool{}
			}
			for k := range ks {
				p.impls[r][k] = true
			}
		}
		for r, rs := range pkg.Flows {
			if p.flows[r] == nil {
				p.flows[r] = map[Ref]bool{}
			}
			for f := range rs {
				p.flows[r][f] = true
			}
		}
	}
	p.solve()
	return p
}

// Summary returns the summary of the function key, or nil.
func (p *Program) Summary(key string) *Summary { return p.funcs[key] }

// Keys returns the keys of all functions, sorted.
func (p *Program) Keys() []string {
	var keys []string
	for k := range p.funcs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Exec returns the execution effects of a call of the function key with
// unknown arguments: its parametric references are taken as open
// references of the parameter types.
func (p *Program) Exec(key string) map[Effect]Origin { return p.exec[key] }

// Resolve returns the effects that open reference r resolves to.
func (p *Program) Resolve(r Ref) map[Effect]Origin { return p.resolved[r] }

// refs returns the open references of s, with the parametric ones
// closed.
func refs(s *Summary) []Ref {
	rs := sortedRefs(s.Q)
	for _, r := range sortedRefs(s.P) {
		if c, ok := closeRef(s, r); ok {
			rs = append(rs, c)
		}
	}
	return rs
}

// implsOf returns the implementations of open reference r, including
// those that flow into it.
func (p *Program) implsOf(r Ref) []string {
	seen := map[Ref]bool{}
	keys := map[string]bool{}
	var visit func(Ref)
	visit = func(r Ref) {
		if seen[r] {
			return
		}
		seen[r] = true
		for k := range p.impls[r] {
			keys[k] = true
		}
		for f := range p.flows[r] {
			visit(f)
		}
	}
	visit(r)
	var out []string
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (p *Program) solve() {
	keys := p.Keys()
	for _, k := range keys {
		x := map[Effect]Origin{}
		for e, o := range p.funcs[k].C {
			x[e] = o
		}
		p.exec[k] = x
	}
	impls := map[Ref][]string{}
	for changed := true; changed; {
		changed = false
		for _, k := range keys {
			s := p.funcs[k]
			for _, r := range refs(s) {
				for e, o := range p.resolve(r, impls) {
					if _, ok := p.exec[k][e]; !ok {
						p.exec[k][e] = Origin{Via: o.Via, Pos: o.Pos, What: "through " + r.String()}
						changed = true
					}
				}
			}
		}
	}
	for r, ks := range impls {
		res := map[Effect]Origin{}
		for _, k := range ks {
			for _, e := range sortedEffects(p.exec[k]) {
				if _, ok := res[e]; !ok {
					res[e] = Origin{Via: k}
				}
			}
		}
		p.resolved[r] = res
	}
}

func (p *Program) resolve(r Ref, cache map[Ref][]string) map[Effect]Origin {
	ks, ok := cache[r]
	if !ok {
		ks = p.implsOf(r)
		cache[r] = ks
	}
	res := map[Effect]Origin{}
	for _, k := range ks {
		for _, e := range sortedEffects(p.exec[k]) {
			if _, ok := res[e]; !ok {
				res[e] = Origin{Via: k}
			}
		}
	}
	return res
}

// Chain returns the call chain from the function key to a source of
// effect e, ending with the description of the source, for diagnostics
// (eff.constraint.report). With exec set, it follows execution effects,
// otherwise own effects.
func (p *Program) Chain(key string, e Effect, exec bool) []string {
	chain := []string{shortKey(key)}
	seen := map[string]bool{key: true}
	for {
		var o Origin
		var ok bool
		if exec {
			o, ok = p.exec[key][e]
		} else if s := p.funcs[key]; s != nil {
			if o, ok = s.C[e]; !ok {
				o, ok = s.D[e] // a deferred effect
			}
		}
		if !ok {
			return append(chain, e.Name())
		}
		if o.Via == "" || seen[o.Via] {
			what := o.What
			if what == "" {
				what = e.Name()
			}
			if o.Pos != "" {
				what += " at " + o.Pos
			}
			return append(chain, what)
		}
		seen[o.Via] = true
		key = o.Via
		chain = append(chain, shortKey(key))
	}
}

// shortKey drops the package path of a key, keeping the package name.
func shortKey(key string) string {
	if i := strings.LastIndexByte(key[:dotBeforeName(key)], '/'); i >= 0 {
		return key[i+1:]
	}
	return key
}

// dotBeforeName returns the index of the dot after the package path.
func dotBeforeName(key string) int {
	slash := strings.LastIndexByte(key, '/')
	if i := strings.IndexByte(key[slash+1:], '.'); i >= 0 {
		return slash + 1 + i
	}
	return len(key)
}

// Expr returns the execution effects of evaluating expression e of the
// package described by info, as for a function without parameters whose
// body is e: the effects of its operations and the execution effects of
// the functions it calls (ct.eval.evaluable). It also returns the chain
// to a source of each effect.
func (p *Program) Expr(info *types.Info, e syntax.Expr) (map[Effect]Origin, func(Effect) []string) {
	return p.Node(info, e)
}

// Node returns the effects of a syntax node of the package of info, such
// as an expression or a statement, and a function that gives the call
// chain to the source of each.
func (p *Program) Node(info *types.Info, e syntax.Node) (map[Effect]Origin, func(Effect) []string) {
	a := &analyzer{
		info:     info,
		imported: p.Summary,
		byKey:    map[string]*fnFacts{},
		litKeys:  map[*syntax.FuncLit]string{},
		pkg:      &Package{Path: info.Pkg.Path, Funcs: map[string]*Summary{}, Impls: map[Ref]map[string]bool{}, Flows: map[Ref]map[Ref]bool{}, Without: map[string][]Effect{}},
	}
	key := info.Pkg.Path + ".constant"
	fn := a.newFn(key, &types.Signature{Params: types.NewTuple(), Results: types.NewTuple()}, nil)
	w := &walker{a: a, fn: fn, callees: map[syntax.Expr]bool{}, commaOk: map[syntax.Expr]bool{}, comms: map[syntax.Node]bool{}}
	syntax.Walk(e, w.visit)
	a.solve()
	x := map[Effect]Origin{}
	for e, o := range fn.sum.C {
		x[e] = o
	}
	for _, r := range refs(fn.sum) {
		for e, o := range p.resolveRef(r) {
			if _, ok := x[e]; !ok {
				x[e] = o
			}
		}
	}
	chain := func(e Effect) []string {
		o := x[e]
		if o.Via == "" {
			what := o.What
			if o.Pos != "" {
				what += " at " + o.Pos
			}
			return []string{what}
		}
		return p.Chain(o.Via, e, true)
	}
	return x, chain
}
