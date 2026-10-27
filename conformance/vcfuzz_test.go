package conformance

// The differential test of the proof obligations (spec/23-conformance.md,
// conf.solver.vc): random programs whose assertions the verification
// proves run in the IR interpreter on random inputs. An assertion that
// fails there, or a run-time check that fails in an instruction marked
// proven, is a fault of the verification, which would remove a check
// that the program needs.

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"an/build"
	"an/format"
	"an/interp"
	"an/ir"
	"an/lower"
	"an/syntax"
)

// fuzzType is an integer type of the generated programs and its range on
// the target of the suite.
type fuzzType struct {
	name   string
	lo, hi int64
}

var fuzzTypes = []fuzzType{
	{"int", -1 << 63, 1<<63 - 1},
	{"i8", -128, 127},
	{"u8", 0, 255},
	{"i16", -32768, 32767},
	{"u16", 0, 65535},
	{"u32", 0, 1<<32 - 1},
}

// widens reports whether every value of s is a value of t.
func (s fuzzType) widens(t fuzzType) bool { return t.lo <= s.lo && s.hi <= t.hi }

// fuzzGen generates one program.
type fuzzGen struct {
	r       *rand.Rand
	vars    map[string]fuzzType // the locals of f by name
	order   []string
	params  []string
	asserts int
	loops   int // the depth of loops
	b       strings.Builder
	indent  int
	// inHelper is set in h, which has no s, xs and inc.
	inHelper bool
	// facts are conditions that held once; asserted again later, they
	// hold only if nothing changed them since.
	facts []string
}

func (g *fuzzGen) line(format string, args ...any) {
	g.b.WriteString(strings.Repeat("\t", g.indent))
	fmt.Fprintf(&g.b, format, args...)
	g.b.WriteString("\n")
}

func (g *fuzzGen) pick(n int) int { return g.r.Intn(n) }

// varsOf returns the variables of type t.
func (g *fuzzGen) varsOf(t fuzzType) []string {
	var out []string
	for _, v := range g.order {
		if g.vars[v] == t {
			out = append(out, v)
		}
	}
	return out
}

// konst returns a small constant of type t.
func (g *fuzzGen) konst(t fuzzType) string {
	k := int64(g.pick(11))
	if t.lo < 0 {
		k -= 5
	}
	if k < 0 {
		return fmt.Sprintf("(%d)", k)
	}
	return strconv.FormatInt(k, 10)
}

// expr returns an expression of type t; contract excludes the operations
// whose meaning in a contract differs from the code (<<, which wraps).
func (g *fuzzGen) expr(t fuzzType, depth int, contract bool) string {
	vs := g.varsOf(t)
	if depth <= 0 || g.pick(3) == 0 {
		if len(vs) > 0 && g.pick(4) != 0 {
			return vs[g.pick(len(vs))]
		}
		return g.konst(t)
	}
	x := func() string { return g.expr(t, depth-1, contract) }
	switch g.pick(16) {
	case 0, 1:
		return fmt.Sprintf("(%s + %s)", x(), x())
	case 2, 3:
		return fmt.Sprintf("(%s - %s)", x(), x())
	case 4:
		return fmt.Sprintf("(%s * %d)", x(), g.pick(4))
	case 5:
		return fmt.Sprintf("(%s / %d)", x(), 1+g.pick(5))
	case 6:
		return fmt.Sprintf("(%s %% %d)", x(), 1+g.pick(5))
	case 7:
		return fmt.Sprintf("(%s & %d)", x(), g.pick(16))
	case 8:
		return fmt.Sprintf("(%s | %d)", x(), g.pick(16))
	case 9:
		return fmt.Sprintf("(%s ^ %d)", x(), g.pick(16))
	case 10:
		return fmt.Sprintf("(%s >> %d)", x(), g.pick(4))
	case 11:
		if !contract {
			return fmt.Sprintf("(%s << %d)", x(), g.pick(3))
		}
		return x()
	case 12:
		return fmt.Sprintf("min(%s, %s)", x(), x())
	case 13:
		return fmt.Sprintf("max(%s, %s)", x(), x())
	case 14:
		// A conversion from a narrower type.
		for _, s := range fuzzTypes {
			if s != t && s.widens(t) && len(g.varsOf(s)) > 0 && g.pick(2) == 0 {
				return fmt.Sprintf("%s(%s)", t.name, g.expr(s, depth-1, contract))
			}
		}
		return x()
	}
	if t.name == "int" && !g.inHelper {
		switch g.pick(6) {
		case 0:
			return fmt.Sprintf("xs[%s]", g.index(depth-1, contract))
		case 1:
			return "len(xs)"
		case 2:
			return "s.a"
		case 3:
			return "s.b"
		case 4:
			if !contract {
				return fmt.Sprintf("inc(%s)", g.expr(fuzzTypes[3], depth-1, contract))
			}
		}
	}
	return x()
}

// index returns an index of xs: in bounds, or of any value.
func (g *fuzzGen) index(depth int, contract bool) string {
	switch g.pick(4) {
	case 0:
		return strconv.Itoa(g.pick(4))
	case 1:
		if g.loops > 0 {
			return "i"
		}
	case 2:
		return fmt.Sprintf("(%s & 3)", g.expr(fuzzTypes[0], depth, contract))
	}
	e := g.expr(fuzzTypes[0], depth, contract)
	if !hasVar.MatchString(e) {
		return fmt.Sprintf("(%s & 3)", e) // a constant index must be in bounds
	}
	return fmt.Sprintf("(%s %% 4)", e)
}

// cond returns a condition.
func (g *fuzzGen) cond(depth int, contract bool) string {
	if depth > 0 {
		switch g.pick(6) {
		case 0:
			return fmt.Sprintf("(%s && %s)", g.cond(depth-1, contract), g.cond(depth-1, contract))
		case 1:
			return fmt.Sprintf("(%s || %s)", g.cond(depth-1, contract), g.cond(depth-1, contract))
		case 2:
			return fmt.Sprintf("!%s", g.cond(depth-1, contract))
		}
	}
	t := fuzzTypes[g.pick(len(fuzzTypes))]
	if len(g.varsOf(t)) == 0 {
		t = fuzzTypes[0]
	}
	ops := []string{"<", "<=", "==", "!=", ">", ">="}
	return fmt.Sprintf("(%s %s %s)", g.expr(t, 2, contract), ops[g.pick(len(ops))], g.expr(t, 1, contract))
}

func (g *fuzzGen) assert(c string) {
	if g.loops == 0 && !strings.Contains(c, "j") && !strings.Contains(c, "w") && !strings.Contains(c, "x ") && !strings.Contains(c, "i") {
		g.facts = append(g.facts, c)
	}
	g.asserts++
	g.line("assert(%s) // A%d", c, g.asserts)
}

// stmts generates n statements.
func (g *fuzzGen) stmts(n, depth int) {
	for i := 0; i < n; i++ {
		g.stmt(depth)
	}
}

func (g *fuzzGen) stmt(depth int) {
	v := g.order[g.pick(len(g.order))]
	t := g.vars[v]
	switch k := g.pick(15); {
	case k < 3:
		e := g.expr(t, 2, false)
		g.line("%s = %s", v, e)
		if g.pick(3) == 0 && !strings.Contains(e, v) && !strings.Contains(e, "<<") && !strings.Contains(e, "inc(") {
			g.assert(fmt.Sprintf("%s == %s", v, e))
		}
	case k == 3:
		ops := []string{"+=", "-=", "&=", "|=", "^="}
		g.line("%s %s %s", v, ops[g.pick(len(ops))], g.expr(t, 1, false))
	case k == 4:
		g.line("xs[%s] = %s", g.index(1, false), g.expr(fuzzTypes[0], 2, false))
	case k == 5:
		f := []string{"s.a", "s.b"}[g.pick(2)]
		g.line("%s = %s", f, g.expr(fuzzTypes[0], 2, false))
	case k == 6 && depth > 0:
		c := g.cond(1, true)
		g.line("if %s {", c)
		g.indent++
		if g.pick(2) == 0 {
			g.assert(c)
		}
		g.stmts(1+g.pick(3), depth-1)
		g.indent--
		g.line("} else {")
		g.indent++
		if g.pick(2) == 0 {
			g.assert("!" + c)
		}
		g.stmts(1+g.pick(2), depth-1)
		g.indent--
		g.line("}")
	case k == 7 && depth > 0 && g.loops == 0:
		g.line("for i := range %d {", 1+g.pick(4))
		g.indent++
		g.loops++
		g.line("_ = i")
		g.stmts(1+g.pick(3), depth-1)
		g.loops--
		g.indent--
		g.line("}")
	case k == 8 && depth > 0:
		g.line("switch %s & 3 {", g.expr(fuzzTypes[0], 1, false))
		for c := 0; c < 2; c++ {
			g.line("case %d:", c)
			g.indent++
			g.stmts(1+g.pick(2), depth-1)
			if c == 0 && g.pick(3) == 0 {
				g.line("fallthrough")
			}
			g.indent--
		}
		g.line("default:")
		g.indent++
		g.stmts(1, depth-1)
		g.indent--
		g.line("}")
	case k == 9:
		g.line("bump(s, %s)", g.expr(fuzzTypes[1], 1, false))
		g.assert("s.b >= -128 && s.b <= 127")
	case k == 10:
		g.richStmt(depth)
	case k == 11 && len(g.facts) > 0 && !g.inHelper:
		g.assert(g.facts[g.pick(len(g.facts))])
	case k >= 13 && !g.inHelper && depth > 0:
		g.probe()
	default:
		g.assert(g.cond(2, true))
	}
}

// probe sets a value before a loop or a branch that may or may not
// change it, and asserts it after.
func (g *fuzzGen) probe() {
	targets := []string{fmt.Sprintf("xs[%d]", g.pick(4)), "s.a", "s.b"}
	for _, v := range g.order {
		if g.vars[v].name == "int" {
			targets = append(targets, v)
		}
	}
	target := targets[g.pick(len(targets))]
	k := g.pick(9) - 4
	g.line("%s = %d", target, k)
	loop := g.pick(2) == 0
	if loop {
		g.line("for i := range %s {", g.expr(fuzzTypes[0], 1, false))
		g.loops++
	} else {
		g.line("if %s {", g.cond(1, false))
	}
	g.indent++
	if loop {
		g.line("_ = i")
	}
	g.stmts(g.pick(2), 0)
	if g.pick(2) == 0 {
		g.line("%s = %s", target, g.expr(fuzzTypes[0], 1, false))
	}
	if loop && g.pick(3) == 0 {
		g.line("xs[i & 3] = %d", k+1)
	}
	g.indent--
	if loop {
		g.loops--
	}
	g.line("}")
	g.assert(fmt.Sprintf("%s == %d", target, k))
}

// richStmt generates a statement of the constructs that the assertions
// after it state facts of: elements, struct literals, Option values,
// loops and narrowing conversions.
func (g *fuzzGen) richStmt(depth int) {
	pure := func(e string, not ...string) bool {
		for _, n := range append(not, "<<", "inc(") {
			if strings.Contains(e, n) {
				return false
			}
		}
		return true
	}
	switch g.pick(10) {
	case 8, 9:
		// A subslice passed inout, or cleared.
		lo := g.pick(4)
		hi := lo + g.pick(5-lo)
		if g.pick(2) == 0 {
			g.line("zap(xs[%d:%d])", lo, hi)
		} else {
			g.line("clear(xs[%d:%d])", lo, hi)
		}
		g.assert(fmt.Sprintf("xs[%d] == %d", g.pick(4), g.pick(9)-4))
	case 7:
		var ints []string
		for _, v := range g.order {
			if g.vars[v].name == "int" {
				ints = append(ints, v)
			}
		}
		v := ints[g.pick(len(ints))]
		g.line("%s = h(%s, %s)", v, g.expr(fuzzTypes[0], 1, false), g.expr(fuzzTypes[0], 1, false))
		g.assert(g.cond(1, true))
	case 0:
		c := g.pick(4)
		e := g.expr(fuzzTypes[0], 2, true)
		g.line("xs[%d] = %s", c, e)
		if pure(e, "xs") {
			g.assert(fmt.Sprintf("xs[%d] == %s", c, e))
		}
	case 1:
		a, b := g.expr(fuzzTypes[0], 1, true), g.expr(fuzzTypes[0], 1, true)
		g.line("s = S{a: %s, b: %s}", a, b)
		if pure(a, "s.") && pure(b, "s.") {
			g.assert(fmt.Sprintf("s.a == %s && s.b == %s", a, b))
		}
	case 2:
		e := g.expr(fuzzTypes[0], 1, true)
		if g.pick(3) == 0 {
			g.line("o = None")
		} else {
			g.line("o = Some(%s)", e)
		}
		g.line("switch o {")
		g.line("case Some{w}:")
		g.indent++
		g.line("_ = w")
		if pure(e, "o") {
			g.assert(fmt.Sprintf("w == %s", e))
		}
		if depth > 0 {
			g.stmts(1, depth-1)
		}
		g.indent--
		g.line("case None:")
		g.indent++
		g.assert("o == None")
		g.indent--
		g.line("}")
	case 3:
		g.line("for i, x := range xs {")
		g.indent++
		g.loops++
		g.line("_, _ = i, x")
		g.assert("x == xs[i]")
		if depth > 0 {
			g.stmts(1, depth-1)
		}
		g.loops--
		g.indent--
		g.line("}")
	case 4:
		k := 1 + g.pick(5)
		g.line("for j := 0; j < %d; j++ {", k)
		g.indent++
		g.line("_ = j")
		g.assert(fmt.Sprintf("j >= 0 && j < %d", k))
		if depth > 0 {
			g.stmts(1, depth-1)
		}
		g.indent--
		g.line("}")
	case 5:
		// A narrowing conversion that the condition proves.
		var vs []string
		for _, v := range g.order {
			if g.vars[v].name != "int" {
				vs = append(vs, v)
			}
		}
		if len(vs) == 0 {
			return
		}
		v := vs[g.pick(len(vs))]
		t := g.vars[v]
		e := g.expr(fuzzTypes[0], 1, true)
		lo, hi := t.lo+int64(g.pick(3)), t.hi-int64(g.pick(3))
		g.line("if %s >= %d && %s <= %d {", e, lo, e, hi)
		g.indent++
		g.line("%s = %s(%s)", v, t.name, e)
		if pure(e, v) {
			g.assert(fmt.Sprintf("int(%s) == %s", v, e))
		}
		g.indent--
		g.line("}")
	case 6:
		e := g.expr(fuzzTypes[0], 2, true)
		g.assert(fmt.Sprintf("(%s & 7) >= 0 && (%s & 7) <= 7 && (%s | 1) != 0", e, e, e))
	}
}

// helper generates func h with random postconditions, of which the
// proofs keep those that hold.
func (g *fuzzGen) helper() {
	saved, savedOrder := g.vars, g.order
	g.vars = map[string]fuzzType{"x": fuzzTypes[0], "y": fuzzTypes[0]}
	g.order = []string{"x", "y"}
	g.inHelper = true
	defer func() { g.inHelper = false }()
	e1, e2 := g.expr(fuzzTypes[0], 2, true), g.expr(fuzzTypes[0], 2, true)
	g.vars["result"] = fuzzTypes[0]
	g.order = append(g.order, "result")
	g.b.WriteString("// h is a function with postconditions.\nfunc h(x int, y int) int {\n")
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&g.b, "\tensures %s\n", g.cond(1, true))
	}
	fmt.Fprintf(&g.b, "\tif x > y {\n\t\treturn %s\n\t}\n\treturn %s\n}\n\n", e1, e2)
	g.vars, g.order = saved, savedOrder
}

// program generates the source of a program with the local variables of
// f read from the input.
func (g *fuzzGen) program() string {
	g.vars = map[string]fuzzType{}
	n := 3 + g.pick(4)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("v%d", i)
		g.vars[name] = fuzzTypes[g.pick(len(fuzzTypes))]
		g.order = append(g.order, name)
	}
	g.vars["n"] = fuzzTypes[0]
	g.order = append(g.order, "n")
	var params []string
	for _, v := range g.order {
		params = append(params, v+" "+g.vars[v].name)
	}
	g.b.WriteString(`package main

import (
	"an/console"
	"an/fmt"
)

type S struct {
	a int
	b int
}

// inc returns x + 1.
func inc(x i16) int {
	ensures result == int(x)+1
	return int(x) + 1
}

// bump sets s.b to d.
func bump(s inout S, d i8) {
	ensures s.b == int(d) && s.a == old(s.a)
	s.b = int(d)
}

// zap sets the first element of s.
func zap(s inout []int) {
	if len(s) > 0 {
		s[0] = 7
	}
}

// read reads two bytes of the input as a number in [-32768, 32767].
func read() int {
	b1, _ := console.ReadByte()
	b2, _ := console.ReadByte()
	return (int(b1)<<8 | int(b2)) - 32768
}

`)
	g.helper()
	g.b.WriteString("func f(" + strings.Join(params, ", ") + ", xs [4]int) {\n")
	g.indent = 1
	g.line("var s S")
	g.line("var o Option[int]")
	g.stmts(6+g.pick(10), 2)
	g.line("_ = o")
	g.line("fmt.Println(%s, s.a, s.b, xs[0], xs[1], xs[2], xs[3])", strings.Join(g.order, ", "))
	g.b.WriteString("}\n\nfunc main() {\n")
	var args []string
	for _, v := range g.order {
		t := g.vars[v]
		lo, hi := max(t.lo, -32768), min(t.hi, 32767)
		fmt.Fprintf(&g.b, "\t%s := read()\n\tif %s < %d {\n\t\t%s = %d\n\t}\n\tif %s > %d {\n\t\t%s = %d\n\t}\n", v, v, lo, v, lo, v, hi, v, hi)
		args = append(args, fmt.Sprintf("%s(%s)", t.name, v))
	}
	g.b.WriteString("\tvar xs [4]int\n\tfor i := range 4 {\n\t\txs[i] = read()\n\t}\n")
	g.b.WriteString("\tf(" + strings.Join(args, ", ") + ", xs)\n}\n")
	return g.b.String()
}

// ensuresLine returns the line of the ensures clause of src that the
// diagnostic msg names, or 0.
func ensuresLine(src, msg string) int {
	clause := strings.TrimPrefix(msg, "the postcondition ")
	i := strings.Index(clause, " of ")
	for i >= 0 {
		want := strings.ReplaceAll(clause[:i], " ", "")
		for n, l := range strings.Split(src, "\n") {
			if t := strings.TrimSpace(l); strings.HasPrefix(t, "ensures ") && strings.ReplaceAll(t[len("ensures "):], " ", "") == want {
				return n + 1
			}
		}
		j := strings.Index(clause[i+1:], " of ")
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return 0
}

// hasVar matches an expression with a variable.
var hasVar = regexp.MustCompile(`\b(v[0-9]+|n|s|xs|i|x|y)\b`)

var assertLine = regexp.MustCompile(`^(\t*)assert\((.*)\) +// (A[0-9]+)$`)

// withoutAsserts removes the assertions on the lines of src.
func withoutAsserts(src string, lines map[int]bool) string {
	ls := strings.Split(src, "\n")
	var out []string
	for i, l := range ls {
		if !lines[i+1] {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// checked replaces each assertion assert(c) of src with a check that
// panics with the name of the assertion when c is false.
func checked(src string) string {
	var out []string
	for _, l := range strings.Split(src, "\n") {
		m := assertLine.FindStringSubmatch(l)
		if m == nil {
			out = append(out, l)
			continue
		}
		out = append(out, m[1]+"fmt.Println(\"@"+m[3]+"\")", m[1]+"if !("+m[2]+") {", m[1]+"\tpanic(\""+m[3]+"\")", m[1]+"}")
	}
	return strings.Join(out, "\n")
}

// proven reports whether the run-time check of in is marked proven.
func proven(in ir.Instr) bool {
	switch in := in.(type) {
	case *ir.Index:
		return in.Proven
	case *ir.IndexAddr:
		return in.Proven
	case *ir.Slice:
		return in.Proven
	case *ir.BinOp:
		return in.Proven
	case *ir.UnOp:
		return in.Proven
	}
	return false
}

func fuzzFormat(t *testing.T, src string) (string, bool) {
	out, errs := format.Source("main.an", []byte(src), syntax.Mode(0))
	if len(errs) > 0 {
		t.Errorf("a generated program does not parse: %v\n%s", errs, src)
		return "", false
	}
	return string(out), true
}

// TestVCDifferential checks the proofs of random programs in the IR
// interpreter (conf.solver.vc). ANLANG_VCFUZZ sets the number of
// programs.
func TestVCDifferential(t *testing.T) {
	programs := 10
	if testing.Short() {
		programs = 3
	}
	if s := os.Getenv("ANLANG_VCFUZZ"); s != "" {
		programs, _ = strconv.Atoi(s)
	}
	seed := int64(1)
	if s := os.Getenv("ANLANG_VCFUZZ_SEED"); s != "" {
		seed, _ = strconv.ParseInt(s, 10, 64)
	}
	target, _ := build.LookupTarget(suiteTarget)
	check := func(src string) []*syntax.Error {
		_, diags := build.CheckSources(suiteModule, target, []build.Source{{Name: "main.an", Data: []byte(src)}})
		return diags
	}
	proved, runs, invalid := 0, 0, 0
	reached := map[string]bool{} // the assertions that the runs reach
	for p := 0; p < programs; p++ {
		g := &fuzzGen{r: rand.New(rand.NewSource(seed + int64(p)))}
		src, ok := fuzzFormat(t, g.program())
		if !ok {
			continue
		}
		// Remove the assertions that are not proven until the others are.
		var diags []*syntax.Error
		for round := 0; ; round++ {
			diags = check(src)
			drop := map[int]bool{}
			var other []*syntax.Error
			for _, d := range diags {
				if d.Rule == "contract.obligation.unproven" && strings.HasPrefix(d.Msg, "the postcondition ") {
					if l := ensuresLine(src, d.Msg); l > 0 {
						drop[l] = true
						continue
					}
				}
				if d.Rule == "contract.obligation.unproven" && strings.HasPrefix(d.Msg, "the assertion ") || d.Rule == "type.conv.int-narrow" || d.Rule == "type.conv.constant" {
					drop[d.Pos.Line] = true
				} else {
					other = append(other, d)
				}
			}
			if len(other) > 0 {
				// Such as a constant expression that overflows its type.
				t.Logf("program %d (seed %d) is skipped: %v", p, seed+int64(p), other[0])
				invalid++
				src = ""
				break
			}
			if len(drop) == 0 {
				break
			}
			if src, ok = fuzzFormat(t, withoutAsserts(src, drop)); !ok {
				break
			}
			t.Logf("program %d: round %d, %d assertions not proven", p, round, len(drop))
		}
		if src == "" {
			continue
		}
		proved += strings.Count(src, "assert(")
		run, ok := fuzzFormat(t, checked(src))
		if !ok {
			continue
		}
		info, diags := build.CheckSources(suiteModule, target, []build.Source{{Name: "main.an", Data: []byte(run)}})
		if len(diags) > 0 {
			t.Errorf("program %d: the program with checks is invalid: %v\n%s", p, diags, run)
			continue
		}
		prog, errs := lower.Program(info)
		if len(errs) > 0 {
			t.Errorf("program %d: lowering: %v", p, errs)
			continue
		}
		for k := 0; k < 40; k++ {
			in := make([]byte, 2*(len(g.order)+4))
			g.r.Read(in)
			if k%4 == 0 {
				// Small values reach more branches.
				for j := 0; j < len(in); j += 2 {
					u := uint16(g.r.Intn(17) - 8 + 32768)
					in[j], in[j+1] = byte(u>>8), byte(u)
				}
			}
			var out bytes.Buffer
			err := interp.Run(prog, interp.Options{Stdout: &out, Stderr: &out, Stdin: bytes.NewReader(in), MaxSteps: 1 << 20})
			runs++
			for _, l := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(l, "@A") {
					reached[fmt.Sprint(p, l)] = true
				}
			}
			pe, isPanic := err.(*interp.Panic)
			switch {
			case isPanic && strings.HasPrefix(pe.Value, "A"):
				t.Errorf("program %d (seed %d): the proven assertion %s fails on input %x\n%s", p, seed+int64(p), pe.Value, in, src)
			case isPanic && pe.Instr != nil && proven(pe.Instr):
				t.Errorf("program %d (seed %d): a check proven not to fail fails on input %x: %s in %T of %v\n%s", p, seed+int64(p), in, pe.Value, pe.Instr, pe.Stack, run)
			}
			if t.Failed() {
				return
			}
		}
	}
	t.Logf("%d programs, %d skipped, %d proven assertions, %d of them reached, %d runs", programs, invalid, proved, len(reached), runs)
	if 2*invalid > programs {
		t.Errorf("%d of %d generated programs are invalid", invalid, programs)
	}
}
