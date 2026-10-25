package syntax

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// Error is a diagnostic tied to a position and a spec rule identifier.
type Error struct {
	Pos  Pos
	Rule string
	Msg  string
}

func (e *Error) Error() string {
	if e.Rule == "" {
		return fmt.Sprintf("%s: %s", e.Pos, e.Msg)
	}
	return fmt.Sprintf("%s: %s [%s]", e.Pos, e.Msg, e.Rule)
}

// Mode selects the language a Scanner or parser accepts.
type Mode int

const (
	// AnlangMode accepts Anlang source.
	AnlangMode Mode = iota
	// GoMode accepts Go source: enum, inout and profile are identifiers
	// and contextual words have no special meaning. It is used to test
	// the parser against the Go sources and by anlang port.
	GoMode
)

// Scanner splits a source file into tokens with automatic semicolon
// insertion, as in Go (lex.token.go).
type Scanner struct {
	mode Mode
	file string
	src  []byte
	off  int
	line int
	col  int

	// insertSemi is set when a newline or EOF must produce a semicolon.
	insertSemi bool

	// pending is a comment token to return before scanning further. It is
	// set when a /*-comment containing a newline ends a line that needs a
	// semicolon: as in Go, the semicolon comes first.
	pending *Token

	Errors []*Error
}

// NewScanner returns a scanner for src. The file name is used in positions.
func NewScanner(file string, src []byte, mode Mode) *Scanner {
	s := &Scanner{mode: mode, file: file, src: src, line: 1, col: 1}
	if !utf8.Valid(src) {
		s.errorf(Pos{file, 0, 1, 1}, "lex.source.utf8", "source is not valid UTF-8")
	}
	return s
}

func (s *Scanner) errorf(pos Pos, rule, format string, args ...any) {
	s.Errors = append(s.Errors, &Error{Pos: pos, Rule: rule, Msg: fmt.Sprintf(format, args...)})
}

func (s *Scanner) pos() Pos { return Pos{s.file, s.off, s.line, s.col} }

func (s *Scanner) peek() rune {
	if s.off >= len(s.src) {
		return -1
	}
	r, _ := utf8.DecodeRune(s.src[s.off:])
	return r
}

func (s *Scanner) peekAt(n int) byte {
	if s.off+n >= len(s.src) {
		return 0
	}
	return s.src[s.off+n]
}

func (s *Scanner) next() rune {
	if s.off >= len(s.src) {
		return -1
	}
	r, size := utf8.DecodeRune(s.src[s.off:])
	s.off += size
	if r == '\n' {
		s.line++
		s.col = 1
	} else {
		s.col += size
	}
	return r
}

// Scan returns the next token. Comments are returned as Comment tokens;
// a comment that contains a newline, or a line comment, ends a line for
// the purpose of semicolon insertion.
func (s *Scanner) Scan() Token {
	if t := s.pending; t != nil {
		s.pending = nil
		return *t
	}
	for {
		r := s.peek()
		if r == '\n' && s.insertSemi {
			pos := s.pos()
			s.next()
			s.insertSemi = false
			return Token{Kind: Semicolon, Pos: pos, Lit: "\n"}
		}
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			s.next()
			continue
		}
		break
	}

	pos := s.pos()
	r := s.peek()
	if r == -1 {
		if s.insertSemi {
			s.insertSemi = false
			return Token{Kind: Semicolon, Pos: pos, Lit: "\n"}
		}
		return Token{Kind: EOF, Pos: pos}
	}

	switch {
	case isLetter(r):
		start := s.off
		for isLetter(s.peek()) || isDigit(s.peek()) {
			s.next()
		}
		lit := string(s.src[start:s.off])
		kind := Lookup(lit)
		if s.mode == GoMode {
			kind = lookupGo(lit)
		}
		s.insertSemi = kind == Ident || kind == Break || kind == Continue ||
			kind == Fallthrough || kind == Return
		return Token{Kind: kind, Pos: pos, Lit: lit}
	case isDecimal(r) || (r == '.' && isDecimal(rune(s.peekAt(1)))):
		tok := s.scanNumber(pos)
		s.insertSemi = true
		return tok
	case r == '"':
		tok := s.scanString(pos)
		s.insertSemi = true
		return tok
	case r == '`':
		tok := s.scanRawString(pos)
		s.insertSemi = true
		return tok
	case r == '\'':
		tok := s.scanRune(pos)
		s.insertSemi = true
		return tok
	case r == '/' && (s.peekAt(1) == '/' || s.peekAt(1) == '*'):
		return s.scanComment(pos)
	}

	return s.scanOperator(pos)
}

func (s *Scanner) scanComment(pos Pos) Token {
	start := s.off
	s.next() // '/'
	if s.next() == '/' {
		for r := s.peek(); r != '\n' && r != -1; r = s.peek() {
			s.next()
		}
		// The newline after a line comment stays in the input, so Scan
		// turns it into a semicolon when one is due.
		return Token{Kind: CommentTok, Pos: pos, Lit: string(s.src[start:s.off])}
	}
	hasNewline := false
	for {
		r := s.next()
		if r == -1 {
			s.errorf(pos, "lex.token.go", "comment not terminated")
			break
		}
		if r == '\n' {
			hasNewline = true
		}
		if r == '*' && s.peek() == '/' {
			s.next()
			break
		}
	}
	tok := Token{Kind: CommentTok, Pos: pos, Lit: string(s.src[start:s.off])}
	if hasNewline && s.insertSemi {
		s.insertSemi = false
		s.pending = &tok
		return Token{Kind: Semicolon, Pos: pos, Lit: "\n"}
	}
	return tok
}

func (s *Scanner) scanNumber(pos Pos) Token {
	start := s.off
	kind := Int
	base := 10
	if s.peek() == '0' {
		switch s.peekAt(1) {
		case 'x', 'X':
			base = 16
			s.next()
			s.next()
		case 'b', 'B':
			base = 2
			s.next()
			s.next()
		case 'o', 'O':
			base = 8
			s.next()
			s.next()
		}
	}
	s.digits(base)
	if s.peek() == '.' && base != 2 && base != 8 {
		kind = Float
		s.next()
		s.digits(base)
	}
	if e := s.peek(); (base == 10 && (e == 'e' || e == 'E')) || (base == 16 && (e == 'p' || e == 'P')) {
		kind = Float
		s.next()
		if c := s.peek(); c == '+' || c == '-' {
			s.next()
		}
		if !isDecimal(s.peek()) {
			s.errorf(pos, "lex.literal.go", "exponent has no digits")
		}
		s.digits(10)
	}
	if s.peek() == 'i' {
		s.next()
		if s.mode == AnlangMode {
			s.errorf(pos, "lex.literal.no-imaginary", "imaginary literals are removed in Anlang")
		}
		kind = Imag
	}
	lit := string(s.src[start:s.off])
	if kind == Float && base == 16 && !hasHexExponent(lit) {
		s.errorf(pos, "lex.literal.go", "hexadecimal mantissa requires a 'p' exponent")
	}
	return Token{Kind: kind, Pos: pos, Lit: lit}
}

func hasHexExponent(lit string) bool {
	for i := 0; i < len(lit); i++ {
		if lit[i] == 'p' || lit[i] == 'P' {
			return true
		}
	}
	return false
}

// digits consumes digits of the given base and '_' separators. Digits
// that are invalid for the base are consumed and reported.
func (s *Scanner) digits(base int) {
	for {
		r := s.peek()
		if r == '_' {
			s.next()
			continue
		}
		d := digitVal(r)
		if d >= 16 || (base != 16 && !isDecimal(r)) {
			return
		}
		if d >= base {
			s.errorf(s.pos(), "lex.literal.go", "invalid digit %q in base-%d literal", r, base)
		}
		s.next()
	}
}

func (s *Scanner) scanString(pos Pos) Token {
	start := s.off
	s.next() // opening quote
	for {
		r := s.peek()
		if r == '"' {
			s.next()
			break
		}
		if r == '\n' || r == -1 {
			s.errorf(pos, "lex.literal.go", "string literal not terminated")
			break
		}
		if r == '\\' {
			s.escape('"')
			continue
		}
		s.next()
	}
	return Token{Kind: String, Pos: pos, Lit: string(s.src[start:s.off])}
}

func (s *Scanner) scanRawString(pos Pos) Token {
	start := s.off
	s.next()
	for {
		r := s.next()
		if r == '`' {
			break
		}
		if r == -1 {
			s.errorf(pos, "lex.literal.go", "raw string literal not terminated")
			break
		}
	}
	return Token{Kind: String, Pos: pos, Lit: string(s.src[start:s.off])}
}

func (s *Scanner) scanRune(pos Pos) Token {
	start := s.off
	s.next()
	n := 0
	for {
		r := s.peek()
		if r == '\'' {
			s.next()
			break
		}
		if r == '\n' || r == -1 {
			s.errorf(pos, "lex.literal.go", "rune literal not terminated")
			break
		}
		if r == '\\' {
			s.escape('\'')
		} else {
			s.next()
		}
		n++
	}
	if n != 1 {
		s.errorf(pos, "lex.literal.go", "rune literal must contain exactly one character")
	}
	return Token{Kind: Rune, Pos: pos, Lit: string(s.src[start:s.off])}
}

// escape consumes an escape sequence starting at '\'.
func (s *Scanner) escape(quote rune) {
	pos := s.pos()
	s.next() // '\'
	r := s.next()
	var n, base int
	switch r {
	case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\', quote:
		return
	case '0', '1', '2', '3', '4', '5', '6', '7':
		n, base = 2, 8
	case 'x':
		n, base = 2, 16
	case 'u':
		n, base = 4, 16
	case 'U':
		n, base = 8, 16
	default:
		s.errorf(pos, "lex.literal.go", "unknown escape sequence")
		return
	}
	for ; n > 0; n-- {
		if digitVal(s.peek()) >= base {
			s.errorf(pos, "lex.literal.go", "invalid escape sequence")
			return
		}
		s.next()
	}
}

func (s *Scanner) scanOperator(pos Pos) Token {
	r := s.next()
	tok := func(k Kind) Token {
		s.insertSemi = k == RParen || k == RBrack || k == RBrace || k == Inc || k == Dec
		return Token{Kind: k, Pos: pos}
	}
	// choose returns a when the next byte is c (consuming it), else b.
	choose := func(c byte, a, b Kind) Kind {
		if s.peekAt(0) == c {
			s.next()
			return a
		}
		return b
	}
	switch r {
	case '(':
		return tok(LParen)
	case ')':
		return tok(RParen)
	case '[':
		return tok(LBrack)
	case ']':
		return tok(RBrack)
	case '{':
		return tok(LBrace)
	case '}':
		return tok(RBrace)
	case ',':
		return tok(Comma)
	case ';':
		t := tok(Semicolon)
		t.Lit = ";"
		return t
	case '~':
		return tok(Tilde)
	case '.':
		if s.peekAt(0) == '.' && s.peekAt(1) == '.' {
			s.next()
			s.next()
			return tok(DotDotDot)
		}
		return tok(Period)
	case ':':
		return tok(choose('=', Define, Colon))
	case '+':
		if s.peekAt(0) == '+' {
			s.next()
			return tok(Inc)
		}
		return tok(choose('=', AddAssign, Add))
	case '-':
		if s.peekAt(0) == '-' {
			s.next()
			return tok(Dec)
		}
		return tok(choose('=', SubAssign, Sub))
	case '*':
		return tok(choose('=', MulAssign, Mul))
	case '/':
		return tok(choose('=', QuoAssign, Quo))
	case '%':
		return tok(choose('=', RemAssign, Rem))
	case '^':
		return tok(choose('=', XorAssign, Xor))
	case '=':
		return tok(choose('=', Eql, Assign))
	case '!':
		return tok(choose('=', Neq, Not))
	case '|':
		if s.peekAt(0) == '|' {
			s.next()
			return tok(LOr)
		}
		return tok(choose('=', OrAssign, Or))
	case '&':
		switch s.peekAt(0) {
		case '&':
			s.next()
			return tok(LAnd)
		case '^':
			s.next()
			return tok(choose('=', AndNotAssign, AndNot))
		}
		return tok(choose('=', AndAssign, And))
	case '<':
		switch s.peekAt(0) {
		case '-':
			s.next()
			return tok(Arrow)
		case '<':
			s.next()
			return tok(choose('=', ShlAssign, Shl))
		}
		return tok(choose('=', Leq, Lss))
	case '>':
		if s.peekAt(0) == '>' {
			s.next()
			return tok(choose('=', ShrAssign, Shr))
		}
		return tok(choose('=', Geq, Gtr))
	}
	s.errorf(pos, "lex.token.go", "invalid character %q", r)
	s.insertSemi = false
	return Token{Kind: Illegal, Pos: pos, Lit: string(r)}
}

func isLetter(r rune) bool {
	return r == '_' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' ||
		r >= utf8.RuneSelf && unicode.IsLetter(r)
}

func isDigit(r rune) bool {
	return isDecimal(r) || r >= utf8.RuneSelf && unicode.IsDigit(r)
}

func isDecimal(r rune) bool { return '0' <= r && r <= '9' }

func digitVal(r rune) int {
	switch {
	case '0' <= r && r <= '9':
		return int(r - '0')
	case 'a' <= r && r <= 'f':
		return int(r - 'a' + 10)
	case 'A' <= r && r <= 'F':
		return int(r - 'A' + 10)
	}
	return 16
}
