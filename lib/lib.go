// Package lib holds the Anlang sources of the standard library packages
// that are not built into the compiler (spec/18-stdlib.md). They are
// embedded in the anlang executable (impl.single-binary): the packages
// under an/ are the import paths an/....
package lib

import "embed"

// FS holds the standard library sources, at their import paths.
//
//go:embed an
var FS embed.FS
