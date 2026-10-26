// Package dos is the built-in emulator of the dos targets (tool.emu.dos):
// the 8086 interpreter generated from the architecture description with
// models of the DOS and BIOS services that programs reach through
// interrupts, of the text video memory, of the keyboard and of the timer.
//
// Limits (tool.emu.limits): only the interrupts and functions listed in
// the specification exist, and other ones stop the emulation with an
// error; the screen is the 80×25 text mode; there are no hardware
// interrupts; time follows the executed clock cycles of a 4.77 MHz 8086,
// from midnight of 1 January 2026.
package dos

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"an/arch/desc"
)

// The segment of the program, the segment of the interrupt handlers and
// the memory of the text screen.
const (
	progSeg    = 0x0700
	handlerSeg = 0xF000
	videoBase  = 0xB8000
	cols, rows = 80, 25
	clockHz    = 4772727
	cyclesTick = 262144 // 18.2065 timer ticks per second
	biosCursor = 0x450  // column and row of the cursor of page 0
	biosTicks  = 0x46C  // the timer tick count
)

// Machine is an 8086 running a .COM program under the model of DOS.
type Machine struct {
	M      *desc.Machine
	Stdout io.Writer
	Stderr io.Writer
	// Stdin is the keyboard: each byte is a key press, a line feed the
	// Enter key. A nil Stdin has no keys.
	Stdin io.Reader
	// MaxSteps bounds the number of executed instructions; 0 means no
	// bound.
	MaxSteps int64
	Steps    int64

	keys   *bufio.Reader
	exited bool
	status int
}

// Load returns a machine with the .COM image loaded at offset 0x100 of
// the program segment, after a program segment prefix whose first bytes
// are int 20h, and a clear screen.
func Load(a *desc.Arch, image []byte) (*Machine, error) {
	if len(image) > 0xFF00-2 {
		return nil, fmt.Errorf("the .COM image is %d bytes, more than 65280", len(image))
	}
	m := desc.NewMachine(a)
	base := progSeg << 4
	m.Mem[base] = 0xCD // int 20h
	m.Mem[base+1] = 0x20
	copy(m.Mem[base+0x100:], image)
	// Interrupt vectors: interrupt n is handled at F000:n by the model,
	// which then executes the IRET there.
	for n := 0; n < 256; n++ {
		m.Mem[4*n] = byte(n)
		m.Mem[4*n+1] = 0
		m.Mem[4*n+2] = byte(handlerSeg & 0xFF)
		m.Mem[4*n+3] = byte(handlerSeg >> 8)
		m.Mem[handlerSeg<<4+n] = 0xCF // IRET
	}
	for _, r := range []string{"CS", "DS", "ES", "SS"} {
		m.SetReg(r, progSeg)
	}
	m.SetReg("IP", 0x100)
	m.SetReg("SP", 0xFFFE) // the word at FFFE is 0: RET goes to int 20h
	d := &Machine{M: m}
	d.clear(0, 0, cols-1, rows-1, 0x07)
	return d, nil
}

// Run executes the program until it exits and returns its exit status.
func (d *Machine) Run() (int, error) {
	if d.Stdin != nil {
		d.keys = bufio.NewReader(d.Stdin)
	}
	for !d.exited {
		if d.MaxSteps > 0 && d.Steps >= d.MaxSteps {
			return 0, fmt.Errorf("the program did not end within %d instructions", d.MaxSteps)
		}
		pc := d.M.FetchAddr()
		if pc >= handlerSeg<<4 && pc < handlerSeg<<4+256 {
			if err := d.interrupt(int(pc - handlerSeg<<4)); err != nil {
				return 0, err
			}
			if d.exited {
				break
			}
		}
		if err := d.M.Step(); err != nil {
			return 0, err
		}
		d.Steps++
	}
	return d.status, nil
}

// Screen returns the characters of the text screen, one string per row,
// without trailing spaces and without trailing empty rows
// (tool.emu.observable).
func (d *Machine) Screen() string {
	var lines []string
	for y := 0; y < rows; y++ {
		var b strings.Builder
		for x := 0; x < cols; x++ {
			c, _ := d.Cell(x, y)
			b.WriteByte(c)
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// Cell returns the character and the attribute at column x of row y of
// the screen.
func (d *Machine) Cell(x, y int) (byte, byte) {
	i := videoBase + 2*(y*cols+x)
	return d.M.Mem[i], d.M.Mem[i+1]
}

func (d *Machine) setCell(x, y int, c, attr byte) {
	i := videoBase + 2*(y*cols+x)
	d.M.Mem[i], d.M.Mem[i+1] = c, attr
}

func (d *Machine) cursor() (int, int) {
	return int(d.M.Mem[biosCursor]), int(d.M.Mem[biosCursor+1])
}

func (d *Machine) setCursor(x, y int) {
	d.M.Mem[biosCursor], d.M.Mem[biosCursor+1] = byte(x), byte(y)
}

// clear fills a window of the screen with spaces of attribute attr.
func (d *Machine) clear(x0, y0, x1, y1 int, attr byte) {
	for y := y0; y <= y1 && y < rows; y++ {
		for x := x0; x <= x1 && x < cols; x++ {
			d.setCell(x, y, ' ', attr)
		}
	}
}

// scroll moves the rows of a window up by n rows (down if n < 0); n = 0
// clears the window.
func (d *Machine) scroll(x0, y0, x1, y1, n int, attr byte) {
	h := y1 - y0 + 1
	if n == 0 || n >= h || -n >= h {
		d.clear(x0, y0, x1, y1, attr)
		return
	}
	move := func(y int) {
		for x := x0; x <= x1; x++ {
			c, a := d.Cell(x, y+n)
			d.setCell(x, y, c, a)
		}
	}
	if n > 0 {
		for y := y0; y <= y1-n; y++ {
			move(y)
		}
		d.clear(x0, y1-n+1, x1, y1, attr)
	} else {
		for y := y1; y >= y0-n; y-- {
			move(y)
		}
		d.clear(x0, y0, x1, y0-n-1, attr)
	}
}

// teletype writes a character at the cursor as int 10h, function 0Eh
// does: control characters move the cursor, and the screen scrolls.
func (d *Machine) teletype(c byte) {
	x, y := d.cursor()
	switch c {
	case 7: // bell
	case 8:
		if x > 0 {
			x--
		}
	case 10:
		y++
	case 13:
		x = 0
	default:
		_, attr := d.Cell(x, y)
		d.setCell(x, y, c, attr)
		x++
		if x == cols {
			x, y = 0, y+1
		}
	}
	if y == rows {
		d.scroll(0, 0, cols-1, rows-1, 1, 0x07)
		y = rows - 1
	}
	d.setCursor(x, y)
}

// console writes bytes to the standard output or standard error of the
// host and to the screen, as DOS writes to the console.
func (d *Machine) console(w io.Writer, b []byte) error {
	for _, c := range b {
		d.teletype(c)
	}
	if w == nil {
		return nil
	}
	_, err := w.Write(b)
	return err
}

// key returns the next key press: the ASCII code and the scan code;
// ok is false when the input has ended.
func (d *Machine) key(peek bool) (ascii, scan byte, ok bool) {
	if d.keys == nil {
		return 0, 0, false
	}
	var c byte
	if peek {
		b, err := d.keys.Peek(1)
		if err != nil {
			return 0, 0, false
		}
		c = b[0]
	} else {
		var err error
		if c, err = d.keys.ReadByte(); err != nil {
			return 0, 0, false
		}
	}
	if c == '\n' {
		c = '\r'
	}
	return c, scanCodes[c], true
}

// scanCodes are the scan codes of the keys of ASCII characters on a US
// keyboard, for int 16h.
var scanCodes = func() [256]byte {
	var t [256]byte
	rowsOfKeys := []struct {
		keys  string
		first byte
	}{{"1234567890-=", 0x02}, {"qwertyuiop[]", 0x10}, {"asdfghjkl;'`", 0x1E}, {"\\zxcvbnm,./", 0x2B}}
	for _, r := range rowsOfKeys {
		for i := 0; i < len(r.keys); i++ {
			t[r.keys[i]] = r.first + byte(i)
			if c := r.keys[i]; c >= 'a' && c <= 'z' {
				t[c-'a'+'A'] = r.first + byte(i)
			}
		}
	}
	t['\r'], t[' '], t[27], t[8], t['\t'] = 0x1C, 0x39, 0x01, 0x0E, 0x0F
	return t
}()

// setFlag sets bit of the flags that the IRET of the handler restores.
func (d *Machine) setFlag(bit uint, on bool) {
	m := d.M
	addr := m.Reg("SS")<<4 + (m.Reg("SP")+4)&0xFFFF
	f := uint16(m.Mem[addr]) | uint16(m.Mem[addr+1])<<8
	if on {
		f |= 1 << bit
	} else {
		f &^= 1 << bit
	}
	m.Mem[addr], m.Mem[addr+1] = byte(f), byte(f>>8)
}

const (
	flagCF = 0
	flagZF = 6
)

// ticks returns the timer ticks since midnight.
func (d *Machine) ticks() uint64 { return uint64(d.M.Cycles) / cyclesTick }

// interrupt performs the service of interrupt n.
func (d *Machine) interrupt(n int) error {
	switch n {
	case 0x10:
		return d.video()
	case 0x16:
		return d.keyboard()
	case 0x1A:
		return d.timer()
	case 0x20:
		d.exited, d.status = true, 0
		return nil
	case 0x21:
		return d.dos()
	}
	return fmt.Errorf("interrupt %02Xh is not modeled (tool.emu.dos)", n)
}

// video performs the BIOS video services of int 10h.
func (d *Machine) video() error {
	m := d.M
	ah := m.Reg("AH")
	switch ah {
	case 0x00: // set mode: the text modes clear the screen
		d.clear(0, 0, cols-1, rows-1, 0x07)
		d.setCursor(0, 0)
	case 0x01: // cursor shape
	case 0x02:
		d.setCursor(int(m.Reg("DL")), int(m.Reg("DH")))
	case 0x03:
		x, y := d.cursor()
		m.SetReg("DL", uint64(x))
		m.SetReg("DH", uint64(y))
		m.SetReg("CX", 0x0607)
	case 0x06, 0x07:
		lines := int(m.Reg("AL"))
		if ah == 0x07 {
			lines = -lines
		}
		d.scroll(int(m.Reg("CL")), int(m.Reg("CH")), int(m.Reg("DL")), int(m.Reg("DH")), lines, byte(m.Reg("BH")))
	case 0x08:
		x, y := d.cursor()
		c, a := d.Cell(x, y)
		m.SetReg("AX", uint64(a)<<8|uint64(c))
	case 0x09, 0x0A:
		x, y := d.cursor()
		c := byte(m.Reg("AL"))
		for i := 0; i < int(m.Reg("CX")) && y*cols+x+i < rows*cols; i++ {
			p := y*cols + x + i
			_, a := d.Cell(p%cols, p/cols)
			if ah == 0x09 {
				a = byte(m.Reg("BL"))
			}
			d.setCell(p%cols, p/cols, c, a)
		}
	case 0x0E:
		d.teletype(byte(m.Reg("AL")))
	case 0x0F:
		m.SetReg("AX", cols<<8|3)
		m.SetReg("BH", 0)
	default:
		return fmt.Errorf("int 10h function %02Xh is not modeled (tool.emu.dos)", ah)
	}
	return nil
}

// keyboard performs the BIOS keyboard services of int 16h.
func (d *Machine) keyboard() error {
	m := d.M
	switch ah := m.Reg("AH"); ah {
	case 0x00, 0x10:
		c, s, ok := d.key(false)
		if !ok {
			return fmt.Errorf("int 16h: the program waits for a key, and the keyboard input has ended")
		}
		m.SetReg("AX", uint64(s)<<8|uint64(c))
	case 0x01, 0x11:
		c, s, ok := d.key(true)
		d.setFlag(flagZF, !ok)
		if ok {
			m.SetReg("AX", uint64(s)<<8|uint64(c))
		}
	case 0x02:
		m.SetReg("AL", 0)
	default:
		return fmt.Errorf("int 16h function %02Xh is not modeled (tool.emu.dos)", ah)
	}
	return nil
}

// timer performs the BIOS time services of int 1Ah.
func (d *Machine) timer() error {
	m := d.M
	switch ah := m.Reg("AH"); ah {
	case 0x00:
		t := d.ticks()
		m.SetReg("CX", t>>16&0xFFFF)
		m.SetReg("DX", t&0xFFFF)
		m.SetReg("AL", 0)
		m.Write(biosTicks, 32, t)
	default:
		return fmt.Errorf("int 1Ah function %02Xh is not modeled (tool.emu.dos)", ah)
	}
	return nil
}

// dos performs the DOS services of int 21h.
func (d *Machine) dos() error {
	m := d.M
	ah := m.Reg("AH")
	switch ah {
	case 0x01, 0x07, 0x08:
		c, _, ok := d.key(false)
		if !ok {
			c = 0x1A // the end of input reads as Ctrl-Z
		}
		m.SetReg("AL", uint64(c))
		if ah == 0x01 && ok {
			return d.console(d.Stdout, []byte{c})
		}
	case 0x02:
		return d.console(d.Stdout, []byte{byte(m.Reg("DL"))})
	case 0x06:
		if dl := byte(m.Reg("DL")); dl != 0xFF {
			return d.console(d.Stdout, []byte{dl})
		}
		c, _, ok := d.key(false)
		d.setFlag(flagZF, !ok)
		m.SetReg("AL", uint64(c))
	case 0x09:
		addr := m.Reg("DS")<<4 + m.Reg("DX")
		var s []byte
		for i := uint64(0); i < 0x10000; i++ {
			c := m.Mem[(addr+i)&0xFFFFF]
			if c == '$' {
				break
			}
			s = append(s, c)
		}
		return d.console(d.Stdout, s)
	case 0x0B:
		_, _, ok := d.key(true)
		if ok {
			m.SetReg("AL", 0xFF)
		} else {
			m.SetReg("AL", 0)
		}
	case 0x2A: // date: 1 January 2026, a Thursday
		m.SetReg("CX", 2026)
		m.SetReg("DX", 1<<8|1)
		m.SetReg("AL", 4)
	case 0x2C:
		hund := uint64(d.M.Cycles) * 100 / clockHz
		s := hund / 100
		m.SetReg("CH", s/3600%24)
		m.SetReg("CL", s/60%60)
		m.SetReg("DH", s%60)
		m.SetReg("DL", hund%100)
	case 0x30:
		m.SetReg("AX", 0x0005) // DOS 5.0
		m.SetReg("BX", 0)
	case 0x3F:
		if m.Reg("BX") != 0 {
			return fmt.Errorf("int 21h function 3Fh: handle %d is not modeled", m.Reg("BX"))
		}
		addr := m.Reg("DS")<<4 + m.Reg("DX")
		n := 0
		for n < int(m.Reg("CX")) {
			c, _, ok := d.key(false)
			if !ok {
				break
			}
			if c == '\r' {
				m.Mem[(addr+uint64(n))&0xFFFFF] = '\r'
				n++
				if n < int(m.Reg("CX")) {
					m.Mem[(addr+uint64(n))&0xFFFFF] = '\n'
					n++
				}
				break
			}
			m.Mem[(addr+uint64(n))&0xFFFFF] = c
			n++
		}
		m.SetReg("AX", uint64(n))
		d.setFlag(flagCF, false)
	case 0x40:
		var w io.Writer
		switch m.Reg("BX") {
		case 1:
			w = d.Stdout
		case 2:
			w = d.Stderr
		default:
			return fmt.Errorf("int 21h function 40h: handle %d is not modeled", m.Reg("BX"))
		}
		addr := m.Reg("DS")<<4 + m.Reg("DX")
		n := m.Reg("CX")
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = m.Mem[(addr+uint64(i))&0xFFFFF]
		}
		if err := d.console(w, buf); err != nil {
			return err
		}
		m.SetReg("AX", n)
		d.setFlag(flagCF, false)
	case 0x4C:
		d.exited, d.status = true, int(m.Reg("AL"))
	default:
		return fmt.Errorf("int 21h function %02Xh is not modeled (tool.emu.dos)", ah)
	}
	return nil
}
