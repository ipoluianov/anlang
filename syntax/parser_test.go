package syntax

import (
	"strings"
	"testing"
)

func parse(t *testing.T, src string) *File {
	t.Helper()
	f, errs := Parse("t.kn", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("%v", errs)
	}
	return f
}

func TestParseHello(t *testing.T) {
	f := parse(t, `// Command hello prints a greeting.
package main

import "kern/fmt"

func main() {
	fmt.Println("Hello, World!")
}
`)
	if f.Name.Name != "main" || len(f.Imports) != 1 || f.Imports[0].Path.Value != `"kern/fmt"` {
		t.Fatalf("file %+v", f)
	}
	fn := f.Decls[1].(*FuncDecl)
	call := fn.Body.List[0].(*ExprStmt).X.(*CallExpr)
	if sel := call.Fun.(*SelectorExpr); sel.Sel.Name != "Println" {
		t.Errorf("call of %s", sel.Sel.Name)
	}
	if len(f.Comments) != 1 || f.Comments[0].List[0].Text != "// Command hello prints a greeting." {
		t.Errorf("comments %+v", f.Comments)
	}
}

func TestParseProfileAndImports(t *testing.T) {
	f := parse(t, "package main\n\nprofile core, app\n\nimport (\n\t\"kern/dos\"\n\tenc \"kern/encoding\"\n)\n")
	if names := f.Profiles(); len(names) != 2 || names[0].Name != "core" || names[1].Name != "app" {
		t.Errorf("profiles %v", names)
	}
	if len(f.Imports) != 2 || f.Imports[1].Name.Name != "enc" {
		t.Errorf("imports %+v", f.Imports)
	}
}

// TestParseKern checks every Kern construct of the novelty inventory
// (spec/01-lexical.md, §1.8) that has its own syntax.
func TestParseKern(t *testing.T) {
	f := parse(t, `package main

profile core

type Shape enum {
	Circle{r f64}
	Rect{w, h f64}
	TooLong{word string, max int}
	Empty
}

type Range struct {
	invariant lo <= hi
	layout c
	lo, hi int
}

func Clamp(x, lo, hi int) int {
	requires lo <= hi
	ensures lo <= result && result <= hi

	if x < lo {
		return lo
	}
	return x
}

func Sum(xs []i32) i64 {
	var s i64
	for i := range xs {
		invariant s == math.Sum(xs[:i])
		s += i64(xs[i])
	}
	return s
}

func (d inout Dictionary) Add(word string, buf inout []u8) error {
	for const f := range meta.Fields(T) {
		w.Key(f.Name)
	}
	switch s {
	case Circle{r}:
		return nil
	case mydictionary.TooLong{word, max}:
		return nil
	}
	return nil
}

func sqlite3_open(path cstring, db inout ffi.Handle) i32 = lib.Symbol("sqlite3_open", ffi.Effects{File})

interrupt TIM2() {
	ticks.Add(1)
}

trap PageFault(frame inout TrapFrame, code PageFaultCode) {
}

gpu func Blur(img []simd.F32x4, out gpu.Out[simd.F32x4]) {
	i := gpu.GlobalID().x
}
`)
	decls := f.Decls

	enum := decls[0].(*GenDecl).Specs[0].(*TypeSpec).Type.(*EnumType)
	if len(enum.Variants) != 4 {
		t.Fatalf("%d variants", len(enum.Variants))
	}
	if v := enum.Variants[1]; v.Fields.List[0].Names[1].Name != "h" {
		t.Errorf("Rect fields %+v", v.Fields.List[0])
	}
	if v := enum.Variants[2]; len(v.Fields.List) != 2 || v.Fields.List[1].Names[0].Name != "max" {
		t.Errorf("TooLong fields %+v", v.Fields.List)
	}
	if enum.Variants[3].Fields != nil {
		t.Error("Empty has fields")
	}

	st := decls[1].(*GenDecl).Specs[0].(*TypeSpec).Type.(*StructType)
	if len(st.Clauses) != 2 || st.Clauses[0].Word != "invariant" || st.Clauses[1].Word != "layout" || len(st.Fields) != 1 {
		t.Errorf("struct clauses %+v fields %d", st.Clauses, len(st.Fields))
	}

	clamp := decls[2].(*FuncDecl)
	if c, ok := clamp.Body.List[1].(*ContractStmt); !ok || c.Word != "ensures" {
		t.Errorf("Clamp body %T", clamp.Body.List[1])
	}

	loop := decls[3].(*FuncDecl).Body.List[1].(*RangeStmt)
	if c, ok := loop.Body.List[0].(*ContractStmt); !ok || c.Word != "invariant" {
		t.Errorf("loop body %T", loop.Body.List[0])
	}

	add := decls[4].(*FuncDecl)
	if !add.Recv.List[0].Inout.IsValid() || !add.Type.Params.List[1].Inout.IsValid() {
		t.Error("inout receiver or parameter not recorded")
	}
	if r := add.Body.List[0].(*RangeStmt); !r.Const {
		t.Error("for const not recorded")
	}
	sw := add.Body.List[1].(*SwitchStmt)
	if lit, ok := sw.Body.List[1].(*CaseClause).List[0].(*CompositeLit); !ok || len(lit.Elts) != 2 {
		t.Errorf("variant pattern %T", sw.Body.List[1].(*CaseClause).List[0])
	}

	if ext := decls[5].(*FuncDecl); ext.Binding == nil || ext.Body != nil {
		t.Error("external function binding not recorded")
	}
	if h := decls[6].(*FuncDecl); h.Handler != "interrupt" || h.Name.Name != "TIM2" {
		t.Errorf("interrupt handler %+v", h)
	}
	if h := decls[7].(*FuncDecl); h.Handler != "trap" {
		t.Errorf("trap handler %+v", h)
	}
	if g := decls[8].(*FuncDecl); !g.Gpu.IsValid() || g.Name.Name != "Blur" {
		t.Errorf("gpu function %+v", g)
	}
}

// TestContextualWordsAreNames checks that contextual words are ordinary
// identifiers outside their positions (lex.ident.contextual).
func TestContextualWordsAreNames(t *testing.T) {
	f := parse(t, `package main

func f() {
	x := 1
	requires := x
	layout, gpu := requires, 2
	_ = layout + gpu
}

type T struct {
	n       int
	layout  int
	invariant bool
}
`)
	body := f.Decls[0].(*FuncDecl).Body
	for _, s := range body.List {
		if _, ok := s.(*ContractStmt); ok {
			t.Errorf("contract clause outside the leading region: %+v", s)
		}
	}
	if st := f.Decls[1].(*GenDecl).Specs[0].(*TypeSpec).Type.(*StructType); len(st.Clauses) != 0 || len(st.Fields) != 3 {
		t.Errorf("struct %+v", st)
	}
}

func TestGoModeKeywords(t *testing.T) {
	src := "package p\n\nvar enum, inout, profile int\n\nfunc f() { requires(x) }\n"
	f, errs := ParseMode("t.go", []byte(src), GoMode)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if n := len(f.Decls[0].(*GenDecl).Specs[0].(*ValueSpec).Names); n != 3 {
		t.Errorf("%d names", n)
	}
	if _, ok := f.Decls[1].(*FuncDecl).Body.List[0].(*ExprStmt); !ok {
		t.Error("requires(x) is not a call in Go mode")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		src  string
		rule string
		msg  string
	}{
		{"func main() {}", "lex.token.go", "expected 'package'"},
		{"package main\nimport . \"kern/fmt\"", "decl.import.no-blank-dot", "dot imports"},
		{"package main\nfunc main() {}\nimport \"kern/fmt\"", "lex.token.go", "imports must appear"},
		{"package main\nfunc main() {", "lex.token.go", "expected '}'"},
		{"package main\nfunc f() { x := []int{1,\n2\n} }", "lex.token.go", "missing ','"},
		{"package main\nfunc f() { for const x := 0; x < 1; x++ {} }", "lex.token.go", "for const requires a range clause"},
		{"package main\nfunc f(a int, string) {}", "lex.token.go", "missing parameter type"},
	}
	for _, tt := range tests {
		_, errs := Parse("t.kn", []byte(tt.src))
		if len(errs) == 0 {
			t.Errorf("%q: no error", tt.src)
			continue
		}
		e := errs[0]
		if e.Rule != tt.rule || !strings.Contains(e.Msg, tt.msg) {
			t.Errorf("%q: got [%s] %s, want [%s] containing %q", tt.src, e.Rule, e.Msg, tt.rule, tt.msg)
		}
	}
}

func TestCompositeLiteralInHeader(t *testing.T) {
	f := parse(t, `package main

func f() {
	if x == (T{}) {
	}
	for _, v := range []int{1, 2} {
	}
	switch s := (S{a: 1}); s {
	}
}
`)
	body := f.Decls[0].(*FuncDecl).Body
	if len(body.List) != 3 {
		t.Fatalf("%d statements", len(body.List))
	}
}
