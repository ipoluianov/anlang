package wasm_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"an/build"
	_ "an/codegen"
)

// TestCounterInBrowser builds the example counter-web for the target web
// and runs its page in headless Chrome, typing commands into the page
// while the program waits for them (target.format.html).
func TestCounterInBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	chrome := ""
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			chrome = p
			break
		}
	}
	if chrome == "" {
		t.Skip("no Chrome or Chromium is installed")
	}
	dir := t.TempDir()
	src := filepath.Join("..", "..", "examples", "counter-web")
	for _, f := range []string{"anlang.mod", "main.an"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	p, errs := build.Load(dir)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, errs := p.Build("web"); len(errs) > 0 {
		t.Fatal(errs)
	}
	page := filepath.Join(dir, "out", "web", "counter.html")
	cmd := exec.Command(node, "testdata/drive.mjs", chrome, "file://"+page,
		"wait:count: 0", "type:+", "wait:count: 1", "type:+", "wait:count: 2", "type:x-", "wait:count: 1", "enter", "type:q")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	want := "Counter: type + to add one, - to subtract one, 0 to reset, q to quit.\ncount: 0\ncount: 1\ncount: 2\ncount: 1\n3 commands, final count 1\n\nstatus: 0\n"
	if got := strings.ReplaceAll(string(out), "\r", ""); got != want {
		t.Errorf("the page shows:\n%s\nwant:\n%s", got, want)
	}
}
