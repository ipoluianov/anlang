// Package build drives a build: it finds the project, selects source
// files, runs the compiler stages and writes the outputs
// (spec/20-toolchain.md).
package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"kern/arch/x86_64"
	"kern/link/elf"
	"kern/modfile"
	"kern/syntax"
	"kern/types"
)

// Target is a standard target (spec/13-targets.md, §13.13).
type Target struct {
	Name     string
	Arch     string
	Platform string
	Step     int // roadmap step that introduces the target
}

// Targets lists the standard targets (target.std.required).
var Targets = []Target{
	{"dos-com", "i8086", "dos", 13},
	{"stm32f103c8", "armv7m", "bare", 16},
	{"web", "wasm32", "web", 18},
	{"linux-x86_64", "x86_64", "linux", 26},
	{"linux-arm64", "arm64", "linux", 27},
	{"macos-arm64", "arm64", "macos", 27},
	{"windows-x86_64", "x86_64", "windows", 28},
	{"atmega328p", "avr", "bare", 51},
	{"dos-exe", "i8086", "dos", 52},
	{"rp2040", "armv6m", "bare", 52},
	{"esp32c3", "riscv32", "bare", 52},
	{"msp430g2553", "msp430", "bare", 52},
	{"bios-boot", "i8086", "bios-boot", 55},
	{"multiboot-x86_64", "x86_64", "multiboot", 55},
	{"uefi-x86_64", "x86_64", "uefi", 55},
	{"uefi-arm64", "arm64", "uefi", 55},
	{"uefi-riscv64", "riscv64", "uefi", 55},
	{"devicetree-arm64", "arm64", "devicetree", 55},
	{"devicetree-riscv64", "riscv64", "devicetree", 55},
	{"android", "arm64", "android", 65},
	{"ios", "arm64", "ios", 66},
	{"ios-sim", "arm64", "ios", 66},
}

// implemented lists the targets this compiler can generate code for.
var implemented = map[string]bool{"linux-x86_64": true}

var (
	architectures = []string{"i8086", "x86_64", "armv6m", "armv7m", "avr", "msp430", "riscv32", "riscv64", "arm64", "wasm32"}
	platforms     = []string{"dos", "bare", "linux", "windows", "macos", "ios", "android", "web"}
)

// LookupTarget finds a standard target by name.
func LookupTarget(name string) (Target, bool) {
	for _, t := range Targets {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

// HostTarget returns the target of the machine running the toolchain.
func HostTarget() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	}
	return runtime.GOOS + "-" + arch
}

// Project is a loaded project: its root directory and kern.mod.
type Project struct {
	Root string
	Mod  *modfile.File
}

// ErrNoProject is returned when no kern.mod is found.
var ErrNoProject = errors.New("no kern.mod found in this directory or any parent (tool.project.root)")

// Load finds the project that contains dir (tool.project.root) and parses
// its kern.mod.
func Load(dir string) (*Project, []error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, []error{err}
	}
	for d := abs; ; d = filepath.Dir(d) {
		data, err := os.ReadFile(filepath.Join(d, "kern.mod"))
		if err == nil {
			mod, errs := modfile.Parse("kern.mod", data)
			if len(errs) > 0 {
				return nil, asErrors(errs)
			}
			return &Project{Root: d, Mod: mod}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, []error{err}
		}
		if filepath.Dir(d) == d {
			return nil, []error{ErrNoProject}
		}
	}
}

// DefaultTargets returns the targets built when no -target is given
// (tool.cmd.build).
func (p *Project) DefaultTargets() []string {
	if len(p.Mod.Targets) > 0 {
		return p.Mod.Targets
	}
	return []string{HostTarget()}
}

// Output returns the path of the executable built for target.
func (p *Project) Output(target string) string {
	return filepath.Join(p.Root, "out", target, p.Mod.Module)
}

// Build builds the program in the project root for one target and
// returns the path of the output file.
func (p *Project) Build(target string) (string, []error) {
	t, ok := LookupTarget(target)
	if !ok {
		return "", []error{fmt.Errorf("unknown target %q (target.select.resolve)", target)}
	}
	if !implemented[target] {
		return "", []error{fmt.Errorf("target %s is not supported yet by this compiler (spec/24-roadmap.md, step %d)", target, t.Step)}
	}

	files, err := SelectFiles(p.Root, t)
	if err != nil {
		return "", []error{err}
	}
	if len(files) == 0 {
		return "", []error{fmt.Errorf("%s: no Kern source files for target %s", p.Root, target)}
	}

	var parsed []*syntax.File
	var errs []*syntax.Error
	for _, name := range files {
		src, err := os.ReadFile(filepath.Join(p.Root, name))
		if err != nil {
			return "", []error{err}
		}
		f, ferrs := syntax.Parse(name, src)
		errs = append(errs, ferrs...)
		if f != nil {
			parsed = append(parsed, f)
		}
	}
	if len(errs) > 0 {
		return "", asErrors(errs)
	}

	prog, errs := types.Check(&types.Package{Module: p.Mod.Module, Files: parsed})
	if len(errs) > 0 {
		return "", asErrors(errs)
	}

	text, entry, err := x86_64.GenLinux(prog)
	if err != nil {
		return "", []error{err}
	}
	exe := &elf.Exec{Machine: elf.MachineX86_64, Base: 0x400000, Text: text, Entry: entry}

	out := p.Output(target)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", []error{err}
	}
	if err := os.WriteFile(out, exe.Bytes(), 0o755); err != nil {
		return "", []error{err}
	}
	return out, nil
}

// SelectFiles returns the names of the source files in dir that belong
// to a build for target t, sorted (decl.file.ext, decl.file.ignored,
// decl.file.suffix). Test files are excluded.
func SelectFiles(dir string, t Target) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".kn") ||
			strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		base := strings.TrimSuffix(name, ".kn")
		if strings.HasSuffix(base, "_test") {
			continue
		}
		if matchSuffix(base, t) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// matchSuffix applies decl.file.suffix to a file name without extension.
// Suffixes are matched against the end of the name, because architecture
// names such as x86_64 contain '_'.
func matchSuffix(base string, t Target) bool {
	for _, a := range architectures {
		if !strings.HasSuffix(base, "_"+a) {
			continue
		}
		rest := strings.TrimSuffix(base, "_"+a)
		for _, p := range platforms {
			if strings.HasSuffix(rest, "_"+p) {
				return p == t.Platform && a == t.Arch
			}
		}
		return a == t.Arch
	}
	for _, p := range platforms {
		if strings.HasSuffix(base, "_"+p) {
			return p == t.Platform
		}
	}
	return true
}

func asErrors(errs []*syntax.Error) []error {
	out := make([]error, len(errs))
	for i, e := range errs {
		out[i] = e
	}
	return out
}
