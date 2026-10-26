package syntax

import "testing"

func TestClone(t *testing.T) {
	src := "package p\n\nfunc f(xs []int) int {\n\tfor const i := range 3 {\n\t\txs[i] = i\n\t}\n\treturn len(xs)\n}\n"
	f, errs := Parse("p.an", []byte(src))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	d := f.Decls[0].(*FuncDecl)
	c := Clone(d)
	if c == d || c.Body == d.Body || c.Body.List[0] == d.Body.List[0] {
		t.Fatal("Clone shares nodes")
	}
	if c.Name.Name != "f" || c.Name.Pos() != d.Name.Pos() {
		t.Errorf("Clone changed the name or position")
	}
	r := c.Body.List[0].(*RangeStmt)
	if !r.Const {
		t.Errorf("Clone lost the const of for const")
	}
	r.Body.List = nil
	if len(d.Body.List[0].(*RangeStmt).Body.List) == 0 {
		t.Errorf("changing the clone changed the original")
	}
}
