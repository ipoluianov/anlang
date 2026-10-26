package desc

import (
	"fmt"
	"strconv"
	"strings"
)

// The semantics notation (spec/13-targets.md §13.5.4): statements and
// expressions over bit vectors.

// Expr is an expression of the semantics notation.
type Expr interface{ pos() int }

type (
	Num struct {
		P   int
		Val uint64
	}
	Ident struct {
		P    int
		Name string
	}
	Unary struct {
		P  int
		Op string
		X  Expr
	}
	Binary struct {
		P    int
		Op   string
		X, Y Expr
	}
	CallExpr struct {
		P    int
		Fun  string
		Args []Expr
	}
	// IndexExpr is x[hi] (Lo nil) or x[hi:lo].
	IndexExpr struct {
		P      int
		X      Expr
		Hi, Lo Expr
	}
	// MemExpr is memN[addr].
	MemExpr struct {
		P    int
		Size int
		Addr Expr
	}
)

func (e *Num) pos() int       { return e.P }
func (e *Ident) pos() int     { return e.P }
func (e *Unary) pos() int     { return e.P }
func (e *Binary) pos() int    { return e.P }
func (e *CallExpr) pos() int  { return e.P }
func (e *IndexExpr) pos() int { return e.P }
func (e *MemExpr) pos() int   { return e.P }

// Stmt is a statement of the semantics notation.
type Stmt interface{ spos() int }

type (
	LetStmt struct {
		P    int
		Name string
		X    Expr
	}
	AssignStmt struct {
		P   int
		LHS Expr // *Ident or *MemExpr
		X   Expr
	}
	IfStmt struct {
		P    int
		Cond Expr
		Then []Stmt
		Else []Stmt
	}
	WhileStmt struct {
		P    int
		Cond Expr
		Body []Stmt
	}
	BreakStmt  struct{ P int }
	ReturnStmt struct {
		P int
		X Expr // nil for return without a value
	}
	ExprStmt struct {
		P int
		X *CallExpr
	}
)

func (s *LetStmt) spos() int    { return s.P }
func (s *AssignStmt) spos() int { return s.P }
func (s *IfStmt) spos() int     { return s.P }
func (s *WhileStmt) spos() int  { return s.P }
func (s *BreakStmt) spos() int  { return s.P }
func (s *ReturnStmt) spos() int { return s.P }
func (s *ExprStmt) spos() int   { return s.P }

// FuncDecl is a function of the shared definitions.
type FuncDecl struct {
	P      int
	Name   string
	Params []string
	Body   []Stmt
}

// token kinds
const (
	tEOF = iota
	tName
	tNum
	tOp
)

type token struct {
	kind int
	text string
	pos  int
}

// SemError is an error in a text of the semantics notation.
type SemError struct {
	Line, Col int
	Msg       string
}

func (e *SemError) Error() string { return fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Msg) }

type semParser struct {
	src  string
	toks []token
	i    int
	err  *SemError
}

var ops3 = []string{">>>"}
var ops2 = []string{"==", "!=", "<=", ">=", "<<", ">>", "&&", "||"}

func newParser(src string) *semParser {
	p := &semParser{src: src}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z':
			j := i
			for j < len(src) && (src[j] == '_' || 'a' <= src[j] && src[j] <= 'z' || 'A' <= src[j] && src[j] <= 'Z' || '0' <= src[j] && src[j] <= '9') {
				j++
			}
			p.toks = append(p.toks, token{tName, src[i:j], i})
			i = j
		case '0' <= c && c <= '9':
			j := i
			for j < len(src) && (src[j] == 'x' || src[j] == 'X' || src[j] == 'b' || '0' <= src[j] && src[j] <= '9' || 'a' <= src[j] && src[j] <= 'f' || 'A' <= src[j] && src[j] <= 'F') {
				j++
			}
			p.toks = append(p.toks, token{tNum, src[i:j], i})
			i = j
		default:
			n := 1
			for _, op := range ops3 {
				if strings.HasPrefix(src[i:], op) {
					n = 3
				}
			}
			if n == 1 {
				for _, op := range ops2 {
					if strings.HasPrefix(src[i:], op) {
						n = 2
					}
				}
			}
			p.toks = append(p.toks, token{tOp, src[i : i+n], i})
			i += n
		}
	}
	p.toks = append(p.toks, token{tEOF, "", len(src)})
	return p
}

func (p *semParser) errorf(pos int, format string, args ...any) {
	if p.err != nil {
		return
	}
	line, col := 1, 1
	for _, c := range p.src[:min(pos, len(p.src))] {
		if c == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	p.err = &SemError{Line: line, Col: col, Msg: fmt.Sprintf(format, args...)}
}

func (p *semParser) tok() token { return p.toks[p.i] }
func (p *semParser) next() token {
	t := p.toks[p.i]
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}

func (p *semParser) is(text string) bool {
	t := p.tok()
	return (t.kind == tOp || t.kind == tName) && t.text == text
}

func (p *semParser) expect(text string) {
	if !p.is(text) {
		p.errorf(p.tok().pos, "expected %s, found %q", text, p.tok().text)
		return
	}
	p.next()
}

// parseFuncs parses the shared definitions.
func parseFuncs(src string) ([]*FuncDecl, *SemError) {
	p := newParser(src)
	var fns []*FuncDecl
	for p.tok().kind != tEOF && p.err == nil {
		pos := p.tok().pos
		p.expect("fn")
		name := p.next()
		if name.kind != tName {
			p.errorf(name.pos, "expected a function name")
			break
		}
		f := &FuncDecl{P: pos, Name: name.text}
		p.expect("(")
		for !p.is(")") && p.err == nil {
			n := p.next()
			if n.kind != tName {
				p.errorf(n.pos, "expected a parameter name")
				break
			}
			f.Params = append(f.Params, n.text)
			if !p.is(")") {
				p.expect(",")
			}
		}
		p.expect(")")
		f.Body = p.block()
		fns = append(fns, f)
	}
	return fns, p.err
}

// parseStmts parses the semantics of an instruction or a prefix.
func parseStmts(src string) ([]Stmt, *SemError) {
	p := newParser(src)
	var list []Stmt
	for p.tok().kind != tEOF && p.err == nil {
		list = append(list, p.stmt())
	}
	return list, p.err
}

// parseExpr parses an expression, such as an address.
func parseExpr(src string) (Expr, *SemError) {
	p := newParser(src)
	e := p.expr()
	if p.tok().kind != tEOF {
		p.errorf(p.tok().pos, "unexpected %q", p.tok().text)
	}
	return e, p.err
}

func (p *semParser) block() []Stmt {
	p.expect("{")
	var list []Stmt
	for !p.is("}") && p.tok().kind != tEOF && p.err == nil {
		list = append(list, p.stmt())
	}
	p.expect("}")
	return list
}

func (p *semParser) stmt() Stmt {
	t := p.tok()
	switch {
	case p.is("let"):
		p.next()
		n := p.next()
		if n.kind != tName {
			p.errorf(n.pos, "expected a name after let")
		}
		p.expect("=")
		x := p.expr()
		p.expect(";")
		return &LetStmt{P: t.pos, Name: n.text, X: x}
	case p.is("if"):
		p.next()
		s := &IfStmt{P: t.pos, Cond: p.expr(), Then: p.block()}
		if p.is("else") {
			p.next()
			if p.is("if") {
				s.Else = []Stmt{p.stmt()}
			} else {
				s.Else = p.block()
			}
		}
		return s
	case p.is("while"):
		p.next()
		return &WhileStmt{P: t.pos, Cond: p.expr(), Body: p.block()}
	case p.is("break"):
		p.next()
		p.expect(";")
		return &BreakStmt{P: t.pos}
	case p.is("return"):
		p.next()
		s := &ReturnStmt{P: t.pos}
		if !p.is(";") {
			s.X = p.expr()
		}
		p.expect(";")
		return s
	}
	x := p.expr()
	if p.is("=") {
		p.next()
		v := p.expr()
		p.expect(";")
		switch x.(type) {
		case *Ident, *MemExpr:
		default:
			p.errorf(t.pos, "cannot assign to this expression")
		}
		return &AssignStmt{P: t.pos, LHS: x, X: v}
	}
	p.expect(";")
	call, ok := x.(*CallExpr)
	if !ok {
		p.errorf(t.pos, "a statement must be an assignment or a call")
		return &ExprStmt{P: t.pos, X: &CallExpr{}}
	}
	return &ExprStmt{P: t.pos, X: call}
}

var precedence = map[string]int{
	"||": 1, "&&": 2,
	"==": 3, "!=": 3, "<": 3, "<=": 3, ">": 3, ">=": 3,
	"|": 4, "^": 4, "+": 4, "-": 4,
	"&": 5, "*": 5, "<<": 5, ">>": 5, ">>>": 5,
}

func (p *semParser) expr() Expr { return p.binary(1) }

func (p *semParser) binary(prec int) Expr {
	x := p.unary()
	for {
		t := p.tok()
		pr, ok := precedence[t.text]
		if t.kind != tOp || !ok || pr < prec {
			return x
		}
		p.next()
		y := p.binary(pr + 1)
		x = &Binary{P: t.pos, Op: t.text, X: x, Y: y}
	}
}

func (p *semParser) unary() Expr {
	t := p.tok()
	if t.kind == tOp && (t.text == "-" || t.text == "~" || t.text == "!") {
		p.next()
		return &Unary{P: t.pos, Op: t.text, X: p.unary()}
	}
	return p.postfix(p.primary())
}

func (p *semParser) postfix(x Expr) Expr {
	for p.is("[") {
		t := p.next()
		hi := p.expr()
		var lo Expr
		if p.is(":") {
			p.next()
			lo = p.expr()
		}
		p.expect("]")
		x = &IndexExpr{P: t.pos, X: x, Hi: hi, Lo: lo}
	}
	return x
}

func (p *semParser) primary() Expr {
	t := p.next()
	switch t.kind {
	case tNum:
		v, err := strconv.ParseUint(strings.ReplaceAll(t.text, "_", ""), 0, 64)
		if err != nil {
			p.errorf(t.pos, "invalid number %q", t.text)
		}
		return &Num{P: t.pos, Val: v}
	case tName:
		if size, ok := map[string]int{"mem8": 8, "mem16": 16, "mem32": 32, "mem64": 64}[t.text]; ok && p.is("[") {
			p.next()
			addr := p.expr()
			p.expect("]")
			return &MemExpr{P: t.pos, Size: size, Addr: addr}
		}
		if p.is("(") {
			p.next()
			c := &CallExpr{P: t.pos, Fun: t.text}
			for !p.is(")") && p.err == nil && p.tok().kind != tEOF {
				c.Args = append(c.Args, p.expr())
				if !p.is(")") {
					p.expect(",")
				}
			}
			p.expect(")")
			return c
		}
		return &Ident{P: t.pos, Name: t.text}
	case tOp:
		if t.text == "(" {
			x := p.expr()
			p.expect(")")
			return x
		}
	}
	p.errorf(t.pos, "unexpected %q", t.text)
	return &Num{P: t.pos}
}
