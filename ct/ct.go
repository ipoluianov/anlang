// Package ct evaluates the constants computed by function calls at
// compile time (spec/10-compile-time.md §10.1–10.3).
//
// The initializer of a computed constant is translated to IR together
// with the functions it calls and run in the IR interpreter, with the
// integer sizes of the target and with limits on steps and memory that
// are the same on every host (ct.limit.deterministic).
package ct

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"an/effects"
	"an/interp"
	"an/lower"
	"an/syntax"
	"an/types"
)

// Limits bound one evaluation (ct.limit.bounded).
type Limits struct {
	Steps  int64 // executed IR instructions
	Memory int64 // values created in slices, arrays, structs and maps
}

// DefaultLimits are the limits of language edition 1.0. They are fixed:
// a project cannot change them (spec/10-compile-time.md §10.3).
var DefaultLimits = Limits{Steps: 10_000_000, Memory: 16 << 20}

// evaluable are the only effects a compile-time evaluated call may have
// (ct.eval.evaluable).
var evaluable = map[string]bool{"Alloc": true, "StaticAlloc": true, "Panic": true, "ReadInput": true}

// Evaluate computes the values of the computed constants of the packages,
// given each after the packages it imports, for a target whose int has
// intBits bits (ct.eval.semantics). files returns the files of the
// directory of a package, which its constants may read; it may be nil.
func Evaluate(infos []*types.Info, prog *effects.Program, intBits int, limits Limits, files func(pkg string) fs.FS) []*syntax.Error {
	e := &evaluator{infos: infos, prog: prog, intBits: intBits, limits: limits, files: files, state: map[*types.Const]int{}, infoOf: map[*types.Const]*types.Info{}}
	for _, info := range infos {
		for _, k := range info.Computed {
			e.infoOf[k] = info
		}
	}
	for _, info := range infos {
		for _, k := range info.Computed {
			e.eval(k, nil)
		}
	}
	return e.errs
}

const (
	pending = iota
	running
	done
	failed
)

type evaluator struct {
	infos   []*types.Info
	prog    *effects.Program
	intBits int
	limits  Limits
	files   func(pkg string) fs.FS
	state   map[*types.Const]int
	infoOf  map[*types.Const]*types.Info
	errs    []*syntax.Error
}

func (e *evaluator) errorf(pos syntax.Pos, rule, format string, args ...any) {
	e.errs = append(e.errs, &syntax.Error{Pos: pos, Rule: rule, Msg: fmt.Sprintf(format, args...)})
}

// eval computes constant k; path lists the constants whose evaluation
// needs k, for cycles.
func (e *evaluator) eval(k *types.Const, path []*types.Const) bool {
	switch e.state[k] {
	case done:
		return true
	case failed:
		return false
	case running:
		var names []string
		for _, c := range append(path, k) {
			names = append(names, c.Name())
		}
		e.errorf(k.Pos(), "ct.const.order", "initialization cycle through constant initializers: %s", strings.Join(names, " → "))
		e.state[k] = failed
		return false
	}
	e.state[k] = running
	ok := e.compute(k, append(path, k))
	if ok {
		e.state[k] = done
	} else {
		e.state[k] = failed
	}
	return ok
}

func (e *evaluator) compute(k *types.Const, path []*types.Const) bool {
	info := e.infoOf[k]
	if info == nil {
		return false
	}
	// The call must be compile-time evaluable (ct.eval.evaluable,
	// ct.eval.no-env).
	x, chain := e.prog.Expr(info, k.Init)
	var bad []effects.Effect
	for eff := range x {
		if !evaluable[eff.Name()] || eff == effects.Foreign {
			bad = append(bad, eff)
		}
	}
	sort.Slice(bad, func(i, j int) bool { return bad[i].Name() < bad[j].Name() })
	for _, eff := range bad {
		e.errorf(k.Pos(), "ct.eval.no-env", "constant %s cannot be computed at compile time: its initializer has effect %s: %s", k.Name(), eff.Name(), strings.Join(chain(eff), " → "))
	}
	if len(bad) > 0 {
		return false
	}
	ready := func(c *types.Const) bool { return e.state[c] == done }
	for {
		prog, missing, errs := lower.Constant(k, info, ready, e.infos...)
		if len(errs) > 0 {
			e.errs = append(e.errs, errs...)
			return false
		}
		if len(missing) > 0 {
			for _, c := range missing {
				if !e.eval(c, path) {
					return false
				}
			}
			continue
		}
		prog.IntBits = e.intBits
		var out strings.Builder
		opts := interp.Options{Stdout: &out, Stderr: &out, MaxSteps: e.limits.Steps, MaxMemory: e.limits.Memory}
		if e.files != nil {
			if dir := e.files(info.Pkg.Path); dir != nil {
				opts.ReadFile = func(name string) ([]byte, error) {
					// Only the files of the directory of the package and its
					// subdirectories (ct.eval.read-input).
					if !fs.ValidPath(name) {
						return nil, fmt.Errorf("%q is not a file of the directory of package %s", name, info.Pkg.Path)
					}
					return fs.ReadFile(dir, name)
				}
			}
		}
		v, err := interp.Eval(prog, opts)
		switch err := err.(type) {
		case nil:
			k.Value = v
			return true
		case *interp.Panic:
			e.errorf(k.Pos(), "ct.eval.panic", "evaluation of constant %s panicked: %s; call stack: %s", k.Name(), err.Value, stack(err.Stack))
		case *interp.LimitError:
			e.errorf(k.Pos(), "ct.limit.exceeded", "evaluation of constant %s exceeded the %s limit of %d; call stack: %s", k.Name(), err.Limit, err.Max, stack(err.Stack))
		default:
			e.errorf(k.Pos(), "ct.const.types", "constant %s: %v", k.Name(), err)
		}
		return false
	}
}

// stack writes a call stack, innermost first.
func stack(fns []string) string {
	var s []string
	for i := len(fns) - 1; i >= 0; i-- {
		s = append(s, fns[i])
	}
	return strings.Join(s, " ← ")
}
