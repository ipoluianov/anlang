package syntax

import "fmt"

// unsupported is the message for valid Kern that the parser does not
// accept yet. It names the roadmap step that adds the full grammar.
const unsupported = "not supported yet by this compiler (spec/24-roadmap.md, step 1)"

// Parse parses one source file. It returns the file and all lexical and
// syntax errors; the file is nil only if the package clause is missing.
func Parse(file string, src []byte) (*File, []*Error) {
	p := &parser{s: NewScanner(file, src)}
	p.next()
	f := p.parseFile()
	errs := append(p.s.Errors, p.errors...)
	return f, errs
}

type parser struct {
	s      *Scanner
	tok    Token
	errors []*Error
}

// next advances to the next token that is not a comment.
func (p *parser) next() {
	for {
		p.tok = p.s.Scan()
		if p.tok.Kind != Comment {
			return
		}
	}
}

func (p *parser) errorf(pos Pos, rule, format string, args ...any) {
	// One error per position keeps cascades short.
	if n := len(p.errors); n > 0 && p.errors[n-1].Pos == pos {
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

func (p *parser) expect(k Kind) Pos {
	pos := p.tok.Pos
	if p.tok.Kind != k {
		p.errorf(pos, "lex.token.go", "expected '%s', found %s", k, p.describe(p.tok))
		return pos
	}
	p.next()
	return pos
}

// expectSemi accepts a semicolon, or nothing before ')' and '}' as in Go.
func (p *parser) expectSemi() {
	switch p.tok.Kind {
	case Semicolon:
		p.next()
	case RParen, RBrace:
	default:
		p.errorf(p.tok.Pos, "lex.token.go", "expected ';' or newline, found %s", p.describe(p.tok))
		p.skipTo(Semicolon)
		if p.tok.Kind == Semicolon {
			p.next()
		}
	}
}

// skipTo advances to the next token of kind k, a closing brace or EOF.
func (p *parser) skipTo(k Kind) {
	for p.tok.Kind != k && p.tok.Kind != RBrace && p.tok.Kind != EOF {
		p.next()
	}
}

func (p *parser) ident() *Name {
	pos := p.tok.Pos
	name := "_"
	if p.tok.Kind == Ident {
		name = p.tok.Lit
		p.next()
	} else {
		p.errorf(pos, "lex.ident.go", "expected identifier, found %s", p.describe(p.tok))
	}
	return &Name{NamePos: pos, Name: name}
}

func (p *parser) parseFile() *File {
	if p.tok.Kind != Package {
		p.errorf(p.tok.Pos, "lex.token.go", "expected 'package', found %s", p.describe(p.tok))
		return nil
	}
	f := &File{Package: p.tok.Pos}
	p.next()
	f.Name = p.ident()
	p.expectSemi()

	if p.tok.Kind == Profile {
		p.next()
		f.Profiles = append(f.Profiles, p.ident())
		for p.tok.Kind == Comma {
			p.next()
			f.Profiles = append(f.Profiles, p.ident())
		}
		p.expectSemi()
	}

	for p.tok.Kind == Import {
		p.next()
		if p.tok.Kind == LParen {
			p.next()
			for p.tok.Kind != RParen && p.tok.Kind != EOF {
				f.Imports = append(f.Imports, p.importSpec())
				p.expectSemi()
			}
			p.expect(RParen)
		} else {
			f.Imports = append(f.Imports, p.importSpec())
		}
		p.expectSemi()
	}

	for p.tok.Kind != EOF {
		if d := p.topLevelDecl(); d != nil {
			f.Decls = append(f.Decls, d)
		}
		if p.tok.Kind != EOF {
			p.expectSemi()
		}
	}
	return f
}

func (p *parser) importSpec() *ImportSpec {
	s := &ImportSpec{}
	switch p.tok.Kind {
	case Ident:
		s.Name = p.ident()
	case Period:
		p.errorf(p.tok.Pos, "decl.import.no-blank-dot", "dot imports are removed in Kern")
		p.next()
	}
	if p.tok.Kind != String {
		p.errorf(p.tok.Pos, "decl.import.paths", "expected import path, found %s", p.describe(p.tok))
		s.Path = &BasicLit{ValuePos: p.tok.Pos, Kind: String, Value: `""`}
		p.skipTo(Semicolon)
		return s
	}
	s.Path = &BasicLit{ValuePos: p.tok.Pos, Kind: String, Value: p.tok.Lit}
	p.next()
	return s
}

func (p *parser) topLevelDecl() Decl {
	switch p.tok.Kind {
	case Func:
		return p.funcDecl()
	case Import:
		p.errorf(p.tok.Pos, "lex.token.go", "imports must appear before other declarations")
	case Profile:
		p.errorf(p.tok.Pos, "lex.token.go", "the profile clause must follow the package clause")
	default:
		p.errorf(p.tok.Pos, "", "%s: %s", p.describe(p.tok), unsupported)
	}
	p.skipDecl()
	return nil
}

// skipDecl skips to the end of the current top-level declaration,
// including any braced or parenthesized body.
func (p *parser) skipDecl() {
	depth := 0
	for p.tok.Kind != EOF {
		switch p.tok.Kind {
		case LBrace, LParen, LBrack:
			depth++
		case RBrace, RParen, RBrack:
			depth--
		case Semicolon:
			if depth <= 0 {
				return
			}
		}
		p.next()
	}
}

func (p *parser) funcDecl() *FuncDecl {
	d := &FuncDecl{Func: p.tok.Pos}
	p.next()
	if p.tok.Kind == LParen {
		p.errorf(p.tok.Pos, "", "methods: %s", unsupported)
		p.skipDecl()
		return nil
	}
	d.Name = p.ident()
	if p.tok.Kind == LBrack {
		p.errorf(p.tok.Pos, "", "type parameters: %s", unsupported)
		p.skipDecl()
		return nil
	}
	d.Params = p.params()
	switch p.tok.Kind {
	case LParen:
		d.Results = p.params()
	case LBrace, Semicolon, Assign:
	default:
		d.Results = []*Field{{Type: p.typeExpr()}}
	}
	if p.tok.Kind == Assign {
		p.errorf(p.tok.Pos, "", "external functions: %s", unsupported)
		p.skipDecl()
		return nil
	}
	if p.tok.Kind != LBrace {
		p.errorf(p.tok.Pos, "decl.func.body", "missing function body")
		return d
	}
	d.Body = p.block()
	return d
}

// params parses a parenthesized parameter list. For now only the empty
// list and lists of plain types are accepted.
func (p *parser) params() []*Field {
	p.expect(LParen)
	var fields []*Field
	for p.tok.Kind != RParen && p.tok.Kind != EOF {
		fields = append(fields, &Field{Type: p.typeExpr()})
		if p.tok.Kind != Comma {
			break
		}
		p.next()
	}
	p.expect(RParen)
	return fields
}

// typeExpr parses a type name or qualified type name.
func (p *parser) typeExpr() Expr {
	if p.tok.Kind != Ident {
		p.errorf(p.tok.Pos, "", "type %s: %s", p.describe(p.tok), unsupported)
		p.next()
		return &Name{NamePos: p.tok.Pos, Name: "_"}
	}
	var x Expr = p.ident()
	if p.tok.Kind == Period {
		p.next()
		x = &SelectorExpr{X: x, Sel: p.ident()}
	}
	return x
}

func (p *parser) block() *BlockStmt {
	b := &BlockStmt{Lbrace: p.expect(LBrace)}
	for p.tok.Kind != RBrace && p.tok.Kind != EOF {
		if s := p.stmt(); s != nil {
			b.List = append(b.List, s)
		}
		p.expectSemi()
	}
	b.Rbrace = p.expect(RBrace)
	return b
}

func (p *parser) stmt() Stmt {
	switch p.tok.Kind {
	case Semicolon:
		return nil // empty statement
	case Ident, String, Int, Float, Rune, LParen:
		x := p.expr()
		if p.tok.Kind != Semicolon && p.tok.Kind != RBrace {
			p.errorf(p.tok.Pos, "", "statement %s: %s", p.describe(p.tok), unsupported)
			p.skipTo(Semicolon)
		}
		return &ExprStmt{X: x}
	}
	p.errorf(p.tok.Pos, "", "statement %s: %s", p.describe(p.tok), unsupported)
	p.skipTo(Semicolon)
	return nil
}

// expr parses an expression. For now: operands, selectors and calls.
func (p *parser) expr() Expr {
	x := p.operand()
	for {
		switch p.tok.Kind {
		case Period:
			p.next()
			x = &SelectorExpr{X: x, Sel: p.ident()}
		case LParen:
			x = p.call(x)
		default:
			if isBinaryOp(p.tok.Kind) {
				p.errorf(p.tok.Pos, "", "operator %s: %s", p.describe(p.tok), unsupported)
				p.skipTo(Semicolon)
			}
			return x
		}
	}
}

func (p *parser) operand() Expr {
	switch p.tok.Kind {
	case Ident:
		return p.ident()
	case Int, Float, Rune, String:
		x := &BasicLit{ValuePos: p.tok.Pos, Kind: p.tok.Kind, Value: p.tok.Lit}
		p.next()
		return x
	case LParen:
		pos := p.tok.Pos
		p.next()
		x := p.expr()
		p.expect(RParen)
		return &ParenExpr{Lparen: pos, X: x}
	}
	p.errorf(p.tok.Pos, "", "expression %s: %s", p.describe(p.tok), unsupported)
	pos := p.tok.Pos
	p.next()
	return &BadExpr{From: pos}
}

func (p *parser) call(fun Expr) *CallExpr {
	c := &CallExpr{Fun: fun, Lparen: p.expect(LParen)}
	for p.tok.Kind != RParen && p.tok.Kind != EOF {
		c.Args = append(c.Args, p.expr())
		if p.tok.Kind == Ellipsis {
			c.Ellipsis = true
			p.next()
		}
		if p.tok.Kind != Comma {
			break
		}
		p.next()
	}
	p.expect(RParen)
	return c
}

func isBinaryOp(k Kind) bool {
	switch k {
	case Add, Sub, Mul, Quo, Rem, And, Or, Xor, Shl, Shr, AndNot,
		LAnd, LOr, Eql, Neq, Lss, Leq, Gtr, Geq:
		return true
	}
	return false
}

// BadExpr marks an expression that failed to parse.
type BadExpr struct {
	From Pos
}

func (x *BadExpr) Pos() Pos { return x.From }
func (*BadExpr) exprNode()  {}
