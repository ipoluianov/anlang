// Package types type-checks a parsed program and lowers it to IR
// (spec/02-types.md, spec/03-declarations.md).
//
// It accepts the subset that the parser accepts: package main with
// functions without parameters or results whose bodies call each other
// and fmt.Print / fmt.Println with constant string arguments. The
// checker grows with roadmap step 2.
package types

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"an/ir"
	"an/syntax"
)

const unsupported = "not supported yet by this compiler (spec/24-roadmap.md, step 2)"

// Package is the input of Check: the files of package main and the
// module name from anlang.mod.
type Package struct {
	Module string
	Files  []*syntax.File
}

type checker struct {
	pkg    *Package
	errors []*syntax.Error
	funcs  map[string]*funcInfo
	prog   *ir.Program
	nData  int
}

type funcInfo struct {
	decl *syntax.FuncDecl
	ir   *ir.Func
}

// fileScope holds the imports of one file: local name to import path.
// The path is "" for an import that was rejected, so its uses are not
// reported again.
type fileScope struct {
	imports map[string]string
	used    map[string]bool
	specs   map[string]*syntax.ImportSpec
}

// Check checks the package and returns its IR. If any error is found,
// the program is nil.
func Check(pkg *Package) (*ir.Program, []*syntax.Error) {
	c := &checker{pkg: pkg, funcs: map[string]*funcInfo{}, prog: &ir.Program{}}
	c.check()
	if len(c.errors) > 0 {
		return nil, c.errors
	}
	return c.prog, nil
}

func (c *checker) errorf(pos syntax.Pos, rule, format string, args ...any) {
	c.errors = append(c.errors, &syntax.Error{Pos: pos, Rule: rule, Msg: fmt.Sprintf(format, args...)})
}

func (c *checker) check() {
	c.checkPackageClauses()

	// Collect functions first, so calls may refer to later declarations.
	for _, f := range c.pkg.Files {
		for _, d := range f.Decls {
			fd, ok := d.(*syntax.FuncDecl)
			if !ok {
				if g := d.(*syntax.GenDecl); g.Tok != syntax.Import {
					c.errorf(g.Pos(), "", "%s declarations: %s", g.Tok, unsupported)
				}
				continue
			}
			switch {
			case fd.Recv != nil:
				c.errorf(fd.Pos(), "", "methods: %s", unsupported)
				continue
			case fd.Type.TypeParams != nil:
				c.errorf(fd.Pos(), "", "generic functions: %s", unsupported)
				continue
			case fd.Handler != "" || fd.Gpu.IsValid():
				c.errorf(fd.Pos(), "", "interrupt, trap and gpu functions: %s", unsupported)
				continue
			case fd.Binding != nil:
				c.errorf(fd.Pos(), "", "external functions: %s", unsupported)
				continue
			case fd.Body == nil:
				c.errorf(fd.Pos(), "decl.func.body", "missing function body")
				continue
			}
			name := fd.Name.Name
			switch {
			case name == "init":
				c.errorf(fd.Name.Pos(), "decl.func.no-init", "func init is not allowed in Anlang: there are no package initializers")
				continue
			case name == "_":
				continue
			}
			if prev, ok := c.funcs[name]; ok {
				c.errorf(fd.Name.Pos(), "decl.func.go", "%s redeclared in this package (previous declaration at %s)", name, prev.decl.Name.Pos())
				continue
			}
			fn := &ir.Func{Name: "main." + name}
			c.funcs[name] = &funcInfo{decl: fd, ir: fn}
			c.prog.Funcs = append(c.prog.Funcs, fn)
		}
	}

	if m, ok := c.funcs["main"]; !ok {
		pos := syntax.Pos{}
		if len(c.pkg.Files) > 0 {
			pos = c.pkg.Files[0].Name.Pos()
		}
		c.errorf(pos, "decl.package.main", "package main has no func main")
	} else {
		c.prog.Main = m.ir
		if hasSignature(m.decl) {
			c.errorf(m.decl.Name.Pos(), "decl.package.main", "func main must have no parameters and no results")
		}
	}

	for _, f := range c.pkg.Files {
		scope := c.imports(f)
		for _, d := range f.Decls {
			fd, ok := d.(*syntax.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			info := c.funcs[fd.Name.Name]
			if info == nil || info.decl != fd {
				continue
			}
			if hasSignature(fd) {
				if fd.Name.Name == "main" {
					continue // reported above
				}
				c.errorf(fd.Name.Pos(), "", "functions with parameters or results: %s", unsupported)
				continue
			}
			c.body(scope, fd.Body, info.ir)
		}
		for name, spec := range scope.specs {
			if !scope.used[name] {
				c.errorf(spec.Pos(), "", "%s imported and not used", spec.Path.Value)
			}
		}
	}
}

// checkPackageClauses checks that all files are package main and agree
// on the profile clause.
func (c *checker) checkPackageClauses() {
	var profiles string
	var profilesFrom *syntax.File
	for _, f := range c.pkg.Files {
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

// knownPackages lists the standard library packages that this compiler
// can compile calls to.
var knownPackages = map[string]bool{
	"an/fmt": true,
}

func (c *checker) imports(f *syntax.File) *fileScope {
	s := &fileScope{imports: map[string]string{}, used: map[string]bool{}, specs: map[string]*syntax.ImportSpec{}}
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
		if prev, ok := s.specs[name]; ok {
			c.errorf(spec.Pos(), "decl.import.name", "%s redeclared in this file (previous import at %s)", name, prev.Pos())
			continue
		}
		s.specs[name] = spec
		if !c.importPath(spec, path, err) {
			s.imports[name] = ""
			s.used[name] = true
			continue
		}
		s.imports[name] = path
	}
	return s
}

// importPath checks the import path of spec and reports whether this
// compiler can compile uses of the package.
func (c *checker) importPath(spec *syntax.ImportSpec, path string, err error) bool {
	if err != nil || path == "" {
		c.errorf(spec.Path.Pos(), "decl.import.paths", "invalid import path %s", spec.Path.Value)
		return false
	}
	elems := strings.Split(path, "/")
	std := elems[0] == "an" && len(elems) > 1
	own := elems[0] == c.pkg.Module
	if !std && !own {
		c.errorf(spec.Path.Pos(), "decl.import.paths", "import path %q must start with an/ (standard library) or %s/ (this module)", path, c.pkg.Module)
		return false
	}
	if i := slices.IndexFunc(elems, func(e string) bool { return !validPathElem(e) }); i >= 0 {
		c.errorf(spec.Path.Pos(), "decl.import.paths", "invalid element %q in import path %q: want [a-z][a-z0-9_]*", elems[i], path)
		return false
	}
	if !knownPackages[path] {
		c.errorf(spec.Path.Pos(), "", "package %q: %s", path, unsupported)
		return false
	}
	return true
}

func (c *checker) body(scope *fileScope, b *syntax.BlockStmt, fn *ir.Func) {
	for _, st := range b.List {
		es, ok := st.(*syntax.ExprStmt)
		if !ok {
			c.errorf(st.Pos(), "", "statement: %s", unsupported)
			continue
		}
		call, ok := unparen(es.X).(*syntax.CallExpr)
		if !ok {
			c.errorf(es.Pos(), "", "expression statement must be a call")
			continue
		}
		c.call(scope, call, fn)
	}
	fn.Body = append(fn.Body, &ir.Return{})
}

func (c *checker) call(scope *fileScope, call *syntax.CallExpr, fn *ir.Func) {
	switch f := unparen(call.Fun).(type) {
	case *syntax.Name:
		info, ok := c.funcs[f.Name]
		if !ok {
			if _, isPkg := scope.imports[f.Name]; isPkg {
				c.errorf(f.Pos(), "", "use of package %s without selector", f.Name)
			} else {
				c.errorf(f.Pos(), "", "undefined: %s", f.Name)
			}
			return
		}
		if len(call.Args) > 0 {
			c.errorf(call.Lparen, "", "too many arguments in call to %s", f.Name)
			return
		}
		fn.Body = append(fn.Body, &ir.Call{Func: info.ir})
	case *syntax.SelectorExpr:
		pkgIdent, ok := unparen(f.X).(*syntax.Name)
		var path string
		if ok {
			path, ok = scope.imports[pkgIdent.Name]
		}
		if !ok {
			c.errorf(f.Pos(), "", "selector expressions other than package-qualified calls: %s", unsupported)
			return
		}
		scope.used[pkgIdent.Name] = true
		if path == "" {
			return // the import was rejected
		}
		c.stdCall(path, f.Sel, call, fn)
	default:
		c.errorf(call.Pos(), "", "call of this form: %s", unsupported)
	}
}

// stdCall compiles a call to a standard library function.
//
// Temporary: fmt.Print and fmt.Println are built into the compiler until
// the standard library is compiled from Anlang source (roadmap steps 9–10).
func (c *checker) stdCall(path string, sel *syntax.Name, call *syntax.CallExpr, fn *ir.Func) {
	if path != "an/fmt" || (sel.Name != "Print" && sel.Name != "Println") {
		c.errorf(sel.Pos(), "", "%s.%s: %s", path, sel.Name, unsupported)
		return
	}
	if call.Ellipsis.IsValid() {
		c.errorf(call.Lparen, "", "... in call to fmt.%s: %s", sel.Name, unsupported)
		return
	}
	var parts []string
	for _, arg := range call.Args {
		lit, ok := unparen(arg).(*syntax.BasicLit)
		if !ok || lit.Kind != syntax.String {
			c.errorf(arg.Pos(), "", "fmt.%s arguments other than string literals: %s", sel.Name, unsupported)
			return
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			c.errorf(lit.Pos(), "lex.literal.go", "invalid string literal %s", lit.Value)
			return
		}
		parts = append(parts, s)
	}
	// Go semantics: Println separates operands with spaces and appends a
	// newline; Print adds spaces only between non-string operands.
	text := strings.Join(parts, "")
	if sel.Name == "Println" {
		text = strings.Join(parts, " ") + "\n"
	}
	if text == "" {
		return
	}
	d := &ir.Data{Name: fmt.Sprintf("str.%d", c.nData), Bytes: []byte(text)}
	c.nData++
	c.prog.Data = append(c.prog.Data, d)
	fn.Body = append(fn.Body, &ir.PrintStdout{Data: d})
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

func hasSignature(fd *syntax.FuncDecl) bool {
	return fd.Type.Params.NumFields() > 0 || fd.Type.Results != nil
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
