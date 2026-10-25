package modfile

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	src := "module  hello\nkern    1.0 // edition\ntargets linux-x86_64, macos-arm64\n"
	f, errs := Parse("kern.mod", []byte(src))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	want := &File{Module: "hello", Edition: "1.0", Targets: []string{"linux-x86_64", "macos-arm64"}}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("got %+v, want %+v", f, want)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		src  string
		rule string
	}{
		{"kern 1.0\n", "tool.mod.module"},
		{"module a\n", "tool.mod.edition"},
		{"module Hello\nkern 1.0\n", "decl.import.module"},
		{"module kern\nkern 1.0\n", "decl.import.module"},
		{"module a\nmodule b\nkern 1.0\n", "tool.mod.module"},
		{"module a\nkern one\n", "tool.mod.edition"},
		{"module a\nkern 1.0\ntargets Linux\n", "tool.mod.targets"},
	}
	for _, tt := range tests {
		_, errs := Parse("kern.mod", []byte(tt.src))
		if len(errs) == 0 || errs[0].Rule != tt.rule {
			t.Errorf("%q: got %v, want rule %s", strings.ReplaceAll(tt.src, "\n", `\n`), errs, tt.rule)
		}
	}
}
