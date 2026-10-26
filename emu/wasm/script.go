package wasm

import (
	"fmt"
	"math"
	"strings"
)

// Script runs the scripts of the specification test suite (.wast): it
// defines and registers modules, invokes their exports and checks the
// assertions.
type Script struct {
	store      *Store
	named      map[string]*Instance
	registered map[string]*Instance
	current    *Instance
	spectest   map[string]Extern

	Passed   int
	Failures []string
}

// NewScript returns a script runner with the spectest module.
func NewScript() *Script {
	s := &Script{store: NewStore(), named: map[string]*Instance{}, registered: map[string]*Instance{}}
	st := s.store
	host := func(params ...ValType) Extern {
		return Extern{Kind: KindFunc, Func: st.HostFunc(FuncType{Params: params}, func([]uint64) ([]uint64, error) { return nil, nil })}
	}
	global := func(t ValType, v uint64) Extern {
		return Extern{Kind: KindGlobal, Global: &GlobalInst{Type: GlobalType{Type: t}, Lo: v}}
	}
	s.spectest = map[string]Extern{
		"print":         host(),
		"print_i32":     host(I32),
		"print_i64":     host(I64),
		"print_f32":     host(F32),
		"print_f64":     host(F64),
		"print_i32_f32": host(I32, F32),
		"print_f64_f64": host(F64, F64),
		"global_i32":    global(I32, 666),
		"global_i64":    global(I64, 666),
		"global_f32":    global(F32, uint64(math.Float32bits(666.6))),
		"global_f64":    global(F64, math.Float64bits(666.6)),
		"table":         {Kind: KindTable, Table: &Table{Elem: FuncRef, Elems: make([]uint64, 10), Max: 20, HasMax: true}},
		"memory":        {Kind: KindMemory, Memory: &Memory{Data: make([]byte, pageSize), Max: 2, HasMax: true}},
	}
	return s
}

func (s *Script) resolve(module, name string) (Extern, bool) {
	if module == "spectest" {
		e, ok := s.spectest[name]
		return e, ok
	}
	inst := s.registered[module]
	if inst == nil {
		return Extern{}, false
	}
	e, ok := inst.Exports[name]
	return e, ok
}

func (s *Script) failf(line int, format string, args ...any) {
	s.Failures = append(s.Failures, fmt.Sprintf("line %d: %s", line, fmt.Sprintf(format, args...)))
}

// Run runs a script.
func (s *Script) Run(src string) error {
	toks, err := lex(src)
	if err != nil {
		return err
	}
	cmds, err := parseSexps(toks)
	if err != nil {
		return err
	}
	// A script that starts with module fields is one module.
	if len(cmds) > 0 && isModuleField(cmds[0].head()) {
		cmds = []*sexp{{isList: true, list: append([]*sexp{{tok: token{kind: tKeyword, text: "module"}}}, cmds...)}}
	}
	for _, c := range cmds {
		s.command(c)
	}
	return nil
}

func isModuleField(h string) bool {
	switch h {
	case "type", "import", "func", "table", "memory", "global", "export", "start", "elem", "data":
		return true
	}
	return false
}

func line(e *sexp) int {
	for e.isList && len(e.list) > 0 {
		e = e.list[0]
	}
	return e.tok.line
}

// moduleOf decodes or parses a module command and returns its name.
func moduleOf(e *sexp) (m *Module, id string, err error) {
	defer func() {
		if r := recover(); r != nil {
			me, ok := r.(*MalformedError)
			if !ok {
				panic(r)
			}
			m, err = nil, me
		}
	}()
	items := e.list[1:]
	if len(items) > 0 && items[0].is(tID) {
		id = items[0].tok.text
		items = items[1:]
	}
	if len(items) > 0 && (items[0].keyword("binary") || items[0].keyword("quote")) {
		var b strings.Builder
		for _, k := range items[1:] {
			b.WriteString(str(k))
		}
		if items[0].keyword("binary") {
			m, err = Decode([]byte(b.String()))
			return m, id, err
		}
		m, err = ParseText(b.String())
		return m, id, err
	}
	m, err = parseModuleFields(items)
	return m, id, err
}

func (s *Script) instantiate(m *Module) (*Instance, error) {
	return s.store.Instantiate(m, s.resolve)
}

func (s *Script) command(c *sexp) {
	ln := line(c)
	switch c.head() {
	case "module":
		m, id, err := moduleOf(c)
		if err == nil {
			var inst *Instance
			inst, err = s.instantiate(m)
			if err == nil {
				s.current = inst
				if id != "" {
					s.named[id] = inst
				}
				s.Passed++
				return
			}
		}
		s.failf(ln, "module: %v", err)
		s.current = nil
	case "register":
		inst := s.current
		if len(c.list) > 2 {
			inst = s.named[c.list[2].tok.text]
		}
		if inst == nil {
			s.failf(ln, "register: no module")
			return
		}
		s.registered[str(c.list[1])] = inst
	case "invoke", "get":
		if _, err := s.action(c); err != nil {
			s.failf(ln, "%s: %v", c.list[0].tok.text, err)
		}
	case "assert_return":
		got, err := s.action(c.list[1])
		if err != nil {
			s.failf(ln, "assert_return: %v", err)
			return
		}
		want := c.list[2:]
		if len(got) != len(want) {
			s.failf(ln, "assert_return: %d results, want %d", len(got), len(want))
			return
		}
		for k, w := range want {
			if ok, msg := s.matches(got[k], w); !ok {
				s.failf(ln, "assert_return: result %d is %s, want %s", k, msg, w)
				return
			}
		}
		s.Passed++
	case "assert_trap", "assert_exhaustion":
		target := c.list[1]
		var err error
		if target.head() == "module" {
			var m *Module
			m, _, err = moduleOf(target)
			if err == nil {
				_, err = s.instantiate(m)
			}
		} else {
			_, err = s.action(target)
		}
		if !IsTrap(err) {
			s.failf(ln, "%s: got %v, want a trap %s", c.head(), err, c.list[len(c.list)-1])
			return
		}
		s.Passed++
	case "assert_malformed", "assert_invalid", "assert_unlinkable", "assert_uninstantiable":
		m, _, err := moduleOf(c.list[1])
		kind := c.head()
		switch {
		case kind == "assert_malformed":
			if err == nil {
				s.failf(ln, "assert_malformed: the module decodes, want %s", c.list[2])
				return
			}
		case err != nil:
			s.failf(ln, "%s: %v", kind, err)
			return
		case kind == "assert_invalid":
			if err := m.Validate(); !IsInvalid(err) {
				s.failf(ln, "assert_invalid: got %v, want %s", err, c.list[2])
				return
			}
		default:
			if err := m.Validate(); err != nil {
				s.failf(ln, "%s: %v", kind, err)
				return
			}
			_, err := s.instantiate(m)
			if kind == "assert_unlinkable" && !IsLink(err) || kind == "assert_uninstantiable" && !IsTrap(err) {
				s.failf(ln, "%s: got %v, want %s", kind, err, c.list[2])
				return
			}
		}
		s.Passed++
	default:
		s.failf(ln, "unknown command %s", c.head())
	}
}

// action performs an invoke or get action.
func (s *Script) action(a *sexp) ([]Value, error) {
	items := a.list[1:]
	inst := s.current
	if len(items) > 0 && items[0].is(tID) {
		inst = s.named[items[0].tok.text]
		items = items[1:]
	}
	if inst == nil {
		return nil, fmt.Errorf("no module")
	}
	if len(items) == 0 || !items[0].is(tString) {
		return nil, fmt.Errorf("no export name")
	}
	ext, ok := inst.Exports[items[0].tok.text]
	if !ok {
		return nil, fmt.Errorf("no export %q", items[0].tok.text)
	}
	switch a.head() {
	case "get":
		if ext.Kind != KindGlobal {
			return nil, fmt.Errorf("%q is not a global", items[0].tok.text)
		}
		g := ext.Global
		return []Value{{Type: g.Type.Type, Lo: g.Lo, Hi: g.Hi}}, nil
	case "invoke":
		if ext.Kind != KindFunc {
			return nil, fmt.Errorf("%q is not a function", items[0].tok.text)
		}
		var args []Value
		for _, k := range items[1:] {
			v, err := constValue(k)
			if err != nil {
				return nil, err
			}
			args = append(args, v)
		}
		return s.store.Invoke(ext.Func, args)
	}
	return nil, fmt.Errorf("unknown action %s", a.head())
}

// constValue evaluates an argument of an invoke.
func constValue(e *sexp) (v Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			me, ok := r.(*MalformedError)
			if !ok {
				panic(r)
			}
			err = me
		}
	}()
	if !e.isList || len(e.list) < 2 && e.head() != "ref.null" {
		return Value{}, fmt.Errorf("bad constant %s", e)
	}
	arg := ""
	if len(e.list) > 1 {
		arg = e.list[1].tok.text
	}
	switch e.head() {
	case "i32.const":
		x, err := parseInt(arg, 32)
		return Value{Type: I32, Lo: x}, err
	case "i64.const":
		x, err := parseInt(arg, 64)
		return Value{Type: I64, Lo: x}, err
	case "f32.const":
		x, err := parseFloat(arg, 32)
		return Value{Type: F32, Lo: x}, err
	case "f64.const":
		x, err := parseFloat(arg, 64)
		return Value{Type: F64, Lo: x}, err
	case "v128.const":
		i := 1
		lo, hi := v128Const(e.list, &i)
		return Value{Type: V128, Lo: lo, Hi: hi}, nil
	case "ref.null":
		t := FuncRef
		if arg == "extern" {
			t = ExternRef
		}
		return Value{Type: t}, nil
	case "ref.extern":
		x, ok, _ := parseUN(arg)
		if !ok {
			return Value{}, fmt.Errorf("bad constant %s", e)
		}
		return Value{Type: ExternRef, Lo: x + 1}, nil
	}
	return Value{}, fmt.Errorf("bad constant %s", e)
}

// matches checks a result against an expected value, which may be a NaN
// pattern.
func (s *Script) matches(got Value, want *sexp) (bool, string) {
	text := fmt.Sprintf("%s:%#x", got.Type, got.Lo)
	if got.Type == V128 {
		text = fmt.Sprintf("v128:%#016x_%016x", got.Hi, got.Lo)
	}
	if !want.isList {
		return false, text
	}
	arg := ""
	if len(want.list) > 1 {
		arg = want.list[1].tok.text
	}
	switch h := want.head(); h {
	case "f32.const", "f64.const":
		n := 32
		if h == "f64.const" {
			n = 64
		}
		if got.Type != typeOfName(h[:3]) {
			return false, text
		}
		return floatMatches(got.Lo, arg, n), text
	case "v128.const":
		if got.Type != V128 || len(want.list) < 2 {
			return false, text
		}
		w, n, float := shape(arg)
		if float && len(want.list) == 2+n {
			v := vec(got.Lo, got.Hi)
			for k := 0; k < n; k++ {
				if !floatMatches(v.lane(w, k), want.list[2+k].tok.text, w) {
					return false, text
				}
			}
			return true, text
		}
	case "ref.func":
		return got.Type == FuncRef && got.Lo != 0, text
	case "ref.extern":
		if len(want.list) == 1 {
			return got.Type == ExternRef && got.Lo != 0, text
		}
	case "ref.null":
		return got.Lo == 0 && got.Type.isRef(), text
	}
	v, err := constValue(want)
	if err != nil {
		return false, text
	}
	return v.Type == got.Type && v.Lo == got.Lo && (v.Type != V128 || v.Hi == got.Hi), text
}

// floatMatches matches the bits of a float of n bits against a literal
// or a NaN pattern.
func floatMatches(bits uint64, pat string, n int) bool {
	mant := uint(52)
	if n == 32 {
		mant = 23
	}
	exp := (bits >> mant) & (1<<uint(n-1-int(mant)) - 1)
	frac := bits & (1<<mant - 1)
	isNaN := exp == 1<<uint(n-1-int(mant))-1 && frac != 0
	switch pat {
	case "nan:canonical":
		return isNaN && frac == 1<<(mant-1)
	case "nan:arithmetic":
		return isNaN && frac&(1<<(mant-1)) != 0
	}
	want, err := parseFloat(pat, n)
	return err == nil && want == bits
}
