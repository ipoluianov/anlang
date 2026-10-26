package vc

import (
	"math/big"
	"math/rand"
	"testing"

	"an/syntax"
	"an/types"
	"an/verify"
)

// TestBitwise checks the encodings of the bit-vector operations against
// the operations of Go on 8-bit values: with x and y fixed, the solver
// must prove the result and refute any other.
func TestBitwise(t *testing.T) {
	file, errs := syntax.Parse("main.an", []byte("package main\n\nfunc main() {}\n"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	info, errs := types.Check(&types.Config{Module: "conf", IntBits: 16}, []*syntax.File{file})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	rng := rand.New(rand.NewSource(1))
	type op struct {
		k  syntax.Kind
		fn func(a, b int64) int64
	}
	ops := []op{
		{syntax.And, func(a, b int64) int64 { return a & b }},
		{syntax.Or, func(a, b int64) int64 { return a | b }},
		{syntax.Xor, func(a, b int64) int64 { return a ^ b }},
		{syntax.AndNot, func(a, b int64) int64 { return a &^ b }},
	}
	for _, signed := range []bool{false, true} {
		typ := types.Type(types.Typ[types.U8])
		if signed {
			typ = types.Typ[types.I8]
		}
		for i := 0; i < 60; i++ {
			a, b := int64(rng.Intn(256)), int64(rng.Intn(256))
			if signed {
				a, b = int64(int8(a)), int64(int8(b))
			}
			o := ops[rng.Intn(len(ops))]
			want := o.fn(a, b)
			if signed {
				want = int64(int8(want))
			} else {
				want = int64(uint8(want))
			}
			for _, form := range []int{0, 1, 2} { // variables, x constant, y constant
				f := newFunc(&checker{}, info, nil, nil, "test")
				st := &state{vars: map[*types.Var]val{}}
				x, y := f.newVar("x"), f.newVar("y")
				st.assume(verify.Cmp(x, verify.EQ, verify.Const(a)))
				st.assume(verify.Cmp(y, verify.EQ, verify.Const(b)))
				xt, yt := x, y
				switch form {
				case 1:
					xt = verify.Const(a)
				case 2:
					yt = verify.Const(b)
				}
				r, ok := f.bitwise(st, o.k, xt, yt, typ)
				if !ok {
					t.Fatalf("no encoding of %v", o.k)
				}
				if p, _ := f.prove(st, verify.Cmp(r, verify.EQ, verify.ConstBig(big.NewInt(want))), syntax.Pos{}, 100000); !p {
					t.Fatalf("%d %v %d (signed %v, form %d): the result %d is not proven", a, o.k, b, signed, form, want)
				}
				if p, _ := f.prove(st, verify.Cmp(r, verify.EQ, verify.ConstBig(big.NewInt(want+1))), syntax.Pos{}, 100000); p {
					t.Fatalf("%d %v %d (signed %v, form %d): %d is proven", a, o.k, b, signed, form, want+1)
				}
			}
		}
	}
	// Masks 2^k - 1 and wrapping arithmetic.
	f := newFunc(&checker{}, info, nil, nil, "test")
	for i := 0; i < 200; i++ {
		a := int64(rng.Intn(1 << 16))
		st := &state{vars: map[*types.Var]val{}}
		x := f.newVar("x")
		st.assume(verify.Cmp(x, verify.EQ, verify.Const(a)))
		r, _ := f.bitwise(st, syntax.And, x, verify.Const(0xFF), types.Typ[types.U16])
		if p, _ := f.prove(st, verify.Cmp(r, verify.EQ, verify.Const(a&0xFF)), syntax.Pos{}, 100000); !p {
			t.Fatalf("%d & 0xFF is not proven", a)
		}
		w := f.wrapped(st, x.Scale(big.NewInt(3)), types.Typ[types.WrapU8])
		if p, _ := f.prove(st, verify.Cmp(w, verify.EQ, verify.Const(int64(uint8(3*a)))), syntax.Pos{}, 100000); !p {
			t.Fatalf("3*%d modulo 256 is not proven", a)
		}
	}
}
