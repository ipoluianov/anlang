package modfile

import (
	"bytes"
	"sort"
	"strings"
)

// order is the canonical order of directives (tool.mod.format).
var order = map[string]int{"module": 0, "kern": 1, "targets": 2, "effects": 3, "generate": 4}

// keyWidth is the column at which directive values start: the length of
// the longest single-line directive name plus one space.
const keyWidth = len("targets") + 1

// Format returns the canonical form of a kern.mod file: directives in
// canonical order, one per line, values aligned, comments kept with the
// directive that follows them or that they end. The file must be valid.
func Format(data []byte) []byte {
	type entry struct {
		comments []string // whole-line comments before the directive
		key      string
		value    string
		trailing string // comment at the end of the line, without "//"
	}
	var entries []*entry
	var pending []string
	for _, raw := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "//") {
			pending = append(pending, "// "+strings.TrimSpace(strings.TrimPrefix(text, "//")))
			continue
		}
		e := &entry{comments: pending}
		pending = nil
		if i := strings.Index(text, "//"); i >= 0 {
			e.trailing = strings.TrimSpace(text[i+2:])
			text = strings.TrimSpace(text[:i])
		}
		fields := strings.Fields(text)
		e.key = fields[0]
		value := strings.TrimSpace(text[len(e.key):])
		if e.key == "targets" {
			var names []string
			for _, t := range strings.Split(value, ",") {
				names = append(names, strings.TrimSpace(t))
			}
			value = strings.Join(names, ", ")
		}
		e.value = value
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return order[entries[i].key] < order[entries[j].key] })

	var b bytes.Buffer
	for _, e := range entries {
		for _, c := range e.comments {
			b.WriteString(c + "\n")
		}
		b.WriteString(e.key)
		b.WriteString(strings.Repeat(" ", keyWidth-len(e.key)))
		b.WriteString(e.value)
		if e.trailing != "" {
			b.WriteString(" // " + e.trailing)
		}
		b.WriteString("\n")
	}
	for _, c := range pending {
		b.WriteString(c + "\n")
	}
	return b.Bytes()
}
