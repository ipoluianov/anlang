package syntax

import "fmt"

// Parse parses one Anlang source file. It returns the file and all lexical
// and syntax errors; the file is nil only if the package clause is
// missing.
func Parse(file string, src []byte) (*File, []*Error) {
	return ParseMode(file, src, AnlangMode)
}

// ParseMode is Parse with an explicit language mode.
func ParseMode(file string, src []byte, mode Mode) (*File, []*Error) {
	p := &parser{s: NewScanner(file, src, mode), mode: mode}
	p.next()
	f := p.parseFile()
	errs := append(p.s.Errors, p.errors...)
	if f != nil {
		f.Comments = p.comments
	}
	return f, errs
}

// The parser follows the structure of the Go grammar and of go/parser.
// exprLev tracks whether a composite literal may start at the current
// position: it is negative in the header of an if, for or switch
// statement, where "{" starts the body.
type parser struct {
	s    *Scanner
	mode Mode

	tok  Token
	prev Pos // end of the previous non-comment token

	comments      []*CommentGroup
	lastGroup     *CommentGroup
	lastGroupLine bool // lastGroup started on the line of a token
	lastLine      int  // end line of the last comment

	errors  []*Error
	exprLev int
}

// ----------------------------------------------------------------------------
// Tokens, comments and errors

func (p *parser) next() {
	if p.tok.Pos.IsValid() {
		p.prev = p.tok.Pos.Advance(p.tokText())
	}
	for {
		p.tok = p.s.Scan()
		if p.tok.Kind != CommentTok {
			return
		}
		p.addComment(p.tok)
	}
}

// tokText returns the source text of the current token.
func (p *parser) tokText() string {
	switch p.tok.Kind {
	case Ident, Int, Float, Rune, String, Imag, Illegal:
		return p.tok.Lit
	case Semicolon:
		if p.tok.Lit == "\n" {
			return ""
		}
		return ";"
	case EOF:
		return ""
	}
	return p.tok.Kind.String()
}

// addComment adds a comment to the current group or starts a new one. As
// in go/parser, a comment on the same line as a preceding token starts a
// group of its own that takes only comments on that line; other groups
// take comments on consecutive lines with no token in between.
func (p *parser) addComment(tok Token) {
	c := &Comment{Slash: tok.Pos, Text: tok.Lit}
	afterToken := p.prev.IsValid() && p.prev.Line == tok.Pos.Line
	join := p.lastGroup != nil &&
		p.prev.Offset <= p.lastGroup.End().Offset && // no token in between
		!afterToken
	if join {
		if p.lastGroupLine {
			join = tok.Pos.Line == p.lastLine
		} else {
			join = tok.Pos.Line <= p.lastLine+1
		}
	}
	if join {
		p.lastGroup.List = append(p.lastGroup.List, c)
	} else {
		p.lastGroup = &CommentGroup{List: []*Comment{c}}
		p.lastGroupLine = afterToken
		p.comments = append(p.comments, p.lastGroup)
	}
	p.lastLine = c.End().Line
	if len(tok.Lit) > 1 && tok.Lit[1] == '/' {
		p.lastLine = tok.Pos.Line
	}
}

func (p *parser) errorf(pos Pos, rule, format string, args ...any) {
	// Report at most one error per line to keep cascades short.
	if n := len(p.errors); n > 0 && p.errors[n-1].Pos.Line == pos.Line {
		return
	}
	p.errors = append(p.errors, &Error{Pos: pos, Rule: rule, Msg: fmt.Sprintf(format, args...)})
}

func (p *parser) describe(t Token) string {
	switch t.Kind {
	case Semicolon:
		if t.Lit == "\n" {
			return "newline"
		}
		return "';'"
	case Ident, Int, Float, Rune, String:
		return fmt.Sprintf("%s %s", t.Kind, t.Lit)
	case EOF:
		return "end of file"
	}
	return fmt.Sprintf("'%s'", t.Kind)
}

func (p *parser) errorExpected(what string) {
	p.errorf(p.tok.Pos, "lex.token.go", "expected %s, found %s", what, p.describe(p.tok))
}

func (p *parser) expect(k Kind) Pos {
	pos := p.tok.Pos
	if p.tok.Kind != k {
		p.errorExpected("'" + k.String() + "'")
	}
	p.next()
	return pos
}

// expectClosing is expect for the closing token of a list; it explains
// the common mistake of a missing trailing comma before a newline.
func (p *parser) expectClosing(k Kind, context string) Pos {
	if p.tok.Kind != k && p.tok.Kind == Semicolon && p.tok.Lit == "\n" {
		p.errorf(p.tok.Pos, "lex.token.go", "missing ',' before newline in %s", context)
		p.next()
	}
	return p.expect(k)
}

func (p *parser) expectSemi() {
	switch p.tok.Kind {
	case RParen, RBrace:
		// A semicolon may be omitted before a closing ")" or "}".
	case Semicolon:
		p.next()
	default:
		p.errorExpected("';' or newline")
		p.advance(stmtStart)
	}
}

func (p *parser) got(k Kind) bool {
	if p.tok.Kind == k {
		p.next()
		return true
	}
	return false
}

var stmtStart = map[Kind]bool{
	Break: true, Const: true, Continue: true, Defer: true, Fallthrough: true,
	For: true, Go: true, Goto: true, If: true, Return: true, Select: true,
	Switch: true, Type: true, Var: true,
}

var declStart = map[Kind]bool{Import: true, Const: true, Type: true, Var: true, Func: true}

// advance skips tokens up to one in the set, past a semicolon, or to EOF.
func (p *parser) advance(to map[Kind]bool) {
	for p.tok.Kind != EOF {
		if to[p.tok.Kind] {
			return
		}
		if p.tok.Kind == Semicolon {
			p.next()
			return
		}
		p.next()
	}
}

// ensureProgress skips the current token if parsing has not moved past
// start, so that error recovery cannot loop forever.
func (p *parser) ensureProgress(start Pos) {
	if p.tok.Pos == start && p.tok.Kind != EOF {
		p.next()
	}
}

// isWord reports whether the current token is the contextual word w.
// Contextual words are recognized only in Anlang mode.
func (p *parser) isWord(w string) bool {
	return p.mode == AnlangMode && p.tok.Kind == Ident && p.tok.Lit == w
}

// ----------------------------------------------------------------------------
// Identifiers

func (p *parser) name() *Name {
	pos := p.tok.Pos
	name := "_"
	if p.tok.Kind == Ident {
		name = p.tok.Lit
		p.next()
	} else {
		p.errorExpected("identifier")
	}
	return &Name{NamePos: pos, Name: name}
}

func (p *parser) nameList() []*Name {
	list := []*Name{p.name()}
	for p.got(Comma) {
		list = append(list, p.name())
	}
	return list
}

// ----------------------------------------------------------------------------
// Source file and declarations

func (p *parser) parseFile() *File {
	if p.tok.Kind != Package {
		p.errorExpected("'package'")
		return nil
	}
	f := &File{Package: p.tok.Pos}
	p.next()
	f.Name = p.name()
	p.expectSemi()

	if p.tok.Kind == Profile {
		c := &ProfileClause{Profile: p.tok.Pos}
		p.next()
		c.Names = p.nameList()
		f.Profile = c
		p.expectSemi()
	}

	for p.tok.Kind == Import {
		d := p.genDecl(Import, p.importSpec)
		f.Decls = append(f.Decls, d)
		p.expectSemi()
	}

	for p.tok.Kind != EOF {
		start := p.tok.Pos
		if d := p.topLevelDecl(); d != nil {
			f.Decls = append(f.Decls, d)
		}
		if p.tok.Kind != EOF {
			p.expectSemi()
		}
		p.ensureProgress(start)
	}
	for _, d := range f.Decls {
		if g, ok := d.(*GenDecl); ok && g.Tok == Import {
			for _, s := range g.Specs {
				f.Imports = append(f.Imports, s.(*ImportSpec))
			}
		}
	}
	f.EOF = p.tok.Pos
	return f
}

func (p *parser) topLevelDecl() Decl {
	switch p.tok.Kind {
	case Const:
		return p.genDecl(Const, p.valueSpec)
	case Var:
		return p.genDecl(Var, p.varSpec)
	case Type:
		return p.genDecl(Type, p.typeSpec)
	case Func:
		return p.funcDecl()
	case Import:
		p.errorf(p.tok.Pos, "lex.token.go", "imports must appear before other declarations")
		return p.genDecl(Import, p.importSpec)
	case Profile:
		p.errorf(p.tok.Pos, "lex.token.go", "the profile clause must follow the package clause")
		p.next()
		p.nameList()
		return nil
	case Ident:
		switch {
		case p.isWord("interrupt") || p.isWord("trap"):
			return p.handlerDecl()
		case p.isWord("gpu"):
			pos := p.tok.Pos
			p.next()
			if p.tok.Kind != Func {
				p.errorExpected("'func' after gpu")
				p.advance(declStart)
				return nil
			}
			d := p.funcDecl()
			d.Gpu = pos
			return d
		}
	}
	p.errorExpected("declaration")
	p.advance(declStart)
	return nil
}

// genDecl parses a declaration with a single spec or a parenthesized group.
func (p *parser) genDecl(tok Kind, spec func() Spec) *GenDecl {
	d := &GenDecl{TokPos: p.tok.Pos, Tok: tok}
	p.next()
	if p.tok.Kind == LParen {
		d.Lparen = p.tok.Pos
		p.next()
		for p.tok.Kind != RParen && p.tok.Kind != EOF {
			start := p.tok.Pos
			d.Specs = append(d.Specs, spec())
			p.expectSemi()
			p.ensureProgress(start)
		}
		d.Rparen = p.expect(RParen)
		return d
	}
	d.Specs = append(d.Specs, spec())
	return d
}

func (p *parser) importSpec() Spec {
	s := &ImportSpec{}
	switch p.tok.Kind {
	case Ident:
		s.Name = p.name()
	case Period:
		if p.mode == AnlangMode {
			p.errorf(p.tok.Pos, "decl.import.no-blank-dot", "dot imports are removed in Anlang")
		}
		s.Name = &Name{NamePos: p.tok.Pos, Name: "."}
		p.next()
	}
	if p.tok.Kind != String {
		p.errorExpected("import path")
		s.Path = &BasicLit{ValuePos: p.tok.Pos, Kind: String, Value: `""`}
		p.advance(nil)
		return s
	}
	s.Path = &BasicLit{ValuePos: p.tok.Pos, Kind: String, Value: p.tok.Lit}
	p.next()
	return s
}

func (p *parser) valueSpec() Spec {
	s := &ValueSpec{Names: p.nameList()}
	if p.tok.Kind != Assign && p.tok.Kind != Semicolon && p.tok.Kind != RParen {
		s.Type = p.typ()
	}
	if p.got(Assign) {
		s.Values = p.exprList()
	}
	return s
}

// varSpec is valueSpec for var declarations, which need a type or values.
func (p *parser) varSpec() Spec {
	s := p.valueSpec().(*ValueSpec)
	if s.Type == nil && s.Values == nil {
		p.errorf(s.Pos(), "lex.token.go", "missing variable type or initialization")
	}
	return s
}

func (p *parser) typeSpec() Spec {
	s := &TypeSpec{Name: p.name()}
	if p.tok.Kind == LBrack {
		// A generic type "T[P C]" or an array type "T [N]E" (as in go/parser).
		lbrack := p.tok.Pos
		p.next()
		if p.tok.Kind != Ident {
			s.Type = p.arrayType(lbrack, nil)
			return s
		}
		var x Expr = p.name()
		if p.tok.Kind != LBrack {
			p.exprLev++
			x = p.binaryExpr(p.primaryExpr(x), 1)
			p.exprLev--
		}
		pname, ptype := extractName(x, p.tok.Kind == Comma)
		if pname == nil || (ptype == nil && p.tok.Kind == RBrack) {
			s.Type = p.arrayType(lbrack, x)
			return s
		}
		s.TypeParams = p.typeParams(lbrack, pname, ptype)
	}
	if p.tok.Kind == Assign {
		s.Assign = p.tok.Pos
		p.next()
	}
	s.Type = p.typ()
	return s
}

// extractName splits x into a type parameter name and the start of its
// constraint, if x can be read that way (as in go/parser).
func extractName(x Expr, force bool) (*Name, Expr) {
	switch x := x.(type) {
	case *Name:
		return x, nil
	case *BinaryExpr:
		switch x.Op {
		case Mul:
			if name, ok := x.X.(*Name); ok && (force || isTypeElem(x.Y)) {
				return name, &StarExpr{Star: x.OpPos, X: x.Y}
			}
		case Or:
			if name, lhs := extractName(x.X, force || isTypeElem(x.Y)); name != nil && lhs != nil {
				op := *x
				op.X = lhs
				return name, &op
			}
		}
	case *CallExpr:
		if name, ok := x.Fun.(*Name); ok && len(x.Args) == 1 && !x.Ellipsis.IsValid() && (force || isTypeElem(x.Args[0])) {
			return name, &ParenExpr{Lparen: x.Lparen, X: x.Args[0], Rparen: x.Rparen}
		}
	}
	return nil, nil
}

// isTypeElem reports whether x is certainly a type element, not a value.
func isTypeElem(x Expr) bool {
	switch x := x.(type) {
	case *ArrayType, *StructType, *FuncType, *InterfaceType, *MapType, *ChanType, *EnumType:
		return true
	case *BinaryExpr:
		return isTypeElem(x.X) || isTypeElem(x.Y)
	case *UnaryExpr:
		return x.Op == Tilde
	case *ParenExpr:
		return isTypeElem(x.X)
	}
	return false
}

// typeParams parses the rest of a type parameter list whose first name,
// and possibly the start of its constraint, has been read.
func (p *parser) typeParams(lbrack Pos, first *Name, firstType Expr) *FieldList {
	l := &FieldList{Opening: lbrack}
	names := []*Name{first}
	var typ Expr
	if firstType == nil {
		for p.got(Comma) {
			names = append(names, p.name())
		}
		typ = p.typeElem()
	} else {
		typ = p.typeElemRest(firstType)
	}
	l.List = append(l.List, &Field{Names: names, Type: typ})
	for p.got(Comma) {
		if p.tok.Kind == RBrack {
			break
		}
		names := p.nameList()
		l.List = append(l.List, &Field{Names: names, Type: p.typeElem()})
	}
	l.Closing = p.expectClosing(RBrack, "type parameter list")
	return l
}

// typeElem parses a constraint: a union of terms, each a type or ~type.
func (p *parser) typeElem() Expr { return p.typeElemRest(p.typeTerm()) }

func (p *parser) typeElemRest(x Expr) Expr {
	for p.tok.Kind == Or {
		pos := p.tok.Pos
		p.next()
		x = &BinaryExpr{X: x, OpPos: pos, Op: Or, Y: p.typeTerm()}
	}
	return x
}

func (p *parser) typeTerm() Expr {
	if p.tok.Kind == Tilde {
		pos := p.tok.Pos
		p.next()
		return &UnaryExpr{OpPos: pos, Op: Tilde, X: p.typ()}
	}
	return p.typ()
}

func (p *parser) funcDecl() *FuncDecl {
	d := &FuncDecl{}
	funcPos := p.expect(Func)
	if p.tok.Kind == LParen {
		d.Recv = p.params(LParen, RParen)
	}
	d.Name = p.name()
	ft := &FuncType{Func: funcPos}
	if p.tok.Kind == LBrack {
		lbrack := p.tok.Pos
		p.next()
		if p.tok.Kind == RBrack {
			p.errorf(p.tok.Pos, "lex.token.go", "empty type parameter list")
			p.next()
		} else {
			ft.TypeParams = p.typeParams(lbrack, p.name(), nil)
		}
	}
	p.signature(ft)
	d.Type = ft
	switch p.tok.Kind {
	case LBrace:
		p.exprLev++
		d.Body = p.funcBody()
		p.exprLev--
	case Assign:
		if p.mode == GoMode {
			p.errorExpected("function body")
		}
		p.next()
		d.Binding = p.expr()
	}
	return d
}

func (p *parser) handlerDecl() *FuncDecl {
	d := &FuncDecl{Handler: p.tok.Lit, HandlerPos: p.tok.Pos}
	p.next()
	d.Name = p.name()
	d.Type = &FuncType{}
	p.signature(d.Type)
	if p.tok.Kind != LBrace {
		p.errorf(p.tok.Pos, "decl.func.body", "%s handler must have a body", d.Handler)
		return d
	}
	d.Body = p.funcBody()
	return d
}

// signature parses parameters and results into ft.
func (p *parser) signature(ft *FuncType) {
	ft.Params = p.params(LParen, RParen)
	switch p.tok.Kind {
	case LParen:
		ft.Results = p.params(LParen, RParen)
	case Ident, LBrack, Struct, Mul, Func, Interface, Map, Chan, Arrow, Enum:
		t := p.typ()
		ft.Results = &FieldList{List: []*Field{{Type: t}}}
	}
}

// params parses a parameter list: entries "names [inout] [...]Type" or,
// when no entry has a name, "[inout] [...]Type". The same grammar is used
// for receivers and for the fields of enum variants.
func (p *parser) params(open, close Kind) *FieldList {
	l := &FieldList{Opening: p.expect(open)}
	p.exprLev++

	// Collect entries first: whether the list is named is known only at
	// the end, as in go/parser.
	type entry struct {
		name  *Name  // a lone identifier: a name or a type
		field *Field // anything else
	}
	var entries []entry
	named := false
	for p.tok.Kind != close && p.tok.Kind != EOF {
		var e entry
		if p.tok.Kind == Ident {
			n := p.name()
			switch p.tok.Kind {
			case Comma, close:
				e.name = n
			case Period:
				p.next()
				t := Expr(&SelectorExpr{X: n, Sel: p.name()})
				if p.tok.Kind == LBrack {
					t = p.typeInstance(t)
				}
				e.field = &Field{Type: t}
			case LBrack:
				if name, t := p.arrayFieldOrInstance(n); name != nil {
					e.field = &Field{Names: []*Name{name}, Type: t}
					named = true
				} else {
					e.field = &Field{Type: t}
				}
			default:
				e.field = &Field{Names: []*Name{n}}
				p.paramType(e.field)
				named = true
			}
		} else {
			e.field = &Field{}
			p.paramType(e.field)
		}
		entries = append(entries, e)
		if !p.got(Comma) {
			break
		}
	}
	p.exprLev--
	l.Closing = p.expectClosing(close, "parameter list")

	if !named {
		for _, e := range entries {
			if e.name != nil {
				l.List = append(l.List, &Field{Type: e.name})
			} else {
				l.List = append(l.List, e.field)
			}
		}
		return l
	}
	// Named list: lone names group with the next entry that has a type.
	var pending []*Name
	for _, e := range entries {
		switch {
		case e.name != nil:
			pending = append(pending, e.name)
		case len(e.field.Names) == 0:
			p.errorf(e.field.Pos(), "lex.token.go", "mixed named and unnamed parameters")
		default:
			e.field.Names = append(pending, e.field.Names...)
			pending = nil
			l.List = append(l.List, e.field)
		}
	}
	if len(pending) > 0 {
		p.errorf(pending[len(pending)-1].Pos(), "lex.token.go", "missing parameter type")
	}
	return l
}

// arrayFieldOrInstance parses "[...]" after the name n of a parameter or
// field. It returns n and an array type for "n [N]E" and "n []E", or nil
// and a generic instantiation for "n[A, B]" (as in go/parser).
func (p *parser) arrayFieldOrInstance(n *Name) (*Name, Expr) {
	lbrack := p.expect(LBrack)
	var args []Expr
	if p.tok.Kind == DotDotDot {
		args = append(args, &Ellipsis{Ellipsis: p.tok.Pos})
		p.next()
	} else if p.tok.Kind != RBrack {
		p.exprLev++
		args = append(args, p.typeOrExpr())
		for p.got(Comma) {
			if p.tok.Kind == RBrack {
				break
			}
			args = append(args, p.typeOrExpr())
		}
		p.exprLev--
	}
	rbrack := p.expect(RBrack)
	if len(args) == 0 {
		return n, &ArrayType{Lbrack: lbrack, Elt: p.typ()}
	}
	if len(args) == 1 {
		if elt := p.tryType(); elt != nil {
			return n, &ArrayType{Lbrack: lbrack, Len: args[0], Elt: elt}
		}
	}
	return nil, &IndexExpr{X: n, Lbrack: lbrack, Indices: args, Rbrack: rbrack}
}

// paramType parses "[inout] [...]Type" into f.
func (p *parser) paramType(f *Field) {
	if p.tok.Kind == Inout {
		f.Inout = p.tok.Pos
		p.next()
	}
	if p.tok.Kind == DotDotDot {
		pos := p.tok.Pos
		p.next()
		f.Type = &Ellipsis{Ellipsis: pos, Elt: p.typ()}
		return
	}
	f.Type = p.typ()
}

func (p *parser) funcBody() *BlockStmt {
	return p.blockWithContracts("requires", "ensures")
}

// blockWithContracts parses a block whose leading statements may be
// contract clauses introduced by the given words
// (lex.context.contract-region).
func (p *parser) blockWithContracts(words ...string) *BlockStmt {
	b := &BlockStmt{Lbrace: p.expect(LBrace)}
	for p.mode == AnlangMode && p.tok.Kind == Ident && isOneOf(p.tok.Lit, words) {
		c := &ContractStmt{WordPos: p.tok.Pos, Word: p.tok.Lit}
		p.next()
		c.X = p.expr()
		b.List = append(b.List, c)
		p.expectSemi()
	}
	b.List = append(b.List, p.stmtList()...)
	b.Rbrace = p.expect(RBrace)
	return b
}

func isOneOf(s string, list []string) bool {
	for _, w := range list {
		if s == w {
			return true
		}
	}
	return false
}

// ----------------------------------------------------------------------------
// Types

// typ parses a type.
func (p *parser) typ() Expr {
	if t := p.tryType(); t != nil {
		return t
	}
	pos := p.tok.Pos
	p.errorExpected("type")
	return &BadExpr{From: pos, To: pos}
}

// tryType parses a type if one starts at the current token, or returns nil.
func (p *parser) tryType() Expr {
	switch p.tok.Kind {
	case Ident:
		t := p.typeName()
		if p.tok.Kind == LBrack {
			t = p.typeInstance(t)
		}
		return t
	case LBrack:
		lbrack := p.tok.Pos
		p.next()
		return p.arrayType(lbrack, nil)
	case Struct:
		return p.structType()
	case Mul:
		pos := p.tok.Pos
		p.next()
		return &StarExpr{Star: pos, X: p.typ()}
	case Func:
		pos := p.tok.Pos
		p.next()
		ft := &FuncType{Func: pos}
		p.signature(ft)
		return ft
	case Interface:
		return p.interfaceType()
	case Map:
		pos := p.tok.Pos
		p.next()
		p.expect(LBrack)
		key := p.typ()
		p.expect(RBrack)
		return &MapType{Map: pos, Key: key, Value: p.typ()}
	case Chan:
		pos := p.tok.Pos
		p.next()
		dir := ChanBoth
		if p.tok.Kind == Arrow {
			p.next()
			dir = ChanSend
		}
		return &ChanType{Begin: pos, Dir: dir, Value: p.typ()}
	case Arrow:
		pos := p.tok.Pos
		p.next()
		p.expect(Chan)
		return &ChanType{Begin: pos, Dir: ChanRecv, Value: p.typ()}
	case Enum:
		return p.enumType()
	case LParen:
		lparen := p.tok.Pos
		p.next()
		t := p.typ()
		return &ParenExpr{Lparen: lparen, X: t, Rparen: p.expect(RParen)}
	}
	return nil
}

func (p *parser) typeName() Expr {
	var x Expr = p.name()
	if p.tok.Kind == Period {
		p.next()
		x = &SelectorExpr{X: x, Sel: p.name()}
	}
	return x
}

// typeInstance parses the type arguments of a generic type.
func (p *parser) typeInstance(x Expr) Expr {
	lbrack := p.expect(LBrack)
	p.exprLev++
	var args []Expr
	for p.tok.Kind != RBrack && p.tok.Kind != EOF {
		args = append(args, p.typ())
		if !p.got(Comma) {
			break
		}
	}
	p.exprLev--
	return &IndexExpr{X: x, Lbrack: lbrack, Indices: args, Rbrack: p.expectClosing(RBrack, "type argument list")}
}

// arrayType parses the rest of an array or slice type after "[". If
// length is not nil, it is the already parsed length.
func (p *parser) arrayType(lbrack Pos, length Expr) Expr {
	if length == nil {
		switch p.tok.Kind {
		case RBrack:
			p.next()
			return &ArrayType{Lbrack: lbrack, Elt: p.typ()}
		case DotDotDot:
			length = &Ellipsis{Ellipsis: p.tok.Pos}
			p.next()
		default:
			p.exprLev++
			length = p.expr()
			p.exprLev--
		}
	}
	p.expect(RBrack)
	return &ArrayType{Lbrack: lbrack, Len: length, Elt: p.typ()}
}

func (p *parser) structType() *StructType {
	t := &StructType{Struct: p.tok.Pos}
	p.next()
	t.Lbrace = p.expect(LBrace)
	for p.isWord("invariant") || p.isWord("layout") {
		c := &StructClause{WordPos: p.tok.Pos, Word: p.tok.Lit}
		p.next()
		if c.Word == "layout" {
			c.X = p.name()
		} else {
			c.X = p.expr()
		}
		t.Clauses = append(t.Clauses, c)
		p.expectSemi()
	}
	for p.tok.Kind == Ident || p.tok.Kind == Mul || p.tok.Kind == LParen {
		t.Fields = append(t.Fields, p.fieldDecl())
		p.expectSemi()
	}
	t.Rbrace = p.expect(RBrace)
	return t
}

func (p *parser) fieldDecl() *Field {
	f := &Field{}
	switch p.tok.Kind {
	case Ident:
		n := p.name()
		switch p.tok.Kind {
		case Period, String, Semicolon, RBrace:
			// Embedded type: T, pkg.T or T[A].
			var t Expr = n
			if p.tok.Kind == Period {
				p.next()
				t = &SelectorExpr{X: n, Sel: p.name()}
				if p.tok.Kind == LBrack {
					t = p.typeInstance(t)
				}
			}
			f.Type = t
		case LBrack:
			name, t := p.arrayFieldOrInstance(n)
			if name != nil {
				f.Names = []*Name{name}
			}
			f.Type = t
		default:
			f.Names = []*Name{n}
			for p.got(Comma) {
				f.Names = append(f.Names, p.name())
			}
			f.Type = p.typ()
		}
	case Mul:
		pos := p.tok.Pos
		p.next()
		t := p.typeName()
		if p.tok.Kind == LBrack {
			t = p.typeInstance(t)
		}
		f.Type = &StarExpr{Star: pos, X: t}
	case LParen:
		p.errorf(p.tok.Pos, "lex.token.go", "cannot parenthesize embedded type")
		f.Type = p.typ()
	}
	if p.tok.Kind == String {
		f.Tag = &BasicLit{ValuePos: p.tok.Pos, Kind: String, Value: p.tok.Lit}
		p.next()
	}
	return f
}

func (p *parser) interfaceType() *InterfaceType {
	t := &InterfaceType{Interface: p.tok.Pos}
	p.next()
	t.Lbrace = p.expect(LBrace)
	for p.tok.Kind != RBrace && p.tok.Kind != EOF {
		start := p.tok.Pos
		if p.tok.Kind == Ident {
			n := p.name()
			if p.tok.Kind == LParen {
				ft := &FuncType{}
				p.signature(ft)
				t.Elems = append(t.Elems, &Field{Names: []*Name{n}, Type: ft})
			} else {
				var x Expr = n
				if p.tok.Kind == Period {
					p.next()
					x = &SelectorExpr{X: n, Sel: p.name()}
				}
				if p.tok.Kind == LBrack {
					x = p.typeInstance(x)
				}
				t.Elems = append(t.Elems, &Field{Type: p.typeElemRest(x)})
			}
		} else {
			t.Elems = append(t.Elems, &Field{Type: p.typeElem()})
		}
		p.expectSemi()
		p.ensureProgress(start)
	}
	t.Rbrace = p.expect(RBrace)
	return t
}

// enumType parses enum { Variant; Variant{fields} } (Anlang, type.enum).
func (p *parser) enumType() *EnumType {
	t := &EnumType{Enum: p.tok.Pos}
	p.next()
	t.Lbrace = p.expect(LBrace)
	for p.tok.Kind == Ident {
		v := &Variant{Name: p.name()}
		if p.tok.Kind == LBrace {
			v.Fields = p.params(LBrace, RBrace)
		}
		t.Variants = append(t.Variants, v)
		p.expectSemi()
	}
	t.Rbrace = p.expect(RBrace)
	return t
}

// ----------------------------------------------------------------------------
// Expressions

func (p *parser) expr() Expr { return p.binaryExpr(nil, 1) }

// typeOrExpr parses an expression that may also be a type; types are
// operands, so this is the same as expr.
func (p *parser) typeOrExpr() Expr { return p.expr() }

func (p *parser) exprList() []Expr {
	list := []Expr{p.expr()}
	for p.got(Comma) {
		list = append(list, p.expr())
	}
	return list
}

func precedence(k Kind) int {
	switch k {
	case LOr:
		return 1
	case LAnd:
		return 2
	case Eql, Neq, Lss, Leq, Gtr, Geq:
		return 3
	case Add, Sub, Or, Xor:
		return 4
	case Mul, Quo, Rem, Shl, Shr, And, AndNot:
		return 5
	}
	return 0
}

// binaryExpr parses a binary expression with operators of at least
// precedence prec. If x is not nil, it is the already parsed left operand.
func (p *parser) binaryExpr(x Expr, prec int) Expr {
	if x == nil {
		x = p.unaryExpr()
	}
	for {
		op := p.tok.Kind
		oprec := precedence(op)
		if oprec < prec {
			return x
		}
		pos := p.tok.Pos
		p.next()
		y := p.binaryExpr(nil, oprec+1)
		x = &BinaryExpr{X: x, OpPos: pos, Op: op, Y: y}
	}
}

func (p *parser) unaryExpr() Expr {
	switch p.tok.Kind {
	case Add, Sub, Not, Xor, And, Tilde:
		pos, op := p.tok.Pos, p.tok.Kind
		p.next()
		return &UnaryExpr{OpPos: pos, Op: op, X: p.unaryExpr()}
	case Arrow:
		pos := p.tok.Pos
		p.next()
		if p.tok.Kind == Chan {
			// <-chan T, possibly followed by a conversion or literal.
			p.next()
			t := &ChanType{Begin: pos, Dir: ChanRecv, Value: p.typ()}
			return p.primaryExpr(t)
		}
		return &UnaryExpr{OpPos: pos, Op: Arrow, X: p.unaryExpr()}
	case Mul:
		pos := p.tok.Pos
		p.next()
		return &StarExpr{Star: pos, X: p.unaryExpr()}
	}
	return p.primaryExpr(nil)
}

// operand parses a name, literal, parenthesized expression, function
// literal or type.
func (p *parser) operand() Expr {
	switch p.tok.Kind {
	case Ident:
		return p.name()
	case Int, Float, Rune, String, Imag:
		x := &BasicLit{ValuePos: p.tok.Pos, Kind: p.tok.Kind, Value: p.tok.Lit}
		p.next()
		return x
	case LParen:
		lparen := p.tok.Pos
		p.next()
		p.exprLev++
		x := p.typeOrExpr()
		p.exprLev--
		return &ParenExpr{Lparen: lparen, X: x, Rparen: p.expectClosing(RParen, "parenthesized expression")}
	case Func:
		pos := p.tok.Pos
		p.next()
		ft := &FuncType{Func: pos}
		p.signature(ft)
		if p.tok.Kind == LBrace {
			p.exprLev++
			body := p.block()
			p.exprLev--
			return &FuncLit{Type: ft, Body: body}
		}
		return ft
	}
	if t := p.tryType(); t != nil {
		return t
	}
	pos := p.tok.Pos
	p.errorExpected("expression")
	p.advance(stmtStart)
	return &BadExpr{From: pos, To: p.tok.Pos}
}

// primaryExpr parses an operand followed by selectors, indexes, slices,
// type assertions, calls and composite literal bodies. If x is not nil,
// it is the already parsed operand.
func (p *parser) primaryExpr(x Expr) Expr {
	if x == nil {
		x = p.operand()
	}
	for {
		switch p.tok.Kind {
		case Period:
			p.next()
			switch p.tok.Kind {
			case Ident:
				x = &SelectorExpr{X: x, Sel: p.name()}
			case LParen:
				lparen := p.tok.Pos
				p.next()
				var t Expr
				if p.tok.Kind == Type {
					p.next()
				} else {
					t = p.typ()
				}
				x = &TypeAssertExpr{X: x, Lparen: lparen, Type: t, Rparen: p.expect(RParen)}
			default:
				p.errorExpected("selector or type assertion")
				return x
			}
		case LBrack:
			x = p.indexOrSlice(x)
		case LParen:
			x = p.call(x)
		case LBrace:
			// A composite literal, unless "{" starts a block in the header
			// of an if, for or switch statement.
			if !isLiteralType(x) || (p.exprLev < 0 && isTypeName(x)) {
				return x
			}
			x = p.compositeLit(x)
		default:
			return x
		}
	}
}

func isTypeName(x Expr) bool {
	switch x := x.(type) {
	case *Name, *SelectorExpr:
		return true
	case *IndexExpr:
		return isTypeName(x.X)
	}
	return false
}

func isLiteralType(x Expr) bool {
	switch x := x.(type) {
	case *Name, *SelectorExpr, *ArrayType, *StructType, *MapType:
		return true
	case *IndexExpr:
		return isTypeName(x.X)
	}
	return false
}

func (p *parser) indexOrSlice(x Expr) Expr {
	lbrack := p.expect(LBrack)
	p.exprLev++
	defer func() { p.exprLev-- }()
	var idx [3]Expr
	colons := 0
	if p.tok.Kind != Colon {
		idx[0] = p.typeOrExpr()
	}
	for p.tok.Kind == Colon && colons < 2 {
		colons++
		p.next()
		if p.tok.Kind != Colon && p.tok.Kind != RBrack && p.tok.Kind != EOF {
			idx[colons] = p.expr()
		}
	}
	if colons > 0 {
		s := &SliceExpr{X: x, Lbrack: lbrack, Low: idx[0], High: idx[1], Max: idx[2], Slice3: colons == 2}
		if s.Slice3 && (s.High == nil || s.Max == nil) {
			p.errorf(lbrack, "lex.token.go", "middle and final index required in 3-index slice")
		}
		s.Rbrack = p.expect(RBrack)
		return s
	}
	ix := &IndexExpr{X: x, Lbrack: lbrack, Indices: []Expr{idx[0]}}
	for p.got(Comma) {
		if p.tok.Kind == RBrack {
			break
		}
		ix.Indices = append(ix.Indices, p.typeOrExpr())
	}
	ix.Rbrack = p.expectClosing(RBrack, "index or type argument list")
	return ix
}

func (p *parser) call(fun Expr) *CallExpr {
	c := &CallExpr{Fun: fun, Lparen: p.expect(LParen)}
	p.exprLev++
	for p.tok.Kind != RParen && p.tok.Kind != EOF {
		c.Args = append(c.Args, p.typeOrExpr())
		if p.tok.Kind == DotDotDot {
			c.Ellipsis = p.tok.Pos
			p.next()
		}
		if !p.got(Comma) {
			break
		}
	}
	p.exprLev--
	c.Rparen = p.expectClosing(RParen, "argument list")
	return c
}

func (p *parser) compositeLit(typ Expr) *CompositeLit {
	lit := &CompositeLit{Type: typ, Lbrace: p.expect(LBrace)}
	p.exprLev++
	for p.tok.Kind != RBrace && p.tok.Kind != EOF {
		lit.Elts = append(lit.Elts, p.element())
		if !p.got(Comma) {
			break
		}
	}
	p.exprLev--
	lit.Rbrace = p.expectClosing(RBrace, "composite literal")
	return lit
}

func (p *parser) element() Expr {
	x := p.elementValue()
	if p.tok.Kind == Colon {
		colon := p.tok.Pos
		p.next()
		return &KeyValueExpr{Key: x, Colon: colon, Value: p.elementValue()}
	}
	return x
}

func (p *parser) elementValue() Expr {
	if p.tok.Kind == LBrace {
		return p.compositeLit(nil)
	}
	return p.expr()
}

// ----------------------------------------------------------------------------
// Statements

func (p *parser) stmtList() []Stmt {
	var list []Stmt
	for p.tok.Kind != Case && p.tok.Kind != Default && p.tok.Kind != RBrace && p.tok.Kind != EOF {
		start := p.tok.Pos
		if s := p.stmt(); s != nil {
			list = append(list, s)
		}
		p.ensureProgress(start)
	}
	return list
}

func (p *parser) block() *BlockStmt {
	b := &BlockStmt{Lbrace: p.expect(LBrace)}
	b.List = p.stmtList()
	b.Rbrace = p.expect(RBrace)
	return b
}

// stmt parses a statement and its terminating semicolon.
func (p *parser) stmt() Stmt {
	var s Stmt
	switch p.tok.Kind {
	case Const:
		s = &DeclStmt{Decl: p.genDecl(Const, p.valueSpec)}
	case Var:
		s = &DeclStmt{Decl: p.genDecl(Var, p.varSpec)}
	case Type:
		s = &DeclStmt{Decl: p.genDecl(Type, p.typeSpec)}
	case Ident, Int, Float, Rune, String, Imag, Func, LParen, LBrack, Struct, Map, Chan, Interface,
		Add, Sub, Mul, And, Xor, Arrow, Not, Enum:
		s = p.simpleStmt(true, false)
		if _, ok := s.(*LabeledStmt); ok {
			return s
		}
	case Go:
		pos := p.tok.Pos
		p.next()
		s = &GoStmt{Go: pos, Call: p.callExpr("go")}
	case Defer:
		pos := p.tok.Pos
		p.next()
		s = &DeferStmt{Defer: pos, Call: p.callExpr("defer")}
	case Return:
		r := &ReturnStmt{Return: p.tok.Pos}
		p.next()
		if p.tok.Kind != Semicolon && p.tok.Kind != RBrace {
			r.Results = p.exprList()
		}
		s = r
	case Break, Continue, Goto, Fallthrough:
		b := &BranchStmt{TokPos: p.tok.Pos, Tok: p.tok.Kind}
		p.next()
		if b.Tok != Fallthrough && p.tok.Kind == Ident {
			b.Label = p.name()
		}
		s = b
	case LBrace:
		s = p.block()
	case If:
		s = p.ifStmt()
	case Switch:
		s = p.switchStmt()
	case Select:
		s = p.selectStmt()
	case For:
		s = p.forStmt()
	case Semicolon:
		s = &EmptyStmt{Semicolon: p.tok.Pos, Implicit: p.tok.Lit == "\n"}
		p.next()
		return s
	default:
		pos := p.tok.Pos
		p.errorExpected("statement")
		p.advance(stmtStart)
		return &BadStmt{From: pos, To: p.tok.Pos}
	}
	p.expectSemi()
	return s
}

func (p *parser) callExpr(what string) *CallExpr {
	x := p.unaryExpr()
	if c, ok := x.(*CallExpr); ok {
		return c
	}
	p.errorf(x.Pos(), "lex.token.go", "expression in %s must be a function call", what)
	return &CallExpr{Fun: x, Lparen: x.End(), Rparen: x.End()}
}

// simpleStmt parses a simple statement. If labelOK, an identifier
// followed by ":" is a label. If rangeOK, a range clause is allowed; it
// is returned as an AssignStmt whose single Rhs is a UnaryExpr with Op
// Range, and whose Tok is Illegal when there are no iteration variables.
func (p *parser) simpleStmt(labelOK, rangeOK bool) Stmt {
	if rangeOK && p.tok.Kind == Range {
		pos := p.tok.Pos
		p.next()
		x := p.expr()
		return &AssignStmt{Tok: Illegal, Rhs: []Expr{&UnaryExpr{OpPos: pos, Op: Range, X: x}}}
	}
	lhs := p.exprList()
	switch tok := p.tok.Kind; tok {
	case Define, Assign, AddAssign, SubAssign, MulAssign, QuoAssign, RemAssign,
		AndAssign, OrAssign, XorAssign, ShlAssign, ShrAssign, AndNotAssign:
		pos := p.tok.Pos
		p.next()
		if rangeOK && p.tok.Kind == Range && (tok == Define || tok == Assign) {
			rpos := p.tok.Pos
			p.next()
			x := p.expr()
			return &AssignStmt{Lhs: lhs, TokPos: pos, Tok: tok, Rhs: []Expr{&UnaryExpr{OpPos: rpos, Op: Range, X: x}}}
		}
		return &AssignStmt{Lhs: lhs, TokPos: pos, Tok: tok, Rhs: p.exprList()}
	}
	if len(lhs) > 1 {
		p.errorExpected("':=' or '='")
		return &BadStmt{From: lhs[0].Pos(), To: p.tok.Pos}
	}
	x := lhs[0]
	switch p.tok.Kind {
	case Colon:
		if n, ok := x.(*Name); ok && labelOK {
			colon := p.tok.Pos
			p.next()
			if p.tok.Kind == RBrace {
				return &LabeledStmt{Label: n, Colon: colon, Stmt: &EmptyStmt{Semicolon: p.tok.Pos, Implicit: true}}
			}
			return &LabeledStmt{Label: n, Colon: colon, Stmt: p.stmt()}
		}
	case Arrow:
		arrow := p.tok.Pos
		p.next()
		return &SendStmt{Chan: x, Arrow: arrow, Value: p.expr()}
	case Inc, Dec:
		s := &IncDecStmt{X: x, TokPos: p.tok.Pos, Tok: p.tok.Kind}
		p.next()
		return s
	}
	return &ExprStmt{X: x}
}

func (p *parser) ifStmt() *IfStmt {
	s := &IfStmt{If: p.expect(If)}
	s.Init, s.Cond = p.ifHeader()
	s.Body = p.block()
	if p.got(Else) {
		switch p.tok.Kind {
		case If:
			s.Else = p.ifStmt()
		case LBrace:
			s.Else = p.block()
		default:
			p.errorExpected("if statement or block")
			s.Else = &BadStmt{From: p.tok.Pos, To: p.tok.Pos}
		}
	}
	return s
}

func (p *parser) ifHeader() (init Stmt, cond Expr) {
	if p.tok.Kind == LBrace {
		p.errorf(p.tok.Pos, "lex.token.go", "missing condition in if statement")
		return nil, &BadExpr{From: p.tok.Pos, To: p.tok.Pos}
	}
	outer := p.exprLev
	p.exprLev = -1
	if p.tok.Kind != Semicolon {
		init = p.simpleStmt(false, false)
	}
	if p.tok.Kind == Semicolon {
		p.next()
		if p.tok.Kind == LBrace {
			p.errorf(p.tok.Pos, "lex.token.go", "missing condition in if statement")
		} else {
			cond = p.stmtToExpr(p.simpleStmt(false, false), "if")
		}
	} else {
		cond = p.stmtToExpr(init, "if")
		init = nil
	}
	p.exprLev = outer
	if cond == nil {
		cond = &BadExpr{From: p.tok.Pos, To: p.tok.Pos}
	}
	return init, cond
}

func (p *parser) stmtToExpr(s Stmt, what string) Expr {
	if s == nil {
		return nil
	}
	if es, ok := s.(*ExprStmt); ok {
		return es.X
	}
	p.errorf(s.Pos(), "lex.token.go", "cannot use statement as value in %s header", what)
	return &BadExpr{From: s.Pos(), To: s.End()}
}

func (p *parser) switchStmt() Stmt {
	pos := p.expect(Switch)
	var s1, s2 Stmt
	if p.tok.Kind != LBrace {
		outer := p.exprLev
		p.exprLev = -1
		if p.tok.Kind != Semicolon {
			s2 = p.simpleStmt(false, false)
		}
		if p.tok.Kind == Semicolon {
			p.next()
			s1, s2 = s2, nil
			if p.tok.Kind != LBrace {
				s2 = p.simpleStmt(false, false)
			}
		}
		p.exprLev = outer
	}
	typeSwitch := isTypeSwitchGuard(s2)
	body := &BlockStmt{Lbrace: p.expect(LBrace)}
	for p.tok.Kind == Case || p.tok.Kind == Default {
		body.List = append(body.List, p.caseClause(typeSwitch))
	}
	body.Rbrace = p.expect(RBrace)
	if typeSwitch {
		return &TypeSwitchStmt{Switch: pos, Init: s1, Assign: s2, Body: body}
	}
	return &SwitchStmt{Switch: pos, Init: s1, Tag: p.stmtToExpr(s2, "switch"), Body: body}
}

// isTypeSwitchGuard reports whether s is x.(type) or v := x.(type),
// possibly parenthesized; anlang fmt removes the parentheses.
func isTypeSwitchGuard(s Stmt) bool {
	switch s := s.(type) {
	case *ExprStmt:
		ta, ok := unparen(s.X).(*TypeAssertExpr)
		return ok && ta.Type == nil
	case *AssignStmt:
		if s.Tok == Define && len(s.Lhs) == 1 && len(s.Rhs) == 1 {
			ta, ok := unparen(s.Rhs[0]).(*TypeAssertExpr)
			return ok && ta.Type == nil
		}
	}
	return false
}

func unparen(x Expr) Expr {
	for {
		p, ok := x.(*ParenExpr)
		if !ok {
			return x
		}
		x = p.X
	}
}

func (p *parser) caseClause(typeSwitch bool) *CaseClause {
	c := &CaseClause{Case: p.tok.Pos}
	if p.got(Case) {
		if typeSwitch {
			c.List = []Expr{p.typ()}
			for p.got(Comma) {
				c.List = append(c.List, p.typ())
			}
		} else {
			c.List = p.exprList()
		}
	} else {
		p.expect(Default)
	}
	c.Colon = p.expect(Colon)
	c.Body = p.stmtList()
	return c
}

func (p *parser) selectStmt() *SelectStmt {
	s := &SelectStmt{Select: p.expect(Select)}
	body := &BlockStmt{Lbrace: p.expect(LBrace)}
	for p.tok.Kind == Case || p.tok.Kind == Default {
		c := &CommClause{Case: p.tok.Pos}
		if p.got(Case) {
			c.Comm = p.simpleStmt(false, false)
		} else {
			p.next()
		}
		c.Colon = p.expect(Colon)
		c.Body = p.stmtList()
		body.List = append(body.List, c)
	}
	body.Rbrace = p.expect(RBrace)
	s.Body = body
	return s
}

func (p *parser) forStmt() Stmt {
	pos := p.expect(For)
	isConst := false
	if p.tok.Kind == Const {
		if p.mode == GoMode {
			p.errorExpected("for clause")
		}
		isConst = true
		p.next()
	}
	var s1, s2, s3 Stmt
	isRange := false
	if p.tok.Kind != LBrace {
		outer := p.exprLev
		p.exprLev = -1
		if p.tok.Kind != Semicolon {
			s2 = p.simpleStmt(false, true)
			isRange = isRangeClause(s2)
		}
		if !isRange && p.tok.Kind == Semicolon {
			p.next()
			s1, s2 = s2, nil
			if p.tok.Kind != Semicolon {
				s2 = p.simpleStmt(false, false)
			}
			p.expect(Semicolon)
			if p.tok.Kind != LBrace {
				s3 = p.simpleStmt(false, false)
			}
		}
		p.exprLev = outer
	}
	body := p.blockWithContracts("invariant")

	if isRange {
		as := s2.(*AssignStmt)
		rng := as.Rhs[0].(*UnaryExpr)
		r := &RangeStmt{For: pos, Const: isConst, Range: rng.OpPos, X: rng.X, Body: body, Tok: as.Tok, TokPos: as.TokPos}
		switch len(as.Lhs) {
		case 0:
		case 1:
			r.Key = as.Lhs[0]
		case 2:
			r.Key, r.Value = as.Lhs[0], as.Lhs[1]
		default:
			p.errorf(as.Lhs[2].Pos(), "lex.token.go", "range clause permits at most two iteration variables")
		}
		return r
	}
	if isConst {
		p.errorf(pos, "lex.token.go", "for const requires a range clause")
	}
	return &ForStmt{For: pos, Init: s1, Cond: p.stmtToExpr(s2, "for"), Post: s3, Body: body}
}

func isRangeClause(s Stmt) bool {
	as, ok := s.(*AssignStmt)
	if !ok || len(as.Rhs) != 1 {
		return false
	}
	u, ok := as.Rhs[0].(*UnaryExpr)
	return ok && u.Op == Range
}
