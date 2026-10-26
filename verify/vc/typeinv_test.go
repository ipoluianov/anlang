package vc_test

import (
	"strings"
	"testing"
)

// TestTypeInvariants checks the invariants of struct types at their
// boundaries (contract.type.boundaries) and the types without a zero
// value (contract.type.zero).
func TestTypeInvariants(t *testing.T) {
	const decl = `package main

profile core

type Range struct {
	invariant lo <= hi
	lo, hi int
}

`
	for _, tc := range []struct {
		name, src string
		want      []string // the parts of the diagnostics, in order
	}{
		{"ok", decl + `func (r inout Range) Shift(d int) {
	r.lo += d
	r.hi += d
}

func width(r Range) int {
	ensures result >= 0
	return r.hi - r.lo
}

func make2(a, b int) Range {
	if a <= b {
		return Range{lo: a, hi: b}
	}
	return Range{b, a}
}

func main() {
	var r Range
	r.Shift(3)
	s := make2(5, 2)
	s.hi = s.lo + width(r)
	_ = width(s)
}
`, nil},
		{"inout", decl + `func (r inout Range) Widen(d int) {
	r.lo += d
}

func main() {
	var r Range
	r.Widen(1)
}
`, []string{"the invariant lo <= hi of Range of inout parameter r at the end of Widen is not proven"}},
		{"literal", decl + `func main() {
	r := Range{lo: 3, hi: 1}
	_ = r
}
`, []string{"the invariant lo <= hi of Range for this composite literal is not proven"}},
		{"argument", decl + `func use(r Range) int { return r.lo }

func main() {
	r := Range{1, 2}
	r.lo = 5
	_ = use(r)
}
`, []string{"the invariant lo <= hi of Range of the argument at this call is not proven"}},
		{"result", decl + `func bad(a int) Range {
	r := Range{a, a}
	r.hi--
	return r
}

func main() { _ = bad(1) }
`, []string{"the invariant lo <= hi of Range of the result is not proven"}},
		{"zero", `package main

profile core

type Pos struct {
	invariant v > 0
	v int
}

type Pair struct {
	a, b Pos
}

func main() {
	var p Pos
	q := Pair{a: Pos{1}}
	_, _ = p, q
}
`, []string{"variable p of type Pos needs a value", "field b omitted from the literal of type Pos needs a value"}},
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

// TestFieldContracts checks fields of structs in contract expressions.
func TestFieldContracts(t *testing.T) {
	src := `package main

profile core

type Range struct {
	invariant lo <= hi
	lo, hi int
}

func (r inout Range) Grow(d int) {
	requires d >= 0 && r.hi <= 1000 && d <= 1000
	ensures r.hi >= r.lo+d
	r.hi += d
}

func main() {
	r := Range{1, 2}
	r.Grow(3)
	assert(r.hi >= r.lo+3)
}
`
	_, errs := checkSource(t, src)
	if len(errs) > 0 {
		t.Error(errs)
	}
}
