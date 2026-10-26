package desc

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The 8086 processor test suite of SingleStepTests
// (https://github.com/SingleStepTests/8086, MIT license): for each opcode,
// the register and memory state before and after one instruction on a
// real Intel 8086. scripts/fetch-8086-tests.sh downloads it into
// external/8086; the test is skipped without it.

type sstState struct {
	Regs map[string]uint64 `json:"regs"`
	RAM  [][2]uint64       `json:"ram"`
}

type sstTest struct {
	Name    string   `json:"name"`
	Bytes   []byte   `json:"bytes"`
	Initial sstState `json:"initial"`
	Final   sstState `json:"final"`
}

type sstMeta struct {
	Opcodes map[string]struct {
		Status    string `json:"status"`
		FlagsMask uint64 `json:"flags-mask"`
		Reg       map[string]struct {
			Status    string `json:"status"`
			FlagsMask uint64 `json:"flags-mask"`
		} `json:"reg"`
	} `json:"opcodes"`
}

var sstRegs = []string{"ax", "bx", "cx", "dx", "cs", "ss", "ds", "es", "sp", "bp", "si", "di", "ip", "flags"}

// TestI8086ProcessorTests checks the acceptance criterion of roadmap step
// 12: the interpreter generated from the description agrees with the
// 8086 processor test suite on every documented instruction. By default
// it runs every tenth test; ANLANG_8086_ALL=1 runs all of them.
func TestI8086ProcessorTests(t *testing.T) {
	dir := filepath.Join("..", "..", "external", "8086")
	metaData, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Skip("the 8086 processor tests are not downloaded: run scripts/fetch-8086-tests.sh")
	}
	var meta sstMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatal(err)
	}
	a := loadI8086(t)
	files, _ := filepath.Glob(filepath.Join(dir, "*.json.gz"))
	sort.Strings(files)
	if f := os.Getenv("ANLANG_8086_FILE"); f != "" {
		files = []string{filepath.Join(dir, f)}
	}
	total, passed, skipped := 0, 0, 0
	for _, file := range files {
		base := strings.TrimSuffix(filepath.Base(file), ".json.gz")
		op, reg, _ := strings.Cut(base, ".")
		info, ok := meta.Opcodes[op]
		status, flagsMask := info.Status, info.FlagsMask
		if reg != "" {
			r := info.Reg[reg]
			status, flagsMask = r.Status, r.FlagsMask
		}
		if !ok || status != "normal" && status != "" {
			skipped++
			continue
		}
		if flagsMask == 0 {
			flagsMask = 0xFFFF
		}
		tests, err := readSST(file)
		if err != nil {
			t.Fatal(err)
		}
		failures := 0
		for i, tt := range tests {
			if os.Getenv("ANLANG_8086_ALL") == "" && i%10 != 0 {
				continue // a sample; ANLANG_8086_ALL=1 runs every test
			}
			total++
			if msg := runSST(a, tt, flagsMask); msg != "" {
				failures++
				if failures <= 3 {
					t.Errorf("%s %s (% X): %s", base, tt.Name, tt.Bytes, msg)
				}
				continue
			}
			passed++
		}
		if failures > 3 {
			t.Errorf("%s: %d of %d tests fail", base, failures, len(tests))
		}
	}
	t.Logf("%d tests, %d pass; %d files skipped (undocumented, aliases, coprocessor)", total, passed, skipped)
}

func readSST(file string) ([]sstTest, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var tests []sstTest
	err = json.NewDecoder(z).Decode(&tests)
	return tests, err
}

// runSST runs one test and returns a description of the differences.
func runSST(a *Arch, tt sstTest, flagsMask uint64) string {
	m := NewMachine(a)
	// Ports without a device read as all ones, an open bus.
	m.In = func(port uint64, width int) uint64 { return mask(width) }
	for _, r := range sstRegs {
		m.SetReg(strings.ToUpper(r), tt.Initial.Regs[r])
	}
	for _, e := range tt.Initial.RAM {
		m.Mem[e[0]] = byte(e[1])
	}
	if err := m.Step(); err != nil {
		return err.Error()
	}
	var diffs []string
	for _, r := range sstRegs {
		want, ok := tt.Final.Regs[r]
		if !ok {
			want = tt.Initial.Regs[r]
		}
		got := m.Reg(strings.ToUpper(r))
		if r == "flags" {
			got, want = got&flagsMask, want&flagsMask
		}
		if got != want {
			diffs = append(diffs, fmt.Sprintf("%s = %#x, want %#x", r, got, want))
		}
	}
	// After a division error, the copy of the flags that the interrupt
	// pushes holds the undefined flags too.
	pushed := map[uint64]uint64{}
	if sp, ok := tt.Final.Regs["sp"]; ok && sp == (tt.Initial.Regs["sp"]-6)&0xFFFF && flagsMask != 0xFFFF {
		ss := tt.Initial.Regs["ss"]
		pushed[(ss<<4+(sp+4)&0xFFFF)&0xFFFFF] = flagsMask & 0xFF
		pushed[(ss<<4+(sp+5)&0xFFFF)&0xFFFFF] = flagsMask >> 8
	}
	for _, e := range tt.Final.RAM {
		if mk, ok := pushed[e[0]]; ok {
			if uint64(m.Mem[e[0]])&mk != e[1]&mk {
				diffs = append(diffs, fmt.Sprintf("pushed flags byte at %x = %#x, want %#x", e[0], m.Mem[e[0]], e[1]))
			}
			continue
		}
		if got := uint64(m.Mem[e[0]]); got != e[1] {
			diffs = append(diffs, fmt.Sprintf("mem[%s] = %#x, want %#x", strconv.FormatUint(e[0], 16), got, e[1]))
		}
	}
	return strings.Join(diffs, ", ")
}
