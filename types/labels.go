package types

import (
	"go/constant"
	"slices"

	"an/syntax"
)

func constBool(b bool) constant.Value { return constant.MakeBool(b) }

// pathElem is a position in a statement list: the list and the index of
// the statement in it.
type pathElem struct {
	block int // identifies the statement list
	index int
}

type labelInfo struct {
	label *Label
	path  []pathElem
}

type gotoInfo struct {
	stmt *syntax.BranchStmt
	path []pathElem
}

// collectLabels declares the labels of a function body and checks its
// goto statements (stmt.go): a goto must not jump into a block or over a
// variable declaration in the label's block.
func (c *checker) collectLabels(body *syntax.BlockStmt) {
	labels := map[string]*labelInfo{}
	var gotos []gotoInfo
	declares := map[int][]int{} // block -> indices of statements declaring variables
	nblocks := 0

	var walkList func(list []syntax.Stmt, path []pathElem)
	var walk func(s syntax.Stmt, path []pathElem)
	walkList = func(list []syntax.Stmt, path []pathElem) {
		nblocks++
		block := nblocks
		for i, s := range list {
			p := append(slices.Clip(path), pathElem{block, i})
			if declaresVars(s) {
				declares[block] = append(declares[block], i)
			}
			walk(s, p)
		}
	}
	walk = func(s syntax.Stmt, path []pathElem) {
		switch s := s.(type) {
		case *syntax.LabeledStmt:
			name := s.Label.Name
			if name != "_" {
				l := &Label{object: object{pos: s.Label.Pos(), name: name}}
				if prev, ok := labels[name]; ok {
					c.errorf(s.Label.Pos(), "stmt.go", "label %s already defined at %s", name, prev.label.pos)
				} else {
					labels[name] = &labelInfo{l, path}
					c.fn.labels[name] = l
					c.info.Defs[s.Label] = l
				}
			}
			walk(s.Stmt, path)
		case *syntax.BranchStmt:
			if s.Tok == syntax.Goto && s.Label != nil {
				gotos = append(gotos, gotoInfo{s, path})
			}
		case *syntax.BlockStmt:
			walkList(s.List, path)
		case *syntax.IfStmt:
			walkList(s.Body.List, path)
			if s.Else != nil {
				walk(s.Else, path)
			}
		case *syntax.ForStmt:
			walkList(s.Body.List, path)
		case *syntax.RangeStmt:
			walkList(s.Body.List, path)
		case *syntax.SwitchStmt:
			for _, cl := range s.Body.List {
				if cl, ok := cl.(*syntax.CaseClause); ok {
					walkList(cl.Body, path)
				}
			}
		case *syntax.TypeSwitchStmt:
			for _, cl := range s.Body.List {
				if cl, ok := cl.(*syntax.CaseClause); ok {
					walkList(cl.Body, path)
				}
			}
		case *syntax.SelectStmt:
			for _, cl := range s.Body.List {
				if cl, ok := cl.(*syntax.CommClause); ok {
					walkList(cl.Body, path)
				}
			}
		}
	}
	walkList(body.List, nil)

	for _, g := range gotos {
		l := labels[g.stmt.Label.Name]
		if l == nil {
			continue // reported by branchStmt
		}
		d := len(l.path) - 1
		if len(g.path) <= d || !slices.Equal(g.path[:d], l.path[:d]) || g.path[d].block != l.path[d].block {
			c.errorf(g.stmt.Label.Pos(), "stmt.go", "goto %s jumps into block", l.label.name)
			continue
		}
		from, to := g.path[d].index, l.path[d].index
		for _, i := range declares[l.path[d].block] {
			if from < i && i < to {
				c.errorf(g.stmt.Label.Pos(), "stmt.go", "goto %s jumps over variable declaration", l.label.name)
				break
			}
		}
	}
}

// declaresVars reports whether s declares variables in its block.
func declaresVars(s syntax.Stmt) bool {
	switch s := s.(type) {
	case *syntax.DeclStmt:
		return s.Decl.Tok == syntax.Var
	case *syntax.AssignStmt:
		return s.Tok == syntax.Define
	case *syntax.LabeledStmt:
		return declaresVars(s.Stmt)
	}
	return false
}

// isTerminatingList reports whether a statement list ends in a
// terminating statement (stmt.go). label is the label of the statement
// the list belongs to, if any.
func (c *checker) isTerminatingList(list []syntax.Stmt, label string) bool {
	for i := len(list) - 1; i >= 0; i-- {
		if _, ok := list[i].(*syntax.EmptyStmt); ok {
			continue
		}
		return c.isTerminating(list[i], label)
	}
	return false
}

func (c *checker) isTerminating(s syntax.Stmt, label string) bool {
	switch s := s.(type) {
	case *syntax.ReturnStmt:
		return true
	case *syntax.BranchStmt:
		return s.Tok == syntax.Goto || s.Tok == syntax.Fallthrough
	case *syntax.ExprStmt:
		if call, ok := unparen(s.X).(*syntax.CallExpr); ok {
			if id, ok := unparen(call.Fun).(*syntax.Name); ok {
				if b, ok := c.info.Uses[id].(*Builtin); ok && b.id == _Panic {
					return true
				}
			}
		}
	case *syntax.BlockStmt:
		return c.isTerminatingList(s.List, "")
	case *syntax.IfStmt:
		return s.Else != nil && c.isTerminating(s.Body, "") && c.isTerminating(s.Else, "")
	case *syntax.LabeledStmt:
		return c.isTerminating(s.Stmt, s.Label.Name)
	case *syntax.ForStmt:
		return s.Cond == nil && !hasBreak(s.Body, label, true)
	case *syntax.SwitchStmt:
		return c.clausesTerminate(s.Body, label)
	case *syntax.TypeSwitchStmt:
		return c.clausesTerminate(s.Body, label)
	case *syntax.SelectStmt:
		for _, cl := range s.Body.List {
			cl := cl.(*syntax.CommClause)
			if !c.isTerminatingList(cl.Body, "") || hasBreakList(cl.Body, label, true) {
				return false
			}
		}
		return true
	}
	return false
}

func (c *checker) clausesTerminate(body *syntax.BlockStmt, label string) bool {
	hasDefault := false
	for _, cl := range body.List {
		cl := cl.(*syntax.CaseClause)
		if cl.List == nil {
			hasDefault = true
		}
		if !c.isTerminatingList(cl.Body, "") || hasBreakList(cl.Body, label, true) {
			return false
		}
	}
	return hasDefault
}

// hasBreak reports whether s contains a break that refers to the
// enclosing statement: an unlabeled break not nested in another
// breakable statement (if implicit is set), or a break with label.
func hasBreak(s syntax.Stmt, label string, implicit bool) bool {
	switch s := s.(type) {
	case *syntax.BranchStmt:
		if s.Tok == syntax.Break {
			if s.Label == nil {
				return implicit
			}
			return s.Label.Name == label
		}
	case *syntax.BlockStmt:
		return hasBreakList(s.List, label, implicit)
	case *syntax.LabeledStmt:
		return hasBreak(s.Stmt, label, implicit)
	case *syntax.IfStmt:
		return hasBreak(s.Body, label, implicit) || s.Else != nil && hasBreak(s.Else, label, implicit)
	case *syntax.CaseClause:
		return hasBreakList(s.Body, label, implicit)
	case *syntax.CommClause:
		return hasBreakList(s.Body, label, implicit)
	case *syntax.ForStmt:
		return label != "" && hasBreak(s.Body, label, false)
	case *syntax.RangeStmt:
		return label != "" && hasBreak(s.Body, label, false)
	case *syntax.SwitchStmt:
		return label != "" && hasBreak(s.Body, label, false)
	case *syntax.TypeSwitchStmt:
		return label != "" && hasBreak(s.Body, label, false)
	case *syntax.SelectStmt:
		return label != "" && hasBreak(s.Body, label, false)
	}
	return false
}

func hasBreakList(list []syntax.Stmt, label string, implicit bool) bool {
	for _, s := range list {
		if hasBreak(s, label, implicit) {
			return true
		}
	}
	return false
}
