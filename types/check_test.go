package types

import (
	"strings"
	"testing"

	"an/ir"
	"an/syntax"
)

func check(t *testing.T, srcs ...string) (*ir.Program, []*syntax.Error) {
	t.Helper()
	var files []*syntax.File
	for i, src := range srcs {
		f, errs := syntax.Parse(string(rune('a'+i))+".an", []byte(src))
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
	}
	return Check(&Package{Module: "m", Files: files})
}

func TestCheckHello(t *testing.T) {
	prog, errs := check(t, `package main

import "an/fmt"

func main() {
    fmt.Println("Hello,", "World!")
    fmt.Print("a", "b")
    greet()
}

func greet() {
    fmt.Println()
}
`)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var texts []string
	for _, d := range prog.Data {
		texts = append(texts, string(d.Bytes))
	}
	// Println joins with spaces and adds a newline; Print of strings
	// adds no spaces.
	want := []string{"Hello, World!\n", "ab", "\n"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Errorf("data %q, want %q", texts, want)
	}
	if prog.Main == nil || len(prog.Main.Body) != 4 {
		t.Fatalf("main body %v", prog.Main)
	}
	if _, ok := prog.Main.Body[2].(*ir.Call); !ok {
		t.Errorf("third instruction %T, want *ir.Call", prog.Main.Body[2])
	}
}

func TestCheckErrors(t *testing.T) {
	tests := []struct {
		src  string
		rule string
		msg  string
	}{
		{"package main\nfunc f() {}", "decl.package.main", "no func main"},
		{"package lib\nfunc main() {}", "decl.package.main", "package lib"},
		{"package main\nfunc main() int {}", "decl.package.main", "no parameters and no results"},
		{"package main\nfunc init() {}\nfunc main() {}", "decl.func.no-init", "init"},
		{"package main\nfunc main() {}\nfunc main() {}", "decl.func.go", "redeclared"},
		{"package main\nprofile kernel\nfunc main() {}", "decl.profile.names", "unknown profile"},
		{"package main\nimport \"fmt\"\nfunc main() {}", "decl.import.paths", "must start with an/"},
		{"package main\nimport _ \"an/fmt\"\nfunc main() {}", "decl.import.no-blank-dot", "blank imports"},
		{"package main\nimport \"an/fmt\"\nfunc main() {}", "", "imported and not used"},
		{"package main\nfunc main() { g() }", "", "undefined: g"},
		{"package main\nimport \"an/fmt\"\nfunc main() { fmt.Println(1) }", "", "not supported yet"},
	}
	for _, tt := range tests {
		_, errs := check(t, tt.src)
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

func TestCheckProfileMismatch(t *testing.T) {
	_, errs := check(t,
		"package main\nprofile core\nfunc main() {}",
		"package main\nprofile app\nfunc f() {}")
	if len(errs) == 0 || errs[0].Rule != "decl.profile.package" {
		t.Errorf("got %v, want decl.profile.package", errs)
	}
}
