package vc_test

import (
	"strings"
	"testing"

	"an/build"
	"an/syntax"
)

// TestOld checks the values at entry in contracts (contract.expr.old).
func TestOld(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string // the parts of the diagnostics, in order
	}{
		{"swap", `package main

profile core

func swap(a, b inout int) {
	ensures a == old(b) && b == old(a)

	a, b = b, a
}

func main() {
	x, y := 1, 2
	swap(x, y)
	assert(x == 2 && y == 1)
}
`, nil},
		{"field", `package main

profile core

type S struct {
	a, b int
}

func bump(s inout S) {
	requires s.a < 100
	ensures s.a == old(s.a)+1 && s.b == old(s).b

	s.a++
}

func main() {
	s := S{a: 5, b: 7}
	bump(s)
	assert(s.a == 6 && s.b == 7)
}
`, nil},
		{"param", `package main

profile core

func f(x int) int {
	ensures result == old(x)+1
	requires x < 100

	y := x + 1
	x = 0
	return y + x
}

func main() {
	y := f(3)
	assert(y == 4)
}
`, nil},
		{"element", `package main

func inc(xs inout []int) {
	requires len(xs) > 1 && xs[0] < 100
	ensures xs[0] == old(xs[0])+1 && xs[1] == old(xs)[1]

	xs[0]++
}

func main() {
	a := []int{1, 2, 3}
	inc(a)
	assert(a[0] == 2 && a[1] == 2)
}
`, nil},
		{"loop", `package main

import "an/math"

func add(xs inout []int) {
	requires math.ForAll(0, len(xs), func(i int) bool { return xs[i] < 100 })
	ensures math.ForAll(0, len(xs), func(i int) bool { return xs[i] == old(xs[i])+1 })

	for i := range xs {
		invariant len(xs) == len(old(xs))
		invariant math.ForAll(0, i, func(j int) bool { return xs[j] == old(xs[j])+1 })
		invariant math.ForAll(i, len(xs), func(j int) bool { return xs[j] == old(xs[j]) })
		xs[i]++
	}
}

func main() {
	a := []int{1, 2}
	add(a)
}
`, nil},
		{"assert", `package main

profile core

func f(n inout int) {
	requires n < 100
	n++
	assert(n == old(n)+1)
}

func main() {
	x := 1
	f(x)
}
`, nil},
		{"wrong", `package main

profile core

func bump(n inout int) {
	requires n < 100
	ensures n == old(n)

	n++
}

func main() {
	x := 1
	bump(x)
}
`, []string{"the postcondition n == old(n) of bump is not proven"}},
		{"wrong-call", `package main

profile core

func bump(n inout int) {
	requires n < 100
	ensures n > old(n)

	n += 2
}

func main() {
	x := 1
	bump(x)
	assert(x == 2)
}
`, []string{"the assertion x == 2 is not proven"}},
		{"misplaced", `package main

profile core

func f(n inout int) int {
	requires old(n) > 0
	ensures result == old(result)
	ensures old(old(n)) == n

	m := n
	assert(old(m) == m)
	return m
}

func main() {
	x := 1
	_ = f(x)
	_ = old(x)
}
`, []string{
			"old may appear only in ensures clauses",
			"the argument of old may refer only to parameters and the receiver, not to result",
			"old may appear only in ensures clauses",
			"the argument of old may refer only to parameters and the receiver, not to m",
			"old may appear only in ensures clauses",
		}},
	} {
		var errs []*syntax.Error
		if strings.Contains(tc.src, "profile core") {
			_, errs = checkSource(t, tc.src)
		} else {
			// Stores into the elements of slices need the profile app.
			tg, _ := build.LookupTarget("linux-x86_64")
			_, errs = build.CheckSources("conf", tg, []build.Source{{Name: "main.an", Data: []byte(tc.src)}})
		}
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		if len(msgs) != len(tc.want) {
			t.Errorf("%s: diagnostics %q, want %d", tc.name, msgs, len(tc.want))
			continue
		}
		for i, w := range tc.want {
			if !strings.Contains(msgs[i], w) {
				t.Errorf("%s: %q, want %q", tc.name, msgs[i], w)
			}
		}
	}
}
