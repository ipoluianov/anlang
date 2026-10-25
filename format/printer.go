// Based on go/printer/printer.go from the Go sources,
// Copyright The Go Authors, BSD-style license (third_party/go/LICENSE).

// Package format prints Anlang syntax trees in canonical form: the output
// of anlang fmt (lex.source.canonical). For the part of Anlang shared with Go
// the canonical form is the one produced by gofmt, which lets the Go
// sources serve as a test oracle.
package format

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode"

	"an/syntax"
)

const (
	maxNewlines = 2 // max. number of newlines between source text
	infinity    = 1 << 30
)

type whiteSpace byte

const (
	ignore   = whiteSpace(0)
	blank    = whiteSpace(' ')
	vtab     = whiteSpace('\v')
	newline  = whiteSpace('\n')
	formfeed = whiteSpace('\f')
	indent   = whiteSpace('>')
	unindent = whiteSpace('<')
)

// pmode is a printer mode toggled by print.
type pmode int

const (
	noExtraBlank     pmode = 1 << iota // disables extra blank after /*-style comment
	noExtraLinebreak                   // disables extra line break after /*-style comment
)

// word is a contextual word (requires, interrupt, ...) printed like an
// identifier.
type word string

type commentInfo struct {
	cindex         int                  // index of the next comment
	comment        *syntax.CommentGroup // = printer.comments[cindex-1]; or nil
	commentOffset  int                  // offset of the first comment of comment; or infinity
	commentNewline bool                 // true if the comment group contains newlines
}

type printer struct {
	raw bool // no tabwriter: used to compute node sizes

	output       []byte
	indent       int         // current indentation
	level        int         // level > 0: inside a composite literal
	mode         pmode       // current printer mode
	endAlignment bool        // if set, terminate alignment immediately
	impliedSemi  bool        // if set, a linebreak implies a semicolon
	lastTok      syntax.Kind // last token printed (Illegal if it's whitespace)
	prevOpen     syntax.Kind // previous non-brace "open" token: ( or [ or Illegal
	wsbuf        []whiteSpace

	// pos is the current position in source space, out in output space,
	// last the value of pos after the last writeString.
	pos, out, last syntax.Pos
	linePtr        *int // if set, record out.Line for the next token in *linePtr

	comments    []*syntax.CommentGroup // all source comments, in order
	allComments []*syntax.CommentGroup // same, for line comment queries
	commentInfo

	nodeSizes map[syntax.Node]int
}

func newPrinter(raw bool, nodeSizes map[syntax.Node]int) *printer {
	return &printer{
		raw:       raw,
		pos:       syntax.Pos{Line: 1, Col: 1},
		out:       syntax.Pos{Line: 1, Col: 1},
		wsbuf:     make([]whiteSpace, 0, 16),
		nodeSizes: nodeSizes,
	}
}

func (p *printer) commentsHaveNewline(list []*syntax.Comment) bool {
	line := list[0].Pos().Line
	for i, c := range list {
		if i > 0 && list[i].Pos().Line != line {
			return true
		}
		if t := c.Text; len(t) >= 2 && (t[1] == '/' || strings.Contains(t, "\n")) {
			return true
		}
	}
	return false
}

func (p *printer) nextComment() {
	for p.cindex < len(p.comments) {
		c := p.comments[p.cindex]
		p.cindex++
		if list := c.List; len(list) > 0 {
			p.comment = c
			p.commentOffset = list[0].Pos().Offset
			p.commentNewline = p.commentsHaveNewline(list)
			return
		}
	}
	p.commentOffset = infinity
}

// commentBefore reports whether the current comment group occurs before
// next and printing it does not introduce implicit semicolons.
func (p *printer) commentBefore(next syntax.Pos) bool {
	return p.commentOffset < next.Offset && (!p.impliedSemi || !p.commentNewline)
}

// commentSizeBefore returns the size of the comments before next.
func (p *printer) commentSizeBefore(next syntax.Pos) int {
	defer func(info commentInfo) { p.commentInfo = info }(p.commentInfo)
	size := 0
	for p.commentBefore(next) {
		for _, c := range p.comment.List {
			size += len(c.Text)
		}
		p.nextComment()
	}
	return size
}

// hasLineComment reports whether a comment group starts on the line of
// end, after end and before next: in the output such a comment ends the
// line of the field or spec that ends at end, because the next field or
// spec always starts a new line. (go/parser decides by the source layout,
// requiring next to be on a later line; that makes gofmt non-idempotent
// for "const(A/**/;B = 1)".)
func (p *printer) hasLineComment(end, next syntax.Pos) bool {
	for _, g := range p.allComments {
		start := g.Pos()
		if start.Offset < end.Offset {
			continue
		}
		if start.Line != end.Line {
			return false
		}
		return !next.IsValid() || start.Offset < next.Offset
	}
	return false
}

func (p *printer) recordLine(linePtr *int) { p.linePtr = linePtr }

func (p *printer) linesFrom(line int) int { return p.out.Line - line }

func (p *printer) writeIndent() {
	n := p.indent
	for i := 0; i < n; i++ {
		p.output = append(p.output, '\t')
	}
	p.pos.Offset += n
	p.pos.Col += n
	p.out.Col += n
}

// writeByte writes ch n times; it is used only for white space.
func (p *printer) writeByte(ch byte, n int) {
	if p.endAlignment {
		switch ch {
		case '\t', '\v':
			ch = ' '
		case '\n', '\f':
			ch = '\f'
			p.endAlignment = false
		}
	}
	if p.out.Col == 1 {
		p.writeIndent()
	}
	for i := 0; i < n; i++ {
		p.output = append(p.output, ch)
	}
	p.pos.Offset += n
	if ch == '\n' || ch == '\f' {
		p.pos.Line += n
		p.out.Line += n
		p.pos.Col = 1
		p.out.Col = 1
		return
	}
	p.pos.Col += n
	p.out.Col += n
}

// writeString writes a token, literal or comment. If isLit is set, s is
// escaped so the tabwriter passes it through unchanged.
func (p *printer) writeString(pos syntax.Pos, s string, isLit bool) {
	if p.out.Col == 1 {
		p.writeIndent()
	}
	if pos.IsValid() {
		p.pos = pos
	}
	if isLit {
		p.output = append(p.output, tabwriter.Escape)
	}
	p.output = append(p.output, s...)

	// Only '\n' ends a source line; a raw formfeed in a literal does not.
	// (go/printer also counts '\f', which misplaces following comments.)
	nlines := 0
	var li int
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			nlines++
			li = i
			p.endAlignment = true
		}
	}
	p.pos.Offset += len(s)
	if nlines > 0 {
		p.pos.Line += nlines
		p.out.Line += nlines
		c := len(s) - li
		p.pos.Col = c
		p.out.Col = c
	} else {
		p.pos.Col += len(s)
		p.out.Col += len(s)
	}
	if isLit {
		p.output = append(p.output, tabwriter.Escape)
	}
	p.last = p.pos
}

// writeCommentPrefix writes the white space before a comment.
func (p *printer) writeCommentPrefix(pos, next syntax.Pos, prev *syntax.Comment, tok syntax.Kind) {
	if len(p.output) == 0 {
		return
	}
	if pos.Line == p.last.Line && (prev == nil || prev.Text[1] != '/') {
		// Comment on the same line as the last item: separate with at
		// least one separator.
		hasSep := false
		if prev == nil {
			j := 0
			for i, ch := range p.wsbuf {
				switch ch {
				case blank:
					p.wsbuf[i] = ignore
					continue
				case vtab:
					hasSep = true
					continue
				case indent:
					continue
				}
				j = i
				break
			}
			p.writeWhitespace(j)
		}
		if !hasSep {
			sep := byte('\t')
			if pos.Line == next.Line {
				sep = ' '
			}
			p.writeByte(sep, 1)
		}
		return
	}

	// Comment on a different line: separate with at least one line break.
	droppedLinebreak := false
	j := 0
	for i, ch := range p.wsbuf {
		switch ch {
		case blank, vtab:
			p.wsbuf[i] = ignore
			continue
		case indent:
			continue
		case unindent:
			if i+1 < len(p.wsbuf) && p.wsbuf[i+1] == unindent {
				continue
			}
			if tok != syntax.RBrace && pos.Col == next.Col {
				continue
			}
		case newline, formfeed:
			p.wsbuf[i] = ignore
			droppedLinebreak = prev == nil
		}
		j = i
		break
	}
	p.writeWhitespace(j)

	n := 0
	if pos.IsValid() && p.last.IsValid() {
		n = max(pos.Line-p.last.Line, 0)
	}
	if p.indent == 0 && droppedLinebreak {
		n++
	}
	if n == 0 && prev != nil && prev.Text[1] == '/' {
		n = 1
	}
	if n > 0 {
		p.writeByte('\f', nlimit(n))
	}
}

func isBlank(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > ' ' {
			return false
		}
	}
	return true
}

func commonPrefix(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] && (a[i] <= ' ' || a[i] == '*') {
		i++
	}
	return a[0:i]
}

func trimRight(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) }

// stripCommonPrefix removes a common prefix from the lines of a /*-style
// comment, so that the comment is re-indented consistently.
func stripCommonPrefix(lines []string) {
	if len(lines) <= 1 {
		return
	}
	prefix := ""
	prefixSet := false
	if len(lines) > 2 {
		for i, line := range lines[1 : len(lines)-1] {
			if isBlank(line) {
				lines[1+i] = ""
			} else {
				if !prefixSet {
					prefix = line
					prefixSet = true
				}
				prefix = commonPrefix(prefix, line)
			}
		}
	}
	if !prefixSet {
		line := lines[len(lines)-1]
		prefix = commonPrefix(line, line)
	}

	lineOfStars := false
	if p, _, ok := strings.Cut(prefix, "*"); ok {
		prefix = strings.TrimSuffix(p, " ")
		lineOfStars = true
	} else {
		first := lines[0]
		if isBlank(first[2:]) {
			i := len(prefix)
			for n := 0; n < 3 && i > 0 && prefix[i-1] == ' '; n++ {
				i--
			}
			if i == len(prefix) && i > 0 && prefix[i-1] == '\t' {
				i--
			}
			prefix = prefix[0:i]
		} else {
			suffix := make([]byte, len(first))
			n := 2
			for n < len(first) && first[n] <= ' ' {
				suffix[n] = first[n]
				n++
			}
			if n > 2 && suffix[2] == '\t' {
				suffix = suffix[2:n]
			} else {
				suffix[0], suffix[1] = ' ', ' '
				suffix = suffix[0:n]
			}
			prefix = strings.TrimSuffix(prefix, string(suffix))
		}
	}

	last := lines[len(lines)-1]
	closing := "*/"
	before, _, _ := strings.Cut(last, closing)
	if isBlank(before) {
		if lineOfStars {
			closing = " */"
		}
		lines[len(lines)-1] = prefix + closing
	} else {
		prefix = commonPrefix(prefix, last)
	}

	for i, line := range lines {
		if i > 0 && line != "" {
			lines[i] = line[len(prefix):]
		}
	}
}

func (p *printer) writeComment(c *syntax.Comment) {
	text := c.Text
	pos := c.Pos()
	if text[1] == '/' {
		p.writeString(pos, trimRight(text), true)
		return
	}
	lines := strings.Split(text, "\n")
	if pos.IsValid() && pos.Col == 1 && p.indent > 0 {
		for i, line := range lines[1:] {
			lines[1+i] = "   " + line
		}
	}
	stripCommonPrefix(lines)
	for i, line := range lines {
		if i > 0 {
			p.writeByte('\f', 1)
			pos = p.pos
		}
		if len(line) > 0 {
			p.writeString(pos, trimRight(line), true)
		}
	}
}

// writeCommentSuffix writes a line break after a comment if needed and
// processes leftover indentation.
func (p *printer) writeCommentSuffix(needsLinebreak bool) (wroteNewline, droppedFF bool) {
	for i, ch := range p.wsbuf {
		switch ch {
		case blank, vtab:
			p.wsbuf[i] = ignore
		case indent, unindent:
		case newline, formfeed:
			if needsLinebreak {
				needsLinebreak = false
				wroteNewline = true
			} else {
				if ch == formfeed {
					droppedFF = true
				}
				p.wsbuf[i] = ignore
			}
		}
	}
	p.writeWhitespace(len(p.wsbuf))
	if needsLinebreak {
		p.writeByte('\n', 1)
		wroteNewline = true
	}
	return
}

func (p *printer) containsLinebreak() bool {
	for _, ch := range p.wsbuf {
		if ch == newline || ch == formfeed {
			return true
		}
	}
	return false
}

// intersperseComments prints the comments before next together with the
// buffered white space.
func (p *printer) intersperseComments(next syntax.Pos, tok syntax.Kind) (wroteNewline, droppedFF bool) {
	var last *syntax.Comment
	for p.commentBefore(next) {
		for _, c := range p.comment.List {
			p.writeCommentPrefix(c.Pos(), next, last, tok)
			p.writeComment(c)
			last = c
		}
		p.nextComment()
	}
	if last == nil {
		return
	}
	needsLinebreak := false
	if p.mode&noExtraBlank == 0 &&
		last.Text[1] == '*' && last.Pos().Line == next.Line &&
		tok != syntax.Comma &&
		(tok != syntax.RParen || p.prevOpen == syntax.LParen) &&
		(tok != syntax.RBrack || p.prevOpen == syntax.LBrack) {
		if p.containsLinebreak() && p.mode&noExtraLinebreak == 0 && p.level == 0 {
			needsLinebreak = true
		} else {
			p.writeByte(' ', 1)
		}
	}
	if last.Text[1] == '/' || tok == syntax.EOF || tok == syntax.RBrace && p.mode&noExtraLinebreak == 0 {
		needsLinebreak = true
	}
	return p.writeCommentSuffix(needsLinebreak)
}

// writeWhitespace writes the first n white space entries.
func (p *printer) writeWhitespace(n int) {
	for i := 0; i < n; i++ {
		switch ch := p.wsbuf[i]; ch {
		case ignore:
		case indent:
			p.indent++
		case unindent:
			p.indent--
			if p.indent < 0 {
				p.indent = 0
			}
		case newline, formfeed:
			// A line break followed by a correcting unindent is swapped
			// with it, so that labels are positioned correctly.
			if i+1 < n && p.wsbuf[i+1] == unindent {
				p.wsbuf[i], p.wsbuf[i+1] = unindent, formfeed
				i--
				continue
			}
			p.writeByte(byte(ch), 1)
		default:
			p.writeByte(byte(ch), 1)
		}
	}
	l := copy(p.wsbuf, p.wsbuf[n:])
	p.wsbuf = p.wsbuf[:l]
}

func nlimit(n int) int { return min(n, maxNewlines) }

// mayCombine reports whether the previous token and the next character
// would form a different token without a separating blank.
func mayCombine(prev syntax.Kind, next byte) bool {
	switch prev {
	case syntax.Int:
		return next == '.'
	case syntax.Add:
		return next == '+'
	case syntax.Sub:
		return next == '-'
	case syntax.Quo:
		return next == '*'
	case syntax.Lss:
		return next == '-' || next == '<'
	case syntax.And:
		return next == '&' || next == '^'
	}
	return false
}

func (p *printer) setPos(pos syntax.Pos) {
	if pos.IsValid() {
		p.pos = pos
	}
}

// print prints a list of items: tokens, names, literals, words, white
// space and mode toggles. White space is buffered until the next token,
// so that comments before the token can be placed well.
func (p *printer) print(args ...any) {
	for _, arg := range args {
		var data string
		var isLit, impliedSemi bool

		switch p.lastTok {
		case syntax.Illegal:
		case syntax.LParen, syntax.LBrack:
			p.prevOpen = p.lastTok
		default:
			p.prevOpen = syntax.Illegal
		}

		switch x := arg.(type) {
		case pmode:
			p.mode ^= x
			continue
		case whiteSpace:
			if x == ignore {
				continue
			}
			if len(p.wsbuf) == cap(p.wsbuf) {
				p.writeWhitespace(len(p.wsbuf))
			}
			p.wsbuf = append(p.wsbuf, x)
			if x == newline || x == formfeed {
				p.impliedSemi = false
			}
			p.lastTok = syntax.Illegal
			continue
		case *syntax.Name:
			data = x.Name
			impliedSemi = true
			p.lastTok = syntax.Ident
		case word:
			data = string(x)
			p.lastTok = syntax.Ident
		case *syntax.BasicLit:
			data = x.Value
			isLit = true
			impliedSemi = true
			p.lastTok = x.Kind
		case syntax.Kind:
			s := x.String()
			if mayCombine(p.lastTok, s[0]) {
				p.wsbuf = append(p.wsbuf[:0], ' ')
			}
			data = s
			switch x {
			case syntax.Break, syntax.Continue, syntax.Fallthrough, syntax.Return,
				syntax.Inc, syntax.Dec, syntax.RParen, syntax.RBrack, syntax.RBrace:
				impliedSemi = true
			}
			p.lastTok = x
		case string:
			data = x
			isLit = true
			impliedSemi = true
			p.lastTok = syntax.String
		default:
			panic(fmt.Sprintf("format: unsupported print argument %T", arg))
		}

		next := p.pos
		wroteNewline, droppedFF := p.flush(next, p.lastTok)

		// Intersperse extra newlines present in the source, if they do not
		// cause extra semicolons.
		if !p.impliedSemi {
			n := nlimit(next.Line - p.pos.Line)
			if wroteNewline && n == maxNewlines {
				n = maxNewlines - 1
			}
			if n > 0 {
				ch := byte('\n')
				if droppedFF {
					ch = '\f'
				}
				p.writeByte(ch, n)
				impliedSemi = false
			}
		}

		if p.linePtr != nil {
			*p.linePtr = p.out.Line
			p.linePtr = nil
		}

		p.writeString(next, data, isLit)
		p.impliedSemi = impliedSemi
	}
}

// flush prints pending comments and white space before next.
func (p *printer) flush(next syntax.Pos, tok syntax.Kind) (wroteNewline, droppedFF bool) {
	if p.commentBefore(next) {
		return p.intersperseComments(next, tok)
	}
	p.writeWhitespace(len(p.wsbuf))
	return
}

// ----------------------------------------------------------------------------
// Trimmer

// trimmer strips tabwriter.Escape characters and trailing blanks and
// tabs, and converts formfeeds and vtabs into newlines and tabs.
type trimmer struct {
	output io.Writer
	state  int
	space  []byte
}

const (
	inSpace = iota
	inEscape
	inText
)

func (t *trimmer) resetSpace() {
	t.state = inSpace
	t.space = t.space[0:0]
}

var aNewline = []byte("\n")

func (t *trimmer) Write(data []byte) (n int, err error) {
	m := 0
	var b byte
	for n, b = range data {
		if b == '\v' {
			b = '\t'
		}
		switch t.state {
		case inSpace:
			switch b {
			case '\t', ' ':
				t.space = append(t.space, b)
			case '\n', '\f':
				t.resetSpace()
				_, err = t.output.Write(aNewline)
			case tabwriter.Escape:
				_, err = t.output.Write(t.space)
				t.state = inEscape
				m = n + 1
			default:
				_, err = t.output.Write(t.space)
				t.state = inText
				m = n
			}
		case inEscape:
			if b == tabwriter.Escape {
				_, err = t.output.Write(data[m:n])
				t.resetSpace()
			}
		case inText:
			switch b {
			case '\t', ' ':
				_, err = t.output.Write(data[m:n])
				t.resetSpace()
				t.space = append(t.space, b)
			case '\n', '\f':
				_, err = t.output.Write(data[m:n])
				t.resetSpace()
				if err == nil {
					_, err = t.output.Write(aNewline)
				}
			case tabwriter.Escape:
				_, err = t.output.Write(data[m:n])
				t.state = inEscape
				m = n + 1
			}
		}
		if err != nil {
			return
		}
	}
	n = len(data)
	switch t.state {
	case inEscape, inText:
		_, err = t.output.Write(data[m:n])
		t.resetSpace()
	}
	return
}

// ----------------------------------------------------------------------------
// Entry points

// File returns the canonical form of a parsed file.
func File(f *syntax.File) ([]byte, error) {
	var buf bytes.Buffer
	sortImports(f)
	p := newPrinter(false, map[syntax.Node]int{})
	p.comments = f.Comments
	p.allComments = f.Comments
	p.nextComment()
	p.file(f)
	p.impliedSemi = false // EOF acts like a newline
	p.flush(syntax.Pos{Offset: infinity, Line: infinity}, syntax.EOF)

	tw := tabwriter.NewWriter(&trimmer{output: &buf}, 0, 8, 1, ' ', tabwriter.DiscardEmptyColumns|tabwriter.TabIndent)
	if _, err := tw.Write(p.output); err != nil {
		return nil, err
	}
	if err := tw.Flush(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// maxPasses bounds the number of formatting passes in Source.
const maxPasses = 4

// Source parses src in the given mode and returns its canonical form.
//
// The canonical form is a fixed point of formatting: Source formats the
// output again until it no longer changes. A few of the layout heuristics
// inherited from gofmt decide by the source layout and can give a
// different result on their own output (for example the re-indentation
// of /*-style comments); iterating makes anlang fmt idempotent, as
// tool.cmd.fmt requires. For canonical input the first pass already
// returns the input.
func Source(name string, src []byte, mode syntax.Mode) ([]byte, []*syntax.Error) {
	out := src
	for pass := 0; pass < maxPasses; pass++ {
		f, errs := syntax.ParseMode(name, out, mode)
		if len(errs) > 0 {
			if pass > 0 {
				// The printer produced invalid code: an internal error.
				errs = append([]*syntax.Error{{Pos: syntax.Pos{File: name}, Msg: "anlang fmt: formatting produced invalid code"}}, errs...)
			}
			return nil, errs
		}
		next, err := File(f)
		if err != nil {
			return nil, []*syntax.Error{{Pos: syntax.Pos{File: name}, Msg: err.Error()}}
		}
		if bytes.Equal(next, out) {
			return out, nil
		}
		out = next
	}
	return out, nil
}

// sizeCounter counts bytes and notes whether a line break was written.
type sizeCounter struct {
	hasNewline bool
	size       int
}

func (c *sizeCounter) Write(p []byte) (int, error) {
	if !c.hasNewline {
		for _, b := range p {
			if b == '\n' || b == '\f' {
				c.hasNewline = true
				break
			}
		}
	}
	c.size += len(p)
	return len(p), nil
}

// nodeSize returns the size of n formatted on one line, or a value
// greater than maxSize if it does not fit on one line within maxSize.
func (p *printer) nodeSize(n syntax.Node, maxSize int) int {
	if size, found := p.nodeSizes[n]; found {
		return size
	}
	size := maxSize + 1
	p.nodeSizes[n] = size

	q := newPrinter(true, p.nodeSizes)
	q.nextComment()
	switch n := n.(type) {
	case syntax.Expr:
		q.expr(n)
	case syntax.Stmt:
		if _, ok := n.(*syntax.LabeledStmt); ok {
			q.indent = 1
		}
		q.stmt(n, false)
	case syntax.Decl:
		q.decl(n)
	case syntax.Spec:
		q.spec(n, 1, false)
	default:
		return size
	}
	q.flush(syntax.Pos{Offset: infinity, Line: infinity}, syntax.EOF)
	var counter sizeCounter
	if _, err := (&trimmer{output: &counter}).Write(q.output); err != nil {
		return size
	}
	if counter.size <= maxSize && !counter.hasNewline {
		size = counter.size
		p.nodeSizes[n] = size
	}
	return size
}

// Canonical checks that src is in canonical form (lex.source.canonical).
// It returns nil if it is, and otherwise an error at the first line that
// differs. Syntax errors are returned as they are.
func Canonical(name string, src []byte) []*syntax.Error {
	out, errs := Source(name, src, syntax.AnlangMode)
	if len(errs) > 0 {
		return errs
	}
	if bytes.Equal(out, src) {
		return nil
	}
	line := 1
	for i := 0; i < len(src) && i < len(out) && src[i] == out[i]; i++ {
		if src[i] == '\n' {
			line++
		}
	}
	return []*syntax.Error{{
		Pos:  syntax.Pos{File: name, Line: line, Col: 1},
		Rule: "lex.source.canonical",
		Msg:  "file is not in canonical form; run anlang fmt",
	}}
}
