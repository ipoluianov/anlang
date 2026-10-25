// Package modfile parses kern.mod (spec/20-toolchain.md, §20.3).
package modfile

import (
	"fmt"
	"regexp"
	"strings"

	"kern/syntax"
)

// File is a parsed kern.mod.
type File struct {
	Module  string   // module name
	Edition string   // language edition, for example "1.0"
	Targets []string // default targets
}

var (
	moduleName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)  // decl.import.module
	edition    = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)   // Edition
	targetName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`) // TargetName
	directive  = regexp.MustCompile(`^(module|kern|targets|effects|generate)\b`)
)

// onceRule names the rule that allows a directive at most once.
var onceRule = map[string]string{
	"module":  "tool.mod.module",
	"kern":    "tool.mod.edition",
	"targets": "tool.mod.targets",
	"effects": "tool.mod.effects",
}

// Parse parses the contents of a kern.mod file. The name is used in
// error positions.
func Parse(name string, data []byte) (*File, []*syntax.Error) {
	f := &File{}
	var errs []*syntax.Error
	errorf := func(line int, rule, format string, args ...any) {
		errs = append(errs, &syntax.Error{
			Pos:  syntax.Pos{File: name, Line: line, Col: 1},
			Rule: rule,
			Msg:  fmt.Sprintf(format, args...),
		})
	}
	seen := map[string]int{}

	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		line := i + 1
		text := raw
		if j := strings.Index(text, "//"); j >= 0 {
			text = text[:j]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		m := directive.FindString(text)
		if m == "" {
			errorf(line, "", "unknown directive %q", strings.Fields(text)[0])
			continue
		}
		if prev, ok := seen[m]; ok && m != "generate" {
			errorf(line, onceRule[m], "duplicate %s directive (first on line %d)", m, prev)
			continue
		}
		seen[m] = line
		arg := strings.TrimSpace(text[len(m):])

		switch m {
		case "module":
			if !moduleName.MatchString(arg) {
				errorf(line, "decl.import.module", "invalid module name %q: must match [a-z][a-z0-9_]*", arg)
			} else if arg == "kern" {
				errorf(line, "decl.import.module", "module name kern is reserved for the standard library")
			}
			f.Module = arg
		case "kern":
			if !edition.MatchString(arg) {
				errorf(line, "tool.mod.edition", "invalid edition %q: want <major>.<minor>", arg)
			}
			f.Edition = arg
		case "targets":
			for _, t := range strings.Split(arg, ",") {
				t = strings.TrimSpace(t)
				if !targetName.MatchString(t) {
					errorf(line, "tool.mod.targets", "invalid target name %q", t)
					continue
				}
				f.Targets = append(f.Targets, t)
			}
		case "effects", "generate":
			errorf(line, "", "%s directive: not supported yet by this compiler", m)
		}
	}

	if _, ok := seen["module"]; !ok {
		errorf(1, "tool.mod.module", "kern.mod has no module directive")
	}
	if _, ok := seen["kern"]; !ok {
		errorf(1, "tool.mod.edition", "kern.mod has no kern directive")
	}
	return f, errs
}
