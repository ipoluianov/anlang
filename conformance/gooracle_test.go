package conformance

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	goscanner "go/scanner"
	"go/token"
	gotypes "go/types"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"testing"

	"an/effects"
	"an/syntax"
	"an/types"
)

// The Go oracle (spec/24-roadmap.md, rule 3; conf.oracle.use): Go source
// files of the Go distribution are renamed into Anlang (int8 → i8 and so
// on) and checked by the Anlang checker; go/types checks the original.
// For the common subset of the two languages, both must accept or both
// must reject a program.

// goRenames maps Go predeclared identifiers to their Anlang names
// (type.numeric.removed).
var goRenames = map[string]string{
	"int8": "i8", "int16": "i16", "int32": "i32", "int64": "i64",
	"uint8": "u8", "uint16": "u16", "uint32": "u32", "uint64": "u64",
	"uintptr": "usize", "float32": "f32", "float64": "f64",
	"byte": "u8", "rune": "i32",
}

// anlangOnlyNames are predeclared in Anlang but not in Go: a Go file that
// uses them as identifiers cannot be renamed.
var anlangOnlyNames = map[string]bool{
	"i8": true, "i16": true, "i32": true, "i64": true, "u8": true, "u16": true, "u32": true, "u64": true,
	"usize": true, "f32": true, "f64": true, "cstring": true, "dstring": true,
	"u16le": true, "u16be": true, "u32le": true, "u32be": true, "u64le": true, "u64be": true,
	"i16le": true, "i16be": true, "i32le": true, "i32be": true, "i64le": true, "i64be": true,
	"Option": true, "None": true, "Some": true,
}

// deviations are the rules by which Anlang deliberately rejects Go code.
// Diagnostics with these rules are ignored when comparing with Go.
var deviations = map[string]bool{
	"type.pointer.removed":           true,
	"expr.removed.address":           true,
	"expr.removed.deref":             true,
	"type.numeric.removed":           true,
	"type.universe.removed":          true,
	"decl.var.package-kinds":         true,
	"decl.func.no-init":              true,
	"type.any.positions":             true,
	"type.interface.empty-value":     true,
	"type.conv.int-narrow":           true,
	"stmt.error.unused":              true,
	"expr.cmp.sync":                  true,
	"decl.package.main":              true,
	"lex.literal.no-imaginary":       true,
	"decl.import.paths":              true,
	"decl.import.no-blank-dot":       true,
	"decl.func.body":                 true,
	"expr.addr.def":                  true,
	"expr.closure.captured-readonly": true,
	"mem.nil.call":                   true,
	"mem.nil.func-param":             true,
	"mem.inout.builtin":              true,
	"ct.reject.precondition":         true,
	"ct.any.use":                     true,
	"ct.any.no-slice":                true,
	"expr.addr.map":                  true,
}

// knownMismatches are the files on which the checker disagrees with Go,
// with the reason. The test fails on any other mismatch, and on an entry
// here that no longer mismatches.
var knownMismatches = map[string]string{
	"test/fixedbugs/bug233.go": "fmt.Print as a function value: an/fmt is built into the compiler until steps 9–10",
	"test/fixedbugs/bug051.go": "ct.const.call: a constant may be computed by a function call",
}

// goImporter provides package fmt to go/types with the signatures of
// the functions an/fmt has in this compiler, and nothing else.
type goImporter struct{ fmt *gotypes.Package }

func newGoImporter() *goImporter {
	pkg := gotypes.NewPackage("fmt", "fmt")
	any := gotypes.NewSlice(gotypes.Universe.Lookup("any").Type())
	str := gotypes.NewTuple(gotypes.NewParam(token.NoPos, pkg, "", gotypes.Typ[gotypes.String]))
	for _, name := range []string{"Print", "Println", "Sprint", "Sprintln", "Printf", "Sprintf"} {
		params := gotypes.NewTuple(gotypes.NewParam(token.NoPos, pkg, "a", any))
		if strings.HasSuffix(name, "f") {
			params = gotypes.NewTuple(gotypes.NewParam(token.NoPos, pkg, "format", gotypes.Typ[gotypes.String]), gotypes.NewParam(token.NoPos, pkg, "a", any))
		}
		var results *gotypes.Tuple
		if name[0] == 'S' {
			results = str
		}
		sig := gotypes.NewSignatureType(nil, nil, nil, params, results, true)
		pkg.Scope().Insert(gotypes.NewFunc(token.NoPos, pkg, name, sig))
	}
	pkg.MarkComplete()
	return &goImporter{pkg}
}

func (im *goImporter) Import(path string) (*gotypes.Package, error) {
	if path == "fmt" {
		return im.fmt, nil
	}
	return nil, fmt.Errorf("package %s not available", path)
}

type oracleResult struct {
	file     string
	goErrs   []string
	anErrs   []string
	missed   []string // Go errors on lines where the Anlang checker reports nothing
	skip     string   // reason for skipping the file; "" if compared
	mismatch string   // "go-rejects", "an-rejects" or "crash"; "" if agreed
}

// renameGo renames the Go predeclared identifiers of src into Anlang
// names and the import path "fmt" into "an/fmt". It reports false if src
// cannot be renamed unambiguously.
func renameGo(src []byte) ([]byte, bool) {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s goscanner.Scanner
	s.Init(file, src, nil, goscanner.ScanComments)
	var out strings.Builder
	last := 0
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		off := file.Offset(pos)
		switch tok {
		case token.IDENT:
			if anlangOnlyNames[lit] || lit == "enum" || lit == "inout" || lit == "profile" {
				return nil, false
			}
			if r, ok := goRenames[lit]; ok {
				out.Write(src[last:off])
				out.WriteString(r)
				last = off + len(lit)
			}
		case token.STRING:
			if lit == `"fmt"` {
				out.Write(src[last:off])
				out.WriteString(`"an/fmt"`)
				last = off + len(lit)
			}
		}
	}
	out.Write(src[last:])
	return []byte(out.String()), true
}

func oracleCheck(path string, im *goImporter) oracleResult {
	r := oracleResult{file: path}
	src, err := os.ReadFile(path)
	if err != nil {
		r.skip = "unreadable"
		return r
	}

	// Go.
	type goLine struct {
		line int
		err  string
	}
	var goLines []goLine
	fset := token.NewFileSet()
	gf, err := goparser.ParseFile(fset, path, src, goparser.SkipObjectResolution)
	if err != nil {
		r.skip = "go parse error"
		return r
	}
	for _, imp := range gf.Imports {
		if imp.Path.Value != `"fmt"` {
			r.skip = "imports " + imp.Path.Value
			return r
		}
	}
	if gf.Name.Name == "main" {
		hasMain := false
		for _, d := range gf.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "main" {
				hasMain = true
			}
		}
		if !hasMain {
			r.skip = "package main without func main"
			return r
		}
	}
	conf := gotypes.Config{
		Importer:  im,
		GoVersion: "go1.23",
		Error: func(err error) {
			r.goErrs = append(r.goErrs, err.Error())
			if te, ok := err.(gotypes.Error); ok {
				goLines = append(goLines, goLine{fset.Position(te.Pos).Line, err.Error()})
			}
		},
	}
	conf.Check(gf.Name.Name, fset, []*ast.File{gf}, nil)

	// Anlang.
	asrc, ok := renameGo(src)
	if !ok {
		r.skip = "uses Anlang names"
		return r
	}
	af, perrs := syntax.Parse(filepath.Base(path), asrc)
	if len(perrs) > 0 {
		r.skip = "anlang parse error"
		return r
	}
	var errs []*syntax.Error
	func() {
		defer func() {
			if e := recover(); e != nil {
				r.mismatch = "crash"
				r.anErrs = []string{fmt.Sprint(e)}
				if os.Getenv("ANLANG_ORACLE") == "v" {
					r.anErrs = append(r.anErrs, string(debug.Stack()))
				}
			}
		}()
		var info *types.Info
		info, errs = types.Check(&types.Config{Module: "conf"}, []*syntax.File{af})
		if len(errs) == 0 && info != nil {
			// Effect inference must not fail on any accepted program.
			effects.NewProgram(effects.AnalyzeAll([]effects.Input{{Info: info}}, nil))
		}
	}()
	if r.mismatch == "crash" {
		return r
	}
	anLines := map[int]bool{}
	for _, e := range errs {
		anLines[e.Pos.Line] = true
	}
	for _, g := range goLines {
		if !anLines[g.line] {
			r.missed = append(r.missed, g.err)
		}
	}
	deviated := false
	for _, e := range errs {
		if e.Rule == "" && strings.Contains(e.Msg, "not supported yet") {
			if os.Getenv("ANLANG_ORACLE") == "unsupported" {
				fmt.Fprintln(os.Stderr, "UNSUPPORTED", e.Msg)
			}
			r.skip = "not supported yet"
			return r
		}
		if deviations[e.Rule] {
			deviated = true
			continue
		}
		r.anErrs = append(r.anErrs, e.Error())
	}
	switch {
	case len(r.goErrs) == 0 && len(r.anErrs) > 0:
		r.mismatch = "an-rejects"
	case len(r.goErrs) > 0 && len(r.anErrs) == 0 && !deviated:
		r.mismatch = "go-rejects"
	}
	return r
}

// TestGoOracle compares the Anlang checker with go/types on the Go
// distribution's test programs. Set ANLANG_ORACLE=v to print every
// mismatch, ANLANG_ORACLE=lines to print Go errors that the Anlang
// checker does not report on the same line, ANLANG_ORACLE=unsupported to
// print the unsupported constructs that make files skipped, and
// ANLANG_ORACLE_FILE to check one file.
func TestGoOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	root := runtime.GOROOT()
	dirs := []string{filepath.Join(root, "test"), filepath.Join(root, "src", "internal", "types", "testdata")}
	var files []string
	for _, dir := range dirs {
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && strings.HasSuffix(d.Name(), ".dir") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return nil
		})
	}
	if len(files) == 0 {
		t.Skip("no Go sources in GOROOT")
	}
	sort.Strings(files)
	if f := os.Getenv("ANLANG_ORACLE_FILE"); f != "" {
		files = []string{f}
	}

	im := newGoImporter()
	skipped := map[string]int{}
	compared, agreed := 0, 0
	var mismatches []oracleResult
	var missed []string
	goErrors := 0
	for _, f := range files {
		r := oracleCheck(f, im)
		if r.skip != "" {
			reason := r.skip
			if strings.HasPrefix(reason, "imports ") {
				reason = "imports other packages"
			}
			skipped[reason]++
			continue
		}
		compared++
		goErrors += len(r.goErrs)
		missed = append(missed, r.missed...)
		if r.mismatch == "" {
			agreed++
			continue
		}
		mismatches = append(mismatches, r)
	}
	t.Logf("%d files: %d compared, %d agree, %d mismatch; skipped: %v", len(files), compared, agreed, len(mismatches), skipped)
	// Errors on other lines are not compared: they are reported to help
	// find missing checks.
	t.Logf("%d of %d Go errors are on lines without an Anlang diagnostic", len(missed), goErrors)
	if os.Getenv("ANLANG_ORACLE") == "lines" {
		for _, m := range missed {
			t.Log(m)
		}
	}
	verbose := os.Getenv("ANLANG_ORACLE") == "v"
	seen := map[string]bool{}
	for _, r := range mismatches {
		rel, _ := filepath.Rel(root, r.file)
		rel = filepath.ToSlash(rel)
		if verbose {
			t.Logf("%s %s\n  go: %s\n  an: %s", r.mismatch, rel, strings.Join(first(r.goErrs, 3), "\n      "), strings.Join(first(r.anErrs, 3), "\n      "))
		}
		if _, ok := knownMismatches[rel]; ok {
			seen[rel] = true
			continue
		}
		t.Errorf("%s: %s", r.mismatch, rel)
	}
	if os.Getenv("ANLANG_ORACLE_FILE") == "" {
		for f := range knownMismatches {
			if !seen[f] {
				t.Errorf("%s is listed in knownMismatches but agrees with Go now", f)
			}
		}
	}
}

func first(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
