package syntax

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoCorpus parses every .go file of the Go distribution in GoMode.
// Anlang syntax is Go syntax plus the novelty inventory, so the Go sources
// are an oracle for the shared part of the parser (spec/23-conformance.md:
// external oracles are used only in tests of the environment).
func TestGoCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus test is slow")
	}
	root := filepath.Join(build.Default.GOROOT, "src")
	if _, err := os.Stat(root); err != nil {
		t.Skip("Go sources not found:", err)
	}
	files, failed := 0, 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// testdata holds deliberately invalid files.
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		if _, errs := ParseMode(path, src, GoMode); len(errs) > 0 {
			failed++
			if failed <= 20 {
				t.Errorf("%s", errs[0])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed > 0 {
		t.Errorf("%d of %d files failed to parse", failed, files)
	}
	t.Logf("parsed %d files", files)
}
