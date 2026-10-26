package wasm

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSpec runs the WebAssembly 2.0 specification test suite, downloaded
// by scripts/fetch-wasm-testsuite.sh (conf.oracle.use).
func TestSpec(t *testing.T) {
	dir := filepath.Join("..", "..", "external", "wasm", "spec", "test", "core")
	files, _ := filepath.Glob(filepath.Join(dir, "*.wast"))
	simd, _ := filepath.Glob(filepath.Join(dir, "simd", "*.wast"))
	files = append(files, simd...)
	if len(files) == 0 {
		t.Skip("the test suite is not downloaded: run scripts/fetch-wasm-testsuite.sh")
	}
	sort.Strings(files)
	only := os.Getenv("WAST")
	total, failed, nfiles := 0, 0, 0
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".wast")
		if only != "" && name != only {
			continue
		}
		nfiles++
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := NewScript()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic: %v", name, r)
				}
			}()
			if err := s.Run(string(src)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}()
		total += s.Passed + len(s.Failures)
		failed += len(s.Failures)
		if len(s.Failures) > 0 {
			msgs := s.Failures
			if len(msgs) > 5 && os.Getenv("WAST") == "" {
				msgs = msgs[:5]
			}
			t.Errorf("%s: %d of %d commands fail:\n\t%s", name, len(s.Failures), s.Passed+len(s.Failures), strings.Join(msgs, "\n\t"))
		}
	}
	t.Logf("%d files: %d of %d commands pass", nfiles, total-failed, total)
}
