package conformance

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// printfValue is an argument of fmt.Printf: its Anlang type and source,
// and the Go value with the same type class, size and value.
type printfValue struct {
	typ, src string
	goVal    any
}

type pair struct {
	A int
	B string
}

var printfValues = []printfValue{
	{"i8", "-7", int8(-7)},
	{"i16", "300", int16(300)},
	{"i32", "-123456", int32(-123456)},
	{"i64", "9007199254740993", int64(9007199254740993)},
	{"int", "42", 42},
	{"u8", "200", uint8(200)},
	{"u16", "65535", uint16(65535)},
	{"u32", "4000000000", uint32(4000000000)},
	{"u64", "18446744073709551615", uint64(18446744073709551615)},
	{"uint", "0", uint(0)},
	{"i32", "'λ'", int32('λ')},
	{"f64", "3.25", 3.25},
	{"f64", "-0.000123", -0.000123},
	{"f64", "1e21", 1e21},
	{"f64", "123456.789", 123456.789},
	{"f32", "0.1", float32(0.1)},
	{"string", `"héllo"`, "héllo"},
	{"string", `"tab\tquote\""`, "tab\tquote\""},
	{"string", `""`, ""},
	{"bool", "true", true},
	{"[]u8", `[]u8("go")`, []byte("go")},
	{"[]int", "[]int{1, -2, 30}", []int{1, -2, 30}},
	{"[2]f64", "[2]f64{1.5, 2}", [2]float64{1.5, 2}},
	{"pair", `pair{7, "x"}`, pair{7, "x"}},
	{"map[string]int", `map[string]int{"b": 2, "a": 1}`, map[string]int{"b": 2, "a": 1}},
}

// verbsFor returns the verbs that apply to values of the Go type of v, as
// the checker decides (ct.reject.precondition).
func verbsFor(v any) string {
	switch v.(type) {
	case bool:
		return "vt"
	case int8, int16, int32, int64, int, uint8, uint16, uint32, uint64, uint:
		return "vdbcoOqxXU"
	case float32, float64:
		return "vbeEfFgGxX"
	case string, []byte:
		return "vsqxX"
	case []int:
		return "vdbcoOqxXU"
	case [2]float64:
		return "veEfgG"
	case pair:
		return "v"
	case map[string]int:
		return "vqxX"
	}
	return "v"
}

// TestPrintfOracle compares fmt.Sprintf in the IR interpreter with Go's
// fmt.Sprintf on the same directives and values (std.fmt.print).
func TestPrintfOracle(t *testing.T) {
	flags := []string{"", "+", "-", "#", " ", "0", "+0", "-#"}
	widths := []string{"", "1", "8"}
	precs := []string{"", ".0", ".3"}
	p := newArithProgram("printf")
	p.src.WriteString("type pair struct {\n\tA int\n\tB string\n}\n\nfunc main() {\n")
	n := 0
	for i, v := range printfValues {
		fmt.Fprintf(&p.src, "\tvar v%d %s = %s\n", i, v.typ, v.src)
		for _, verb := range verbsFor(v.goVal) {
			if verb == 'U' || verb == 'c' {
				if _, isSlice := v.goVal.([]int); isSlice {
					continue
				}
			}
			for _, f := range flags {
				if f == "#" && verb == 'v' || strings.Contains(f, "#") && verb == 'v' {
					continue // %#v is not supported yet
				}
				for _, w := range widths {
					for _, pr := range precs {
						dir := "%" + f + w + pr + string(verb)
						fmt.Fprintf(&p.src, "\tfmt.Println(fmt.Sprintf(%s, v%d))\n", strconv.Quote("|"+dir+"|"), i)
						fmt.Fprintf(&p.want, "%s\n", fmt.Sprintf("|"+dir+"|", v.goVal))
						n++
					}
				}
			}
		}
	}
	p.src.WriteString("\tfmt.Printf(\"%d%% of %s, %5.2f\\n\", 50, \"x\", 3.14159)\n}\n")
	p.want.WriteString(fmt.Sprintf("%d%% of %s, %5.2f\n", 50, "x", 3.14159))
	p.run(t)
	t.Logf("%d directives compared", n)
}
