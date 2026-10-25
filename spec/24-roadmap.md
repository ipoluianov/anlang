# 24. Roadmap

*Informative.*

The plan covers the whole environment. It is split into small steps, each of which can be built and checked on its own. The compiler is written in Go; after the self-hosting steps (phase 9) it lives in Kern.

## 24.1 Working rules

1. **A step is a unit of work and of checking.** Every step has an automated acceptance criterion; a step that depends on it does not start until the criterion is met.
2. **Every step extends the permanent test suite**: the conformance suite, reference programs, fuzzing (see [23-conformance](23-conformance.md)). A regression of any earlier test blocks merging.
3. **Where an independent implementation exists, it is the oracle.** Go for the common subset, the IR interpreter for backends, QEMU and processor test suites for architecture interpreters, the official WebAssembly test suite. External tools are used only in the tests of the environment and are not shipped.
4. **Every "proved" of the solver is confirmed by a certificate**, checked by a small independent checker.
5. Until the built-in interpreters are ready, DOSBox and QEMU are acceptable in the first steps.

## 24.2 Phase 1. Language core

| # | Step | Done when |
| --- | --- | --- |
| 1 | Lexer, parser and canonical formatting for all of Go's syntax and the novelty inventory | a corpus of Go code parses; `kern fmt` is idempotent; parse → print → parse gives the same tree; fuzzing without crashes |
| 2 | Type checking of the Go subset: structures, functions, methods, interfaces, generics, closures | conformance suite "compiles / error with rule X" for every rule |
| 3 | `enum`, patterns in `case`, exhaustive `switch` | as above; adding a variant breaks every incomplete `switch` in the tests |
| 4 | Value semantics: `inout`, second-class references, exclusive access | negative tests for every violation with the expected rule identifier |
| 5 | `nil` without dereference, zero values, the `enum` zero value rule | flow analysis tests: a method call on an empty interface does not compile, after a check it does |
| 6 | Shared SSA representation, textual IR format, IR interpreter | reference programs run in the IR interpreter; output matches Go on the common subset |
| 7 | Overflow and bounds checks, packages `bits` and `wrap` | overflow tests for every integer type; `wrap` results match Go |
| 8 | Effect inference: per-package summaries, parametric references, whole-program substitution | effect test suite; incremental recomputation matches full recomputation |
| 9 | Profiles and effect constraints at boundaries | negative tests for every profile and `effect.Without` |
| 10 | Compile-time evaluation, `for const`, `meta`, `...any` arguments | the CRC table matches the reference; limits trigger; `fmt` tests with compile-time format checking |

## 24.3 Phase 2. First targets

| # | Step | Done when |
| --- | --- | --- |
| 11 | Format of architecture descriptions and formal instruction semantics; description of i8086 | the description type-checks; all instructions needed for .COM are covered |
| 12 | Generation of the interpreter, assembler and instruction package from the description | the interpreter agrees with the 8086 processor test suite and with QEMU on random instructions; assembler ↔ disassembler round-trips |
| 13 | Instruction selection by rules, i8086 backend, .COM format | reference programs in the 8086 interpreter give the same result as the IR interpreter |
| 14 | DOS peripheral models: `int 21h`, video memory, keyboard, timer; packages `dos`, `bios` | the video memory example passes; observable-behavior tests |
| 15 | ARMv7-M: description, interpreter, backend, ELF, BIN, Intel HEX | as steps 12–13, for Cortex-M |
| 16 | Register generator from SVD, description of `stm32f103c8`, timer, UART and GPIO models | the LED example toggles the pin at the right frequency in the model |
| 17 | `interrupt`, vector table, peripheral ownership through effects, stack analysis | negative tests for ownership and stack; the initialization phase before `interrupts.Enable()` |
| 18 | wasm32: backend and WebAssembly interpreter | the interpreter passes the official specification test suite; the counter works in a browser |
| 19 | Prototype success criterion | the same function, unchanged, works in DOS, on Cortex-M and in WebAssembly in the built-in interpreters |

## 24.4 Phase 3. Solver and contracts

| # | Step | Done when |
| --- | --- | --- |
| 20 | Solver stage 1: linear integer arithmetic; certificates and the independent checker | every "proved" is confirmed by the checker; on small domains results match exhaustive enumeration |
| 21 | `requires`, `ensures`, `assert`; removal of proven checks | in typical loops, bounds checks are absent from the output; counterexamples are shown |
| 22 | Stage 2: bit vectors | absence of overflow is proven in typical loops; register masks are checked |
| 23 | Stage 3: arrays and slices, loop and type `invariant` | the `Sum` example is proven; type invariants are checked at function boundaries |
| 24 | Stages 4–5: `enum`, `Option`, bounded quantifiers | a set of contracts with quantifiers is proven within the step limit |

## 24.5 Phase 4. Runtime and host targets

| # | Step | Done when |
| --- | --- | --- |
| 25 | `app` allocator, copy-on-write, arenas | allocator fuzzing; the memory-checking mode of tests finds no errors; access to a removed arena node is caught |
| 26 | x86-64: description, interpreter, backend; ELF and Linux system calls | reference programs agree with the IR interpreter and run on a real machine |
| 27 | arm64: Linux and macOS, Mach-O format | as above; binaries run on both operating systems |
| 28 | Windows x86-64, PE/COFF format | as above on Windows |
| 29 | `app` standard library: strings, maps, `fmt`, `os`, `fs`, `time` | package tests; output compared with Go on the common subset |

## 24.6 Phase 5. Concurrency

| # | Step | Done when |
| --- | --- | --- |
| 30 | Structured tasks, channels, `select`, synchronization types, deterministic scheduler | the deterministic scheduler finds planted deadlocks and ordering races |
| 31 | M:N scheduler on Linux (epoll), cancellation and deadlines | stress tests; no task outlives its parent |
| 32 | `inout` in `go` with proof of disjointness, `task.Detach` | `SortParallel` compiles; overlapping parts are rejected |
| 33 | Priorities, inheritance, priority ceiling; static tasks on Cortex-M, cooperative tasks in DOS | priority inversion tests; tasks work on all three first targets |
| 34 | kqueue, IOCP, io\_uring | I/O tests on macOS and Windows |

## 24.7 Phase 6. External code

| # | Step | Done when |
| --- | --- | --- |
| 35 | `ffi.Library`, callbacks on Linux, macOS, Windows | SQLite's own test suite passes through `ffi` |
| 36 | `ffi.Static`, object file linker for ELF, Mach-O, COFF; `ffi.At` | zlib links statically; compression results match the reference |

## 24.8 Phase 7. Tools

| # | Step | Done when |
| --- | --- | --- |
| 37 | Environment protocol (JSON-RPC), JSON diagnostics with fixes | every diagnostic of the conformance suite has a rule identifier; an applied fix yields compiling code |
| 38 | `kern lsp` | editor scenarios over LSP: errors, go to definition, effects on hover |
| 39 | `kern test`: property-based tests, `Clock` and `Random` substitution, record and replay | replay of a recorded run is deterministic |
| 40 | `kern doc` with executable examples | the documentation examples of the standard library pass as tests |
| 41 | DWARF, CodeView/PDB, `kern debug` locally and through the protocol | debugging scenarios through the protocol: breakpoint, step, reading `enum`, `Option`, `Secret` |
| 42 | `kern flash`, `kern debug -probe` (SWD/JTAG) | test rig with real boards: flashing, breakpoint, reading registers |
| 43 | Reports: `kern size`, memory map, stack depth | reports match the references for the reference programs |
| 44 | `kern diff`: semantic diff of two versions of code | changes of API, effects and contracts are found on a set of reference version pairs |
| 45 | `REQUIREMENTS`: links between requirements and tests | `kern test` reports requirements without tests, missing tests and failing tests |
| 46 | `kern port` for Go and C | a set of real Go packages is ported; their tests pass on both versions |

## 24.9 Phase 8. Data safety

| # | Step | Done when |
| --- | --- | --- |
| 47 | `Secret`, `Untrusted`, safe sinks in `sql`, `html`, `fs` | negative tests for every vulnerability class of [11-data-safety](11-data-safety.md) |
| 48 | `secret.ConstantTime()`: branches, then indexed memory access | the standard library's cryptographic primitives pass the check |

## 24.10 Phase 9. Self-hosting

| # | Step | Done when |
| --- | --- | --- |
| 49 | The compiler is ported from Go to Kern and built by the Go version | the Kern compiler passes the whole test suite |
| 50 | The Kern compiler builds itself | byte-identical results of stages 1 and 2 ([22-compiler](22-compiler.md), 22.9) |

## 24.11 Phase 10. Embedded systems and operating systems

| # | Step | Done when |
| --- | --- | --- |
| 51 | AVR with pointers to flash and RAM | reference programs in the AVR interpreter |
| 52 | RISC-V 32 and 64, MSP430; formats MZ (.EXE) and UF2 | reference programs in the interpreters; UF2 flashes on RP2040 |
| 53 | WCET: cycle model, `wcet.Within` | the estimate is never below the time measured in an interpreter with an exact cycle model, over the whole set |
| 54 | OS: `trap`, `Privileged`, `PhysAddr`/`VirtAddr`, `abi.Table` | negative tests for the kernel/user boundary |
| 55 | Boot targets: BIOS, Multiboot, UEFI, Device Tree | a minimal kernel boots in QEMU and handles page faults and system calls |

## 24.12 Phase 11. User interfaces

| # | Step | Done when |
| --- | --- | --- |
| 56 | The `ui` profile: `Update`/`View`, `ui.Node`, layout engine in pixels and character cells | layout snapshots match the references |
| 57 | Text renderer: terminal, DOS 80×25 | screens match the references in the DOS interpreter and in a terminal |
| 58 | Software rasterizer: shapes, images, fonts | pixel comparison with the references within tolerance |
| 59 | Text shaping: complex scripts, bidirectional text | results match a reference shaping test suite |
| 60 | GPU renderer: WebGPU, Vulkan, Metal, Direct3D | frames match the software rasterizer within tolerance |
| 61 | Input, focus, IME, accessibility (ARIA, VoiceOver, TalkBack) | the accessibility tree matches the reference; input scenarios through the protocol |
| 62 | Web and desktop: browser, windows on Linux, macOS, Windows | reference applications work on each platform |
| 63 | Hot reload | a change to `View` is applied without losing state in a test scenario |

## 24.13 Phase 12. GPU computing

| # | Step | Done when |
| --- | --- | --- |
| 64 | `gpu func` → SPIR-V, WGSL, MSL; `gpu.Out`, `gpu.Dispatch` | kernel results match execution of the same code on the CPU; output passes the format validators |

## 24.14 Phase 13. Mobile platforms

| # | Step | Done when |
| --- | --- | --- |
| 65 | Android: JNI bridge, APK and AAB, permissions from effects, lifecycle | a reference application passes in the Android emulator; the manifest matches the reference |
| 66 | iOS: Objective-C runtime, .ipa, Info.plist, signing | a reference application passes in the simulator and on a device |
| 67 | Device capabilities: `Camera`, `Location`, `Notifications` and others | permissions are derived from effects; scenarios on both platforms |
| 68 | Hot reload on an iOS device through the WebAssembly interpreter | a change to `View` is applied on the device in a development build |

## 24.15 Planning questions

- Syntax of the `asm` block and the full set of functions of package `unsafe`.
- General-purpose allocator for `app`: own or ported (needed by step 25).
- Textual IR format for debugging and tests.
- Format of architecture descriptions: structure of the constants and description of instruction semantics.
- Composition of the standard library in the prototype.
- Catalog of diagnostics and their stability rules (partly answered by rule identifiers, see [README](README.md)).
- License of the project.
