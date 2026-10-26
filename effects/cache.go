package effects

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"an/types"
)

// Input is a checked package for AnalyzeAll.
type Input struct {
	Info       *types.Info
	SourceHash string   // hash of the package's source files
	Imports    []string // paths of the project packages it imports
}

// Cache holds package results by the hash of the package and of the
// results of the packages it imports (spec/22-compiler.md §22.7).
type Cache struct {
	pkgs         map[string]*Package
	Hits, Misses int
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{pkgs: map[string]*Package{}} }

// version is part of every hash, so that results of an older analysis
// are never reused (impl.abi.unstable).
const version = "effects/1"

// AnalyzeAll analyzes the packages of a program, given each after the
// packages it imports. Results found in cache are reused; cache may be
// nil.
func AnalyzeAll(in []Input, cache *Cache) []*Package {
	byPath := map[string]*Package{}
	funcs := map[string]*Summary{}
	imported := func(key string) *Summary { return funcs[key] }
	var out []*Package
	for _, pkg := range in {
		h := sha256.New()
		h.Write([]byte(version + "\x00" + pkg.Info.Pkg.Path + "\x00" + pkg.SourceHash))
		imports := append([]string(nil), pkg.Imports...)
		sort.Strings(imports)
		for _, path := range imports {
			if dep := byPath[path]; dep != nil {
				h.Write([]byte("\x00" + dep.Hash))
			}
		}
		hash := hex.EncodeToString(h.Sum(nil))
		var res *Package
		if cache != nil {
			if res = cache.pkgs[hash]; res != nil {
				cache.Hits++
			} else {
				cache.Misses++
			}
		}
		if res == nil {
			res = Analyze(pkg.Info, imported)
			res.Hash = hash
			if cache != nil {
				cache.pkgs[hash] = res
			}
		}
		byPath[res.Path] = res
		for k, s := range res.Funcs {
			funcs[k] = s
		}
		out = append(out, res)
	}
	return out
}
