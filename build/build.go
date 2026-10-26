// Package build drives a build: it finds the project, selects source
// files, runs the compiler stages and writes the outputs
// (spec/20-toolchain.md).
package build

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"go/constant"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"an/arch/x86_64"
	"an/ct"
	"an/effects"
	"an/format"
	"an/ir"
	"an/lib"
	"an/link/elf"
	"an/link/ihex"
	"an/link/wasm"
	"an/lower"
	"an/modfile"
	"an/syntax"
	"an/types"
	"an/verify/vc"
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

// wordBits returns the width of int on the target (type.int.word).
func (t Target) wordBits() int {
	if d, _ := t.Description(); d != nil {
		return d.Word
	}
	return wordBits[t.Arch]
}

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
	if d, errs := t.Description(); len(errs) > 0 {
		return "", errs
	} else if d != nil && Backend != nil {
		return p.buildImage(t, d)
	}
	if !implemented[target] {
		return "", []error{fmt.Errorf("target %s is not supported yet by this compiler (spec/24-roadmap.md, step %d)", target, t.Step)}
	}

	prog, errs := p.Program(target)
	if len(errs) > 0 {
		return "", errs
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

// buildImage builds the program with the code generator and writes one
// file for each format of the target (target.format.all).
func (p *Project) buildImage(t Target, d *TargetDesc) (string, []error) {
	prog, errs := p.BackendProgram(t.Name)
	if len(errs) > 0 {
		return "", errs
	}
	image, errs := Backend(prog, d)
	if len(errs) > 0 {
		return "", errs
	}
	image.Name = p.Mod.Module
	var first string
	for _, f := range d.Formats {
		out := p.Output(t.Name) + f.Ext(d)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return "", []error{err}
		}
		if err := os.WriteFile(out, FormatImage(f, d, image), 0o644); err != nil {
			return "", []error{err}
		}
		if first == "" {
			first = out
		}
	}
	return first, nil
}

// FormatImage returns the contents of the output file of format f for
// the linked image: the image itself for the raw formats, Intel HEX
// records, an ELF executable whose segment holds the image at the start
// of the Exec region, or a WebAssembly module (target.format.all).
func FormatImage(f FormatDesc, d *TargetDesc, im *Image) []byte {
	code, _, hasRegions := d.Regions()
	origin := uint32(code.At)
	image := im.Bytes
	switch f.Kind() {
	case "wasm":
		return wasmModule(f, d, im)
	case "html":
		// The page runs the module of the first WebAssembly format.
		for _, w := range d.Formats {
			if w.Kind() == "wasm" {
				return wasm.Page(im.Name, wasmModule(w, d, im))
			}
		}
		return nil
	case "hex":
		return ihex.Encode(origin, image)
	case "elf":
		im := &elf.Image{Segments: []elf.Segment{{Addr: origin, Data: image, Exec: true}}}
		if d.Arch.Name == "armv7m" {
			// The EABI version 5 with the soft-float calling convention;
			// the entry is the reset vector of the vector table.
			im.Machine, im.Flags = elf.MachineARM, 0x05000200
			if hasRegions && len(image) >= 8 {
				im.Entry = binary.LittleEndian.Uint32(image[4:])
			}
		}
		return im.Bytes()
	}
	return image
}

// wasmModule returns the WebAssembly module of a structured image.
func wasmModule(f FormatDesc, d *TargetDesc, im *Image) []byte {
	m := &wasm.Module{Funcs: im.Funcs, Entry: im.Entry, Data: im.Bytes, Origin: f.Origin, Memory: im.Memory, Globals: im.Registers}
	for _, i := range d.Platform.Imports {
		m.Imports = append(m.Imports, wasm.Import{Module: i.Module, Name: i.Name, Params: i.Params, Results: i.Results})
	}
	return m.Bytes()
}

// Check checks the program in the project root for one target: package
// main and the project packages it imports, each checked after the
// packages it imports. Diagnostics are returned as *syntax.Error; other
// errors (an unknown target, unreadable files) as plain errors.
func (p *Project) Check(target string) ([]*types.Info, []error) {
	order, _, errs := p.check(target, nil)
	if len(errs) > 0 {
		return nil, errs
	}
	var infos []*types.Info // root, package main, is last
	for _, lp := range order {
		infos = append(infos, lp.info)
	}
	return infos, nil
}

// Effects checks the program for one target and infers its effects
// (spec/07-effects.md). Package results are reused from cache, which may
// be nil.
func (p *Project) Effects(target string, cache *effects.Cache) (*effects.Program, []error) {
	_, prog, errs := p.check(target, cache)
	return prog, errs
}

// check loads and checks the packages of the program, each after the
// packages it imports, infers their effects and checks the effects
// against the constraints.
func (p *Project) check(target string, cache *effects.Cache) ([]*loadedPkg, *effects.Program, []error) {
	return p.checkFrom(p.Mod.Module, target, cache)
}

// RuntimePath is the import path of the runtime package, which the code
// generator links into programs (comp.gen.runtime).
const RuntimePath = "an/runtime"

// Library checks the standard library package path with the packages it
// imports for a target, evaluates its computed constants and returns the
// type information of the packages, path last. It is used to read the
// descriptions of architectures, platforms and targets.
func Library(path, target string) ([]*types.Info, []error) {
	p := &Project{Mod: &modfile.File{Module: "anlang_library"}}
	order, _, errs := p.checkFrom(path, target, nil)
	if len(errs) > 0 {
		return nil, errs
	}
	var infos []*types.Info
	for _, lp := range order {
		infos = append(infos, lp.info)
	}
	return infos, nil
}

// checkFrom checks package rootPath with the packages it imports, and
// the packages extra, which are loaded first.
func (p *Project) checkFrom(rootPath, target string, cache *effects.Cache, extra ...string) ([]*loadedPkg, *effects.Program, []error) {
	t, ok := LookupTarget(target)
	if !ok {
		return nil, nil, []error{fmt.Errorf("unknown target %q (target.select.resolve)", target)}
	}
	l := &loader{p: p, t: t, pkgs: map[string]*loadedPkg{}}
	for _, path := range extra {
		if _, err := l.load(path, nil); err != nil {
			return nil, nil, []error{err}
		}
	}
	root, err := l.load(rootPath, nil)
	if err != nil {
		return nil, nil, []error{err}
	}
	if root == nil {
		return nil, nil, []error{fmt.Errorf("%s: no Anlang source files for target %s", p.Root, target)}
	}
	if len(l.errs) > 0 {
		return nil, nil, asErrors(l.errs)
	}
	var errs []*syntax.Error
	checked := map[string]*types.Package{}
	for _, lp := range l.order {
		imports := map[string]*types.Package{}
		for _, path := range lp.imports {
			if pkg := checked[path]; pkg != nil {
				imports[path] = pkg
			}
		}
		info, perrs := checkFiles(p.Mod.Module, lp.path, t, imports, lp.files)
		errs = append(errs, perrs...)
		checked[lp.path] = info.Pkg
		lp.info = info
	}
	if len(errs) > 0 {
		return nil, nil, asErrors(errs)
	}
	desc, derrs := t.Description()
	if len(derrs) > 0 {
		return nil, nil, derrs
	}
	if perrs := checkPlatformProfile(l.order[len(l.order)-1], desc, t.Name); len(perrs) > 0 {
		return nil, nil, asErrors(perrs)
	}
	var in []effects.Input
	for _, lp := range l.order {
		in = append(in, effects.Input{Info: lp.info, SourceHash: lp.hash, Imports: lp.imports})
	}
	prog, eerrs := checkEffects(in, p.Mod.Module, "anlang.mod", p.Mod.Effects, t, desc, cache)
	if len(eerrs) > 0 {
		return nil, nil, asErrors(eerrs)
	}
	var infos []*types.Info
	for _, lp := range l.order {
		infos = append(infos, lp.info)
	}
	if cerrs := ct.Evaluate(infos, prog, t.wordBits(), ct.DefaultLimits, l.files); len(cerrs) > 0 {
		return nil, nil, asErrors(cerrs)
	}
	// The proof obligations (spec/08-contracts.md).
	var keys []string
	for _, lp := range l.order {
		keys = append(keys, lp.path+"\x00"+lp.hash)
	}
	if verrs := vc.CheckCached(infos, keys); len(verrs) > 0 {
		return nil, nil, asErrors(verrs)
	}
	return l.order, prog, nil
}

// Program checks the program in the project root for one target and
// translates it to IR.
func (p *Project) Program(target string) (*ir.Program, []error) {
	infos, errs := p.Check(target)
	if len(errs) > 0 {
		return nil, errs
	}
	t, _ := LookupTarget(target)
	prog, lerrs := lower.Program(infos...)
	if len(lerrs) > 0 {
		return nil, asErrors(lerrs)
	}
	prog.IntBits = t.wordBits()
	return prog, nil
}

// mainProfile returns the profile in which package main runs on the
// target: the first of its profiles that the platform allows.
func mainProfile(order []*loadedPkg, d *TargetDesc) string {
	for _, lp := range order {
		if lp.info == nil || lp.info.Pkg.Name != "main" {
			continue
		}
		for _, p := range lp.info.Pkg.Profiles {
			for _, q := range d.Platform.Profiles {
				if p == q {
					return p
				}
			}
		}
	}
	return ""
}

// Backend generates the image of a program for a target that has a
// target package (spec/22-compiler.md §22.11). Package codegen sets it.
var Backend func(prog *ir.Program, d *TargetDesc) (*Image, []error)

// Image is a linked image (comp.gen.link): the code and the constant data
// at the origin, or, for an architecture with structured control flow
// (target.arch.structured), the code of each function and the data.
type Image struct {
	// Bytes are the code and the data from the origin; of a structured
	// image, the data.
	Bytes []byte
	// Funcs are the bodies of the functions of a structured image, in the
	// order of their indices after the imports of the platform; Entry is
	// the index of the entry code among them, Memory the bytes of memory
	// from address 0 that the data and the stack need, and Registers the
	// number of registers of the architecture that the functions share.
	Funcs     [][]byte
	Entry     int
	Memory    int
	Registers int
	// Name is the name of the program, for the formats that show it.
	Name string
}

// BackendProgram checks the program for a target and translates it to
// IR together with the runtime package (comp.gen.runtime).
func (p *Project) BackendProgram(target string) (*ir.Program, []error) {
	order, _, errs := p.checkFrom(p.Mod.Module, target, nil, RuntimePath)
	if len(errs) > 0 {
		return nil, errs
	}
	var infos []*types.Info
	var extra []*types.Func
	for _, lp := range order {
		infos = append(infos, lp.info)
		if lp.path == RuntimePath {
			extra = append(extra, lp.info.Funcs...)
		}
	}
	t, _ := LookupTarget(target)
	prog, lerrs := lower.ProgramWith(extra, infos...)
	if len(lerrs) > 0 {
		return nil, asErrors(lerrs)
	}
	prog.IntBits = t.wordBits()
	if d, _ := t.Description(); d != nil {
		prog.Core = mainProfile(order, d) == "core"
	}
	return prog, nil
}

// loader loads the packages of a program.
type loader struct {
	p     *Project
	t     Target
	pkgs  map[string]*loadedPkg
	stack []*loadedPkg // packages being loaded, for cycle detection
	order []*loadedPkg // loaded packages, each after the packages it imports
	errs  []*syntax.Error
}

type loadedPkg struct {
	path    string
	dir     fs.FS  // the files of the directory of the package
	hash    string // hash of the source files
	files   []*syntax.File
	imports []string // project packages imported
	info    *types.Info
	done    bool
}

// load loads the project package path and the project packages it
// imports. It returns nil if the package has no source files for the
// target; the importer then reports the missing package.
var (
	deviceMu  sync.Mutex
	deviceSrc = map[string][]byte{}
)

// devicePackage returns the source of an/device for a target, generated
// once.
func devicePackage(target string, d *TargetDesc) []byte {
	deviceMu.Lock()
	defer deviceMu.Unlock()
	if src, ok := deviceSrc[target]; ok {
		return src
	}
	src := DevicePackage(target, &d.Device)
	deviceSrc[target] = src
	return src
}

// files returns the files of the directory of a loaded package, for its
// computed constants (ct.eval.read-input).
func (l *loader) files(path string) fs.FS {
	if lp := l.pkgs[path]; lp != nil {
		return lp.dir
	}
	return nil
}

func (l *loader) load(path string, from *syntax.ImportSpec) (*loadedPkg, error) {
	if lp := l.pkgs[path]; lp != nil {
		if !lp.done {
			var cycle []string
			for _, s := range l.stack[slices.Index(l.stack, lp):] {
				cycle = append(cycle, s.path)
			}
			l.errs = append(l.errs, &syntax.Error{Pos: from.Path.Pos(), Rule: "decl.import.no-cycle",
				Msg: fmt.Sprintf("import cycle not allowed: %s -> %s", strings.Join(cycle, " -> "), path)})
		}
		return lp, nil
	}
	// A project package, or a standard library package of the embedded
	// sources.
	var fsys fs.FS
	var dir, rel string
	var generated []byte
	if strings.HasPrefix(path, "an/arch/") && InstructionPackage != nil && !isLibPackage(path) {
		src, ok := InstructionPackage(strings.TrimPrefix(path, "an/arch/"))
		if !ok {
			return nil, nil
		}
		generated = src
	} else if path == "an/device" {
		// The package of the device of the target (emb.device.package).
		d, errs := l.t.Description()
		if len(errs) > 0 {
			return nil, errs[0]
		}
		if d == nil || d.Device.Name == "" {
			return nil, nil // reported at the import
		}
		generated = devicePackage(l.t.Name, d)
	} else if isLibPackage(path) {
		fsys, dir, rel = lib.FS, path, path
	} else {
		rel = strings.TrimPrefix(strings.TrimPrefix(path, l.p.Mod.Module), "/")
		fsys, dir = os.DirFS(l.p.Root), rel
		if dir == "" {
			dir = "."
		}
		if fi, err := fs.Stat(fsys, dir); err != nil || !fi.IsDir() {
			return nil, nil
		}
	}
	var names []string
	if generated == nil {
		var err error
		names, err = selectFiles(fsys, dir, l.t)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, nil
		}
	}
	var srcs []Source
	if generated != nil {
		srcs = append(srcs, Source{Name: path + "/" + pathpkg.Base(path) + ".an", Data: generated})
	}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, pathpkg.Join(dir, name))
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, Source{Name: pathpkg.Join(rel, name), Data: data})
	}
	files, errs := parseSources(srcs)
	l.errs = append(l.errs, errs...)

	h := sha256.New()
	for _, src := range srcs {
		fmt.Fprintf(h, "%s\x00%d\x00", src.Name, len(src.Data))
		h.Write(src.Data)
	}
	lp := &loadedPkg{path: path, files: files, hash: hex.EncodeToString(h.Sum(nil))}
	if fsys != nil {
		lp.dir, _ = fs.Sub(fsys, dir)
	}
	l.pkgs[path] = lp
	l.stack = append(l.stack, lp)
	module := l.p.Mod.Module
	for _, f := range files {
		for _, spec := range f.Imports {
			ipath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || !strings.HasPrefix(ipath, module+"/") && !isLibPackage(ipath) && !strings.HasPrefix(ipath, "an/arch/") && ipath != "an/device" {
				continue // the root package and other paths are reported by the checker
			}
			if ipath == "an/runtime" && !strings.HasPrefix(path, "an/") {
				l.errs = append(l.errs, &syntax.Error{Pos: spec.Path.Pos(), Rule: "rt.lang.not-importable", Msg: "the runtime package an/runtime cannot be imported"})
				continue
			}
			if _, err := l.load(ipath, spec); err != nil {
				return nil, err
			}
			if !slices.Contains(lp.imports, ipath) {
				lp.imports = append(lp.imports, ipath)
			}
		}
	}
	l.stack = l.stack[:len(l.stack)-1]
	lp.done = true
	l.order = append(l.order, lp)
	return lp, nil
}

// InstructionPackage returns the generated source of the instruction
// package of an architecture, an/arch/<name> (target.arch.instr-package),
// or false if there is no such architecture. Package arch/desc sets it.
var InstructionPackage func(arch string) ([]byte, bool)

// isLibPackage reports whether path is a standard library package with
// Anlang sources in the embedded library.
func isLibPackage(path string) bool {
	if !strings.HasPrefix(path, "an/") {
		return false
	}
	fi, err := fs.Stat(lib.FS, path)
	return err == nil && fi.IsDir()
}

// Source is a source file: its name for diagnostics and its contents.
type Source struct {
	Name string
	Data []byte
}

// CheckSources parses srcs, checks that they are in canonical form and
// type-checks them as package main of module for target t.
func CheckSources(module string, t Target, srcs []Source) (*types.Info, []*syntax.Error) {
	files, errs := parseSources(srcs)
	if len(errs) > 0 {
		return nil, errs
	}
	info, errs := checkFiles(module, module, t, nil, files)
	if len(errs) > 0 {
		return info, errs
	}
	desc, derrs := t.Description()
	for _, err := range derrs {
		errs = append(errs, &syntax.Error{Msg: err.Error()})
	}
	errs = append(errs, checkPlatformProfile(&loadedPkg{info: info, files: files}, desc, t.Name)...)
	if len(errs) > 0 {
		return info, errs
	}
	prog, errs := checkEffects([]effects.Input{{Info: info}}, module, "anlang.mod", nil, t, desc, nil)
	if len(errs) > 0 {
		return info, errs
	}
	if errs := ct.Evaluate([]*types.Info{info}, prog, t.wordBits(), ct.DefaultLimits, nil); len(errs) > 0 {
		return info, errs
	}
	return info, vc.Check([]*types.Info{info})
}

// targetEffects are the environment effects that targets provide and
// lack (target.effects.provides, target.effects.lacks), for the targets
// without a target package yet whose descriptions the specification
// shows. Temporary: they come from the target packages of the roadmap
// steps that introduce the targets.
var targetEffects = map[string]struct{ provides, lacks []string }{
	"linux-x86_64":   {provides: hostEffects},
	"linux-arm64":    {provides: hostEffects},
	"macos-arm64":    {provides: hostEffects},
	"windows-x86_64": {provides: hostEffects},
}

var hostEffects = []string{"Alloc", "StaticAlloc", "Console", "File", "Network", "Keyboard", "Tasks", "Clock", "Random"}

// checkEffects infers the effects of the checked packages, given each
// after the packages it imports with package main last, and checks them
// against the profiles, anlang.mod and the target (spec/07-effects.md
// §7.6).
func checkEffects(in []effects.Input, module, modName string, rules []modfile.EffectRule, t Target, desc *TargetDesc, cache *effects.Cache) (*effects.Program, []*syntax.Error) {
	prog := effects.NewProgram(effects.AnalyzeAll(in, cache))
	c := effects.Constraints{
		Module:   module,
		ModFile:  modName,
		Rules:    rules,
		Profiles: map[string][]string{},
		Known:    map[string]bool{},
		Target:   t.Name,
	}
	for _, name := range types.BaseEffects {
		c.Known[name] = true
	}
	for _, name := range effects.LibraryEffects {
		c.Known[name] = true
	}
	for _, x := range in {
		c.Profiles[x.Info.Pkg.Path] = x.Info.Pkg.Profiles
		for _, name := range x.Info.Scope.Names() {
			if k, ok := x.Info.Scope.Lookup(name).(*types.Const); ok && k.Type() == types.EffectType && k.Val != nil {
				v := constant.StringVal(k.Val)
				c.Known[v[strings.LastIndexByte(v, '#')+1:]] = true
			}
		}
	}
	if main := in[len(in)-1].Info.Main; main != nil {
		c.Main = in[len(in)-1].Info.Pkg.Path + ".main"
	}
	if desc != nil {
		c.HasDesc, c.Provides, c.Lacks = true, desc.Provides, desc.Lacks
	} else if te, ok := targetEffects[t.Name]; ok {
		c.HasDesc, c.Provides, c.Lacks = true, te.provides, te.lacks
	}
	var infos []*types.Info
	for _, x := range in {
		infos = append(infos, x.Info)
		for _, h := range x.Info.Handlers {
			c.Handlers = append(c.Handlers, effects.FuncKey(h))
		}
	}
	errs := prog.Check(c)
	if len(errs) == 0 {
		errs = prog.Ownership(infos)
	}
	return prog, errs
}

// parseSources parses srcs and checks that they are in canonical form
// (lex.source.canonical).
func parseSources(srcs []Source) ([]*syntax.File, []*syntax.Error) {
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
	return parsed, errs
}

// checkFiles type-checks the files of package path for target t.
func checkFiles(module, path string, t Target, imports map[string]*types.Package, files []*syntax.File) (*types.Info, []*syntax.Error) {
	word := wordBits[t.Arch]
	conf := &types.Config{Module: module, Path: path, Imports: imports, IntBits: word, AddrBits: word, Arch: t.Arch, DeferNarrowing: true}
	// The interrupt vectors of the device, for the files with handlers.
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*syntax.FuncDecl); ok && fd.Handler != "" && conf.Interrupts == nil {
				conf.Interrupts = []string{}
				if desc, _ := t.Description(); desc != nil {
					for _, irq := range desc.Device.Interrupts {
						conf.Interrupts = append(conf.Interrupts, irq.Name)
					}
				}
			}
		}
	}
	return types.Check(conf, files)
}

// SelectFiles returns the names of the source files in dir that belong
// to a build for target t, sorted (decl.file.ext, decl.file.ignored,
// decl.file.suffix). Test files are excluded.
func SelectFiles(dir string, t Target) ([]string, error) {
	return selectFiles(os.DirFS(dir), ".", t)
}

func selectFiles(fsys fs.FS, dir string, t Target) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
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
