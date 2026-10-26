package vc_test

import (
	"testing"

	"an/syntax"
	"an/types"
)

// provenArith returns, for each arithmetic node of function name, its
// source text and whether its overflow check is proven.
func provenArith(info *types.Info, name string) map[string]bool {
	out := map[string]bool{}
	for _, f := range info.Funcs {
		if f.Name() != name || f.Decl.Body == nil {
			continue
		}
		syntax.Walk(f.Decl.Body, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.IncDecStmt:
				out[types.ExprString(n.X)+"++"] = info.Proven[n]
			case *syntax.AssignStmt:
				if n.Tok != syntax.Assign && n.Tok != syntax.Define {
					out[types.ExprString(n.Lhs[0])+" op= "+types.ExprString(n.Rhs[0])] = info.Proven[n]
				}
			case *syntax.BinaryExpr:
				switch n.Op {
				case syntax.Add, syntax.Sub, syntax.Mul:
					if !types.IsWrap(info.Types[n].Type) {
						// The operations of wrap types have no checks.
						out[types.ExprString(n)] = info.Proven[n]
					}
				}
			}
			return true
		})
	}
	return out
}

// TestLoopOverflow checks that the overflow checks of the counters and
// the index arithmetic of typical loops are proven (spec/24-roadmap.md,
// step 22).
func TestLoopOverflow(t *testing.T) {
	src := `package main

profile core

import "an/wrap"

func loops(xs []u8, n u16) u32 {
	var h wrap.U32 = 2166136261
	for i := 0; i < len(xs); i++ {
		h = (h ^ wrap.U32(xs[i])) * 16777619
	}
	var c u16
	for c = 0; c < n; c++ {
	}
	for k := n; k > 0; k-- {
	}
	m := 0
	for i := 0; i+1 < len(xs); i += 2 {
		m = int(xs[i]) + int(xs[i+1])
	}
	low := u8(h & 0xFF)
	top := u8(h >> 24)
	mid := u16((h >> 8) & 0xFFFF)
	_ = m
	return u32(low) + u32(top) + u32(mid) + u32(c)
}

func main() {
	var a [4]u8
	_ = loops(a[:], 3)
}
`
	info, errs := checkSource(t, src)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	for e, ok := range provenArith(info, "loops") {
		if !ok {
			t.Errorf("loops: the overflow check of %s is not proven", e)
		}
	}
}
