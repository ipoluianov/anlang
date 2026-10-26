package desc

import (
	"bytes"
	"testing"
)

// TestWasm32Asm checks encodings of the wasm32 description against the
// binary format of WebAssembly.
func TestWasm32Asm(t *testing.T) {
	a, errs := ForName("wasm32")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	for _, c := range []struct {
		text string
		want []byte
	}{
		{"i32.const 0", []byte{0x41, 0x00}},
		{"i32.const 63", []byte{0x41, 0x3F}},
		{"i32.const 64", []byte{0x41, 0xC0, 0x00}},
		{"i32.const -1", []byte{0x41, 0x7F}},
		{"i32.const 4294967295", []byte{0x41, 0x7F}},
		{"i32.const -65", []byte{0x41, 0xBF, 0x7F}},
		{"i32.const 2147483647", []byte{0x41, 0xFF, 0xFF, 0xFF, 0xFF, 0x07}},
		{"i32.const -2147483648", []byte{0x41, 0x80, 0x80, 0x80, 0x80, 0x78}},
		{"i64.const 18446744073709551615", []byte{0x42, 0x7F}},
		{"i64.const -9223372036854775808", []byte{0x42, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7F}},
		{"i64.const 9223372036854775807", []byte{0x42, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x00}},
		{"i32.load 0", []byte{0x28, 0x02, 0x00}},
		{"i32.store8 300", []byte{0x3A, 0x00, 0xAC, 0x02}},
		{"call 4294967295", []byte{0x10, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F}},
		{"global.get SP", []byte{0x23, 0x00}},
		{"GLOBAL.SET sl", []byte{0x24, 0x01}},
		{"br_if 2", []byte{0x0D, 0x02}},
		{"memory.copy", []byte{0xFC, 0x0A, 0x00, 0x00}},
		{"call_indirect", []byte{0x11, 0x00, 0x00}},
	} {
		got, err := a.Asm(c.text)
		if err != nil {
			t.Errorf("%s: %v", c.text, err)
			continue
		}
		if !bytes.Equal(got, c.want) {
			t.Errorf("%s: % X, want % X", c.text, got, c.want)
		}
	}
}
