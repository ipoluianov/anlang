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
		{"package main\nfunc f(n int) int {\n\trequires n > 0\n\treturn n\n}\nfunc main() { _ = f(1) }", "requires clauses: not supported yet"},
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

// TestImports checks a package that imports another package of the
// project: exported names are visible, unexported ones are not
// (decl.visibility.go).
func TestImports(t *testing.T) {
	parse := func(name, src string) []*syntax.File {
		f, errs := syntax.Parse(name, []byte(src))
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		return []*syntax.File{f}
	}
	lib, errs := Check(&Config{Module: "m", Path: "m/lib"}, parse("lib/lib.an", `package lib

const Max = 10

type Point struct {
	X, Y int
	tag  string
}

func (p Point) Sum() int { return p.X + p.Y }

func (p Point) hidden() {}

type Shape interface {
	Area() int
}

func New(x int) Point { return Point{X: x} }
`))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	imports := map[string]*Package{"m/lib": lib.Pkg}
	_, errs = Check(&Config{Module: "m", Imports: imports}, parse("main.an", `package main

import "m/lib"

func main() {
	p := lib.New(lib.Max)
	var q lib.Point = lib.Point{X: 1, Y: 2}
	_ = p.Sum() + q.X
}
`))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	_, errs = Check(&Config{Module: "m", Imports: imports}, parse("main.an", `package main

import "m/lib"

func main() {
	p := lib.New(1)
	p.hidden()
	_ = p.tag
	_ = lib.Point{1, 2, "t"}
	_ = lib.Point{tag: "t"}
	_ = lib.max
}
`))
	var got []string
	for _, e := range errs {
		got = append(got, e.Rule)
	}
	want := "decl.visibility.go decl.visibility.go decl.visibility.go decl.visibility.go decl.visibility.go"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v, want five decl.visibility.go errors", errs)
	}
}

// TestAddVariant checks the acceptance criterion of roadmap step 3:
// adding a variant to an enum type breaks every switch over it that has
// no default case (stmt.switch.variant.exhaustive).
func TestAddVariant(t *testing.T) {
	src := `package main

type Shape enum {
	Circle{r f64}
	Rect{w, h f64}
	%s
}

func Area(s Shape) f64 {
	switch s {
	case Circle{r}:
		return 3 * r * r
	case Rect{w, h}:
		return w * h
	}
}

func Name(s Shape) string {
	switch s {
	case Circle{_}:
		return "circle"
	default:
		return "other"
	}
}

func Kind(s Shape) int {
	n := 0
	switch s {
	case Circle, Rect:
		n = 1
	}
	return n
}

func main() {
	_ = Area(Circle(1)) + f64(Kind(Rect(1, 2)))
	_ = Name(Circle(1))
}
`
	if _, errs := check(t, strings.Replace(src, "%s", "", 1)); len(errs) > 0 {
		t.Fatalf("before: %v", errs)
	}
	_, errs := check(t, strings.Replace(src, "%s", "Square{side f64}", 1))
	var lines []int
	for _, e := range errs {
		if e.Rule != "stmt.switch.variant.exhaustive" || !strings.Contains(e.Msg, "missing Square") {
			t.Errorf("unexpected error %v", e)
		}
		lines = append(lines, e.Pos.Line)
	}
	// The switches in Area and Kind; Name has a default case.
	if len(lines) != 2 || lines[0] != 10 || lines[1] != 29 {
		t.Errorf("errors at lines %v, want 10 and 29", lines)
	}
}
