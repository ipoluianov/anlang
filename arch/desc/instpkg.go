package desc

import (
	"fmt"
	"strings"

	"an/format"
)

// InstructionPackage returns the Anlang source of the instruction
// package an/arch/<name> of the architecture (target.arch.instr-package):
// a type Regs with the registers, and a function for each instruction
// form whose operands are immediates, constants and fixed registers and
// which does not change the program counter directly. A function takes
// the immediates and the registers, and returns the registers after the
// instruction; the compiler replaces a call by the instruction, and the
// function has the effects of the instruction (target.arch.instr-effects).
func (a *Arch) InstructionPackage() []byte {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("// Package %s exposes the instructions and registers of the %s", a.Name, a.Name)
	w("// architecture (spec/13-targets.md §13.5.2). It is generated from the")
	w("// architecture description arch.%s; the compiler replaces a call of a", strings.ToUpper(a.Name[:1])+a.Name[1:])
	w("// function by its instruction.")
	w("package %s", a.Name)
	w("")
	w("profile core")
	w("")
	if a.Pointers.Far == 32 {
		w(`import (`)
		w(`	"an/effect"`)
		w(`	"an/unsafe"`)
		w(`)`)
	} else {
		w(`import "an/effect"`)
	}
	w("")
	w("// Regs are the registers that instructions read and write.")
	w("type Regs struct {")
	for _, r := range a.PackageRegisters() {
		w("\t%s u%d", r.Name, roundWidth(r.Width))
	}
	w("}")
	if a.Pointers.Far == 32 {
		// A far address: the offset in the low 16 bits, the segment in
		// the high 16 bits, as far pointers are stored in memory.
		w("")
		w("// Far returns the far address of offset off in segment seg.")
		w("func Far(seg, off u16) unsafe.Far {")
		w("\treturn unsafe.Far(u32(seg)<<16 | u32(off))")
		w("}")
	}
	forms, names := a.packageFunctions()
	for k, in := range forms {
		name := names[k]
		var params []string
		for i, o := range in.ops {
			if o.Imm != 0 {
				params = append(params, fmt.Sprintf("%s u%d", opName(i), roundWidth(o.Imm)))
			}
		}
		params = append(params, "r Regs")
		w("")
		w("// %s executes %s (%s).", name, in, in.Encoding)
		w("func %s(%s) Regs {", name, strings.Join(params, ", "))
		for _, e := range in.Effects {
			w("\teffect.Mark(effect.%s)", e)
		}
		for i, o := range in.ops {
			if o.Imm != 0 {
				w("\t_ = %s", opName(i))
			}
		}
		w("\treturn r")
		w("}")
	}
	src, errs := format.Source(a.Name+".an", []byte(b.String()), 0)
	if len(errs) > 0 {
		panic(fmt.Sprintf("desc: the instruction package of %s: %v", a.Name, errs))
	}
	return src
}

// packageFunctions returns the instruction forms of the instruction
// package and the names of their functions.
func (a *Arch) packageFunctions() ([]*Instruction, []string) {
	forms := a.packageForms()
	count := map[string]int{}
	for _, in := range forms {
		count[in.Mnemonic]++
	}
	var names []string
	for _, in := range forms {
		name := title(in.Mnemonic)
		if count[in.Mnemonic] > 1 {
			for _, o := range in.ops {
				name += ident(o.Name)
			}
		}
		names = append(names, name)
	}
	return forms, names
}

// PackageInstruction returns the assembler text of the instruction of
// function name of the instruction package, given the values of its
// immediate operands in order; false if there is no such function.
func (a *Arch) PackageInstruction(name string, imms []uint64) (string, bool) {
	forms, names := a.packageFunctions()
	for k, in := range forms {
		if names[k] != name {
			continue
		}
		var ops []string
		j := 0
		for _, o := range in.ops {
			switch {
			case o.Register != "":
				ops = append(ops, o.Register)
			case o.Constant:
				ops = append(ops, fmt.Sprint(o.Value))
			default:
				if j < len(imms) {
					ops = append(ops, fmt.Sprint(imms[j]))
				}
				j++
			}
		}
		text := in.Mnemonic
		if len(ops) > 0 {
			text += " " + strings.Join(ops, ", ")
		}
		return text, true
	}
	return "", false
}

// PackageRegisters returns the registers of type Regs of the instruction
// package, in the order of its fields.
func (a *Arch) PackageRegisters() []*Register {
	var regs []*Register
	for i := range a.Registers {
		r := &a.Registers[i]
		if r.Parent != "" || r.Class == "prefix" || r == a.pc {
			continue
		}
		regs = append(regs, r)
	}
	return regs
}

// packageForms returns the instruction forms that the instruction
// package exposes.
func (a *Arch) packageForms() []*Instruction {
	var forms []*Instruction
	for i := range a.Instructions {
		in := &a.Instructions[i]
		ok := true
		for _, o := range in.ops {
			if o.Class != "" || o.Memory != 0 || o.Relative || o.Imm > 16 {
				ok = false
			}
		}
		if ok && !assignsReg(in.body, a.PC) {
			forms = append(forms, in)
		}
	}
	// A form with a constant operand, such as INT 3, is left out when a
	// form of the same mnemonic and arity takes an immediate instead.
	var out []*Instruction
	for _, in := range forms {
		redundant := false
		for _, o := range in.ops {
			if !o.Constant {
				continue
			}
			for _, other := range forms {
				if other != in && other.Mnemonic == in.Mnemonic && len(other.ops) == len(in.ops) {
					redundant = true
				}
			}
		}
		if !redundant {
			out = append(out, in)
		}
	}
	return out
}

// assignsReg reports whether statements assign register name directly.
func assignsReg(list []Stmt, name string) bool {
	for _, s := range list {
		switch s := s.(type) {
		case *AssignStmt:
			if id, ok := s.LHS.(*Ident); ok && id.Name == name {
				return true
			}
		case *IfStmt:
			if assignsReg(s.Then, name) || assignsReg(s.Else, name) {
				return true
			}
		case *WhileStmt:
			if assignsReg(s.Body, name) {
				return true
			}
		}
	}
	return false
}

func roundWidth(w int) int {
	for _, n := range []int{8, 16, 32, 64} {
		if w <= n {
			return n
		}
	}
	return 64
}

func title(s string) string {
	s = strings.ToLower(s)
	return strings.ToUpper(s[:1]) + s[1:]
}

// ident turns an operand kind into a part of an identifier.
func ident(kind string) string {
	var b strings.Builder
	upper := true
	for _, c := range kind {
		switch {
		case c >= 'a' && c <= 'z':
			if upper {
				c -= 'a' - 'A'
			}
			b.WriteRune(c)
			upper = false
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteRune(c)
			upper = false
		default:
			upper = true
		}
	}
	return b.String()
}

// Register returns the register or register part named name, or nil.
func (a *Arch) Register(name string) *Register {
	return a.regs[strings.ToUpper(name)]
}
