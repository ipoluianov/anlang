package ir

import (
	"fmt"

	"an/types"
)

// Validate checks the structural invariants of the program: every block
// ends in exactly one control instruction, the successors of a block
// agree with its control instruction, and no type contains a type
// parameter (spec/22-compiler.md §22.10.1).
func (p *Program) Validate() []error {
	var errs []error
	for _, f := range p.Funcs {
		errorf := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf("%s: %s", f.Name, fmt.Sprintf(format, args...)))
		}
		if len(f.Blocks) == 0 {
			errorf("no blocks")
		}
		check := func(what string, t types.Type) {
			if tp := typeParam(t, map[types.Type]bool{}); tp != nil {
				errorf("%s has type %s, which contains type parameter %s", what, t, tp)
			}
		}
		for _, prm := range f.Params {
			check(prm.Pname, prm.Typ)
		}
		for _, v := range f.FreeVars {
			check(v.Vname, v.Typ)
		}
		for _, b := range f.Blocks {
			if !b.Terminated() {
				errorf("b%d does not end in a control instruction", b.Index)
				continue
			}
			for i, in := range b.Instrs {
				if i < len(b.Instrs)-1 && isControl(in) {
					errorf("b%d: control instruction %q before the end of the block", b.Index, InstrString(in))
				}
				if r, ok := in.(interface{ reg() *register }); ok && r.reg().typ != nil {
					check(InstrString(in), r.reg().typ)
				}
				for _, op := range in.Operands() {
					if c, ok := op.(*Const); ok {
						check(InstrString(in), c.Typ)
					}
				}
			}
			want := 0
			switch b.Instrs[len(b.Instrs)-1].(type) {
			case *Jump:
				want = 1
			case *If:
				want = 2
			}
			if len(b.Succs) != want {
				errorf("b%d has %d successors, want %d", b.Index, len(b.Succs), want)
			}
		}
	}
	return errs
}

func isControl(in Instr) bool {
	switch in.(type) {
	case *Jump, *If, *Return, *Panic:
		return true
	}
	return false
}

// typeParam returns a type parameter that t contains, or nil.
func typeParam(t types.Type, seen map[types.Type]bool) types.Type {
	if t == nil || seen[t] {
		return nil
	}
	seen[t] = true
	if types.IsTypeParam(t) {
		return t
	}
	vars := func(vs []*types.Var) types.Type {
		for _, v := range vs {
			if tp := typeParam(v.Type(), seen); tp != nil {
				return tp
			}
		}
		return nil
	}
	switch t := t.(type) {
	case *types.Named:
		if len(t.TypeParamList()) > 0 && len(t.TypeArgs()) == 0 {
			return t.TypeParamList()[0]
		}
		for _, a := range t.TypeArgs() {
			if tp := typeParam(a, seen); tp != nil {
				return tp
			}
		}
	case *Pointer:
		return typeParam(t.Elem, seen)
	case *types.Slice:
		return typeParam(t.Elem, seen)
	case *types.Array:
		return typeParam(t.Elem, seen)
	case *types.Chan:
		return typeParam(t.Elem, seen)
	case *types.Map:
		if tp := typeParam(t.Key, seen); tp != nil {
			return tp
		}
		return typeParam(t.Elem, seen)
	case *types.Struct:
		return vars(t.Fields)
	case *types.Tuple:
		if t != nil {
			return vars(t.Vars)
		}
	case *types.Signature:
		if t.Params != nil {
			if tp := vars(t.Params.Vars); tp != nil {
				return tp
			}
		}
		if t.Results != nil {
			return vars(t.Results.Vars)
		}
	}
	return nil
}
