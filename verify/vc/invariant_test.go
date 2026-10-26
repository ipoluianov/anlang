package vc_test

import (
	"strings"
	"testing"
)

// TestInvariants checks loop invariants (contract.loop.obligations) with
// sums of elements (spec/24-roadmap.md, step 23).
func TestInvariants(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string // the parts of the diagnostics, in order
	}{
		{"sum", `package main

profile core

import "an/math"

func Sum(xs []i32) i64 {
	ensures result == math.Sum(xs)

	var s i64
	for i := range xs {
		invariant s == math.Sum(xs[:i])
		s += i64(xs[i])
	}
	return s
}

func main() {
	var xs [3]i32
	_ = Sum(xs[:])
}
`, nil},
		{"sum-for", `package main

profile core

import "an/math"

func Sum(xs []int) int {
	ensures result == math.Sum(xs)

	s := 0
	for i := 0; i < len(xs); i++ {
		invariant 0 <= i && i <= len(xs) && s == math.Sum(xs[:i])
		s += xs[i]
	}
	return s
}

func main() {
	var xs [2]int
	_ = Sum(xs[:])
}
`, nil},
		{"sum-value", `package main

profile core

import "an/math"

func Sum(xs []u8) u32 {
	ensures result == math.Sum(xs)

	var s u32
	for i, x := range xs {
		invariant s == math.Sum(xs[:i])
		s += u32(x)
	}
	return s
}

func main() {
	var xs [2]u8
	_ = Sum(xs[:])
}
`, nil},
		{"store", `package main

profile core

import "an/math"

func clear3(xs [4]int) [4]int {
	ensures math.Sum(result) == math.Sum(xs)-xs[3]+7
	xs[3] = 7
	return xs
}

func main() { _ = clear3([4]int{}) }
`, nil},
		{"sum-wrong", `package main

profile core

import "an/math"

func Sum(xs []i32) i64 {
	ensures result == math.Sum(xs)

	var s i64
	for i := range xs {
		invariant s == math.Sum(xs[:i])
		s += i64(xs[i]) + 1
	}
	return s
}

func main() {
	var xs [3]i32
	_ = Sum(xs[:])
}
`, []string{"the invariant s == math.Sum(xs[:i]) after an iteration of the loop is not proven"}},
		{"sum-post", `package main

profile core

import "an/math"

func Sum(xs []i32) i64 {
	ensures result == math.Sum(xs[1:])

	var s i64
	for i := range xs {
		invariant s == math.Sum(xs[:i])
		s += i64(xs[i])
	}
	return s
}

func main() {
	var xs [3]i32
	_ = Sum(xs[:])
}
`, []string{"the postcondition result == math.Sum(xs[1:]) of Sum is not proven"}},
		{"entry", `package main

profile core

func f(n int) int {
	requires n >= 0
	i := 1
	for i <= n {
		invariant i >= 0 && i <= n
		i++
	}
	return i
}

func main() { _ = f(3) }
`, []string{"the invariant i >= 0 && i <= n on entry to the loop is not proven, for example when", "the invariant i >= 0 && i <= n after an iteration of the loop is not proven"}},
		{"contract-only", `package main

profile core

import "an/math"

func main() {
	xs := []int{1}
	_ = math.Sum(xs)
}
`, []string{"math.Sum may appear only in contract expressions and assertions"}},
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
