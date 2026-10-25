// Command kern is the Kern toolchain (spec/20-toolchain.md).
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"kern/build"
	"kern/format"
	"kern/modfile"
	"kern/syntax"
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
	case "help", "-h", "-help", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "kern: unknown command %q\n", cmd)
		usage()
		os.Exit(exitUsage)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: kern <command> [flags]

Commands:
  build [-target t1,t2]   build the program in the current project
  run   [-target t] [-- args]
                          build and run the program on this machine
  fmt   [-check] [files]  rewrite Kern files and kern.mod in canonical form

Only the target linux-x86_64 is implemented so far.
`)
}

func runBuild(args []string) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	target := fs.String("target", "", "comma-separated targets (default: targets in kern.mod)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "kern build: unexpected arguments:", strings.Join(fs.Args(), " "))
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
		if _, errs := p.Build(strings.TrimSpace(t)); len(errs) > 0 {
			report(errs)
			status = exitProblems
		}
	}
	return status
}

func runRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	target := fs.String("target", build.HostTarget(), "target to build and run")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *target != build.HostTarget() {
		fmt.Fprintf(os.Stderr, "kern run: target %s cannot run on this machine (%s); emulators arrive later (kern run -emu)\n", *target, build.HostTarget())
		return exitUsage
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
		fmt.Fprintln(os.Stderr, "kern run:", err)
		return exitUsage
	}
	return exitOK
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
			fmt.Fprintln(os.Stderr, "kern fmt:", err)
			return exitUsage
		}
		files = append(files, filepath.Join(p.Root, "kern.mod"))
		for _, f := range rel {
			files = append(files, filepath.Join(p.Root, f))
		}
	}

	status := exitOK
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "kern fmt:", err)
			status = exitProblems
			continue
		}
		name := displayPath(path)
		var out []byte
		if filepath.Base(path) == "kern.mod" {
			if _, errs := modfile.Parse(name, src); len(errs) > 0 {
				report(asErrors(errs))
				status = exitProblems
				continue
			}
			out = modfile.Format(src)
		} else {
			var errs []*syntax.Error
			out, errs = format.Source(name, src, syntax.KernMode)
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
			fmt.Fprintln(os.Stderr, "kern fmt:", err)
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
