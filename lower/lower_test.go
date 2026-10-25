package lower

import (
	"strings"
	"testing"

	"an/ir"
	"an/syntax"
	"an/types"
)

func lower(t *testing.T, src string) (*ir.Program, []*syntax.Error) {
	t.Helper()
	f, errs := syntax.Parse("a.an", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	info, errs := types.Check(&types.Config{Module: "m"}, []*syntax.File{f})
	if len(errs) > 0 {
		t.Fatalf("check: %v", errs)
	}
	return Program(info)
}

func TestHello(t *testing.T) {
	prog, errs := lower(t, `package main

import "an/fmt"

const answer = 6 * 7

func main() {
	fmt.Println("Hello,", "World!")
	fmt.Print("a", "b", 1, 2, "c\n")
	greet()
	fmt.Println(answer, true, 1.5, f32(0.1), 1e21, 'x', i8(-3))
}

func greet() {
	fmt.Println()
}
`)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var texts []string
	for _, d := range prog.Data {
		texts = append(texts, string(d.Bytes))
	}
	want := []string{"Hello, World!\n", "ab1 2c\n", "42 true 1.5 0.1 1e+21 120 -3\n", "\n"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Errorf("data %q, want %q", texts, want)
	}
	if prog.Main == nil || len(prog.Main.Body) != 5 {
		t.Fatalf("main body %v", prog.Main)
	}
	if _, ok := prog.Main.Body[2].(*ir.Call); !ok {
		t.Errorf("third instruction %T, want *ir.Call", prog.Main.Body[2])
	}
}

func TestUnsupported(t *testing.T) {
	_, errs := lower(t, `package main

import "an/fmt"

func main() {
	x := 1
	fmt.Println(x)
}
`)
	if len(errs) == 0 || !strings.Contains(errs[0].Msg, "step 6") {
		t.Errorf("got %v, want an error naming step 6", errs)
	}
}
