package build

import (
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"sync"

	"an/ct"
	"an/lib"
	"an/syntax"
	"an/types"
)

// TargetDesc is a target description (target.desc.types) as the
// toolchain reads it, with the defaults applied.
type TargetDesc struct {
	Arch struct {
		Name     string
		Word     int
		Pointers Pointers
	}
	Platform PlatformDesc
	Word     int
	Pointers Pointers
	Formats  []FormatDesc
	Floats   int // 0 default, 1 hard, 2 soft, 3 none
	Stack    int
	Provides []string // effect names
	Lacks    []string
	OnPanic  int // 1 PrintAndExit, 2 Reset, 3 Halt
	Clock    int
}

// Pointers are the widths of the pointer kinds (target.pointers.kinds).
type Pointers struct {
	Flat, Near, Far, Program int
}

// PlatformDesc is a platform description (target.platform.schema).
type PlatformDesc struct {
	Name     string
	Provides []string
	Dynamic  bool
	Rules    []RuleDesc
	Formats  []string
	Profiles []string
	OnPanic  int
}

// RuleDesc is an instruction selection rule (target.arch.rule).
type RuleDesc struct {
	Op    string
	Width int
	From  int
	Code  string
}

// FormatDesc is a binary format: the name of its type and its fields.
type FormatDesc struct {
	Type    string
	Origin  int
	MaxSize int
}

// Kind returns the kind of the format (target.format.kind).
func (f FormatDesc) Kind() string {
	switch f.Type {
	case "Flat":
		return "flat"
	case "BootSector":
		return "boot"
	}
	return strings.ToLower(f.Type)
}

// Ext returns the extension of the output file of the format.
func (f FormatDesc) Ext(d *TargetDesc) string {
	switch {
	case f.Kind() == "flat" && d.Platform.Name == "dos":
		return ".com"
	case f.Kind() == "mz":
		return ".exe"
	}
	return ".bin"
}

// PointerBits returns the width of the default pointer kind
// (target.word.usize).
func (d *TargetDesc) PointerBits() int {
	if d.Pointers.Flat != 0 {
		return d.Pointers.Flat
	}
	return d.Pointers.Near
}

var (
	descMu    sync.Mutex
	descCache = map[string]*descResult{}
)

type descResult struct {
	desc *TargetDesc
	errs []error
}

// Description returns the description of the target from its target
// package an/target/<name> of the standard library, or nil if the target
// has no package yet (target.pkg.description).
func (t Target) Description() (*TargetDesc, []error) {
	path := "an/target/" + t.Name
	if fi, err := fs.Stat(lib.FS, path); err != nil || !fi.IsDir() {
		return nil, nil
	}
	descMu.Lock()
	defer descMu.Unlock()
	if r, ok := descCache[t.Name]; ok {
		return r.desc, r.errs
	}
	d, errs := loadDescription(path)
	descCache[t.Name] = &descResult{d, errs}
	return d, errs
}

func loadDescription(path string) (*TargetDesc, []error) {
	infos, errs := Library(path, "linux-x86_64")
	if len(errs) > 0 {
		return nil, errs
	}
	k, ok := infos[len(infos)-1].Scope.Lookup("Description").(*types.Const)
	if !ok || !k.Computed {
		return nil, []error{fmt.Errorf("%s: no constant Description (target.pkg.description)", path)}
	}
	d := &TargetDesc{}
	if err := ct.Decode(k.Value, k.Type(), reflect.ValueOf(d).Elem()); err != nil {
		return nil, []error{fmt.Errorf("%s.Description: %v", path, err)}
	}
	for _, list := range []*[]string{&d.Provides, &d.Lacks, &d.Platform.Provides} {
		for i, e := range *list {
			(*list)[i] = e[strings.LastIndex(e, "#")+1:] // "an/effect.Console#Console"
		}
	}
	if d.Word == 0 {
		d.Word = d.Arch.Word
	}
	if d.Pointers == (Pointers{}) {
		d.Pointers = d.Arch.Pointers
	}
	if d.OnPanic == 0 {
		d.OnPanic = d.Platform.OnPanic
	}
	return d, nil
}

// checkPlatformProfile checks that the profile of package main is one
// the platform allows (target.platform.profile).
func checkPlatformProfile(main *loadedPkg, d *TargetDesc, target string) []*syntax.Error {
	if d == nil || main.info == nil || main.info.Pkg.Name != "main" {
		return nil
	}
	for _, p := range main.info.Pkg.Profiles {
		for _, q := range d.Platform.Profiles {
			if p == q {
				return nil
			}
		}
	}
	var pos syntax.Pos
	if len(main.files) > 0 {
		pos = main.files[0].Name.Pos()
	}
	return []*syntax.Error{{Pos: pos, Rule: "target.platform.profile", Msg: fmt.Sprintf("package main has profile %s, but platform %s of target %s allows only %s",
		strings.Join(main.info.Pkg.Profiles, ", "), d.Platform.Name, target, strings.Join(d.Platform.Profiles, ", "))}}
}
