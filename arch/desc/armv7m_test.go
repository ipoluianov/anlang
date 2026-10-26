package desc

import (
	"fmt"
	"math/rand"
	"testing"
)

func loadARMv7M(t *testing.T) *Arch {
	t.Helper()
	a, errs := Load("ARMv7M")
	for i, err := range errs {
		if i < 30 {
			t.Error(err)
		}
	}
	if a == nil || len(errs) > 0 {
		t.FailNow()
	}
	return a
}

// TestARMv7M checks that the description of ARMv7-M type-checks, as
// Anlang and in its encodings and semantics (spec/24-roadmap.md, step
// 15).
func TestARMv7M(t *testing.T) {
	a := loadARMv7M(t)
	if a.Name != "armv7m" || a.Word != 32 || a.Unit != 16 || len(a.Instructions) < 250 {
		t.Errorf("unexpected description: %s, word %d, unit %d, %d instructions", a.Name, a.Word, a.Unit, len(a.Instructions))
	}
}

// thumb32 reports whether a first halfword starts a 32-bit instruction.
func thumb32(hw uint16) bool { return hw>>11 >= 0b11101 }

// TestARMv7MRoundTrip decodes every 16-bit encoding and random 32-bit
// encodings: none is ambiguous, and assembling the disassembled text of
// each gives an encoding with the same text (target.arch.round-trip).
func TestARMv7MRoundTrip(t *testing.T) {
	a := loadARMv7M(t)
	rng := rand.New(rand.NewSource(1))
	undecoded := map[string]int{}
	failures := 0
	check := func(code []byte) {
		d, amb := a.Decode(code)
		if len(amb) > 0 {
			failures++
			if failures < 20 {
				t.Errorf("% X: ambiguous: %v", code, amb)
			}
			return
		}
		if d == nil {
			undecoded[fmt.Sprintf("%04X", uint16(code[1])<<8|uint16(code[0]))[:2]]++
			return
		}
		text := a.Disasm(d)
		re, err := a.Asm(text)
		if err != nil {
			failures++
			if failures < 20 {
				t.Errorf("% X: %s: %v", code[:d.Length], text, err)
			}
			return
		}
		d2, _ := a.Decode(append(re, 0, 0, 0, 0))
		if d2 == nil || a.Disasm(d2) != text {
			failures++
			if failures < 20 {
				t.Errorf("% X: %s: reassembled as % X", code[:d.Length], text, re)
			}
		}
	}
	for hw := 0; hw < 1<<16; hw++ {
		if thumb32(uint16(hw)) {
			for k := 0; k < 16; k++ {
				hw2 := rng.Intn(1 << 16)
				check([]byte{byte(hw), byte(hw >> 8), byte(hw2), byte(hw2 >> 8)})
			}
			continue
		}
		check([]byte{byte(hw), byte(hw >> 8), 0, 0})
	}
	if failures > 0 {
		t.Errorf("%d failures", failures)
	}
	if testing.Verbose() {
		t.Logf("undecoded by the high byte of the first halfword: %v", undecoded)
	}
}
