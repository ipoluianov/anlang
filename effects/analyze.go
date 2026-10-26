package effects

import (
	"go/constant"
	"strings"

	"an/syntax"
	"an/types"
)

// Analyze computes the effect summaries of the functions of the package
// described by info. imported returns the summary of a function of an
// imported package by its key, or nil.
func Analyze(info *types.Info, imported func(key string) *Summary) *Package {
	a := &analyzer{
		info:     info,
		imported: imported,
		byKey:    map[string]*fnFacts{},
		litKeys:  map[*syntax.FuncLit]string{},
		pkg: &Package{
			Path:    info.Pkg.Path,
			Funcs:   map[string]*Summary{},
			Impls:   map[Ref]map[string]bool{},
			Flows:   map[Ref]map[Ref]bool{},
			Without: map[string][]Effect{},
		},
	}
	for _, f := range append(append([]*types.Func(nil), info.Funcs...), info.Methods...) {
		if f.Decl == nil || f.Decl.Body == nil || info.Templates[f] {
			continue // a template is analyzed per instance (ct.any.effects)
		}
		fn := a.newFn(funcKey(f), f.Signature(), nil)
		fn.sum.Pos = f.Decl.Name.Pos()
		a.without(fn, f.Decl.Body)
		if f.Handler != "" {
			// An interrupt handler neither allocates, nor blocks, nor
			// panics (emb.irq.effects).
			fn.sum.Handler = true
			fn.sum.Without = append(fn.sum.Without, Alloc, Block, Panic)
			a.pkg.Without[fn.key] = append(a.pkg.Without[fn.key], Alloc, Block, Panic)
		}
		a.walkBody(fn, f.Decl.Body)
	}
	for _, inst := range info.TemplateInstances {
		if inst.Decl.Body == nil || inst.Sig == nil {
			continue
		}
		fn := a.newFn(instKey(info, inst), inst.Sig, nil)
		fn.sum.Pos = inst.Decl.Name.Pos()
		a.walkBody(fn, inst.Decl.Body)
	}
	a.solve()
	a.callSites()
	for _, fn := range a.fns {
		a.pkg.Funcs[fn.key] = fn.sum
	}
	return a.pkg
}

type analyzer struct {
	info     *types.Info
	imported func(string) *Summary
	pkg      *Package
	fns      []*fnFacts
	byKey    map[string]*fnFacts
	litKeys  map[*syntax.FuncLit]string
}

// fnFacts are the facts of one function body from which its summary is
// computed (eff.infer.rules).
type fnFacts struct {
	key     string
	sig     *types.Signature
	sum     *Summary
	params  map[*types.Var]int
	tparams map[*types.TypeParam]int
	outer   *fnFacts // the enclosing function of a function literal
	nlit    int

	sources  []effectFact
	deferred []effectFact
	prefs    []prefFact // references to parameters of this or an enclosing function
	open     []refFact
	calls    []*callFact
	adds     []addFact // functions whose summaries are added (rules 6, 7)
	lits     []*fnFacts

	lifted []prefFact   // references of this literal to parameters of enclosing functions
	liftQ  map[Ref]bool // the open references standing for lifted ones in the literal's summary
}

type effectFact struct {
	e      Effect
	origin Origin
}

type refFact struct {
	r      Ref
	origin Origin
}

// prefFact is a reference to parameter v or type parameter tp of a
// function that may enclose the current one.
type prefFact struct {
	kind   RefKind
	v      *types.Var
	tp     *types.TypeParam
	method string
	origin Origin
}

type addFact struct {
	key    string
	origin Origin
}

type argKind int

const (
	argNone argKind = iota
	argParam
	argParamMethod
	argFuncs
	argType
	argTypeParam
	argOpenFunc
	argOpenIface
)

// argInfo describes an argument of a static call, for the substitution of
// the callee's parametric references (eff.infer.rules 2).
type argInfo struct {
	kind   argKind
	v      *types.Var
	tp     *types.TypeParam
	method string
	keys   []string
	lit    *syntax.FuncLit // argFuncs: a function literal
	typ    types.Type
	key    string // signature or interface key
}

type callFact struct {
	pos    syntax.Pos
	callee string
	lit    *syntax.FuncLit // the callee is this function literal
	args   map[int]argInfo // by parameter index; -1 is the receiver
	targs  []argInfo       // type arguments: argType, argTypeParam or argOpenIface
	origin Origin
}

func (a *analyzer) newFn(key string, sig *types.Signature, outer *fnFacts) *fnFacts {
	fn := &fnFacts{key: key, sig: sig, sum: newSummary(key), params: map[*types.Var]int{}, tparams: map[*types.TypeParam]int{}, outer: outer}
	if sig.Recv != nil {
		fn.params[sig.Recv] = -1
	}
	for i := range sig.Params.Len() {
		p := sig.Params.At(i)
		fn.params[p] = i
		fn.sum.Params = append(fn.sum.Params, p.Name())
	}
	tps := sig.TypeParams
	if len(tps) == 0 {
		tps = sig.RecvTypeParams
	}
	for i, tp := range tps {
		fn.tparams[tp] = i
		fn.sum.TypeParams = append(fn.sum.TypeParams, tp.Obj.Name())
	}
	a.fns = append(a.fns, fn)
	a.byKey[key] = fn
	return fn
}

// without records the clauses ensures effect.Without of a function.
func (a *analyzer) without(fn *fnFacts, body *syntax.BlockStmt) {
	for _, s := range body.List {
		c, ok := s.(*syntax.ContractStmt)
		if !ok {
			break
		}
		call, ok := unparen(c.X).(*syntax.CallExpr)
		if !ok || c.Word != "ensures" || a.builtinName(call.Fun) != "effect.Without" {
			continue
		}
		for _, arg := range call.Args {
			if e, ok := a.effectConst(arg); ok {
				a.pkg.Without[fn.key] = append(a.pkg.Without[fn.key], e)
				fn.sum.Without = append(fn.sum.Without, e)
			}
		}
	}
}

func (a *analyzer) effectConst(e syntax.Expr) (Effect, bool) {
	v := a.info.Types[e].Value
	if v == nil || v.Kind() != constant.String {
		return "", false
	}
	return Effect(constant.StringVal(v)), true
}

// instKey returns the key of an instance of a template.
func instKey(info *types.Info, inst *types.TemplateInstance) string {
	return info.Pkg.Path + "." + inst.Key
}

// funcKey returns the key of a declared function or method: the package
// path and the name, with the receiver base type name for a method.
// FuncKey returns the key of the summary of function f.
func FuncKey(f *types.Func) string { return funcKey(f) }

func funcKey(f *types.Func) string {
	path := "?"
	if f.Pkg() != nil {
		path = f.Pkg().Path
	}
	sig := f.Signature()
	if sig.Recv != nil {
		t := sig.Recv.Type()
		if n, ok := t.(*types.Named); ok {
			return path + "." + n.Origin().Obj.Name() + "." + f.Name()
		}
		return path + "." + t.String() + "." + f.Name()
	}
	return path + "." + f.Name()
}

// typeKey identifies a type in open references.
func typeKey(t types.Type) string {
	if n, ok := t.(*types.Named); ok && n.Obj.Pkg() != nil {
		return n.Obj.Pkg().Path + "." + n.String()
	}
	return t.String()
}

// sigKey identifies a function type in open references: its parameter
// and result types, without names.
func sigKey(t types.Type) string {
	sig, ok := coreOf(t).(*types.Signature)
	if !ok {
		return t.String()
	}
	var b strings.Builder
	b.WriteString("func(")
	for i := range sig.Params.Len() {
		if i > 0 {
			b.WriteString(", ")
		}
		p := sig.Params.At(i)
		if p.Inout() {
			b.WriteString("inout ")
		}
		pt := p.Type()
		if sig.Variadic && i == sig.Params.Len()-1 {
			b.WriteString("...")
			pt = pt.Underlying().(*types.Slice).Elem
		}
		b.WriteString(typeKey(pt))
	}
	b.WriteString(")")
	if n := sig.Results.Len(); n > 0 {
		b.WriteString(" (")
		for i := range n {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(typeKey(sig.Results.At(i).Type()))
		}
		b.WriteString(")")
	}
	return b.String()
}

// methods returns the method names of interface type t.
func methods(t types.Type) []string {
	iface, ok := t.Underlying().(*types.Interface)
	if !ok {
		return nil
	}
	var names []string
	for _, m := range iface.MethodSet() {
		names = append(names, m.Name())
	}
	return names
}

// methodKey returns the key of method name of concrete type t, or "".
func (a *analyzer) methodKey(t types.Type, name string) string {
	obj, _ := a.info.LookupFieldOrMethod(t, name)
	if f, ok := obj.(*types.Func); ok && f.Signature().Recv != nil {
		return funcKey(f)
	}
	return ""
}

func unparen(e syntax.Expr) syntax.Expr {
	for {
		p, ok := e.(*syntax.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

func (a *analyzer) typeOf(e syntax.Expr) types.Type {
	if tv, ok := a.info.Types[e]; ok && tv.Type != nil {
		return tv.Type
	}
	if id, ok := e.(*syntax.Name); ok {
		if obj := a.info.Defs[id]; obj != nil {
			return obj.Type()
		}
		if obj := a.info.Uses[id]; obj != nil {
			return obj.Type()
		}
	}
	return nil
}

func (a *analyzer) isConst(e syntax.Expr) bool { return a.info.Types[e].Value != nil }

// builtinName returns the name of the built-in function that e denotes,
// qualified by the package for the functions of the standard library
// built into the compiler ("len", "fmt.Println"), or "".
func (a *analyzer) builtinName(e syntax.Expr) string {
	var id *syntax.Name
	switch e := unparen(e).(type) {
	case *syntax.Name:
		id = e
	case *syntax.SelectorExpr:
		id = e.Sel
	default:
		return ""
	}
	b, ok := a.info.Uses[id].(*types.Builtin)
	if !ok {
		return ""
	}
	if b.Pkg() != "" {
		return strings.TrimPrefix(b.Pkg(), "an/") + "." + b.Name()
	}
	return b.Name()
}

// param returns the function among fn and its enclosing functions whose
// parameter v is.
func (fn *fnFacts) paramOwner(v *types.Var) *fnFacts {
	for f := fn; f != nil; f = f.outer {
		if _, ok := f.params[v]; ok {
			return f
		}
	}
	return nil
}

func (fn *fnFacts) tparamOwner(tp *types.TypeParam) *fnFacts {
	for f := fn; f != nil; f = f.outer {
		if _, ok := f.tparams[tp]; ok {
			return f
		}
	}
	return nil
}

func origin(pos syntax.Pos, what string) Origin { return Origin{Pos: pos.String(), What: what} }

func (fn *fnFacts) source(e Effect, pos syntax.Pos, what string) {
	fn.sources = append(fn.sources, effectFact{e, origin(pos, what)})
}

// coreOf returns the core type of t, or nil if t is nil.
func coreOf(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	return types.CoreType(t)
}
