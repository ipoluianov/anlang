package conformance

import (
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"an/build"
	"an/interp"
	"an/lower"
)

// The arithmetic oracle (spec/24-roadmap.md, step 7): for every integer
// type and operator, on boundary and random operands, the IR interpreter
// must panic exactly when the mathematical result is not representable
// (expr.int.checked, expr.int.div), compute wrapping types as Go
// computes its integer types (expr.wrap.ops, expr.wrap.other-ops), and
// compute the functions of an/bits from the mathematical result
// (expr.bits.functions).

type intType struct {
	name   string
	bits   int
	signed bool
}

var checkedTypes = []intType{
	{"i8", 8, true}, {"i16", 16, true}, {"i32", 32, true}, {"i64", 64, true}, {"int", 64, true},
	{"u8", 8, false}, {"u16", 16, false}, {"u32", 32, false}, {"u64", 64, false}, {"uint", 64, false},
}

var wrapTypes = []intType{
	{"wrap.I8", 8, true}, {"wrap.I16", 16, true}, {"wrap.I32", 32, true}, {"wrap.I64", 64, true},
	{"wrap.U8", 8, false}, {"wrap.U16", 16, false}, {"wrap.U32", 32, false}, {"wrap.U64", 64, false},
}

func (t intType) min() *big.Int {
	if !t.signed {
		return big.NewInt(0)
	}
	return new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), uint(t.bits-1)))
}

func (t intType) max() *big.Int {
	n := t.bits
	if t.signed {
		n--
	}
	return new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(n)), big.NewInt(1))
}

func (t intType) fits(x *big.Int) bool { return x.Cmp(t.min()) >= 0 && x.Cmp(t.max()) <= 0 }

// wrapBig reduces x modulo 2ⁿ into the range of t.
func (t intType) wrapBig(x *big.Int) *big.Int {
	mod := new(big.Int).Lsh(big.NewInt(1), uint(t.bits))
	r := new(big.Int).Mod(x, mod)
	if r.Cmp(t.max()) > 0 {
		r.Sub(r, mod)
	}
	return r
}

// operands returns boundary values and random values of t.
func (t intType) operands(rng *rand.Rand) []*big.Int {
	lo, hi := t.min(), t.max()
	one := big.NewInt(1)
	vals := []*big.Int{big.NewInt(0), big.NewInt(1), big.NewInt(2), lo, new(big.Int).Add(lo, one), hi,
		new(big.Int).Sub(hi, one), new(big.Int).Rsh(hi, 1), new(big.Int).Add(new(big.Int).Rsh(hi, 1), one)}
	if t.signed {
		vals = append(vals, big.NewInt(-1), big.NewInt(-2), new(big.Int).Rsh(lo, 1))
	}
	span := new(big.Int).Add(new(big.Int).Sub(hi, lo), one)
	for range 4 {
		vals = append(vals, new(big.Int).Add(lo, new(big.Int).Rand(rng, span)))
	}
	return vals
}

// goWrap computes x op y with Go's integer type of the size and
// signedness of t, whose arithmetic wraps.
func goWrap(t intType, op string, x, y *big.Int) *big.Int {
	if t.signed {
		a, b := x.Int64(), y.Int64()
		var r int64
		switch t.bits {
		case 8:
			r = int64(goOpS(int8(a), int8(b), op))
		case 16:
			r = int64(goOpS(int16(a), int16(b), op))
		case 32:
			r = int64(goOpS(int32(a), int32(b), op))
		default:
			r = goOpS(a, b, op)
		}
		return big.NewInt(r)
	}
	a, b := x.Uint64(), y.Uint64()
	var r uint64
	switch t.bits {
	case 8:
		r = uint64(goOpU(uint8(a), uint8(b), op))
	case 16:
		r = uint64(goOpU(uint16(a), uint16(b), op))
	case 32:
		r = uint64(goOpU(uint32(a), uint32(b), op))
	default:
		r = goOpU(a, b, op)
	}
	return new(big.Int).SetUint64(r)
}

func goOpS[T int8 | int16 | int32 | int64](a, b T, op string) T {
	switch op {
	case "+":
		return a + b
	case "-":
		return a - b
	case "*":
		return a * b
	case "/":
		return a / b
	case "%":
		return a % b
	case "neg":
		return -a
	case "<<":
		return a << uint64(b)
	case ">>":
		return a >> uint64(b)
	}
	panic(op)
}

func goOpU[T uint8 | uint16 | uint32 | uint64](a, b T, op string) T {
	switch op {
	case "+":
		return a + b
	case "-":
		return a - b
	case "*":
		return a * b
	case "/":
		return a / b
	case "%":
		return a % b
	case "neg":
		return -a
	case "<<":
		return a << uint64(b)
	case ">>":
		return a >> uint64(b)
	}
	panic(op)
}

// mathOp returns the mathematical result of x op y; Go's / and % on
// big.Int with Quo and Rem truncate toward zero, as Anlang does.
func mathOp(op string, x, y *big.Int) *big.Int {
	r := new(big.Int)
	switch op {
	case "+":
		return r.Add(x, y)
	case "-":
		return r.Sub(x, y)
	case "*":
		return r.Mul(x, y)
	case "/":
		return r.Quo(x, y)
	case "%":
		return r.Rem(x, y)
	case "neg":
		return r.Neg(x)
	}
	panic(op)
}

// arithProgram is a generated program with its expected output.
type arithProgram struct {
	name string
	src  strings.Builder
	want strings.Builder
}

func newArithProgram(name string, imports ...string) *arithProgram {
	p := &arithProgram{name: name}
	imports = append(imports, "an/fmt")
	sort.Strings(imports)
	p.src.WriteString("package main\n\nimport (\n")
	for _, imp := range imports {
		fmt.Fprintf(&p.src, "\t%q\n", imp)
	}
	p.src.WriteString(")\n\n")
	return p
}

func (p *arithProgram) run(t *testing.T) {
	t.Helper()
	target, _ := build.LookupTarget(suiteTarget)
	src := p.src.String()
	info, diags := build.CheckSources(suiteModule, target, []build.Source{{Name: p.name + ".an", Data: []byte(src)}})
	if len(diags) > 0 {
		t.Fatalf("%s: %v\n%s", p.name, diags, src)
	}
	prog, errs := lower.Program(info)
	if len(errs) > 0 {
		t.Fatalf("%s: lowering: %v", p.name, errs)
	}
	prog.IntBits = 64
	var out strings.Builder
	if err := interp.Run(prog, interp.Options{Stdout: &out, Stderr: &out}); err != nil {
		out.WriteString("exit status 2\n")
	}
	got, want := strings.Split(out.String(), "\n"), strings.Split(p.want.String(), "\n")
	for i := range max(len(got), len(want)) {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Errorf("%s: line %d: got %q, want %q", p.name, i+1, g, w)
			return
		}
	}
}

// literal writes x so that it converts to type t.
func literal(t intType, x *big.Int) string {
	if strings.HasPrefix(t.name, "wrap.") {
		return t.name + "(" + x.String() + ")"
	}
	return x.String()
}

func TestArithmeticOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	binOps := []string{"+", "-", "*", "/", "%"}

	for _, typ := range checkedTypes {
		vals := typ.operands(rng)
		// Operations whose result is representable, in one program.
		p := newArithProgram("checked_" + typ.name)
		fmt.Fprintf(&p.src, "func main() {\n\txs := []%s{", typ.name)
		for i, v := range vals {
			if i > 0 {
				p.src.WriteString(", ")
			}
			p.src.WriteString(v.String())
		}
		p.src.WriteString("}\n\tcs := []uint{0, 1, 7, 8, 31, 32, 63, 64}\n")
		type failure struct{ expr, msg string }
		var failures []failure
		for i, x := range vals {
			for j, y := range vals {
				for _, op := range binOps {
					e := fmt.Sprintf("xs[%d] %s xs[%d]", i, op, j)
					if (op == "/" || op == "%") && y.Sign() == 0 {
						failures = append(failures, failure{e, "integer divide by zero"})
						continue
					}
					r := mathOp(op, x, y)
					if op == "%" && typ.fits(mathOp("/", x, y)) || op != "%" && typ.fits(r) {
						fmt.Fprintf(&p.src, "\tfmt.Println(%s)\n", e)
						fmt.Fprintf(&p.want, "%s\n", r)
					} else {
						failures = append(failures, failure{e, "integer overflow"})
					}
				}
			}
			e := fmt.Sprintf("-xs[%d]", i)
			if r := mathOp("neg", x, nil); typ.fits(r) {
				fmt.Fprintf(&p.src, "\tfmt.Println(%s)\n", e)
				fmt.Fprintf(&p.want, "%s\n", r)
			} else {
				failures = append(failures, failure{e, "integer overflow"})
			}
			// Shifts discard the bits shifted out (expr.int.shift).
			for k, c := range []int64{0, 1, 7, 8, 31, 32, 63, 64} {
				for _, op := range []string{"<<", ">>"} {
					fmt.Fprintf(&p.src, "\tfmt.Println(xs[%d] %s cs[%d])\n", i, op, k)
					fmt.Fprintf(&p.want, "%s\n", goWrap(typ, op, x, big.NewInt(c)))
				}
			}
		}
		// Float-to-integer conversions truncate toward zero and panic
		// when the result is not representable (type.conv.float-int).
		maxF, _ := new(big.Float).SetInt(typ.max()).Float64()
		minF, _ := new(big.Float).SetInt(typ.min()).Float64()
		for _, f := range []float64{0, 0.9, -0.9, 1.5, -2.5, 100.7, maxF, minF, maxF * 2, minF - 1e6, minF * 2} {
			e := fmt.Sprintf("%s([]f64{%v}[0])", typ.name, f)
			tr, _ := new(big.Float).SetFloat64(f).Int(nil) // truncates toward zero
			if typ.fits(tr) {
				fmt.Fprintf(&p.src, "\tfmt.Println(%s)\n", e)
				fmt.Fprintf(&p.want, "%s\n", tr)
			} else {
				failures = append(failures, failure{"conv " + e, "integer overflow"})
			}
		}
		p.src.WriteString("}\n")
		p.run(t)

		// Each failing operation panics, in a program of its own; a few
		// per operator.
		count := map[string]int{}
		for _, f := range failures {
			op := strings.Fields(strings.TrimPrefix(f.expr, "-"))[0]
			if len(strings.Fields(f.expr)) > 1 {
				op = strings.Fields(f.expr)[1]
			}
			key := op + f.msg
			if count[key] >= 3 {
				continue
			}
			count[key]++
			p := newArithProgram(fmt.Sprintf("panic_%s_%d", typ.name, len(count)))
			fmt.Fprintf(&p.src, "func main() {\n\txs := []%s{", typ.name)
			for i, v := range vals {
				if i > 0 {
					p.src.WriteString(", ")
				}
				p.src.WriteString(v.String())
			}
			fmt.Fprintf(&p.src, "}\n\t_ = xs\n\tfmt.Println(%s)\n}\n", strings.TrimPrefix(f.expr, "conv "))
			fmt.Fprintf(&p.want, "panic: runtime error: %s\nexit status 2\n", f.msg)
			p.run(t)
		}

		// The functions of an/bits.
		p = newArithProgram("bits_"+typ.name, "an/bits")
		fmt.Fprintf(&p.src, "func main() {\n\txs := []%s{", typ.name)
		for i, v := range vals {
			if i > 0 {
				p.src.WriteString(", ")
			}
			p.src.WriteString(v.String())
		}
		p.src.WriteString("}\n")
		for i, x := range vals {
			for j, y := range vals {
				for _, op := range []string{"Add", "Sub", "Mul"} {
					sym := map[string]string{"Add": "+", "Sub": "-", "Mul": "*"}[op]
					r := mathOp(sym, x, y)
					sat := r
					if r.Cmp(typ.min()) < 0 {
						sat = typ.min()
					} else if r.Cmp(typ.max()) > 0 {
						sat = typ.max()
					}
					fmt.Fprintf(&p.src, "\tfmt.Println(bits.Wrapping%s(xs[%d], xs[%d]), bits.Saturating%s(xs[%d], xs[%d]))\n", op, i, j, op, i, j)
					fmt.Fprintf(&p.want, "%s %s\n", goWrap(typ, sym, x, y), sat)
					fmt.Fprintf(&p.src, "\tfmt.Println(bits.Checked%s(xs[%d], xs[%d]))\n", op, i, j)
					if typ.fits(r) {
						fmt.Fprintf(&p.want, "%s true\n", r)
					} else {
						p.want.WriteString("0 false\n")
					}
				}
			}
		}
		p.src.WriteString("}\n")
		p.run(t)
	}

	// Wrapping types compute as Go's integer types.
	for _, typ := range wrapTypes {
		vals := typ.operands(rng)
		p := newArithProgram("wrap_"+strings.TrimPrefix(typ.name, "wrap."), "an/wrap")
		fmt.Fprintf(&p.src, "func main() {\n\txs := []%s{", typ.name)
		for i, v := range vals {
			if i > 0 {
				p.src.WriteString(", ")
			}
			p.src.WriteString(literal(typ, v))
		}
		p.src.WriteString("}\n\tcs := []uint{0, 1, 7, 8, 31, 32, 63, 64}\n")
		for i, x := range vals {
			for j, y := range vals {
				for _, op := range binOps {
					if (op == "/" || op == "%") && y.Sign() == 0 {
						continue
					}
					fmt.Fprintf(&p.src, "\tfmt.Println(xs[%d] %s xs[%d])\n", i, op, j)
					fmt.Fprintf(&p.want, "%s\n", goWrap(typ, op, x, y))
				}
			}
			fmt.Fprintf(&p.src, "\tfmt.Println(-xs[%d])\n", i)
			fmt.Fprintf(&p.want, "%s\n", goWrap(typ, "neg", x, x))
			for k, c := range []int64{0, 1, 7, 8, 31, 32, 63, 64} {
				for _, op := range []string{"<<", ">>"} {
					fmt.Fprintf(&p.src, "\tfmt.Println(xs[%d] %s cs[%d])\n", i, op, k)
					fmt.Fprintf(&p.want, "%s\n", goWrap(typ, op, x, big.NewInt(c)))
				}
			}
			// Conversions from i64 truncate (type.conv.wrap).
			s := intType{"i64", 64, true}.wrapBig(new(big.Int).Add(new(big.Int).Mul(x, big.NewInt(1000003)), big.NewInt(5)))
			fmt.Fprintf(&p.src, "\tfmt.Println(%s([]i64{%s}[0]))\n", typ.name, s)
			fmt.Fprintf(&p.want, "%s\n", typ.wrapBig(s))
		}
		p.src.WriteString("\tfmt.Println(xs[1] / (xs[0] * xs[1]))\n}\n")
		p.want.WriteString("panic: runtime error: integer divide by zero\nexit status 2\n")
		p.run(t)
	}
}

// goSlice evaluates an index or slice expression on a Go slice, array or
// string of length 3 and capacity 5 (3 for arrays and strings) and
// returns the length and capacity of the result, or the panic message.
func goSlice(kind string, idx []int, three bool) (res string) {
	defer func() {
		if r := recover(); r != nil {
			res = "panic: " + r.(error).Error()
		}
	}()
	s := make([]int, 3, 5)
	a := [3]int{}
	str := "abc"
	lo := idx[0]
	if len(idx) == 1 {
		switch kind {
		case "slice":
			return fmt.Sprint(s[lo])
		case "array":
			return fmt.Sprint(a[lo])
		}
		return fmt.Sprint(str[lo])
	}
	hi, mx := idx[1], idx[2]
	switch {
	case kind == "slice" && three:
		r := s[lo:hi:mx]
		return fmt.Sprint(len(r), cap(r))
	case kind == "slice":
		r := s[lo:hi]
		return fmt.Sprint(len(r), cap(r))
	case kind == "array" && three:
		r := a[lo:hi:mx]
		return fmt.Sprint(len(r), cap(r))
	case kind == "array":
		r := a[lo:hi]
		return fmt.Sprint(len(r), cap(r))
	}
	r := str[lo:hi]
	return fmt.Sprint(len(r), len(r))
}

// TestBoundsOracle compares index and slice bounds checks with Go: the
// same expressions run on Go values decide the expected result or panic
// message (expr.bounds.check).
func TestBoundsOracle(t *testing.T) {
	vals := []int{-1, 0, 2, 3, 4, 5, 6}
	decl := "\ts := make([]int, 3, 5)\n\tvar a [3]int\n\tstr := \"abc\"\n\tis := []int{-1, 0, 2, 3, 4, 5, 6}\n\t_, _, _, _ = s, a, str, is\n"
	operand := map[string]string{"slice": "s", "array": "a", "string": "str"}
	ix := func(v int) string {
		for i, x := range vals {
			if x == v {
				return fmt.Sprintf("is[%d]", i)
			}
		}
		panic(v)
	}
	ok := newArithProgram("bounds")
	ok.src.WriteString("func main() {\n" + decl)
	n := 0
	for _, kind := range []string{"slice", "array", "string"} {
		x := operand[kind]
		var cases [][]int
		var threes []bool
		for _, i := range vals {
			cases, threes = append(cases, []int{i}), append(threes, false)
			for _, j := range vals {
				cases, threes = append(cases, []int{i, j, 0}), append(threes, false)
				if kind != "string" {
					for _, k := range vals {
						cases, threes = append(cases, []int{i, j, k}), append(threes, true)
					}
				}
			}
		}
		for c, idx := range cases {
			var e string
			switch {
			case len(idx) == 1:
				e = fmt.Sprintf("%s[%s]", x, ix(idx[0]))
			case threes[c]:
				e = fmt.Sprintf("%s[%s:%s:%s]", x, ix(idx[0]), ix(idx[1]), ix(idx[2]))
			default:
				e = fmt.Sprintf("%s[%s:%s]", x, ix(idx[0]), ix(idx[1]))
			}
			want := goSlice(kind, idx, threes[c])
			print := e
			if len(idx) > 1 {
				print = fmt.Sprintf("len(%s), cap(%s)", e, e)
				if kind == "string" {
					print = fmt.Sprintf("len(%s), len(%s)", e, e)
				}
			}
			if !strings.HasPrefix(want, "panic: ") {
				fmt.Fprintf(&ok.src, "\tfmt.Println(%s)\n", print)
				fmt.Fprintf(&ok.want, "%s\n", want)
				continue
			}
			// Each panic in a program of its own; a sample of them.
			if n++; n%5 != 0 {
				continue
			}
			p := newArithProgram(fmt.Sprintf("bounds_panic_%d", n))
			fmt.Fprintf(&p.src, "func main() {\n%s\tfmt.Println(%s)\n}\n", decl, print)
			fmt.Fprintf(&p.want, "%s\nexit status 2\n", want)
			p.run(t)
		}
	}
	ok.src.WriteString("}\n")
	ok.run(t)
}
