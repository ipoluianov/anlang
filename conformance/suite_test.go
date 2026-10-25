package conformance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"an/build"
	"an/syntax"
)

// suiteModule is the module name of single-file tests.
const suiteModule = "conf"

// noRule stands for a diagnostic without a rule identifier in the sets
// of rules that tests compare. No expectation can name it, so such a
// diagnostic always fails a test.
const noRule = "(none)"

// suiteTest is one test of the conformance suite (spec/23-conformance.md,
// §23.1.1).
type suiteTest struct {
	rule string // directory name: the rule the test is filed under
	name string // file or directory name within the rule directory
	path string
	dir  bool // a project test, otherwise a single-file test
}

// loadSuite lists the tests in testdata.
func loadSuite(t *testing.T) []suiteTest {
	t.Helper()
	rules, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var tests []suiteTest
	for _, r := range rules {
		if !r.IsDir() {
			t.Errorf("testdata/%s: want only rule directories in testdata", r.Name())
			continue
		}
		entries, err := os.ReadDir(filepath.Join("testdata", r.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			path := filepath.Join("testdata", r.Name(), e.Name())
			switch {
			case e.IsDir():
				tests = append(tests, suiteTest{rule: r.Name(), name: e.Name(), path: path, dir: true})
			case strings.HasSuffix(e.Name(), ".an"):
				tests = append(tests, suiteTest{rule: r.Name(), name: strings.TrimSuffix(e.Name(), ".an"), path: path})
			default:
				t.Errorf("%s: want .an files and project directories only", path)
			}
		}
	}
	return tests
}

// TestSuite runs the conformance suite (conf.suite.expectation).
func TestSuite(t *testing.T) {
	defined := specRules(t)
	for _, tt := range loadSuite(t) {
		t.Run(tt.rule+"/"+tt.name, func(t *testing.T) {
			if !defined[tt.rule] {
				t.Fatalf("directory %s is not a rule defined in spec/ (conf.suite.naming)", tt.rule)
			}
			want, checks := tt.expectation(t)
			for _, r := range append(slices.Clone(want), checks...) {
				if !defined[r] {
					t.Fatalf("rule %s is not defined in spec/", r)
				}
			}
			if len(want) > 0 && !slices.Contains(want, tt.rule) && !slices.Contains(checks, tt.rule) {
				t.Fatalf("a negative test filed under %s must list it in // want: or // checks: (conf.suite.naming)", tt.rule)
			}
			diags := tt.run(t)
			got := ruleSet(diags)
			if !slices.Equal(got, want) {
				var b strings.Builder
				for _, d := range diags {
					fmt.Fprintf(&b, "\n\t%v", d)
				}
				t.Errorf("rules %s, want %s; diagnostics:%s", show(got), show(want), b.String())
			}
		})
	}
}

// expectation reads the // want: and // checks: lines of the test's
// sources. want is the sorted set of rules expected in the diagnostics;
// it is empty for "// want: ok".
func (tt suiteTest) expectation(t *testing.T) (want, checks []string) {
	t.Helper()
	files := []string{tt.path}
	if tt.dir {
		files = nil
		err := filepath.WalkDir(tt.path, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && (strings.HasSuffix(path, ".an") || d.Name() == "anlang.mod") {
				files = append(files, path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	nwant := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if rest, ok := strings.CutPrefix(line, "// want:"); ok {
				nwant++
				fields := strings.Fields(rest)
				if len(fields) == 1 && fields[0] == "ok" {
					continue
				}
				if len(fields) == 0 || slices.Contains(fields, "ok") {
					t.Fatalf("%s: want \"// want: ok\" or \"// want: <rule> ...\"", f)
				}
				want = append(want, fields...)
			} else if rest, ok := strings.CutPrefix(line, "// checks:"); ok {
				checks = append(checks, strings.Fields(rest)...)
			}
		}
	}
	if nwant != 1 {
		t.Fatalf("found %d // want: lines, want exactly one", nwant)
	}
	slices.Sort(want)
	return slices.Compact(want), checks
}

// run compiles the test up to code generation and returns its
// diagnostics.
func (tt suiteTest) run(t *testing.T) []*syntax.Error {
	t.Helper()
	if !tt.dir {
		data, err := os.ReadFile(tt.path)
		if err != nil {
			t.Fatal(err)
		}
		_, diags := build.CheckSources(suiteModule, []build.Source{{Name: filepath.Base(tt.path), Data: data}})
		return diags
	}
	p, errs := build.Load(tt.path)
	if p != nil {
		_, errs = p.Check(p.DefaultTargets()[0])
	}
	var diags []*syntax.Error
	for _, err := range errs {
		var d *syntax.Error
		if !errors.As(err, &d) {
			t.Fatal(err)
		}
		diags = append(diags, d)
	}
	return diags
}

func ruleSet(diags []*syntax.Error) []string {
	var rules []string
	for _, d := range diags {
		if d.Rule == "" {
			rules = append(rules, noRule)
		} else {
			rules = append(rules, d.Rule)
		}
	}
	slices.Sort(rules)
	return slices.Compact(rules)
}

func show(rules []string) string {
	if len(rules) == 0 {
		return "ok"
	}
	return strings.Join(rules, " ")
}

// TestSuiteCoverage reports, with -v, the rules of the language areas
// that have no test in the suite yet (conf.suite.per-rule). It does not
// fail: the suite grows with the roadmap.
func TestSuiteCoverage(t *testing.T) {
	defined := specRules(t)
	tested := map[string]bool{}
	for _, tt := range loadSuite(t) {
		tested[tt.rule] = true
	}
	missing := map[string][]string{}
	for r := range defined {
		area := r[:strings.Index(r, ".")]
		if !tested[r] {
			missing[area] = append(missing[area], r)
		}
	}
	var areas []string
	for a := range missing {
		areas = append(areas, a)
	}
	sort.Strings(areas)
	for _, a := range areas {
		sort.Strings(missing[a])
		t.Logf("%s: %d rules without tests: %s", a, len(missing[a]), strings.Join(missing[a], " "))
	}
	t.Logf("%d of %d rules have tests", len(tested), len(defined))
}
