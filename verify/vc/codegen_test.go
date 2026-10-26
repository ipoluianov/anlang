package vc_test

import (
	"os"
	"path/filepath"
	"testing"

	"an/build"
	"an/ir"
)

// TestChecksRemoved checks that the IR of a typical loop has no bounds
// check: its index instructions are proven, so that the code generator
// emits no test (contract.check.runtime).
func TestChecksRemoved(t *testing.T) {
	for _, tc := range []struct {
		body    string
		checked int // the index instructions that keep their check
	}{
		{"for i := range 8 {\n\t\ts += int(a[i])\n\t}", 0},
		{"for i := 0; i < len(a); i++ {\n\t\ts += int(a[i])\n\t}", 0},
		{"for i := 0; i <= len(a); i++ {\n\t\ts += int(a[i%9])\n\t}", 1},
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "anlang.mod"), []byte("module  conf\nanlang  1.0\ntargets dos-com\n"), 0o644)
		src := "package main\n\nprofile core\n\nimport \"an/fmt\"\n\nfunc main() {\n\tvar a [8]u8\n\ts := 0\n\t" + tc.body + "\n\tfmt.Println(s)\n}\n"
		os.WriteFile(filepath.Join(dir, "main.an"), []byte(src), 0o644)
		p, errs := build.Load(dir)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		prog, errs := p.BackendProgram("dos-com")
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		checked := 0
		for _, b := range prog.Main.Blocks {
			for _, in := range b.Instrs {
				switch in := in.(type) {
				case *ir.Index:
					if !in.Proven {
						checked++
					}
				case *ir.IndexAddr:
					if !in.Proven {
						checked++
					}
				}
			}
		}
		if checked != tc.checked {
			t.Errorf("%s: %d index instructions keep their check, want %d", tc.body, checked, tc.checked)
		}
	}
}
