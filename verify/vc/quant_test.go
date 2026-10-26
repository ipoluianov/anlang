package vc_test

import (
	"strings"
	"testing"
)

// TestQuantifiers checks contracts with bounded quantifiers
// (std.math.quantifier): the set of spec/24-roadmap.md, step 24.
func TestQuantifiers(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string // the parts of the diagnostics, in order
	}{
		{"find", `package main

profile core

import "an/math"

func find(xs []int, x int) int {
	ensures result == -1 || 0 <= result && result < len(xs) && xs[result] == x
	ensures result != -1 || math.ForAll(0, len(xs), func(j int) bool { return xs[j] != x })

	for i := range xs {
		invariant math.ForAll(0, i, func(j int) bool { return xs[j] != x })
		if xs[i] == x {
			return i
		}
	}
	return -1
}

func main() {
	var a [4]int
	_ = find(a[:], 3)
}
`, nil},
		{"fill", `package main

profile core

import "an/math"

func fill(xs [8]int, v int) [8]int {
	ensures math.ForAll(0, 8, func(j int) bool { return result[j] == v })

	for i := range xs {
		invariant math.ForAll(0, i, func(j int) bool { return xs[j] == v })
		xs[i] = v
	}
	return xs
}

func main() { _ = fill([8]int{}, 1) }
`, nil},
		{"max", `package main

profile core

import "an/math"

func max(xs []int) int {
	requires len(xs) > 0
	ensures math.ForAll(0, len(xs), func(j int) bool { return xs[j] <= result })
	ensures math.Exists(0, len(xs), func(j int) bool { return xs[j] == result })

	m := xs[0]
	for i := range xs {
		invariant math.ForAll(0, i, func(j int) bool { return xs[j] <= m })
		invariant math.Exists(0, len(xs), func(j int) bool { return xs[j] == m })
		if xs[i] > m {
			m = xs[i]
		}
	}
	return m
}

func main() {
	var a [3]int
	_ = max(a[:])
}
`, nil},
		{"sorted", `package main

profile core

import "an/math"

func check(xs []int) {
	requires len(xs) >= 3
	requires math.ForAll(0, len(xs)-1, func(i int) bool { return xs[i] <= xs[i+1] })
	assert(xs[0] <= xs[2])
}

func main() {
	a := [3]int{1, 2, 3}
	check(a[:])
}
`, nil},
		{"wrong", `package main

profile core

import "an/math"

func positive(xs []int) bool {
	ensures !result || math.ForAll(0, len(xs), func(j int) bool { return xs[j] > 0 })

	for i := range xs {
		invariant math.ForAll(0, i, func(j int) bool { return xs[j] > 0 })
		if xs[i] < 0 {
			return false
		}
	}
	return true
}

func main() {
	var a [2]int
	_ = positive(a[:])
}
`, []string{"the invariant math.ForAll(0, i, func literal) after an iteration of the loop is not proven, for example when i = 1"}},
	} {
		_, errs := checkSource(t, tc.src)
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
