// Package cortexm is the built-in emulator of Cortex-M microcontrollers
// (tool.emu.target): the interpreter generated from the architecture
// description (tool.emu.generated), with the memory regions of the target
// and the semihosting calls of a debugger, through which programs of the
// platform bare write their output and end (spec/20-toolchain.md,
// tool.emu.cortexm).
package cortexm

import (
	"bufio"
	"fmt"
	"io"

	"an/arch/desc"
	"an/build"
)

// Machine is a microcontroller running a program.
type Machine struct {
	M *desc.Machine
	// Stdout and Stderr receive the semihosting console; Stdin is read by
	// SYS_READ.
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	// UART receives the output of the serial ports; nil discards it.
	UART io.Writer
	// MaxSteps bounds the number of executed instructions; 0 means no
	// bound.
	MaxSteps int64
	Steps    int64
	// MaxTime, if not 0, ends the run when the time of the model reaches
	// it, in picoseconds; TimeUp then reports that it did.
	MaxTime uint64
	TimeUp  bool

	in      *bufio.Reader
	dev     *devices
	reset   bool
	vectors uint64         // the address of the vector table
	active  int            // the exception being handled, or 0
	handles map[uint64]int // open files: the stream, 0 to 2
	next    uint64
	exited  bool
	status  int
}

// The semihosting operations (Arm, "Semihosting for AArch32 and AArch64").
const (
	sysOpen         = 0x01
	sysClose        = 0x02
	sysWriteC       = 0x03
	sysWrite0       = 0x04
	sysWrite        = 0x05
	sysRead         = 0x06
	sysIsTTY        = 0x09
	sysExit         = 0x18
	sysExitExtended = 0x20

	applicationExit = 0x20026 // ADP_Stopped_ApplicationExit
)

// Load returns a machine with the memory regions of the target, the
// image at the start of its Exec region, and the registers after reset:
// SP and PC from the first two words of the vector table.
func Load(a *desc.Arch, d *build.TargetDesc, image []byte) (*Machine, error) {
	code, _, ok := d.Regions()
	if !ok {
		return nil, fmt.Errorf("target has no Exec and RAM regions")
	}
	if len(image) > code.Size {
		return nil, fmt.Errorf("the image is %d bytes, more than the %d bytes of region %s", len(image), code.Size, code.Name)
	}
	if len(image) < 8 {
		return nil, fmt.Errorf("the image has no vector table")
	}
	m := desc.NewMachine(a)
	for _, r := range d.Memory {
		mem := m.Map(uint64(r.At), r.Size)
		if r.At == code.At {
			copy(mem, image)
		}
	}
	word := func(i int) uint64 {
		return uint64(image[i]) | uint64(image[i+1])<<8 | uint64(image[i+2])<<16 | uint64(image[i+3])<<24
	}
	m.SetReg("SP", word(0))
	m.SetReg("PC", word(4)&^1)
	c := &Machine{M: m, handles: map[uint64]int{}, next: 1, vectors: uint64(code.At)}
	if d.Device.Name != "" {
		c.dev = newDevices(c, &d.Device)
	}
	return c, nil
}

// Run executes the program until it exits and returns its exit status.
func (c *Machine) Run() (int, error) {
	if c.Stdin != nil {
		c.in = bufio.NewReader(c.Stdin)
	}
	m := c.M
	for !c.exited {
		if c.MaxSteps > 0 && c.Steps >= c.MaxSteps {
			return 0, fmt.Errorf("the program did not end within %d instructions", c.MaxSteps)
		}
		if c.dev != nil && c.MaxTime != 0 && c.dev.now() >= c.MaxTime {
			c.TimeUp = true
			return 0, nil
		}
		pc := m.Reg("PC")
		if pc >= 0xFFFFFFF0 && c.active != 0 {
			c.exceptionReturn()
			continue
		}
		if c.takeException() {
			continue
		}
		switch op := m.Read(pc, 16); {
		case op == 0xBEAB: // BKPT 0xAB
			if err := c.semihost(); err != nil {
				return 0, fmt.Errorf("at %#x: %v", pc, err)
			}
			m.SetReg("PC", pc+2)
		case (op == 0xBF30 || op == 0xBF20) && c.dev != nil: // WFI, WFE
			// Sleep until an interrupt is pending, which wakes the
			// processor even while it is masked.
			if !c.dev.wait(c.MaxTime) {
				if c.MaxTime != 0 {
					c.TimeUp = true
					return 0, nil
				}
				return 0, fmt.Errorf("at %#x: WFI waits for an interrupt that no enabled peripheral raises", pc)
			}
			m.Cycles++
			m.SetReg("PC", pc+2)
		default:
			if err := m.Step(); err != nil {
				return 0, err
			}
		}
		if m.Halted {
			return 0, fmt.Errorf("the processor stopped at %#x", pc)
		}
		if c.reset {
			return 0, fmt.Errorf("at %#x: the program requested a system reset", pc)
		}
		c.Steps++
	}
	return c.status, nil
}

// takeException enters the handler of the pending exception with the
// lowest number, the highest priority, if the interrupts are enabled and
// no exception is being handled: the processor pushes R0 to R3, R12, LR,
// the return address and xPSR, aligning the stack to 8 bytes, sets LR to
// EXC_RETURN and continues at the vector of the exception. Exceptions do
// not preempt each other: they have the same priority.
func (c *Machine) takeException() bool {
	m := c.M
	if c.dev == nil || c.active != 0 || m.Reg("PRIMASK") != 0 {
		return false
	}
	n := c.dev.nextPending()
	if n == 0 {
		return false
	}
	c.dev.clearPending(n)
	sp := m.Reg("SP")
	xpsr := m.Reg("APSR")
	if sp%8 != 0 {
		sp -= 4
		xpsr |= 1 << 9
	}
	sp -= 32
	for i, r := range []string{"R0", "R1", "R2", "R3", "R12", "LR"} {
		m.Write(sp+uint64(4*i), 32, m.Reg(r))
	}
	m.Write(sp+24, 32, m.Reg("PC"))
	m.Write(sp+28, 32, xpsr)
	m.SetReg("SP", sp)
	m.SetReg("LR", 0xFFFFFFF9) // return to thread mode, main stack
	m.SetReg("IPSR", uint64(n))
	m.SetReg("PC", m.Read(c.vectors+uint64(4*n), 32)&^1)
	m.Cycles += 12
	c.active = n
	return true
}

// exceptionReturn returns from the handler of an exception: the handler
// has loaded EXC_RETURN into PC.
func (c *Machine) exceptionReturn() {
	m := c.M
	sp := m.Reg("SP")
	for i, r := range []string{"R0", "R1", "R2", "R3", "R12", "LR"} {
		m.SetReg(r, m.Read(sp+uint64(4*i), 32))
	}
	pc, xpsr := m.Read(sp+24, 32), m.Read(sp+28, 32)
	sp += 32
	if xpsr&(1<<9) != 0 {
		sp += 4
	}
	m.SetReg("SP", sp)
	m.SetReg("PC", pc)
	m.SetReg("APSR", xpsr)
	m.SetReg("IPSR", 0)
	m.Cycles += 12
	c.active = 0
}

// Pins returns the changes of the output pins so far.
func (c *Machine) Pins() []PinEvent {
	if c.dev == nil {
		return nil
	}
	return c.dev.pins
}

// PinText returns the changes of the output pins, one per line: the time
// in seconds, the pin and its level (tool.emu.observable).
func (c *Machine) PinText() string { return formatPins(c.Pins()) }

// Time returns the time of the model in picoseconds.
func (c *Machine) Time() uint64 {
	if c.dev == nil {
		return 0
	}
	return c.dev.now()
}

// uartSend writes a byte that a serial port transmits.
func (c *Machine) uartSend(b byte) {
	if c.UART != nil {
		c.UART.Write([]byte{b})
	}
}

// uartReceive gives a serial port whose receiver is on the next byte of
// the standard input, when it has none.
func (c *Machine) uartReceive(p *periph) {
	if c.in == nil || p.reg("CR1")&(1<<13|1<<2) != 1<<13|1<<2 || p.reg("SR")&(1<<5) != 0 {
		return
	}
	b, err := c.in.ReadByte()
	if err != nil {
		return
	}
	p.setReg("DR", uint64(b))
	p.setReg("SR", p.reg("SR")|1<<5)
}

// stream returns the writer of stream n.
func (c *Machine) stream(n int) io.Writer {
	w := c.Stdout
	if n == 2 {
		w = c.Stderr
	}
	if w == nil {
		w = io.Discard
	}
	return w
}

// semihost performs the semihosting call in R0 with the argument R1.
func (c *Machine) semihost() error {
	m := c.M
	op, arg := m.Reg("R0"), m.Reg("R1")
	field := func(i uint64) uint64 { return m.Read(arg+4*i, 32) }
	bytes := func(addr, n uint64) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(m.Read(addr+uint64(i), 8))
		}
		return b
	}
	switch op {
	case sysOpen:
		name := string(bytes(field(0), field(2)))
		if name != ":tt" {
			m.SetReg("R0", ^uint64(0)&0xFFFFFFFF) // no files
			return nil
		}
		// Modes 0 to 3 read, 4 to 7 write and 8 to 11 append.
		stream := [...]int{0, 1, 2}[min(field(1)/4, 2)]
		h := c.next
		c.next++
		c.handles[h] = stream
		m.SetReg("R0", h)
	case sysClose:
		delete(c.handles, field(0))
		m.SetReg("R0", 0)
	case sysWriteC:
		c.stream(1).Write(bytes(arg, 1))
	case sysWrite0:
		var s []byte
		for a := arg; ; a++ {
			b := byte(m.Read(a, 8))
			if b == 0 {
				break
			}
			s = append(s, b)
		}
		c.stream(1).Write(s)
	case sysWrite:
		stream, ok := c.handles[field(0)]
		if !ok || stream == 0 {
			m.SetReg("R0", field(2))
			return nil
		}
		c.stream(stream).Write(bytes(field(1), field(2)))
		m.SetReg("R0", 0) // the number of bytes not written
	case sysRead:
		n := field(2)
		var got uint64
		if stream, ok := c.handles[field(0)]; ok && stream == 0 && c.in != nil {
			buf := make([]byte, n)
			k, _ := io.ReadFull(c.in, buf)
			for i := 0; i < k; i++ {
				m.Write(field(1)+uint64(i), 8, uint64(buf[i]))
			}
			got = uint64(k)
		}
		m.SetReg("R0", n-got)
	case sysIsTTY:
		m.SetReg("R0", 1)
	case sysExit:
		c.exited, c.status = true, exitStatus(arg, 0)
	case sysExitExtended:
		c.exited, c.status = true, exitStatus(field(0), int(int32(field(1))))
	default:
		return fmt.Errorf("semihosting call %#x is not supported", op)
	}
	return nil
}

// exitStatus returns the exit status of a stop with the given reason:
// code for the exit of the application, 1 for any other.
func exitStatus(reason uint64, code int) int {
	if reason == applicationExit {
		return code
	}
	return 1
}
