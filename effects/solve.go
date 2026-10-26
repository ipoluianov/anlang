package effects

import "an/types"

// solve computes the least summaries that satisfy the rules
// (eff.infer.rules, eff.infer.recursion): the facts are applied until
// nothing changes. Sets only grow, so the iteration terminates.
func (a *analyzer) solve() {
	for _, fn := range a.fns {
		a.paramKeys(fn)
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range a.fns {
			if a.update(fn) {
				changed = true
			}
		}
	}
}

// paramKeys records the keys that close the parametric references of fn
// to open references: the function type or interface type of each
// parameter.
func (a *analyzer) paramKeys(fn *fnFacts) {
	fn.sum.ParamKeys = map[int]string{}
	for v, i := range fn.params {
		t := v.Type()
		switch {
		case types.IsInterface(t):
			fn.sum.ParamKeys[i] = typeKey(t)
		case isSignature(t):
			fn.sum.ParamKeys[i] = sigKey(t)
		}
	}
}

// closeRef returns the open reference that stands for parametric
// reference r of s when the arguments are not known.
func closeRef(s *Summary, r Ref) (Ref, bool) {
	key, ok := s.ParamKeys[r.Index]
	switch {
	case r.Kind == ParamCall && ok:
		return Ref{Kind: OpenFunc, Type: key}, true
	case r.Kind == ParamMethod && ok:
		return Ref{Kind: OpenMethod, Method: r.Method, Type: key}, true
	}
	return Ref{}, false
}

func (a *analyzer) lookup(key string) *Summary {
	if fn := a.byKey[key]; fn != nil {
		return fn.sum
	}
	if a.imported != nil {
		return a.imported(key)
	}
	return nil
}

type updater struct {
	a       *analyzer
	fn      *fnFacts
	changed bool
}

func (u *updater) addC(e Effect, o Origin) {
	if _, ok := u.fn.sum.C[e]; !ok {
		u.fn.sum.C[e] = o
		u.changed = true
	}
}

func (u *updater) addD(e Effect, o Origin) {
	if _, ok := u.fn.sum.D[e]; !ok {
		u.fn.sum.D[e] = o
		u.changed = true
	}
}

func (u *updater) addQ(r Ref, o Origin) {
	if _, ok := u.fn.sum.Q[r]; !ok {
		u.fn.sum.Q[r] = o
		u.changed = true
	}
}

func (u *updater) addP(r Ref, o Origin) {
	if _, ok := u.fn.sum.P[r]; !ok {
		u.fn.sum.P[r] = o
		u.changed = true
	}
}

// pref resolves a reference to a parameter or type parameter: a
// reference in P if it is one of fn, a reference lifted to the enclosing
// function otherwise (eff.infer.rules 7).
func (u *updater) pref(pf prefFact) {
	fn := u.fn
	var owner *fnFacts
	var idx int
	if pf.tp != nil {
		owner = fn.tparamOwner(pf.tp)
		if owner != nil {
			idx = owner.tparams[pf.tp]
		}
	} else {
		owner = fn.paramOwner(pf.v)
		if owner != nil {
			idx = owner.params[pf.v]
		}
	}
	switch {
	case owner == nil:
	case owner == fn:
		u.addP(Ref{Kind: pf.kind, Index: idx, Method: pf.method}, pf.origin)
	default:
		for _, l := range fn.lifted {
			if l.kind == pf.kind && l.v == pf.v && l.tp == pf.tp && l.method == pf.method {
				return
			}
		}
		fn.lifted = append(fn.lifted, pf)
		u.changed = true
		// The literal's own summary, used when it is called through a
		// function value, sees the reference as an open one.
		if pf.v != nil {
			s := &Summary{ParamKeys: map[int]string{}}
			t := pf.v.Type()
			if types.IsInterface(t) {
				s.ParamKeys[0] = typeKey(t)
			} else if isSignature(t) {
				s.ParamKeys[0] = sigKey(t)
			}
			if r, ok := closeRef(s, Ref{Kind: pf.kind, Index: 0, Method: pf.method}); ok {
				if fn.liftQ == nil {
					fn.liftQ = map[Ref]bool{}
				}
				if _, had := fn.sum.Q[r]; !had {
					fn.liftQ[r] = true
				}
				u.addQ(r, pf.origin)
			}
		}
	}
}

// add adds the summary of the function key to fn, with the parametric
// references closed (eff.infer.rules 2, 6, 7).
func (u *updater) add(key string, o Origin, skipQ map[Ref]bool) {
	s := u.a.lookup(key)
	if s == nil || s == u.fn.sum {
		return
	}
	via := Origin{Via: key, Pos: o.Pos, What: o.What}
	for _, e := range sortedEffects(s.C) {
		u.addC(e, via)
	}
	for _, e := range sortedEffects(s.D) {
		u.addD(e, via)
	}
	for _, r := range sortedRefs(s.Q) {
		if !skipQ[r] {
			u.addQ(r, via)
		}
	}
	for _, r := range sortedRefs(s.P) {
		if c, ok := closeRef(s, r); ok {
			u.addQ(c, via)
		}
	}
}

func (a *analyzer) update(fn *fnFacts) bool {
	u := &updater{a: a, fn: fn}
	for _, f := range fn.sources {
		u.addC(f.e, f.origin)
	}
	for _, f := range fn.deferred {
		u.addD(f.e, f.origin)
	}
	for _, pf := range fn.prefs {
		u.pref(pf)
	}
	for _, f := range fn.open {
		u.addQ(f.r, f.origin)
	}
	for _, f := range fn.adds {
		u.add(f.key, f.origin, nil)
	}
	for _, lit := range fn.lits {
		u.add(lit.key, Origin{Pos: lit.sum.Pos.String(), What: "function literal"}, lit.liftQ)
		for _, pf := range lit.lifted {
			u.pref(pf)
		}
	}
	for _, c := range fn.calls {
		u.call(c)
	}
	return u.changed
}

// call applies a static call: the callee's summary, with its parametric
// references replaced by the arguments (eff.infer.rules 2) and its type
// parameter references by the type arguments (eff.infer.generic).
func (u *updater) call(c *callFact) {
	a, fn := u.a, u.fn
	key := c.callee
	if c.lit != nil {
		key = a.litKeys[c.lit]
	}
	s := a.lookup(key)
	if s == nil {
		return
	}
	via := Origin{Via: key, Pos: c.origin.Pos}
	if s != fn.sum {
		for _, e := range sortedEffects(s.C) {
			u.addC(e, via)
		}
		for _, e := range sortedEffects(s.D) {
			u.addD(e, via)
		}
		for _, r := range sortedRefs(s.Q) {
			u.addQ(r, via)
		}
	}
	for _, r := range sortedRefs(s.P) {
		switch r.Kind {
		case ParamCall, ParamMethod:
			arg, ok := c.args[r.Index]
			if !ok {
				if cr, ok := closeRef(s, r); ok {
					u.addQ(cr, via)
				}
				continue
			}
			u.substArg(s, r, arg, via)
		case TypeParamMethod:
			if r.Index < len(c.targs) {
				u.substType(r.Method, c.targs[r.Index], via)
			}
		}
	}
	for _, cv := range s.Converts {
		if cv.Index >= len(c.targs) {
			continue
		}
		targ := c.targs[cv.Index]
		switch targ.kind {
		case argType:
			for _, m := range cv.Methods {
				if mk := a.methodKey(targ.typ, m); mk != "" {
					u.add(mk, via, nil)
					a.impl(Ref{Kind: OpenMethod, Method: m, Type: cv.Iface}, mk)
				}
			}
		case argTypeParam:
			if fn.tparamOwner(targ.tp) == fn {
				n := len(fn.sum.Converts)
				fn.sum.Converts = appendConvert(fn.sum.Converts, Convert{Index: fn.tparams[targ.tp], Iface: cv.Iface, Methods: cv.Methods})
				if len(fn.sum.Converts) != n {
					u.changed = true
				}
			}
		case argOpenIface:
			for _, m := range cv.Methods {
				a.flowRef(Ref{Kind: OpenMethod, Method: m, Type: cv.Iface}, Ref{Kind: OpenMethod, Method: m, Type: targ.key})
			}
		}
	}
}

func (u *updater) substArg(s *Summary, r Ref, arg argInfo, via Origin) {
	a := u.a
	switch arg.kind {
	case argParam:
		u.pref(prefFact{kind: r.Kind, v: arg.v, method: r.Method, origin: via})
		return
	case argParamMethod:
		if r.Kind == ParamCall {
			u.pref(prefFact{kind: ParamMethod, v: arg.v, method: arg.method, origin: via})
			return
		}
	case argFuncs:
		if r.Kind == ParamCall {
			keys := arg.keys
			if arg.lit != nil {
				keys = []string{a.litKeys[arg.lit]}
			}
			for _, k := range keys {
				u.add(k, via, nil)
			}
			return
		}
	case argOpenFunc:
		if r.Kind == ParamCall {
			u.addQ(Ref{Kind: OpenFunc, Type: arg.key}, via)
			return
		}
	case argType:
		if r.Kind == ParamMethod {
			if mk := a.methodKey(arg.typ, r.Method); mk != "" {
				u.add(mk, via, nil)
			}
			return
		}
	case argTypeParam:
		if r.Kind == ParamMethod {
			u.pref(prefFact{kind: TypeParamMethod, tp: arg.tp, method: r.Method, origin: via})
			return
		}
	case argOpenIface:
		if r.Kind == ParamMethod {
			u.addQ(Ref{Kind: OpenMethod, Method: r.Method, Type: arg.key}, via)
			return
		}
	}
	if cr, ok := closeRef(s, r); ok {
		u.addQ(cr, via)
	}
}

func (u *updater) substType(method string, targ argInfo, via Origin) {
	switch targ.kind {
	case argType:
		if mk := u.a.methodKey(targ.typ, method); mk != "" {
			u.add(mk, via, nil)
		}
	case argTypeParam:
		u.pref(prefFact{kind: TypeParamMethod, tp: targ.tp, method: method, origin: via})
	case argOpenIface:
		u.addQ(Ref{Kind: OpenMethod, Method: method, Type: targ.key}, via)
	}
}

// callSites records the static calls of functions with ensures
// effect.Without and what their arguments contribute to the execution
// effects of each call (eff.attr.exec).
func (a *analyzer) callSites() {
	for _, fn := range a.fns {
		for _, c := range fn.calls {
			key := c.callee
			if c.lit != nil {
				key = a.litKeys[c.lit]
			}
			s := a.lookup(key)
			if s == nil || len(s.Without) == 0 {
				continue
			}
			cs := CallSite{Caller: fn.key, Callee: key, Pos: c.pos}
			for _, r := range sortedRefs(s.P) {
				keys, refs := a.contribution(fn, s, r, c)
				cs.Keys = append(cs.Keys, keys...)
				cs.Refs = append(cs.Refs, refs...)
			}
			a.pkg.Calls = append(a.pkg.Calls, cs)
		}
	}
}

// contribution returns the functions and open references that the
// arguments of call c substitute for parametric reference r of the
// callee s. An argument that is a parameter of the caller contributes the
// open reference of its type (eff.infer.close).
func (a *analyzer) contribution(fn *fnFacts, s *Summary, r Ref, c *callFact) ([]string, []Ref) {
	closeVar := func(v *types.Var, kind RefKind, method string) []Ref {
		t := v.Type()
		switch {
		case kind == ParamCall && isSignature(t):
			return []Ref{{Kind: OpenFunc, Type: sigKey(t)}}
		case kind == ParamMethod && types.IsInterface(t):
			return []Ref{{Kind: OpenMethod, Method: method, Type: typeKey(t)}}
		}
		return nil
	}
	targ := func(t argInfo, method string) ([]string, []Ref) {
		switch t.kind {
		case argType:
			if mk := a.methodKey(t.typ, method); mk != "" {
				return []string{mk}, nil
			}
		case argOpenIface:
			return nil, []Ref{{Kind: OpenMethod, Method: method, Type: t.key}}
		}
		return nil, nil
	}
	if r.Kind == TypeParamMethod {
		if r.Index < len(c.targs) {
			return targ(c.targs[r.Index], r.Method)
		}
		return nil, nil
	}
	arg, ok := c.args[r.Index]
	if !ok {
		if cr, ok := closeRef(s, r); ok {
			return nil, []Ref{cr}
		}
		return nil, nil
	}
	switch {
	case arg.kind == argParam:
		return nil, closeVar(arg.v, r.Kind, r.Method)
	case arg.kind == argParamMethod && r.Kind == ParamCall:
		return nil, closeVar(arg.v, ParamMethod, arg.method)
	case arg.kind == argFuncs && r.Kind == ParamCall:
		if arg.lit != nil {
			return []string{a.litKeys[arg.lit]}, nil
		}
		return arg.keys, nil
	case arg.kind == argOpenFunc && r.Kind == ParamCall:
		return nil, []Ref{{Kind: OpenFunc, Type: arg.key}}
	case r.Kind == ParamMethod && (arg.kind == argType || arg.kind == argOpenIface):
		return targ(arg, r.Method)
	}
	if cr, ok := closeRef(s, r); ok {
		return nil, []Ref{cr}
	}
	return nil, nil
}
