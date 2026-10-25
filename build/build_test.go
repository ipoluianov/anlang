package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMatchSuffix(t *testing.T) {
	linux, _ := LookupTarget("linux-x86_64")
	dos, _ := LookupTarget("dos-com")
	tests := []struct {
		file       string
		linux, dos bool
	}{
		{"main", true, true},
		{"main_linux", true, false},
		{"main_x86_64", true, false},
		{"main_linux_x86_64", true, false},
		{"main_dos_i8086", false, true},
		{"main_i8086", false, true},
		{"main_linux_i8086", false, false},
		{"my_helper", true, true},
	}
	for _, tt := range tests {
		if got := matchSuffix(tt.file, linux); got != tt.linux {
			t.Errorf("%s for linux-x86_64: got %v", tt.file, got)
		}
		if got := matchSuffix(tt.file, dos); got != tt.dos {
			t.Errorf("%s for dos-com: got %v", tt.file, got)
		}
	}
}

func TestSelectFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"main.an", "io_linux.an", "io_dos.an", "main_test.an", "_draft.an", ".hidden.an", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	linux, _ := LookupTarget("linux-x86_64")
	got, err := SelectFiles(dir, linux)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "io_linux.an main.an" {
		t.Errorf("got %v", got)
	}
}

// newProject writes a project with the given files into a temporary
// directory and loads it.
func newProject(t *testing.T, files map[string]string) *Project {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, errs := Load(dir)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return p
}

func requireLinuxAMD64(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("running linux-x86_64 binaries needs a linux/amd64 host")
	}
}

// TestHelloExample builds examples/hello, runs it and checks its output.
func TestHelloExample(t *testing.T) {
	requireLinuxAMD64(t)
	src, err := os.ReadFile("../examples/hello/main.an")
	if err != nil {
		t.Fatal(err)
	}
	p := newProject(t, map[string]string{
		"anlang.mod": "module  hello\nanlang  1.0\ntargets linux-x86_64\n",
		"main.an":    string(src),
	})
	out, errs := p.Build("linux-x86_64")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got, err := exec.Command(out).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Hello, World!\n" {
		t.Errorf("output %q", got)
	}
}

func TestPrintFailurePanics(t *testing.T) {
	requireLinuxAMD64(t)
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	p := newProject(t, map[string]string{
		"anlang.mod": "module  p\nanlang  1.0\n",
		"main.an":    "package main\n\nimport \"an/fmt\"\n\nfunc main() {\n\tfmt.Println(\"x\")\n}\n",
	})
	out, errs := p.Build("linux-x86_64")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	var stderr bytes.Buffer
	cmd := exec.Command(out)
	cmd.Stdout, cmd.Stderr = full, &stderr
	err = cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 {
		t.Fatalf("got %v, want exit status 2 (std.fmt.print)", err)
	}
	if !strings.HasPrefix(stderr.String(), "panic: ") {
		t.Errorf("stderr %q", stderr.String())
	}
}

func TestCallsAndOrder(t *testing.T) {
	requireLinuxAMD64(t)
	p := newProject(t, map[string]string{
		"anlang.mod": "module  p\nanlang  1.0\n",
		"main.an": `package main

import "an/fmt"

func main() {
	first()
	fmt.Println("two", "three")
	last()
}
`,
		"helpers.an": `package main

import f "an/fmt"

func first() {
	f.Print("one ")
}

func last() {
	f.Println("four")
}
`,
	})
	out, errs := p.Build("linux-x86_64")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got, err := exec.Command(out).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one two three\nfour\n" {
		t.Errorf("output %q", got)
	}
}

// TestReproducible checks that two builds give identical bytes
// (spec/20-toolchain.md, reproducibility).
func TestReproducible(t *testing.T) {
	files := map[string]string{
		"anlang.mod": "module  p\nanlang  1.0\n",
		"main.an":    "package main\n\nimport \"an/fmt\"\n\nfunc main() {\n\tfmt.Println(\"x\")\n}\n",
	}
	var outs [][]byte
	for i := 0; i < 2; i++ {
		p := newProject(t, files)
		out, errs := p.Build("linux-x86_64")
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		outs = append(outs, data)
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Error("builds differ")
	}
}

func TestUnsupportedTarget(t *testing.T) {
	p := newProject(t, map[string]string{"anlang.mod": "module  p\nanlang  1.0\n"})
	_, errs := p.Build("dos-com")
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "step 13") {
		t.Errorf("got %v", errs)
	}
	_, errs = p.Build("pdp11")
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "target.select.resolve") {
		t.Errorf("got %v", errs)
	}
}

func TestNonCanonicalRejected(t *testing.T) {
	p := newProject(t, map[string]string{
		"anlang.mod": "module  p\nanlang  1.0\n",
		"main.an":    "package main\n\nfunc main() {\n    f()\n}\n\nfunc f() {}\n",
	})
	_, errs := p.Build("linux-x86_64")
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "main.an:4:1") || !strings.Contains(errs[0].Error(), "lex.source.canonical") {
		t.Errorf("got %v, want lex.source.canonical at main.an:4", errs)
	}
}
