// Package ir defines the intermediate representation shared by all
// backends (spec/22-compiler.md).
//
// This is the first, minimal form: functions are straight-line lists of
// instructions. Values, blocks and SSA arrive with roadmap step 6.
package ir

// Program is a whole program ready for a backend.
type Program struct {
	Funcs []*Func
	Main  *Func // func main of package main
	Data  []*Data
}

// Data is a read-only byte string placed in the output image.
type Data struct {
	Name  string
	Bytes []byte
}

// Func is a function without parameters or results.
type Func struct {
	Name string
	Body []Instr
}

// Instr is an instruction of a function body.
type Instr interface{ instr() }

// Call calls a function of the program.
type Call struct {
	Func *Func
}

// PrintStdout writes Data to standard output and panics if the write
// fails (std.fmt.print). It is the lowering of fmt.Print and fmt.Println
// with constant arguments.
//
// Temporary: once the standard library is compiled from Anlang source
// (roadmap steps 9–10), fmt.Println is ordinary Anlang code and this
// instruction is replaced by a runtime write primitive.
type PrintStdout struct {
	Data *Data
}

// Return returns from the function.
type Return struct{}

func (*Call) instr()        {}
func (*PrintStdout) instr() {}
func (*Return) instr()      {}
