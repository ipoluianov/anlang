package syntax

// Walk calls f for n and, if f returns true, for the statements and
// expressions inside n, in source order.
func Walk(n Node, f func(Node) bool) {
	if n == nil || !f(n) {
		return
	}
	exprs := func(list []Expr) {
		for _, e := range list {
			if e != nil {
				Walk(e, f)
			}
		}
	}
	stmts := func(list []Stmt) {
		for _, s := range list {
			if s != nil {
				Walk(s, f)
			}
		}
	}
	opt := func(nodes ...Node) {
		for _, m := range nodes {
			switch m := m.(type) {
			case Expr:
				if m != nil {
					Walk(m, f)
				}
			case Stmt:
				if m != nil {
					Walk(m, f)
				}
			}
		}
	}
	switch n := n.(type) {
	// Expressions.
	case *CompositeLit:
		opt(n.Type)
		exprs(n.Elts)
	case *FuncLit:
		Walk(n.Body, f)
	case *ParenExpr:
		Walk(n.X, f)
	case *SelectorExpr:
		Walk(n.X, f)
	case *IndexExpr:
		Walk(n.X, f)
		exprs(n.Indices)
	case *SliceExpr:
		Walk(n.X, f)
		opt(n.Low, n.High, n.Max)
	case *TypeAssertExpr:
		Walk(n.X, f)
	case *CallExpr:
		Walk(n.Fun, f)
		exprs(n.Args)
	case *StarExpr:
		Walk(n.X, f)
	case *UnaryExpr:
		Walk(n.X, f)
	case *BinaryExpr:
		Walk(n.X, f)
		Walk(n.Y, f)
	case *KeyValueExpr:
		Walk(n.Key, f)
		Walk(n.Value, f)

	// Statements.
	case *LabeledStmt:
		Walk(n.Stmt, f)
	case *ExprStmt:
		Walk(n.X, f)
	case *SendStmt:
		Walk(n.Chan, f)
		Walk(n.Value, f)
	case *IncDecStmt:
		Walk(n.X, f)
	case *AssignStmt:
		exprs(n.Lhs)
		exprs(n.Rhs)
	case *GoStmt:
		Walk(n.Call, f)
	case *DeferStmt:
		Walk(n.Call, f)
	case *ReturnStmt:
		exprs(n.Results)
	case *BlockStmt:
		stmts(n.List)
	case *IfStmt:
		opt(n.Init, n.Cond)
		Walk(n.Body, f)
		opt(n.Else)
	case *CaseClause:
		exprs(n.List)
		stmts(n.Body)
	case *SwitchStmt:
		opt(n.Init, n.Tag)
		Walk(n.Body, f)
	case *TypeSwitchStmt:
		opt(n.Init, n.Assign)
		Walk(n.Body, f)
	case *CommClause:
		opt(n.Comm)
		stmts(n.Body)
	case *SelectStmt:
		Walk(n.Body, f)
	case *ForStmt:
		opt(n.Init, n.Cond, n.Post)
		Walk(n.Body, f)
	case *RangeStmt:
		opt(n.Key, n.Value)
		Walk(n.X, f)
		Walk(n.Body, f)
	case *DeclStmt:
		for _, s := range n.Decl.Specs {
			if v, ok := s.(*ValueSpec); ok {
				exprs(v.Values)
			}
		}
	}
}
