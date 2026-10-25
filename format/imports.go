// Based on go/ast/import.go from the Go sources,
// Copyright The Go Authors, BSD-style license (third_party/go/LICENSE).

package format

import (
	"cmp"
	"slices"
	"strconv"

	"an/syntax"
)

// sortImports sorts runs of consecutive import lines in import blocks,
// by path and then by local name, as gofmt does. Comments move with the
// imports on their lines. Unlike gofmt, duplicates are not removed: a
// repeated import is a compile error in Anlang.
func sortImports(f *syntax.File) {
	changed := false
	for _, d := range f.Decls {
		d, ok := d.(*syntax.GenDecl)
		if !ok || d.Tok != syntax.Import {
			break // imports are always first
		}
		if !d.Lparen.IsValid() {
			continue
		}
		i := 0
		for j, s := range d.Specs {
			if j > i && s.Pos().Line > 1+d.Specs[j-1].End().Line {
				changed = sortSpecs(f, d.Specs[i:j]) || changed
				i = j
			}
		}
		changed = sortSpecs(f, d.Specs[i:]) || changed
	}
	if !changed {
		return
	}
	slices.SortStableFunc(f.Comments, func(a, b *syntax.CommentGroup) int {
		return cmp.Compare(a.Pos().Offset, b.Pos().Offset)
	})
	f.Imports = f.Imports[:0]
	for _, d := range f.Decls {
		if d, ok := d.(*syntax.GenDecl); ok && d.Tok == syntax.Import {
			for _, s := range d.Specs {
				f.Imports = append(f.Imports, s.(*syntax.ImportSpec))
			}
		}
	}
}

func importPath(s syntax.Spec) string {
	t, err := strconv.Unquote(s.(*syntax.ImportSpec).Path.Value)
	if err != nil {
		return ""
	}
	return t
}

func importName(s syntax.Spec) string {
	if n := s.(*syntax.ImportSpec).Name; n != nil {
		return n.Name
	}
	return ""
}

type commentPlace struct {
	left bool // the comment is on the line before the spec, or before the first spec
	g    *syntax.CommentGroup
}

// sortSpecs sorts one run of import specs in place and reports whether
// the order changed.
func sortSpecs(f *syntax.File, specs []syntax.Spec) bool {
	if len(specs) <= 1 {
		return false
	}
	sorted := slices.IsSortedFunc(specs, compareSpecs)
	if sorted {
		return false
	}

	type span struct{ start, end syntax.Pos }
	pos := make([]span, len(specs))
	for i, s := range specs {
		pos[i] = span{s.Pos(), s.End()}
	}

	// Comments from the start of the first spec's line to the end of the
	// last spec's line belong to the specs.
	firstLine := pos[0].start.Line
	lastLine := pos[len(pos)-1].end.Line
	places := map[*syntax.ImportSpec][]commentPlace{}
	specIndex := 0
	for _, g := range f.Comments {
		gl := g.Pos().Line
		if gl < firstLine || g.End().Line > lastLine {
			continue
		}
		for specIndex+1 < len(specs) && pos[specIndex+1].start.Offset <= g.Pos().Offset {
			specIndex++
		}
		left := false
		if specIndex == 0 && pos[0].start.Offset > g.Pos().Offset {
			left = true
		} else if specIndex+1 < len(specs) && pos[specIndex].start.Line+1 == gl {
			specIndex++
			left = true
		}
		s := specs[specIndex].(*syntax.ImportSpec)
		places[s] = append(places[s], commentPlace{left: left, g: g})
	}

	slices.SortStableFunc(specs, compareSpecs)

	// Give the sorted specs the positions of the original slots and move
	// their comments along.
	for i, s := range specs {
		s := s.(*syntax.ImportSpec)
		start := pos[i].start
		if s.Name != nil {
			s.Name.NamePos = start
			start = start.Advance(s.Name.Name + " ")
		}
		s.Path.ValuePos = start
		s.EndPos = pos[i].end
		for _, pl := range places[s] {
			for _, c := range pl.g.List {
				if pl.left {
					c.Slash = syntax.Pos{File: pos[i].start.File, Offset: pos[i].start.Offset - 1, Line: pos[i].start.Line, Col: pos[i].start.Col - 1}
				} else {
					c.Slash = pos[i].end
				}
			}
		}
	}
	return true
}

func compareSpecs(a, b syntax.Spec) int {
	if r := cmp.Compare(importPath(a), importPath(b)); r != 0 {
		return r
	}
	return cmp.Compare(importName(a), importName(b))
}
