package syntax

// Node is any node of the syntax tree.
type Node interface {
	Pos() Pos // position of the first character of the node
	End() Pos // position immediately after the node
}

// Expr is an expression or type node.
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

// Spec is a specification inside a GenDecl: an import, value or type spec.
type Spec interface {
	Node
	specNode()
}

// ----------------------------------------------------------------------------
// Comments

// Comment is a single // or /* */ comment.
type Comment struct {
	Slash Pos
	Text  string // including the comment markers
}

func (c *Comment) Pos() Pos { return c.Slash }
func (c *Comment) End() Pos { return c.Slash.Advance(c.Text) }

// CommentGroup is a sequence of comments with no blank line between them.
type CommentGroup struct {
	List []*Comment
}

func (g *CommentGroup) Pos() Pos { return g.List[0].Pos() }
func (g *CommentGroup) End() Pos { return g.List[len(g.List)-1].End() }

// ----------------------------------------------------------------------------
// Fields

// Field is a struct field, a parameter, a result, a type parameter, an
// interface element or a variant field.
type Field struct {
	Names []*Name   // nil for unnamed parameters and embedded fields
	Inout Pos       // position of "inout"; invalid if absent (Kern)
	Type  Expr      // field type; nil only in method declarations of enum variants
	Tag   *BasicLit // struct field tag; nil if absent
}

func (f *Field) Pos() Pos {
	if len(f.Names) > 0 {
		return f.Names[0].Pos()
	}
	if f.Inout.IsValid() {
		return f.Inout
	}
	return f.Type.Pos()
}

func (f *Field) End() Pos {
	if f.Tag != nil {
		return f.Tag.End()
	}
	if f.Type != nil {
		return f.Type.End()
	}
	return f.Names[len(f.Names)-1].End()
}

// FieldList is a list of fields enclosed in parentheses, brackets or
// braces. Opening and Closing are invalid when there are no delimiters.
type FieldList struct {
	Opening Pos
	List    []*Field
	Closing Pos
}

func (l *FieldList) Pos() Pos {
	if l.Opening.IsValid() || len(l.List) == 0 {
		return l.Opening
	}
	return l.List[0].Pos()
}

func (l *FieldList) End() Pos {
	if l.Closing.IsValid() {
		return l.Closing.Advance(")")
	}
	if n := len(l.List); n > 0 {
		return l.List[n-1].End()
	}
	return l.Opening
}

// NumFields returns the number of names, counting unnamed fields as one.
func (l *FieldList) NumFields() int {
	if l == nil {
		return 0
	}
	n := 0
	for _, f := range l.List {
		if len(f.Names) == 0 {
			n++
		} else {
			n += len(f.Names)
		}
	}
	return n
}

// ----------------------------------------------------------------------------
// Expressions

// BadExpr is an expression that could not be parsed.
type BadExpr struct {
	From, To Pos
}

// Name is an identifier.
type Name struct {
	NamePos Pos
	Name    string
}

// BasicLit is an integer, floating-point, rune or string literal.
type BasicLit struct {
	ValuePos Pos
	Kind     Kind // Int, Float, Rune or String
	Value    string
}

// Ellipsis is "..." in a variadic parameter type or an array length.
type Ellipsis struct {
	Ellipsis Pos
	Elt      Expr // element type; nil in [...]T
}

// CompositeLit is Type{Elts}. In Kern it also writes a variant with
// fields and a variant pattern in a case clause.
type CompositeLit struct {
	Type   Expr // nil when the type is elided
	Lbrace Pos
	Elts   []Expr
	Rbrace Pos
}

// FuncLit is a function literal.
type FuncLit struct {
	Type *FuncType
	Body *BlockStmt
}

// ParenExpr is (X).
type ParenExpr struct {
	Lparen Pos
	X      Expr
	Rparen Pos
}

// SelectorExpr is X.Sel.
type SelectorExpr struct {
	X   Expr
	Sel *Name
}

// IndexExpr is X[Indices]: an index expression or a generic instantiation.
type IndexExpr struct {
	X       Expr
	Lbrack  Pos
	Indices []Expr
	Rbrack  Pos
}

// SliceExpr is X[Low:High] or X[Low:High:Max].
type SliceExpr struct {
	X              Expr
	Lbrack         Pos
	Low, High, Max Expr // may be nil
	Slice3         bool
	Rbrack         Pos
}

// TypeAssertExpr is X.(Type); Type is nil for X.(type) in a type switch.
type TypeAssertExpr struct {
	X      Expr
	Lparen Pos
	Type   Expr
	Rparen Pos
}

// CallExpr is Fun(Args). Ellipsis is the position of a final "...".
type CallExpr struct {
	Fun      Expr
	Lparen   Pos
	Args     []Expr
	Ellipsis Pos
	Rparen   Pos
}

// StarExpr is *X. Pointer types and dereferences are removed in Kern
// (lex.operator.go); the parser keeps them so the checker can report
// them, and so that Go sources parse in GoMode.
type StarExpr struct {
	Star Pos
	X    Expr
}

// UnaryExpr is Op X for +, -, !, ^, <-, & and ~.
type UnaryExpr struct {
	OpPos Pos
	Op    Kind
	X     Expr
}

// BinaryExpr is X Op Y.
type BinaryExpr struct {
	X     Expr
	OpPos Pos
	Op    Kind
	Y     Expr
}

// KeyValueExpr is Key: Value in a composite literal.
type KeyValueExpr struct {
	Key   Expr
	Colon Pos
	Value Expr
}

// ----------------------------------------------------------------------------
// Types

// ArrayType is [Len]Elt, or []Elt when Len is nil.
type ArrayType struct {
	Lbrack Pos
	Len    Expr // nil for slices; *Ellipsis for [...]T
	Elt    Expr
}

// StructClause is an invariant or layout clause at the start of a struct
// body (lex.context.struct-region).
type StructClause struct {
	WordPos Pos
	Word    string // "invariant" or "layout"
	X       Expr   // the condition, or the layout kind as a *Name
}

// StructType is a struct type.
type StructType struct {
	Struct  Pos
	Lbrace  Pos
	Clauses []*StructClause
	Fields  []*Field
	Rbrace  Pos
}

// FuncType is a function signature. Func is invalid in declarations.
type FuncType struct {
	Func       Pos
	TypeParams *FieldList // nil if not generic
	Params     *FieldList
	Results    *FieldList // nil if there are no results
}

// InterfaceType is an interface type. Elements are methods (named fields
// with a FuncType) and embedded types or type sets (unnamed fields).
type InterfaceType struct {
	Interface Pos
	Lbrace    Pos
	Elems     []*Field
	Rbrace    Pos
}

// MapType is map[Key]Value.
type MapType struct {
	Map   Pos
	Key   Expr
	Value Expr
}

// ChanDir is the direction of a channel type.
type ChanDir int

const (
	ChanBoth ChanDir = iota
	ChanSend
	ChanRecv
)

// ChanType is chan T, chan<- T or <-chan T.
type ChanType struct {
	Begin Pos // position of "chan" or of "<-" for <-chan
	Dir   ChanDir
	Value Expr
}

// Variant is one variant of an enum type (Kern). Fields is nil for a
// variant without data.
type Variant struct {
	Name   *Name
	Fields *FieldList
}

func (v *Variant) Pos() Pos { return v.Name.Pos() }
func (v *Variant) End() Pos {
	if v.Fields != nil {
		return v.Fields.Closing.Advance("}")
	}
	return v.Name.End()
}

// EnumType is enum { Variants } (Kern).
type EnumType struct {
	Enum     Pos
	Lbrace   Pos
	Variants []*Variant
	Rbrace   Pos
}

// ----------------------------------------------------------------------------
// Statements

// BadStmt is a statement that could not be parsed.
type BadStmt struct {
	From, To Pos
}

// DeclStmt is a const, type or var declaration inside a function.
type DeclStmt struct {
	Decl *GenDecl
}

// EmptyStmt is an empty statement. Implicit is set when it stands for an
// omitted semicolon or a statement list position without a statement.
type EmptyStmt struct {
	Semicolon Pos
	Implicit  bool
}

// LabeledStmt is Label: Stmt.
type LabeledStmt struct {
	Label *Name
	Colon Pos
	Stmt  Stmt
}

// ExprStmt is an expression used as a statement.
type ExprStmt struct {
	X Expr
}

// SendStmt is Chan <- Value.
type SendStmt struct {
	Chan  Expr
	Arrow Pos
	Value Expr
}

// IncDecStmt is X++ or X--.
type IncDecStmt struct {
	X      Expr
	TokPos Pos
	Tok    Kind // Inc or Dec
}

// AssignStmt is an assignment or a short variable declaration.
type AssignStmt struct {
	Lhs    []Expr
	TokPos Pos
	Tok    Kind // Assign, Define or an op-assign kind
	Rhs    []Expr
}

// GoStmt is go Call.
type GoStmt struct {
	Go   Pos
	Call *CallExpr
}

// DeferStmt is defer Call.
type DeferStmt struct {
	Defer Pos
	Call  *CallExpr
}

// ReturnStmt is return Results.
type ReturnStmt struct {
	Return  Pos
	Results []Expr
}

// BranchStmt is break, continue, goto or fallthrough.
type BranchStmt struct {
	TokPos Pos
	Tok    Kind
	Label  *Name // nil if absent
}

// BlockStmt is a braced statement list.
type BlockStmt struct {
	Lbrace Pos
	List   []Stmt
	Rbrace Pos
}

// IfStmt is an if statement. Else is nil, a *BlockStmt or an *IfStmt.
type IfStmt struct {
	If   Pos
	Init Stmt
	Cond Expr
	Body *BlockStmt
	Else Stmt
}

// CaseClause is a case or default clause of a switch statement. List is
// nil for default. In Kern, the expressions of a switch over an enum are
// variant patterns (stmt.switch.variant.parse).
type CaseClause struct {
	Case  Pos
	List  []Expr
	Colon Pos
	Body  []Stmt
}

// SwitchStmt is an expression switch (including a Kern variant switch).
type SwitchStmt struct {
	Switch Pos
	Init   Stmt
	Tag    Expr // nil if absent
	Body   *BlockStmt
}

// TypeSwitchStmt is a type switch. Assign is x := y.(type) or y.(type).
type TypeSwitchStmt struct {
	Switch Pos
	Init   Stmt
	Assign Stmt
	Body   *BlockStmt
}

// CommClause is a case or default clause of a select statement.
type CommClause struct {
	Case  Pos
	Comm  Stmt // send or receive; nil for default
	Colon Pos
	Body  []Stmt
}

// SelectStmt is a select statement.
type SelectStmt struct {
	Select Pos
	Body   *BlockStmt
}

// ForStmt is a for statement with an optional condition and clauses.
type ForStmt struct {
	For  Pos
	Init Stmt
	Cond Expr
	Post Stmt
	Body *BlockStmt
}

// RangeStmt is a for statement with a range clause. Const is set for the
// Kern compile-time loop "for const" (ForConstStmt).
type RangeStmt struct {
	For        Pos
	Const      bool
	Key, Value Expr // nil if absent
	TokPos     Pos  // position of Tok; invalid if there are no variables
	Tok        Kind // Define or Assign; Illegal if there are no variables
	Range      Pos
	X          Expr
	Body       *BlockStmt
}

// ContractStmt is a requires, ensures or invariant clause at the start of
// a function or loop body (Kern, lex.context.contract-region).
type ContractStmt struct {
	WordPos Pos
	Word    string // "requires", "ensures" or "invariant"
	X       Expr
}

// ----------------------------------------------------------------------------
// Declarations

// ImportSpec is a single import. EndPos is set when the spec has been
// moved (by import sorting); otherwise the end is the end of Path.
type ImportSpec struct {
	Name   *Name // local name; nil if absent
	Path   *BasicLit
	EndPos Pos
}

// ValueSpec is a const or var specification.
type ValueSpec struct {
	Names  []*Name
	Type   Expr // nil if absent
	Values []Expr
}

// TypeSpec is a type declaration or alias.
type TypeSpec struct {
	Name       *Name
	TypeParams *FieldList // nil if not generic
	Assign     Pos        // position of "=" for an alias; invalid otherwise
	Type       Expr
}

// GenDecl is an import, const, type or var declaration. Lparen is
// invalid for an ungrouped declaration.
type GenDecl struct {
	TokPos Pos
	Tok    Kind // Import, Const, Type or Var
	Lparen Pos
	Specs  []Spec
	Rparen Pos
}

// FuncDecl is a function or method declaration, including the Kern forms:
// external functions (Binding), interrupt and trap handlers (Handler) and
// GPU functions (Gpu).
type FuncDecl struct {
	Gpu        Pos    // position of "gpu"; invalid if absent
	Handler    string // "interrupt" or "trap"; "" for ordinary functions
	HandlerPos Pos
	Recv       *FieldList // nil for functions
	Name       *Name
	Type       *FuncType
	Body       *BlockStmt // nil for external functions
	Binding    Expr       // "= expr" of an external function; nil otherwise
}

// ProfileClause is "profile core, app" after the package clause (Kern).
type ProfileClause struct {
	Profile Pos
	Names   []*Name
}

func (c *ProfileClause) Pos() Pos { return c.Profile }
func (c *ProfileClause) End() Pos { return c.Names[len(c.Names)-1].End() }

// File is a parsed source file.
type File struct {
	Package  Pos
	Name     *Name
	Profile  *ProfileClause // nil if absent
	Decls    []Decl         // import declarations first, as in the source
	Imports  []*ImportSpec  // all imports, from Decls
	Comments []*CommentGroup
	EOF      Pos
}

func (f *File) Pos() Pos { return f.Package }
func (f *File) End() Pos { return f.EOF }

// Profiles returns the names of the profile clause, or nil.
func (f *File) Profiles() []*Name {
	if f.Profile == nil {
		return nil
	}
	return f.Profile.Names
}

// ----------------------------------------------------------------------------
// Positions of nodes

func (x *BadExpr) Pos() Pos  { return x.From }
func (x *Name) Pos() Pos     { return x.NamePos }
func (x *BasicLit) Pos() Pos { return x.ValuePos }
func (x *Ellipsis) Pos() Pos { return x.Ellipsis }
func (x *CompositeLit) Pos() Pos {
	if x.Type != nil {
		return x.Type.Pos()
	}
	return x.Lbrace
}
func (x *FuncLit) Pos() Pos        { return x.Type.Pos() }
func (x *ParenExpr) Pos() Pos      { return x.Lparen }
func (x *SelectorExpr) Pos() Pos   { return x.X.Pos() }
func (x *IndexExpr) Pos() Pos      { return x.X.Pos() }
func (x *SliceExpr) Pos() Pos      { return x.X.Pos() }
func (x *TypeAssertExpr) Pos() Pos { return x.X.Pos() }
func (x *CallExpr) Pos() Pos       { return x.Fun.Pos() }
func (x *StarExpr) Pos() Pos       { return x.Star }
func (x *UnaryExpr) Pos() Pos      { return x.OpPos }
func (x *BinaryExpr) Pos() Pos     { return x.X.Pos() }
func (x *KeyValueExpr) Pos() Pos   { return x.Key.Pos() }
func (x *ArrayType) Pos() Pos      { return x.Lbrack }
func (x *StructType) Pos() Pos     { return x.Struct }
func (x *FuncType) Pos() Pos {
	if x.Func.IsValid() || x.Params == nil {
		return x.Func
	}
	return x.Params.Pos()
}
func (x *InterfaceType) Pos() Pos { return x.Interface }
func (x *MapType) Pos() Pos       { return x.Map }
func (x *ChanType) Pos() Pos      { return x.Begin }
func (x *EnumType) Pos() Pos      { return x.Enum }

func (x *BadExpr) End() Pos  { return x.To }
func (x *Name) End() Pos     { return x.NamePos.Advance(x.Name) }
func (x *BasicLit) End() Pos { return x.ValuePos.Advance(x.Value) }
func (x *Ellipsis) End() Pos {
	if x.Elt != nil {
		return x.Elt.End()
	}
	return x.Ellipsis.Advance("...")
}
func (x *CompositeLit) End() Pos   { return x.Rbrace.Advance("}") }
func (x *FuncLit) End() Pos        { return x.Body.End() }
func (x *ParenExpr) End() Pos      { return x.Rparen.Advance(")") }
func (x *SelectorExpr) End() Pos   { return x.Sel.End() }
func (x *IndexExpr) End() Pos      { return x.Rbrack.Advance("]") }
func (x *SliceExpr) End() Pos      { return x.Rbrack.Advance("]") }
func (x *TypeAssertExpr) End() Pos { return x.Rparen.Advance(")") }
func (x *CallExpr) End() Pos       { return x.Rparen.Advance(")") }
func (x *StarExpr) End() Pos       { return x.X.End() }
func (x *UnaryExpr) End() Pos      { return x.X.End() }
func (x *BinaryExpr) End() Pos     { return x.Y.End() }
func (x *KeyValueExpr) End() Pos   { return x.Value.End() }
func (x *ArrayType) End() Pos      { return x.Elt.End() }
func (x *StructType) End() Pos     { return x.Rbrace.Advance("}") }
func (x *FuncType) End() Pos {
	if x.Results != nil {
		return x.Results.End()
	}
	return x.Params.End()
}
func (x *InterfaceType) End() Pos { return x.Rbrace.Advance("}") }
func (x *MapType) End() Pos       { return x.Value.End() }
func (x *ChanType) End() Pos      { return x.Value.End() }
func (x *EnumType) End() Pos      { return x.Rbrace.Advance("}") }

func (*BadExpr) exprNode()        {}
func (*Name) exprNode()           {}
func (*BasicLit) exprNode()       {}
func (*Ellipsis) exprNode()       {}
func (*CompositeLit) exprNode()   {}
func (*FuncLit) exprNode()        {}
func (*ParenExpr) exprNode()      {}
func (*SelectorExpr) exprNode()   {}
func (*IndexExpr) exprNode()      {}
func (*SliceExpr) exprNode()      {}
func (*TypeAssertExpr) exprNode() {}
func (*CallExpr) exprNode()       {}
func (*StarExpr) exprNode()       {}
func (*UnaryExpr) exprNode()      {}
func (*BinaryExpr) exprNode()     {}
func (*KeyValueExpr) exprNode()   {}
func (*ArrayType) exprNode()      {}
func (*StructType) exprNode()     {}
func (*FuncType) exprNode()       {}
func (*InterfaceType) exprNode()  {}
func (*MapType) exprNode()        {}
func (*ChanType) exprNode()       {}
func (*EnumType) exprNode()       {}

func (s *BadStmt) Pos() Pos        { return s.From }
func (s *DeclStmt) Pos() Pos       { return s.Decl.Pos() }
func (s *EmptyStmt) Pos() Pos      { return s.Semicolon }
func (s *LabeledStmt) Pos() Pos    { return s.Label.Pos() }
func (s *ExprStmt) Pos() Pos       { return s.X.Pos() }
func (s *SendStmt) Pos() Pos       { return s.Chan.Pos() }
func (s *IncDecStmt) Pos() Pos     { return s.X.Pos() }
func (s *AssignStmt) Pos() Pos     { return s.Lhs[0].Pos() }
func (s *GoStmt) Pos() Pos         { return s.Go }
func (s *DeferStmt) Pos() Pos      { return s.Defer }
func (s *ReturnStmt) Pos() Pos     { return s.Return }
func (s *BranchStmt) Pos() Pos     { return s.TokPos }
func (s *BlockStmt) Pos() Pos      { return s.Lbrace }
func (s *IfStmt) Pos() Pos         { return s.If }
func (s *CaseClause) Pos() Pos     { return s.Case }
func (s *SwitchStmt) Pos() Pos     { return s.Switch }
func (s *TypeSwitchStmt) Pos() Pos { return s.Switch }
func (s *CommClause) Pos() Pos     { return s.Case }
func (s *SelectStmt) Pos() Pos     { return s.Select }
func (s *ForStmt) Pos() Pos        { return s.For }
func (s *RangeStmt) Pos() Pos      { return s.For }
func (s *ContractStmt) Pos() Pos   { return s.WordPos }

func (s *BadStmt) End() Pos  { return s.To }
func (s *DeclStmt) End() Pos { return s.Decl.End() }
func (s *EmptyStmt) End() Pos {
	if s.Implicit {
		return s.Semicolon
	}
	return s.Semicolon.Advance(";")
}
func (s *LabeledStmt) End() Pos { return s.Stmt.End() }
func (s *ExprStmt) End() Pos    { return s.X.End() }
func (s *SendStmt) End() Pos    { return s.Value.End() }
func (s *IncDecStmt) End() Pos  { return s.TokPos.Advance("++") }
func (s *AssignStmt) End() Pos  { return s.Rhs[len(s.Rhs)-1].End() }
func (s *GoStmt) End() Pos      { return s.Call.End() }
func (s *DeferStmt) End() Pos   { return s.Call.End() }
func (s *ReturnStmt) End() Pos {
	if n := len(s.Results); n > 0 {
		return s.Results[n-1].End()
	}
	return s.Return.Advance("return")
}
func (s *BranchStmt) End() Pos {
	if s.Label != nil {
		return s.Label.End()
	}
	return s.TokPos.Advance(s.Tok.String())
}
func (s *BlockStmt) End() Pos { return s.Rbrace.Advance("}") }
func (s *IfStmt) End() Pos {
	if s.Else != nil {
		return s.Else.End()
	}
	return s.Body.End()
}
func (s *CaseClause) End() Pos {
	if n := len(s.Body); n > 0 {
		return s.Body[n-1].End()
	}
	return s.Colon.Advance(":")
}
func (s *SwitchStmt) End() Pos     { return s.Body.End() }
func (s *TypeSwitchStmt) End() Pos { return s.Body.End() }
func (s *CommClause) End() Pos {
	if n := len(s.Body); n > 0 {
		return s.Body[n-1].End()
	}
	return s.Colon.Advance(":")
}
func (s *SelectStmt) End() Pos   { return s.Body.End() }
func (s *ForStmt) End() Pos      { return s.Body.End() }
func (s *RangeStmt) End() Pos    { return s.Body.End() }
func (s *ContractStmt) End() Pos { return s.X.End() }

func (*BadStmt) stmtNode()        {}
func (*DeclStmt) stmtNode()       {}
func (*EmptyStmt) stmtNode()      {}
func (*LabeledStmt) stmtNode()    {}
func (*ExprStmt) stmtNode()       {}
func (*SendStmt) stmtNode()       {}
func (*IncDecStmt) stmtNode()     {}
func (*AssignStmt) stmtNode()     {}
func (*GoStmt) stmtNode()         {}
func (*DeferStmt) stmtNode()      {}
func (*ReturnStmt) stmtNode()     {}
func (*BranchStmt) stmtNode()     {}
func (*BlockStmt) stmtNode()      {}
func (*IfStmt) stmtNode()         {}
func (*CaseClause) stmtNode()     {}
func (*SwitchStmt) stmtNode()     {}
func (*TypeSwitchStmt) stmtNode() {}
func (*CommClause) stmtNode()     {}
func (*SelectStmt) stmtNode()     {}
func (*ForStmt) stmtNode()        {}
func (*RangeStmt) stmtNode()      {}
func (*ContractStmt) stmtNode()   {}

func (s *ImportSpec) Pos() Pos {
	if s.Name != nil {
		return s.Name.Pos()
	}
	return s.Path.Pos()
}
func (s *ValueSpec) Pos() Pos { return s.Names[0].Pos() }
func (s *TypeSpec) Pos() Pos  { return s.Name.Pos() }

func (s *ImportSpec) End() Pos {
	if s.EndPos.IsValid() {
		return s.EndPos
	}
	return s.Path.End()
}
func (s *ValueSpec) End() Pos {
	if n := len(s.Values); n > 0 {
		return s.Values[n-1].End()
	}
	if s.Type != nil {
		return s.Type.End()
	}
	return s.Names[len(s.Names)-1].End()
}
func (s *TypeSpec) End() Pos { return s.Type.End() }

func (*ImportSpec) specNode() {}
func (*ValueSpec) specNode()  {}
func (*TypeSpec) specNode()   {}

func (d *GenDecl) Pos() Pos { return d.TokPos }
func (d *GenDecl) End() Pos {
	if d.Rparen.IsValid() {
		return d.Rparen.Advance(")")
	}
	return d.Specs[0].End()
}
func (d *FuncDecl) Pos() Pos {
	switch {
	case d.Gpu.IsValid():
		return d.Gpu
	case d.Handler != "":
		return d.HandlerPos
	}
	return d.Type.Func
}
func (d *FuncDecl) End() Pos {
	switch {
	case d.Body != nil:
		return d.Body.End()
	case d.Binding != nil:
		return d.Binding.End()
	}
	return d.Type.End()
}

func (*GenDecl) declNode()  {}
func (*FuncDecl) declNode() {}
