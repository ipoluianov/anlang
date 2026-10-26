// Package effects infers the effects of functions (spec/07-effects.md,
// spec/22-compiler.md §22.5).
//
// Inference has two steps. Analyze computes the effect summaries of the
// functions of one package from its typed syntax tree and the summaries
// of the packages it imports; the result depends on nothing else, so it
// can be cached by the hash of the package and of its imports
// (eff.infer.modular). NewProgram joins the packages of a program and
// resolves open references to compute execution effects
// (eff.attr.resolve, eff.attr.exec).
package effects

import (
	"fmt"
	"sort"
	"strings"

	"an/syntax"
)

// Effect is an effect, written "IDENTITY#NAME": the identity is the
// qualified name of the declaring constant (eff.model.identity), NAME the
// bare name (eff.model.names).
type Effect string

// Name returns the bare name of the effect.
func (e Effect) Name() string {
	if i := strings.LastIndexByte(string(e), '#'); i >= 0 {
		return string(e[i+1:])
	}
	return string(e)
}

// Base returns the base effect named name (eff.base.set).
func Base(name string) Effect { return Effect("an/effect." + name + "#" + name) }

// The base effects with sources in the language.
var (
	Alloc   = Base("Alloc")
	Panic   = Base("Panic")
	Block   = Base("Block")
	Tasks   = Base("Tasks")
	Global  = Base("Global")
	Console = Base("Console")
	Foreign = Base("Foreign")
)

// RefKind is the kind of a parametric or open reference.
type RefKind int

const (
	// Parametric references (eff.infer.summary, P).
	ParamCall       RefKind = iota // effects of calling parameter Index
	ParamMethod                    // effects of method Method of parameter Index
	TypeParamMethod                // effects of method Method of type parameter Index
	// Open references (Q).
	OpenMethod // effects of method Method of interface type Type
	OpenFunc   // effects of calling a value of function type Type
)

// Ref is a parametric or open reference. Parameter indices count the
// parameters of the signature; the receiver is -1.
type Ref struct {
	Kind   RefKind
	Index  int
	Method string
	Type   string
}

func (r Ref) open() bool { return r.Kind >= OpenMethod }

// String writes the reference as the tools show it, with the parameter
// and type parameter names of s if s is not nil.
func (r Ref) string(s *Summary) string {
	param := func(names []string) string {
		if r.Index == -1 {
			return "receiver"
		}
		if s != nil && r.Index < len(names) && names[r.Index] != "" {
			return names[r.Index]
		}
		return fmt.Sprintf("#%d", r.Index)
	}
	switch r.Kind {
	case ParamCall:
		var names []string
		if s != nil {
			names = s.Params
		}
		return param(names)
	case ParamMethod:
		var names []string
		if s != nil {
			names = s.Params
		}
		return param(names) + "." + r.Method
	case TypeParamMethod:
		var names []string
		if s != nil {
			names = s.TypeParams
		}
		return param(names) + "." + r.Method
	case OpenMethod:
		return "(" + r.Type + ")." + r.Method
	}
	return "(" + r.Type + ")()"
}

func (r Ref) String() string { return r.string(nil) }

// Origin records why a summary contains an effect or reference: a call
// of the function Via, or a source described by What at Pos.
type Origin struct {
	Via  string
	Pos  string
	What string
}

// Convert is a conversion of a value of type parameter Index to the
// interface type Iface with the methods Methods, which records
// implementations when the function is instantiated (eff.infer.rules 6).
type Convert struct {
	Index   int
	Iface   string
	Methods []string
}

// Summary is the effect summary Σ(f) = (C, P, Q) of a function
// (eff.infer.summary), with the deferred effects of eff.deferred.declare.
type Summary struct {
	Key        string
	Pos        syntax.Pos
	Params     []string // parameter names, for the tools
	TypeParams []string
	// ParamKeys are the interface or function type keys of the
	// parameters, which close parametric references to open ones.
	ParamKeys map[int]string
	// Without lists the effects of the function's clauses ensures
	// effect.Without (eff.without.form).
	Without  []Effect
	C        map[Effect]Origin
	P        map[Ref]Origin
	Q        map[Ref]Origin
	D        map[Effect]Origin
	Converts []Convert
}

func newSummary(key string) *Summary {
	return &Summary{Key: key, C: map[Effect]Origin{}, P: map[Ref]Origin{}, Q: map[Ref]Origin{}, D: map[Effect]Origin{}}
}

// Own returns the own effects of the function (eff.attr.own), sorted by
// name.
func (s *Summary) Own() []Effect { return sortedEffects(s.C) }

func sortedEffects(m map[Effect]Origin) []Effect {
	var es []Effect
	for e := range m {
		es = append(es, e)
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].Name() != es[j].Name() {
			return es[i].Name() < es[j].Name()
		}
		return es[i] < es[j]
	})
	return es
}

func sortedRefs(m map[Ref]Origin) []Ref {
	var rs []Ref
	for r := range m {
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool { return refLess(rs[i], rs[j]) })
	return rs
}

func refLess(a, b Ref) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Index != b.Index {
		return a.Index < b.Index
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.Method < b.Method
}

// String writes the summary in a canonical form.
func (s *Summary) String() string {
	var b strings.Builder
	b.WriteString(s.Key)
	b.WriteString(":")
	for _, e := range s.Own() {
		b.WriteString(" " + e.Name())
	}
	if len(s.P) > 0 {
		b.WriteString("; params:")
		for _, r := range sortedRefs(s.P) {
			b.WriteString(" " + r.string(s))
		}
	}
	if len(s.Q) > 0 {
		b.WriteString("; open:")
		for _, r := range sortedRefs(s.Q) {
			b.WriteString(" " + r.String())
		}
	}
	if len(s.D) > 0 {
		b.WriteString("; deferred:")
		for _, e := range sortedEffects(s.D) {
			b.WriteString(" " + e.Name())
		}
	}
	return b.String()
}

// Package is the result of analyzing one package.
type Package struct {
	Path  string
	Hash  string
	Funcs map[string]*Summary
	// Impls records, for each open reference, the functions that
	// implement it (eff.infer.rules 6, 7).
	Impls map[Ref]map[string]bool
	// Flows records, for each open reference, the open references whose
	// implementations flow into it through conversions between
	// interface or function types.
	Flows map[Ref]map[Ref]bool
	// Without lists the constraints ensures effect.Without of functions
	// (eff.without.form).
	Without map[string][]Effect
	// Calls are the static calls of functions with ensures
	// effect.Without, for the check at each call (eff.without.check).
	Calls []CallSite
}

// CallSite is a static call of a function with ensures effect.Without.
// The execution effects of the call are those of the callee without its
// parametric references, plus the execution effects of the functions
// Keys and the effects that the open references Refs resolve to, which
// the arguments contribute.
type CallSite struct {
	Caller, Callee string
	Pos            syntax.Pos
	Keys           []string
	Refs           []Ref
}

// Keys returns the keys of the functions of the package, sorted.
func (p *Package) Keys() []string {
	var keys []string
	for k := range p.Funcs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// String writes the result in a canonical form, which is equal for
// equal results.
func (p *Package) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n", p.Path)
	for _, k := range p.Keys() {
		b.WriteString(p.Funcs[k].String() + "\n")
	}
	var refs []Ref
	for r := range p.Impls {
		refs = append(refs, r)
	}
	sort.Slice(refs, func(i, j int) bool { return refLess(refs[i], refs[j]) })
	for _, r := range refs {
		var impls []string
		for k := range p.Impls[r] {
			impls = append(impls, k)
		}
		sort.Strings(impls)
		fmt.Fprintf(&b, "impl %s: %s\n", r, strings.Join(impls, " "))
	}
	refs = refs[:0]
	for r := range p.Flows {
		refs = append(refs, r)
	}
	sort.Slice(refs, func(i, j int) bool { return refLess(refs[i], refs[j]) })
	for _, r := range refs {
		var from []string
		for f := range p.Flows[r] {
			from = append(from, f.String())
		}
		sort.Strings(from)
		fmt.Fprintf(&b, "flow %s: %s\n", r, strings.Join(from, " "))
	}
	return b.String()
}
