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
	Module   string // module name from anlang.mod
	IntBits  int    // size of int and uint in bits (type.int.word); 0 means 64
	AddrBits int    // size of usize in bits (type.int.usize); 0 means IntBits
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
	// Methods are the methods in source order.
	Methods []*Func
	// Main is func main.
	Main *Func
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
	pkgScope *Scope

	decls        map[Object]*declInfo
	methodDecls  []methodDecl
	aliasMethods []aliasMethod
	methods      map[*TypeName][]*Func // methods by receiver base type
	lazy         map[*TypeName]*lazyAlias
	interfaces   []interfaceInfo // interface types, checked for method conflicts at the end
	objList      []Object        // package-level objects in source order, including blank ones
	objPath      []Object        // objects whose declarations are being checked

	untyped map[syntax.Expr]untypedInfo

	// State of the declaration or function body being checked.
	scope  *Scope
	iota   constant.Value
	errPos syntax.Pos // if valid, the position of all errors // value of iota in a constant declaration; nil otherwise
	fn     *funcContext
}

// declInfo is the declaration of a package-level object.
type declInfo struct {
	file  *Scope
	from  *syntax.ValueSpec // spec that gives the type and initializer of a constant
	typ   syntax.Expr       // type of a constant or variable; may be nil
	init  syntax.Expr       // initializer of a constant or variable; may be nil
	iota  int               // index of the constant spec
	vars  []*Var            // all variables of a spec with a multi-value initializer
	tspec *syntax.TypeSpec  // type declaration
	fdecl *syntax.FuncDecl  // function declaration
	spec  *syntax.ValueSpec // const or var spec
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
			Types: map[syntax.Expr]TypeAndValue{},
			Defs:  map[*syntax.Name]Object{},
			Uses:  map[*syntax.Name]Object{},
		},
		pkgScope: NewScope(Universe),
		decls:    map[Object]*declInfo{},
		methods:  map[*TypeName][]*Func{},
		lazy:     map[*TypeName]*lazyAlias{},
		untyped:  map[syntax.Expr]untypedInfo{},
	}
	if c.intBits == 0 {
		c.intBits = 64
	}
	if c.addrBits == 0 {
		c.addrBits = c.intBits
	}
	c.info.Scope = c.pkgScope
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
	c.errors = append(c.errors, &syntax.Error{Pos: pos, Rule: rule, Msg: fmt.Sprintf(format, args...)})
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
	c.checkMain()

	for _, f := range append(c.info.Funcs, c.info.Methods...) {
		if d := c.decls[f].fdecl; d.Body != nil {
			c.funcBody(c.decls[f].file, f.name, f.Signature(), d, d.Body)
		}
	}

	c.checkInterfaces()

	for _, s := range fileScopes {
		for _, name := range s.Names() {
			if p := s.Lookup(name).(*PkgName); !p.used {
				c.errorf(p.pos, "decl.import.used", "%q imported and not used", p.Imported.Path)
			}
		}
	}
}

// checkPackageClauses checks that all files are package main and agree
// on the profile clause.
func (c *checker) checkPackageClauses() {
	var profiles string
	var profilesFrom *syntax.File
	for _, f := range c.files {
		if f.Name.Name != "main" {
			c.errorf(f.Name.Pos(), "decl.package.main", "package %s in the project root: a program is built from package main", f.Name.Name)
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
	case d.Type.TypeParams != nil:
		c.notSupported(d.Pos(), 2, "generic functions")
		return
	case d.Handler != "":
		c.notSupported(d.Pos(), 17, d.Handler+" handlers")
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
	obj := &Func{object: object{pos: d.Name.Pos(), name: d.Name.Name}, Decl: d}
	if c.declarePkg(d.Name, obj, &declInfo{file: fs, fdecl: d}) {
		c.info.Funcs = append(c.info.Funcs, obj)
	}
}

// declarePkg declares a package-level object. It reports whether the
// object is to be checked: blank objects are checked, but not declared
// (decl.visibility.go).
func (c *checker) declarePkg(name *syntax.Name, obj Object, di *declInfo) bool {
	if _, isFunc := obj.(*Func); name.Name == "init" && !isFunc {
		c.errorf(name.Pos(), "decl.visibility.go", "cannot declare init: the name is reserved, as in Go")
		return false
	}
	if name.Name == "_" {
		c.info.Defs[name] = nil
		obj.base().parent = c.pkgScope
	} else if alt := c.pkgScope.Insert(obj); alt != nil {
		c.errorf(name.Pos(), "decl.visibility.go", "%s redeclared in this block (previous declaration at %s)", name.Name, alt.Pos())
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
	if sig := m.Signature(); sig.Params.Len() > 0 || sig.Results.Len() > 0 {
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
	pkg := stdPackages[path]
	if pkg == nil {
		step := 29
		if own {
			step = 2
		}
		c.notSupported(spec.Path.Pos(), step, fmt.Sprintf("package %q", path))
	}
	return pkg
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
