// Package conformance holds checks that tie the implementation to the
// specification (spec/23-conformance.md).
package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ruleID matches a rule identifier of the specification.
var ruleID = regexp.MustCompile(`\b(?:lex|type|decl|expr|stmt|mem|eff|contract|conc|ct|safe|ffi|target|emb|os|gpu|ui|std|rt|tool|proto|impl|conf)(?:\.[a-z0-9-]+)+\b`)

// ruleDef matches the definition of a rule in a spec file.
var ruleDef = regexp.MustCompile(`(?m)^\s*(?:[-*>]|\d+\.)?\s*\*\*([a-z0-9.-]+)\*\*`)

// TestRuleIDsExist checks that every rule identifier quoted in a Go
// string literal of the compiler is defined in the specification, so a
// diagnostic never points to a rule that does not exist.
func TestRuleIDsExist(t *testing.T) {
	defined := map[string]bool{}
	specs, _ := filepath.Glob("../spec/*.md")
	for _, f := range specs {
		if strings.HasSuffix(f, ".ru.md") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ruleDef.FindAllStringSubmatch(string(data), -1) {
			defined[m[1]] = true
		}
	}
	if len(defined) < 100 {
		t.Fatalf("found only %d rule definitions in ../spec", len(defined))
	}

	quoted := regexp.MustCompile(`"([a-z0-9.-]+)"`)
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "spec" || d.Name() == "examples" || strings.HasPrefix(d.Name(), ".")) && path != ".." {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range quoted.FindAllStringSubmatch(string(data), -1) {
			if ruleID.MatchString(m[1]) && ruleID.FindString(m[1]) == m[1] && !defined[m[1]] {
				t.Errorf("%s: rule %s is not defined in spec/", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
