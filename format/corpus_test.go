package format

import (
	"bytes"
	"fmt"
	"go/build"
	goformat "go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kern/syntax"
)

// TestGoCorpus formats every .go file of the Go distribution in GoMode
// and compares the result with the output of go/format, the library
// behind gofmt. For the part of Kern shared with Go both must agree byte
// for byte.
func TestGoCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus test is slow")
	}
	root := filepath.Join(build.Default.GOROOT, "src")
	if _, err := os.Stat(root); err != nil {
		t.Skip("Go sources not found:", err)
	}
	var files, same int
	var diffs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
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
		want, err := goformat.Source(src)
		if err != nil {
			return nil // not valid Go: nothing to compare with
		}
		files++
		out, errs := Source(path, src, syntax.GoMode)
		if len(errs) > 0 {
			diffs = append(diffs, fmt.Sprintf("%s: %v", path, errs[0]))
			return nil
		}
		before, _ := syntax.ParseMode(path, src, syntax.GoMode)
		after, _ := syntax.ParseMode(path, out, syntax.GoMode)
		if !sameTree(before, after) {
			diffs = append(diffs, path+": formatting changed the syntax tree")
			return nil
		}
		if bytes.Equal(out, want) {
			same++
			return nil
		}
		diffs = append(diffs, firstDiff(path, want, out))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d of %d files reproduced exactly", same, files)
	for i, d := range diffs {
		if i == 30 {
			break
		}
		t.Error(d)
	}
	if len(diffs) > 0 {
		t.Errorf("%d files differ", len(diffs))
	}
}

// firstDiff describes the first differing line of want and got.
func firstDiff(name string, want, got []byte) string {
	wl := strings.Split(string(want), "\n")
	gl := strings.Split(string(got), "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("%s:%d:\n\twant %q\n\tgot  %q", name, i+1, w, g)
		}
	}
	return name + ": differs"
}
