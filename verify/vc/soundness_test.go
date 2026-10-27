package vc_test

import (
	"strings"
	"testing"

	"an/build"
	"an/syntax"
)

// TestSoundness keeps the faults of the verification that the review of
// step 24 and the differential test (conf.solver.vc) found: each
// assertion or postcondition here does not hold for some input, and the
// verification once proved it.
func TestSoundness(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      string // the diagnostic that rejects the false claim
	}{
		{"loop-element", `package main

profile core

func f(n int) {
	var xs [4]int
	xs[0] = 5
	for i := range n {
		_ = i
		xs[0] = 7
	}
	assert(xs[0] == 5)
}

func main() { f(1) }
`, "the assertion xs[0] == 5 is not proven"},
		{"loop-field", `package main

profile core

type S struct {
	a int
}

func f(n int) {
	var s S
	for i := range n {
		_ = i
		s.a = 7
	}
	assert(s.a == 0)
}

func main() { f(1) }
`, "the assertion s.a == 0 is not proven"},
		{"range-copy", `package main

profile core

func f(xs [4]int) {
	for i, x := range xs {
		assert(x == xs[i])
		xs[2] = -5
	}
}

func main() { f([4]int{1, 2, 3, 4}) }
`, "the assertion x == xs[i] is not proven"},
		{"inout-subslice", `package main

func g(s inout []int) { s[0] = 9 }

func f() {
	xs := [3]int{1, 2, 3}
	g(xs[1:3])
	assert(xs[1] == 2)
}

func main() { f() }
`, "the assertion xs[1] == 2 is not proven"},
		{"inout-subslice-loop", `package main

func g(s inout []int) { s[0] = 9 }

func f(n int) {
	xs := [3]int{1, 2, 3}
	for i := range n {
		_ = i
		g(xs[1:3])
	}
	assert(xs[1] == 2)
}

func main() { f(1) }
`, "the assertion xs[1] == 2 is not proven"},
		{"copy", `package main

func f(a [2]int, b [2]int) {
	s := a[:]
	assert(s[0] == a[0])
	copy(s, b[:])
	assert(s[0] == a[0])
}

func main() { f([2]int{1, 2}, [2]int{3, 4}) }
`, "the assertion s[0] == a[0] is not proven"},
		{"range-negative", `package main

profile core

func f(n int) {
	for i := range n {
		_ = i
	}
	assert(n >= 0)
}

func main() { f(-3) }
`, "the assertion n >= 0 is not proven"},
		{"fallthrough", `package main

profile core

func f(n int) {
	switch n {
	case 1:
		fallthrough
	case 2:
		assert(n == 2)
	}
}

func main() { f(1) }
`, "the assertion n == 2 is not proven"},
		{"string-range", `package main

profile core

func f(s string) {
	for i, r := range s {
		assert(r == i32(s[i]))
	}
}

func main() { f("é") }
`, "the assertion r == i32(s[i]) is not proven"},
		{"defer-result", `package main

profile core

func set(x inout int) { x = 5 }

func f() (r int) {
	ensures r == 0
	defer set(r)
	return 0
}

func main() { _ = f() }
`, "the postcondition r == 0 of f is not proven"},
		{"defer-inout", `package main

profile core

func set(x inout int) { x = 5 }

func f(x inout int) {
	ensures x == 0
	defer set(x)
	x = 0
}

func main() {
	v := 1
	f(v)
}
`, "the postcondition x == 0 of f is not proven"},
		{"inout-loop-invariant", `package main

profile core

type Span struct {
	invariant lo <= hi
	lo, hi int
}

type Spans struct {
	s [2]Span
}

func (t inout Spans) All() func(yield func(inout Span) bool) {
	return func(yield func(inout Span) bool) {
		for i := range t.s {
			if !yield(t.s[i]) {
				return
			}
		}
	}
}

func main() {
	var t Spans
	for v := range t.All() {
		if v.hi < 100 {
			v.lo = v.hi + 1
		}
	}
}
`, "the invariant lo <= hi of Span of inout loop variable v at the end of an iteration is not proven"},
	} {
		var errs []*syntax.Error
		if strings.Contains(tc.src, "profile core") {
			_, errs = checkSource(t, tc.src)
		} else {
			// Stores into the elements of slices need the profile app.
			tg, _ := build.LookupTarget("linux-x86_64")
			_, errs = build.CheckSources("conf", tg, []build.Source{{Name: "main.an", Data: []byte(tc.src)}})
		}
		found := false
		for _, e := range errs {
			found = found || strings.Contains(e.Error(), tc.want)
		}
		if !found {
			t.Errorf("%s: diagnostics %q, want %q", tc.name, errs, tc.want)
		}
	}
}
