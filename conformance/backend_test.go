package conformance

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"an/arch/desc"
	"an/build"
	_ "an/codegen"
	"an/emu/cortexm"
	"an/emu/dos"
	"an/emu/wasm"
	"an/interp"
)

// TestBackendDOS runs the reference programs that the target dos-com
// accepts (conf.backend.select) in the 8086 interpreter and compares
// their observable behavior with the interpretation of their IR for the
// same target (conf.backend.ir).
func TestBackendDOS(t *testing.T) { testBackend(t, "dos-com") }

// TestBackendSTM32 does the same for the target stm32f103c8 in the
// Cortex-M emulator, and, if QEMU is downloaded (scripts/fetch-qemu.sh),
// also runs each program in QEMU.
func TestBackendSTM32(t *testing.T) { testBackend(t, "stm32f103c8") }

// TestBackendWeb does the same for the target web in the WebAssembly
// interpreter, and, if Node.js is installed, also runs each module in its
// engine V8.
func TestBackendWeb(t *testing.T) { testBackend(t, "web") }

// emulator is a built-in emulator running a program.
type emulator interface {
	Run() (int, error)
}

func testBackend(t *testing.T, target string) {
	tg, _ := build.LookupTarget(target)
	d, errs := tg.Description()
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	a, errs := desc.ForName(d.Arch.Name)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	ran := 0
	for _, tt := range loadSuite(t) {
		if _, err := os.Stat(filepath.Join("testdata", tt.rule, tt.name+".out")); err != nil {
			continue
		}
		t.Run(tt.rule+"/"+tt.name, func(t *testing.T) {
			dir := tt.path
			if !tt.dir {
				dir = t.TempDir()
				data, err := os.ReadFile(tt.path)
				if err != nil {
					t.Fatal(err)
				}
				os.WriteFile(filepath.Join(dir, "anlang.mod"), []byte("module  conf\nanlang  1.0\ntargets "+target+"\n"), 0o644)
				os.WriteFile(filepath.Join(dir, "main.an"), data, 0o644)
			}
			p, errs := build.Load(dir)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			prog, errs := p.BackendProgram(target)
			if len(errs) > 0 {
				t.Skipf("the target does not accept the program: %v", errs[0])
			}
			// The IR interpreter gives the expected behavior; a program that
			// uses its architecture directly has it in its .out file.
			var want strings.Builder
			opts := interp.Options{Stdout: &want, Stderr: &want, MaxSteps: 1e8}
			if in, err := os.ReadFile(filepath.Join("testdata", tt.rule, tt.name+".in")); err == nil {
				opts.Stdin = strings.NewReader(string(in))
			}
			err := interp.Run(prog, opts)
			var ne *interp.NotExecutableError
			if errors.As(err, &ne) {
				out, _ := os.ReadFile(filepath.Join("testdata", tt.rule, tt.name+".out"))
				want.Reset()
				want.Write(out)
			} else if err != nil {
				want.WriteString("exit status 2\n")
			}
			image, errs := build.Backend(prog, d)
			if len(errs) == 1 && strings.Contains(errs[0].Error(), "(target.format.size") {
				t.Skipf("the program does not fit the target: %v", errs[0])
			}
			if len(errs) > 0 && strings.Contains(errs[0].Error(), "not supported yet by the code generator") {
				t.Skipf("the code generator does not translate the program yet: %v", errs[0])
			}
			if len(errs) > 0 && strings.Contains(errs[0].Error(), "[emb.stack.") {
				t.Skipf("the stack of the target does not accept the program: %v", errs[0])
			}
			if len(errs) > 0 {
				t.Fatalf("code generation: %v", errs)
			}
			var got strings.Builder
			var stdin io.Reader
			if in, err := os.ReadFile(filepath.Join("testdata", tt.rule, tt.name+".in")); err == nil {
				stdin = strings.NewReader(string(in))
			}
			var em emulator
			var screen func() string
			switch d.Platform.Name {
			case "web":
				w := &wasmRun{module: build.FormatImage(d.Formats[0], d, image), stdin: stdin, out: &got}
				em, screen = w, func() string { return "" }
			case "dos":
				m, err := dos.Load(a, image.Bytes)
				if err != nil {
					t.Fatal(err)
				}
				m.Stdout, m.Stderr, m.MaxSteps, m.Stdin = &got, &got, 50_000_000, stdin
				em, screen = m, m.Screen
			default:
				m, err := cortexm.Load(a, d, image.Bytes)
				if err != nil {
					t.Fatal(err)
				}
				m.Stdout, m.Stderr, m.MaxSteps, m.Stdin, m.UART = &got, &got, 50_000_000, stdin, &got
				em, screen = m, func() string { return "" }
			}
			status, err := em.Run()
			if err != nil {
				t.Fatalf("emulation: %v; output so far:\n%s", err, got.String())
			}
			if status != 0 {
				got.WriteString("exit status " + itoa(status) + "\n")
			}
			if ne != nil && os.Getenv("ANLANG_UPDATE") != "" {
				// Record the behavior of a program that only its target runs.
				base := filepath.Join("testdata", tt.rule, tt.name)
				os.WriteFile(base+".out", []byte(got.String()), 0o644)
				os.WriteFile(base+".screen", []byte(screen()), 0o644)
				return
			}
			if got.String() != want.String() {
				t.Errorf("output:\n%s\nwant:\n%s", got.String(), want.String())
			}
			if want, err := os.ReadFile(filepath.Join("testdata", tt.rule, tt.name+".screen")); err == nil && screen() != string(want) {
				t.Errorf("screen:\n%s\nwant:\n%s", screen(), want)
			}
			if d.Platform.Name == "bare" && ne == nil {
				// Programs that use the device of the target run only in its
				// model: the board of QEMU has other peripherals.
				in, _ := os.ReadFile(filepath.Join("testdata", tt.rule, tt.name+".in"))
				qemuCompare(t, d, image, in, got.String())
			}
			if d.Platform.Name == "web" {
				nodeCompare(t, build.FormatImage(d.Formats[0], d, image), stdin, got.String())
			}
			ran++
		})
	}
	if ran == 0 {
		t.Errorf("no reference program ran on %s (conf.backend.select)", target)
	}
	t.Logf("%d reference programs agree on %s", ran, target)
}

// qemuCompare runs the image of a program for a Cortex-M target in QEMU,
// on a board with flash at the same address and enough RAM, and compares
// its output and exit status with those of the built-in emulator.
func qemuCompare(t *testing.T, d *build.TargetDesc, image *build.Image, stdin []byte, want string) {
	qemu := filepath.Join("..", "external", "qemu", "root", "usr", "bin", "qemu-system-arm")
	if _, err := os.Stat(qemu); err != nil {
		return
	}
	file := filepath.Join(t.TempDir(), "prog.elf")
	if err := os.WriteFile(file, build.FormatImage(build.FormatDesc{Type: "ELFFormat"}, d, image), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// The semihosting console is the standard input and output of QEMU,
	// which -nographic would take for the serial port.
	cmd := exec.CommandContext(ctx, qemu, "-M", "olimex-stm32-h405", "-display", "none", "-monitor", "none", "-serial", "none",
		"-semihosting-config", "enable=on,target=native", "-kernel", file)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Stdin = strings.NewReader(string(stdin))
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("QEMU did not finish; output:\n%s", out.String())
	}
	if ee, ok := err.(*exec.ExitError); ok {
		out.WriteString("exit status " + itoa(ee.ExitCode()) + "\n")
	} else if err != nil {
		t.Fatal(err)
	}
	if out.String() != want {
		t.Errorf("QEMU output:\n%s\nthe emulator:\n%s", out.String(), want)
	}
}

func itoa(n int) string {
	s := ""
	if n == 0 {
		return "0"
	}
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

// wasmRun runs a module in the WebAssembly interpreter.
type wasmRun struct {
	module []byte
	stdin  io.Reader
	out    io.Writer
}

func (w *wasmRun) Run() (int, error) {
	r := &wasm.WASI{Stdin: w.stdin, Stdout: w.out, Stderr: w.out, MaxSteps: 50_000_000}
	return r.Run(w.module)
}

// nodeCompare runs a module in Node.js with its WASI and compares its
// output and exit status with those of the WebAssembly interpreter.
func nodeCompare(t *testing.T, module []byte, stdin io.Reader, want string) {
	node, err := exec.LookPath("node")
	if err != nil {
		return
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(file, module, 0o644); err != nil {
		t.Fatal(err)
	}
	js := filepath.Join(dir, "run.mjs")
	os.WriteFile(js, []byte(`import { readFileSync } from "node:fs";
import { WASI } from "node:wasi";
const wasi = new WASI({ version: "preview1", returnOnExit: true });
const { instance } = await WebAssembly.instantiate(readFileSync(process.argv[2]), wasi.getImportObject());
process.exitCode = wasi.start(instance);
`), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--no-warnings", js, file)
	if stdin != nil {
		if s, ok := stdin.(io.Seeker); ok {
			s.Seek(0, io.SeekStart)
		}
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	got := string(out)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		got += "exit status " + itoa(ee.ExitCode()) + "\n"
	} else if err != nil {
		t.Fatalf("node: %v", err)
	}
	if got != want {
		t.Errorf("node:\n%s\nwant (the WebAssembly interpreter):\n%s", got, want)
	}
}
