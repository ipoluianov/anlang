package cortexm

import (
	"fmt"
	"math/bits"
	"strings"

	"an/arch/desc"
	"an/build"
)

// The peripherals of the device description (spec/20-toolchain.md,
// tool.emu.cortexm): every register of the description holds its value
// from its reset value; the peripherals of the STM32F1 family, the
// SysTick timer and the interrupt controller of the Cortex-M3 also behave
// as the hardware does. The model has a time in picoseconds, which runs
// with the executed cycles at the clock of the processor that RCC selects,
// and which WFI advances to the next event of a peripheral.

const picosecond = 1_000_000_000_000 // per second

// The clocks of the STM32F1: the internal oscillator, and the crystal of
// the board.
const (
	hsiHz = 8_000_000
	hseHz = 8_000_000
)

// PinEvent is a change of an output pin (tool.emu.observable).
type PinEvent struct {
	Time uint64 // picoseconds
	Port byte   // 'A', 'B', …
	Pin  int
	High bool
}

// devices are the peripherals of a machine.
type devices struct {
	c       *Machine
	periphs []*periph
	byName  map[string]*periph
	// The time: the cycles at the start of the current clock segment,
	// the time then, and the clock of the processor since.
	segCycles int64
	segTime   uint64
	hclk      uint64
	pins      []PinEvent
	uart      []byte
	// sysTickPending is the pending state of the SysTick exception.
	sysTickPending bool
}

// periph is a peripheral of the description.
type periph struct {
	d     *devices
	desc  *build.Peripheral
	kind  string // the group, such as "GPIO"
	regs  map[uint64]uint64
	sizes map[uint64]int
	names map[string]uint64 // the offsets of the registers
	// The state of a timer.
	psc, pscCnt uint64 // the prescaler in use and its counter
	last        uint64 // the time of the last update of the counter
	rem         uint64 // ticks of the timer clock in picoseconds, not yet counted
	// SysTick: the time it last counted.
	irq int // the interrupt of the peripheral, or -1
}

func newDevices(c *Machine, d *build.Device) *devices {
	ds := &devices{c: c, byName: map[string]*periph{}, hclk: hsiHz}
	for i := range d.Peripherals {
		p := &d.Peripherals[i]
		if len(p.Registers) == 0 {
			continue
		}
		pe := &periph{d: ds, desc: p, kind: p.Group, regs: map[uint64]uint64{}, sizes: map[uint64]int{}, names: map[string]uint64{}, irq: -1}
		if pe.kind == "" {
			pe.kind = p.Name
		}
		var end uint64
		for _, r := range p.Registers {
			pe.regs[r.Offset] = r.Reset
			pe.sizes[r.Offset] = r.Size
			pe.names[r.Name] = r.Offset
			end = max(end, r.Offset+uint64(r.Size/8))
		}
		for _, irq := range d.Interrupts {
			if irq.Name == p.Name {
				pe.irq = irq.Number
			}
		}
		ds.periphs = append(ds.periphs, pe)
		ds.byName[p.Name] = pe
		c.M.MapIO(p.Base, end, pe)
	}
	return ds
}

// now returns the time of the model.
func (ds *devices) now() uint64 {
	return ds.segTime + mulDiv(uint64(ds.c.M.Cycles-ds.segCycles), picosecond, ds.hclk)
}

// setClock changes the clock of the processor from now on.
func (ds *devices) setClock(hz uint64) {
	if hz == ds.hclk {
		return
	}
	ds.segTime = ds.now()
	ds.segCycles = ds.c.M.Cycles
	ds.hclk = hz
}

// mulDiv returns a*b/c rounded down, without overflow of a*b.
func mulDiv(a, b, c uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	if hi >= c {
		return ^uint64(0)
	}
	q, _ := bits.Div64(hi, lo, c)
	return q
}

// mulDivRem returns a*b/c and the remainder.
func mulDivRem(a, b, c uint64) (uint64, uint64) {
	hi, lo := bits.Mul64(a, b)
	if hi >= c {
		return ^uint64(0), 0
	}
	return bits.Div64(hi, lo, c)
}

func (p *periph) reg(name string) uint64 { return p.regs[p.names[name]] }

func (p *periph) setReg(name string, v uint64) { p.regs[p.names[name]] = v }

// register returns the offset of the register at off and the shift of an
// access to a part of it.
func (p *periph) register(off uint64, write bool) (uint64, int) {
	for start, size := range p.sizes {
		if off >= start && off < start+uint64(size/8) {
			return start, int(off-start) * 8
		}
	}
	what := "read"
	if write {
		what = "write"
	}
	desc.Fault("%s of %s at offset %#x: no register", what, p.desc.Name, off)
	return 0, 0
}

// Read reads a register (desc.IO).
func (p *periph) Read(off uint64, width int) uint64 {
	start, shift := p.register(off, false)
	p.before(start, false)
	v := p.regs[start]
	if p.kind == "STK" && start == p.names["CTRL"] {
		p.regs[start] &^= 1 << 16 // COUNTFLAG clears on a read
	}
	if p.kind == "USART" && start == p.names["DR"] {
		p.setReg("SR", p.reg("SR")&^(1<<5)) // RXNE
	}
	return v >> uint(shift)
}

// Write writes a register (desc.IO).
func (p *periph) Write(off uint64, width int, v uint64) {
	start, shift := p.register(off, true)
	p.before(start, true)
	mask := uint64(1)<<uint(width) - 1
	old := p.regs[start]
	nv := old&^(mask<<uint(shift)) | (v&mask)<<uint(shift)
	p.write(start, old, nv)
}

// before brings the state of a register up to the time of an access.
func (p *periph) before(off uint64, write bool) {
	switch p.kind {
	case "TIM":
		p.timerAdvance()
	case "STK":
		p.d.sysTickAdvance(p)
	case "USART":
		if off == p.names["SR"] && !write {
			p.d.c.uartReceive(p)
		}
	}
}

// write stores the value nv of a register that held old, with the
// behavior of the hardware.
func (p *periph) write(off, old, nv uint64) {
	ds := p.d
	name := ""
	for n, o := range p.names {
		if o == off {
			name = n
		}
	}
	switch p.kind + "." + name {
	case "RCC.CR":
		// The oscillators and the PLL are ready as soon as they are on.
		nv = nv&^(1<<1|1<<17|1<<25) | nv&1<<1 | nv>>16&1<<17 | nv>>24&1<<25
		p.regs[off] = nv
		ds.updateClock()
		return
	case "RCC.CFGR":
		nv = nv&^(3<<2) | nv&3<<2 // SWS follows SW
		p.regs[off] = nv
		ds.updateClock()
		return
	case "GPIO.ODR":
		p.regs[off] = nv & 0xFFFF
		ds.pinChanges(p, old)
		return
	case "GPIO.BSRR":
		odr := p.reg("ODR")
		p.setReg("ODR", (odr&^(nv>>16)|nv)&0xFFFF)
		ds.pinChanges(p, odr)
		return
	case "GPIO.BRR":
		odr := p.reg("ODR")
		p.setReg("ODR", odr&^nv&0xFFFF)
		ds.pinChanges(p, odr)
		return
	case "GPIO.IDR":
		return // read-only
	case "TIM.EGR":
		if nv&1 != 0 {
			p.timerUpdate(true)
		}
		return
	case "TIM.SR":
		p.regs[off] = old & nv // bits are cleared by writing 0
		return
	case "TIM.CR1":
		p.regs[off] = nv
		if old&1 == 0 && nv&1 != 0 {
			p.last = ds.now()
			p.rem = 0
		}
		return
	case "USART.DR":
		if p.reg("CR1")&(1<<13|1<<3) == 1<<13|1<<3 { // UE and TE
			ds.uart = append(ds.uart, byte(nv))
			ds.c.uartSend(byte(nv))
		}
		return
	case "USART.SR":
		p.regs[off] = old & (nv | ^uint64(1<<5|1<<6)) // RXNE and TC are cleared by writing 0
		return
	case "STK.VAL":
		p.regs[off] = 0
		p.setReg("CTRL", p.reg("CTRL")&^(1<<16))
		return
	case "STK.CTRL":
		p.regs[off] = nv&^(1<<16) | old&(1<<16)
		p.last = ds.now()
		return
	case "NVIC.ISER0", "NVIC.ISER1":
		p.regs[off] = old | nv
		p.mirror(name[4:], old|nv)
		return
	case "NVIC.ICER0", "NVIC.ICER1":
		k := name[4:]
		p.setReg("ISER"+k, p.reg("ISER"+k)&^nv)
		p.setReg("ICER"+k, p.reg("ISER"+k))
		return
	case "NVIC.ISPR0", "NVIC.ISPR1":
		k := name[4:]
		p.setReg("ISPR"+k, p.reg("ISPR"+k)|nv)
		p.setReg("ICPR"+k, p.reg("ISPR"+k))
		return
	case "NVIC.ICPR0", "NVIC.ICPR1":
		k := name[4:]
		p.setReg("ISPR"+k, p.reg("ISPR"+k)&^nv)
		p.setReg("ICPR"+k, p.reg("ISPR"+k))
		return
	case "SCB.ICSR":
		if nv&(1<<25) != 0 {
			ds.sysTickPending = false // PENDSTCLR
		}
		return
	case "SCB.AIRCR":
		if nv>>16 == 0x05FA && nv&(1<<2) != 0 {
			ds.c.reset = true
		}
		p.regs[off] = old // reads back with its key
		return
	}
	p.regs[off] = nv
}

// mirror makes the set and clear registers of word k read the same.
func (p *periph) mirror(k string, v uint64) {
	p.setReg("ISER"+k, v)
	p.setReg("ICER"+k, v)
}

// pending sets interrupt n pending.
func (ds *devices) pending(n int) {
	if nvic := ds.byName["NVIC"]; nvic != nil && n >= 0 {
		k := fmt.Sprint(n / 32)
		v := nvic.reg("ISPR"+k) | 1<<uint(n%32)
		nvic.setReg("ISPR"+k, v)
		nvic.setReg("ICPR"+k, v)
	}
}

// pendingEnabled reports whether an enabled interrupt is pending.
func (ds *devices) pendingEnabled() bool {
	nvic := ds.byName["NVIC"]
	if nvic == nil {
		return false
	}
	for _, k := range []string{"0", "1"} {
		if nvic.reg("ISPR"+k)&nvic.reg("ISER"+k) != 0 {
			return true
		}
	}
	return false
}

// nextPending returns the number of the pending exception with the
// lowest number, which an enabled interrupt or SysTick has: 15 for
// SysTick, 16 plus its number for an interrupt; 0 if none is pending.
func (ds *devices) nextPending() int {
	if ds.sysTickPending {
		return 15
	}
	nvic := ds.byName["NVIC"]
	if nvic == nil {
		return 0
	}
	for k := 0; k < 2; k++ {
		ks := fmt.Sprint(k)
		if p := nvic.reg("ISPR"+ks) & nvic.reg("ISER"+ks); p != 0 {
			return 16 + 32*k + bits.TrailingZeros64(p)
		}
	}
	return 0
}

// clearPending clears the pending state of exception n, which the
// processor enters.
func (ds *devices) clearPending(n int) {
	if n == 15 {
		ds.sysTickPending = false
		return
	}
	if nvic := ds.byName["NVIC"]; nvic != nil {
		k := fmt.Sprint((n - 16) / 32)
		v := nvic.reg("ISPR"+k) &^ (1 << uint((n-16)%32))
		nvic.setReg("ISPR"+k, v)
		nvic.setReg("ICPR"+k, v)
	}
}

// enabled reports whether interrupt n is enabled.
func (ds *devices) enabled(n int) bool {
	nvic := ds.byName["NVIC"]
	return nvic != nil && n >= 0 && nvic.reg("ISER"+fmt.Sprint(n/32))>>uint(n%32)&1 != 0
}

// updateClock computes the clock of the processor from RCC.
func (ds *devices) updateClock() {
	ds.setClock(ds.sysclk() >> ds.ahbShift())
}

func (ds *devices) sysclk() uint64 {
	rcc := ds.byName["RCC"]
	if rcc == nil {
		return hsiHz
	}
	cfgr := rcc.reg("CFGR")
	switch cfgr & 3 {
	case 1:
		return hseHz
	case 2:
		in := uint64(hsiHz / 2)
		if cfgr>>16&1 != 0 {
			in = hseHz
			if cfgr>>17&1 != 0 {
				in /= 2
			}
		}
		mul := cfgr>>18&0xF + 2
		if mul > 16 {
			mul = 16
		}
		return in * mul
	}
	return hsiHz
}

// ahbShift returns the AHB prescaler as a shift.
func (ds *devices) ahbShift() uint {
	rcc := ds.byName["RCC"]
	if rcc == nil {
		return 0
	}
	hpre := rcc.reg("CFGR") >> 4 & 0xF
	switch {
	case hpre < 8:
		return 0
	case hpre < 12:
		return uint(hpre - 7)
	}
	return uint(hpre - 6)
}

// timerClock returns the clock of the timers on APB1: the bus clock, or
// twice it if the bus is divided.
func (ds *devices) timerClock() uint64 {
	rcc := ds.byName["RCC"]
	hclk := ds.hclk
	if rcc == nil {
		return hclk
	}
	ppre := rcc.reg("CFGR") >> 8 & 7
	if ppre < 4 {
		return hclk
	}
	return hclk >> (ppre - 3) * 2
}

// pinChanges records the changes of the output pins of a GPIO port whose
// output data register held old.
func (ds *devices) pinChanges(p *periph, old uint64) {
	odr := p.reg("ODR")
	port := p.desc.Name[len(p.desc.Name)-1]
	for pin := 0; pin < 16; pin++ {
		if (old^odr)>>uint(pin)&1 == 0 {
			continue
		}
		cr := p.reg("CRL")
		if pin >= 8 {
			cr = p.reg("CRH")
		}
		if cr>>uint(4*(pin%8))&3 == 0 {
			continue // an input
		}
		ds.pins = append(ds.pins, PinEvent{Time: ds.now(), Port: port, Pin: pin, High: odr>>uint(pin)&1 != 0})
	}
}

// timerAdvance counts a timer up to now.
func (p *periph) timerAdvance() {
	ds := p.d
	now := ds.now()
	if p.reg("CR1")&1 == 0 {
		p.last = now
		return
	}
	ticks, rem := mulDivRem(now-p.last, ds.timerClock(), picosecond)
	rem += p.rem
	ticks += rem / picosecond
	p.rem = rem % picosecond
	p.last = now
	p.timerCount(ticks)
}

// timerCount counts ticks of the timer clock.
func (p *periph) timerCount(ticks uint64) {
	step := p.psc + 1
	total := p.pscCnt + ticks
	incs := total / step
	p.pscCnt = total % step
	cnt, arr := p.reg("CNT"), p.reg("ARR")
	for incs > 0 {
		left := arr - cnt + 1 // increments to the update event
		if arr < cnt {
			left = 0x10000 - cnt + arr + 1
		}
		if incs < left {
			cnt += incs
			break
		}
		incs -= left
		cnt = 0
		p.timerUpdate(false)
	}
	p.setReg("CNT", cnt&0xFFFF)
}

// timerUpdate is an update event of a timer: by UG, or by the counter
// reaching ARR.
func (p *periph) timerUpdate(ug bool) {
	p.psc = p.reg("PSC")
	if ug {
		p.setReg("CNT", 0)
		p.pscCnt = 0
		if p.reg("CR1")&(1<<2) != 0 {
			return // URS: UG sets no flag
		}
	}
	p.setReg("SR", p.reg("SR")|1) // UIF
	if p.reg("DIER")&1 != 0 {
		p.d.pending(p.irq)
	}
}

// timerNext returns the time of the next update event of a timer whose
// interrupt is enabled, or 0.
func (p *periph) timerNext() uint64 {
	if p.reg("CR1")&1 == 0 || p.reg("DIER")&1 == 0 || !p.d.enabled(p.irq) {
		return 0
	}
	p.timerAdvance()
	cnt, arr := p.reg("CNT"), p.reg("ARR")
	left := arr - cnt + 1
	if arr < cnt {
		left = 0x10000 - cnt + arr + 1
	}
	ticks := left*(p.psc+1) - p.pscCnt
	return p.last + ticksTime(ticks, p.rem, p.d.timerClock())
}

// ticksTime returns the picoseconds, rounded up, in which a clock of clk
// Hz makes ticks ticks after a part rem/10^12 of a tick has passed.
func ticksTime(ticks, rem, clk uint64) uint64 {
	hi, lo := bits.Mul64(ticks, picosecond)
	lo, borrow := bits.Sub64(lo, rem, 0)
	hi -= borrow
	if hi >= clk {
		return ^uint64(0) >> 1
	}
	q, r := bits.Div64(hi, lo, clk)
	if r != 0 {
		q++
	}
	return q
}

// sysTickAdvance counts SysTick down to now.
func (ds *devices) sysTickAdvance(p *periph) {
	now := ds.now()
	ctrl := p.reg("CTRL")
	if ctrl&1 == 0 {
		p.last = now
		return
	}
	clk := ds.hclk
	if ctrl&4 == 0 {
		clk /= 8
	}
	ticks, rem := mulDivRem(now-p.last, clk, picosecond)
	rem += p.rem
	ticks += rem / picosecond
	p.rem = rem % picosecond
	p.last = now
	val, load := p.reg("VAL"), p.reg("LOAD")&0xFFFFFF
	for ticks > 0 && load != 0 {
		if val == 0 {
			// A tick at 0 reloads the counter.
			ticks--
			val = load
			continue
		}
		if ticks < val {
			val -= ticks
			break
		}
		// Counting down to 0 sets COUNTFLAG.
		ticks -= val
		val = 0
		p.setReg("CTRL", p.reg("CTRL")|1<<16)
		if ctrl&2 != 0 {
			ds.sysTickPending = true
		}
	}
	p.setReg("VAL", val)
}

// sysTickNext returns the time SysTick next counts to 0 with its
// exception enabled, or 0.
func (ds *devices) sysTickNext() uint64 {
	p := ds.byName["STK"]
	if p == nil || p.reg("CTRL")&3 != 3 {
		return 0
	}
	ds.sysTickAdvance(p)
	clk := ds.hclk
	if p.reg("CTRL")&4 == 0 {
		clk /= 8
	}
	val := p.reg("VAL")
	if val == 0 {
		val = p.reg("LOAD")&0xFFFFFF + 1 // a reload, then the count down
	}
	return p.last + ticksTime(val, p.rem, clk)
}

// wait advances the time to the next event that sets an enabled
// interrupt pending, for WFI. It returns false if there is none before
// limit (0 for none).
func (ds *devices) wait(limit uint64) bool {
	if ds.pendingEnabled() || ds.sysTickPending {
		return true
	}
	var next uint64
	for _, p := range ds.periphs {
		if p.kind == "TIM" {
			if t := p.timerNext(); t != 0 && (next == 0 || t < next) {
				next = t
			}
		}
	}
	if t := ds.sysTickNext(); t != 0 && (next == 0 || t < next) {
		next = t
	}
	if next == 0 || limit != 0 && next > limit {
		if limit != 0 {
			ds.advanceTo(limit)
		}
		return false
	}
	ds.advanceTo(next)
	for _, p := range ds.periphs {
		switch p.kind {
		case "TIM":
			p.timerAdvance()
		case "STK":
			ds.sysTickAdvance(p)
		}
	}
	return true
}

// advanceTo moves the time forward to t.
func (ds *devices) advanceTo(t uint64) {
	if now := ds.now(); t > now {
		ds.segTime += t - now
	}
}

// formatPins writes the pin changes, one per line: the time in seconds,
// the pin and its level.
func formatPins(events []PinEvent) string {
	var b strings.Builder
	for _, e := range events {
		level := "low"
		if e.High {
			level = "high"
		}
		fmt.Fprintf(&b, "%d.%09d s P%c%d %s\n", e.Time/picosecond, e.Time%picosecond/1000, e.Port, e.Pin, level)
	}
	return b.String()
}
