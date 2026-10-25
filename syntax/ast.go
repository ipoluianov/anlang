package syntax

// The AST covers the subset of Kern that the parser accepts so far:
// the package clause, the profile clause, imports, and functions whose
// bodies are expression statements. It grows with the roadmap (step 1).

// Node is any node of the syntax tree.
type Node interface {
	Pos() Pos
}

// Expr is an expression node.
type Expr interface {
	Node
	exprNode()
}

// Stmt is a statement node.
type Stmt interface {
	Node
	stmtNode()
}

// Decl is a top-level declaration.
type Decl interface {
	Node
	declNode()
}

// File is a parsed source file (SourceFile in spec/03-declarations.md).
type File struct {
	Name     *Name   // package name
	Profiles []*Name // profile clause; nil if absent
	Imports  []*ImportSpec
	Decls    []Decl
	Package  Pos // position of the package keyword
}

func (f *File) Pos() Pos { return f.Package }

// ImportSpec is one import. Name is nil when no local name is given.
type ImportSpec struct {
	Name *Name
	Path *BasicLit
}

func (s *ImportSpec) Pos() Pos {
	if s.Name != nil {
		return s.Name.NamePos
	}
	return s.Path.ValuePos
}

// FuncDecl is a function declaration.
type FuncDecl struct {
	Func    Pos
	Name    *Name
	Params  []*Field
	Results []*Field
	Body    *BlockStmt
}

func (d *FuncDecl) Pos() Pos { return d.Func }
func (*FuncDecl) declNode()  {}

// Field is a parameter or result: names (possibly none) and a type.
type Field struct {
	Names []*Name
	Type  Expr
}

// BlockStmt is a braced list of statements.
type BlockStmt struct {
	Lbrace Pos
	List   []Stmt
	Rbrace Pos
}

func (b *BlockStmt) Pos() Pos { return b.Lbrace }
func (*BlockStmt) stmtNode()  {}

// ExprStmt is an expression used as a statement.
type ExprStmt struct {
	X Expr
}

func (s *ExprStmt) Pos() Pos { return s.X.Pos() }
func (*ExprStmt) stmtNode()  {}

// Name is an identifier.
type Name struct {
	NamePos Pos
	Name    string
}

func (x *Name) Pos() Pos { return x.NamePos }
func (*Name) exprNode()  {}

// BasicLit is a literal. Value is the source text.
type BasicLit struct {
	ValuePos Pos
	Kind     Kind // Int, Float, Rune or String
	Value    string
}

func (x *BasicLit) Pos() Pos { return x.ValuePos }
func (*BasicLit) exprNode()  {}

// SelectorExpr is X.Sel.
type SelectorExpr struct {
	X   Expr
	Sel *Name
}

func (x *SelectorExpr) Pos() Pos { return x.X.Pos() }
func (*SelectorExpr) exprNode()  {}

// CallExpr is Fun(Args...). Ellipsis is set for a final "...".
type CallExpr struct {
	Fun      Expr
	Lparen   Pos
	Args     []Expr
	Ellipsis bool
}

func (x *CallExpr) Pos() Pos { return x.Fun.Pos() }
func (*CallExpr) exprNode()  {}

// ParenExpr is (X).
type ParenExpr struct {
	Lparen Pos
	X      Expr
}

func (x *ParenExpr) Pos() Pos { return x.Lparen }
func (*ParenExpr) exprNode()  {}
