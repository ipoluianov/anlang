package ir

import (
	"fmt"
	"strings"

	"an/types"
)

// The textual IR format (spec/22-compiler.md §22.10.2). A function is
//
//	func NAME(PARAM TYPE, ...) RESULTS
//	bN:                          // comment; preds: bM, ...
//		tK: TYPE = OPERATION OPERANDS
//		OPERATION OPERANDS
//
// Values are named tK; constants are written VALUE:TYPE, the zero value
// zero:TYPE; parameters, free variables and functions by their names.

// String returns the textual form of the program.
func (p *Program) String() string {
	var b strings.Builder
	for i, f := range p.Funcs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(f.String())
	}
	return b.String()
}

// String returns the textual form of the function.
func (f *Func) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "func %s(", f.Name)
	for i, p := range f.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		t := p.Typ.String()
		if p.Inout {
			t = "inout " + p.Typ.(*Pointer).Elem.String()
		}
		fmt.Fprintf(&b, "%s %s", p.Pname, t)
	}
	b.WriteString(")")
	if r := f.Sig.Results; r.Len() == 1 {
		b.WriteString(" " + r.At(0).Type().String())
	} else if r.Len() > 1 {
		b.WriteString(" " + r.String())
	}
	if len(f.FreeVars) > 0 {
		b.WriteString(" free(")
		for i, v := range f.FreeVars {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s %s", v.Vname, v.Typ)
		}
		b.WriteString(")")
	}
	b.WriteString("\n")
	for _, bl := range f.Blocks {
		fmt.Fprintf(&b, "b%d:", bl.Index)
		var notes []string
		if bl.Comment != "" {
			notes = append(notes, bl.Comment)
		}
		if len(bl.Preds) > 0 {
			var preds []string
			for _, p := range bl.Preds {
				preds = append(preds, fmt.Sprintf("b%d", p.Index))
			}
			notes = append(notes, "preds: "+strings.Join(preds, ", "))
		}
		if len(notes) > 0 {
			b.WriteString(" // " + strings.Join(notes, "; "))
		}
		b.WriteString("\n")
		for _, in := range bl.Instrs {
			b.WriteString("\t" + InstrString(in) + "\n")
		}
	}
	return b.String()
}

func name(v Value) string {
	switch v := v.(type) {
	case nil:
		return "_"
	case *Const:
		return v.Name() + ":" + v.Typ.String()
	}
	return v.Name()
}

func names(vs []Value) string {
	var s []string
	for _, v := range vs {
		s = append(s, name(v))
	}
	return strings.Join(s, ", ")
}

// InstrString returns the textual form of an instruction.
func InstrString(in Instr) string {
	op := operation(in)
	if r, ok := in.(interface{ reg() *register }); ok {
		reg := r.reg()
		return fmt.Sprintf("t%d: %s = %s", reg.id, typeString(reg.typ), op)
	}
	return op
}

func typeString(t types.Type) string {
	if t == nil {
		return "()"
	}
	if tu, ok := t.(*types.Tuple); ok && tu.Len() == 0 {
		return "()"
	}
	return t.String()
}

func operation(in Instr) string {
	switch i := in.(type) {
	case *Alloc:
		return "alloc // " + i.Comment
	case *Load:
		return "load " + name(i.Addr)
	case *Store:
		return "store " + name(i.Addr) + ", " + name(i.Val)
	case *Copy:
		return "copy " + name(i.X)
	case *FieldAddr:
		return fmt.Sprintf("fieldaddr %s, %d", name(i.X), i.Field)
	case *IndexAddr:
		return "indexaddr " + name(i.X) + ", " + name(i.Index)
	case *SubsliceAddr:
		return "subsliceaddr " + name(i.X) + "[" + optName(i.Low) + ":" + optName(i.High) + "]"
	case *Field:
		return fmt.Sprintf("field %s, %d", name(i.X), i.Field)
	case *Index:
		return "index " + name(i.X) + ", " + name(i.Index)
	case *Lookup:
		s := "lookup " + name(i.X) + ", " + name(i.Key)
		if i.CommaOk {
			s += ", ok"
		}
		return s
	case *MapUpdate:
		return "mapupdate " + names([]Value{i.Map, i.Key, i.Value})
	case *Slice:
		return fmt.Sprintf("slice %s[%s:%s:%s]", name(i.X), optName(i.Low), optName(i.High), optName(i.Max))
	case *BinOp:
		return name(i.X) + " " + i.Op.String() + " " + name(i.Y)
	case *UnOp:
		s := i.Op.String() + name(i.X)
		if i.CommaOk {
			s += ", ok"
		}
		return s
	case *Convert:
		return "convert " + name(i.X)
	case *MakeInterface:
		return "makeinterface " + name(i.X)
	case *TypeAssert:
		s := "typeassert " + name(i.X) + ".(" + i.Asserted.String() + ")"
		if i.CommaOk {
			s += ", ok"
		}
		return s
	case *MakeSlice:
		return "makeslice " + name(i.Len) + ", " + name(i.Cap)
	case *MakeMap:
		return "makemap"
	case *MakeStruct:
		return "makestruct {" + names(i.Fields) + "}"
	case *MakeArray:
		return "makearray [" + names(i.Elems) + "]"
	case *MakeVariant:
		return fmt.Sprintf("makevariant #%d {%s}", i.Variant, names(i.Fields))
	case *VariantIndex:
		return "variantindex " + name(i.X)
	case *VariantField:
		return fmt.Sprintf("variantfield %s, #%d.%d", name(i.X), i.Variant, i.Field)
	case *MakeClosure:
		return "closure " + i.Fn.Name + " [" + names(i.Bindings) + "]"
	case *Call:
		if i.Method != "" {
			return fmt.Sprintf("invoke %s.%s(%s)", name(i.Callee), i.Method, names(i.Args))
		}
		return fmt.Sprintf("call %s(%s)", name(i.Callee), names(i.Args))
	case *Extract:
		return fmt.Sprintf("extract %s, %d", name(i.Tuple), i.Index)
	case *Jump:
		return fmt.Sprintf("jump b%d", i.b.Succs[0].Index)
	case *If:
		return fmt.Sprintf("if %s b%d b%d", name(i.Cond), i.b.Succs[0].Index, i.b.Succs[1].Index)
	case *Return:
		if len(i.Results) == 0 {
			return "return"
		}
		return "return " + names(i.Results)
	case *Panic:
		return "panic " + name(i.X)
	case *Defer:
		return "defer " + operation(i.Call)
	case *Asm:
		in := ""
		if i.In != nil {
			in = name(i.In)
		}
		return fmt.Sprintf("asm(%s) %q", in, i.Text)
	}
	return fmt.Sprintf("%T", in)
}

func optName(v Value) string {
	if v == nil {
		return ""
	}
	return name(v)
}
