package vc

import (
	"fmt"

	"an/syntax"
	"an/types"
	"an/verify"
)

// Type invariants (spec/08-contracts.md §8.5): a value of a struct type
// with invariant clauses satisfies them, and so do the values of its
// fields, at its boundaries (contract.type.boundaries), and a function
// assumes them for the values it receives (contract.type.assume).

// hasInvariants reports whether type t or the type of a field of it, at
// any depth, has invariant clauses.
func (c *checker) hasInvariants(t types.Type) bool {
	if t == nil {
		return false
	}
	if c.hasInv == nil {
		c.hasInv = map[types.Type]bool{}
	}
	if h, ok := c.hasInv[t]; ok {
		return h
	}
	c.hasInv[t] = false // a struct does not contain itself
	s := structOf(t)
	h := false
	if s != nil {
		h = len(s.Invariants) > 0
		for _, fl := range s.Fields {
			h = h || c.hasInvariants(fl.Type())
		}
	}
	c.hasInv[t] = h
	return h
}

// invInfo returns the information of the package that declares the
// invariants of type t.
func (f *fn) invInfo(t types.Type) *types.Info {
	if n, ok := t.(*types.Named); ok && n.Obj != nil {
		if info := f.vc.pkgInfo[n.Obj.Pkg()]; info != nil {
			return info
		}
	}
	return f.info
}

// clause is an invariant clause with the values of the fields.
type clause struct {
	x    syntax.Expr
	env  map[*types.Var]val
	info *types.Info
	typ  types.Type
}

// clauses returns the invariant clauses that value x of type t
// satisfies, and false if x is not tracked.
func (f *fn) clauses(t types.Type, x val) ([]clause, bool) {
	if !f.vc.hasInvariants(t) {
		return nil, true
	}
	s := structOf(t)
	if x.kind != vStruct || len(x.fields) != len(s.Fields) {
		return nil, false
	}
	var out []clause
	if len(s.Invariants) > 0 {
		env := map[*types.Var]val{}
		for i, fl := range s.Fields {
			env[fl] = x.fields[i]
		}
		for _, inv := range s.Invariants {
			out = append(out, clause{x: inv, env: env, info: f.invInfo(t), typ: t})
		}
	}
	for i, fl := range s.Fields {
		cs, ok := f.clauses(fl.Type(), x.fields[i])
		if !ok {
			return nil, false
		}
		out = append(out, cs...)
	}
	return out, true
}

// valid assumes the invariants of type t for value x.
func (f *fn) valid(st *state, t types.Type, x val) {
	cs, _ := f.clauses(t, x)
	for _, c := range cs {
		if g, ok := f.contractIn(c.info, st, c.x, c.env, false); ok {
			st.assume(g)
		}
	}
}

// received returns an unknown value of type t that satisfies the
// invariants of t.
func (f *fn) received(st *state, t types.Type, name string) val {
	x := f.fresh(st, t, name)
	f.valid(st, t, x)
	return x
}

// boundary proves the invariants of type t for value x at a boundary
// (contract.type.boundaries); what names the boundary.
func (f *fn) boundary(st *state, t types.Type, x val, pos syntax.Pos, what string) {
	if st.dead || !f.vc.hasInvariants(t) {
		return
	}
	cs, ok := f.clauses(t, x)
	if !ok {
		f.errorf(pos, "contract.obligation.unproven", "the invariants of %s %s are not checked: the value is not tracked (spec/24-roadmap.md, step 24)", t, what)
		return
	}
	for _, c := range cs {
		g, ok := f.contractGoal(c.info, st, c.x, c.env, true)
		if !ok {
			continue
		}
		save := f.info
		if c.info != nil {
			f.info = c.info // the counterexample names the fields
		}
		f.obligation(st, g, pos, c.x, c.env, fmt.Sprintf("the invariant %s of %s %s", exprString(c.x), c.typ, what))
		f.info = save
	}
}

// exit proves what holds when the function returns the values rs: the
// postconditions, the invariants of the values of its inout parameters
// and those of its results.
func (f *fn) exit(st *state, pos syntax.Pos, rs []val) {
	f.checkEnsures(st, pos, rs)
	params := []*types.Var{}
	if f.sig != nil {
		if f.sig.Recv != nil {
			params = append(params, f.sig.Recv)
		}
		if f.sig.Params != nil {
			params = append(params, f.sig.Params.Vars...)
		}
	}
	for _, p := range params {
		if p.Inout() {
			f.boundary(st, p.Type(), f.read(st, p), pos, "of inout parameter "+p.Name()+" at the end of "+f.name)
		}
	}
	for i, r := range f.results {
		if i < len(rs) {
			f.boundary(st, r.Type(), rs[i], pos, "of the result")
		}
	}
}

// field returns the field of struct value x at the path of indices.
func field(x val, path []int) (val, bool) {
	for _, i := range path {
		if x.kind != vStruct || i >= len(x.fields) {
			return val{}, false
		}
		x = x.fields[i]
	}
	return x, true
}

// withField returns struct value x with the field at path set to y.
func withField(x val, path []int, y val) (val, bool) {
	if len(path) == 0 {
		return y, true
	}
	if x.kind != vStruct || path[0] >= len(x.fields) {
		return val{}, false
	}
	z, ok := withField(x.fields[path[0]], path[1:], y)
	if !ok {
		return val{}, false
	}
	fs := append([]val(nil), x.fields...)
	fs[path[0]] = z
	return val{kind: vStruct, fields: fs}, true
}

// zeroViolation returns the invariant clause that the zero value of type
// t does not satisfy, or nil: a type with such a clause, and a struct or
// an array that contains one, have no zero value (contract.type.zero).
func (c *checker) zeroViolation(t types.Type, info *types.Info) syntax.Expr {
	if t == nil {
		return nil
	}
	if c.noZero == nil {
		c.noZero = map[types.Type]syntax.Expr{}
	}
	if x, ok := c.noZero[t]; ok {
		return x
	}
	c.noZero[t] = nil
	var bad syntax.Expr
	switch u := core(t).(type) {
	case *types.Struct:
		if len(u.Invariants) > 0 {
			f := newFunc(c, info, nil, nil, "zero")
			if pinfo := f.invInfo(t); pinfo != nil {
				f.info = pinfo
			}
			st := &state{vars: map[*types.Var]val{}}
			x := f.zero(t)
			env := map[*types.Var]val{}
			for i, fl := range u.Fields {
				env[fl] = x.fields[i]
			}
			for _, inv := range u.Invariants {
				g, ok := f.contractGoal(f.info, st, inv, env, false)
				if proved, _ := f.prove(st, g, inv.Pos(), verify.DefaultSteps); !ok || !proved {
					bad = inv
					break
				}
			}
		}
		for _, fl := range u.Fields {
			if bad == nil {
				bad = c.zeroViolation(fl.Type(), info)
			}
		}
	case *types.Array:
		if u.Len > 0 {
			bad = c.zeroViolation(u.Elem, info)
		}
	}
	c.noZero[t] = bad
	return bad
}

// zeroValues reports the variables declared without an initial value,
// the named results and the fields omitted from composite literals whose
// types have no zero value (contract.type.zero).
func (c *checker) zeroValues(info *types.Info) []*syntax.Error {
	var errs []*syntax.Error
	report := func(pos syntax.Pos, what string, t types.Type) {
		if bad := c.zeroViolation(t, info); bad != nil {
			errs = append(errs, &syntax.Error{Pos: pos, Rule: "contract.type.zero", Msg: fmt.Sprintf("%s of type %s needs a value: the type has no zero value, because the zero value does not satisfy the invariant %s", what, t, exprString(bad))})
		}
	}
	vars := func(d *syntax.GenDecl) {
		if d.Tok != syntax.Var {
			return
		}
		for _, sp := range d.Specs {
			vs, ok := sp.(*syntax.ValueSpec)
			if !ok || len(vs.Values) > 0 {
				continue
			}
			for _, name := range vs.Names {
				if v, ok := info.Defs[name].(*types.Var); ok {
					report(name.Pos(), "variable "+name.Name, v.Type())
				}
			}
		}
	}
	results := func(ft *syntax.FuncType) {
		if ft == nil || ft.Results == nil {
			return
		}
		for _, fl := range ft.Results.List {
			for _, name := range fl.Names {
				if v, ok := info.Defs[name].(*types.Var); ok {
					report(name.Pos(), "result "+name.Name, v.Type())
				}
			}
		}
	}
	visit := func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.DeclStmt:
			vars(n.Decl)
		case *syntax.FuncLit:
			results(n.Type)
		case *syntax.CompositeLit:
			s := structOf(info.Types[n].Type)
			if s == nil {
				return true
			}
			given := map[string]bool{}
			for i, el := range n.Elts {
				if kv, ok := el.(*syntax.KeyValueExpr); ok {
					if id, ok := kv.Key.(*syntax.Name); ok {
						given[id.Name] = true
					}
				} else if i < len(s.Fields) {
					given[s.Fields[i].Name()] = true
				}
			}
			for _, fl := range s.Fields {
				if !given[fl.Name()] {
					report(n.Pos(), "field "+fl.Name()+" omitted from the literal", fl.Type())
				}
			}
		}
		return true
	}
	for _, file := range info.Files {
		for _, d := range file.Decls {
			switch d := d.(type) {
			case *syntax.GenDecl:
				vars(d)
				for _, sp := range d.Specs {
					if vs, ok := sp.(*syntax.ValueSpec); ok {
						for _, v := range vs.Values {
							syntax.Walk(v, visit)
						}
					}
				}
			case *syntax.FuncDecl:
				results(d.Type)
				if d.Body != nil {
					syntax.Walk(d.Body, visit)
				}
			}
		}
	}
	return errs
}
