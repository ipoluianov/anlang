package conformance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"an/arch/desc"
	"an/build"
	_ "an/codegen"
	"an/emu/dos"
	"an/interp"
)

// TestBackendDOS runs the reference programs that the target dos-com
// accepts (conf.backend.select) in the 8086 interpreter and compares
// their observable behavior with the interpretation of their IR for the
// same target (conf.backend.ir).
func TestBackendDOS(t *testing.T) {
	const target = "dos-com"
	tg, _ := build.LookupTarget(target)
	d, errs := tg.Description()
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	a, errs := desc.ForName(d.Arch.Name)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	ran := 0
	for _, tt := range loadSuite(t) {
		if _, err := os.Stat(filepath.Join("testdata", tt.rule, tt.name+".out")); err != nil {
			continue
		}
		t.Run(tt.rule+"/"+tt.name, func(t *testing.T) {
			dir := tt.path
			if !tt.dir {
				dir = t.TempDir()
				data, err := os.ReadFile(tt.path)
				if err != nil {
					t.Fatal(err)
				}
				os.WriteFile(filepath.Join(dir, "anlang.mod"), []byte("module  conf\nanlang  1.0\ntargets "+target+"\n"), 0o644)
				os.WriteFile(filepath.Join(dir, "main.an"), data, 0o644)
			}
			p, errs := build.Load(dir)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			prog, errs := p.BackendProgram(target)
			if len(errs) > 0 {
				t.Skipf("the target does not accept the program: %v", errs[0])
			}
			// The IR interpreter gives the expected behavior; a program that
			// uses its architecture directly has it in its .out file.
			var want strings.Builder
			err := interp.Run(prog, interp.Options{Stdout: &want, Stderr: &want, MaxSteps: 1e8})
			var ne *interp.NotExecutableError
			if errors.As(err, &ne) {
				out, _ := os.ReadFile(filepath.Join("testdata", tt.rule, tt.name+".out"))
				want.Reset()
				want.Write(out)
			} else if err != nil {
				want.WriteString("exit status 2\n")
			}
			image, errs := build.Backend(prog, d)
			if len(errs) > 0 {
				t.Fatalf("code generation: %v", errs)
			}
			m, err := dos.Load(a, image)
			if err != nil {
				t.Fatal(err)
			}
			var got strings.Builder
			m.Stdout, m.Stderr, m.MaxSteps = &got, &got, 50_000_000
			if in, err := os.ReadFile(filepath.Join("testdata", tt.rule, tt.name+".in")); err == nil {
				m.Stdin = strings.NewReader(string(in))
			}
			status, err := m.Run()
			if err != nil {
				t.Fatalf("emulation: %v; output so far:\n%s", err, got.String())
			}
			if status != 0 {
				got.WriteString("exit status " + itoa(status) + "\n")
			}
			if ne != nil && os.Getenv("ANLANG_UPDATE") != "" {
				// Record the behavior of a program that only its target runs.
				base := filepath.Join("testdata", tt.rule, tt.name)
				os.WriteFile(base+".out", []byte(got.String()), 0o644)
				os.WriteFile(base+".screen", []byte(m.Screen()), 0o644)
				return
			}
			if got.String() != want.String() {
				t.Errorf("output:\n%s\nwant:\n%s", got.String(), want.String())
			}
			if screen, err := os.ReadFile(filepath.Join("testdata", tt.rule, tt.name+".screen")); err == nil && m.Screen() != string(screen) {
				t.Errorf("screen:\n%s\nwant:\n%s", m.Screen(), screen)
			}
			ran++
		})
	}
	if ran == 0 {
		t.Errorf("no reference program ran on %s (conf.backend.select)", target)
	}
	t.Logf("%d reference programs agree on %s", ran, target)
}

func itoa(n int) string {
	s := ""
	if n == 0 {
		return "0"
	}
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
