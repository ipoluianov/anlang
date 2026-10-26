package modfile

import (
	"bytes"
	"sort"
	"strings"
)

// order is the canonical order of directives (tool.mod.format).
var order = map[string]int{"module": 0, "anlang": 1, "targets": 2, "effects": 3, "generate": 4}

// keyWidth is the column at which directive values start: the length of
// the longest single-line directive name plus one space.
const keyWidth = len("targets") + 1

// Format returns the canonical form of an anlang.mod file: directives in
// canonical order, one per line, values aligned, comments kept with the
// directive that follows them or that they end. The file must be valid.
func Format(data []byte) []byte {
	type entry struct {
		comments []string // whole-line comments before the directive
		key      string
		value    string
		trailing string // comment at the end of the line, without "//"
		rules    []rule // the rules of the effects directive
	}
	var entries []*entry
	var pending []string
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
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
		if e.key == "effects" {
			e.rules, i = formatRules(lines, i+1)
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return order[entries[i].key] < order[entries[j].key] })

	var b bytes.Buffer
	for _, e := range entries {
		for _, c := range e.comments {
			b.WriteString(c + "\n")
		}
		if e.key == "effects" {
			b.WriteString("effects (")
			if e.trailing != "" {
				b.WriteString(" // " + e.trailing)
			}
			b.WriteString("\n")
			writeRules(&b, e.rules)
			b.WriteString(")\n")
			continue
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

// rule is an effect rule with its comments.
type rule struct {
	comments []string
	pattern  string
	word     string
	names    []string
	trailing string
}

// formatRules reads the rules of an effects directive from lines[i:]
// up to the closing ")" and returns them sorted by pattern, with the
// index of the closing line.
func formatRules(lines []string, i int) ([]rule, int) {
	var rules []rule
	var pending []string
	for ; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "//") {
			pending = append(pending, "// "+strings.TrimSpace(strings.TrimPrefix(text, "//")))
			continue
		}
		if text == ")" {
			break
		}
		r := rule{comments: pending}
		pending = nil
		if j := strings.Index(text, "//"); j >= 0 {
			r.trailing = strings.TrimSpace(text[j+2:])
			text = strings.TrimSpace(text[:j])
		}
		m := effectRule.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		r.pattern, r.word = m[1], m[2]
		for _, name := range strings.Split(m[3], ",") {
			if name = strings.TrimSpace(name); name != "" {
				r.names = append(r.names, name)
			}
		}
		sort.Strings(r.names)
		rules = append(rules, r)
	}
	sort.SliceStable(rules, func(a, b int) bool {
		if rules[a].pattern != rules[b].pattern {
			return rules[a].pattern < rules[b].pattern
		}
		return rules[a].word < rules[b].word
	})
	return rules, i
}

// writeRules writes rules indented by a tab, with the patterns padded to
// the longest one and "deny" padded to the width of "allow"
// (tool.mod.format).
func writeRules(b *bytes.Buffer, rules []rule) {
	width := 0
	for _, r := range rules {
		width = max(width, len(r.pattern))
	}
	for _, r := range rules {
		for _, c := range r.comments {
			b.WriteString("\t" + c + "\n")
		}
		b.WriteString("\t" + r.pattern + strings.Repeat(" ", width-len(r.pattern)+1))
		b.WriteString(r.word + strings.Repeat(" ", len("allow")-len(r.word)+1))
		b.WriteString("{" + strings.Join(r.names, ", ") + "}")
		if r.trailing != "" {
			b.WriteString(" // " + r.trailing)
		}
		b.WriteString("\n")
	}
}
