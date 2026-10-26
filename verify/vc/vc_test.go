package vc_test

import (
	"strings"
	"testing"

	"an/build"
	"an/syntax"
	"an/types"
)

func checkSource(t *testing.T, src string) (*types.Info, []*syntax.Error) {
	t.Helper()
	tg, _ := build.LookupTarget("dos-com")
	return build.CheckSources("conf", tg, []build.Source{{Name: "main.an", Data: []byte(src)}})
}

// provenIndexes returns, for each index expression of function name, its
// source text and whether its bounds check is proven.
func provenIndexes(info *types.Info, name string) map[string]bool {
	out := map[string]bool{}
	for _, f := range info.Funcs {
		if f.Name() != name || f.Decl.Body == nil {
			continue
		}
		syntax.Walk(f.Decl.Body, func(n syntax.Node) bool {
			if ix, ok := n.(*syntax.IndexExpr); ok {
				out[types.ExprString(ix)] = info.Proven[ix]
			}
			return true
		})
	}
	return out
}

// TestTypicalLoops checks that the bounds checks of the typical loops
// over slices and arrays are proven (spec/24-roadmap.md, step 21).
func TestTypicalLoops(t *testing.T) {
	src := `package main

profile core

func ranges(xs []int, a [8]u8) int {
	s := 0
	for i := range xs {
		s = xs[i]
	}
	for i, x := range xs {
		s = xs[i] + x - x
	}
	for i := 0; i < len(xs); i++ {
		s = xs[i]
	}
	for i := len(xs) - 1; i >= 0; i-- {
		s = xs[i]
	}
	for i := range 8 {
		s = int(a[i])
	}
	for i := 0; i+1 < len(xs); i += 2 {
		s = xs[i] + xs[i+1]
	}
	if len(xs) > 3 {
		s = xs[3]
	}
	return s
}

func unknown(xs []int, i int, j int) int {
	s := xs[i]
	for k := 0; k <= len(xs); k++ {
		s = xs[k]
	}
	return s + xs[j]
}

func main() {
	var a [8]u8
	var b [4]int
	_ = ranges(b[:], a) + unknown(b[:], 1, 2)
}
`
	info, errs := checkSource(t, src)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	for e, ok := range provenIndexes(info, "ranges") {
		if !ok {
			t.Errorf("ranges: the bounds check of %s is not proven", e)
		}
	}
	for e, ok := range provenIndexes(info, "unknown") {
		if ok {
			t.Errorf("unknown: the bounds check of %s is proven, but it may fail", e)
		}
	}
}

// TestContracts checks contracts that hold and that do not, with the
// counterexamples in the diagnostics.
func TestContracts(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string // the parts of the diagnostics, in order
	}{
		{"ok", `package main

profile core

func clamp(x, lo, hi int) int {
	requires lo <= hi
	ensures lo <= result && result <= hi
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func half(n int) int {
	requires n >= 0
	ensures 2*result <= n && n <= 2*result+1
	return n / 2
}

func main() {
	c := clamp(17, 0, 10)
	assert(c <= 10)
	h := half(c)
	assert(h <= 5)
	var k int = 3
	k += h
	assert(k >= 3 && k <= 8)
}
`, nil},
		{"bitwise", `package main

profile core

func low(x u16) u8 {
	requires x&0xFF00 == 0
	ensures u16(result) == x
	return u8(x)
}

func field(r u32) u32 {
	ensures result <= 3 && result == (r>>4)&3
	return (r >> 4) & 3
}

func flags(x u8) u8 {
	ensures result&1 == 0 && result >= 0x80
	return x&^1 | 0x80
}

func main() {
	_ = low(0x7F)
	_ = low(u16(field(0x30)) << 2)
	_ = flags(1)
}
`, nil},
		{"bitwise-requires", `package main

profile core

func low(x u16) u8 {
	requires x&0xFF00 == 0
	return u8(x)
}

func main() { _ = low(0x100) }
`, []string{"precondition x & 0xFF00 == 0 of low at this call is not proven"}},
		{"requires", `package main

profile core

func f(n int) int {
	requires n > 0
	return n
}

func main() {
	x := 0
	_ = f(x)
}
`, []string{"precondition n > 0 of f at this call is not proven, for example when n = 0"}},
		{"ensures", `package main

profile core

func f(n int) int {
	requires n >= 0
	ensures result > n
	if n > 10 {
		return n
	}
	return n + 1
}

func main() { _ = f(1) }
`, []string{"postcondition result > n of f is not proven, for example when result = 11, n = 11"}},
		{"assert", `package main

profile core

func main() {
	for i := range 10 {
		assert(i < 9)
	}
}
`, []string{"assertion i < 9 is not proven, for example when i = 9"}},
		{"indirect", `package main

profile core

func f(n int) int {
	requires n > 0
	return n
}

func main() {
	g := f
	_ = g
}
`, []string{"f has preconditions and cannot be used as a function value"}},
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
