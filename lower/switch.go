package lower

import (
	"go/constant"

	"an/ir"
	"an/syntax"
	"an/types"
)

func constOne() constant.Value { return constant.MakeInt64(1) }

func (fb *funcBuilder) switchStmt(s *syntax.SwitchStmt, label string) {
	if s.Init != nil {
		fb.stmt(s.Init)
	}
	done := fb.newBlock("switch.done")
	if s.Tag != nil {
		if _, ok := fb.typeOf(s.Tag).Underlying().(*types.Enum); ok {
			fb.variantSwitch(s, label, done)
			return
		}
	}
	var tag ir.Value
	if s.Tag != nil {
		tag = fb.copy(fb.expr(s.Tag))
	}
	clauses := s.Body.List
	bodies := make([]*ir.Block, len(clauses))
	for i := range clauses {
		bodies[i] = fb.newBlock("switch.case")
	}
	var dflt *ir.Block
	for i, cs := range clauses {
		cl := cs.(*syntax.CaseClause)
		if cl.List == nil {
			dflt = bodies[i]
			continue
		}
		for _, e := range cl.List {
			next := fb.newBlock("switch.next")
			if tag == nil {
				fb.cond(e, bodies[i], next)
			} else {
				v := fb.expr(e)
				x, y := tag, v
				switch {
				case types.IsInterface(x.Type()) && !types.IsInterface(y.Type()):
					y = fb.convert(y, x.Type())
				case types.IsInterface(y.Type()) && !types.IsInterface(x.Type()):
					x = fb.convert(x, y.Type())
				default:
					y = fb.convert(y, x.Type())
				}
				eq := fb.emit(&ir.BinOp{Op: syntax.Eql, X: x, Y: y}, types.Typ[types.Bool])
				fb.branch(eq, bodies[i], next)
			}
			fb.cur = next
		}
	}
	if dflt != nil {
		fb.jump(dflt)
	} else {
		fb.jump(done)
	}
	fb.targets = append(fb.targets, &target{label: label, brk: done})
	for i, cs := range clauses {
		cl := cs.(*syntax.CaseClause)
		fb.cur = bodies[i]
		saved := fb.fallthroughTo
		if i+1 < len(bodies) {
			fb.fallthroughTo = bodies[i+1]
		}
		fb.stmts(cl.Body)
		fb.fallthroughTo = saved
		fb.jump(done)
	}
	fb.targets = fb.targets[:len(fb.targets)-1]
	fb.cur = done
}

// variantSwitch translates a switch over an enum value: the variant index
// selects the clause; bindings get copies of the fields.
func (fb *funcBuilder) variantSwitch(s *syntax.SwitchStmt, label string, done *ir.Block) {
	tag := fb.copy(fb.expr(s.Tag))
	idx := fb.emit(&ir.VariantIndex{X: tag}, types.Typ[types.Int])
	enum := tag.Type().Underlying().(*types.Enum)
	clauses := s.Body.List
	bodies := make([]*ir.Block, len(clauses))
	for i := range clauses {
		bodies[i] = fb.newBlock("variant.case")
	}
	var dflt *ir.Block
	for i, cs := range clauses {
		cl := cs.(*syntax.CaseClause)
		if cl.List == nil {
			dflt = bodies[i]
			continue
		}
		for _, e := range cl.List {
			name, _ := patternName(e)
			v := fb.variantOf(name)
			if v == nil {
				continue
			}
			next := fb.newBlock("variant.next")
			eq := fb.emit(&ir.BinOp{Op: syntax.Eql, X: idx, Y: constant_(types.Typ[types.Int], constant.MakeInt64(int64(v.Index())))}, types.Typ[types.Bool])
			fb.branch(eq, bodies[i], next)
			fb.cur = next
		}
	}
	if dflt != nil {
		fb.jump(dflt)
	} else {
		fb.jump(done)
	}
	fb.targets = append(fb.targets, &target{label: label, brk: done})
	for i, cs := range clauses {
		cl := cs.(*syntax.CaseClause)
		fb.cur = bodies[i]
		if len(cl.List) == 1 {
			if name, bindings := patternName(cl.List[0]); bindings != nil {
				v := fb.variantOf(name)
				fields := enum.Variants[v.Index()].Fields()
				for j, b := range bindings {
					id, ok := b.(*syntax.Name)
					if !ok || id.Name == "_" {
						continue
					}
					bv, ok := fb.info.Defs[id].(*types.Var)
					if !ok {
						continue
					}
					ft := fields[j].Type()
					cell := fb.alloc(ft, id.Name)
					fb.store(cell, fb.emit(&ir.VariantField{X: tag, Variant: v.Index(), Field: j}, ft))
					fb.vars[bv] = cell
				}
			}
		}
		fb.stmts(cl.Body)
		fb.jump(done)
	}
	fb.targets = fb.targets[:len(fb.targets)-1]
	fb.cur = done
}

func patternName(e syntax.Expr) (syntax.Expr, []syntax.Expr) {
	if cl, ok := unparen(e).(*syntax.CompositeLit); ok && cl.Type != nil {
		return cl.Type, cl.Elts
	}
	return e, nil
}

func (fb *funcBuilder) typeSwitch(s *syntax.TypeSwitchStmt, label string) {
	if s.Init != nil {
		fb.stmt(s.Init)
	}
	var guard syntax.Expr
	var bound *syntax.Name
	switch a := s.Assign.(type) {
	case *syntax.ExprStmt:
		guard = a.X.(*syntax.TypeAssertExpr).X
	case *syntax.AssignStmt:
		guard = a.Rhs[0].(*syntax.TypeAssertExpr).X
		bound = a.Lhs[0].(*syntax.Name)
	}
	x := fb.copy(fb.expr(guard))
	done := fb.newBlock("typeswitch.done")
	clauses := s.Body.List
	bodies := make([]*ir.Block, len(clauses))
	for i := range clauses {
		bodies[i] = fb.newBlock("typeswitch.case")
	}
	var dflt *ir.Block
	for i, cs := range clauses {
		cl := cs.(*syntax.CaseClause)
		if cl.List == nil {
			dflt = bodies[i]
			continue
		}
		for _, e := range cl.List {
			next := fb.newBlock("typeswitch.next")
			var ok ir.Value
			if id, isName := unparen(e).(*syntax.Name); isName {
				if _, isNil := fb.info.Uses[id].(*types.Nil); isNil {
					ok = fb.emit(&ir.BinOp{Op: syntax.Eql, X: x, Y: zero(x.Type())}, types.Typ[types.Bool])
				}
			}
			if ok == nil {
				t := fb.typeOf(e)
				pair := fb.emit(&ir.TypeAssert{X: x, Asserted: t, CommaOk: true}, commaOkType(t))
				ok = fb.emit(&ir.Extract{Tuple: pair, Index: 1}, types.Typ[types.Bool])
			}
			fb.branch(ok, bodies[i], next)
			fb.cur = next
		}
	}
	if dflt != nil {
		fb.jump(dflt)
	} else {
		fb.jump(done)
	}
	fb.targets = append(fb.targets, &target{label: label, brk: done})
	for i, cs := range clauses {
		cl := cs.(*syntax.CaseClause)
		fb.cur = bodies[i]
		if bound != nil {
			fb.bindTypeSwitchVar(bound, cl, x)
		}
		fb.stmts(cl.Body)
		fb.jump(done)
	}
	fb.targets = fb.targets[:len(fb.targets)-1]
	fb.cur = done
}

// bindTypeSwitchVar declares the variable of a type switch in a clause:
// with the type of a single case type, the asserted value.
func (fb *funcBuilder) bindTypeSwitchVar(bound *syntax.Name, cl *syntax.CaseClause, x ir.Value) {
	var v *types.Var
	walkNames(cl.Body, func(id *syntax.Name) {
		if u, ok := fb.info.Uses[id].(*types.Var); ok && u.Pos() == bound.Pos() {
			v = u
		}
	})
	if v == nil {
		return
	}
	t := fb.typ(v.Type())
	cell := fb.alloc(t, v.Name())
	var val ir.Value = x
	if !types.Identical(t, x.Type()) {
		if types.IsInterface(t) {
			pair := fb.emit(&ir.TypeAssert{X: x, Asserted: t, CommaOk: true}, commaOkType(t))
			val = fb.emit(&ir.Extract{Tuple: pair, Index: 0}, t)
		} else {
			val = fb.emit(&ir.TypeAssert{X: x, Asserted: t}, t)
		}
	}
	fb.store(cell, val)
	fb.vars[v] = cell
}
