package wasm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestV8Validate encodes the text modules of the test suite and checks
// that V8, the engine of Node.js, an independent implementation
// (conf.oracle.use), accepts exactly the modules that the validator
// accepts. Binary encodings that the decoder and the text parser agree
// on but that differ from the standard, such as a misnumbered opcode,
// fail here.
func TestV8Validate(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := filepath.Join("..", "..", "external", "wasm", "spec", "test", "core")
	files, _ := filepath.Glob(filepath.Join(dir, "*.wast"))
	simd, _ := filepath.Glob(filepath.Join(dir, "simd", "*.wast"))
	files = append(files, simd...)
	if len(files) == 0 {
		t.Skip("the test suite is not downloaded: run scripts/fetch-wasm-testsuite.sh")
	}
	out := t.TempDir()
	var names []string
	valid := map[string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		toks, err := lex(string(src))
		if err != nil {
			t.Fatal(err)
		}
		cmds, err := parseSexps(toks)
		if err != nil {
			t.Fatal(err)
		}
		var mods []*sexp
		for _, c := range cmds {
			switch c.head() {
			case "module":
				mods = append(mods, c)
			case "assert_invalid", "assert_unlinkable", "assert_trap", "assert_uninstantiable":
				if c.list[1].head() == "module" {
					mods = append(mods, c.list[1])
				}
			}
		}
		base := strings.TrimSuffix(filepath.Base(f), ".wast")
		for k, c := range mods {
			m, _, err := moduleOf(c)
			if err != nil {
				continue
			}
			b := m.Encode()
			// The decoder reads back the same module.
			m2, err := Decode(b)
			if err != nil {
				t.Errorf("%s module %d: the encoding does not decode: %v", base, k, err)
				continue
			}
			if !bytes.Equal(m2.Encode(), b) {
				t.Errorf("%s module %d: the encoding does not round-trip", base, k)
			}
			name := fmt.Sprintf("%s.%d.wasm", base, k)
			if err := os.WriteFile(filepath.Join(out, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
			err = m.Validate()
			if err != nil && newerFeature(err.Error()) {
				names = names[:len(names)-1]
				os.Remove(filepath.Join(out, name))
				continue
			}
			valid[name] = err == nil
		}
	}
	script := `const fs = require("fs"), path = require("path");
const dir = process.argv[2];
for (const f of fs.readdirSync(dir).sort()) {
	if (!f.endsWith(".wasm")) continue;
	console.log(f + " " + WebAssembly.validate(fs.readFileSync(path.join(dir, f))));
}`
	js := filepath.Join(out, "validate.js")
	if err := os.WriteFile(js, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := exec.Command(node, js, out).Output()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(res)), "\n") {
		name, v8, _ := strings.Cut(line, " ")
		n++
		if (v8 == "true") != valid[name] {
			t.Errorf("%s: V8 validates %s, the validator %v", name, v8, valid[name])
		}
	}
	if n != len(names) {
		t.Errorf("V8 checked %d modules of %d", n, len(names))
	}
	t.Logf("%d modules agree with V8", n)
}

// newerFeature reports whether a validation error is one that WebAssembly
// 3.0, which V8 implements, no longer makes.
func newerFeature(msg string) bool {
	return strings.Contains(msg, "multiple memories") || strings.Contains(msg, "may refer only to imported globals")
}
