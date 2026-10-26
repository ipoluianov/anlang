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
	"an/effects"
	"an/interp"
	"an/lower"
	"an/syntax"
	"an/types"
)

// suiteModule and suiteTarget are the module name and the target of
// single-file tests.
const (
	suiteModule = "conf"
	suiteTarget = "linux-x86_64"
)

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
			case strings.HasSuffix(e.Name(), ".out"), strings.HasSuffix(e.Name(), ".in"), strings.HasSuffix(e.Name(), ".screen"):
				// Expected output, keyboard input and expected screen of a
				// reference program (conf.suite.layout).
				base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
				if !slices.ContainsFunc(entries, func(f os.DirEntry) bool {
					return f.Name() == base || f.Name() == base+".an"
				}) {
					t.Errorf("%s: no test named %s", path, base)
				}
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
			infos, diags := tt.run(t)
			got := ruleSet(diags)
			if !slices.Equal(got, want) {
				var b strings.Builder
				for _, d := range diags {
					fmt.Fprintf(&b, "\n\t%v", d)
				}
				t.Fatalf("rules %s, want %s; diagnostics:%s", show(got), show(want), b.String())
			}
			tt.checkEffects(t, infos, len(want) == 0)
			out, err := os.ReadFile(filepath.Join("testdata", tt.rule, tt.name+".out"))
			if err == nil {
				if len(want) > 0 {
					t.Fatalf("a negative test has no expected output")
				}
				tt.interpret(t, infos, string(out))
			}
		})
	}
}

// expectation reads the // want: and // checks: lines of the test's
// sources. want is the sorted set of rules expected in the diagnostics;
// it is empty for "// want: ok".
func (tt suiteTest) expectation(t *testing.T) (want, checks []string) {
	t.Helper()
	files := tt.files(t)
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

// files returns the source files and anlang.mod of the test.
func (tt suiteTest) files(t *testing.T) []string {
	if !tt.dir {
		return []string{tt.path}
	}
	var files []string
	err := filepath.WalkDir(tt.path, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(path, ".an") || d.Name() == "anlang.mod") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// run checks the test and returns the type information of its packages,
// package main last, and its diagnostics.
func (tt suiteTest) run(t *testing.T) ([]*types.Info, []*syntax.Error) {
	t.Helper()
	if !tt.dir {
		data, err := os.ReadFile(tt.path)
		if err != nil {
			t.Fatal(err)
		}
		t, _ := build.LookupTarget(suiteTarget)
		info, diags := build.CheckSources(suiteModule, t, []build.Source{{Name: filepath.Base(tt.path), Data: data}})
		return []*types.Info{info}, diags
	}
	var infos []*types.Info
	p, errs := build.Load(tt.path)
	if p != nil {
		infos, errs = p.Check(p.DefaultTargets()[0])
	}
	var diags []*syntax.Error
	for _, err := range errs {
		var d *syntax.Error
		if !errors.As(err, &d) {
			t.Fatal(err)
		}
		diags = append(diags, d)
	}
	return infos, diags
}

// checkEffects compares the // effects: and // exec: expectations of
// the test with the inferred effects (conf.suite.layout, item 8).
func (tt suiteTest) checkEffects(t *testing.T, infos []*types.Info, positive bool) {
	t.Helper()
	type expect struct {
		kind, fn string
		want     []string
	}
	var expects []expect
	for _, f := range tt.files(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			for _, kind := range []string{"effects", "exec"} {
				if rest, ok := strings.CutPrefix(line, "// "+kind+":"); ok {
					fields := strings.Fields(rest)
					if len(fields) < 2 {
						t.Fatalf("%s: want \"// %s: <function> <effect> ...\"", f, kind)
					}
					want := fields[1:]
					if len(want) == 1 && want[0] == "none" {
						want = nil
					}
					slices.Sort(want)
					expects = append(expects, expect{kind, fields[0], want})
				}
			}
		}
	}
	if len(expects) == 0 {
		return
	}
	if !positive {
		t.Fatalf("effect expectations in a negative test")
	}
	var in []effects.Input
	for _, info := range infos {
		in = append(in, effects.Input{Info: info})
	}
	prog := effects.NewProgram(effects.AnalyzeAll(in, nil))
	root := infos[len(infos)-1].Pkg.Path
	for _, x := range expects {
		key := root + "." + x.fn
		s := prog.Summary(key)
		if s == nil {
			t.Errorf("// %s: no function %s", x.kind, x.fn)
			continue
		}
		set := s.C
		if x.kind == "exec" {
			set = prog.Exec(key)
		}
		var got []string
		for e := range set {
			got = append(got, e.Name())
		}
		slices.Sort(got)
		if !slices.Equal(got, x.want) {
			var chains []string
			for e := range set {
				chains = append(chains, strings.Join(prog.Chain(key, e, x.kind == "exec"), " → "))
			}
			slices.Sort(chains)
			t.Errorf("%s of %s: got %s, want %s\n\t%s\n\t%s", x.kind, x.fn, effectList(got), effectList(x.want), s, strings.Join(chains, "\n\t"))
		}
	}
}

func effectList(es []string) string {
	if len(es) == 0 {
		return "none"
	}
	return strings.Join(es, " ")
}

// interpret runs a reference program in the IR interpreter and compares
// its observable behavior with the expected output (conf.reference):
// what the program writes to standard output and standard error, in
// order, followed by a line "exit status N" unless the status is 0.
func (tt suiteTest) interpret(t *testing.T, infos []*types.Info, want string) {
	t.Helper()
	prog, errs := lower.Program(infos...)
	if len(errs) > 0 {
		t.Fatalf("lowering: %v", errs)
	}
	if errs := prog.Validate(); len(errs) > 0 {
		t.Fatalf("invalid IR: %v", errs)
	}
	prog.IntBits = 64
	var out strings.Builder
	err := interp.Run(prog, interp.Options{Stdout: &out, Stderr: &out, MaxSteps: 1e8})
	var ne *interp.NotExecutableError
	if errors.As(err, &ne) {
		// A program for one target: its emulator runs it (TestBackendDOS).
		t.Logf("not run in the IR interpreter: %v", err)
		return
	}
	if err != nil {
		out.WriteString("exit status 2\n")
	}
	if got := out.String(); got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
		if testing.Verbose() {
			t.Logf("IR:\n%s", prog)
		}
	}
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

// TestLowerSuite translates every positive test of the suite to IR and
// validates the result (spec/22-compiler.md §22.10.1). Constructs of
// later roadmap steps are skipped.
func TestLowerSuite(t *testing.T) {
	for _, tt := range loadSuite(t) {
		t.Run(tt.rule+"/"+tt.name, func(t *testing.T) {
			if want, _ := tt.expectation(t); len(want) > 0 {
				t.Skip("negative test")
			}
			infos, diags := tt.run(t)
			if len(diags) > 0 {
				t.Skip("rejected; TestSuite reports it")
			}
			var in []effects.Input
			for _, info := range infos {
				in = append(in, effects.Input{Info: info})
			}
			effects.NewProgram(effects.AnalyzeAll(in, nil))
			prog, errs := lower.Program(infos...)
			for _, err := range errs {
				if !strings.Contains(err.Msg, "not supported yet") {
					t.Errorf("lowering: %v", err)
				}
			}
			if len(errs) > 0 {
				return
			}
			for _, err := range prog.Validate() {
				t.Errorf("invalid IR: %v", err)
			}
		})
	}
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
