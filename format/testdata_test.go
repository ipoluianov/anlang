package format

import (
	"bytes"
	"go/build"
	goformat "go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kern/syntax"
)

// TestGoTestdata formats the files in the testdata directories of the Go
// distribution: unlike the rest of the Go sources, many of them are not
// in canonical form, including the inputs of the go/printer tests. The
// output must match go/format except for features kern fmt does not
// implement: reformatting doc comments, //go:build lines and //line
// directives, removal of duplicate imports, and formatting of fragments.
func TestGoTestdata(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus test is slow")
	}
	root := filepath.Join(build.Default.GOROOT, "src")
	var files, same int
	var bad []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if d.IsDir() || !strings.Contains(path, "/testdata/") {
			return nil
		}
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".input") && !strings.HasSuffix(path, ".golden") {
			return nil
		}
		src, _ := os.ReadFile(path)
		want, err := goformat.Source(src)
		if err != nil {
			return nil
		}
		files++
		out, errs := Source(path, src, syntax.GoMode)
		if len(errs) == 0 {
			before, _ := syntax.ParseMode(path, src, syntax.GoMode)
			after, _ := syntax.ParseMode(path, out, syntax.GoMode)
			if !sameTree(before, after) {
				t.Errorf("%s: formatting changed the syntax tree", path)
			}
		}
		if len(errs) == 0 && bytes.Equal(out, want) {
			same++
			return nil
		}
		if len(errs) > 0 {
			bad = append(bad, path+": "+errs[0].Error())
		} else {
			bad = append(bad, firstDiff(path, want, out))
		}
		return nil
	})
	t.Logf("%d of %d files match go/format", same, files)
	if files == 0 {
		t.Skip("no Go testdata found")
	}
	// 1119 of 1168 match with Go 1.25; the rest are the features above.
	if float64(same) < 0.95*float64(files) {
		for _, b := range bad {
			t.Log(b)
		}
		t.Errorf("only %d of %d files match go/format", same, files)
	}
}
