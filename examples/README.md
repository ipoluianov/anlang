# Anlang examples

Each example is a separate project: a directory with its own `anlang.mod`.

| Example | Profile | Targets | Shows |
| --- | --- | --- | --- |
| [hello](hello/) | `app` | `linux-x86_64`, `macos-arm64` | The smallest program: printing a line |
| [hello-dos](hello-dos/) | `core` | `dos-com` | A DOS .COM program: compile-time CP866 encoding, `$`-terminated strings |
| [video-dos](video-dos/) | `core` | `dos-com` | Writing to the DOS text video memory through a hardware mapping (`unsafe.Map`), the `_i8086` file suffix |
| [packages](packages/) | `app` | `linux-x86_64`, `macos-arm64` | Nested project packages (`myprogram/mydictionary`), an `internal` package, exported and unexported names, `inout` receivers, comma-ok lookup, package errors as an `enum` with `errors.Is` and `errors.As` |
| [hello-stm32](hello-stm32/) | `core` | `stm32f103c8` | A Cortex-M program: output through the semihosting console, ELF, BIN and Intel HEX files |
| [blink-stm32](blink-stm32/) | `core` | `stm32f103c8` | Registers of the generated package `an/device`: the PLL at 72 MHz, a GPIO output and a timer that wakes the processor with `WFI`; `anlang run -gpio -for 3s` shows the pin changes in the model |
| [irq-stm32](irq-stm32/) | `core` | `stm32f103c8` | An `interrupt` handler of TIM2 that counts in an `atomic.U32`, `interrupts.Enable` after the initialization phase, and `cpu.WaitForInterrupt`; `anlang run -gpio -for 3s` shows the LED changing every 0.500 s |
| [counter-web](counter-web/) | `core` | `web`, `dos-com`, `stm32f103c8` | One program on three targets: a counter that reads its commands with `console.ReadByte`; on `web` the build writes `counter.wasm` and the page `counter.html`, which runs it in a browser and waits for the keys typed into it |
| [bootsector](bootsector/) | `core` | `bios-boot` | A 512-byte BIOS boot sector: BIOS calls through `an/arch/i8086`, the `_i8086` file suffix, a contract that removes overflow checks |

Build and run an example from its directory:

```
anlang run                    # first target from anlang.mod
anlang run -target dos-com    # a dos-com program in the built-in 8086 emulator
anlang run -screen            # the same, then the text screen of the emulator
```

A program for `stm32f103c8` runs in the built-in Cortex-M emulator; its files in `out/stm32f103c8/` also run in QEMU, on a board with flash at the same address:

```
qemu-system-arm -M olimex-stm32-h405 -nographic -semihosting-config enable=on,target=native -kernel out/stm32f103c8/hellostm32.elf
```

A program for `web` runs in the built-in WebAssembly interpreter; its module in `out/web/` also runs in Node.js, and its page in a browser that has JavaScript Promise Integration, such as Chrome 137 or later, from the file itself:

```
anlang run -target web        # in the built-in WebAssembly interpreter
node --no-warnings -e 'const {WASI} = require("node:wasi"); const w = new WASI({version: "preview1"}); WebAssembly.instantiate(require("fs").readFileSync("out/web/counter.wasm"), w.getImportObject()).then(({instance}) => w.start(instance))'
```

Open `out/web/counter.html` in the browser to use the counter there.

The boot sector is a raw 512-byte disk image in `out/bios-boot/`. Run it in QEMU or write it to the first sector of a disk:

```
qemu-system-i386 -drive format=raw,file=out/bios-boot/bootsector.bin
```
