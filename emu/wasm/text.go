package wasm

import (
	"math"
	"math/bits"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The text format of WebAssembly 2.0: tokens, S-expressions and modules,
// which are translated to the same Module as the binary format.

type tokKind int

const (
	tLParen tokKind = iota
	tRParen
	tKeyword
	tID
	tString
	tReserved
)

type token struct {
	kind tokKind
	text string // the token as written; for a string, its bytes
	line int
}

func isIDChar(c byte) bool {
	switch {
	case '0' <= c && c <= '9', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		return true
	}
	return strings.IndexByte("!#$%&'*+-./:<=>?@\\^_`|~", c) >= 0
}

// lex splits text into tokens.
func lex(src string) ([]token, error) {
	var toks []token
	line := 1
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == ';' && i+1 < len(src) && src[i+1] == ';':
			for i < len(src) && src[i] != '\n' && src[i] != '\r' {
				i++
			}
		case c == '(' && i+1 < len(src) && src[i+1] == ';':
			depth := 0
			for {
				if i+1 >= len(src) {
					return nil, malformed("unclosed comment")
				}
				switch {
				case src[i] == '(' && src[i+1] == ';':
					depth++
					i += 2
				case src[i] == ';' && src[i+1] == ')':
					depth--
					i += 2
				default:
					if src[i] == '\n' {
						line++
					}
					i++
				}
				if depth == 0 {
					break
				}
			}
		case c == '(':
			toks = append(toks, token{kind: tLParen, line: line})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen, line: line})
			i++
		case c == '"':
			s, n, err := lexString(src[i:])
			if err != nil {
				return nil, err
			}
			i += n
			if i < len(src) && (isIDChar(src[i]) || src[i] == '"') {
				return nil, malformed("unknown operator")
			}
			toks = append(toks, token{kind: tString, text: s, line: line})
		case isIDChar(c):
			j := i
			for j < len(src) && isIDChar(src[j]) {
				j++
			}
			t := src[i:j]
			if j < len(src) && src[j] == '"' {
				return nil, malformed("unknown operator")
			}
			kind := tReserved
			switch {
			case t[0] == '$' && len(t) > 1:
				kind = tID
			case 'a' <= t[0] && t[0] <= 'z':
				kind = tKeyword
			}
			toks = append(toks, token{kind: kind, text: t, line: line})
			i = j
		default:
			return nil, malformed("unexpected character %q", c)
		}
	}
	return toks, nil
}

// lexString reads a string literal at the start of s and returns its
// bytes and its length in s.
func lexString(s string) (string, int, error) {
	var b []byte
	for i := 1; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			return string(b), i + 1, nil
		case c == '\\':
			if i+1 >= len(s) {
				return "", 0, malformed("unclosed string")
			}
			e := s[i+1]
			i += 2
			switch e {
			case 't':
				b = append(b, '\t')
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case '"', '\'', '\\':
				b = append(b, e)
			case 'u':
				if i >= len(s) || s[i] != '{' {
					return "", 0, malformed("illegal escape")
				}
				j := strings.IndexByte(s[i:], '}')
				if j < 0 {
					return "", 0, malformed("illegal escape")
				}
				v, ok := parseNat(s[i+1:i+j], true)
				if !ok || v >= 0xD800 && v < 0xE000 || v > 0x10FFFF {
					return "", 0, malformed("illegal escape")
				}
				b = utf8.AppendRune(b, rune(v))
				i += j + 1
			default:
				if i < len(s) && isHexDigit(e) && isHexDigit(s[i]) {
					v, _ := strconv.ParseUint(s[i-1:i+1], 16, 8)
					b = append(b, byte(v))
					i++
					break
				}
				return "", 0, malformed("illegal escape")
			}
		case c < 0x20 || c == 0x7F:
			return "", 0, malformed("illegal character in string")
		default:
			b = append(b, c)
			i++
		}
	}
	return "", 0, malformed("unclosed string")
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// sexp is an S-expression: an atom or a list.
type sexp struct {
	tok    token
	isList bool
	list   []*sexp
}

func (e *sexp) String() string {
	if !e.isList {
		return e.tok.text
	}
	var parts []string
	for _, k := range e.list {
		parts = append(parts, k.String())
	}
	return "(" + strings.Join(parts, " ") + ")"
}

// head returns the keyword that starts a list, or "".
func (e *sexp) head() string {
	if e.isList && len(e.list) > 0 && e.list[0].tok.kind == tKeyword && !e.list[0].isList {
		return e.list[0].tok.text
	}
	return ""
}

func (e *sexp) is(kind tokKind) bool { return !e.isList && e.tok.kind == kind }

func (e *sexp) keyword(k string) bool { return e.is(tKeyword) && e.tok.text == k }

// parseSexps parses the S-expressions of tokens.
func parseSexps(toks []token) ([]*sexp, error) {
	var stack [][]*sexp
	var cur []*sexp
	for _, t := range toks {
		switch t.kind {
		case tLParen:
			stack = append(stack, cur)
			cur = nil
		case tRParen:
			if len(stack) == 0 {
				return nil, malformed("unexpected )")
			}
			l := &sexp{isList: true, list: cur, tok: t}
			cur = append(stack[len(stack)-1], l)
			stack = stack[:len(stack)-1]
		default:
			cur = append(cur, &sexp{tok: t})
		}
	}
	if len(stack) != 0 {
		return nil, malformed("unclosed (")
	}
	return cur, nil
}

// ---------------------------------------------------------------------
// Numbers.

// parseNat parses an unsigned number with underscores between digits,
// decimal or hexadecimal. A number that does not fit in 64 bits is
// math.MaxUint64 with overflow set.
func parseNat(s string, hex bool) (uint64, bool) {
	v, ok, _ := parseNatOverflow(s, hex)
	return v, ok
}

func parseNatOverflow(s string, hex bool) (v uint64, ok, overflow bool) {
	if s == "" || s[0] == '_' || s[len(s)-1] == '_' || strings.Contains(s, "__") {
		return 0, false, false
	}
	base := uint64(10)
	if hex {
		base = 16
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			continue
		}
		var d uint64
		switch {
		case '0' <= c && c <= '9':
			d = uint64(c - '0')
		case hex && 'a' <= c && c <= 'f':
			d = uint64(c-'a') + 10
		case hex && 'A' <= c && c <= 'F':
			d = uint64(c-'A') + 10
		default:
			return 0, false, false
		}
		hi, lo := bits.Mul64(v, base)
		if hi != 0 || lo+d < lo {
			overflow = true
			v = math.MaxUint64
			continue
		}
		if !overflow {
			v = lo + d
		}
	}
	return v, true, overflow
}

// parseUN parses an unsigned literal: a decimal or hexadecimal number
// without a sign. It returns the value, whether it fits in 64 bits and
// whether it is well formed.
func parseUN(s string) (uint64, bool, bool) {
	if s == "" || s[0] == '+' || s[0] == '-' {
		return 0, false, false
	}
	hex := strings.HasPrefix(s, "0x")
	if hex {
		s = s[2:]
	}
	v, ok, overflow := parseNatOverflow(s, hex)
	return v, ok && !overflow, ok
}

// parseInt parses an integer literal of n bits: unsigned, or signed with
// a sign.
func parseInt(s string, n int) (uint64, error) {
	sign := byte(0)
	if s != "" && (s[0] == '+' || s[0] == '-') {
		sign, s = s[0], s[1:]
	}
	hex := strings.HasPrefix(s, "0x")
	if hex {
		s = s[2:]
	}
	v, ok, overflow := parseNatOverflow(s, hex)
	if !ok {
		return 0, malformed("unknown operator")
	}
	if overflow {
		return 0, malformed("constant out of range")
	}
	limit := uint64(math.MaxUint64)
	if n < 64 {
		limit = 1<<uint(n) - 1
	}
	switch sign {
	case '-':
		if v > 1<<uint(n-1) {
			return 0, malformed("constant out of range")
		}
		return -v & limit, nil
	case '+':
		if v >= 1<<uint(n-1) {
			return 0, malformed("constant out of range")
		}
	default:
		if v > limit {
			return 0, malformed("constant out of range")
		}
	}
	return v, nil
}

// parseFloat parses a floating-point literal of n bits and returns its
// bits.
func parseFloat(s string, n int) (uint64, error) {
	neg := false
	t := s
	if t != "" && (t[0] == '+' || t[0] == '-') {
		neg = t[0] == '-'
		t = t[1:]
	}
	signBit := uint64(0)
	if neg {
		signBit = 1 << uint(n-1)
	}
	mant := 52
	if n == 32 {
		mant = 23
	}
	expMask := uint64(1)<<uint(n-1) - 1 - (uint64(1)<<uint(mant) - 1)
	switch {
	case t == "inf":
		return signBit | expMask, nil
	case t == "nan":
		return signBit | expMask | 1<<uint(mant-1), nil
	case strings.HasPrefix(t, "nan:0x"):
		p, ok := parseNat(t[6:], true)
		if !ok || p == 0 || p >= 1<<uint(mant) {
			return 0, malformed("constant out of range")
		}
		return signBit | expMask | p, nil
	}
	var text string
	if strings.HasPrefix(t, "0x") {
		m, e, ok := splitFloat(t[2:], true)
		if !ok {
			return 0, malformed("unknown operator")
		}
		if e == "" {
			e = "0"
		}
		text = "0x" + m + "p" + e
	} else {
		m, e, ok := splitFloat(t, false)
		if !ok {
			return 0, malformed("unknown operator")
		}
		text = m
		if e != "" {
			text += "e" + e
		}
	}
	if neg {
		text = "-" + text
	}
	f, err := strconv.ParseFloat(text, n)
	if err != nil {
		return 0, malformed("constant out of range")
	}
	if n == 32 {
		return uint64(math.Float32bits(float32(f))), nil
	}
	return math.Float64bits(f), nil
}

// splitFloat checks the mantissa and the exponent of a float literal and
// returns them without underscores.
func splitFloat(s string, hex bool) (mant, exp string, ok bool) {
	ech := "eE"
	if hex {
		ech = "pP"
	}
	if i := strings.IndexAny(s, ech); i >= 0 {
		s, exp = s[:i], s[i+1:]
		digits := exp
		if digits != "" && (digits[0] == '+' || digits[0] == '-') {
			digits = digits[1:]
		}
		if _, ok := parseNat(digits, false); !ok {
			return "", "", false
		}
		exp = strings.ReplaceAll(exp, "_", "")
	}
	whole, frac, dot := strings.Cut(s, ".")
	if _, ok := parseNat(whole, hex); !ok {
		return "", "", false
	}
	if dot && frac != "" {
		if _, ok := parseNat(frac, hex); !ok {
			return "", "", false
		}
	}
	return strings.ReplaceAll(s, "_", ""), exp, true
}

// ---------------------------------------------------------------------
// Modules.

// space is an index space of a module, with the names of its entries.
type space struct {
	n     uint32
	names map[string]uint32
}

func (s *space) add(id string) error {
	if id != "" {
		if s.names == nil {
			s.names = map[string]uint32{}
		}
		if _, dup := s.names[id]; dup {
			return malformed("duplicate identifier %s", id)
		}
		s.names[id] = s.n
	}
	s.n++
	return nil
}

type textModule struct {
	m                                          *Module
	types, funcs, tables, mems, globals, elems space
	datas                                      space
	typeParamNames                             [][]string
	defined                                    bool // a definition of a function, table, memory or global was seen
	startSeen                                  bool
	implicit                                   []FuncType
}

// ParseText parses the fields of a module in the text format.
func ParseText(src string) (*Module, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	fields, err := parseSexps(toks)
	if err != nil {
		return nil, err
	}
	return parseModuleFields(fields)
}

func parseModuleFields(fields []*sexp) (m *Module, err error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*MalformedError)
			if !ok {
				panic(r)
			}
			m, err = nil, e
		}
	}()
	t := &textModule{m: &Module{Start: -1, DataCount: -1}}
	t.collect(fields)
	// The explicit types first, so that type uses may refer to any.
	for _, f := range fields {
		if f.head() == "type" {
			t.field(f)
		}
	}
	for _, f := range fields {
		if f.head() != "type" {
			t.field(f)
		}
	}
	t.m.Types = append(t.m.Types, t.implicit...)
	usesData := false
	for _, f := range t.m.Funcs {
		for _, in := range f.Body {
			if in.Op == prefixMisc+0x08 || in.Op == prefixMisc+0x09 {
				usesData = true
			}
		}
	}
	if usesData {
		t.m.DataCount = len(t.m.Datas)
	}
	return t.m, nil
}

func fail(format string, args ...any) {
	panic(malformed(format, args...))
}

// id returns the identifier at items[*i], if any.
func optID(items []*sexp, i *int) string {
	if *i < len(items) && items[*i].is(tID) {
		*i++
		return items[*i-1].tok.text
	}
	return ""
}

// collect assigns the indices of the named entities.
func (t *textModule) collect(fields []*sexp) {
	for _, f := range fields {
		if !f.isList {
			fail("unexpected token %s", f)
		}
		head := f.head()
		i := 1
		id := optID(f.list, &i)
		var err error
		switch head {
		case "type":
			err = t.types.add(id)
		case "import":
			if len(f.list) < 4 || !f.list[3].isList {
				fail("unexpected token")
			}
			desc := f.list[3]
			j := 1
			did := optID(desc.list, &j)
			switch desc.head() {
			case "func":
				err = t.funcs.add(did)
			case "table":
				err = t.tables.add(did)
			case "memory":
				err = t.mems.add(did)
			case "global":
				err = t.globals.add(did)
			default:
				fail("unexpected token")
			}
		case "func":
			err = t.funcs.add(id)
		case "table":
			err = t.tables.add(id)
			// An inline element segment.
			for _, k := range f.list[i:] {
				if k.head() == "elem" {
					t.elems.add("")
				}
			}
		case "memory":
			err = t.mems.add(id)
			for _, k := range f.list[i:] {
				if k.head() == "data" {
					t.datas.add("")
				}
			}
		case "global":
			err = t.globals.add(id)
		case "elem":
			err = t.elems.add(id)
		case "data":
			err = t.datas.add(id)
		case "export", "start":
		default:
			fail("unexpected token %s", f.head())
		}
		if err != nil {
			panic(err)
		}
	}
}

// index resolves an index or an identifier of space s.
func index(e *sexp, s *space, what string) uint32 {
	switch {
	case e.is(tID):
		v, ok := s.names[e.tok.text]
		if !ok {
			fail("unknown %s %s", what, e.tok.text)
		}
		return v
	case !e.isList:
		v, ok, _ := parseUN(e.tok.text)
		if !ok || v > math.MaxUint32 {
			fail("unknown operator")
		}
		return uint32(v)
	}
	fail("unexpected token")
	return 0
}

func isIndex(e *sexp) bool {
	if e.is(tID) {
		return true
	}
	if e.isList || e.tok.kind != tReserved {
		return false
	}
	_, ok, _ := parseUN(e.tok.text)
	return ok
}

func valType(e *sexp) ValType {
	if e.is(tKeyword) {
		switch e.tok.text {
		case "i32":
			return I32
		case "i64":
			return I64
		case "f32":
			return F32
		case "f64":
			return F64
		case "v128":
			return V128
		case "funcref":
			return FuncRef
		case "externref":
			return ExternRef
		}
	}
	fail("unexpected token %s", e)
	return 0
}

func refType(e *sexp) ValType {
	t := valType(e)
	if !t.isRef() {
		fail("unexpected token %s", e)
	}
	return t
}

func str(e *sexp) string {
	if !e.is(tString) {
		fail("unexpected token %s", e)
	}
	return e.tok.text
}

func name(e *sexp) string {
	s := str(e)
	if !utf8.ValidString(s) {
		fail("malformed UTF-8 encoding")
	}
	return s
}

// funcSig parses (param ...)* (result ...)* at items[*i:], with the names
// of the parameters.
func funcSig(items []*sexp, i *int, allowNames bool) (FuncType, []string) {
	var ft FuncType
	var names []string
	for *i < len(items) && items[*i].head() == "param" {
		l := items[*i].list
		if len(l) > 1 && l[1].is(tID) {
			if !allowNames || len(l) != 3 {
				fail("unexpected token")
			}
			ft.Params = append(ft.Params, valType(l[2]))
			names = append(names, l[1].tok.text)
		} else {
			for _, k := range l[1:] {
				ft.Params = append(ft.Params, valType(k))
				names = append(names, "")
			}
		}
		*i++
	}
	for *i < len(items) && items[*i].head() == "result" {
		for _, k := range items[*i].list[1:] {
			ft.Results = append(ft.Results, valType(k))
		}
		*i++
	}
	return ft, names
}

// typeIndex returns the index of a function type, adding it to the
// implicit types if no type has it.
func (t *textModule) typeIndex(ft FuncType) uint32 {
	for i, u := range t.m.Types {
		if u.equal(ft) {
			return uint32(i)
		}
	}
	for i, u := range t.implicit {
		if u.equal(ft) {
			return uint32(len(t.m.Types) + i)
		}
	}
	t.implicit = append(t.implicit, ft)
	return uint32(len(t.m.Types) + len(t.implicit) - 1)
}

// typeUse parses a type use: (type x)? followed by the parameters and
// results, and returns the type index and the parameter names.
func (t *textModule) typeUse(items []*sexp, i *int) (uint32, []string) {
	if *i < len(items) && items[*i].head() == "type" {
		l := items[*i].list
		if len(l) != 2 {
			fail("unexpected token")
		}
		x := index(l[1], &t.types, "type")
		*i++
		ft, names := funcSig(items, i, true)
		inline := len(ft.Params)+len(ft.Results) > 0
		var def FuncType
		switch {
		case int(x) < len(t.m.Types):
			def = t.m.Types[x]
		case int(x) < len(t.m.Types)+len(t.implicit):
			def = t.implicit[int(x)-len(t.m.Types)]
		case inline:
			fail("unknown type")
		default:
			return x, names // invalid: an unknown type
		}
		if inline && !def.equal(ft) {
			fail("inline function type")
		}
		if names == nil {
			names = make([]string, len(def.Params))
		}
		return x, names
	}
	ft, names := funcSig(items, i, true)
	return t.typeIndex(ft), names
}

// exports parses inline exports at items[*i:].
func (t *textModule) exports(items []*sexp, i *int, kind byte, idx uint32) {
	for *i < len(items) && items[*i].head() == "export" {
		l := items[*i].list
		if len(l) != 2 {
			fail("unexpected token")
		}
		t.m.Exports = append(t.m.Exports, Export{Name: name(l[1]), Kind: kind, Index: idx})
		*i++
	}
}

// inlineImport parses an inline import at items[*i], if any.
func (t *textModule) inlineImport(items []*sexp, i *int) (string, string, bool) {
	if *i < len(items) && items[*i].head() == "import" {
		l := items[*i].list
		if len(l) != 3 {
			fail("unexpected token")
		}
		*i++
		return name(l[1]), name(l[2]), true
	}
	return "", "", false
}

func (t *textModule) noDefinitionYet(kind string) {
	if t.defined {
		fail("import after %s", kind)
	}
}

func limits(items []*sexp, i *int, max uint64) Limits {
	var l Limits
	if *i >= len(items) {
		fail("unexpected token")
	}
	v, ok, _ := parseUN(items[*i].tok.text)
	if items[*i].isList || !ok || v > max {
		fail("unknown operator")
	}
	l.Min = uint32(v)
	*i++
	if *i < len(items) && !items[*i].isList && items[*i].tok.kind == tReserved {
		v, ok, _ := parseUN(items[*i].tok.text)
		if !ok || v > max {
			fail("unknown operator")
		}
		l.Max, l.HasMax = uint32(v), true
		*i++
	}
	return l
}

func globalType(e *sexp) GlobalType {
	if e.head() == "mut" {
		if len(e.list) != 2 {
			fail("unexpected token")
		}
		return GlobalType{Type: valType(e.list[1]), Mutable: true}
	}
	return GlobalType{Type: valType(e)}
}

func end(items []*sexp, i int) {
	if i < len(items) {
		fail("unexpected token %s", items[i])
	}
}

func (t *textModule) field(f *sexp) {
	items := f.list
	i := 1
	optID(items, &i) // named in collect
	switch f.head() {
	case "type":
		if i >= len(items) || items[i].head() != "func" {
			fail("unexpected token")
		}
		j := 1
		ft, names := funcSig(items[i].list, &j, true)
		end(items[i].list, j)
		end(items, i+1)
		t.m.Types = append(t.m.Types, ft)
		t.typeParamNames = append(t.typeParamNames, names)
	case "import":
		if len(items) != 4 {
			fail("unexpected token")
		}
		im := Import{Module: name(items[1]), Name: name(items[2])}
		desc := items[3]
		j := 1
		optID(desc.list, &j)
		switch desc.head() {
		case "func":
			t.noDefinitionYet("function")
			im.Kind = KindFunc
			im.Func, _ = t.typeUse(desc.list, &j)
		case "table":
			t.noDefinitionYet("table")
			im.Kind = KindTable
			im.Table.Limits = limits(desc.list, &j, math.MaxUint32)
			if j >= len(desc.list) {
				fail("unexpected token")
			}
			im.Table.Elem = refType(desc.list[j])
			j++
		case "memory":
			t.noDefinitionYet("memory")
			im.Kind = KindMemory
			im.Memory = limits(desc.list, &j, math.MaxUint32)
		case "global":
			t.noDefinitionYet("global")
			im.Kind = KindGlobal
			if j >= len(desc.list) {
				fail("unexpected token")
			}
			im.Global = globalType(desc.list[j])
			j++
		}
		end(desc.list, j)
		t.m.Imports = append(t.m.Imports, im)
	case "func":
		idx := uint32(t.m.imported(KindFunc) + len(t.m.Funcs))
		t.exports(items, &i, KindFunc, idx)
		if mod, nm, ok := t.inlineImport(items, &i); ok {
			t.noDefinitionYet("function")
			x, _ := t.typeUse(items, &i)
			end(items, i)
			t.m.Imports = append(t.m.Imports, Import{Module: mod, Name: nm, Kind: KindFunc, Func: x})
			return
		}
		t.defined = true
		x, names := t.typeUse(items, &i)
		fn := Func{Type: x}
		locals := append([]string(nil), names...)
		for i < len(items) && items[i].head() == "local" {
			l := items[i].list
			if len(l) > 1 && l[1].is(tID) {
				if len(l) != 3 {
					fail("unexpected token")
				}
				fn.Locals = append(fn.Locals, valType(l[2]))
				locals = append(locals, l[1].tok.text)
			} else {
				for _, k := range l[1:] {
					fn.Locals = append(fn.Locals, valType(k))
					locals = append(locals, "")
				}
			}
			i++
		}
		fc := &funcCtx{t: t, locals: map[string]uint32{}}
		for k, n := range locals {
			if n == "" {
				continue
			}
			if _, dup := fc.locals[n]; dup {
				fail("duplicate local %s", n)
			}
			fc.locals[n] = uint32(k)
		}
		fc.seq(items[i:])
		fn.Body = append(fc.out, Instr{Op: opEnd})
		t.m.Funcs = append(t.m.Funcs, fn)
		// The parameter names are known now; the body was parsed in order.
	case "table":
		idx := uint32(t.m.imported(KindTable) + len(t.m.Tables))
		t.exports(items, &i, KindTable, idx)
		if mod, nm, ok := t.inlineImport(items, &i); ok {
			t.noDefinitionYet("table")
			im := Import{Module: mod, Name: nm, Kind: KindTable}
			im.Table.Limits = limits(items, &i, math.MaxUint32)
			if i >= len(items) {
				fail("unexpected token")
			}
			im.Table.Elem = refType(items[i])
			end(items, i+1)
			t.m.Imports = append(t.m.Imports, im)
			return
		}
		t.defined = true
		if i < len(items) && items[i].is(tKeyword) {
			// reftype (elem ...)
			rt := refType(items[i])
			i++
			if i >= len(items) || items[i].head() != "elem" {
				fail("unexpected token")
			}
			el := items[i].list
			e := Elem{Mode: ModeActive, Table: idx, Type: rt, Offset: []Instr{{Op: opI32Const}, {Op: opEnd}}}
			if len(el) > 1 && el[1].isList {
				for _, k := range el[1:] {
					e.Init = append(e.Init, t.elemExpr(k))
				}
			} else {
				for _, k := range el[1:] {
					e.Init = append(e.Init, []Instr{{Op: opRefFunc, A: index(k, &t.funcs, "function")}, {Op: opEnd}})
				}
			}
			end(items, i+1)
			n := uint32(len(e.Init))
			t.m.Tables = append(t.m.Tables, TableType{Elem: rt, Limits: Limits{Min: n, Max: n, HasMax: true}})
			t.m.Elems = append(t.m.Elems, e)
			return
		}
		var tt TableType
		tt.Limits = limits(items, &i, math.MaxUint32)
		if i >= len(items) {
			fail("unexpected token")
		}
		tt.Elem = refType(items[i])
		end(items, i+1)
		t.m.Tables = append(t.m.Tables, tt)
	case "memory":
		idx := uint32(t.m.imported(KindMemory) + len(t.m.Memories))
		t.exports(items, &i, KindMemory, idx)
		if mod, nm, ok := t.inlineImport(items, &i); ok {
			t.noDefinitionYet("memory")
			im := Import{Module: mod, Name: nm, Kind: KindMemory}
			im.Memory = limits(items, &i, math.MaxUint32)
			end(items, i)
			t.m.Imports = append(t.m.Imports, im)
			return
		}
		t.defined = true
		if i < len(items) && items[i].head() == "data" {
			var b []byte
			for _, k := range items[i].list[1:] {
				b = append(b, str(k)...)
			}
			end(items, i+1)
			pages := uint32((len(b) + pageSize - 1) / pageSize)
			t.m.Memories = append(t.m.Memories, Limits{Min: pages, Max: pages, HasMax: true})
			t.m.Datas = append(t.m.Datas, Data{Mode: ModeActive, Memory: idx, Offset: []Instr{{Op: opI32Const}, {Op: opEnd}}, Bytes: b})
			return
		}
		l := limits(items, &i, math.MaxUint32)
		end(items, i)
		t.m.Memories = append(t.m.Memories, l)
	case "global":
		idx := uint32(t.m.imported(KindGlobal) + len(t.m.Globals))
		t.exports(items, &i, KindGlobal, idx)
		if mod, nm, ok := t.inlineImport(items, &i); ok {
			t.noDefinitionYet("global")
			if i >= len(items) {
				fail("unexpected token")
			}
			im := Import{Module: mod, Name: nm, Kind: KindGlobal, Global: globalType(items[i])}
			end(items, i+1)
			t.m.Imports = append(t.m.Imports, im)
			return
		}
		t.defined = true
		if i >= len(items) {
			fail("unexpected token")
		}
		g := Global{Type: globalType(items[i])}
		g.Init = t.constExpr(items[i+1:])
		t.m.Globals = append(t.m.Globals, g)
	case "export":
		if len(items) != 3 || !items[2].isList || len(items[2].list) != 2 {
			fail("unexpected token")
		}
		e := Export{Name: name(items[1])}
		ref := items[2]
		switch ref.head() {
		case "func":
			e.Kind, e.Index = KindFunc, index(ref.list[1], &t.funcs, "function")
		case "table":
			e.Kind, e.Index = KindTable, index(ref.list[1], &t.tables, "table")
		case "memory":
			e.Kind, e.Index = KindMemory, index(ref.list[1], &t.mems, "memory")
		case "global":
			e.Kind, e.Index = KindGlobal, index(ref.list[1], &t.globals, "global")
		default:
			fail("unexpected token")
		}
		t.m.Exports = append(t.m.Exports, e)
	case "start":
		if len(items) != 2 {
			fail("unexpected token")
		}
		if t.startSeen {
			fail("multiple start sections")
		}
		t.startSeen = true
		t.m.Start = int(index(items[1], &t.funcs, "function"))
	case "elem":
		t.elem(items, i)
	case "data":
		d := Data{Mode: ModePassive}
		if i < len(items) && items[i].isList {
			d.Mode = ModeActive
			if items[i].head() == "memory" {
				if len(items[i].list) != 2 {
					fail("unexpected token")
				}
				d.Memory = index(items[i].list[1], &t.mems, "memory")
				i++
			}
			if i >= len(items) || !items[i].isList {
				fail("unexpected token")
			}
			d.Offset = t.offset(items[i])
			i++
		}
		for _, k := range items[i:] {
			d.Bytes = append(d.Bytes, str(k)...)
		}
		t.m.Datas = append(t.m.Datas, d)
	}
}

// offset parses (offset expr) or a single folded instruction.
func (t *textModule) offset(e *sexp) []Instr {
	if e.head() == "offset" {
		return t.constExpr(e.list[1:])
	}
	return t.constExpr([]*sexp{e})
}

func (t *textModule) constExpr(items []*sexp) []Instr {
	fc := &funcCtx{t: t, locals: map[string]uint32{}}
	fc.seq(items)
	return append(fc.out, Instr{Op: opEnd})
}

// elemExpr parses an element expression: (item expr) or a folded
// instruction.
func (t *textModule) elemExpr(e *sexp) []Instr {
	if e.head() == "item" {
		return t.constExpr(e.list[1:])
	}
	if !e.isList {
		fail("unexpected token")
	}
	return t.constExpr([]*sexp{e})
}

func (t *textModule) elem(items []*sexp, i int) {
	e := Elem{Mode: ModePassive, Type: FuncRef}
	if i < len(items) && items[i].keyword("declare") {
		e.Mode = ModeDeclarative
		i++
	} else if i < len(items) && items[i].isList {
		e.Mode = ModeActive
		if items[i].head() == "table" {
			if len(items[i].list) != 2 {
				fail("unexpected token")
			}
			e.Table = index(items[i].list[1], &t.tables, "table")
			i++
		}
		if i >= len(items) || !items[i].isList {
			fail("unexpected token")
		}
		e.Offset = t.offset(items[i])
		i++
		// The legacy form: function indices without "func".
		if i == len(items) || isIndex(items[i]) {
			for _, k := range items[i:] {
				e.Init = append(e.Init, []Instr{{Op: opRefFunc, A: index(k, &t.funcs, "function")}, {Op: opEnd}})
			}
			t.m.Elems = append(t.m.Elems, e)
			return
		}
	}
	if i < len(items) && items[i].keyword("func") {
		i++
		for _, k := range items[i:] {
			e.Init = append(e.Init, []Instr{{Op: opRefFunc, A: index(k, &t.funcs, "function")}, {Op: opEnd}})
		}
		t.m.Elems = append(t.m.Elems, e)
		return
	}
	if i < len(items) && !items[i].isList {
		e.Type = refType(items[i])
		i++
	} else if i < len(items) {
		fail("unexpected token")
	}
	for _, k := range items[i:] {
		e.Init = append(e.Init, t.elemExpr(k))
	}
	t.m.Elems = append(t.m.Elems, e)
}

// ---------------------------------------------------------------------
// Instructions.

type funcCtx struct {
	t      *textModule
	locals map[string]uint32
	labels []string
	out    []Instr
}

func (fc *funcCtx) emit(in Instr) { fc.out = append(fc.out, in) }

// seq parses a sequence of instructions, plain and folded.
func (fc *funcCtx) seq(items []*sexp) {
	for i := 0; i < len(items); {
		if items[i].isList {
			fc.folded(items[i])
			i++
			continue
		}
		i = fc.plain(items, i)
	}
}

func (fc *funcCtx) label(id string) {
	fc.labels = append(fc.labels, id)
}

// labelIndex resolves a label reference.
func (fc *funcCtx) labelIndex(e *sexp) uint32 {
	if e.is(tID) {
		for k := len(fc.labels) - 1; k >= 0; k-- {
			if fc.labels[k] == e.tok.text {
				return uint32(len(fc.labels) - 1 - k)
			}
		}
		fail("unknown label %s", e.tok.text)
	}
	return index(e, &space{}, "label")
}

func (fc *funcCtx) localIndex(e *sexp) uint32 {
	if e.is(tID) {
		v, ok := fc.locals[e.tok.text]
		if !ok {
			fail("unknown local %s", e.tok.text)
		}
		return v
	}
	return index(e, &space{}, "local")
}

// blockType parses the label and the block type of a block instruction
// at items[*i:].
func (fc *funcCtx) blockType(items []*sexp, i *int, in *Instr) string {
	id := optID(items, i)
	if *i < len(items) && items[*i].head() == "type" {
		x, _ := fc.t.typeUse(items, i)
		in.A, in.B = 2, x
		return id
	}
	j := *i
	ft, names := funcSig(items, i, false)
	_ = names
	_ = j
	switch {
	case len(ft.Params) == 0 && len(ft.Results) == 0:
	case len(ft.Params) == 0 && len(ft.Results) == 1:
		in.A, in.B = 1, uint32(ft.Results[0])
	default:
		in.A, in.B = 2, fc.t.typeIndex(ft)
	}
	return id
}

// plain parses a plain instruction at items[i] and returns the index
// after it.
func (fc *funcCtx) plain(items []*sexp, i int) int {
	e := items[i]
	if !e.is(tKeyword) {
		fail("unexpected token %s", e)
	}
	kw := e.tok.text
	i++
	switch kw {
	case "block", "loop", "if":
		in := Instr{Op: opByName[kw]}
		id := fc.blockType(items, &i, &in)
		fc.emit(in)
		fc.label(id)
		return i
	case "else", "end":
		if len(fc.labels) == 0 {
			fail("unexpected token %s", kw)
		}
		if i < len(items) && items[i].is(tID) {
			if items[i].tok.text != fc.labels[len(fc.labels)-1] {
				fail("mismatching label")
			}
			i++
		}
		if kw == "end" {
			fc.labels = fc.labels[:len(fc.labels)-1]
			fc.emit(Instr{Op: opEnd})
		} else {
			fc.emit(Instr{Op: opElse})
		}
		return i
	}
	in, n := fc.instr(kw, items[i:])
	fc.emit(in)
	return i + n
}

// folded parses a folded instruction.
func (fc *funcCtx) folded(e *sexp) {
	kw := e.head()
	items := e.list
	i := 1
	switch kw {
	case "":
		fail("unexpected token %s", e)
	case "block", "loop":
		in := Instr{Op: opByName[kw]}
		id := fc.blockType(items, &i, &in)
		fc.emit(in)
		fc.label(id)
		fc.seq(items[i:])
		fc.labels = fc.labels[:len(fc.labels)-1]
		fc.emit(Instr{Op: opEnd})
		return
	case "if":
		in := Instr{Op: opIf}
		id := fc.blockType(items, &i, &in)
		for i < len(items) && items[i].isList && items[i].head() != "then" {
			fc.folded(items[i])
			i++
		}
		if i >= len(items) || items[i].head() != "then" {
			fail("unexpected token")
		}
		fc.emit(in)
		fc.label(id)
		fc.seq(items[i].list[1:])
		i++
		if i < len(items) && items[i].head() == "else" {
			fc.emit(Instr{Op: opElse})
			fc.seq(items[i].list[1:])
			i++
		}
		end(items, i)
		fc.labels = fc.labels[:len(fc.labels)-1]
		fc.emit(Instr{Op: opEnd})
		return
	}
	in, n := fc.instr(kw, items[1:])
	for _, k := range items[1+n:] {
		if !k.isList {
			fail("unexpected token %s", k)
		}
		fc.folded(k)
	}
	fc.emit(in)
}

// instr parses the immediates of instruction kw in items and returns it
// with the number of items used.
func (fc *funcCtx) instr(kw string, items []*sexp) (Instr, int) {
	op, ok := opByName[kw]
	if !ok {
		fail("unknown operator %s", kw)
	}
	t := fc.t
	info := ops[op]
	in := Instr{Op: op}
	i := 0
	atom := func() *sexp {
		if i >= len(items) || items[i].isList {
			fail("unexpected token")
		}
		i++
		return items[i-1]
	}
	optIndex := func() (*sexp, bool) {
		if i < len(items) && isIndex(items[i]) {
			i++
			return items[i-1], true
		}
		return nil, false
	}
	switch info.imm {
	case immLabel:
		in.A = fc.labelIndex(atom())
	case immBrTable:
		for i < len(items) && isIndex(items[i]) {
			in.Ext = append(in.Ext, fc.labelIndex(items[i]))
			i++
		}
		if len(in.Ext) == 0 {
			fail("unexpected token")
		}
	case immFunc:
		in.A = index(atom(), &t.funcs, "function")
	case immCallInd:
		if x, ok := optIndex(); ok {
			in.B = index(x, &t.tables, "table")
		}
		x, names := t.typeUse(items, &i)
		for _, n := range names {
			if n != "" {
				fail("unexpected token")
			}
		}
		in.A = x
	case immLocal:
		in.A = fc.localIndex(atom())
	case immGlobal:
		in.A = index(atom(), &t.globals, "global")
	case immTable:
		if x, ok := optIndex(); ok {
			in.A = index(x, &t.tables, "table")
		}
	case immMem, immMemLane:
		in.A = uint32(bits.TrailingZeros(uint(accessSize(kw))))
		for i < len(items) && !items[i].isList && (strings.HasPrefix(items[i].tok.text, "offset=") || strings.HasPrefix(items[i].tok.text, "align=")) {
			k := items[i].tok.text
			switch {
			case strings.HasPrefix(k, "offset="):
				v, ok, _ := parseUN(k[7:])
				if !ok || v > math.MaxUint32 {
					fail("unknown operator")
				}
				in.C = v
			case strings.HasPrefix(k, "align="):
				v, ok, _ := parseUN(k[6:])
				if !ok || v == 0 || v&(v-1) != 0 || v > math.MaxUint32 {
					fail("alignment")
				}
				in.A = uint32(bits.TrailingZeros64(v))
			default:
				fail("unknown operator")
			}
			i++
		}
		if info.imm == immMemLane {
			in.B = laneIndex(atom())
		}
	case immI32:
		v, err := parseInt(atom().tok.text, 32)
		if err != nil {
			panic(err)
		}
		in.C = v
	case immI64:
		v, err := parseInt(atom().tok.text, 64)
		if err != nil {
			panic(err)
		}
		in.C = v
	case immF32, immF64:
		n := 32
		if info.imm == immF64 {
			n = 64
		}
		v, err := parseFloat(atom().tok.text, n)
		if err != nil {
			panic(err)
		}
		in.C = v
	case immSelectT:
	case immRefType:
		k := atom()
		switch {
		case k.keyword("func"):
			in.A = uint32(FuncRef)
		case k.keyword("extern"):
			in.A = uint32(ExternRef)
		default:
			fail("unexpected token")
		}
	case immElemTable:
		x, ok1 := optIndex()
		y, ok2 := optIndex()
		switch {
		case ok1 && ok2:
			in.B = index(x, &t.tables, "table")
			in.A = index(y, &t.elems, "elem segment")
		case ok1:
			in.A = index(x, &t.elems, "elem segment")
		default:
			fail("unexpected token")
		}
	case immTable2:
		x, ok1 := optIndex()
		y, ok2 := optIndex()
		if ok1 != ok2 {
			fail("unexpected token")
		}
		if ok1 {
			in.A = index(x, &t.tables, "table")
			in.B = index(y, &t.tables, "table")
		}
	case immDataZero, immData:
		in.A = index(atom(), &t.datas, "data segment")
	case immElem:
		in.A = index(atom(), &t.elems, "elem segment")
	case immLane:
		in.A = laneIndex(atom())
	case immV128:
		in.C, in.D = v128Const(items, &i)
	case immShuffle:
		var v v128
		for k := 0; k < 16; k++ {
			v[k] = byte(laneIndex(atom()))
		}
		in.C, in.D = v.halves()
	}
	if op == opSelect {
		// select with result types is the typed form.
		for i < len(items) && items[i].head() == "result" {
			in.Op = opSelectT
			for _, k := range items[i].list[1:] {
				in.Ext = append(in.Ext, uint32(valType(k)))
			}
			i++
		}
	}
	return in, i
}

func laneIndex(e *sexp) uint32 {
	v, ok, _ := parseUN(e.tok.text)
	if e.isList || !ok || v > 255 {
		fail("malformed lane index")
	}
	return uint32(v)
}

// v128Const parses the shape and the lanes of v128.const.
func v128Const(items []*sexp, i *int) (uint64, uint64) {
	if *i >= len(items) || !items[*i].is(tKeyword) {
		fail("unexpected token")
	}
	w, n, float := shape(items[*i].tok.text)
	if w == 0 {
		fail("unexpected token")
	}
	*i++
	var v v128
	for k := 0; k < n; k++ {
		if *i >= len(items) || items[*i].isList {
			fail("wrong number of lane literals")
		}
		text := items[*i].tok.text
		*i++
		var x uint64
		var err error
		if float {
			x, err = parseFloat(text, w)
		} else {
			x, err = parseInt(text, w)
		}
		if err != nil {
			panic(err)
		}
		v.setLane(w, k, x)
	}
	return v.halves()
}
