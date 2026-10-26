package codegen

import (
	"fmt"
	"strings"

	"an/syntax"
	"an/types"
)

// Stack analysis (spec/14-embedded.md §5, spec/19-runtime.md §19.5): the
// stack that a routine needs is its frame, with what enter pushes
// (ABI.Params), and the deepest of the routines it calls. A direct call
// calls its label; an indirect call may call each routine whose address
// the program takes and whose parameters and results have the same
// types. An interrupt handler needs ABI.InterruptFrame more, for what
// the processor and enter.handler push. Handlers do not preempt each
// other, so the stack of main needs its own depth and that of the
// deepest handler. A routine that calls itself with a proven bound on
// the depth of its recursion needs its frame that many times; other
// recursion is rejected (rt.stack.core-recursion), except through the
// routine that runs the deferred calls during a panic, and through the
// panic functions of the runtime package, which count once.

// stackInfo is what the stack analysis knows of a routine.
type stackInfo struct {
	name    string
	pos     syntax.Pos
	frame   int
	depth   int // the bound on the depth of its recursion, or 0
	handler bool
	calls   []string // the labels of direct calls
	ind     []string // the signatures of indirect calls
}

// sigKey returns the signature of a call of a function value: the types
// of its arguments and results.
func sigKey(params, results []types.Type) string {
	var b strings.Builder
	for _, t := range params {
		b.WriteString(t.String() + ",")
	}
	b.WriteString("->")
	for _, t := range results {
		b.WriteString(t.String() + ",")
	}
	return b.String()
}

// addressTaken records that the address of the routine at label, with
// the signature key, is a value.
func (g *gen) addressTaken(label, key string) {
	if g.addrTaken == nil {
		g.addrTaken = map[string]string{}
	}
	g.addrTaken[label] = key
}

// info returns the stack information of the routine of fn.
func (fn *fnGen) stackInfo() *stackInfo {
	g := fn.g
	if fn.label == "" {
		return nil
	}
	if g.stackInfos == nil {
		g.stackInfos = map[string]*stackInfo{}
	}
	s := g.stackInfos[fn.label]
	if s == nil {
		s = &stackInfo{name: fn.name, pos: fn.f.Pos, depth: fn.f.RecursionDepth}
		g.stackInfos[fn.label] = s
	}
	return s
}

// panicRoutine reports whether the routine at label is part of a panic,
// which ends the program: the panic functions of the runtime package and
// the routine that runs the deferred calls.
func (g *gen) panicRoutine(label string) bool {
	if label == "LrunDefers" {
		return true
	}
	s := g.stackInfos[label]
	if s == nil {
		return false
	}
	name, ok := strings.CutPrefix(s.name, "an/runtime.")
	return ok && (strings.HasPrefix(name, "Panic") || name == "sliceFail")
}

// checkStack checks the recursion of a program in profile core and its
// stack against the Stack of the target, if it has one (emb.stack.limit);
// in the other profiles the stack is checked when it is used.
func (g *gen) checkStack() {
	limit := g.desc.Stack
	if !g.prog.Core {
		return
	}
	depth := map[string]int{}
	deepest := map[string]string{} // the callee on the deepest path
	const visiting = -1
	var errs []error
	reported := map[string]bool{} // the routines whose recursion is reported
	var path []string
	var visit func(l string) int
	visit = func(l string) int {
		if d, ok := depth[l]; ok {
			if d == visiting {
				i := len(path) - 1
				for i > 0 && path[i] != l {
					i--
				}
				for _, p := range append(path[i:], l) {
					if g.panicRoutine(p) {
						return 0 // the routines of a panic count once
					}
				}
				if reported[l] {
					return 0
				}
				reported[l] = true
				var names []string
				for _, p := range append(path[i:], l) {
					names = append(names, g.stackInfos[p].name)
				}
				s := g.stackInfos[l]
				errs = append(errs, &syntax.Error{Pos: s.pos, Rule: "emb.stack.recursion", Msg: fmt.Sprintf("%s is recursive: %s; the depth of recursion in the core profile must be bounded: a function may call only itself, with an integer parameter that decreases at each such call (rt.stack.core-recursion)", s.name, strings.Join(names, " → "))})
				return 0
			}
			return d
		}
		s := g.stackInfos[l]
		if s == nil {
			return 0
		}
		depth[l] = visiting
		path = append(path, l)
		best, bestCallee := 0, ""
		callees := append([]string(nil), s.calls...)
		for _, key := range s.ind {
			for target, k := range g.addrTaken {
				if k == key {
					callees = append(callees, target)
				}
			}
		}
		for i, c := range callees {
			if c == l && s.depth > 0 && i < len(s.calls) {
				continue // a direct call of itself, counted by the depth
			}
			if d := visit(c); d > best {
				best, bestCallee = d, c
			}
		}
		path = path[:len(path)-1]
		depth[l] = s.frame + best
		if s.depth > 0 {
			depth[l] = s.frame*s.depth + best
		}
		deepest[l] = bestCallee
		return depth[l]
	}
	total := visit("entry")
	chainOf := func(l string) []string {
		var names []string
		seen := map[string]bool{}
		for ; l != "" && !seen[l]; l = deepest[l] {
			seen[l] = true
			if s := g.stackInfos[l]; s != nil {
				if s.depth > 0 {
					names = append(names, fmt.Sprintf("%s (%d bytes × %d)", s.name, s.frame, s.depth))
				} else {
					names = append(names, fmt.Sprintf("%s (%d bytes)", s.name, s.frame))
				}
			}
		}
		return names
	}
	chain := chainOf("entry")
	worst := ""
	for l, s := range g.stackInfos {
		if s.handler {
			if d := visit(l); d > 0 && (worst == "" || d > depth[worst]) {
				worst = l
			}
		}
	}
	if worst != "" {
		total += depth[worst]
		chain = append(chain, "then interrupt "+strings.Join(chainOf(worst), " → "))
	}
	g.errs = append(g.errs, errs...)
	if len(errs) == 0 && limit > 0 && total > limit {
		g.errs = append(g.errs, &syntax.Error{Pos: g.prog.Main.Pos, Rule: "emb.stack.limit", Msg: fmt.Sprintf("the stack needs %d bytes, more than the Stack of %d bytes of the target: %s", total, limit, strings.Join(chain, " → "))})
	}
	g.stackNeed = total
}
