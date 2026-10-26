package ct_test

import (
	"go/constant"
	"hash/crc32"
	"os"
	"testing"

	"an/build"
	"an/types"
)

// TestCRCTable checks the acceptance criterion of roadmap step 10: the
// CRC table of spec/10-compile-time.md, computed at compile time, equals
// the IEEE table of Go's hash/crc32.
func TestCRCTable(t *testing.T) {
	src, err := os.ReadFile("../conformance/testdata/ct.const.call/crc.an")
	if err != nil {
		t.Fatal(err)
	}
	target, _ := build.LookupTarget("linux-x86_64")
	info, errs := build.CheckSources("conf", target, []build.Source{{Name: "crc.an", Data: src}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var table *types.Const
	for _, k := range info.Computed {
		if k.Name() == "crcTable" {
			table = k
		}
	}
	if table == nil {
		t.Fatal("no computed constant crcTable")
	}
	v, ok := table.Value.(*types.Composite)
	if !ok || len(v.Elems) != 256 {
		t.Fatalf("crcTable = %v, want 256 elements", table.Value)
	}
	for i, e := range v.Elems {
		got, _ := constant.Uint64Val(e.(constant.Value))
		if want := uint64(crc32.IEEETable[i]); got != want {
			t.Errorf("crcTable[%d] = %#x, want %#x", i, got, want)
		}
	}
}
