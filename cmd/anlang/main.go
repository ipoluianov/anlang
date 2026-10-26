// Command anlang is the Anlang toolchain (spec/20-toolchain.md).
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"an/arch/desc"
	"an/build"
	_ "an/codegen" // the code generator of the targets with target packages
	"an/effects"
	"an/emu/cortexm"
	"an/emu/dos"
	"an/emu/wasm"
	"an/format"
	"an/interp"
	"an/modfile"
	"an/syntax"
)

// Exit statuses (tool.exit-status).
const (
	exitOK       = 0
	exitProblems = 1
	exitUsage    = 2
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitUsage)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "build":
		os.Exit(runBuild(args))
	case "run":
		os.Exit(runRun(args))
	case "fmt":
		os.Exit(runFmt(args))
	case "query":
		os.Exit(runQuery(args))
	case "help", "-h", "-help", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "anlang: unknown command %q\n", cmd)
		usage()
		os.Exit(exitUsage)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: anlang <command> [flags]

Commands:
  build [-target t1,t2] [-ir]
                          build the program in the current project,
                          or print its IR with -ir
  run   [-target t] [-emu ir|builtin] [-screen] [-- args]
                          build and run the program on this machine, in
                          the built-in emulator of a target that cannot
                          run here (or with -emu builtin), or in the IR
                          interpreter with -emu ir
  fmt   [-check] [files]  rewrite Anlang files and anlang.mod in canonical form
  query [-target t] effects [symbol]
                          print the effects of the functions of the program
                          or of one function, with the call chain to the
                          source of each

Implemented targets: linux-x86_64 (a walking skeleton) and dos-com.
`)
}

func runBuild(args []string) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	target := fs.String("target", "", "comma-separated targets (default: targets in anlang.mod)")
	printIR := fs.Bool("ir", false, "print the IR of the program instead of building it")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "anlang build: unexpected arguments:", strings.Join(fs.Args(), " "))
		return exitUsage
	}
	p, status := load()
	if p == nil {
		return status
	}
	targets := p.DefaultTargets()
	if *target != "" {
		targets = strings.Split(*target, ",")
	}
	status = exitOK
	for _, t := range targets {
		if *printIR {
			prog, errs := p.Program(strings.TrimSpace(t))
			if len(errs) > 0 {
				report(errs)
				status = exitProblems
				continue
			}
			fmt.Print(prog)
			continue
		}
		if _, errs := p.Build(strings.TrimSpace(t)); len(errs) > 0 {
			report(errs)
			status = exitProblems
		}
	}
	return status
}

func runRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	target := fs.String("target", "", "target to build and run (default: the first target in anlang.mod)")
	emu := fs.String("emu", "", "emulator to run in: ir (the IR interpreter) or builtin (the emulator of the target)")
	screen := fs.Bool("screen", false, "write the text screen of the built-in emulator after the program ends")
	gpio := fs.Bool("gpio", false, "write the changes of the output pins in the built-in emulator after the program ends")
	limit := fs.Duration("for", 0, "end the run in the built-in emulator after this much time of the model, such as 3s")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *target == "" {
		p, status := load()
		if p == nil {
			return status
		}
		*target = p.DefaultTargets()[0]
	}
	switch *emu {
	case "":
	case "ir":
		return runIR(*target)
	case "builtin":
		return runBuiltin(*target, emuOptions{*screen, *gpio, *limit})
	default:
		fmt.Fprintf(os.Stderr, "anlang run: unknown emulator %s\n", *emu)
		return exitUsage
	}
	if *target != build.HostTarget() {
		return runBuiltin(*target, emuOptions{*screen, *gpio, *limit})
	}
	p, status := load()
	if p == nil {
		return status
	}
	out, errs := p.Build(*target)
	if len(errs) > 0 {
		report(errs)
		return exitProblems
	}
	cmd := exec.Command(out, fs.Args()...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "anlang run:", err)
		return exitUsage
	}
	return exitOK
}

// runBuiltin builds the program and runs it in the built-in emulator of
// the target (tool.emu.target).
// emuOptions are the observations and limits of a run in a built-in
// emulator.
type emuOptions struct {
	screen, gpio bool
	limit        time.Duration
}

func runBuiltin(target string, opts emuOptions) int {
	t, ok := build.LookupTarget(target)
	if !ok {
		fmt.Fprintf(os.Stderr, "anlang run: unknown target %q\n", target)
		return exitUsage
	}
	d, errs := t.Description()
	if len(errs) > 0 {
		report(errs)
		return exitProblems
	}
	if d == nil || d.Platform.Name != "dos" && d.Platform.Name != "web" && (d.Platform.Name != "bare" || d.Arch.Name != "armv7m") {
		fmt.Fprintf(os.Stderr, "anlang run: target %s has no built-in emulator yet\n", target)
		return exitUsage
	}
	p, status := load()
	if p == nil {
		return status
	}
	out, errs := p.Build(target)
	if len(errs) > 0 {
		report(errs)
		return exitProblems
	}
	image, err := os.ReadFile(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "anlang run:", err)
		return exitUsage
	}
	if d.Platform.Name == "web" {
		// The WebAssembly interpreter with WASI (tool.emu.wasm).
		w := &wasm.WASI{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
		code, err := w.Run(image)
		if err != nil {
			fmt.Fprintln(os.Stderr, "anlang run: emulation stopped:", err)
			return exitUsage
		}
		return code
	}
	a, errs := desc.ForName(d.Arch.Name)
	if len(errs) > 0 {
		report(errs)
		return exitProblems
	}
	if d.Platform.Name == "bare" {
		return runCortexM(a, d, out, opts)
	}
	m, err := dos.Load(a, image)
	if err != nil {
		fmt.Fprintln(os.Stderr, "anlang run:", err)
		return exitUsage
	}
	m.Stdout, m.Stderr, m.Stdin = os.Stdout, os.Stderr, os.Stdin
	code, err := m.Run()
	if opts.screen {
		fmt.Print(m.Screen())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "anlang run: emulation stopped:", err)
		return exitUsage
	}
	return code
}

// runCortexM runs the BIN image next to the first output file out in
// the Cortex-M emulator (tool.emu.cortexm).
func runCortexM(a *desc.Arch, d *build.TargetDesc, out string, opts emuOptions) int {
	bin := strings.TrimSuffix(out, filepath.Ext(out)) + ".bin"
	image, err := os.ReadFile(bin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "anlang run: the target writes no BIN file:", err)
		return exitUsage
	}
	m, err := cortexm.Load(a, d, image)
	if err != nil {
		fmt.Fprintln(os.Stderr, "anlang run:", err)
		return exitUsage
	}
	m.Stdout, m.Stderr, m.Stdin, m.UART = os.Stdout, os.Stderr, os.Stdin, os.Stdout
	m.MaxTime = uint64(opts.limit.Nanoseconds()) * 1000
	code, err := m.Run()
	if opts.gpio {
		fmt.Print(m.PinText())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "anlang run: emulation stopped:", err)
		return exitUsage
	}
	return code
}

// runIR runs the program in the IR interpreter (tool.emu.ir).
func runIR(target string) int {
	p, status := load()
	if p == nil {
		return status
	}
	prog, errs := p.Program(target)
	if len(errs) > 0 {
		report(errs)
		return exitProblems
	}
	if err := interp.Run(prog, interp.Options{Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
		return 2 // the status of a panicking program (tool.emu.ir)
	}
	return exitOK
}

// runQuery answers a query of the semantic API (tool.cmd.query).
func runQuery(args []string) int {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	target := fs.String("target", build.HostTarget(), "target")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() < 1 || fs.NArg() > 2 || fs.Arg(0) != "effects" {
		fmt.Fprintln(os.Stderr, "usage: anlang query [-target t] effects [symbol]")
		return exitUsage
	}
	p, status := load()
	if p == nil {
		return status
	}
	prog, errs := p.Effects(*target, nil)
	if len(errs) > 0 {
		report(errs)
		return exitProblems
	}
	symbol := fs.Arg(1)
	found := false
	for _, key := range prog.Keys() {
		if symbol != "" && key != symbol && !strings.HasSuffix(key, "/"+symbol) && !strings.HasSuffix(key, "."+symbol) {
			continue
		}
		found = true
		printEffects(prog, key)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "anlang query: no function %s\n", symbol)
		return exitProblems
	}
	return exitOK
}

// printEffects prints the effects of a function: its own effects, its
// parametric and open references, and the further effects of executing
// it, each with the call chain to its source (eff.report.tools).
func printEffects(prog *effects.Program, key string) {
	s := prog.Summary(key)
	fmt.Printf("%s (%s)\n", key, s.Pos)
	for _, e := range s.Own() {
		fmt.Printf("\t%s: %s\n", e.Name(), strings.Join(prog.Chain(key, e, false), " → "))
	}
	for _, line := range strings.Split(strings.TrimPrefix(s.String(), s.Key+":"), ";")[1:] {
		fmt.Printf("\t%s\n", strings.TrimSpace(line))
	}
	exec := prog.Exec(key)
	var more []string
	for e := range exec {
		if _, own := s.C[e]; !own {
			more = append(more, fmt.Sprintf("\texecutes %s: %s", e.Name(), strings.Join(prog.Chain(key, e, true), " → ")))
		}
	}
	sort.Strings(more)
	for _, line := range more {
		fmt.Println(line)
	}
}

func load() (*build.Project, int) {
	p, errs := build.Load(".")
	if len(errs) > 0 {
		report(errs)
		if errors.Is(errs[0], build.ErrNoProject) {
			return nil, exitUsage
		}
		return nil, exitProblems
	}
	return p, exitOK
}

func report(errs []error) {
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, e)
	}
}

func runFmt(args []string) int {
	fs := flag.NewFlagSet("fmt", flag.ContinueOnError)
	check := fs.Bool("check", false, "report files that are not canonical; do not modify them")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	files := fs.Args()
	if len(files) == 0 {
		p, status := load()
		if p == nil {
			return status
		}
		rel, err := p.SourceFiles()
		if err != nil {
			fmt.Fprintln(os.Stderr, "anlang fmt:", err)
			return exitUsage
		}
		files = append(files, filepath.Join(p.Root, "anlang.mod"))
		for _, f := range rel {
			files = append(files, filepath.Join(p.Root, f))
		}
	}

	status := exitOK
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "anlang fmt:", err)
			status = exitProblems
			continue
		}
		name := displayPath(path)
		var out []byte
		if filepath.Base(path) == "anlang.mod" {
			if _, errs := modfile.Parse(name, src); len(errs) > 0 {
				report(asErrors(errs))
				status = exitProblems
				continue
			}
			out = modfile.Format(src)
		} else {
			var errs []*syntax.Error
			out, errs = format.Source(name, src, syntax.AnlangMode)
			if len(errs) > 0 {
				report(asErrors(errs))
				status = exitProblems
				continue
			}
		}
		if bytes.Equal(out, src) {
			continue
		}
		fmt.Println(name)
		if *check {
			status = exitProblems
			continue
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "anlang fmt:", err)
			status = exitProblems
		}
	}
	return status
}

// displayPath returns path relative to the working directory if possible.
func displayPath(path string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return path
}

func asErrors(errs []*syntax.Error) []error {
	out := make([]error, len(errs))
	for i, e := range errs {
		out[i] = e
	}
	return out
}
