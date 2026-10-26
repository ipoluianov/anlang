package build

import (
	"strings"
	"testing"
)

// TestDeviceSTM32 reads the device description of stm32f103c8 from its
// SVD file at compile time (emb.device.source) and generates an/device
// from it (emb.device.package).
func TestDeviceSTM32(t *testing.T) {
	tg, _ := LookupTarget("stm32f103c8")
	d, errs := tg.Description()
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	dev := &d.Device
	if dev.Name != "STM32F103" {
		t.Fatalf("device %q", dev.Name)
	}
	byName := map[string]*Peripheral{}
	for i := range dev.Peripherals {
		byName[dev.Peripherals[i].Name] = &dev.Peripherals[i]
	}
	gpioc := byName["GPIOC"]
	if gpioc == nil || gpioc.Base != 0x40011000 || gpioc.Group != "GPIO" || len(gpioc.Registers) != 7 {
		t.Fatalf("GPIOC: %+v", gpioc)
	}
	var crh *Register
	for i := range gpioc.Registers {
		if gpioc.Registers[i].Name == "CRH" {
			crh = &gpioc.Registers[i]
		}
	}
	if crh == nil || crh.Offset != 4 || crh.Reset != 0x44444444 || crh.Size != 32 {
		t.Fatalf("GPIOC.CRH: %+v", crh)
	}
	mode13 := crh.Fields[10]
	if mode13.Name != "MODE13" || mode13.Lsb != 20 || mode13.Width != 2 || len(mode13.Values) != 4 || mode13.Values[2].Name != "Output2MHz" {
		t.Errorf("MODE13: %+v", mode13)
	}
	for i := 1; i < len(dev.Interrupts); i++ {
		if dev.Interrupts[i].Number < dev.Interrupts[i-1].Number {
			t.Errorf("the interrupts are not in order: %v", dev.Interrupts)
		}
	}
	src := string(DevicePackage("stm32f103c8", dev))
	for _, want := range []string{
		"GPIOC  = unsafe.Map[GPIO_Regs](unsafe.Flat(0x40011000))",
		"type GPIO_MODE enum {",
		"func (r GPIO_CRH) WithMODE13(v GPIO_MODE) GPIO_CRH {",
		"InterruptTIM2   Interrupt = 28",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("an/device has no %q", want)
		}
	}
}
