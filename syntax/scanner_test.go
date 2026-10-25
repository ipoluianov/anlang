package syntax

import (
	"strings"
	"testing"
)

// scan returns the kinds and literals of all tokens of src, without
// comments.
func scan(t *testing.T, src string) ([]Token, []*Error) {
	t.Helper()
	s := NewScanner("test.kn", []byte(src), KernMode)
	var toks []Token
	for {
		tok := s.Scan()
		if tok.Kind == CommentTok {
			continue
		}
		toks = append(toks, tok)
		if tok.Kind == EOF {
			return toks, s.Errors
		}
	}
}

func kinds(toks []Token) string {
	var parts []string
	for _, t := range toks {
		parts = append(parts, t.Kind.String())
	}
	return strings.Join(parts, " ")
}

func TestScanKeywords(t *testing.T) {
	toks, errs := scan(t, "enum inout profile func package requires ensures")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	// requires and ensures are contextual words, so they scan as
	// identifiers (lex.ident.contextual).
	want := "enum inout profile func package identifier identifier ; EOF"
	if got := kinds(toks); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestScanSemicolonInsertion(t *testing.T) {
	src := "package main\n\nfunc main() {\n\tf(1)\n\treturn\n}\n"
	toks, errs := scan(t, src)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	want := "package identifier ; func identifier ( ) { identifier ( integer literal ) ; return ; } ; EOF"
	if got := kinds(toks); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestScanSemicolonAfterLineComment(t *testing.T) {
	toks, _ := scan(t, "x // comment\ny")
	want := "identifier ; identifier ; EOF"
	if got := kinds(toks); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestScanOperators(t *testing.T) {
	src := "+ += ++ - -= -- * / % & &^ &^= && | || ^ << <<= >> >>= <- == != <= >= := ... ~"
	toks, errs := scan(t, src)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var got []string
	for _, tok := range toks[:len(toks)-1] {
		got = append(got, tok.Kind.String())
	}
	if strings.Join(got, " ") != src {
		t.Errorf("got  %s\nwant %s", strings.Join(got, " "), src)
	}
}

func TestScanLiterals(t *testing.T) {
	tests := []struct {
		src  string
		kind Kind
	}{
		{"42", Int},
		{"0x0800_0000", Int},
		{"0b1010", Int},
		{"0o17", Int},
		{"1.5", Float},
		{".5", Float},
		{"1e9", Float},
		{"0x1.8p3", Float},
		{"'a'", Rune},
		{`'\n'`, Rune},
		{`'é'`, Rune},
		{`"Привет из Kern!\r\n"`, String},
		{"`raw\nstring`", String},
	}
	for _, tt := range tests {
		toks, errs := scan(t, tt.src)
		if len(errs) > 0 {
			t.Errorf("%s: %v", tt.src, errs)
			continue
		}
		if toks[0].Kind != tt.kind || toks[0].Lit != tt.src {
			t.Errorf("%s: got %s %q, want %s", tt.src, toks[0].Kind, toks[0].Lit, tt.kind)
		}
	}
}

func TestScanErrors(t *testing.T) {
	tests := []struct {
		src  string
		rule string
	}{
		{"2i", "lex.literal.no-imaginary"},
		{`"unterminated`, "lex.literal.go"},
		{"0b102", "lex.literal.go"},
		{`'ab'`, "lex.literal.go"},
		{"@", "lex.token.go"},
		{"\xff", "lex.source.utf8"},
	}
	for _, tt := range tests {
		_, errs := scan(t, tt.src)
		if len(errs) == 0 {
			t.Errorf("%q: no error, want %s", tt.src, tt.rule)
			continue
		}
		if errs[0].Rule != tt.rule {
			t.Errorf("%q: got rule %s (%s), want %s", tt.src, errs[0].Rule, errs[0].Msg, tt.rule)
		}
	}
}

func TestScanPositions(t *testing.T) {
	toks, _ := scan(t, "a\n  bé c")
	want := []Pos{{"test.kn", 0, 1, 1}, {"test.kn", 1, 1, 2}, {"test.kn", 4, 2, 3}, {"test.kn", 8, 2, 7}}
	for i, p := range want {
		if toks[i].Pos != p {
			t.Errorf("token %d: got %v, want %v", i, toks[i].Pos, p)
		}
	}
}
