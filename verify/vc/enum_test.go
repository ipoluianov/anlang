package vc_test

import (
	"strings"
	"testing"
)

// TestEnums checks the facts of enum values and Option: which variant is
// possible at a point (spec/24-roadmap.md, step 24).
func TestEnums(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string // the parts of the diagnostics, in order
	}{
		{"option", `package main

profile core

func first(xs []int) Option[int] {
	ensures (len(xs) > 0) == (result != None)
	if len(xs) == 0 {
		return None
	}
	return Some(xs[0])
}

func main() {
	var a [3]int
	o := first(a[:])
	v, ok := o.Get()
	assert(ok)
	_ = v
}
`, nil},
		{"switch", `package main

profile core

type Shape enum {
	Circle{r int}
	Rect{w, h int}
	Empty
}

func area(s Shape) int {
	requires s != Empty
	switch s {
	case Circle{r}:
		assert(s != Empty)
		return 3 * r * r
	case Rect{w, h}:
		return w * h
	case Empty:
		panic("unreachable")
	}
}

func main() {
	s := Rect(2, 3)
	switch s {
	case Rect{w, h}:
		assert(w == 2 && h == 3)
	default:
		assert(false)
	}
	_ = area(Circle(1))
}
`, nil},
		{"requires", `package main

profile core

type Mode enum {
	Off
	On
}

func use(m Mode) {
	requires m == On
}

func main() {
	var m Mode
	use(m)
}
`, []string{"the precondition m == On of use at this call is not proven"}},
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
