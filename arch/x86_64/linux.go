package x86_64

import (
	"fmt"
	"go/constant"
	"math"
	"strconv"
	"strings"

	"an/ir"
	"an/types"
)

// Linux system call numbers.
const (
	sysWrite     = 1
	sysExitGroup = 231
)

// Exit status of a program that panics (rt.panic.actions, PrintAndExit).
const panicExitStatus = 2

const panicPrintMessage = "panic: fmt: write to standard output failed\n"

// GenLinux generates the code and read-only data of a static Linux
// program. It returns the section and the offset of the entry point.
//
// Layout: the entry stub, the program's functions, the runtime routines,
// then read-only data. The runtime is written here by hand for now; from
// roadmap step 25 it is Anlang code (spec/19-runtime.md).
func GenLinux(p *ir.Program) (section []byte, entry int, err error) {
	a := NewAsm()

	// Entry: call main, then exit with status 0.
	a.Label("_start")
	a.Call(funcLabel(p.Main))
	a.MovImm32(RDI, 0)
	a.MovImm32(RAX, sysExitGroup)
	a.Syscall()

	var data [][]byte
	for _, f := range p.Funcs {
		if err := genFunc(a, f, &data); err != nil {
			return nil, 0, err
		}
	}

	genPrintStdout(a)

	for i, d := range data {
		a.Label(dataLabel(i))
		a.Bytes(d...)
	}
	a.Label("rt.panicPrintMessage")
	a.Bytes([]byte(panicPrintMessage)...)

	section, err = a.Finish()
	if err != nil {
		return nil, 0, err
	}
	entry, _ = a.Offset("_start")
	return section, entry, nil
}

// genPrintStdout emits rt.printStdout: write RDX bytes at RSI to file
// descriptor 1, retrying short writes and EINTR. On any other failure it
// writes a panic message to file descriptor 2 and exits with status 2.
func genPrintStdout(a *Asm) {
	const eintr = -4

	a.Label("rt.printStdout")
	a.Label("rt.printStdout.loop")
	a.Test64(RDX)
	a.Jcc(CondE, "rt.printStdout.done")
	a.MovImm32(RAX, sysWrite)
	a.MovImm32(RDI, 1)
	a.Syscall()
	a.CmpImm8(RAX, eintr)
	a.Jcc(CondE, "rt.printStdout.loop")
	a.Test64(RAX)
	a.Jcc(CondLE, "rt.printStdout.fail")
	a.Add64(RSI, RAX)
	a.Sub64(RDX, RAX)
	a.Jmp("rt.printStdout.loop")
	a.Label("rt.printStdout.done")
	a.Ret()

	a.Label("rt.printStdout.fail")
	a.LeaRIP(RSI, "rt.panicPrintMessage")
	a.MovImm32(RDX, uint32(len(panicPrintMessage)))
	a.MovImm32(RAX, sysWrite)
	a.MovImm32(RDI, 2)
	a.Syscall()
	a.MovImm32(RDI, panicExitStatus)
	a.MovImm32(RAX, sysExitGroup)
	a.Syscall()
}

func funcLabel(f *ir.Func) string { return "func." + f.Name }
func dataLabel(i int) string      { return fmt.Sprintf("data.%d", i) }

// unsupported reports a construct this backend cannot compile yet.
func unsupported(f *ir.Func, what string) error {
	return fmt.Errorf("%s: %s: not supported yet by the x86-64 code generator (spec/24-roadmap.md, step 26)", f.Pos, what)
}

// genFunc generates a function. Until the backend of roadmap step 26, it
// handles functions whose blocks call functions without arguments and
// fmt.Print or fmt.Println with constant arguments, whose text is
// computed at compile time.
func genFunc(a *Asm, f *ir.Func, data *[][]byte) error {
	if len(f.Params) > 0 || len(f.FreeVars) > 0 || f.Sig.Results.Len() > 0 {
		return unsupported(f, "functions with parameters or results")
	}
	consts := map[ir.Value]*ir.Const{}
	a.Label(funcLabel(f))
	for _, b := range f.Blocks {
		a.Label(fmt.Sprintf("%s.b%d", funcLabel(f), b.Index))
		for _, in := range b.Instrs {
			switch in := in.(type) {
			case *ir.MakeInterface:
				c, ok := in.X.(*ir.Const)
				if !ok {
					return unsupported(f, "interface values")
				}
				consts[in] = c
			case *ir.Call:
				switch callee := in.Callee.(type) {
				case *ir.FuncValue:
					if len(in.Args) > 0 {
						return unsupported(f, "calls with arguments")
					}
					a.Call(funcLabel(callee.Func))
				case *ir.Builtin:
					if callee.Bname != "fmt.Print" && callee.Bname != "fmt.Println" {
						return unsupported(f, callee.Bname)
					}
					text, ok := printText(callee.Bname == "fmt.Println", in.Args, consts)
					if !ok {
						return unsupported(f, callee.Bname+" with arguments that are not constant")
					}
					if text == "" {
						continue
					}
					if len(text) > 1<<31-1 {
						return fmt.Errorf("x86_64: string too large")
					}
					*data = append(*data, []byte(text))
					a.LeaRIP(RSI, dataLabel(len(*data)-1))
					a.MovImm32(RDX, uint32(len(text)))
					a.Call("rt.printStdout")
				default:
					return unsupported(f, "calls of function values")
				}
			case *ir.Jump:
				a.Jmp(fmt.Sprintf("%s.b%d", funcLabel(f), b.Succs[0].Index))
			case *ir.Return:
				a.Ret()
			default:
				return unsupported(f, fmt.Sprintf("instruction %s", ir.InstrString(in)))
			}
		}
	}
	return nil
}

// printText formats constant arguments as fmt.Print and fmt.Println do.
func printText(ln bool, args []ir.Value, consts map[ir.Value]*ir.Const) (string, bool) {
	var b strings.Builder
	prevString := false
	for i, arg := range args {
		c, ok := consts[arg]
		if !ok {
			return "", false
		}
		isString := c.Val != nil && c.Val.Kind() == constant.String
		if i > 0 && (ln || !isString && !prevString) {
			b.WriteString(" ")
		}
		b.WriteString(constText(c))
		prevString = isString
	}
	if ln {
		b.WriteString("\n")
	}
	return b.String(), true
}

// constText formats a constant as the verb %v does.
func constText(c *ir.Const) string {
	switch c.Val.Kind() {
	case constant.String:
		return constant.StringVal(c.Val)
	case constant.Bool:
		return strconv.FormatBool(constant.BoolVal(c.Val))
	case constant.Int:
		if b, ok := c.Typ.Underlying().(*types.Basic); ok && b.Info()&types.IsFloat != 0 {
			return formatFloat(c)
		}
		return c.Val.ExactString()
	case constant.Float:
		return formatFloat(c)
	}
	return c.Val.String()
}

func formatFloat(c *ir.Const) string {
	bits := 64
	if b, ok := c.Typ.Underlying().(*types.Basic); ok && b.Kind() == types.F32 {
		bits = 32
	}
	f, _ := constant.Float64Val(constant.ToFloat(c.Val))
	if bits == 32 {
		f = float64(float32(f))
	}
	if math.IsInf(f, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, bits)
}
