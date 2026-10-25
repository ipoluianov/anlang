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

	"an/arch/x86_64"
	"an/format"
	"an/link/elf"
	"an/lower"
	"an/modfile"
	"an/syntax"
	"an/types"
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

// wordBits is the machine word size of each architecture, the size of
// int (type.int.word).
//
// Temporary: until architecture descriptions exist (roadmap step 11),
// the size of usize is taken to be the word size too.
var wordBits = map[string]int{
	"i8086": 16, "avr": 16, "msp430": 16,
	"armv6m": 32, "armv7m": 32, "riscv32": 32, "wasm32": 32,
	"x86_64": 64, "arm64": 64, "riscv64": 64,
}

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

// Project is a loaded project: its root directory and anlang.mod.
type Project struct {
	Root string
	Mod  *modfile.File
}

// ErrNoProject is returned when no anlang.mod is found.
var ErrNoProject = errors.New("no anlang.mod found in this directory or any parent (tool.project.root)")

// Load finds the project that contains dir (tool.project.root) and parses
// its anlang.mod.
func Load(dir string) (*Project, []error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, []error{err}
	}
	for d := abs; ; d = filepath.Dir(d) {
		data, err := os.ReadFile(filepath.Join(d, "anlang.mod"))
		if err == nil {
			mod, errs := modfile.Parse("anlang.mod", data)
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

	info, errs := p.Check(target)
	if len(errs) > 0 {
		return "", errs
	}
	prog, lerrs := lower.Program(info)
	if len(lerrs) > 0 {
		return "", asErrors(lerrs)
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

// Check selects the source files of the program in the project root for
// one target, parses and type-checks them. Diagnostics are returned as
// *syntax.Error; other errors (an unknown target, unreadable files) as
// plain errors.
func (p *Project) Check(target string) (*types.Info, []error) {
	t, ok := LookupTarget(target)
	if !ok {
		return nil, []error{fmt.Errorf("unknown target %q (target.select.resolve)", target)}
	}
	files, err := SelectFiles(p.Root, t)
	if err != nil {
		return nil, []error{err}
	}
	if len(files) == 0 {
		return nil, []error{fmt.Errorf("%s: no Anlang source files for target %s", p.Root, target)}
	}
	var srcs []Source
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(p.Root, name))
		if err != nil {
			return nil, []error{err}
		}
		srcs = append(srcs, Source{Name: name, Data: data})
	}
	info, errs := CheckSources(p.Mod.Module, t, srcs)
	if len(errs) > 0 {
		return nil, asErrors(errs)
	}
	return info, nil
}

// Source is a source file: its name for diagnostics and its contents.
type Source struct {
	Name string
	Data []byte
}

// CheckSources parses srcs, checks that they are in canonical form and
// type-checks them as package main of module for target t.
func CheckSources(module string, t Target, srcs []Source) (*types.Info, []*syntax.Error) {
	var parsed []*syntax.File
	var errs []*syntax.Error
	for _, src := range srcs {
		f, ferrs := syntax.Parse(src.Name, src.Data)
		errs = append(errs, ferrs...)
		if len(ferrs) == 0 {
			errs = append(errs, format.Canonical(src.Name, src.Data)...)
		}
		if f != nil {
			parsed = append(parsed, f)
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	word := wordBits[t.Arch]
	return types.Check(&types.Config{Module: module, IntBits: word, AddrBits: word}, parsed)
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
		if e.IsDir() || !strings.HasSuffix(name, ".an") ||
			strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		base := strings.TrimSuffix(name, ".an")
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

// SourceFiles returns the paths, relative to the project root, of all
// Anlang source files of the project: every .an file in the root and its
// subdirectories, except in out/, testdata/ and directories whose names
// begin with "." or "_".
func (p *Project) SourceFiles() ([]string, error) {
	var files []string
	err := filepath.WalkDir(p.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != p.Root && (name == "out" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".an") && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "_") {
			rel, err := filepath.Rel(p.Root, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}
