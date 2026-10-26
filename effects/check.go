package effects

import (
	"fmt"
	"sort"
	"strings"

	"an/modfile"
	"an/syntax"
)

// Constraints are the boundaries at which a program's effects are
// constrained (spec/07-effects.md §7.6).
type Constraints struct {
	Module   string
	ModFile  string               // name of anlang.mod for diagnostics
	Rules    []modfile.EffectRule // the effects directive of anlang.mod
	Profiles map[string][]string  // the profiles of each package
	Known    map[string]bool      // bare names of the effects declared in the program
	Main     string               // key of func main
	// The target: its environment effects, if its description is known.
	Target   string
	Provides []string
	Lacks    []string
	HasDesc  bool
}

// Environment are the environment effects (target.effects.environment).
var Environment = map[string]bool{
	"Alloc": true, "StaticAlloc": true, "Console": true, "File": true, "Network": true, "Video": true,
	"Keyboard": true, "ReadInput": true, "WriteOutput": true, "Tasks": true, "Interrupts": true,
	"Gpio": true, "Uart": true, "Timers": true, "Clock": true, "Random": true, "Privileged": true,
	"GPU": true, "Camera": true, "Location": true, "Notifications": true, "Contacts": true,
	"Photos": true, "Bluetooth": true, "Background": true,
}

// LibraryEffects are the effects that the standard library declares with
// effect.Declare (eff.base.library). Temporary: they are known by name
// until those packages are compiled from Anlang source.
var LibraryEffects = []string{"Detach", "ExposeSecret", "SQL", "Privileged", "GPU", "ReadInput", "WriteOutput",
	"Camera", "Location", "Notifications", "Contacts", "Photos", "Bluetooth", "Background"}

// hardware are the hardware effect classes (eff.base.set, group Hardware).
var hardware = map[string]bool{"Gpio": true, "Uart": true, "Timers": true}

// forbids reports whether profile p forbids effect e (eff.profile.allowed).
func forbids(p string, e Effect) bool {
	switch p {
	case "core":
		return e == Alloc || e == "an/task.Detach#Detach"
	case "app", "ui":
		return e == Base("Interrupts") || e.Name() == "Privileged" && strings.HasPrefix(string(e), "an/arch/") ||
			hardware[e.Name()] && strings.HasPrefix(string(e), "an/effect.")
	}
	return false
}

// forbidsName reports whether profile p forbids the effects named name.
func forbidsName(p, name string) bool {
	switch p {
	case "core":
		return name == "Alloc" || name == "Detach"
	case "app", "ui":
		return name == "Interrupts" || name == "Privileged" || hardware[name]
	}
	return false
}

// Check checks the effects of the program against the constraints and
// returns the violations (eff.constraint.report).
func (p *Program) Check(c Constraints) []*syntax.Error {
	ck := &checker{p: p, c: c}
	ck.mod()
	for _, pkg := range p.Packages {
		ck.pkg(pkg)
	}
	ck.target()
	sort.SliceStable(ck.errs, func(i, j int) bool {
		a, b := ck.errs[i].Pos, ck.errs[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Offset < b.Offset || a.Offset == b.Offset && a.Line < b.Line
	})
	return ck.errs
}

type checker struct {
	p    *Program
	c    Constraints
	errs []*syntax.Error
}

func (ck *checker) errorf(pos syntax.Pos, rule, format string, args ...any) {
	ck.errs = append(ck.errs, &syntax.Error{Pos: pos, Rule: rule, Msg: fmt.Sprintf(format, args...)})
}

// rel returns the path of a package relative to the module root.
func (ck *checker) rel(path string) string {
	return strings.TrimPrefix(strings.TrimPrefix(path, ck.c.Module), "/")
}

// mod checks the effects directive itself: effect names
// (tool.mod.effects.names) and allow rules (eff.mod.allow).
func (ck *checker) mod() {
	for _, r := range ck.c.Rules {
		pos := syntax.Pos{File: ck.c.ModFile, Line: r.Line, Col: 1}
		for _, name := range r.Effects {
			if !ck.c.Known[name] {
				ck.errorf(pos, "tool.mod.effects.names", "unknown effect %s in anlang.mod", name)
			}
		}
		if !r.Allow {
			continue
		}
		for _, pkg := range ck.p.Packages {
			if !r.Matches(ck.rel(pkg.Path)) {
				continue
			}
			for _, prof := range ck.c.Profiles[pkg.Path] {
				for _, name := range r.Effects {
					if forbidsName(prof, name) {
						ck.errorf(pos, "eff.mod.allow", "anlang.mod allows %s for package %s, but profile %s forbids it", name, pkg.Path, prof)
					}
				}
			}
		}
	}
}

// decision returns the rule of anlang.mod that decides effect e for the
// package rel: the matching rule with the longest pattern that names e,
// deny before allow for one pattern (eff.mod.precedence).
func (ck *checker) decision(rel string, e Effect) *modfile.EffectRule {
	var best *modfile.EffectRule
	for i := range ck.c.Rules {
		r := &ck.c.Rules[i]
		if !r.Matches(rel) {
			continue
		}
		names := false
		for _, n := range r.Effects {
			if n == e.Name() || e == Foreign {
				names = true
			}
		}
		if !names {
			continue
		}
		if best == nil || len(r.Pattern) > len(best.Pattern) || len(r.Pattern) == len(best.Pattern) && !r.Allow && best.Allow {
			best = r
		}
	}
	return best
}

// reported reports whether a violation of effect e by function key is
// reported at the function itself: the effect enters it from a source in
// its body or from a function of another package, not from a function of
// its package, which is reported instead.
func (ck *checker) reported(pkg *Package, o Origin) bool {
	return o.Via == "" || pkg.Funcs[o.Via] == nil
}

// pkg checks the own effects of the functions of a package against its
// profiles and anlang.mod (eff.profile.allowed, eff.profile.privileged,
// eff.mod.deny), and the clauses ensures effect.Without
// (eff.without.check).
func (ck *checker) pkg(pkg *Package) {
	rel := ck.rel(pkg.Path)
	for _, key := range pkg.Keys() {
		s := pkg.Funcs[key]
		own := map[Effect]Origin{}
		for e, o := range s.C {
			own[e] = o
		}
		for e, o := range s.D {
			if _, ok := own[e]; !ok {
				own[e] = o // deferred effects are attributed like own ones (eff.deferred.attribution)
			}
		}
		for _, e := range sortedEffects(own) {
			if !ck.reported(pkg, own[e]) {
				continue
			}
			chain := strings.Join(ck.p.Chain(key, e, false), " → ")
			for _, prof := range ck.c.Profiles[pkg.Path] {
				if forbids(prof, e) || e == Foreign {
					ck.errorf(s.Pos, "eff.profile.allowed", "%s has effect %s, which profile %s forbids: %s", shortKey(key), e.Name(), prof, chain)
					break
				}
			}
			r := ck.decision(rel, e)
			switch {
			case r != nil && !r.Allow:
				ck.errorf(s.Pos, "eff.mod.deny", "%s has effect %s, which anlang.mod denies for %s (line %d): %s", shortKey(key), e.Name(), r.Pattern, r.Line, chain)
			case e.Name() == "Privileged" && r == nil:
				ck.errorf(s.Pos, "eff.profile.privileged", "%s has effect Privileged, which anlang.mod does not allow for package %s: %s", shortKey(key), pkg.Path, chain)
			}
		}
	}
	for _, key := range pkg.Keys() {
		s := pkg.Funcs[key]
		if len(s.Without) == 0 {
			continue
		}
		x := ck.p.execNoParams(key)
		for _, e := range violations(x, s.Without) {
			ck.errorf(s.Pos, "eff.without.check", "%s ensures effect.Without(%s), but it has effect %s: %s", shortKey(key), names(s.Without), e.Name(), strings.Join(ck.chain(key, e, x), " → "))
		}
	}
	for _, cs := range pkg.Calls {
		callee := ck.p.Summary(cs.Callee)
		if callee == nil {
			continue
		}
		x := map[Effect]Origin{}
		for e, o := range ck.p.execNoParams(cs.Callee) {
			x[e] = o
		}
		for _, k := range cs.Keys {
			for e := range ck.p.exec[k] {
				if _, ok := x[e]; !ok {
					x[e] = Origin{Via: k}
				}
			}
		}
		for _, r := range cs.Refs {
			for e, o := range ck.p.resolveRef(r) {
				if _, ok := x[e]; !ok {
					x[e] = o
				}
			}
		}
		for _, e := range violations(x, callee.Without) {
			ck.errorf(cs.Pos, "eff.without.check", "call of %s, which ensures effect.Without(%s), has effect %s: %s", shortKey(cs.Callee), names(callee.Without), e.Name(), strings.Join(append([]string{shortKey(cs.Caller)}, ck.chain(cs.Callee, e, x)...), " → "))
		}
	}
}

// chain returns the chain to a source of e, which x contains, from the
// function key.
func (ck *checker) chain(key string, e Effect, x map[Effect]Origin) []string {
	o := x[e]
	if o.Via == "" || o.Via == key {
		return ck.p.Chain(key, e, true)
	}
	return append([]string{shortKey(key)}, ck.p.Chain(o.Via, e, true)...)
}

// violations returns the effects of without that x contains; x
// containing Foreign contains every effect (eff.model.foreign).
func violations(x map[Effect]Origin, without []Effect) []Effect {
	var out []Effect
	for _, e := range without {
		if _, ok := x[e]; ok {
			out = append(out, e)
		} else if _, ok := x[Foreign]; ok {
			out = append(out, Foreign)
			break
		}
	}
	return out
}

func names(es []Effect) string {
	var s []string
	for _, e := range es {
		s = append(s, "effect."+e.Name())
	}
	return strings.Join(s, ", ")
}

// target checks the execution effects of the program against the target
// (eff.target.check): the execution effects of main and the deferred
// effects attributed to it.
func (ck *checker) target() {
	if !ck.c.HasDesc || ck.c.Main == "" {
		return
	}
	s := ck.p.Summary(ck.c.Main)
	if s == nil {
		return
	}
	x := map[Effect]Origin{}
	for e, o := range ck.p.exec[ck.c.Main] {
		x[e] = o
	}
	for e, o := range s.D {
		if _, ok := x[e]; !ok {
			x[e] = o
		}
	}
	provides, lacks := map[string]bool{}, map[string]bool{}
	for _, n := range ck.c.Provides {
		provides[n] = true
	}
	for _, n := range ck.c.Lacks {
		lacks[n] = true
	}
	for _, e := range sortedEffects(x) {
		name := e.Name()
		chain := strings.Join(ck.p.Chain(ck.c.Main, e, true), " → ")
		switch {
		case e == Foreign:
			if len(lacks) > 0 {
				ck.errorf(s.Pos, "target.effects.foreign", "the program calls external code with undeclared effects (Foreign), which target %s does not accept: %s", ck.c.Target, chain)
			}
		case !Environment[name]:
		case lacks[name]:
			ck.errorf(s.Pos, "target.effects.lacks", "target %s lacks %s: %s", ck.c.Target, name, chain)
		case !provides[name]:
			ck.errorf(s.Pos, "target.effects.provides", "target %s does not provide %s: %s", ck.c.Target, name, chain)
		}
	}
}

// execNoParams returns the execution effects of the function key with its
// parametric references left out: C ∪ resolve(Q) (eff.without.check).
func (p *Program) execNoParams(key string) map[Effect]Origin {
	s := p.funcs[key]
	x := map[Effect]Origin{}
	if s == nil {
		return x
	}
	for e, o := range s.C {
		x[e] = o
	}
	for _, r := range sortedRefs(s.Q) {
		for e, o := range p.resolveRef(r) {
			if _, ok := x[e]; !ok {
				x[e] = o
			}
		}
	}
	return x
}

// resolveRef returns the effects that open reference r resolves to.
func (p *Program) resolveRef(r Ref) map[Effect]Origin {
	if res, ok := p.resolved[r]; ok {
		return res
	}
	res := map[Effect]Origin{}
	for _, k := range p.implsOf(r) {
		for _, e := range sortedEffects(p.exec[k]) {
			if _, ok := res[e]; !ok {
				res[e] = Origin{Via: k}
			}
		}
	}
	p.resolved[r] = res
	return res
}
