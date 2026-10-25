package conformance

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"an/interp"
	"an/lower"
	"an/syntax"
	"an/types"
)

// knownRunMismatches are Go "run" tests whose output in the IR
// interpreter differs from Go's because of a deviation of Anlang that
// the test cannot detect by itself.
var knownRunMismatches = map[string]string{
	"test/fixedbugs/issue5704.go":  "expr.cmp.nil-empty: an empty slice equals nil",
	"test/fixedbugs/issue9738.go":  "expr.closure.capture-copy: the deferred closure sees the result as it was when the closure was created",
	"test/fixedbugs/issue19710.go": "mem.value.copy: the deferred deletes modify the map after its copy has been returned",
}

// runResult is the outcome of running one Go test program.
type runResult struct {
	file     string
	skip     string // reason for skipping the file; "" if compared
	deviated string // the Anlang deviation that explains a difference
	got      string
	want     string
}

// goRun runs a Go test program, renamed into Anlang, in the IR
// interpreter and compares its output with the output Go expects: the
// file with the extension .out next to it, or no output.
func goRun(path string) (r runResult) {
	r.file = path
	src, err := os.ReadFile(path)
	if err != nil {
		r.skip = "unreadable"
		return r
	}
	if line, _, _ := bytes.Cut(src, []byte("\n")); strings.TrimSpace(string(line)) != "// run" {
		r.skip = "not a run test"
		return r
	}
	asrc, ok := renameGo(src)
	if !ok {
		r.skip = "uses Anlang names"
		return r
	}
	af, perrs := syntax.Parse(filepath.Base(path), asrc)
	if len(perrs) > 0 {
		r.skip = "anlang parse error"
		return r
	}
	for _, imp := range af.Imports {
		if imp.Path.Value != `"an/fmt"` {
			r.skip = "imports other packages"
			return r
		}
	}
	info, errs := types.Check(&types.Config{Module: "conf"}, []*syntax.File{af})
	if len(errs) > 0 {
		r.skip = "rejected by the checker"
		return r
	}
	prog, errs := lower.Program(info)
	if len(errs) > 0 {
		r.skip = "not supported yet by the IR builder"
		if os.Getenv("ANLANG_ORACLE") == "unsupported" {
			fmt.Fprintln(os.Stderr, "UNSUPPORTED", errs[0])
		}
		return r
	}
	if errs := prog.Validate(); len(errs) > 0 {
		r.got = fmt.Sprintf("invalid IR: %v\n", errs)
		return r
	}
	prog.IntBits = 64
	want, err := os.ReadFile(strings.TrimSuffix(path, ".go") + ".out")
	if err == nil {
		r.want = string(want)
	}
	var out strings.Builder
	func() {
		defer func() {
			if e := recover(); e != nil {
				fmt.Fprintf(&out, "interpreter crash: %v\n", e)
			}
		}()
		if err := interp.Run(prog, interp.Options{Stdout: &out, Stderr: &out, MaxSteps: 2e7}); err != nil {
			p, _ := err.(*interp.Panic)
			if p != nil && p.Value == "runtime error: integer overflow" {
				r.deviated = "expr.int.checked"
			}
		}
	}()
	r.got = out.String()
	return r
}

// TestGoRunOracle runs the "run" test programs of the Go distribution
// that use the common subset in the IR interpreter and compares their
// output with Go's (spec/24-roadmap.md, step 6). Set ANLANG_ORACLE=v to
// print every mismatch and ANLANG_ORACLE_FILE to run one file.
func TestGoRunOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	var files []string
	for _, dir := range []string{"test", "test/ken", "test/fixedbugs"} {
		matches, _ := filepath.Glob(filepath.Join(runtime.GOROOT(), dir, "*.go"))
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Skip("no Go sources in GOROOT")
	}
	sort.Strings(files)
	if f := os.Getenv("ANLANG_ORACLE_FILE"); f != "" {
		files = []string{f}
	}
	skipped := map[string]int{}
	compared, agreed, deviated := 0, 0, 0
	var mismatches []runResult
	for _, f := range files {
		r := goRun(f)
		if r.skip != "" {
			skipped[r.skip]++
			continue
		}
		compared++
		switch {
		case r.got == r.want:
			agreed++
		case r.deviated != "":
			deviated++
		default:
			mismatches = append(mismatches, r)
		}
	}
	t.Logf("%d files: %d compared, %d agree, %d differ by deviation, %d mismatch; skipped: %v",
		len(files), compared, agreed, deviated, len(mismatches), skipped)
	verbose := os.Getenv("ANLANG_ORACLE") == "v"
	for _, r := range mismatches {
		rel, _ := filepath.Rel(runtime.GOROOT(), r.file)
		if verbose {
			t.Logf("%s\n--- got:\n%s--- want:\n%s", rel, r.got, r.want)
		}
		if _, ok := knownRunMismatches[rel]; !ok {
			t.Errorf("mismatch: %s", rel)
		}
	}
}
