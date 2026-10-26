package dos_test

import (
	"path/filepath"
	"strings"
	"testing"

	"an/arch/desc"
	"an/build"
	_ "an/codegen"
	"an/emu/dos"
)

// run builds the example project in dir for dos-com and runs it in the
// emulator.
func run(t *testing.T, dir string) (string, *dos.Machine) {
	t.Helper()
	p, errs := build.Load(filepath.Join("..", "..", "examples", dir))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	prog, errs := p.BackendProgram("dos-com")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	tg, _ := build.LookupTarget("dos-com")
	d, _ := tg.Description()
	image, errs := build.Backend(prog, d)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	a, errs := desc.ForName("i8086")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	m, err := dos.Load(a, image)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	m.Stdout, m.Stderr, m.MaxSteps = &out, &out, 10_000_000
	if status, err := m.Run(); err != nil || status != 0 {
		t.Fatalf("status %d, %v", status, err)
	}
	return out.String(), m
}

// cp866 is the greeting of the examples in code page 866.
var cp866 = []byte{0x8F, 0xE0, 0xA8, 0xA2, 0xA5, 0xE2, 0x20, 0xA8, 0xA7, 0x20}

func TestHelloDOS(t *testing.T) {
	out, _ := run(t, "hello-dos")
	if want := string(cp866) + "Anlang!\r\n"; out != want {
		t.Errorf("output %q, want %q", out, want)
	}
}

// TestVideoMemory runs the video memory example (spec/24-roadmap.md, step
// 14): the text it writes to the screen through a hardware mapping, with
// its attribute.
func TestVideoMemory(t *testing.T) {
	out, m := run(t, "video-dos")
	if !strings.HasSuffix(out, "Anlang!\r\n") {
		t.Errorf("output %q", out)
	}
	for i, c := range []byte("HELLO") {
		ch, attr := m.Cell(10+i, 5)
		if ch != c || attr != 0x1E {
			t.Errorf("cell (%d, 5) is %q with attribute %#x, want %q with 0x1e", 10+i, ch, attr, c)
		}
	}
	if !strings.Contains(m.Screen(), "          HELLO") {
		t.Errorf("screen:\n%s", m.Screen())
	}
}
