// Package syntax implements the lexer and parser of Kern source files
// (spec/01-lexical.md).
package syntax

import "fmt"

// Kind is the kind of a token.
type Kind int

const (
	Illegal Kind = iota
	EOF
	Comment

	// Literals and identifiers.
	Ident
	Int
	Float
	Rune
	String

	// Operators and punctuation (lex.operator.go).
	Add    // +
	Sub    // -
	Mul    // *
	Quo    // /
	Rem    // %
	And    // &
	Or     // |
	Xor    // ^
	Shl    // <<
	Shr    // >>
	AndNot // &^
	AddAssign
	SubAssign
	MulAssign
	QuoAssign
	RemAssign
	AndAssign
	OrAssign
	XorAssign
	ShlAssign
	ShrAssign
	AndNotAssign
	LAnd     // &&
	LOr      // ||
	Arrow    // <-
	Inc      // ++
	Dec      // --
	Eql      // ==
	Lss      // <
	Gtr      // >
	Assign   // =
	Not      // !
	Neq      // !=
	Leq      // <=
	Geq      // >=
	Define   // :=
	Ellipsis // ...
	LParen   // (
	LBrack   // [
	LBrace   // {
	Comma    // ,
	Period   // .
	RParen   // )
	RBrack   // ]
	RBrace   // }
	Semicolon
	Colon // :
	Tilde // ~

	// Keywords (lex.keyword.set).
	keywordBegin
	Break
	Case
	Chan
	Const
	Continue
	Default
	Defer
	Else
	Fallthrough
	For
	Func
	Go
	Goto
	If
	Import
	Interface
	Map
	Package
	Range
	Return
	Select
	Struct
	Switch
	Type
	Var
	Enum
	Inout
	Profile
	keywordEnd
)

var kindNames = [...]string{
	Illegal: "ILLEGAL",
	EOF:     "EOF",
	Comment: "COMMENT",
	Ident:   "identifier",
	Int:     "integer literal",
	Float:   "floating-point literal",
	Rune:    "rune literal",
	String:  "string literal",

	Add: "+", Sub: "-", Mul: "*", Quo: "/", Rem: "%",
	And: "&", Or: "|", Xor: "^", Shl: "<<", Shr: ">>", AndNot: "&^",
	AddAssign: "+=", SubAssign: "-=", MulAssign: "*=", QuoAssign: "/=", RemAssign: "%=",
	AndAssign: "&=", OrAssign: "|=", XorAssign: "^=", ShlAssign: "<<=", ShrAssign: ">>=",
	AndNotAssign: "&^=",
	LAnd:         "&&", LOr: "||", Arrow: "<-", Inc: "++", Dec: "--",
	Eql: "==", Lss: "<", Gtr: ">", Assign: "=", Not: "!",
	Neq: "!=", Leq: "<=", Geq: ">=", Define: ":=", Ellipsis: "...",
	LParen: "(", LBrack: "[", LBrace: "{", Comma: ",", Period: ".",
	RParen: ")", RBrack: "]", RBrace: "}", Semicolon: ";", Colon: ":", Tilde: "~",

	Break: "break", Case: "case", Chan: "chan", Const: "const", Continue: "continue",
	Default: "default", Defer: "defer", Else: "else", Fallthrough: "fallthrough",
	For: "for", Func: "func", Go: "go", Goto: "goto", If: "if", Import: "import",
	Interface: "interface", Map: "map", Package: "package", Range: "range",
	Return: "return", Select: "select", Struct: "struct", Switch: "switch",
	Type: "type", Var: "var",
	Enum: "enum", Inout: "inout", Profile: "profile",
}

func (k Kind) String() string {
	if int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// IsKeyword reports whether k is a reserved keyword.
func (k Kind) IsKeyword() bool { return keywordBegin < k && k < keywordEnd }

var keywords = func() map[string]Kind {
	m := make(map[string]Kind)
	for k := keywordBegin + 1; k < keywordEnd; k++ {
		m[kindNames[k]] = k
	}
	return m
}()

// Lookup returns the keyword kind of an identifier, or Ident.
func Lookup(name string) Kind {
	if k, ok := keywords[name]; ok {
		return k
	}
	return Ident
}

// Pos is a position in a source file. Line and Col are 1-based; Col
// counts bytes.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string {
	if p.File == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Token is a lexical token. Lit holds the source text for identifiers,
// literals and comments, and "\n" or ";" for semicolons.
type Token struct {
	Kind Kind
	Pos  Pos
	Lit  string
}
