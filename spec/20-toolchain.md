# 20. Toolchain

All work with Anlang goes through one command, `anlang`, as in Go. This file specifies projects, the files the toolchain reads (`anlang.mod`, `REQUIREMENTS`), the commands, builds, tests, emulators, debugging, documentation, `anlang port` and `anlang diff`.

## 20.1 General requirements

**tool.single-command** — Every operation described in this file MUST be available as a subcommand of the single executable `anlang`.

**tool.net.none** — A `anlang` command MUST NOT access the network. All inputs are the project directory, the standard library and the files named on the command line.

**Note** — There is no package manager, no external package source and no dependency metadata. Foreign code is copied into the project and becomes project code (see [03-declarations](03-declarations.md)).

**tool.output.json** — Every command MUST support the flag `-json`. With it, the command MUST write a single machine-readable JSON result to standard output, whose schema is published with the specification (see [21-protocol](21-protocol.md)). Without it, the command writes human-readable text.

**tool.output.diagnostics** — Commands that report problems in source code MUST report them as diagnostics as defined in [21-protocol](21-protocol.md), **proto.diag.shape**, each naming the identifier of the violated rule.

**tool.exit-status** — A command MUST exit with status 0 on success, 1 when it completed but found problems (compile errors, failing tests, failed checks), and 2 on invalid usage or an internal error.

## 20.2 Projects

**tool.project.root** — A project is a directory that contains a file named `anlang.mod`. A command run inside a project MUST use the nearest ancestor directory (including the current one) that contains `anlang.mod` as the project root.

**tool.project.package** — A package is a directory under the project root that contains at least one `.an` file. Its import path is the module name followed by the directory path relative to the project root, with `/` as separator (see [03-declarations](03-declarations.md)). The project root itself is the package whose import path is the module name.

**tool.project.reserved** — The directory `out/` at the project root is reserved for build outputs and MUST NOT contain packages.

### 20.2.1 Layout

*Informative.*

```
weather/
├── anlang.mod
├── REQUIREMENTS
├── main.an
├── server/
│   ├── server.an
│   ├── server_linux.an
│   └── server_test.an
├── jsonparse/              // copied foreign library: an ordinary project package
├── internal/
└── out/                    // build outputs, one directory per target
```

## 20.3 anlang.mod

`anlang.mod` is a line-oriented file in the style of `go.mod`: one directive per line, `//` comments to end of line.

### 20.3.1 Grammar

*(new)*

```
AnlangModFile      = { newline } { Directive { newline } } .
Directive        = ModuleDirective | EditionDirective | TargetsDirective | EffectsDirective | GenerateDirective .

ModuleDirective  = "module" ModuleName newline .
ModuleName       = ascii_lower { ascii_lower | decimal_digit | "_" } .

EditionDirective = "anlang" Edition newline .
Edition          = decimal_digits "." decimal_digits .

TargetsDirective = "targets" TargetName { "," TargetName } newline .
TargetName       = ascii_lower { ascii_lower | decimal_digit | "-" | "_" } .

EffectsDirective = "effects" "(" newline { EffectRule } ")" newline .
EffectRule       = PackagePattern ( "allow" | "deny" ) EffectSet newline .
PackagePattern   = "..." | PackageDir [ "/..." ] .
PackageDir       = path_element { "/" path_element } .
path_element     = ascii_lower { ascii_lower | decimal_digit | "_" } .
EffectSet        = "{" [ EffectName { "," EffectName } ] "}" .
EffectName       = identifier .

GenerateDirective = "generate" GeneratorPath InputPattern { InputPattern } "->" OutputPath newline .
GeneratorPath    = ImportPath .
InputPattern     = PackageDir [ "/" FilePattern ] | FilePattern .
FilePattern      = { path_char | "*" } .
OutputPath       = ImportPath .

ascii_lower      = "a" … "z" .
```

Tokens are separated by spaces or tabs; `newline` is a line feed. `decimal_digit`, `decimal_digits` and `identifier` are as in the Go specification.

### 20.3.2 Rules

**tool.mod.module** — `anlang.mod` MUST contain exactly one `module` directive. The module name and the elements of package paths follow **decl.import.module** and **decl.import.paths** ([03-declarations](03-declarations.md)); in particular the name MUST NOT be `an`.

**tool.mod.edition** — `anlang.mod` MUST contain exactly one `anlang` directive. It fixes the language edition of the project (see 20.8).

**tool.mod.targets** — `anlang.mod` MAY contain one `targets` directive. It lists the default targets for `anlang build`, `anlang check` and `anlang test` when no `-target` flag is given. Each name MUST resolve to a target description (see [13-targets](13-targets.md)).

**tool.mod.effects** — `anlang.mod` MAY contain one `effects` directive. Patterns and the meaning of `deny` and `allow` rules, including precedence when several rules match, are specified in [07-effects](07-effects.md) (**eff.mod.pattern**, **eff.mod.deny**, **eff.mod.allow**, **eff.mod.precedence**).

**tool.mod.effects.names** — Every effect name in `anlang.mod` MUST name an effect known in the program: a built-in effect, an effect of the target's device description or an effect declared with `effect.Declare` (see [07-effects](07-effects.md)).

**tool.mod.generate** — `anlang.mod` MAY contain any number of `generate` directives. Each declares a generator ([10-compile-time](10-compile-time.md), §10.8): the generator package (a `main` package of the project), its input files as patterns relative to the project root, and the import path of the output package, which MUST be a path under the module name. Example: `generate gen/svdgen boards/stm32/*.svd -> myprogram/device`.

**tool.mod.generate.unique** — Two `generate` directives MUST NOT name the same output package, and the output package MUST NOT be a directory that exists in the project.

**tool.mod.format** — `anlang fmt` MUST also format `anlang.mod` canonically: directives in the order `module`, `anlang`, `targets`, `effects`, `generate`; effect rules sorted by pattern; effect names sorted; `generate` directives sorted by output path. Each single-line directive is written as its name padded with spaces to 8 columns followed by its value (`module  hello`, `anlang  1.0`, `targets linux-x86_64, macos-arm64`); target names are separated by ", ". The rules of the `effects` directive are written one per line between `effects (` and `)`, indented by a tab, with the patterns padded with spaces to the longest pattern plus one space, `deny` padded to the width of `allow`, and effect names separated by ", ". Whole-line comments stay before the directive that follows them, trailing comments at the end of their directive's line; blank lines are removed.

### 20.3.3 Example

*Informative.*

```
module  weather
anlang  1.0
targets linux-x86_64, macos-arm64, web

effects (
	jsonparse/... deny  {File, Network, Panic}
	kernel/...    allow {Privileged}
)
```

## 20.4 File selection

File selection is specified in [03-declarations](03-declarations.md) §3.4 (**decl.file.ext**, **decl.file.ignored**, **decl.file.suffix**, **decl.file.names**, **decl.file.profile**). Test files (`_test.an`) are included only when tests are built: by `anlang test` and by the `test` operation of the protocol ([21-protocol](21-protocol.md)).

**Note** — Import restrictions on architecture instruction packages (`an/arch/<architecture>` only in files with the matching suffix) are specified in [13-targets](13-targets.md).

## 20.5 REQUIREMENTS

`REQUIREMENTS` is an optional line-oriented file at the project root. It holds requirement texts and the tests that check each requirement. The text is free-form and written for humans; it MAY be in any language.

### 20.5.1 Grammar

*(new)*

```
RequirementsFile = { BlankOrComment } { Requirement { BlankOrComment } } .
Requirement      = RequirementID spaces Text newline { indent Text newline } indent TestsLine newline .
RequirementID    = ascii_upper { ascii_upper | decimal_digit } "-" decimal_digit { decimal_digit } .
TestsLine        = "tests:" spaces TestRef { "," [ spaces ] TestRef } .
TestRef          = [ PackageDir "." ] TestName .
TestName         = "Test" { letter | unicode_digit } .
Text             = { unicode_char } .
BlankOrComment   = [ spaces ] [ "//" Text ] newline .
indent           = spaces .
spaces           = ( " " | "\t" ) { " " | "\t" } .

ascii_upper      = "A" … "Z" .
```

### 20.5.2 Rules

**tool.req.unique** — Requirement identifiers MUST be unique within the file.

**tool.req.has-tests** — Every requirement MUST list at least one test.

**tool.req.test-exists** — Every test reference MUST name an existing test function: a function in a `_test.an` file whose name starts with `Test` and whose signature is `func(t inout testing.T)`. A reference without a package directory MUST match exactly one test function in the project.

**tool.req.test-passes** — When `anlang test` runs all tests of the project, every test referenced by a requirement MUST pass.

**tool.req.check** — `anlang test` MUST check **tool.req.unique**, **tool.req.has-tests** and **tool.req.test-exists** on every run, and **tool.req.test-passes** when it runs all tests of the project. A violation MUST be reported as a diagnostic naming the requirement and MUST make `anlang test` fail.

**Note** — Code that no requirement covers is allowed and is not reported.

### 20.5.3 Example

*Informative.*

```
// REQUIREMENTS
AUTH-3  The user logs in with a login and a password
        tests: TestLoginSuccess, TestLoginWrongPassword
AUTH-4  After five failed attempts, login is locked for 15 minutes
        tests: auth.TestLockAfterFiveFailures, auth.TestUnlockAfter15Minutes
```

## 20.6 Commands

**tool.cmd.set** — `anlang` MUST provide the following commands.

| Command | Purpose |
| --- | --- |
| `anlang build [-target T[,T…]] [-verify] [-ir]` | Build the program for one or more targets |
| `anlang check [-target T[,T…]]` | Check types, effects and contracts without generating code |
| `anlang fmt [-check] [paths]` | Canonical formatting |
| `anlang test [-target T] [-emu E] [packages]` | Unit, property-based and visual tests; requirement checks |
| `anlang run [-target T] [-emu E] [-screen] [-gpio] [-for D] [-record F]` | Build and run, on the host or in an emulator |
| `anlang flash [-target T] [-probe P]` | Write a firmware image to a device over SWD/JTAG or UF2 |
| `anlang size [-target T]` | Flash, RAM and stack usage by package |
| `anlang query <query>` | Queries to the semantic API |
| `anlang diff <old> <new>` | Semantic diff of two versions of code |
| `anlang debug [-target T] [-emu E] [-probe P] [-replay F]` | Debugging on the host, in an emulator or on a device |
| `anlang doc [-html] [packages]` | Documentation |
| `anlang dev [-target T]` | Run with hot reload |
| `anlang port -from go\|c <source> <dest>` | Port code from Go or C |
| `anlang gen -show <package>` | Print the source of a generated package |
| `anlang serve` | Environment protocol server (see [21-protocol](21-protocol.md)) |
| `anlang lsp` | Language Server Protocol server (see [21-protocol](21-protocol.md)) |

**tool.cmd.build** — `anlang build` MUST check the program (as `anlang check`) and, if there are no errors, write the outputs for each target to `out/<target>/`. If no `-target` is given, the targets of the `targets` directive are used; if there is none, the host target is used.

**tool.cmd.build.ir** — `anlang build -ir` MUST check the program and print its intermediate representation for each target in the textual format of [22-compiler](22-compiler.md), 22.10, instead of writing outputs.

**tool.cmd.build.multi** — When several targets are given, target-independent checks (types, effects without target substitution, contracts) MUST be performed once; target-dependent steps once per target.

**tool.cmd.check** — `anlang check` MUST perform all checks of `anlang build` that precede code generation, including effect checks against each selected target, and MUST NOT write outputs.

**tool.cmd.fmt** — `anlang fmt` MUST rewrite each Anlang source file and `anlang.mod` in canonical formatting (see [01-lexical](01-lexical.md)). With `-check`, it MUST NOT modify files and MUST fail if any file is not canonically formatted. Formatting MUST be idempotent.

**tool.fmt.style** — The canonical form of an Anlang source file is the output of `anlang fmt`. For the part of Anlang shared with Go it is the output of `gofmt` (Go 1.25): tabs for indentation, spaces for alignment, imports sorted within each run of consecutive import lines, numeric literal prefixes and exponents in lower case. The Anlang constructs are laid out as follows: the profile clause on its own line after the package clause, separated by blank lines; enum variants one per line; struct clauses (`invariant`, `layout`) before the fields, in their own alignment section; contract clauses as statements; an external function binding `= expr` on the line of the signature; handler and GPU function declarations like function declarations.

**tool.fmt.fixed-point** — The canonical form MUST be a fixed point: formatting it again MUST give the same bytes. `anlang fmt` formats repeatedly until the output no longer changes. Where a layout heuristic of `gofmt` depends on the source layout and makes `gofmt` non-idempotent, `anlang fmt` decides by the output layout instead: a comment that starts on the line of a struct field or of a spec in a group is that line's trailing comment; the last statement of a block is its last non-empty statement; only `\n` ends a line inside a literal.

**tool.fmt.differences** — `anlang fmt` does not reformat doc comments, does not rewrite `//go:build`, `// +build` or `//line` comments (they have no meaning in Anlang, **lex.comment.inert**), and does not remove duplicate imports (a duplicate import is a compile error).

**tool.cmd.size** — `anlang size` MUST report, for each package, the flash (code and constant data) and RAM (static data) it contributes, and, for `main`, each task and each interrupt handler, the computed maximum stack depth or the fact that a reserve was needed (see [19-runtime](19-runtime.md), **rt.stack.report**).

**tool.cmd.query** — `anlang query` MUST answer the queries of the semantic API: the callers of a function; the effects reachable from a point and the call chain that introduces each; what breaks if a type or function changes; which contract condition is not proven and why. The same queries are available as the `query` operation of the protocol (see [21-protocol](21-protocol.md)).

**tool.cmd.gen** — `anlang gen -show <package>` MUST run the generator of the given output package if its cached output is stale, and print the generated source files in canonical formatting. Without `-show`, `anlang gen` MUST bring all generated packages up to date in the build cache without building the program.

**tool.cmd.flash** — `anlang flash` MUST first build the target as `anlang build` does, then write the image of the `core` target to a device through a debug probe (SWD or JTAG) or, for targets with the UF2 format, by writing the UF2 file.

## 20.7 Builds

**tool.build.offline** — Building MUST use only the project, the standard library and the toolchain. See also **tool.net.none**.

**tool.build.reproducible** — Given the same project sources, the same `anlang` version and the same target, `anlang build` MUST produce byte-identical outputs, independent of the time, the absolute path of the project, the machine and the environment variables.

**tool.build.no-stamps** — Outputs MUST NOT contain timestamps or file-system paths of the build machine.

**tool.build.verify** — `anlang build -verify` MUST build each target twice, from scratch and independently of the build cache, and fail if the outputs differ in any byte.

**tool.build.outputs** — The outputs of a target MUST be written only under `out/<target>/`. The set and names of files are given by the target's binary formats (see [13-targets](13-targets.md)) and the debug side table (see 20.11).

**Note** — Caching of build results is described in [22-compiler](22-compiler.md); the cache never changes the result (**impl.cache.transparent**).

## 20.8 Editions

**tool.edition.fixed** — The `anlang` directive in `anlang.mod` fixes the edition. The compiler MUST apply the rules of that edition to the whole project.

**tool.edition.migrate** — A `anlang` version that supports a newer edition MUST provide automatic migrations from each older supported edition. Migrations MUST be applied only on an explicit request (the `-edition` flag of `anlang fmt`), MUST update the `anlang` directive and MUST produce code that compiles under the new edition.

**tool.edition.codes** — Rule identifiers used in diagnostics MUST be stable within an edition (see [21-protocol](21-protocol.md), **proto.stability**).

## 20.9 Testing

**tool.test.files** — Tests are functions with signature `func(t inout testing.T)` whose name starts with `Test`, declared in `_test.an` files, as in Go. Package `an/testing` provides `testing.T`.

**tool.test.examples** — Code examples in documentation comments MUST be compiled and executed as tests by `anlang test`. An example with an expected-output comment MUST match that output.

**tool.test.property** — `anlang test` MUST support property-based tests: a test receives generated inputs from `an/testing` and, on failure, reports a minimized counterexample. Generation MUST be deterministic for a given seed, and the seed MUST be reported.

**tool.test.substitution** — In tests, the sources of the `Clock` and `Random` effects and of I/O MUST be replaceable by test implementations. Because effects are inferred, the compiler knows which code depends on them; substitution MUST NOT require changes to the code under test.

**tool.test.scheduler** — Tests that start tasks MUST be able to run under the deterministic scheduler, which enumerates interleavings of tasks to find deadlocks and ordering errors (see [09-concurrency](09-concurrency.md)). A failure MUST be reported with the interleaving that reproduces it.

**tool.test.visual** — A test MAY render a UI component and compare the image with a reference image stored in the project. On mismatch, `anlang test` MUST fail and write the actual image and a difference image to `out/`.

**tool.test.emu** — With `-target` and `-emu`, `anlang test` MUST run tests on the target inside a built-in emulator (see 20.10).

**tool.test.requirements** — `anlang test` MUST perform the requirement checks of 20.5.

## 20.10 Emulators

**tool.emu.builtin** — Emulators for tests MUST be built into `anlang` and MUST NOT require external programs.

**tool.emu.generated** — The interpreter of each architecture MUST be generated from the formal instruction semantics in its architecture description, the same description from which the backend, the assembler and the architecture instruction package are built (see [13-targets](13-targets.md)). The interpreter of an architecture with structured control flow is built in instead and is checked against the test suite of the architecture (**target.arch.structured**, **tool.emu.wasm**).

**tool.emu.ir** — `anlang run -emu ir` MUST run the program in the IR interpreter (see [22-compiler](22-compiler.md), 22.10.3) for any target, without generating machine code. Its observable behavior is the reference for the backends of all targets (see [23-conformance](23-conformance.md), **conf.backend.ir**). A program that panics MUST exit with status 2.

**tool.emu.target** — `anlang run -target T` for a target that cannot run on the host, and `anlang run -emu builtin` for any target with a built-in emulator, MUST build the program and run it in the built-in emulator of the target. The program's standard output and standard error go to those of `anlang`, and `anlang` exits with the program's exit status.

**tool.emu.dos** — The built-in emulator of the `dos` targets runs the 8086 interpreter with models of DOS and of the PC. It loads a `.COM` image at offset `0x100` of a segment with a program segment prefix whose first bytes are `int 20h`, sets `CS`, `DS`, `ES` and `SS` to that segment and `SP` to `0xFFFE` with a zero word on the stack, and models:

1. the text screen: 80×25 cells of a character and an attribute at physical address `0xB8000`, with the cursor in the BIOS data area; output of DOS to the console is written to the screen as by the BIOS teletype, and to the standard output or standard error of `anlang`;
2. `int 10h`: functions `00h` (set the mode: clears the screen), `01h`, `02h`, `03h` (cursor), `06h`, `07h` (scroll a window), `08h`, `09h`, `0Ah` (read and write characters at the cursor), `0Eh` (teletype) and `0Fh` (mode);
3. the keyboard: each byte of the standard input of `anlang` is a key press, a line feed the Enter key; `int 16h` functions `00h`, `01h` (with the scan codes of a US keyboard), `02h`;
4. the timer: time runs with the executed clock cycles of a 4.77 MHz 8086 from midnight of 1 January 2026; `int 1Ah` function `00h` returns the ticks, 18.2 per second;
5. `int 20h` (exit with status 0) and these functions of `int 21h`: `01h`, `07h`, `08h` (read a character; the end of input reads as `1Ah`), `02h`, `06h`, `09h`, `40h` (write; handle 1 is standard output, 2 standard error), `0Bh` (input status), `2Ah`, `2Ch` (date and time), `30h` (version 5.0), `3Fh` (read from handle 0), `4Ch` (exit with the status in `AL`).

`anlang run -screen` writes the text screen after the program ends: its rows without trailing spaces, without trailing empty rows (**tool.emu.observable**). Limits (**tool.emu.limits**): other interrupts and functions stop the emulation with an error; there are no hardware interrupts, graphics modes or files; timing is the cycle count of the description, not measured hardware.

**tool.emu.cortexm** — The built-in emulator of the Cortex-M targets of platform `bare` runs the interpreter of the architecture with the memory regions of the target and models of its peripherals. It loads the BIN image at the start of the `Exec` region, sets `SP` and `PC` from the first two words of the vector table, and models:

1. the semihosting calls of a debugger (`BKPT 0xAB`): `SYS_OPEN` of the console `:tt` (modes 0 to 3 open standard input, 4 to 7 standard output, 8 to 11 standard error), `SYS_CLOSE`, `SYS_WRITEC`, `SYS_WRITE0`, `SYS_WRITE`, `SYS_READ`, `SYS_ISTTY`, `SYS_EXIT` and `SYS_EXIT_EXTENDED`, whose status is 0, or the code for `SYS_EXIT_EXTENDED`, when the reason is `ADP_Stopped_ApplicationExit`, and 1 otherwise;
2. the registers of the device description (**emb.device.schema**), each holding its value from its reset value; an access to an address of a peripheral without a register stops the emulation with an error;
3. time, which runs with the executed cycles at the clock of the processor: on the STM32F1, the clock that RCC selects from the 8 MHz internal oscillator, an 8 MHz crystal or the PLL, divided by the AHB prescaler; the oscillators and the PLL are ready as soon as they are on, and the timers on APB1 run at its clock, or twice it if it is divided;
4. GPIO: the output data register, set and reset by `BSRR` and `BRR`, and the changes of the pins configured as outputs with their time; the input data register reads the output data;
5. the general-purpose timers: the prescaler, the counter up to the auto-reload value, the update event with its flag, `UG`, and the interrupt it sets pending;
6. USART: the bytes written to the data register while the port and its transmitter are enabled are its output, which goes to the standard output of `anlang`; the receiver reads the standard input; the transmit register is always empty;
7. SysTick, counting the processor clock or an eighth of it, with its count flag and its exception pending state; the interrupt controller with the enable and pending registers of the interrupts; a system reset request through `AIRCR`;
8. `WFI` and `WFE`: the processor sleeps until an enabled interrupt or the SysTick exception is pending, which wakes it even while `PRIMASK` masks it, and the time advances to the next event of a peripheral that sets one pending;
9. exceptions: while `PRIMASK` is clear and no exception is active, the pending exception with the lowest number (SysTick before the interrupts, an interrupt before those with higher numbers) is taken: the processor pushes `R0`–`R3`, `R12`, `LR`, `PC` and `xPSR` on the main stack, sets `LR` to `EXC_RETURN` and continues at its vector; a branch to an `EXC_RETURN` address pops them. Exceptions do not preempt each other.

`anlang run -gpio` writes the changes of the output pins after the run, one per line: the time in seconds with nine decimals, the pin, such as `PC13`, and `high` or `low` (**tool.emu.observable**); `anlang run -for D` ends the run of a program that does not end when the time of the model reaches the duration `D`, such as `3s`, with status 0. Limits (**tool.emu.limits**): exceptions have one priority, the priority registers are not modeled (roadmap step 33), and the process stack and the faults are not modeled; the other peripherals and the other behavior of the modeled ones have only their registers; other semihosting calls, `BKPT` with another number, `SVC`, `UDF` and a system reset stop the emulation with an error; timing is the cycle count of the description.

**tool.emu.wasm** — The built-in emulator of the target `web` is the WebAssembly interpreter of the toolchain, an implementation of WebAssembly 2.0 with the fixed-width vector instructions. It decodes and validates the module, compiles each function once into instructions over a stack of 64-bit slots with the targets of its branches, and runs `_start` with the functions of WASI of **target.platform.web**: `fd_write` to the standard output and standard error of `anlang`, `fd_read` from its standard input, and `proc_exit`, whose code is the exit status. It also reads the text format of WebAssembly and runs the scripts of the specification test suite (`.wast`), its oracle (**conf.oracle.use**). Limits (**tool.emu.limits**): the call stack has at most 50000 frames and 2^24 slots, beyond which the call traps as an exhausted stack; a memory grows to at most 65536 pages and a table to 2^24 elements; the other functions of WASI and the proposals after WebAssembly 2.0 are not implemented.

**tool.emu.coverage** — `anlang` MUST provide at least:

| Target | Built-in emulation |
| --- | --- |
| 8086, Cortex-M, AVR, RISC-V | architecture interpreter plus peripheral models: timer, UART, GPIO, text video memory, `int 21h` |
| `web` | WebAssembly interpreter; the interface is rendered by the Anlang renderer into an image, no browser required |
| iOS, Android | logic and interface checked on the developer machine with the same renderer |

**tool.emu.observable** — Emulator tests check observable behavior: pin changes with their timing, UART output, text on the DOS screen, rendered screens. The emulator MUST expose these observations to tests through `an/testing`.

**tool.emu.limits** — Built-in emulators are not required to be cycle-accurate, to model peripherals completely, or to check system integration (camera, permissions, behavior of the real iOS). The documentation of each emulator MUST state its limits. External emulators (DOSBox, QEMU, Renode), the Apple and Android simulators and real devices MAY be used through `anlang run` and `anlang debug`, but are never required by `anlang test`.

## 20.11 Debugging

**tool.debug.formats** — Builds for debugging MUST emit debug information in the format of the target:

| Target | Debug information | Debugger |
| --- | --- | --- |
| Linux, macOS | DWARF | `anlang debug`; GDB and LLDB through their remote protocols |
| Windows | PDB / CodeView | as above |
| DOS | CodeView | through the DOSBox debug port |
| `web` | source maps, DWARF for WebAssembly | browser tools |
| microcontrollers | DWARF + SVD | SWD/JTAG through `anlang debug -probe` |

**tool.debug.side-table** — On every target, including `core`, the build MUST produce a side table, in a separate file in `out/<target>/`, that maps code addresses to functions, files and lines. The side table MUST NOT be part of the program image.

**tool.debug.values** — The debugger MUST display values of Anlang types in Anlang terms: an `enum` with its variant and fields, `Option` as `None` or `Some`, synchronization types as handles. A `Secret` value MUST be displayed as `***`.

**tool.debug.record** — `anlang run -record F` MUST record all nondeterministic inputs of the run (clock, randomness, I/O, task interleaving) to file `F`. `anlang debug -replay F` MUST reproduce the run exactly.

**tool.debug.protocol** — The debugger MUST be available through the `debug` operation of the environment protocol: breakpoints, stepping, reading state (see [21-protocol](21-protocol.md)).

## 20.12 Hot reload

**tool.dev.hot-reload** — `anlang dev` MUST run a `ui` program and apply changes to its `View` and `Update` functions without restart, preserving the state. If the `State` type changes, `anlang dev` MUST offer a migration function or a restart. The mechanisms per target are specified in [17-ui](17-ui.md).

**tool.dev.release** — Hot reload MUST NOT be available in release builds (see [19-runtime](19-runtime.md), **rt.dev.only**).

## 20.13 Documentation

**tool.doc.content** — `anlang doc` MUST generate documentation from source: for each exported declaration, its signature, inferred effects (including parametric references, see [07-effects](07-effects.md)), contracts, documentation comment and examples.

**tool.doc.formats** — `anlang doc` MUST produce HTML with `-html` and JSON with `-json`. The JSON form is intended for AI tools and carries the same information.

**tool.doc.examples** — Examples in documentation are executable tests (**tool.test.examples**).

## 20.14 anlang port

`anlang port` translates code from Go or C to Anlang. An AI model performs the translation; the compiler checks the result; the tests of the source project are run against both versions.

**tool.port.check** — The result of `anlang port` MUST be checked by the compiler. `anlang port` MUST report every diagnostic of the ported code.

**tool.port.tests** — When the source project has tests, `anlang port` MUST port them and run them against both the original and the ported version, and MUST report every test whose result differs.

**tool.port.go.auto** — When porting from Go, `anlang port` MUST translate automatically: syntax, structures, interfaces, goroutines and channels, error handling, and import paths of the Go standard library (`"fmt"` → `"an/fmt"`) and of the ported project (`"example.com/x/y"` → `"<module>/y"`).

**tool.port.go.decide** — When porting from Go, `anlang port` MUST report as requiring a decision: pointers and nil pointers, `init` functions, values of type `interface{}` / `any`, and mutable package-level variables (which it MAY wrap in `sync.Mutex[T]` or move into a structure, see [06-memory](06-memory.md)).

**tool.port.c** — When porting from C, `anlang port` MUST translate functions, structures and arithmetic, and MUST report as requiring a decision: manual memory management, preprocessor use and code whose behavior is undefined in C.

## 20.15 anlang diff

**tool.diff.inputs** — `anlang diff <old> <new>` compares two versions of code: two directories, or two package sets of the same project at two file-system states.

**tool.diff.content** — The result MUST list, per package:

- added, removed and changed exported declarations, with old and new signatures;
- functions whose implementation changed while the signature did not;
- changes of inferred effects of each function, including new parametric references;
- changes of contracts: weakened or strengthened `requires` and `ensures`;
- changes of external function declarations (`ffi`), including their declared effects, listed separately because the compiler cannot check them (see [12-ffi](12-ffi.md)).

**tool.diff.effects-highlight** — A new effect of an exported function MUST be marked prominently in the human-readable output.

### 20.15.1 Example

*Informative.*

```
anlang diff old/ new/

jsonparse

API:
  + func DecodeStream(r io.Reader) (Value, error)
  ~ func Parse(s string) (Value, error)        implementation changed

Effects:
  ! Parse: {} → {Network}                    NEW EFFECT
```
