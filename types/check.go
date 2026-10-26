// Package types type-checks a parsed program (spec/02-types.md,
// spec/03-declarations.md, spec/04-expressions.md, spec/05-statements.md).
//
// It checks package main of a project. Constructs that the checker does
// not support yet are reported as such, with the roadmap step that adds
// them (spec/24-roadmap.md).
package types

import (
	"fmt"
	"go/constant"
	"slices"
	"sort"
	"strconv"
	"strings"

	"an/syntax"
)

// Config describes the package to check and the target.
type Config struct {
	Module string // module name from anlang.mod
	Path   string // import path of the package; "" or Module for package main in the project root
	// Imports are the checked packages of the project that the package
	// imports, by import path.
	Imports  map[string]*Package
	IntBits  int    // size of int and uint in bits (type.int.word); 0 means 64
	AddrBits int    // size of usize in bits (type.int.usize); 0 means IntBits
	Arch     string // the architecture of the target, for asm statements
	// Interrupts are the names of the interrupt vectors of the device of
	// the target (emb.irq.name).
	Interrupts []string
	// DeferNarrowing leaves the narrowing conversions that the checker
	// does not prove to the proof obligations, which have the facts of
	// their program points (type.conv.int-narrow-facts): they are in
	// Info.Narrowings.
	DeferNarrowing bool
}

// Info is the result of type checking.
type Info struct {
	// Types maps expressions, including type expressions, to their types
	// and, for constants, values.
	Types map[syntax.Expr]TypeAndValue
	// Defs maps identifiers to the objects they declare.
	Defs map[*syntax.Name]Object
	// Uses maps identifiers to the objects they denote.
	Uses map[*syntax.Name]Object
	// Scope is the package block.
	Scope *Scope
	// Funcs are the package-level functions in source order.
	Funcs []*Func
	// Handlers are the interrupt handlers, also in Funcs.
	Handlers []*Func
	// Methods are the methods in source order.
	Methods []*Func
	// Main is func main; nil for packages other than main.
	Main *Func
	// Pkg is the checked package.
	Pkg *Package
	// Files are the files of the package.
	Files []*syntax.File
	// Selections maps selector expressions of fields and methods to
	// what they select.
	Selections map[*syntax.SelectorExpr]*Selection
	// Instances maps the identifiers of generic functions and types to
	// their instantiations at their uses.
	Instances map[*syntax.Name]Instance
	// Computed lists the constants computed by function calls, in the
	// order of their declarations (ct.const.call).
	Computed []*Const
	// Bytes are the []u8 constants of calls of the functions of
	// an/encoding (ct.encoding.const).
	Bytes map[syntax.Expr][]byte
	// Templates are the functions checked per instance; TemplateCalls
	// maps calls of them to the instance they use (ct.any.instance).
	Templates         map[*Func]bool
	TemplateCalls     map[*syntax.CallExpr]*TemplateInstance
	TemplateInstances []*TemplateInstance
	// Unrolled holds the instances of the bodies of for const loops
	// (ct.for.unroll).
	Unrolled map[*syntax.RangeStmt][]*Unrolled
	// StaticSwitch maps a static type switch to its chosen clause
	// (ct.typeswitch.static).
	StaticSwitch map[*syntax.TypeSwitchStmt]int
	// MetaCalls maps calls of descriptor methods to what they do.
	MetaCalls map[*syntax.CallExpr]*MetaCall
	// ContractResults maps the ensures clauses of the functions with one
	// unnamed result to the variable result (contract.expr.result).
	ContractResults map[*syntax.ContractStmt]*Var
	// Narrowings maps the operands of the narrowing conversions left to the
	// proof obligations to the types they convert to (Config.DeferNarrowing).
	Narrowings map[syntax.Expr]Type
	// Proven holds the expressions and statements whose run-time checks the
	// solver proves not to fail (contract.check.runtime): index and slice
	// expressions, arithmetic, and the statements x op= y and x++.
	Proven map[syntax.Node]bool
	// RecursionDepth maps the functions that call themselves to the
	// proven bound on the depth of their recursion (rt.stack.core-recursion).
	RecursionDepth map[*Func]int

	ctx *checker // for the type operations of the API

	intBits, addrBits int // the widths of int and usize on the target
}

// TypeAndValue is the type and, for a constant, the value of an
// expression.
type TypeAndValue struct {
	mode  operandMode
	Type  Type
	Value constant.Value // nil unless the expression is a constant
}

// IsType reports whether the expression denotes a type.
func (tv TypeAndValue) IsType() bool { return tv.mode == typexpr }

// IsBuiltin reports whether the expression denotes a built-in function.
func (tv TypeAndValue) IsBuiltin() bool { return tv.mode == builtin }

// IsValue reports whether the expression denotes a value.
func (tv TypeAndValue) IsValue() bool {
	switch tv.mode {
	case constant_, variable, mapindex, value, commaok:
		return true
	}
	return false
}

const unsupported = "not supported yet by this compiler"

type checker struct {
	conf     *Config
	intBits  int
	addrBits int
	files    []*syntax.File
	errors   []*syntax.Error
	info     *Info
	pkg      *Package
	pkgScope *Scope

	decls        map[Object]*declInfo
	methodDecls  []methodDecl
	aliasMethods []aliasMethod
	methods      map[*TypeName][]*Func // methods by receiver base type
	lazy         map[*TypeName]*lazyAlias

	invalidSwitch   map[*syntax.SwitchStmt]bool // switches with an invalid tag
	instances       map[string]*Named           // instances of generic types
	enumVariants    map[*TypeName][]*Variant    // variants of enum type declarations
	variantSwitches map[*syntax.SwitchStmt]bool // variant switches: whether exhaustive or with default
	inArgs          bool                        // checking call arguments
	held            []heldPath                  // paths held by enclosing inout loops
	pathDepth       int                         // nesting of the operand of selectors, indices and slices
	callee          syntax.Expr                 // the callee of the call being checked
	secondOK        bool                        // a second-class closure may initialize a new variable
	secondLits      map[*syntax.FuncLit]bool    // function literals that capture inout variables
	funcName        string                      // the function whose body is checked
	zeroEdges       []zeroEdge                  // type arguments, for the zero value constraint
	pendingZero     map[syntax.Expr]pendingZero // single-value map index and receive expressions
	delayed         []func()                    // checks run when declarations and bodies are complete
	interfaces      []interfaceInfo             // interface types, checked for method conflicts at the end
	objList         []Object                    // package-level objects in source order, including blank ones
	objPath         []Object                    // objects whose declarations are being checked

	untyped map[syntax.Expr]untypedInfo

	// State of the declaration or function body being checked.
	scope  *Scope
	iota   constant.Value
	errPos syntax.Pos // if valid, the position of all errors // value of iota in a constant declaration; nil otherwise
	fn     *funcContext

	// The package-level constant whose initializer is checked, for
	// effect.Declare (eff.custom.declare).
	declConst  *Const
	declInit   syntax.Expr
	declMapVar *Var // the package-level variable whose unsafe.Map initializer is checked
	inEnsures  bool // checking the expression of an ensures clause
	inContract int  // checking a contract expression or the argument of assert

	// rangeKeys are the range key variables with a known number of
	// iterations (type.conv.int-narrow-facts 6).
	rangeKeys map[*Var]int64

	// Templates (spec/10-compile-time.md).
	templates map[*Func]bool
	tmplInsts map[string]*TemplateInstance
	instance  *TemplateInstance // the instance whose body is checked
	instTuple []Type            // the tuple of the instance signature being computed
	tupleOK   bool              // a tuple parameter may be used here (ct.any.use)
	errNote   string            // appended to the errors of an instance
	unrolling bool              // checking an instance of the body of a for const loop
	metaSeqs  map[syntax.Expr][]*Const
}

// declInfo is the declaration of a package-level object.
type declInfo struct {
	file   *Scope
	from   *syntax.ValueSpec // spec that gives the type and initializer of a constant
	typ    syntax.Expr       // type of a constant or variable; may be nil
	init   syntax.Expr       // initializer of a constant or variable; may be nil
	iota   int               // index of the constant spec
	vars   []*Var            // all variables of a spec with a multi-value initializer
	tspec  *syntax.TypeSpec  // type declaration
	enumOf *TypeName         // enum type of a variant
	fdecl  *syntax.FuncDecl  // function declaration
	scope  *Scope            // scope of the type parameters of a function, the parent of its body
	spec   *syntax.ValueSpec // const or var spec
}

// Check checks package main. The Info is returned even if there are
// errors; it then may be incomplete.
func Check(conf *Config, files []*syntax.File) (*Info, []*syntax.Error) {
	c := &checker{
		conf:     conf,
		intBits:  conf.IntBits,
		addrBits: conf.AddrBits,
		files:    files,
		info: &Info{
			Files: files,
			Types: map[syntax.Expr]TypeAndValue{},
			Defs:  map[*syntax.Name]Object{},
			Uses:  map[*syntax.Name]Object{},

			Selections: map[*syntax.SelectorExpr]*Selection{},
			Instances:  map[*syntax.Name]Instance{},

			Templates:     map[*Func]bool{},
			TemplateCalls: map[*syntax.CallExpr]*TemplateInstance{},
			Unrolled:      map[*syntax.RangeStmt][]*Unrolled{},
			StaticSwitch:  map[*syntax.TypeSwitchStmt]int{},
			MetaCalls:     map[*syntax.CallExpr]*MetaCall{},

			Bytes: map[syntax.Expr][]byte{},

			ContractResults: map[*syntax.ContractStmt]*Var{},
			Narrowings:      map[syntax.Expr]Type{},
			Proven:          map[syntax.Node]bool{},
			RecursionDepth:  map[*Func]int{},
		},
		pkgScope: NewScope(Universe),
		decls:    map[Object]*declInfo{},
		methods:  map[*TypeName][]*Func{},
		lazy:     map[*TypeName]*lazyAlias{},

		invalidSwitch:   map[*syntax.SwitchStmt]bool{},
		instances:       map[string]*Named{},
		pendingZero:     map[syntax.Expr]pendingZero{},
		enumVariants:    map[*TypeName][]*Variant{},
		variantSwitches: map[*syntax.SwitchStmt]bool{},
		untyped:         map[syntax.Expr]untypedInfo{},
		rangeKeys:       map[*Var]int64{},
		templates:       map[*Func]bool{},
		tmplInsts:       map[string]*TemplateInstance{},
		metaSeqs:        map[syntax.Expr][]*Const{},
	}
	if c.intBits == 0 {
		c.intBits = 64
	}
	if c.addrBits == 0 {
		c.addrBits = c.intBits
	}
	c.info.intBits, c.info.addrBits = c.intBits, c.addrBits
	path := conf.Path
	if path == "" {
		path = conf.Module
	}
	c.pkg = &Package{Path: path, Scope: c.pkgScope}
	c.info.Scope, c.info.Pkg = c.pkgScope, c.pkg
	c.check()
	sort.SliceStable(c.errors, func(i, j int) bool { return posLess(c.errors[i].Pos, c.errors[j].Pos) })
	return c.info, c.errors
}

func posLess(a, b syntax.Pos) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	return a.Offset < b.Offset
}

func (c *checker) errorf(pos syntax.Pos, rule, format string, args ...any) {
	if c.errPos.IsValid() {
		pos = c.errPos
	}
	c.errors = append(c.errors, &syntax.Error{Pos: pos, Rule: rule, Msg: fmt.Sprintf(format, args...) + c.errNote})
}

// notSupported reports a construct that this compiler does not support
// yet. step is the roadmap step that adds it.
func (c *checker) notSupported(pos syntax.Pos, step int, what string) {
	c.errorf(pos, "", "%s: %s (spec/24-roadmap.md, step %d)", what, unsupported, step)
}

func (c *checker) check() {
	c.checkPackageClauses()
	fileScopes := c.collectObjects()
	c.collectMethods()

	for _, obj := range c.objList {
		c.objDecl(obj)
	}
	c.checkMethods()
	if c.isRoot() {
		c.checkMain()
	}

	for _, f := range append(c.info.Funcs, c.info.Methods...) {
		if c.isTemplate(f) {
			if r := f.Signature().Recv; r != nil && len(f.Signature().RecvTypeParams) > 0 {
				c.notSupported(f.pos, 29, "methods with ...any parameters of generic types")
				continue
			}
			c.templates[f] = true
			c.info.Templates[f] = true
		}
	}
	for _, f := range append(c.info.Funcs, c.info.Methods...) {
		if c.templates[f] {
			continue // checked per instance
		}
		if d := c.decls[f].fdecl; d.Body != nil {
			scope := c.decls[f].scope
			if scope == nil {
				scope = c.decls[f].file
			}
			c.funcName = f.name
			if r := f.Signature().Recv; r != nil {
				c.funcName = fmt.Sprintf("%s.%s", r.typ, f.name)
			}
			c.funcBody(scope, f.Signature(), d.Recv, d.Type, d.Body, false)
		}
	}

	c.checkInterfaces()
	for i := 0; i < len(c.delayed); i++ {
		c.delayed[i]()
	}
	c.checkPendingZero()
	c.checkZeroEdges()

	for _, s := range fileScopes {
		for _, name := range s.Names() {
			if p := s.Lookup(name).(*PkgName); !p.used {
				c.errorf(p.pos, "decl.import.used", "%q imported and not used", p.Imported.Path)
			}
		}
	}
}

// isRoot reports whether the package is the project root, package main.
func (c *checker) isRoot() bool { return c.pkg.Path == c.conf.Module }

// checkPackageClauses checks that all files are package main and agree
// on the profile clause.
func (c *checker) checkPackageClauses() {
	var profiles string
	var profilesFrom *syntax.File
	root := c.isRoot()
	// Hyphens of a target package's directory are dropped.
	last := strings.ReplaceAll(c.pkg.Path[strings.LastIndex(c.pkg.Path, "/")+1:], "-", "")
	for i, f := range c.files {
		name := f.Name.Name
		switch {
		case root && name != "main":
			c.errorf(f.Name.Pos(), "decl.package.main", "package %s in the project root: a program is built from package main", name)
		case root:
		case i > 0 && name != c.pkg.Name:
			c.errorf(f.Name.Pos(), "decl.package.dir", "package %s differs from package %s in %s", name, c.pkg.Name, c.files[0].Name.Pos().File)
		case i == 0 && name != last && name != "main":
			c.errorf(f.Name.Pos(), "decl.package.name", "package %s in directory %s: the package name must be %s", name, c.pkg.Path, last)
		}
		if i == 0 {
			c.pkg.Name = name
		}
		seen := map[string]bool{}
		for _, p := range f.Profiles() {
			switch p.Name {
			case "core", "app", "ui":
			default:
				c.errorf(p.Pos(), "decl.profile.names", "unknown profile %s: want core, app or ui", p.Name)
			}
			if seen[p.Name] {
				c.errorf(p.Pos(), "decl.profile.names", "profile %s repeated in the profile clause", p.Name)
			}
			seen[p.Name] = true
		}
		if f.Profile == nil {
			continue
		}
		set := profileSet(f.Profiles())
		if profilesFrom == nil {
			profiles, profilesFrom = set, f
		} else if set != profiles {
			c.errorf(f.Profile.Pos(), "decl.profile.package", "profile clause %s differs from %s in %s", set, profiles, profilesFrom.Name.Pos().File)
		}
	}
	c.pkg.Profiles = []string{"app"}
	if profilesFrom != nil {
		c.pkg.Profiles = nil
		for _, p := range []string{"core", "app", "ui"} {
			if slices.Contains(strings.Split(profiles, ", "), p) {
				c.pkg.Profiles = append(c.pkg.Profiles, p)
			}
		}
	}
}

// profileSet returns the set of profile names in a canonical spelling.
func profileSet(ps []*syntax.Name) string {
	var names []string
	for _, p := range ps {
		if !slices.Contains(names, p.Name) {
			names = append(names, p.Name)
		}
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// collectObjects declares the imports of every file in its file scope
// and the package-level objects in the package scope. It returns the
// file scopes.
func (c *checker) collectObjects() []*Scope {
	var fileScopes []*Scope
	for _, f := range c.files {
		fs := NewScope(c.pkgScope)
		fileScopes = append(fileScopes, fs)
		c.imports(f, fs)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *syntax.GenDecl:
				c.collectGenDecl(fs, d)
			case *syntax.FuncDecl:
				c.collectFunc(fs, d)
			}
		}
	}
	// An import and a package-level declaration conflict (decl.visibility.go).
	for _, fs := range fileScopes {
		for _, name := range fs.Names() {
			if alt := c.pkgScope.Lookup(name); alt != nil {
				c.errorf(alt.Pos(), "decl.visibility.go", "%s already declared through import of package %s at %s", name, name, fs.Lookup(name).Pos())
			}
		}
	}
	return fileScopes
}

func (c *checker) collectGenDecl(fs *Scope, d *syntax.GenDecl) {
	switch d.Tok {
	case syntax.Import:
	case syntax.Const:
		var last *syntax.ValueSpec // spec whose type and values repeat (decl.const.go)
		for i, s := range d.Specs {
			s := s.(*syntax.ValueSpec)
			if s.Type != nil || len(s.Values) > 0 {
				last = s
			}
			for j, name := range s.Names {
				obj := &Const{object: object{pos: name.Pos(), name: name.Name}}
				di := &declInfo{file: fs, iota: i, spec: s, from: last}
				if last != nil {
					di.typ = last.Type
					if j < len(last.Values) {
						di.init = last.Values[j]
					}
				}
				c.declarePkg(name, obj, di)
			}
			c.checkConstArity(s, last)
		}
	case syntax.Var:
		for _, s := range d.Specs {
			s := s.(*syntax.ValueSpec)
			multi := len(s.Names) > 1 && len(s.Values) == 1
			var vars []*Var
			for j, name := range s.Names {
				obj := &Var{object: object{pos: name.Pos(), name: name.Name}}
				vars = append(vars, obj)
				di := &declInfo{file: fs, typ: s.Type, spec: s}
				switch {
				case multi:
					di.init = s.Values[0]
				case j < len(s.Values):
					di.init = s.Values[j]
				}
				c.declarePkg(name, obj, di)
			}
			if multi {
				for _, v := range vars {
					c.decls[v].vars = vars
				}
			} else {
				c.checkVarArity(s)
			}
		}
	case syntax.Type:
		for _, s := range d.Specs {
			s := s.(*syntax.TypeSpec)
			obj := &TypeName{object{pos: s.Name.Pos(), name: s.Name.Name}}
			c.declarePkg(s.Name, obj, &declInfo{file: fs, tspec: s})
			if et, ok := s.Type.(*syntax.EnumType); ok {
				// The variants are declared in the package block
				// (type.enum.variant-scope).
				seen := map[string]bool{}
				for i, v := range c.newVariants(obj, et) {
					if seen[v.name] {
						continue // reported by enumType (type.enum.nonempty)
					}
					seen[v.name] = true
					c.declarePkg(et.Variants[i].Name, v, &declInfo{file: fs, enumOf: obj})
				}
			}
		}
	}
}

// checkConstArity checks the number of names and values of a constant
// spec; last is the spec that gives the values (decl.const.go).
func (c *checker) checkConstArity(s, last *syntax.ValueSpec) {
	switch {
	case last == nil:
		c.errorf(s.Pos(), "decl.const.go", "missing init expr for const declaration")
	case len(s.Values) == 0 && s.Type != nil:
		c.errorf(s.Pos(), "decl.const.go", "missing init expr for const declaration")
	case len(last.Values) < len(s.Names):
		c.errorf(s.Pos(), "decl.const.go", "missing init expr for const declaration")
	case len(last.Values) > len(s.Names) && last == s:
		c.errorf(s.Values[len(s.Names)].Pos(), "decl.const.go", "extra init expr")
	}
}

func (c *checker) checkVarArity(s *syntax.ValueSpec) {
	switch {
	case len(s.Values) == 0:
		if s.Type == nil {
			c.errorf(s.Pos(), "decl.var.local", "missing type or init expr")
		}
	case len(s.Values) < len(s.Names):
		c.errorf(s.Pos(), "stmt.assign.go", "assignment mismatch: %d variables but %d values", len(s.Names), len(s.Values))
	case len(s.Values) > len(s.Names):
		c.errorf(s.Values[len(s.Names)].Pos(), "stmt.assign.go", "assignment mismatch: %d variables but %d values", len(s.Names), len(s.Values))
	}
}

func (c *checker) collectFunc(fs *Scope, d *syntax.FuncDecl) {
	switch {
	case d.Recv != nil:
		c.methodDecls = append(c.methodDecls, methodDecl{fs, d})
		return
	case d.Handler == "interrupt":
		c.handlerDecl(fs, d)
		return
	case d.Handler != "":
		c.notSupported(d.Pos(), 54, d.Handler+" handlers")
		return
	case d.Gpu.IsValid():
		c.notSupported(d.Pos(), 64, "gpu functions")
		return
	case d.Binding != nil:
		c.notSupported(d.Pos(), 35, "external functions")
		return
	case d.Body == nil:
		// Declared anyway, so its uses are checked.
		c.errorf(d.Pos(), "decl.func.body", "missing function body")
	}
	if d.Name.Name == "init" {
		c.errorf(d.Name.Pos(), "decl.func.no-init", "func init is not allowed in Anlang: there are no package initializers")
		return
	}
	obj := &Func{object: object{pos: d.Name.Pos(), name: d.Name.Name, pkg: c.pkg}, Decl: d}
	if c.declarePkg(d.Name, obj, &declInfo{file: fs, fdecl: d}) {
		c.info.Funcs = append(c.info.Funcs, obj)
	}
}

// handlerDecl declares an interrupt handler (spec/14-embedded.md §3): a
// function without a name in any scope, checked like the others.
func (c *checker) handlerDecl(fs *Scope, d *syntax.FuncDecl) {
	name := d.Name.Name
	if len(c.pkg.Profiles) != 1 || c.pkg.Profiles[0] != "core" {
		c.errorf(d.Pos(), "emb.irq.profile", "interrupt %s: interrupt handlers are permitted only in packages whose profile set is exactly {core}", name)
	}
	if d.Type.TypeParams != nil {
		c.errorf(d.Name.Pos(), "decl.handler.no-generic", "interrupt %s has type parameters", name)
	}
	if len(d.Type.Params.List) > 0 || d.Type.Results != nil && len(d.Type.Results.List) > 0 {
		c.errorf(d.Name.Pos(), "emb.irq.signature", "interrupt %s has parameters or results: a handler has neither", name)
	}
	if !slices.Contains(c.conf.Interrupts, name) {
		c.errorf(d.Name.Pos(), "emb.irq.name", "interrupt %s: the device of the target has no interrupt vector %s", name, name)
	}
	for _, h := range c.info.Handlers {
		if h.Handler == name {
			c.errorf(d.Name.Pos(), "emb.irq.name", "interrupt %s has a second handler (the first at %s)", name, h.Pos())
		}
	}
	obj := &Func{object: object{pos: d.Name.Pos(), name: "interrupt " + name, pkg: c.pkg, parent: c.pkgScope}, Decl: d, Handler: name}
	c.info.Defs[d.Name] = nil
	c.decls[obj] = &declInfo{file: fs, fdecl: d}
	c.objList = append(c.objList, obj)
	c.info.Funcs = append(c.info.Funcs, obj)
	c.info.Handlers = append(c.info.Handlers, obj)
}

// declarePkg declares a package-level object. It reports whether the
// object is to be checked: blank objects are checked, but not declared
// (decl.visibility.go).
func (c *checker) declarePkg(name *syntax.Name, obj Object, di *declInfo) bool {
	obj.base().pkg = c.pkg
	if _, isFunc := obj.(*Func); name.Name == "init" && !isFunc {
		c.errorf(name.Pos(), "decl.visibility.go", "cannot declare init: the name is reserved, as in Go")
		return false
	}
	if name.Name == "_" {
		c.info.Defs[name] = nil
		obj.base().parent = c.pkgScope
	} else if alt := c.pkgScope.Insert(obj); alt != nil {
		rule := "decl.visibility.go"
		if _, ok := obj.(*Variant); ok {
			rule = "type.enum.variant-scope"
		}
		c.errorf(name.Pos(), rule, "%s redeclared in this block (previous declaration at %s)", name.Name, alt.Pos())
		return false
	} else {
		c.info.Defs[name] = obj
	}
	c.decls[obj] = di
	c.objList = append(c.objList, obj)
	return true
}

func (c *checker) checkMain() {
	obj := c.pkgScope.Lookup("main")
	m, ok := obj.(*Func)
	if !ok {
		pos := syntax.Pos{}
		if obj != nil {
			pos = obj.Pos()
		} else if len(c.files) > 0 {
			pos = c.files[0].Name.Pos()
		}
		c.errorf(pos, "decl.package.main", "package main has no func main")
		return
	}
	c.info.Main = m
	if sig := m.Signature(); sig.Params.Len() > 0 || sig.Results.Len() > 0 || len(sig.TypeParams) > 0 {
		c.errorf(m.pos, "decl.package.main", "func main must have no parameters and no results")
	}
}

// imports declares the imports of f in its file scope fs.
func (c *checker) imports(f *syntax.File, fs *Scope) {
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		switch name {
		case "_":
			c.errorf(spec.Name.Pos(), "decl.import.no-blank-dot", "blank imports are removed in Anlang: imports have no side effects")
			continue
		case ".":
			continue // reported by the parser
		}
		pkg := c.importPath(spec, path, err)
		if pkg == nil {
			// Declare the name anyway, so its uses are not reported.
			pkg = &Package{Path: path, Name: name}
		}
		obj := &PkgName{object: object{pos: spec.Pos(), name: name, typ: Typ[Invalid]}, Imported: pkg, used: pkg.Scope == nil}
		if alt := fs.Insert(obj); alt != nil {
			c.errorf(spec.Pos(), "decl.import.name", "%s redeclared in this file (previous import at %s)", name, alt.Pos())
			continue
		}
		if spec.Name != nil {
			c.info.Defs[spec.Name] = obj
		}
	}
}

// importPath checks the import path of spec and returns the imported
// package, or nil if this compiler cannot compile its uses.
func (c *checker) importPath(spec *syntax.ImportSpec, path string, err error) *Package {
	if err != nil || path == "" {
		c.errorf(spec.Path.Pos(), "decl.import.paths", "invalid import path %s", spec.Path.Value)
		return nil
	}
	elems := strings.Split(path, "/")
	std := elems[0] == "an" && len(elems) > 1
	own := elems[0] == c.conf.Module
	if !std && !own {
		c.errorf(spec.Path.Pos(), "decl.import.paths", "import path %q must start with an/ (standard library) or %s/ (this module)", path, c.conf.Module)
		return nil
	}
	if i := slices.IndexFunc(elems, func(e string) bool { return !validPathElem(e) }); i >= 0 {
		c.errorf(spec.Path.Pos(), "decl.import.paths", "invalid element %q in import path %q: want [a-z][a-z0-9_]*", elems[i], path)
		return nil
	}
	if i := slices.Index(elems, "internal"); i >= 0 {
		parent := strings.Join(elems[:i], "/")
		if c.pkg.Path != parent && !strings.HasPrefix(c.pkg.Path, parent+"/") {
			c.errorf(spec.Path.Pos(), "decl.import.internal", "package %s may import %q only from within %s", c.pkg.Path, path, parent)
			return nil
		}
	}
	if own {
		switch {
		case path == c.conf.Module:
			c.errorf(spec.Path.Pos(), "decl.package.main", "package main cannot be imported")
			return nil
		case path == c.pkg.Path:
			c.errorf(spec.Path.Pos(), "decl.import.no-cycle", "package %s imports itself", path)
			return nil
		}
		pkg := c.conf.Imports[path]
		switch {
		case pkg == nil:
			c.errorf(spec.Path.Pos(), "decl.import.paths", "no package %q in this project", path)
		case pkg.Name == "main":
			c.errorf(spec.Path.Pos(), "decl.package.main", "%q is a package main and cannot be imported", path)
			return nil
		default:
			c.profileImport(spec, pkg)
		}
		return pkg
	}
	if arch, ok := strings.CutPrefix(path, "an/arch/"); ok && !strings.HasPrefix(c.pkg.Path, "an/arch") {
		// An instruction package only in files of the architecture
		// (target.arch.import-suffix).
		file := strings.TrimSuffix(pathBase(spec.Path.Pos().File), ".an")
		if !strings.HasSuffix(file, "_"+arch) {
			c.errorf(spec.Path.Pos(), "target.arch.import-suffix", "%q may be imported only in files with the suffix _%s", path, arch)
		}
	}
	if pkg := c.conf.Imports[path]; pkg != nil {
		return pkg // a standard library package compiled from source
	}
	pkg := stdPackages[path]
	if pkg == nil && path == "an/device" {
		// The build provides an/device for a target with a device
		// description.
		c.errorf(spec.Path.Pos(), "emb.device.package", "package an/device exists only for a target with a device description, and this target has none")
		return nil
	}
	if pkg == nil {
		c.notSupported(spec.Path.Pos(), 29, fmt.Sprintf("package %q", path))
	}
	if path == "an/unsafe" && (len(c.pkg.Profiles) != 1 || c.pkg.Profiles[0] != "core") {
		c.errorf(spec.Path.Pos(), "decl.import.unsafe", "package an/unsafe may be imported only by a package whose profile set is exactly {core}")
	}
	return pkg
}

// profileImport checks that for every profile of the package, the
// imported package declares the same or a stricter profile
// (eff.profile.import).
func (c *checker) profileImport(spec *syntax.ImportSpec, imported *Package) {
	rank := map[string]int{"core": 0, "app": 1, "ui": 2}
	for _, p := range c.pkg.Profiles {
		ok := false
		for _, q := range imported.Profiles {
			if rank[q] <= rank[p] {
				ok = true
			}
		}
		if !ok {
			c.errorf(spec.Path.Pos(), "eff.profile.import", "package %s with profile %s cannot import %s, whose profiles are %s", c.pkg.Path, p, imported.Path, strings.Join(imported.Profiles, ", "))
			return
		}
	}
}

// validPathElem reports whether e is a valid import path element.
func validPathElem(e string) bool {
	if e == "" || e[0] < 'a' || e[0] > 'z' {
		return false
	}
	for _, r := range e {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func unparen(x syntax.Expr) syntax.Expr {
	for {
		p, ok := x.(*syntax.ParenExpr)
		if !ok {
			return x
		}
		x = p.X
	}
}

func pathBase(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}
