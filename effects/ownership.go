package effects

import (
	"fmt"
	"sort"
	"strings"

	"an/syntax"
	"an/types"
)

// Ownership checks the access of a program to the peripherals of its
// device (spec/14-embedded.md §2). The execution contexts are main and
// each interrupt handler; the accesses of main in its initialization
// phase, the top-level statements of main before the first top-level
// call of interrupts.Enable, do not count (emb.init.end). A peripheral
// that more than one context uses MUST be used inside its critical
// sections, the bodies of range statements over its Lock, in every
// context (emb.ctx.multi). interrupts.Enable is called only directly
// from main (emb.init.calls). infos are the packages, package main last.
func (p *Program) Ownership(infos []*types.Info) []*syntax.Error {
	var errs []*syntax.Error
	main := infos[len(infos)-1]
	if main.Main == nil || main.Main.Decl == nil {
		return nil
	}
	mainDecl := main.Main.Decl
	// emb.init.calls, and whether main enables the interrupts.
	enables := false
	for _, info := range infos {
		for _, f := range append(append([]*types.Func(nil), info.Funcs...), info.Methods...) {
			if f.Decl == nil || f.Decl.Body == nil {
				continue
			}
			syntax.Walk(f.Decl.Body, func(n syntax.Node) bool {
				call, ok := n.(*syntax.CallExpr)
				if !ok || !isEnable(info, call) {
					return true
				}
				if f.Decl != mainDecl {
					errs = append(errs, &syntax.Error{Pos: call.Pos(), Rule: "emb.init.calls", Msg: fmt.Sprintf("interrupts.Enable is called in %s: it may be called only directly from main", f.Name())})
				} else {
					enables = true
				}
				return true
			})
		}
	}
	if !enables {
		// The interrupts stay disabled: no handler runs.
		return errs
	}
	type context struct {
		name    string
		pos     syntax.Pos
		effects map[Effect]Origin
		chain   func(Effect) []string
	}
	var ctxs []*context
	// The statements of main after its initialization phase.
	body := mainDecl.Body.List
	start := 0
	for i, s := range body {
		if es, ok := s.(*syntax.ExprStmt); ok {
			if call, ok := es.X.(*syntax.CallExpr); ok && isEnable(main, call) {
				start = i + 1
				break
			}
		}
	}
	mc := &context{name: "main", pos: mainDecl.Name.Pos(), effects: map[Effect]Origin{}}
	for _, s := range body[start:] {
		x, _ := p.Node(main, s)
		for e, o := range x {
			if _, ok := mc.effects[e]; !ok {
				mc.effects[e] = o
			}
		}
	}
	mc.chain = func(e Effect) []string {
		for _, s := range body[start:] {
			x, chain := p.Node(main, s)
			if _, ok := x[e]; ok {
				return chain(e)
			}
		}
		return nil
	}
	ctxs = append(ctxs, mc)
	for _, info := range infos {
		for _, h := range info.Handlers {
			key := funcKey(h)
			x := map[Effect]Origin{}
			for e, o := range p.exec[key] {
				x[e] = o
			}
			k := key
			ctxs = append(ctxs, &context{name: "interrupt " + h.Handler, pos: h.Pos(), effects: x, chain: func(e Effect) []string { return p.Chain(k, e, true) }})
		}
	}
	// The peripherals and their contexts.
	units := map[string]bool{}
	for _, c := range ctxs {
		for e := range c.effects {
			if name, ok := unitName(e); ok {
				units[name] = true
			}
		}
	}
	names := make([]string, 0, len(units))
	for u := range units {
		names = append(names, u)
	}
	sort.Strings(names)
	for _, u := range names {
		var users []*context
		for _, c := range ctxs {
			_, plain := c.effects[UnitEffect(u, false)]
			_, locked := c.effects[UnitEffect(u, true)]
			if plain || locked {
				users = append(users, c)
			}
		}
		if len(users) < 2 {
			continue // emb.ctx.single
		}
		var list []string
		for _, c := range users {
			list = append(list, c.name)
		}
		for _, c := range users {
			e := UnitEffect(u, false)
			if _, ok := c.effects[e]; !ok {
				continue
			}
			errs = append(errs, &syntax.Error{Pos: c.pos, Rule: "emb.ctx.multi", Msg: fmt.Sprintf("peripheral %s is used by %s, and %s uses it outside a critical section (range over %s.Lock): %s", u, strings.Join(list, " and "), c.name, u, strings.Join(c.chain(e), " → "))})
		}
	}
	return errs
}

// unitName returns the peripheral of an instance effect.
func unitName(e Effect) (string, bool) {
	s := string(e)
	if !strings.HasPrefix(s, "an/device.") {
		return "", false
	}
	name := e.Name()
	return strings.TrimSuffix(name, ".Lock"), true
}

// isEnable reports whether call is a call of interrupts.Enable.
func isEnable(info *types.Info, call *syntax.CallExpr) bool {
	var id *syntax.Name
	switch f := call.Fun.(type) {
	case *syntax.SelectorExpr:
		id = f.Sel
	case *syntax.Name:
		id = f
	}
	if id == nil {
		return false
	}
	f, ok := info.Uses[id].(*types.Func)
	return ok && f.Name() == "Enable" && f.Pkg() != nil && f.Pkg().Path == "an/interrupts"
}
