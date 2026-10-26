package build

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"an/effects"
)

// programString writes the summaries and execution effects of every
// function of a program, for comparisons.
func programString(prog *effects.Program) string {
	var b strings.Builder
	for _, pkg := range prog.Packages {
		b.WriteString(pkg.String())
	}
	for _, k := range prog.Keys() {
		b.WriteString("exec " + k + ":")
		var names []string
		for e := range prog.Exec(k) {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		b.WriteString(" " + strings.Join(names, " "))
		b.WriteString("\n")
	}
	return b.String()
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestEffectsIncremental checks the acceptance criterion of roadmap step
// 8: after each change to one package, recomputing the effects with the
// cached results of unchanged packages gives the same result as a full
// recomputation, and only the changed package and the packages that
// depend on it are analyzed again (eff.infer.modular).
func TestEffectsIncremental(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"anlang.mod": "module  app\nanlang  1.0\ntargets linux-x86_64\n",
		"main.an": `package main

import (
	"app/report"
	"app/util"
)

func main() {
	report.Print(util.Double(2))
}
`,
		"report/report.an": `package report

import (
	"an/fmt"
	"app/store"
)

// Print prints n and saves it.
func Print(n int) {
	fmt.Println(n)
	store.Save(n)
}
`,
		"store/store.an": `package store

// Save saves n.
func Save(n int) {
	_ = n
}
`,
		"util/util.an": `package util

// Double doubles n.
func Double(n int) int {
	return n * 2
}
`,
	})
	p, errs := Load(root)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	cache := effects.NewCache()
	check := func(step string, wantMisses int) {
		t.Helper()
		before := cache.Misses
		inc, errs := p.Effects("linux-x86_64", cache)
		if len(errs) > 0 {
			t.Fatalf("%s: %v", step, errs)
		}
		full, errs := p.Effects("linux-x86_64", nil)
		if len(errs) > 0 {
			t.Fatalf("%s: %v", step, errs)
		}
		if got, want := programString(inc), programString(full); got != want {
			t.Errorf("%s: incremental result differs from the full one:\n%s\nwant:\n%s", step, got, want)
		}
		if got := cache.Misses - before; got != wantMisses {
			t.Errorf("%s: %d packages analyzed, want %d", step, got, wantMisses)
		}
	}
	check("first build", 4)
	check("no change", 0)

	// A change in store gives Save an effect; report and main depend on
	// it, util does not.
	writeFiles(t, root, map[string]string{"store/store.an": `package store

import "an/fmt"

// Save saves n.
func Save(n int) {
	fmt.Println("saving", n)
}
`})
	check("store changed", 3)
	full, _ := p.Effects("linux-x86_64", nil)
	if s := full.Summary("app.main"); s == nil || !strings.Contains(s.String(), "Console") {
		t.Errorf("main does not get Console from store.Save: %v", s)
	}

	// A change in util only.
	writeFiles(t, root, map[string]string{"util/util.an": `package util

// Double doubles n.
func Double(n int) int {
	return n + n
}
`})
	check("util changed", 2)

	// Back to the first version of store: the cached result is reused.
	writeFiles(t, root, map[string]string{"store/store.an": `package store

// Save saves n.
func Save(n int) {
	_ = n
}
`})
	check("store restored", 1) // store and report are found in the cache
}
