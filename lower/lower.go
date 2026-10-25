// Package lower translates a checked program to IR (spec/22-compiler.md).
//
// Temporary: until the shared SSA representation of roadmap step 6, it
// translates only what the first IR can express: functions without
// parameters or results whose bodies call each other and fmt.Print /
// fmt.Println with constant arguments. Everything else the checker
// accepts is reported as not supported yet by the code generator.
package lower

import (
	"fmt"
	"go/constant"
	"math"
	"strconv"
	"strings"

	"an/ir"
	"an/syntax"
	"an/types"
)

const unsupported = "not supported yet by the code generator (spec/24-roadmap.md, step 6)"

type lowerer struct {
	info   *types.Info
	prog   *ir.Program
	funcs  map[*types.Func]*ir.Func
	errors []*syntax.Error
}

// Program translates the checked package main to IR.
func Program(info *types.Info) (*ir.Program, []*syntax.Error) {
	l := &lowerer{info: info, prog: &ir.Program{}, funcs: map[*types.Func]*ir.Func{}}
	for _, f := range info.Funcs {
		if f.Name() == "_" {
			continue
		}
		fn := &ir.Func{Name: "main." + f.Name()}
		l.funcs[f] = fn
		l.prog.Funcs = append(l.prog.Funcs, fn)
	}
	l.prog.Main = l.funcs[info.Main]
	for _, f := range info.Funcs {
		if fn := l.funcs[f]; fn != nil {
			l.function(f, fn)
		}
	}
	if len(l.errors) > 0 {
		return nil, l.errors
	}
	return l.prog, nil
}

func (l *lowerer) errorf(pos syntax.Pos, format string, args ...any) {
	l.errors = append(l.errors, &syntax.Error{Pos: pos, Msg: fmt.Sprintf(format, args...) + ": " + unsupported})
}

func (l *lowerer) function(f *types.Func, fn *ir.Func) {
	sig := f.Signature()
	if sig.Params.Len() > 0 || sig.Results.Len() > 0 {
		l.errorf(f.Pos(), "functions with parameters or results")
		return
	}
	// Only the first construct that cannot be lowered is reported in
	// each function.
	nerrors := len(l.errors)
	for _, s := range f.Decl.Body.List {
		if len(l.errors) > nerrors {
			break
		}
		switch s := s.(type) {
		case *syntax.EmptyStmt:
		case *syntax.ExprStmt:
			call, ok := unparen(s.X).(*syntax.CallExpr)
			if !ok {
				l.errorf(s.Pos(), "this statement")
				continue
			}
			l.call(call, fn)
		default:
			l.errorf(s.Pos(), "this statement")
		}
	}
	fn.Body = append(fn.Body, &ir.Return{})
}

func (l *lowerer) call(call *syntax.CallExpr, fn *ir.Func) {
	var id *syntax.Name
	switch f := unparen(call.Fun).(type) {
	case *syntax.Name:
		id = f
	case *syntax.SelectorExpr:
		id = f.Sel
	}
	switch obj := l.info.Uses[id].(type) {
	case *types.Func:
		if callee := l.funcs[obj]; callee != nil && len(call.Args) == 0 {
			fn.Body = append(fn.Body, &ir.Call{Func: callee})
			return
		}
	case *types.Builtin:
		if obj.Pkg() == "an/fmt" {
			l.print(call, obj.Name() == "Println", fn)
			return
		}
	}
	l.errorf(call.Pos(), "this call")
}

// print lowers fmt.Print and fmt.Println with constant arguments to a
// single write of the formatted text.
func (l *lowerer) print(call *syntax.CallExpr, ln bool, fn *ir.Func) {
	var parts []string
	var isString []bool
	for _, arg := range call.Args {
		tv := l.info.Types[arg]
		if tv.Value == nil {
			l.errorf(arg.Pos(), "fmt.Print and fmt.Println with non-constant arguments")
			return
		}
		parts = append(parts, formatConst(tv.Value, tv.Type))
		isString = append(isString, tv.Value.Kind() == constant.String)
	}
	// Go semantics: Println separates operands with spaces and appends a
	// newline; Print adds spaces between operands when neither is a string.
	var b strings.Builder
	for i, p := range parts {
		if i > 0 && (ln || !isString[i-1] && !isString[i]) {
			b.WriteString(" ")
		}
		b.WriteString(p)
	}
	if ln {
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return
	}
	d := &ir.Data{Name: fmt.Sprintf("str.%d", len(l.prog.Data)), Bytes: []byte(b.String())}
	l.prog.Data = append(l.prog.Data, d)
	fn.Body = append(fn.Body, &ir.PrintStdout{Data: d})
}

// formatConst formats a constant of type t as fmt's %v verb does.
func formatConst(v constant.Value, t types.Type) string {
	switch v.Kind() {
	case constant.String:
		return constant.StringVal(v)
	case constant.Bool:
		return strconv.FormatBool(constant.BoolVal(v))
	case constant.Int:
		if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&types.IsFloat != 0 {
			return formatFloat(v, b)
		}
		return v.ExactString()
	case constant.Float:
		b, _ := t.Underlying().(*types.Basic)
		return formatFloat(v, b)
	}
	return v.String()
}

// formatFloat formats like %v: the shortest representation, with an
// exponent if it is less than -4 or at least 21.
func formatFloat(v constant.Value, b *types.Basic) string {
	bits := 64
	if b != nil && b.Kind() == types.F32 {
		bits = 32
	}
	f, _ := constant.Float64Val(constant.ToFloat(v))
	if bits == 32 {
		f = float64(float32(f))
	}
	if f == 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return strconv.FormatFloat(f, 'g', -1, bits)
	}
	e := strconv.FormatFloat(f, 'e', -1, bits)
	exp, _ := strconv.Atoi(e[strings.LastIndexByte(e, 'e')+1:])
	if exp < -4 || exp >= 21 {
		return e
	}
	return strconv.FormatFloat(f, 'f', -1, bits)
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
