package build

import "testing"

// TestLibraryPackages checks the standard library packages that the
// toolchain reads or links: the target descriptions and the runtime.
func TestLibraryPackages(t *testing.T) {
	d, errs := Target{Name: "dos-com"}.Description()
	for _, e := range errs {
		t.Error(e)
	}
	if d != nil {
		if d.Arch.Name != "i8086" || d.Word != 16 || d.PointerBits() != 16 || d.Platform.Name != "dos" || d.OnPanic != 1 {
			t.Errorf("dos-com: %+v", d)
		}
		if len(d.Formats) != 1 || d.Formats[0].Kind() != "flat" || d.Formats[0].Origin != 0x100 || d.Formats[0].MaxSize != 65280 {
			t.Errorf("dos-com formats: %+v", d.Formats)
		}
		if len(d.Provides) == 0 || d.Provides[0] != "Console" || len(d.Lacks) != 1 || d.Lacks[0] != "Network" {
			t.Errorf("dos-com effects: %v, %v", d.Provides, d.Lacks)
		}
	}
	for _, target := range []string{"linux-x86_64", "dos-com"} {
		if _, errs := Library("an/runtime", target); len(errs) > 0 {
			t.Error(target, errs)
		}
	}
}
