# 13. Targets

Area: `target`.

A build target combines three independent descriptions: an **architecture**, a **platform** and one or more **binary formats**. All three are declarative data written as Anlang constants, in the standard library or in the project. Adding an architecture does not require changing the compiler.

| Layer | Describes | Examples |
| --- | --- | --- |
| Architecture | instructions, registers, word size, pointer kinds, calling conventions, timing | `i8086`, `x86_64`, `armv6m`, `armv7m`, `avr`, `msp430`, `riscv32`, `riscv64`, `arm64`, `wasm32` |
| Platform | what the environment provides: effects, ABI, entry point, dynamic loading | `dos`, `bare`, `linux`, `windows`, `macos`, `ios`, `android`, `web` |
| Format | structure of the output file | `flat` (.COM), `mz` (.EXE), ELF, PE, Mach-O, wasm, BIN, Intel HEX, UF2 |

Shader targets for GPU functions (`spirv`, `wgsl`, `msl`) are defined in [16-gpu](16-gpu.md).

## 13.1 Target packages

**target.pkg.description** — A *target package* is a package that declares an exported constant `Description` of type `target.Desc` (package `an/target`). The constant is evaluated at compile time (see [10-compile-time](10-compile-time.md)); a misspelled field or a value of the wrong type is an ordinary compile error.

**target.pkg.name** — The *target name* is the last element of the target package's directory path. A directory name MAY contain hyphens, for example `an/target/dos-com` with package clause `package doscom`.

**target.pkg.location** — Standard targets are the packages under `an/target/`. A project MAY declare its own target packages anywhere in the module, for example `myprogram/boards/myboard`.

**target.pkg.pure** — A target package MUST NOT have effects other than those permitted in compile-time evaluation (**ct.eval.evaluable**). It MAY read files in its own directory, such as an SVD file (**ct.eval.read-input**).

```go
package doscom

import (
    "an/arch"
    "an/platform"
    "an/target"
)

const Description = target.Desc{
    Arch:     arch.I8086,
    Platform: platform.DOS,
    Word:     16,
    Pointers: target.Pointers{Near: 16, Far: 32},
    Formats:  []target.Format{target.Flat{Origin: 0x100, MaxSize: 65280}},
    Floats:   target.SoftFloat,
    Provides: []Effect{Console, Video, File, Keyboard, StaticAlloc},
    Lacks:    []Effect{Network},
    OnPanic:  target.PrintAndExit,
}
```

## 13.2 The `target.Desc` schema

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `Arch` | `arch.Arch` | yes | architecture description (13.5) |
| `Platform` | `platform.Platform` | yes | platform description (13.4) |
| `Word` | `int` | no; default from `Arch` | width in bits of `int` and `uint`: 16, 32 or 64 |
| `Pointers` | `target.Pointers` | no; default from `Arch` | widths of pointer kinds (13.6) |
| `Formats` | `[]target.Format` | yes, at least one | output formats (13.9) |
| `Floats` | `target.FloatSupport` | no; default from `Arch` | `target.HardFloat`, `target.SoftFloat` or `target.NoFloat` (13.7) |
| `Memory` | `[]target.Region` | yes on `bare`; otherwise no | memory regions (13.8) |
| `Stack` | size in bytes | yes on `bare`; otherwise no | size of the stack of `main` (13.8) |
| `Device` | device description | no | peripherals, registers and interrupts, usually `svd.Load(file)` (see [14-embedded](14-embedded.md)) |
| `Provides` | `[]Effect` | yes | environment effects available on the target (13.3) |
| `Lacks` | `[]Effect` | no | environment effects explicitly absent (13.3) |
| `OnPanic` | `target.PanicAction` | no; default from `Platform` | what a panic does (13.10) |
| `Clock` | frequency in Hz | no; required for `wcet.Within` | CPU clock frequency used with the architecture's timing model for WCET (see [14-embedded](14-embedded.md), **emb.wcet.model**) |

**target.desc.valid** — An implementation MUST reject a target description that violates this schema: a missing required field, `Word` outside {16, 32, 64}, a `Word` or `Pointers` value the architecture does not support, `Provides` and `Lacks` that intersect, overlapping memory regions, or a format that the platform does not support.

## 13.3 Effects provided by a target

**target.effects.environment** — The *environment effects* are the effects that only the target can make real: memory allocation (`Alloc`, `StaticAlloc`), input and output (`Console`, `File`, `Network`, `Video`, `Keyboard`, `ReadInput`, `WriteOutput`), execution (`Tasks`, `Interrupts`), hardware effects from the device description, nondeterminism (`Clock`, `Random`), `Privileged`, `GPU`, and the device capability effects of the `ui` profile (`Camera`, `Location`, …). This classification is normative; the meaning of each effect is given in [07-effects](07-effects.md) (**eff.base.set**), which delegates the target check to this section (**eff.target.check**).

**target.effects.provides** — Every environment effect reachable from the program's entry points in the build for a target MUST be listed in the target's `Provides`. A reachable environment effect that is not listed is a compile error that shows the call chain to its source.

**target.effects.lacks** — `Lacks` lists environment effects the target explicitly does not have. A reachable effect listed in `Lacks` is a compile error with a diagnostic that says the platform lacks it. `Lacks` does not change which programs compile; it changes the diagnostic and the answers of `anlang query`.

**target.effects.foreign** — `Foreign` is accepted on a target only if `Lacks` is empty (**ffi.effects.foreign-deny** in [12-ffi](12-ffi.md)).

**target.effects.computational** — Effects that are not environment effects — `Panic`, `Global`, `Block` and custom effects declared with `effect.Declare` — need not be listed. The environment effects underneath a custom effect are checked as usual.

## 13.4 Platforms

**target.platform.desc** — A platform is described by a constant of type `platform.Platform` in an Anlang package. The description states:

1. the environment effects the platform can provide;
2. whether it has dynamic loading (`ffi.Library`, see [12-ffi](12-ffi.md));
3. its system call mechanism, if any, and its C ABI variant;
4. its entry point and start-up sequence (see [19-runtime](19-runtime.md));
5. the binary formats it accepts;
6. the profiles allowed for the `main` package;
7. the default `OnPanic` action;
8. limits on background execution, for platforms that restrict it (see [17-ui](17-ui.md));
9. for a platform implemented in Anlang, its system call table (`abi.Table`, see [15-os](15-os.md)).

**target.platform.profile** — The profile of the `main` package MUST be one of the profiles the platform allows. Packages in stricter profiles are callable from any profile (see [07-effects](07-effects.md)).

| Platform | Dynamic loading | `main` profiles | Default `OnPanic` |
| --- | --- | --- | --- |
| `bare` | no | `core` | `Halt` |
| `dos` | no | `core` | `PrintAndExit` |
| `linux`, `windows`, `macos` | yes | `core`, `app`, `ui` | `PrintAndExit` |
| `ios`, `android` | yes | `ui` | `PrintAndExit` |
| `web` | no (WebAssembly imports) | `ui`, `app` | `PrintAndExit` |

## 13.5 Architectures

### 13.5.1 Architecture descriptions

An *architecture description* consists of three components, all Anlang data:

1. **Machine model** — registers and register classes, word size, endianness, alignment, pointer kinds and address spaces, stack direction, calling conventions (the internal Anlang ABI and the C ABI), interrupt and trap entry, the names and handler signatures of the exceptions available to `trap` (see [15-os](15-os.md)), which instructions are privileged (effect `Privileged`, see [15-os](15-os.md)), and a timing model per instruction used for WCET (see [14-embedded](14-embedded.md)).
2. **Instruction selection rules** — declarative rules that map patterns of IR operations to instruction sequences, with operand constraints. Example: addition of two 16-bit values → `ADD r16, r/m16`.
3. **Formal instruction semantics** — the effect of each instruction on machine state, in the style of Sail.

**target.arch.value** — An architecture is identified by a constant of type `arch.Arch` in package `an/arch` (for example `arch.I8086`, `arch.ARMv7M`, `arch.X86_64`) or in a project package. The constant refers to the three components.

**target.arch.no-compiler-change** — An implementation MUST derive the code generator for an architecture from its instruction selection rules. Adding an architecture MUST NOT require changing the compiler.

**target.arch.rules-checked** — An implementation MUST check every instruction selection rule against the formal semantics: the instruction sequence must compute what the IR pattern computes, for all operand values permitted by the rule's constraints. The check uses the solver or exhaustive testing where the operand space allows it (see [22-compiler](22-compiler.md), [23-conformance](23-conformance.md)).

**target.arch.generated** — From the formal semantics an implementation MUST generate:

1. an assembler and disassembler for the architecture;
2. the instruction package `an/arch/<name>` (13.5.2);
3. an interpreter used by the built-in emulators (see [20-toolchain](20-toolchain.md)).

### 13.5.2 Instruction packages

**target.arch.instr-package** — The instruction package `an/arch/<name>` exposes the architecture's instructions and registers as ordinary Anlang functions and types, for example `i8086.Int(0x21, i8086.Regs{AH: 0x09})`, `armv7m.WFI()`, `x86_64.ReadCR2()`. Named operands are passed as a struct, since Anlang has no named arguments.

**target.arch.instr-effects** — The effects of each instruction function are derived from the formal semantics: an instruction that changes interrupt state has `Interrupts`, a privileged instruction has `Privileged`, and so on. The compiler knows which registers each instruction clobbers.

**target.arch.import-suffix** — A file MAY import `an/arch/<name>` only if its name carries the architecture suffix `_<name>` (for example `print_i8086.an`), as defined in [03-declarations](03-declarations.md). Architecture description packages themselves are exempt.

**target.arch.asm** — The assembler for `asm` blocks (see [05-statements](05-statements.md)) is the one generated from the same description.

## 13.6 Integer widths and pointer kinds

**target.word.int** — `int` and `uint` have the width given by `Word`.

**target.word.usize** — `usize` has the width of a data address of the default pointer kind: the flat pointer width, or the near pointer width on segmented architectures.

**target.pointers.kinds** — `target.Pointers` has the fields `Flat`, `Near`, `Far` and `Program`, each a width in bits; zero means the kind does not exist. Pointer kinds appear only inside `unsafe` and architecture packages (see [06-memory](06-memory.md)).

| Architecture | `int` | `usize` | Pointer kinds |
| --- | --- | --- | --- |
| `i8086` | 16 | 16 | `Near` 16, `Far` 32 (segment:offset) |
| `avr` | 16 | 16 | `Near` 16 (RAM), `Program` 16 or 24 (flash, read by special instructions) |
| `msp430` | 16 | 16 | `Flat` 16 |
| `armv6m`, `armv7m`, `riscv32`, `wasm32` | 32 | 32 | `Flat` 32 |
| `x86_64`, `arm64`, `riscv64` | 64 | 64 | `Flat` 64 |

**Note** — Portable code uses sized types (`i8` … `i64`, `u8` … `u64`) by default.

## 13.7 Floating point

**target.floats.hard** — With `HardFloat`, `f32` and `f64` operations use the processor's floating-point instructions.

**target.floats.soft** — With `SoftFloat`, they use software emulation linked from the standard library, with the same IEEE 754 results.

**target.floats.none** — With `NoFloat`, a function reachable in the build that has a value or operation of type `f32`, `f64` or a floating-point `simd` type is a compile error.

## 13.8 Memory regions and stack

**target.memory.region** — `target.Region` has the fields `Name string`, `At` (start address), `Size` (bytes) and `Exec bool`. A region with `Exec: true` holds code and read-only data and is not written at run time; a region with `Exec: false` holds writable data, zero-initialized data and stacks.

**target.memory.placement** — Code and constants (**ct.const.placement**) are placed in the first `Exec` region; initial values of writable data are stored there and copied to RAM at start-up (see [19-runtime](19-runtime.md)). Writable data and stacks are placed in the first non-`Exec` region.

**target.memory.overflow** — If the contents assigned to any region exceed its size, the build fails. The diagnostic MUST show the size of the region, the total used, and a breakdown per package of code, constants, static data and stack reserve.

**target.memory.stack** — `Stack` is the stack size of `main`. Stack analysis compares it with the computed maximum depth (see [14-embedded](14-embedded.md)).

```go
const Description = target.Desc{
    Arch:     arch.ARMv7M,
    Platform: platform.Bare,
    Word:     32,
    Memory: []target.Region{
        {Name: "flash", At: 0x0800_0000, Size: 64 * KiB, Exec: true},
        {Name: "ram",   At: 0x2000_0000, Size: 20 * KiB},
    },
    Stack:    2 * KiB,
    Formats:  []target.Format{target.ELF, target.BIN, target.HEX},
    Device:   svd.Load("STM32F103.svd"),
    Provides: []Effect{Gpio, Uart, Timers, Interrupts, StaticAlloc, Tasks},
    Lacks:    []Effect{File},
    OnPanic:  target.Reset,
}
```

## 13.9 Binary formats

**target.format.interface** — A binary format is a value implementing interface `target.Format`. The linker hands it the linked image — segments with addresses and contents, the entry point and symbols — and the format produces the output file. Formats are Anlang code like architectures; adding one does not change the compiler.

**target.format.all** — A build produces one output file for each format listed in `Formats`, in `out/<target>/`.

| Format | Value | Requirements |
| --- | --- | --- |
| flat (.COM) | `target.Flat{Origin, MaxSize}` | raw image without header or relocations, loaded at `Origin`; execution starts at the first byte; the image MUST NOT exceed `MaxSize` bytes; for DOS `.COM`, `Origin` is `0x100` and `MaxSize` is `65280` |
| boot sector | `target.BootSector` | raw image loaded at `0x7C00`, at most 510 bytes, padded with zero bytes to 510 bytes and followed by the boot signature `0x55 0xAA`; the file is exactly 512 bytes; execution starts at the first byte |
| MZ (.EXE) | `target.MZ` | DOS executable with MZ header and relocation table |
| ELF | `target.ELF` | executable on Linux; on `bare`, load segments at region addresses for debuggers and flashing tools |
| PE | `target.PE` | Windows executable |
| Mach-O | `target.MachO` | macOS and iOS executable |
| wasm | `target.Wasm` | WebAssembly module with imports from `js.Import` |
| BIN | `target.BIN` | raw image of the `Exec` region contents, starting at the region's start address |
| Intel HEX | `target.HEX` | Intel HEX records of the same contents as BIN |
| UF2 | `target.UF2{FamilyID}` | UF2 blocks for USB mass-storage bootloaders |

**target.format.size** — Exceeding a format's size limit is a build error with the same per-package breakdown as **target.memory.overflow**.

**target.format.macos-sign** — A Mach-O executable for macOS on `arm64` MUST carry an ad-hoc code signature, because the operating system does not run unsigned arm64 code. Signing iOS applications for devices is described in [17-ui](17-ui.md).

## 13.10 Panic action

**target.panic.action** — `target.PanicAction` has the values `target.PrintAndExit`, `target.Reset` and `target.Halt`. Their behavior — message, stack trace, reset, halt — is defined in [19-runtime](19-runtime.md).

## 13.11 Selecting targets

**target.select.flag** — The target is chosen at build time with `-target`, never in source code. A package header contains only `profile` (see [03-declarations](03-declarations.md)).

**target.select.resolve** — A target argument is resolved as follows:

1. If it contains `/`, it is the import path of a target package.
2. Otherwise it is a target name. The implementation searches the module for target packages with that name (**target.pkg.name**). If exactly one is found, it is used; if more than one, the build fails.
3. If none is found in the module, `an/target/<name>` is used; if it does not exist, the build fails.

**target.select.default** — Without `-target`, the targets listed in `anlang.mod` are built (see [20-toolchain](20-toolchain.md)).

**target.select.files** — For each target, source files are selected by their platform and architecture suffixes as defined in [03-declarations](03-declarations.md).

## 13.12 Multi-target builds

**target.multi.flag** — `anlang build -target a,b,c` builds all listed targets in one run.

**target.multi.equivalent** — The result for each target MUST be identical to building that target alone. An implementation MAY share work between targets — for example type, effect and contract checking of packages whose selected file sets are the same — but MUST NOT let one target's result depend on another's.

**target.multi.output** — The outputs of target `t` are placed in `out/<t>/`.

## 13.13 Standard targets

**target.std.name** — Target names of the standard library follow these rules:

1. A target for an operating system is named `<platform>-<architecture>`: `linux-x86_64`, `windows-x86_64`.
2. DOS and BIOS targets are named after the kind of output: `dos-com`, `dos-exe`, `bios-boot`.
3. Other boot targets are named `<boot environment>-<architecture>`: `multiboot-x86_64`, `uefi-arm64`, `devicetree-riscv64`.
4. A microcontroller target is named after the chip, in lower case without separators: `stm32f103c8`, `rp2040`.
5. A mobile target is named after the platform and builds all its architectures into one package: `android`, `ios`, `ios-sim`.
6. The browser target is `web`.

Architecture names inside target names keep their own spelling (`x86_64`).

**target.std.required** — The implementation MUST provide the following target packages under `an/target/`. The step column is the roadmap step that introduces the target ([24-roadmap](24-roadmap.md)); a target is required from the release that completes that step.

| Target | Architecture | Platform | Formats | Step |
| --- | --- | --- | --- | --- |
| `dos-com` | `i8086` | `dos` | `Flat{Origin: 0x100, MaxSize: 65280}` | 13 |
| `stm32f103c8` | `armv7m` (Cortex-M3) | `bare` | ELF, BIN, Intel HEX | 15–16 |
| `web` | `wasm32` | `web` | wasm | 18 |
| `linux-x86_64` | `x86_64` | `linux` | ELF | 26 |
| `linux-arm64` | `arm64` | `linux` | ELF | 27 |
| `macos-arm64` | `arm64` | `macos` | Mach-O | 27 |
| `windows-x86_64` | `x86_64` | `windows` | PE | 28 |
| `atmega328p` | `avr` | `bare` | ELF, Intel HEX | 51 |
| `dos-exe` | `i8086` | `dos` | MZ | 52 |
| `rp2040` | `armv6m` (Cortex-M0+) | `bare` | ELF, UF2 | 52 |
| `esp32c3` | `riscv32` | `bare` | ELF, BIN | 52 |
| `msp430g2553` | `msp430` | `bare` | ELF, Intel HEX | 52 |
| `bios-boot` | `i8086` | BIOS boot sector | `BootSector` | 55 |
| `multiboot-x86_64` | `x86_64` | Multiboot | ELF | 55 |
| `uefi-x86_64`, `uefi-arm64`, `uefi-riscv64` | as named | UEFI | PE (UEFI application) | 55 |
| `devicetree-arm64`, `devicetree-riscv64` | as named | Device Tree | ELF, BIN | 55 |
| `android` | `arm64`, `x86_64` | `android` | APK, AAB | 65 |
| `ios`, `ios-sim` | `arm64`; `arm64`, `x86_64` | `ios` | `.ipa`; `.app` | 66 |

**Note** — The `rp2040` has two cores; until multi-core microcontrollers are specified ([14-embedded](14-embedded.md)), the target runs on core 0 only. A project adds other chips and boards as its own target packages (**target.pkg.location**).

## Open issues


1. **Format of architecture descriptions.** Deferred to implementation — step 11. Only the required content is specified (13.5.1); the structure of the constants and the notation of instruction semantics are fixed with the i8086 description.
2. **Field names of platform descriptions.** Deferred to implementation — step 13. The content is specified by **target.platform.desc**; field names are fixed with the first platform (`dos`).
