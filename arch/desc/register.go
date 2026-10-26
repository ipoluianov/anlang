package desc

import (
	"fmt"
	"strings"
	"sync"

	"an/build"
	"an/effects"
)

// The architectures whose descriptions are in package an/arch, by the
// name of their constant.
var archConsts = map[string]string{"i8086": "I8086", "armv7m": "ARMv7M", "wasm32": "Wasm32"}

var (
	pkgMu     sync.Mutex
	pkgCache  = map[string][]byte{}
	archCache = map[string]*archResult{}
)

type archResult struct {
	a    *Arch
	errs []error
}

// ForName returns the checked description of the architecture with the
// given name, such as "i8086".
func ForName(name string) (*Arch, []error) {
	k, ok := archConsts[name]
	if !ok {
		return nil, []error{fmt.Errorf("no description of architecture %s", name)}
	}
	pkgMu.Lock()
	defer pkgMu.Unlock()
	if r, ok := archCache[name]; ok {
		return r.a, r.errs
	}
	a, errs := Load(k)
	archCache[name] = &archResult{a, errs}
	return a, errs
}

func init() {
	effects.AsmEffects = func(arch, text string) []string {
		a, errs := ForName(arch)
		if len(errs) > 0 {
			return nil
		}
		return a.AsmEffects(text)
	}
	build.InstructionPackage = func(name string) ([]byte, bool) {
		if _, ok := archConsts[name]; !ok {
			return nil, false
		}
		a, errs := ForName(name)
		if len(errs) > 0 {
			var msgs []string
			for _, err := range errs {
				msgs = append(msgs, err.Error())
			}
			panic("desc: " + strings.Join(msgs, "; "))
		}
		pkgMu.Lock()
		defer pkgMu.Unlock()
		if src, ok := pkgCache[name]; ok {
			return src, true
		}
		src := a.InstructionPackage()
		pkgCache[name] = src
		return src, true
	}
}
