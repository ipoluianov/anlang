package x86_64

import (
	"fmt"

	"an/ir"
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

	for _, f := range p.Funcs {
		a.Label(funcLabel(f))
		for _, in := range f.Body {
			switch in := in.(type) {
			case *ir.Call:
				a.Call(funcLabel(in.Func))
			case *ir.PrintStdout:
				if len(in.Data.Bytes) > 1<<31-1 {
					return nil, 0, fmt.Errorf("x86_64: data %s too large", in.Data.Name)
				}
				a.LeaRIP(RSI, dataLabel(in.Data))
				a.MovImm32(RDX, uint32(len(in.Data.Bytes)))
				a.Call("rt.printStdout")
			case *ir.Return:
				a.Ret()
			default:
				return nil, 0, fmt.Errorf("x86_64: unsupported instruction %T", in)
			}
		}
	}

	genPrintStdout(a)

	for _, d := range p.Data {
		a.Label(dataLabel(d))
		a.Bytes(d.Bytes...)
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
func dataLabel(d *ir.Data) string { return "data." + d.Name }
