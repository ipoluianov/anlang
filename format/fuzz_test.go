package format

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"kern/syntax"
)

// FuzzFormat checks that the parser and printer never panic, and that
// for every input that parses, the canonical form parses to the same
// syntax tree and is a fixed point of formatting (tool.cmd.fmt:
// formatting is idempotent).
func FuzzFormat(f *testing.F) {
	seeds, _ := filepath.Glob("../examples/*/*.kn")
	more, _ := filepath.Glob("../examples/*/*/*/*/*.kn")
	for _, name := range append(seeds, more...) {
		if src, err := os.ReadFile(name); err == nil {
			f.Add(src)
		}
	}
	f.Add([]byte("package p\n\ntype T enum {\n\tA\n\tB{x, y int, s string}\n}\n"))
	f.Add([]byte("package p\n\nfunc f(x inout []int) int {\n\trequires len(x) > 0\n\tfor i := range x {\n\t\tinvariant i >= 0\n\t\tx[i]++\n\t}\n\treturn x[0]\n}\n"))
	f.Add([]byte("package p\n\nvar ( a = 1 /* c */ ; b, c int = 2, 3 )\nfunc  g( ) { switch {case a>b: ; default:} }\n"))

	f.Fuzz(func(t *testing.T, src []byte) {
		out, errs := Source("fuzz.kn", src, syntax.KernMode)
		if len(errs) > 0 {
			return
		}
		before, _ := syntax.Parse("fuzz.kn", src)
		after, _ := syntax.Parse("fuzz.kn", out)
		if !sameTree(before, after) {
			t.Fatalf("formatting changed the syntax tree\n--- input\n%s\n--- output\n%s", src, out)
		}
		again, errs := Source("fuzz.kn", out, syntax.KernMode)
		if len(errs) > 0 {
			t.Fatalf("canonical form does not parse: %v\n--- input\n%s\n--- output\n%s", errs[0], src, out)
		}
		if !bytes.Equal(again, out) {
			t.Fatalf("formatting is not idempotent\n--- input\n%s\n--- first\n%s\n--- second\n%s", src, out, again)
		}
	})
}
