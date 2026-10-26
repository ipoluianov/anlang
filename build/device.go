package build

import (
	"fmt"
	"sort"
	"strings"

	"an/format"
)

// DevicePackage returns the source of package an/device for a target
// with a device description (emb.device.package): the registers of each
// peripheral as a struct of register types mapped at the base address of
// each instance (emb.reg.unsafe), named after the group of the
// peripheral with the suffix _Regs, a type for each register and each bit
// field (emb.reg.types), and the numbers of the interrupts.
//
// A register type has, for each field, a method that returns the field
// and a method With… that returns the register with the field replaced.
// A field with allowed values has an enum type, whose variant names are
// the type name and the value name joined by an underscore; reading it
// returns the variant and whether the bits are an allowed value. A field
// of one bit without allowed values is a bool; others are unsigned
// integers of the smallest width. Peripherals of one group share their
// types, named after the group, and fields whose names differ in a
// trailing number and whose allowed values are the same share a type
// named without the number.
func DevicePackage(target string, d *Device) []byte {
	g := &deviceGen{d: d}
	g.generate(target)
	src, errs := format.Source("device.an", []byte(g.b.String()), 0)
	if len(errs) > 0 {
		panic(fmt.Sprintf("build: the device package of %s: %v\n%s", target, errs, g.b.String()))
	}
	return src
}

type deviceGen struct {
	d     *Device
	b     strings.Builder
	types map[string]bool // the field types declared
}

func (g *deviceGen) w(format string, args ...any) { fmt.Fprintf(&g.b, format+"\n", args...) }

// group returns the name of the types of a peripheral.
func group(p *Peripheral) string {
	if p.Group != "" {
		return p.Group
	}
	return p.Name
}

// uintType returns the unsigned integer type of bits bits.
func uintType(bits int) string {
	switch {
	case bits <= 8:
		return "u8"
	case bits <= 16:
		return "u16"
	case bits <= 32:
		return "u32"
	}
	return "u64"
}

// stem returns a name without its trailing digits.
func stem(name string) string {
	s := strings.TrimRight(name, "0123456789")
	if s == "" {
		return name
	}
	return s
}

// comment writes a doc comment for name from a description.
func (g *deviceGen) comment(name, what, desc string) {
	text := name + " is " + what
	if desc != "" {
		text += ": " + strings.Join(strings.Fields(desc), " ")
	}
	text += "."
	line := "//"
	for _, word := range strings.Fields(text) {
		if len(line)+1+len(word) > 76 && line != "//" {
			g.w("%s", line)
			line = "//"
		}
		line += " " + word
	}
	g.w("%s", line)
}

func (g *deviceGen) generate(target string) {
	d := g.d
	g.types = map[string]bool{}
	g.w("// Package device is generated from the device description %s of", d.Name)
	g.w("// target %s (spec/14-embedded.md §1): the registers of its", target)
	g.w("// peripherals, mapped at their addresses, and its interrupts.")
	g.w("package device")
	g.w("")
	g.w("profile core")
	g.w("")
	g.w(`import (`)
	g.w(`	"an/runtime"`)
	g.w(`	"an/unsafe"`)
	g.w(`)`)
	// The register types of each group, from its first peripheral.
	done := map[string]bool{}
	for i := range d.Peripherals {
		p := &d.Peripherals[i]
		if done[group(p)] || len(p.Registers) == 0 {
			continue
		}
		done[group(p)] = true
		g.peripheralTypes(p)
	}
	g.w("")
	g.w("// The peripherals, mapped at their base addresses.")
	g.w("var (")
	for i := range d.Peripherals {
		p := &d.Peripherals[i]
		if len(p.Registers) == 0 {
			continue
		}
		g.w("\t%s = unsafe.Map[%s_Regs](unsafe.Flat(0x%X))", p.Name, group(p), p.Base)
	}
	g.w(")")
	if len(d.Interrupts) > 0 {
		g.w("")
		g.w("// Interrupt is the number of an interrupt of the device.")
		g.w("type Interrupt int")
		g.w("")
		g.w("// The interrupts.")
		g.w("const (")
		for _, irq := range d.Interrupts {
			g.w("\tInterrupt%s Interrupt = %d // %s", irq.Name, irq.Number, strings.Join(strings.Fields(irq.Description), " "))
		}
		g.w(")")
	}
}

// fieldType returns the name of the type of field f of register r of
// group grp, declaring it if needed.
func (g *deviceGen) fieldType(grp string, r *Register, f *Field) string {
	name := grp + "_" + r.Name + "_" + f.Name
	if s := stem(f.Name); s != f.Name {
		// Fields such as MODE0 … MODE15 share a type if their values are
		// the same.
		name = grp + "_" + s
		if len(f.Values) == 0 {
			name = grp + "_" + r.Name + "_" + s
		}
	} else if len(f.Values) > 0 {
		name = grp + "_" + f.Name
	}
	if g.types[name] {
		return name
	}
	g.types[name] = true
	g.w("")
	switch {
	case len(f.Values) > 0:
		g.comment(name, "the allowed values of field "+f.Name+" of "+grp, stem(f.Description))
		g.w("type %s enum {", name)
		for _, v := range f.Values {
			g.w("\t%s_%s // %d: %s", name, v.Name, v.Value, strings.Join(strings.Fields(v.Description), " "))
		}
		g.w("}")
	case f.Width == 1:
		g.comment(name, "a bit of "+grp+"."+r.Name, f.Description)
		g.w("type %s bool", name)
	default:
		g.comment(name, "field "+f.Name+" of "+grp+"."+r.Name, f.Description)
		g.w("type %s %s", name, uintType(f.Width))
	}
	return name
}

func (g *deviceGen) peripheralTypes(p *Peripheral) {
	grp := group(p)
	regs := append([]Register(nil), p.Registers...)
	sort.SliceStable(regs, func(i, j int) bool { return regs[i].Offset < regs[j].Offset })
	g.w("")
	g.comment(grp+"_Regs", "the registers of a "+grp+" peripheral", p.Description)
	g.w("type %s_Regs struct {", grp)
	var off uint64
	pad := 0
	for _, r := range regs {
		if r.Offset > off {
			g.w("\tpad%d [%d]u8", pad, r.Offset-off)
			pad++
		}
		g.w("\t%s %s_%s", r.Name, grp, r.Name)
		off = r.Offset + uint64(r.Size/8)
	}
	g.w("}")
	g.w("")
	g.w("// Lock is an iterator whose body, in a range statement, is a critical")
	g.w("// section of the peripheral: it runs once, with the interrupts disabled")
	g.w("// (emb.lock.form, emb.lock.ceiling).")
	g.w("func (p inout %s_Regs) Lock(yield func(inout %s_Regs) bool) {", grp, grp)
	g.w("\ts := runtime.DisableInterrupts()")
	g.w("\tyield(p)")
	g.w("\truntime.RestoreInterrupts(s)")
	g.w("}")
	for i := range regs {
		r := &regs[i]
		rt := grp + "_" + r.Name
		g.w("")
		g.comment(rt, "register "+r.Name+" of "+grp, r.Description)
		g.w("type %s %s", rt, uintType(r.Size))
		for j := range r.Fields {
			f := &r.Fields[j]
			ft := g.fieldType(grp, r, f)
			g.fieldMethods(rt, uintType(r.Size), f, ft)
		}
	}
}

// fieldMethods writes the methods of field f of register type rt, whose
// underlying type is base, with the field type ft.
func (g *deviceGen) fieldMethods(rt, base string, f *Field, ft string) {
	mask := uint64(1)<<uint(f.Width) - 1
	if f.Width >= 64 {
		mask = ^uint64(0)
	}
	bits := fmt.Sprintf("(%s(r) >> %d & 0x%X)", base, f.Lsb, mask)
	readable := f.Access != 2 // not write-only
	g.w("")
	switch {
	case len(f.Values) > 0:
		if readable {
			g.w("// %s returns field %s and whether it holds an allowed value.", f.Name, f.Name)
			g.w("func (r %s) %s() (%s, bool) {", rt, f.Name, ft)
			g.w("\tswitch %s {", bits)
			for _, v := range f.Values {
				g.w("\tcase 0x%X:", v.Value)
				g.w("\t\treturn %s_%s, true", ft, v.Name)
			}
			g.w("\t}")
			g.w("\treturn %s_%s, false", ft, f.Values[0].Name)
			g.w("}")
			g.w("")
		}
		g.w("// With%s returns r with field %s set to v.", f.Name, f.Name)
		g.w("func (r %s) With%s(v %s) %s {", rt, f.Name, ft, rt)
		g.w("\tvar x %s", base)
		g.w("\tswitch v {")
		for _, v := range f.Values {
			g.w("\tcase %s_%s:", ft, v.Name)
			g.w("\t\tx = 0x%X", v.Value)
		}
		g.w("\t}")
		g.w("\treturn %s(%s(r)&^(0x%X<<%d) | x<<%d)", rt, base, mask, f.Lsb, f.Lsb)
		g.w("}")
	case f.Width == 1:
		if readable {
			g.w("// %s returns bit %s.", f.Name, f.Name)
			g.w("func (r %s) %s() %s {", rt, f.Name, ft)
			g.w("\treturn %s(%s != 0)", ft, bits)
			g.w("}")
			g.w("")
		}
		g.w("// With%s returns r with bit %s set to v.", f.Name, f.Name)
		g.w("func (r %s) With%s(v %s) %s {", rt, f.Name, ft, rt)
		g.w("\tif v {")
		g.w("\t\treturn %s(%s(r) | 1<<%d)", rt, base, f.Lsb)
		g.w("\t}")
		g.w("\treturn %s(%s(r) &^ (1 << %d))", rt, base, f.Lsb)
		g.w("}")
	default:
		fbase := uintType(f.Width)
		if readable {
			g.w("// %s returns field %s.", f.Name, f.Name)
			g.w("func (r %s) %s() %s {", rt, f.Name, ft)
			g.w("\treturn %s(%s(%s))", ft, fbase, bits)
			g.w("}")
			g.w("")
		}
		g.w("// With%s returns r with field %s set to v, which fits its %d bits", f.Name, f.Name, f.Width)
		g.w("// (emb.reg.values).")
		g.w("func (r %s) With%s(v %s) %s {", rt, f.Name, ft, rt)
		if f.Width != map[string]int{"u8": 8, "u16": 16, "u32": 32, "u64": 64}[fbase] {
			g.w("\trequires v <= 0x%X", mask)
		}
		g.w("\treturn %s(%s(r)&^(0x%X<<%d) | %s(%s(v)&0x%X)<<%d)", rt, base, mask, f.Lsb, base, fbase, mask, f.Lsb)
		g.w("}")
	}
}
