package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"an/arch/desc"
	"an/build"
	"an/emu/cortexm"
	"an/emu/dos"
)

// prototypeTargets are the first three targets (spec/24-roadmap.md,
// phase 2).
var prototypeTargets = []string{"dos-com", "stm32f103c8", "web"}

// TestPrototype checks the success criterion of the prototype
// (conf.backend.same, roadmap step 19): a reference program that the
// first three targets accept, unchanged, behaves the same in the
// built-in emulators of DOS, Cortex-M and WebAssembly.
func TestPrototype(t *testing.T) {
	ran := 0
	for _, tt := range loadSuite(t) {
		if _, err := os.Stat(filepath.Join("testdata", tt.rule, tt.name+".out")); err != nil || tt.dir {
			continue
		}
		src, err := os.ReadFile(tt.path)
		if err != nil {
			t.Fatal(err)
		}
		stdin, _ := os.ReadFile(filepath.Join("testdata", tt.rule, tt.name+".in"))
		t.Run(tt.rule+"/"+tt.name, func(t *testing.T) {
			if samePrototype(t, map[string][]byte{"main.an": src}, stdin) {
				ran++
			}
		})
	}
	t.Run("examples/counter-web", func(t *testing.T) {
		files := map[string][]byte{}
		for _, f := range []string{"main.an"} {
			b, err := os.ReadFile(filepath.Join("..", "examples", "counter-web", f))
			if err != nil {
				t.Fatal(err)
			}
			files[f] = b
		}
		if !samePrototype(t, files, []byte("++-x0+q")) {
			t.Fatal("the example does not run on the three targets")
		}
		ran++
	})
	if ran < 10 {
		t.Errorf("only %d programs run on all of %s (conf.backend.same)", ran, strings.Join(prototypeTargets, ", "))
	}
	t.Logf("%d programs behave the same on %s", ran, strings.Join(prototypeTargets, ", "))
}

// samePrototype builds the program of files for each prototype target,
// runs it with stdin in the built-in emulator and checks that the
// behavior is the same; it reports false if a target does not accept the
// program.
func samePrototype(t *testing.T, files map[string][]byte, stdin []byte) bool {
	outs := map[string]string{}
	for _, target := range prototypeTargets {
		out, skip := emulate(t, target, files, stdin)
		if skip != "" {
			t.Logf("%s does not run the program: %s", target, skip)
			return false
		}
		outs[target] = out
	}
	first := prototypeTargets[0]
	for _, target := range prototypeTargets[1:] {
		if outs[target] != outs[first] {
			t.Errorf("%s:\n%s\n%s:\n%s", target, outs[target], first, outs[first])
		}
	}
	return true
}

// emulate builds the program of files for target and runs it with stdin
// in the built-in emulator of the target; skip tells why the target does
// not run the program.
func emulate(t *testing.T, target string, files map[string][]byte, stdin []byte) (out, skip string) {
	tg, _ := build.LookupTarget(target)
	d, errs := tg.Description()
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "anlang.mod"), []byte("module  conf\nanlang  1.0\ntargets "+target+"\n"), 0o644)
	for name, b := range files {
		os.WriteFile(filepath.Join(dir, name), b, 0o644)
	}
	p, errs := build.Load(dir)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	prog, errs := p.BackendProgram(target)
	if len(errs) > 0 {
		return "", errs[0].Error()
	}
	image, errs := build.Backend(prog, d)
	if len(errs) > 0 {
		msg := errs[0].Error()
		for _, why := range []string{"(target.format.size", "not supported yet by the code generator", "[emb.stack."} {
			if strings.Contains(msg, why) {
				return "", msg
			}
		}
		t.Fatalf("%s: code generation: %v", target, errs)
	}
	var got strings.Builder
	in := strings.NewReader(string(stdin))
	var em emulator
	switch d.Platform.Name {
	case "web":
		em = &wasmRun{module: build.FormatImage(d.Formats[0], d, image), stdin: in, out: &got}
	default:
		a, errs := desc.ForName(d.Arch.Name)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		if d.Platform.Name == "dos" {
			m, err := dos.Load(a, image.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			m.Stdout, m.Stderr, m.MaxSteps, m.Stdin = &got, &got, 50_000_000, in
			em = m
		} else {
			m, err := cortexm.Load(a, d, image.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			m.Stdout, m.Stderr, m.MaxSteps, m.Stdin, m.UART = &got, &got, 50_000_000, in, &got
			em = m
		}
	}
	status, err := em.Run()
	if err != nil {
		t.Fatalf("%s: emulation: %v; output so far:\n%s", target, err, got.String())
	}
	if status != 0 {
		got.WriteString("exit status " + itoa(status) + "\n")
	}
	return got.String(), ""
}
