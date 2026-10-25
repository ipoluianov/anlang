package types

import (
	"go/constant"
	"strings"
	"testing"

	"an/syntax"
)

func check(t *testing.T, srcs ...string) (*Info, []*syntax.Error) {
	t.Helper()
	var files []*syntax.File
	for i, src := range srcs {
		f, errs := syntax.Parse(string(rune('a'+i))+".an", []byte(src))
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
	}
	return Check(&Config{Module: "m"}, files)
}

// TestCheckErrors checks diagnostics without rule identifiers: constructs
// this compiler does not support yet. Errors with rule identifiers are
// tested by the conformance suite (conformance/testdata).
func TestCheckErrors(t *testing.T) {
	tests := []struct {
		src string
		msg string
	}{
		{"package main\nfunc main() { _ = func() {} }", "function literals: not supported yet by this compiler (spec/24-roadmap.md, step 2)"},
		{"package main\ntype T struct{}\nfunc (t inout T) M() {}\nfunc main() {}", "inout receivers: not supported yet"},
		{"package main\nfunc F[T any]() {}\nfunc main() {}", "generic functions: not supported yet"},
		{"package main\ntype E enum { A }\nfunc main() {}", "enum types: not supported yet by this compiler (spec/24-roadmap.md, step 3)"},
		{"package main\nfunc f(x inout int) {}\nfunc main() {}", "inout parameters: not supported yet by this compiler (spec/24-roadmap.md, step 4)"},
		{"package main\nimport \"m/lib\"\nfunc main() { lib.F() }", "package \"m/lib\": not supported yet"},
		{"package main\nfunc main() { var o Option[int]; _ = o }", "Option: not supported yet"},
		{"package main\nfunc main() { for const i := range 3 { _ = i } }", "for const: not supported yet"},
	}
	for _, tt := range tests {
		_, errs := check(t, tt.src)
		if len(errs) == 0 {
			t.Errorf("%q: no error", tt.src)
			continue
		}
		e := errs[0]
		if e.Rule != "" || !strings.Contains(e.Msg, tt.msg) {
			t.Errorf("%q: got [%s] %s, want a message containing %q", tt.src, e.Rule, e.Msg, tt.msg)
		}
	}
}

// TestConstants checks constant values and the types of untyped
// expressions recorded in Info.
func TestConstants(t *testing.T) {
	info, errs := check(t, `package main

const (
	a = iota * 10
	b
	c i8 = 1 << 6
	d    = 'x'
	e    = 7 / 2
	f    = 7 / 2.0
	g    = "s" + "t"
	h    = len("héllo")
)

func main() {
	var s u8 = 3
	x := 1 << s
	var y i64 = 1 << s
	_, _ = x, y
}
`)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	want := map[string]string{
		"a": "0 untyped int",
		"b": "10 untyped int",
		"c": "64 i8",
		"d": "120 untyped rune",
		"e": "3 untyped int",
		"f": "3.5 untyped float",
		"g": `"st" untyped string`,
		"h": "6 int",
	}
	for name, w := range want {
		obj := info.Scope.Lookup(name).(*Const)
		if got := obj.Val.String() + " " + obj.typ.String(); got != w {
			t.Errorf("%s = %s, want %s", name, got, w)
		}
	}
	// The untyped 1 in a non-constant shift takes its type from the
	// context (expr.go, as in Go).
	shifts := map[string]bool{}
	for e, tv := range info.Types {
		if b, ok := e.(*syntax.BasicLit); ok && b.Value == "1" && tv.Value != nil && constant.Sign(tv.Value) > 0 {
			shifts[tv.Type.String()] = true
		}
	}
	if !shifts["int"] || !shifts["i64"] {
		t.Errorf("types of shifted constants: %v, want int and i64", shifts)
	}
}

func TestWordSize(t *testing.T) {
	src := `package main

func main() {
	var x int = 40000
	_ = x
}
`
	f, _ := syntax.Parse("a.an", []byte(src))
	if _, errs := Check(&Config{Module: "m", IntBits: 64}, []*syntax.File{f}); len(errs) > 0 {
		t.Errorf("64-bit int: %v", errs)
	}
	_, errs := Check(&Config{Module: "m", IntBits: 16}, []*syntax.File{f})
	if len(errs) != 1 || errs[0].Rule != "expr.int.const" {
		t.Errorf("16-bit int: got %v, want one expr.int.const error", errs)
	}
}
