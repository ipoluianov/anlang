package modfile

import "testing"

func TestEffectsDirective(t *testing.T) {
	src := "module  weather\nanlang  1.0\neffects (\n    kernel/... allow {Privileged}\n  // parsers\n    jsonparse/...  deny  {Panic, File, Network}\n)\ntargets linux-x86_64\n"
	f, errs := Parse("anlang.mod", []byte(src))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(f.Effects) != 2 || f.Effects[1].Pattern != "jsonparse/..." || f.Effects[1].Allow || len(f.Effects[1].Effects) != 3 {
		t.Fatalf("rules %+v", f.Effects)
	}
	if !f.Effects[1].Matches("jsonparse/lexer") || f.Effects[1].Matches("jsonparser") || !f.Effects[0].Matches("kernel") {
		t.Errorf("pattern matching is wrong")
	}
	want := "module  weather\nanlang  1.0\ntargets linux-x86_64\neffects (\n\t// parsers\n\tjsonparse/... deny  {File, Network, Panic}\n\tkernel/...    allow {Privileged}\n)\n"
	if got := string(Format([]byte(src))); got != want {
		t.Errorf("Format:\n%s\nwant:\n%s", got, want)
	}
	if got := string(Format([]byte(want))); got != want {
		t.Errorf("Format is not idempotent:\n%s", got)
	}
	for _, bad := range []string{
		"module  m\nanlang  1.0\neffects (\n\t... forbid {Alloc}\n)\n",
		"module  m\nanlang  1.0\neffects (\n\t... deny {Alloc}\n",
		"module  m\nanlang  1.0\neffects {\n",
	} {
		if _, errs := Parse("anlang.mod", []byte(bad)); len(errs) == 0 || errs[0].Rule != "eff.mod.syntax" {
			t.Errorf("%q: errors %v, want eff.mod.syntax", bad, errs)
		}
	}
}
