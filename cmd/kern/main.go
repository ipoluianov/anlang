// Command kern is the Kern toolchain (spec/20-toolchain.md).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"kern/build"
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
