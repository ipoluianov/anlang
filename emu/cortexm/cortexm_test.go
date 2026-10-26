package cortexm_test

import (
	"path/filepath"
	"strings"
	"testing"

	"an/arch/desc"
	"an/build"
	_ "an/codegen"
	"an/emu/cortexm"
)

// build builds an example project for stm32f103c8.
func build_(t *testing.T, dir string) (*desc.Arch, *build.TargetDesc, []byte) {
	t.Helper()
	p, errs := build.Load(filepath.Join("..", "..", "examples", dir))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	prog, errs := p.BackendProgram("stm32f103c8")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	tg, _ := build.LookupTarget("stm32f103c8")
	d, _ := tg.Description()
	image, errs := build.Backend(prog, d)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	a, errs := desc.ForName("armv7m")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return a, d, image.Bytes
}

// TestBlink runs the example blink-stm32 in the model for 3.2 seconds
// (spec/24-roadmap.md, step 16): pin PC13 changes every half second, and
// the LED blinks once a second.
func TestBlink(t *testing.T) {
	a, d, image := build_(t, "blink-stm32")
	m, err := cortexm.Load(a, d, image)
	if err != nil {
		t.Fatal(err)
	}
	m.MaxTime, m.MaxSteps = 3_200_000_000_000, 1_000_000
	if _, err := m.Run(); err != nil || !m.TimeUp {
		t.Fatalf("%v; the model ran for %d ps", err, m.Time())
	}
	pins := m.Pins()
	t.Logf("%d instructions\n%s", m.Steps, m.PinText())
	if len(pins) != 7 {
		t.Fatalf("%d changes of the pins, want 7", len(pins))
	}
	for i, e := range pins {
		if e.Port != 'C' || e.Pin != 13 || e.High != (i%2 == 0) {
			t.Errorf("change %d: P%c%d high %v", i, e.Port, e.Pin, e.High)
		}
		if i < 2 {
			continue
		}
		// Half a second between changes, within the instructions after a
		// wakeup.
		if dt := e.Time - pins[i-1].Time; dt < 499_990_000_000 || dt > 500_010_000_000 {
			t.Errorf("change %d is %d ps after the previous one, want half a second", i, dt)
		}
	}
}

// TestInterrupts runs the example irq-stm32 (spec/24-roadmap.md, step
// 17): the handler of TIM2 counts, and main sets the LED from the count.
func TestInterrupts(t *testing.T) {
	a, d, image := build_(t, "irq-stm32")
	m, err := cortexm.Load(a, d, image)
	if err != nil {
		t.Fatal(err)
	}
	m.MaxTime, m.MaxSteps = 2_200_000_000_000, 1_000_000
	if _, err := m.Run(); err != nil || !m.TimeUp {
		t.Fatalf("%v; the model ran for %d ps", err, m.Time())
	}
	pins := m.Pins()
	if len(pins) != 5 {
		t.Fatalf("%d changes of the pins, want 5:\n%s", len(pins), m.PinText())
	}
	for i := 2; i < len(pins); i++ {
		if dt := pins[i].Time - pins[i-1].Time; dt < 499_990_000_000 || dt > 500_010_000_000 {
			t.Errorf("change %d is %d ps after the previous one, want half a second", i, dt)
		}
	}
}

// TestHelloSTM32 builds the example hello-stm32 for stm32f103c8 and runs
// it in the emulator: its output goes through semihosting.
func TestHelloSTM32(t *testing.T) {
	p, errs := build.Load(filepath.Join("..", "..", "examples", "hello-stm32"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	prog, errs := p.BackendProgram("stm32f103c8")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	tg, _ := build.LookupTarget("stm32f103c8")
	d, _ := tg.Description()
	image, errs := build.Backend(prog, d)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	a, errs := desc.ForName("armv7m")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	m, err := cortexm.Load(a, d, image.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	m.Stdout, m.Stderr, m.MaxSteps = &out, &out, 10_000_000
	if status, err := m.Run(); err != nil || status != 0 {
		t.Fatalf("status %d, %v", status, err)
	}
	if want := "Hello from Cortex-M!\nfib(40) = 102334155\n"; out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
	if b := image.Bytes; b[0] != 0 || b[3] != 0x20 {
		t.Errorf("the initial stack pointer is % X, want the end of RAM", b[:4])
	}
}
