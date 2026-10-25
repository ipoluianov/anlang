package syntax

import (
	"strings"
	"testing"
)

func TestParseHello(t *testing.T) {
	src := `// Command hello prints a greeting.
package main

import "kern/fmt"

func main() {
    fmt.Println("Hello, World!")
}
`
	f, errs := Parse("main.kn", []byte(src))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if f.Name.Name != "main" {
		t.Errorf("package %s, want main", f.Name.Name)
	}
	if len(f.Imports) != 1 || f.Imports[0].Path.Value != `"kern/fmt"` {
		t.Fatalf("imports %+v", f.Imports)
	}
	if len(f.Decls) != 1 {
		t.Fatalf("got %d decls, want 1", len(f.Decls))
	}
	fn := f.Decls[0].(*FuncDecl)
	if fn.Name.Name != "main" || len(fn.Body.List) != 1 {
		t.Fatalf("func %s with %d statements", fn.Name.Name, len(fn.Body.List))
	}
	call := fn.Body.List[0].(*ExprStmt).X.(*CallExpr)
	sel := call.Fun.(*SelectorExpr)
	if sel.X.(*Name).Name != "fmt" || sel.Sel.Name != "Println" {
		t.Errorf("call of %s.%s", sel.X.(*Name).Name, sel.Sel.Name)
	}
	if lit := call.Args[0].(*BasicLit); lit.Value != `"Hello, World!"` {
		t.Errorf("argument %s", lit.Value)
	}
}

func TestParseProfileAndGroupedImports(t *testing.T) {
	src := `package main

profile core, app

import (
    "kern/dos"
    enc "kern/encoding"
)

func main() {}
`
	f, errs := Parse("main.kn", []byte(src))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(f.Profiles) != 2 || f.Profiles[0].Name != "core" || f.Profiles[1].Name != "app" {
		t.Errorf("profiles %v", f.Profiles)
	}
	if len(f.Imports) != 2 || f.Imports[1].Name.Name != "enc" {
		t.Errorf("imports %+v", f.Imports)
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
		{"package main\nvar x int", "", "not supported yet"},
		{"package main\nfunc main() { x := 1 }", "", "not supported yet"},
		{"package main\nfunc main() {", "lex.token.go", "expected '}'"},
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
