// Based on go/printer/nodes.go from the Go sources,
// Copyright The Go Authors, BSD-style license (third_party/go/LICENSE).
// Extended with the Kern constructs of the novelty inventory.

package format

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"kern/syntax"
)

const (
	lowestPrec  = 0
	unaryPrec   = 6
	highestPrec = 7
)

func precedence(k syntax.Kind) int {
	switch k {
	case syntax.LOr:
		return 1
	case syntax.LAnd:
		return 2
	case syntax.Eql, syntax.Neq, syntax.Lss, syntax.Leq, syntax.Gtr, syntax.Geq:
		return 3
	case syntax.Add, syntax.Sub, syntax.Or, syntax.Xor:
		return 4
	case syntax.Mul, syntax.Quo, syntax.Rem, syntax.Shl, syntax.Shr, syntax.And, syntax.AndNot:
		return 5
	}
	return lowestPrec
}

// linebreak prints as many newlines as needed, but at least min, to get
// to line. ws is printed before the first line break; if newSection is
// set, the first break is a formfeed. It returns 0 if no line break was
// printed, 1 for one newline and more for a formfeed or several newlines.
func (p *printer) linebreak(line, min int, ws whiteSpace, newSection bool) (nbreaks int) {
	n := max(nlimit(line-p.pos.Line), min)
	if n > 0 {
		p.print(ws)
		if newSection {
			p.print(formfeed)
			n--
			nbreaks = 2
		}
		nbreaks += n
		for ; n > 0; n-- {
			p.print(newline)
		}
	}
	return
}

type exprListMode uint

const (
	commaTerm exprListMode = 1 << iota // list is optionally terminated by a comma
	noIndent                           // no extra indentation in multi-line lists
)

func (p *printer) identList(list []*syntax.Name, indent bool) {
	xlist := make([]syntax.Expr, len(list))
	for i, x := range list {
		xlist[i] = x
	}
	var mode exprListMode
	if !indent {
		mode = noIndent
	}
	p.exprList(syntax.Pos{}, xlist, 1, mode, syntax.Pos{})
}

// exprList prints a list of expressions. If the list spans several
// source lines, the original line breaks are respected. prev and next
// are the positions of the tokens before and after the list.
func (p *printer) exprList(prev syntax.Pos, list []syntax.Expr, depth int, mode exprListMode, next syntax.Pos) {
	if len(list) == 0 {
		return
	}
	line := list[0].Pos().Line
	endLine := list[len(list)-1].End().Line

	if prev.IsValid() && prev.Line == line && line == endLine {
		// All entries on one line.
		for i, x := range list {
			if i > 0 {
				p.setPos(x.Pos())
				p.print(syntax.Comma, blank)
			}
			p.expr0(x, depth)
		}
		return
	}

	// The list spans several lines; use source positions for breaks.
	ws := ignore
	if mode&noIndent == 0 {
		ws = indent
	}
	prevBreak := -1
	if prev.IsValid() && prev.Line < line && p.linebreak(line, 0, ws, true) > 0 {
		ws = ignore
		prevBreak = 0
	}

	size := 0
	lnsum := 0.0
	count := 0
	prevLine := prev.Line
	for i, x := range list {
		line = x.Pos().Line

		// Use formfeed for the next line break unless the sizes of this
		// and the previous entries suggest aligning them in columns.
		useFF := true
		prevSize := size
		const infinity = 1e6
		size = p.nodeSize(x, infinity)
		pair, isPair := x.(*syntax.KeyValueExpr)
		if size <= infinity && prev.IsValid() && next.IsValid() {
			if isPair {
				size = p.nodeSize(pair.Key, infinity)
			}
		} else {
			size = 0
		}
		if prevSize > 0 && size > 0 {
			const smallSize = 40
			if count == 0 || prevSize <= smallSize && size <= smallSize {
				useFF = false
			} else {
				const r = 2.5
				geomean := math.Exp(lnsum / float64(count))
				ratio := float64(size) / geomean
				useFF = r*ratio <= 1 || r <= ratio
			}
		}

		needsLinebreak := 0 < prevLine && prevLine < line
		if i > 0 {
			if !needsLinebreak {
				p.setPos(x.Pos())
			}
			p.print(syntax.Comma)
			needsBlank := true
			if needsLinebreak {
				nbreaks := p.linebreak(line, 0, ws, useFF || prevBreak+1 < i)
				if nbreaks > 0 {
					ws = ignore
					prevBreak = i
					needsBlank = false
				}
				if nbreaks > 1 {
					lnsum = 0
					count = 0
				}
			}
			if needsBlank {
				p.print(blank)
			}
		}

		if len(list) > 1 && isPair && size > 0 && needsLinebreak {
			// A key:value pair on its own line: put the key in a column
			// so that consecutive entries align.
			p.expr(pair.Key)
			p.setPos(pair.Colon)
			p.print(syntax.Colon, vtab)
			p.expr(pair.Value)
		} else {
			p.expr0(x, depth)
		}

		if size > 0 {
			lnsum += math.Log(float64(size))
			count++
		}
		prevLine = line
	}

	if mode&commaTerm != 0 && next.IsValid() && p.pos.Line < next.Line {
		// A terminating comma when the next token is on a new line.
		p.print(syntax.Comma)
		if ws == ignore && mode&noIndent == 0 {
			p.print(unindent)
		}
		p.print(formfeed)
		return
	}
	if ws == ignore && mode&noIndent == 0 {
		p.print(unindent)
	}
}

type paramMode int

const (
	funcParam    paramMode = iota
	funcTParam             // type parameters of a function
	typeTParam             // type parameters of a type
	variantParam           // fields of an enum variant (Kern)
)

func (p *printer) parameters(fields *syntax.FieldList, mode paramMode) {
	openTok, closeTok := syntax.LParen, syntax.RParen
	switch mode {
	case funcTParam, typeTParam:
		openTok, closeTok = syntax.LBrack, syntax.RBrack
	case variantParam:
		openTok, closeTok = syntax.LBrace, syntax.RBrace
	}
	p.setPos(fields.Opening)
	p.print(openTok)
	if len(fields.List) > 0 {
		prevLine := fields.Opening.Line
		ws := indent
		for i, par := range fields.List {
			parLineBeg := par.Pos().Line
			parLineEnd := par.End().Line
			needsLinebreak := 0 < prevLine && prevLine < parLineBeg
			if i > 0 {
				if !needsLinebreak {
					p.setPos(par.Pos())
				}
				p.print(syntax.Comma)
			}
			if needsLinebreak && p.linebreak(parLineBeg, 0, ws, true) > 0 {
				ws = ignore
			} else if i > 0 {
				p.print(blank)
			}
			if len(par.Names) > 0 {
				p.identList(par.Names, ws == indent)
				p.print(blank)
			}
			if par.Inout.IsValid() {
				p.setPos(par.Inout)
				p.print(syntax.Inout, blank)
			}
			p.expr(stripParensAlways(par.Type))
			prevLine = parLineEnd
		}

		if closing := fields.Closing.Line; 0 < prevLine && prevLine < closing {
			p.print(syntax.Comma)
			p.linebreak(closing, 0, ignore, true)
		} else if mode == typeTParam && fields.NumFields() == 1 && combinesWithName(stripParensAlways(fields.List[0].Type)) {
			// [P *T,] keeps the list from being read as an array length.
			p.print(syntax.Comma)
		}
		if ws == ignore {
			p.print(unindent)
		}
	}
	p.setPos(fields.Closing)
	p.print(closeTok)
}

// combinesWithName reports whether a name followed by x syntactically
// combines into another valid value expression.
func combinesWithName(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.StarExpr:
		return !isTypeElem(x.X)
	case *syntax.BinaryExpr:
		return combinesWithName(x.X) && !isTypeElem(x.Y)
	case *syntax.ParenExpr:
		return !isTypeElem(x.X)
	}
	return false
}

func isTypeElem(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.ArrayType, *syntax.StructType, *syntax.FuncType, *syntax.InterfaceType,
		*syntax.MapType, *syntax.ChanType, *syntax.EnumType:
		return true
	case *syntax.UnaryExpr:
		return x.Op == syntax.Tilde
	case *syntax.BinaryExpr:
		return isTypeElem(x.X) || isTypeElem(x.Y)
	case *syntax.ParenExpr:
		return isTypeElem(x.X)
	}
	return false
}

func (p *printer) signature(sig *syntax.FuncType) {
	if sig.TypeParams != nil {
		p.parameters(sig.TypeParams, funcTParam)
	}
	if sig.Params != nil {
		p.parameters(sig.Params, funcParam)
	} else {
		p.print(syntax.LParen, syntax.RParen)
	}
	res := sig.Results
	if n := res.NumFields(); n > 0 {
		p.print(blank)
		if n == 1 && res.List[0].Names == nil && !res.List[0].Inout.IsValid() {
			p.expr(stripParensAlways(res.List[0].Type))
			return
		}
		p.parameters(res, funcParam)
	}
}

func identListSize(list []*syntax.Name, maxSize int) (size int) {
	for i, x := range list {
		if i > 0 {
			size += len(", ")
		}
		size += utf8.RuneCountInString(x.Name)
		if size >= maxSize {
			break
		}
	}
	return
}

func (p *printer) isOneLineFieldList(list []*syntax.Field) bool {
	if len(list) != 1 {
		return false
	}
	f := list[0]
	if f.Tag != nil {
		return false
	}
	const maxSize = 30
	namesSize := identListSize(f.Names, maxSize)
	if namesSize > 0 {
		namesSize = 1
	}
	typeSize := p.nodeSize(f.Type, maxSize)
	return namesSize+typeSize <= maxSize
}

// structType prints a struct type, including Kern's leading invariant and
// layout clauses.
func (p *printer) structType(t *syntax.StructType) {
	p.print(syntax.Struct)
	lbrace, rbrace, list := t.Lbrace, t.Rbrace, t.Fields
	hasComments := p.commentBefore(rbrace)
	srcIsOneLine := lbrace.IsValid() && rbrace.IsValid() && lbrace.Line == rbrace.Line

	if !hasComments && srcIsOneLine && len(t.Clauses) == 0 {
		if len(list) == 0 {
			p.setPos(lbrace)
			p.print(syntax.LBrace)
			p.setPos(rbrace)
			p.print(syntax.RBrace)
			return
		}
		if p.isOneLineFieldList(list) {
			p.setPos(lbrace)
			p.print(syntax.LBrace, blank)
			f := list[0]
			for i, x := range f.Names {
				if i > 0 {
					p.print(syntax.Comma, blank)
				}
				p.expr(x)
			}
			if len(f.Names) > 0 {
				p.print(blank)
			}
			p.expr(f.Type)
			p.print(blank)
			p.setPos(rbrace)
			p.print(syntax.RBrace)
			return
		}
	}

	p.print(blank)
	p.setPos(lbrace)
	p.print(syntax.LBrace, indent)
	if hasComments || len(list) > 0 || len(t.Clauses) > 0 {
		p.print(formfeed)
	}

	var line int
	for i, c := range t.Clauses {
		if i > 0 {
			p.linebreak(c.WordPos.Line, 1, ignore, p.linesFrom(line) > 0)
		}
		p.recordLine(&line)
		p.setPos(c.WordPos)
		p.print(word(c.Word), blank)
		p.expr(c.X)
	}
	if len(t.Clauses) > 0 && len(list) > 0 {
		// Clauses and fields are separate alignment sections.
		p.linebreak(list[0].Pos().Line, 1, ignore, true)
	}

	sep := vtab
	if len(list) == 1 {
		sep = blank
	}
	for i, f := range list {
		if i > 0 {
			p.linebreak(f.Pos().Line, 1, ignore, p.linesFrom(line) > 0)
		}
		extraTabs := 0
		p.recordLine(&line)
		if len(f.Names) > 0 {
			p.identList(f.Names, false)
			p.print(sep)
			p.expr(f.Type)
			extraTabs = 1
		} else {
			p.expr(f.Type)
			extraTabs = 2
		}
		if f.Tag != nil {
			if len(f.Names) > 0 && sep == vtab {
				p.print(sep)
			}
			p.print(sep)
			p.expr(f.Tag)
			extraTabs = 0
		}
		next := rbrace
		if i+1 < len(list) {
			next = list[i+1].Pos()
		}
		if p.hasLineComment(f.End(), next) {
			for ; extraTabs > 0; extraTabs-- {
				p.print(sep)
			}
		}
	}
	p.print(unindent, formfeed)
	p.setPos(rbrace)
	p.print(syntax.RBrace)
}

func (p *printer) interfaceType(t *syntax.InterfaceType) {
	p.print(syntax.Interface)
	lbrace, rbrace, list := t.Lbrace, t.Rbrace, t.Elems
	hasComments := p.commentBefore(rbrace)
	srcIsOneLine := lbrace.IsValid() && rbrace.IsValid() && lbrace.Line == rbrace.Line

	if !hasComments && srcIsOneLine {
		if len(list) == 0 {
			p.setPos(lbrace)
			p.print(syntax.LBrace)
			p.setPos(rbrace)
			p.print(syntax.RBrace)
			return
		}
		if p.isOneLineFieldList(list) {
			p.setPos(lbrace)
			p.print(syntax.LBrace, blank)
			f := list[0]
			if len(f.Names) > 0 {
				p.expr(f.Names[0])
				p.signature(f.Type.(*syntax.FuncType))
			} else {
				p.expr(f.Type)
			}
			p.print(blank)
			p.setPos(rbrace)
			p.print(syntax.RBrace)
			return
		}
	}

	p.print(blank)
	p.setPos(lbrace)
	p.print(syntax.LBrace, indent)
	if hasComments || len(list) > 0 {
		p.print(formfeed)
	}
	var line int
	for i, f := range list {
		if i > 0 {
			p.linebreak(f.Pos().Line, 1, ignore, p.linesFrom(line) > 0)
		}
		p.recordLine(&line)
		if len(f.Names) > 0 {
			p.expr(f.Names[0])
			p.signature(f.Type.(*syntax.FuncType))
		} else {
			p.expr(f.Type)
		}
	}
	p.print(unindent, formfeed)
	p.setPos(rbrace)
	p.print(syntax.RBrace)
}

// enumType prints enum { ... } with one variant per line (Kern).
func (p *printer) enumType(t *syntax.EnumType) {
	p.print(syntax.Enum)
	if len(t.Variants) == 0 && !p.commentBefore(t.Rbrace) {
		p.setPos(t.Lbrace)
		p.print(syntax.LBrace)
		p.setPos(t.Rbrace)
		p.print(syntax.RBrace)
		return
	}
	p.print(blank)
	p.setPos(t.Lbrace)
	p.print(syntax.LBrace, indent, formfeed)
	var line int
	for i, v := range t.Variants {
		if i > 0 {
			p.linebreak(v.Pos().Line, 1, ignore, p.linesFrom(line) > 0)
		}
		p.recordLine(&line)
		p.expr(v.Name)
		if v.Fields != nil {
			p.parameters(v.Fields, variantParam)
		}
	}
	p.print(unindent, formfeed)
	p.setPos(t.Rbrace)
	p.print(syntax.RBrace)
}

// ----------------------------------------------------------------------------
// Expressions

func walkBinary(e *syntax.BinaryExpr) (has4, has5 bool, maxProblem int) {
	switch precedence(e.Op) {
	case 4:
		has4 = true
	case 5:
		has5 = true
	}

	switch l := e.X.(type) {
	case *syntax.BinaryExpr:
		if precedence(l.Op) < precedence(e.Op) {
			// Parentheses will be inserted; treat as a ParenExpr.
			break
		}
		h4, h5, mp := walkBinary(l)
		has4 = has4 || h4
		has5 = has5 || h5
		maxProblem = max(maxProblem, mp)
	}

	switch r := e.Y.(type) {
	case *syntax.BinaryExpr:
		if precedence(r.Op) <= precedence(e.Op) {
			break
		}
		h4, h5, mp := walkBinary(r)
		has4 = has4 || h4
		has5 = has5 || h5
		maxProblem = max(maxProblem, mp)
	case *syntax.StarExpr:
		if e.Op == syntax.Quo { // `*/`
			maxProblem = 5
		}
	case *syntax.UnaryExpr:
		switch e.Op.String() + r.Op.String() {
		case "/*", "&&", "&^":
			maxProblem = 5
		case "++", "--":
			maxProblem = max(maxProblem, 4)
		}
	}
	return
}

func cutoff(e *syntax.BinaryExpr, depth int) int {
	has4, has5, maxProblem := walkBinary(e)
	if maxProblem > 0 {
		return maxProblem + 1
	}
	if has4 && has5 {
		if depth == 1 {
			return 5
		}
		return 4
	}
	if depth == 1 {
		return 6
	}
	return 4
}

func diffPrec(expr syntax.Expr, prec int) int {
	x, ok := expr.(*syntax.BinaryExpr)
	if !ok || prec != precedence(x.Op) {
		return 1
	}
	return 0
}

func reduceDepth(depth int) int {
	return max(depth-1, 1)
}

// binaryExpr formats a binary expression. Spaces around operators of
// precedence 4 and 5 depend on the cutoff: in Normal mode (depth 1) a mix
// of levels 4 and 5 spaces the lower level only; in Compact mode (depth
// > 1) there are no spaces at levels 4 and 5.
func (p *printer) binaryExpr(x *syntax.BinaryExpr, prec1, cutoff, depth int) {
	prec := precedence(x.Op)
	if prec < prec1 {
		p.print(syntax.LParen)
		p.expr0(x, reduceDepth(depth))
		p.print(syntax.RParen)
		return
	}
	printBlank := prec < cutoff

	ws := indent
	p.expr1(x.X, prec, depth+diffPrec(x.X, prec))
	if printBlank {
		p.print(blank)
	}
	xline := p.pos.Line
	yline := x.Y.Pos().Line
	p.setPos(x.OpPos)
	p.print(x.Op)
	if xline != yline && xline > 0 && yline > 0 {
		if p.linebreak(yline, 1, ws, true) > 0 {
			ws = ignore
			printBlank = false
		}
	}
	if printBlank {
		p.print(blank)
	}
	p.expr1(x.Y, prec+1, depth+1)
	if ws == ignore {
		p.print(unindent)
	}
}

func isBinary(expr syntax.Expr) bool {
	_, ok := expr.(*syntax.BinaryExpr)
	return ok
}

func (p *printer) expr1(expr syntax.Expr, prec1, depth int) {
	p.setPos(expr.Pos())

	switch x := expr.(type) {
	case *syntax.BadExpr:
		p.print("BadExpr")

	case *syntax.Name:
		p.print(x)

	case *syntax.BinaryExpr:
		p.binaryExpr(x, prec1, cutoff(x, depth), depth)

	case *syntax.KeyValueExpr:
		p.expr(x.Key)
		p.setPos(x.Colon)
		p.print(syntax.Colon, blank)
		p.expr(x.Value)

	case *syntax.StarExpr:
		if unaryPrec < prec1 {
			p.print(syntax.LParen, syntax.Mul)
			p.expr(x.X)
			p.print(syntax.RParen)
		} else {
			p.print(syntax.Mul)
			p.expr(x.X)
		}

	case *syntax.UnaryExpr:
		if unaryPrec < prec1 {
			p.print(syntax.LParen)
			p.expr(x)
			p.print(syntax.RParen)
		} else {
			p.print(x.Op)
			p.expr1(x.X, unaryPrec, depth)
		}

	case *syntax.BasicLit:
		p.print(normalizedNumber(x))

	case *syntax.FuncLit:
		p.setPos(x.Type.Pos())
		p.print(syntax.Func)
		startCol := p.out.Col - len("func")
		p.signature(x.Type)
		p.funcBody(p.distanceFrom(x.Type.Pos(), startCol), blank, x.Body)

	case *syntax.ParenExpr:
		if _, hasParens := x.X.(*syntax.ParenExpr); hasParens {
			p.expr0(x.X, depth)
		} else {
			p.print(syntax.LParen)
			p.expr0(x.X, reduceDepth(depth))
			p.setPos(x.Rparen)
			p.print(syntax.RParen)
		}

	case *syntax.SelectorExpr:
		p.selectorExpr(x, depth, false)

	case *syntax.TypeAssertExpr:
		p.expr1(x.X, highestPrec, depth)
		p.print(syntax.Period)
		p.setPos(x.Lparen)
		p.print(syntax.LParen)
		if x.Type != nil {
			p.expr(x.Type)
		} else {
			p.print(syntax.Type)
		}
		p.setPos(x.Rparen)
		p.print(syntax.RParen)

	case *syntax.IndexExpr:
		p.expr1(x.X, highestPrec, 1)
		p.setPos(x.Lbrack)
		p.print(syntax.LBrack)
		if len(x.Indices) == 1 {
			p.expr0(x.Indices[0], depth+1)
		} else {
			p.exprList(x.Lbrack, x.Indices, depth+1, commaTerm, x.Rbrack)
		}
		p.setPos(x.Rbrack)
		p.print(syntax.RBrack)

	case *syntax.SliceExpr:
		p.expr1(x.X, highestPrec, 1)
		p.setPos(x.Lbrack)
		p.print(syntax.LBrack)
		indices := []syntax.Expr{x.Low, x.High}
		if x.Max != nil {
			indices = append(indices, x.Max)
		}
		var needsBlanks bool
		if depth <= 1 {
			var indexCount int
			var hasBinaries bool
			for _, x := range indices {
				if x != nil {
					indexCount++
					if isBinary(x) {
						hasBinaries = true
					}
				}
			}
			if indexCount > 1 && hasBinaries {
				needsBlanks = true
			}
		}
		for i, x := range indices {
			if i > 0 {
				if indices[i-1] != nil && needsBlanks {
					p.print(blank)
				}
				p.print(syntax.Colon)
				if x != nil && needsBlanks {
					p.print(blank)
				}
			}
			if x != nil {
				p.expr0(x, depth+1)
			}
		}
		p.setPos(x.Rbrack)
		p.print(syntax.RBrack)

	case *syntax.CallExpr:
		if len(x.Args) > 1 {
			depth++
		}
		paren := false
		switch t := x.Fun.(type) {
		case *syntax.FuncType:
			paren = true
		case *syntax.ChanType:
			paren = t.Dir == syntax.ChanRecv
		}
		if paren {
			p.print(syntax.LParen)
		}
		wasIndented := p.possibleSelectorExpr(x.Fun, highestPrec, depth)
		if paren {
			p.print(syntax.RParen)
		}
		p.setPos(x.Lparen)
		p.print(syntax.LParen)
		if x.Ellipsis.IsValid() {
			p.exprList(x.Lparen, x.Args, depth, 0, x.Ellipsis)
			p.setPos(x.Ellipsis)
			p.print(syntax.DotDotDot)
			if x.Rparen.IsValid() && x.Ellipsis.Line < x.Rparen.Line {
				p.print(syntax.Comma, formfeed)
			}
		} else {
			p.exprList(x.Lparen, x.Args, depth, commaTerm, x.Rparen)
		}
		p.setPos(x.Rparen)
		p.print(syntax.RParen)
		if wasIndented {
			p.print(unindent)
		}

	case *syntax.CompositeLit:
		if x.Type != nil {
			p.expr1(x.Type, highestPrec, depth)
		}
		p.level++
		p.setPos(x.Lbrace)
		p.print(syntax.LBrace)
		p.exprList(x.Lbrace, x.Elts, 1, commaTerm, x.Rbrace)
		mode := noExtraLinebreak
		if len(x.Elts) > 0 {
			mode |= noExtraBlank
		}
		p.print(indent, unindent, mode)
		p.setPos(x.Rbrace)
		p.print(syntax.RBrace, mode)
		p.level--

	case *syntax.Ellipsis:
		p.print(syntax.DotDotDot)
		if x.Elt != nil {
			p.expr(x.Elt)
		}

	case *syntax.ArrayType:
		p.print(syntax.LBrack)
		if x.Len != nil {
			p.expr(x.Len)
		}
		p.print(syntax.RBrack)
		p.expr(x.Elt)

	case *syntax.StructType:
		p.structType(x)

	case *syntax.FuncType:
		p.print(syntax.Func)
		p.signature(x)

	case *syntax.InterfaceType:
		p.interfaceType(x)

	case *syntax.MapType:
		p.print(syntax.Map, syntax.LBrack)
		p.expr(x.Key)
		p.print(syntax.RBrack)
		p.expr(x.Value)

	case *syntax.ChanType:
		switch x.Dir {
		case syntax.ChanBoth:
			p.print(syntax.Chan)
		case syntax.ChanRecv:
			p.print(syntax.Arrow, syntax.Chan)
		case syntax.ChanSend:
			p.print(syntax.Chan)
			p.setPos(x.Begin.Advance("chan"))
			p.print(syntax.Arrow)
		}
		p.print(blank)
		p.expr(x.Value)

	case *syntax.EnumType:
		p.enumType(x)

	default:
		panic("format: unexpected expression")
	}
}

// normalizedNumber rewrites base prefixes and exponents of numbers to
// lower case (0X1 to 0x1, 1E3 to 1e3); hexadecimal digits are kept.
func normalizedNumber(lit *syntax.BasicLit) *syntax.BasicLit {
	if lit.Kind != syntax.Int && lit.Kind != syntax.Float && lit.Kind != syntax.Imag {
		return lit
	}
	if len(lit.Value) < 2 {
		return lit
	}
	x := lit.Value
	switch x[:2] {
	default:
		if i := strings.LastIndexByte(x, 'E'); i >= 0 {
			x = x[:i] + "e" + x[i+1:]
			break
		}
		if x[len(x)-1] == 'i' && !strings.ContainsAny(x, ".e") {
			x = strings.TrimLeft(x, "0_")
			if x == "i" {
				x = "0i"
			}
		}
	case "0X":
		x = "0x" + x[2:]
		if i := strings.LastIndexByte(x, 'P'); i >= 0 {
			x = x[:i] + "p" + x[i+1:]
		}
	case "0x":
		i := strings.LastIndexByte(x, 'P')
		if i == -1 {
			return lit
		}
		x = x[:i] + "p" + x[i+1:]
	case "0O":
		x = "0o" + x[2:]
	case "0o":
		return lit
	case "0B":
		x = "0b" + x[2:]
	case "0b":
		return lit
	}
	return &syntax.BasicLit{ValuePos: lit.ValuePos, Kind: lit.Kind, Value: x}
}

func (p *printer) possibleSelectorExpr(expr syntax.Expr, prec1, depth int) bool {
	if x, ok := expr.(*syntax.SelectorExpr); ok {
		return p.selectorExpr(x, depth, true)
	}
	p.expr1(expr, prec1, depth)
	return false
}

// selectorExpr prints x and reports whether it spans several lines.
func (p *printer) selectorExpr(x *syntax.SelectorExpr, depth int, isMethod bool) bool {
	p.expr1(x.X, highestPrec, depth)
	p.print(syntax.Period)
	if line := x.Sel.Pos().Line; p.pos.IsValid() && p.pos.Line < line {
		p.print(indent, newline)
		p.setPos(x.Sel.Pos())
		p.print(x.Sel)
		if !isMethod {
			p.print(unindent)
		}
		return true
	}
	p.setPos(x.Sel.Pos())
	p.print(x.Sel)
	return false
}

func (p *printer) expr0(x syntax.Expr, depth int) { p.expr1(x, lowestPrec, depth) }

func (p *printer) expr(x syntax.Expr) { p.expr1(x, lowestPrec, 1) }

// ----------------------------------------------------------------------------
// Statements

// stmtList prints a statement list indented, without a newline after the
// last statement. At most one empty line is kept between statements.
func (p *printer) stmtList(list []syntax.Stmt, nindent int, nextIsRBrace bool) {
	if nindent > 0 {
		p.print(indent)
	}
	// The last statement is the last non-empty one: empty statements are
	// not printed. (go/printer compares with the length of the whole list,
	// which makes gofmt non-idempotent for "case 0:;A:}".)
	last := -1
	for j, s := range list {
		if _, isEmpty := s.(*syntax.EmptyStmt); !isEmpty {
			last = j
		}
	}
	var line int
	i := 0
	for j, s := range list {
		if _, isEmpty := s.(*syntax.EmptyStmt); isEmpty {
			continue
		}
		if len(p.output) > 0 {
			p.linebreak(s.Pos().Line, 1, ignore, i == 0 || nindent == 0 || p.linesFrom(line) > 0)
		}
		p.recordLine(&line)
		p.stmt(s, nextIsRBrace && j == last)
		for t := s; ; {
			lt, _ := t.(*syntax.LabeledStmt)
			if lt == nil {
				break
			}
			line++
			t = lt.Stmt
		}
		i++
	}
	if nindent > 0 {
		p.print(unindent)
	}
}

// block prints a block; it always spans at least two lines.
func (p *printer) block(b *syntax.BlockStmt, nindent int) {
	p.setPos(b.Lbrace)
	p.print(syntax.LBrace)
	p.stmtList(b.List, nindent, true)
	p.linebreak(b.Rbrace.Line, 1, ignore, true)
	p.setPos(b.Rbrace)
	p.print(syntax.RBrace)
}

func isTypeName(x syntax.Expr) bool {
	switch t := x.(type) {
	case *syntax.Name:
		return true
	case *syntax.SelectorExpr:
		return isTypeName(t.X)
	}
	return false
}

// stripParens removes parentheses around a control clause expression,
// unless they protect a composite literal starting with a type name.
func stripParens(x syntax.Expr) syntax.Expr {
	px, ok := x.(*syntax.ParenExpr)
	if !ok {
		return x
	}
	if protectsCompositeLit(px.X) {
		return x
	}
	return stripParens(px.X)
}

// protectsCompositeLit reports whether x contains, outside nested
// parentheses, a composite literal whose type is a type name.
func protectsCompositeLit(x syntax.Expr) bool {
	found := false
	var visit func(n syntax.Expr) bool // returns false to stop descending
	visit = func(n syntax.Expr) bool {
		if found || n == nil {
			return false
		}
		switch n := n.(type) {
		case *syntax.ParenExpr:
			return false
		case *syntax.CompositeLit:
			if isTypeName(n.Type) {
				found = true
			}
			return false
		case *syntax.BinaryExpr:
			visit(n.X)
			visit(n.Y)
		case *syntax.UnaryExpr:
			visit(n.X)
		case *syntax.StarExpr:
			visit(n.X)
		case *syntax.SelectorExpr:
			visit(n.X)
		case *syntax.IndexExpr:
			visit(n.X)
			for _, i := range n.Indices {
				visit(i)
			}
		case *syntax.SliceExpr:
			visit(n.X)
			visit(n.Low)
			visit(n.High)
			visit(n.Max)
		case *syntax.TypeAssertExpr:
			visit(n.X)
		case *syntax.CallExpr:
			visit(n.Fun)
			for _, a := range n.Args {
				visit(a)
			}
		case *syntax.KeyValueExpr:
			visit(n.Key)
			visit(n.Value)
		}
		return true
	}
	visit(x)
	return found
}

func stripParensAlways(x syntax.Expr) syntax.Expr {
	if px, ok := x.(*syntax.ParenExpr); ok {
		return stripParensAlways(px.X)
	}
	return x
}

func (p *printer) controlClause(isForStmt bool, init syntax.Stmt, expr syntax.Expr, post syntax.Stmt) {
	p.print(blank)
	needsBlank := false
	if init == nil && post == nil {
		if expr != nil {
			p.expr(stripParens(expr))
			needsBlank = true
		}
	} else {
		if init != nil {
			p.stmt(init, false)
		}
		p.print(syntax.Semicolon, blank)
		if expr != nil {
			p.expr(stripParens(expr))
			needsBlank = true
		}
		if isForStmt {
			p.print(syntax.Semicolon, blank)
			needsBlank = false
			if post != nil {
				p.stmt(post, false)
				needsBlank = true
			}
		}
	}
	if needsBlank {
		p.print(blank)
	}
}

// indentList reports whether a multi-line expression list would look
// better indented as a whole.
func (p *printer) indentList(list []syntax.Expr) bool {
	if len(list) >= 2 {
		b := list[0].Pos().Line
		e := list[len(list)-1].End().Line
		if 0 < b && b < e {
			n := 0
			line := b
			for _, x := range list {
				xb := x.Pos().Line
				xe := x.End().Line
				if line < xb {
					return true
				}
				if xb < xe {
					n++
				}
				line = xe
			}
			return n > 1
		}
	}
	return false
}

func (p *printer) stmt(stmt syntax.Stmt, nextIsRBrace bool) {
	p.setPos(stmt.Pos())

	switch s := stmt.(type) {
	case *syntax.BadStmt:
		p.print("BadStmt")

	case *syntax.DeclStmt:
		p.decl(s.Decl)

	case *syntax.EmptyStmt:

	case *syntax.LabeledStmt:
		p.print(unindent)
		p.expr(s.Label)
		p.setPos(s.Colon)
		p.print(syntax.Colon, indent)
		if e, isEmpty := s.Stmt.(*syntax.EmptyStmt); isEmpty {
			if !nextIsRBrace {
				p.print(newline)
				p.setPos(e.Pos())
				p.print(syntax.Semicolon)
				break
			}
		} else {
			p.linebreak(s.Stmt.Pos().Line, 1, ignore, true)
		}
		p.stmt(s.Stmt, nextIsRBrace)

	case *syntax.ExprStmt:
		p.expr0(s.X, 1)

	case *syntax.SendStmt:
		p.expr0(s.Chan, 1)
		p.print(blank)
		p.setPos(s.Arrow)
		p.print(syntax.Arrow, blank)
		p.expr0(s.Value, 1)

	case *syntax.IncDecStmt:
		p.expr0(s.X, 2)
		p.setPos(s.TokPos)
		p.print(s.Tok)

	case *syntax.AssignStmt:
		depth := 1
		if len(s.Lhs) > 1 && len(s.Rhs) > 1 {
			depth++
		}
		p.exprList(s.Pos(), s.Lhs, depth, 0, s.TokPos)
		p.print(blank)
		p.setPos(s.TokPos)
		p.print(s.Tok, blank)
		p.exprList(s.TokPos, s.Rhs, depth, 0, syntax.Pos{})

	case *syntax.GoStmt:
		p.print(syntax.Go, blank)
		p.expr(s.Call)

	case *syntax.DeferStmt:
		p.print(syntax.Defer, blank)
		p.expr(s.Call)

	case *syntax.ReturnStmt:
		p.print(syntax.Return)
		if s.Results != nil {
			p.print(blank)
			if p.indentList(s.Results) {
				p.print(indent)
				p.exprList(syntax.Pos{}, s.Results, 1, noIndent, syntax.Pos{})
				p.print(unindent)
			} else {
				p.exprList(syntax.Pos{}, s.Results, 1, 0, syntax.Pos{})
			}
		}

	case *syntax.BranchStmt:
		p.print(s.Tok)
		if s.Label != nil {
			p.print(blank)
			p.expr(s.Label)
		}

	case *syntax.BlockStmt:
		p.block(s, 1)

	case *syntax.IfStmt:
		p.print(syntax.If)
		p.controlClause(false, s.Init, s.Cond, nil)
		p.block(s.Body, 1)
		if s.Else != nil {
			p.print(blank, syntax.Else, blank)
			p.stmt(s.Else, nextIsRBrace)
		}

	case *syntax.CaseClause:
		if s.List != nil {
			p.print(syntax.Case, blank)
			p.exprList(s.Pos(), s.List, 1, 0, s.Colon)
		} else {
			p.print(syntax.Default)
		}
		p.setPos(s.Colon)
		p.print(syntax.Colon)
		p.stmtList(s.Body, 1, nextIsRBrace)

	case *syntax.SwitchStmt:
		p.print(syntax.Switch)
		p.controlClause(false, s.Init, s.Tag, nil)
		p.block(s.Body, 0)

	case *syntax.TypeSwitchStmt:
		p.print(syntax.Switch)
		if s.Init != nil {
			p.print(blank)
			p.stmt(s.Init, false)
			p.print(syntax.Semicolon)
		}
		p.print(blank)
		p.stmt(s.Assign, false)
		p.print(blank)
		p.block(s.Body, 0)

	case *syntax.CommClause:
		if s.Comm != nil {
			p.print(syntax.Case, blank)
			p.stmt(s.Comm, false)
		} else {
			p.print(syntax.Default)
		}
		p.setPos(s.Colon)
		p.print(syntax.Colon)
		p.stmtList(s.Body, 1, nextIsRBrace)

	case *syntax.SelectStmt:
		p.print(syntax.Select, blank)
		body := s.Body
		if len(body.List) == 0 && !p.commentBefore(body.Rbrace) {
			p.setPos(body.Lbrace)
			p.print(syntax.LBrace)
			p.setPos(body.Rbrace)
			p.print(syntax.RBrace)
		} else {
			p.block(body, 0)
		}

	case *syntax.ForStmt:
		p.print(syntax.For)
		p.controlClause(true, s.Init, s.Cond, s.Post)
		p.block(s.Body, 1)

	case *syntax.RangeStmt:
		p.print(syntax.For, blank)
		if s.Const {
			p.print(syntax.Const, blank)
		}
		if s.Key != nil {
			p.expr(s.Key)
			if s.Value != nil {
				p.setPos(s.Value.Pos())
				p.print(syntax.Comma, blank)
				p.expr(s.Value)
			}
			p.print(blank)
			p.setPos(s.TokPos)
			p.print(s.Tok, blank)
		}
		p.print(syntax.Range, blank)
		p.expr(stripParens(s.X))
		p.print(blank)
		p.block(s.Body, 1)

	case *syntax.ContractStmt:
		p.print(word(s.Word), blank)
		p.expr(s.X)

	default:
		panic("format: unexpected statement")
	}
}

// ----------------------------------------------------------------------------
// Declarations

// keepTypeColumn reports for each spec of a const or var group whether
// its type column must be kept, or whether a run of values may move into
// the type column.
func keepTypeColumn(specs []syntax.Spec) []bool {
	m := make([]bool, len(specs))
	populate := func(i, j int, keepType bool) {
		if keepType {
			for ; i < j; i++ {
				m[i] = true
			}
		}
	}
	i0 := -1
	var keepType bool
	for i, s := range specs {
		t := s.(*syntax.ValueSpec)
		if t.Values != nil {
			if i0 < 0 {
				i0 = i
				keepType = false
			}
		} else if i0 >= 0 {
			populate(i0, i, keepType)
			i0 = -1
		}
		if t.Type != nil {
			keepType = true
		}
	}
	if i0 >= 0 {
		populate(i0, len(specs), keepType)
	}
	return m
}

func (p *printer) valueSpec(s *syntax.ValueSpec, keepType bool, next syntax.Pos) {
	p.identList(s.Names, false)
	extraTabs := 3
	if s.Type != nil || keepType {
		p.print(vtab)
		extraTabs--
	}
	if s.Type != nil {
		p.expr(s.Type)
	}
	if s.Values != nil {
		p.print(vtab, syntax.Assign, blank)
		p.exprList(syntax.Pos{}, s.Values, 1, 0, syntax.Pos{})
		extraTabs--
	}
	if p.hasLineComment(s.End(), next) {
		for ; extraTabs > 0; extraTabs-- {
			p.print(vtab)
		}
	}
}

func sanitizeImportPath(lit *syntax.BasicLit) *syntax.BasicLit {
	if lit.Kind != syntax.String {
		return lit
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil || s == "" {
		return lit
	}
	const illegalChars = `!"#$%&'()*,:;<=>?[\]^{|}` + "`�"
	for _, r := range s {
		if !unicode.IsGraphic(r) || unicode.IsSpace(r) || strings.ContainsRune(illegalChars, r) {
			return lit
		}
	}
	s = strconv.Quote(s)
	if s == lit.Value {
		return lit
	}
	return &syntax.BasicLit{ValuePos: lit.ValuePos, Kind: syntax.String, Value: s}
}

// spec prints a spec; n is the number of specs in the group.
func (p *printer) spec(spec syntax.Spec, n int, doIndent bool) {
	switch s := spec.(type) {
	case *syntax.ImportSpec:
		if s.Name != nil {
			p.expr(s.Name)
			p.print(blank)
		}
		p.expr(sanitizeImportPath(s.Path))
		p.setPos(s.End())

	case *syntax.ValueSpec:
		p.identList(s.Names, doIndent)
		if s.Type != nil {
			p.print(blank)
			p.expr(s.Type)
		}
		if s.Values != nil {
			p.print(blank, syntax.Assign, blank)
			p.exprList(syntax.Pos{}, s.Values, 1, 0, syntax.Pos{})
		}

	case *syntax.TypeSpec:
		p.expr(s.Name)
		if s.TypeParams != nil {
			p.parameters(s.TypeParams, typeTParam)
		}
		if n == 1 {
			p.print(blank)
		} else {
			p.print(vtab)
		}
		if s.Assign.IsValid() {
			p.print(syntax.Assign, blank)
		}
		p.expr(s.Type)

	default:
		panic("format: unexpected spec")
	}
}

func (p *printer) genDecl(d *syntax.GenDecl) {
	p.setPos(d.Pos())
	p.print(d.Tok, blank)

	if !d.Lparen.IsValid() && len(d.Specs) == 1 {
		p.spec(d.Specs[0], 1, true)
		return
	}

	p.setPos(d.Lparen)
	p.print(syntax.LParen)
	if n := len(d.Specs); n > 0 {
		p.print(indent, formfeed)
		var line int
		if n > 1 && (d.Tok == syntax.Const || d.Tok == syntax.Var) {
			keepType := keepTypeColumn(d.Specs)
			for i, s := range d.Specs {
				if i > 0 {
					p.linebreak(s.Pos().Line, 1, ignore, p.linesFrom(line) > 0)
				}
				p.recordLine(&line)
				next := d.Rparen
				if i+1 < n {
					next = d.Specs[i+1].Pos()
				}
				p.valueSpec(s.(*syntax.ValueSpec), keepType[i], next)
			}
		} else {
			for i, s := range d.Specs {
				if i > 0 {
					p.linebreak(s.Pos().Line, 1, ignore, p.linesFrom(line) > 0)
				}
				p.recordLine(&line)
				p.spec(s, n, false)
			}
		}
		p.print(unindent, formfeed)
	}
	p.setPos(d.Rparen)
	p.print(syntax.RParen)
}

// numLines returns the number of source lines spanned by n.
func (p *printer) numLines(n syntax.Node) int {
	if from := n.Pos(); from.IsValid() {
		if to := n.End(); to.IsValid() {
			return to.Line - from.Line + 1
		}
	}
	return infinity
}

// bodySize estimates the size of a block printed on one line.
func (p *printer) bodySize(b *syntax.BlockStmt, maxSize int) int {
	pos1 := b.Pos()
	pos2 := b.Rbrace
	if pos1.IsValid() && pos2.IsValid() && pos1.Line != pos2.Line {
		return maxSize + 1
	}
	if len(b.List) > 5 {
		return maxSize + 1
	}
	bodySize := p.commentSizeBefore(pos2)
	for i, s := range b.List {
		if bodySize > maxSize {
			break
		}
		if i > 0 {
			bodySize += 2
		}
		bodySize += p.nodeSize(s, maxSize)
	}
	return bodySize
}

// funcBody prints a function body after a header of headerSize. A small
// and simple body is printed on the header line.
func (p *printer) funcBody(headerSize int, sep whiteSpace, b *syntax.BlockStmt) {
	if b == nil {
		return
	}
	defer func(level int) { p.level = level }(p.level)
	p.level = 0

	const maxSize = 100
	if headerSize+p.bodySize(b, maxSize) <= maxSize {
		p.print(sep)
		p.setPos(b.Lbrace)
		p.print(syntax.LBrace)
		if len(b.List) > 0 {
			p.print(blank)
			for i, s := range b.List {
				if i > 0 {
					p.print(syntax.Semicolon, blank)
				}
				p.stmt(s, i == len(b.List)-1)
			}
			p.print(blank)
		}
		p.print(noExtraLinebreak)
		p.setPos(b.Rbrace)
		p.print(syntax.RBrace, noExtraLinebreak)
		return
	}
	if sep != ignore {
		p.print(blank)
	}
	p.block(b, 1)
}

// distanceFrom returns the column difference between the current output
// position and startOutCol, or infinity if startPos is on another line.
func (p *printer) distanceFrom(startPos syntax.Pos, startOutCol int) int {
	if startPos.IsValid() && p.pos.IsValid() && startPos.Line == p.pos.Line {
		return p.out.Col - startOutCol
	}
	return infinity
}

func (p *printer) funcDecl(d *syntax.FuncDecl) {
	p.setPos(d.Pos())
	if d.Gpu.IsValid() {
		p.print(word("gpu"), blank)
	}
	if d.Handler != "" {
		// interrupt Name(...) { ... } and trap Name(...) { ... }
		p.print(word(d.Handler), blank)
		startCol := p.out.Col - len(d.Handler) - 1
		p.expr(d.Name)
		p.signature(d.Type)
		p.funcBody(p.distanceFrom(d.Pos(), startCol), vtab, d.Body)
		return
	}
	p.print(syntax.Func, blank)
	startCol := p.out.Col - len("func ")
	if d.Recv != nil {
		p.parameters(d.Recv, funcParam)
		p.print(blank)
	}
	p.expr(d.Name)
	p.signature(d.Type)
	if d.Binding != nil {
		p.print(blank, syntax.Assign, blank)
		p.expr(d.Binding)
		return
	}
	p.funcBody(p.distanceFrom(d.Pos(), startCol), vtab, d.Body)
}

func (p *printer) decl(decl syntax.Decl) {
	switch d := decl.(type) {
	case *syntax.GenDecl:
		p.genDecl(d)
	case *syntax.FuncDecl:
		p.funcDecl(d)
	default:
		panic("format: unexpected declaration")
	}
}

func declToken(decl syntax.Decl) syntax.Kind {
	switch d := decl.(type) {
	case *syntax.GenDecl:
		return d.Tok
	case *syntax.FuncDecl:
		return syntax.Func
	}
	return syntax.Illegal
}

func (p *printer) declList(list []syntax.Decl) {
	tok := syntax.Illegal
	for _, d := range list {
		prev := tok
		tok = declToken(d)
		if len(p.output) > 0 {
			min := 1
			if prev != tok {
				min = 2
			}
			p.linebreak(d.Pos().Line, min, ignore, tok == syntax.Func && p.numLines(d) > 1)
		}
		p.decl(d)
	}
}

func (p *printer) file(f *syntax.File) {
	p.setPos(f.Pos())
	p.print(syntax.Package, blank)
	p.expr(f.Name)
	if c := f.Profile; c != nil {
		p.linebreak(c.Profile.Line, 2, ignore, false)
		p.setPos(c.Profile)
		p.print(syntax.Profile, blank)
		for i, n := range c.Names {
			if i > 0 {
				p.print(syntax.Comma, blank)
			}
			p.expr(n)
		}
	}
	p.declList(f.Decls)
	p.print(newline)
}
