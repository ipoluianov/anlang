# 22. Compiler

This file describes how the `anlang` compiler is built. It is *informative*, except for the rules with identifiers, which are normative implementation requirements.

## 22.1 Normative requirements

**impl.single-binary** — The toolchain MUST be a single executable file. It MUST NOT require an external compiler, assembler, linker, solver or SDK of another language to build, check, test or run Anlang programs. The only exception is code signing of iOS applications with Apple tools (see [17-ui](17-ui.md)).

**impl.no-external-solver** — The contract solver MUST be part of the toolchain. External solvers (for example, Z3 or cvc5) MUST NOT be used.

**impl.cache.transparent** — Using the build cache MUST NOT change any output, diagnostic or protocol result. A build from an empty cache and a build with a full cache MUST produce byte-identical outputs.

**impl.incremental** — After a change to a single function, re-checking SHOULD take a fraction of a second on a typical project.

**impl.abi.external** — External functions and callbacks MUST use the C ABI of the target, as given by its architecture and platform descriptions (see [12-ffi](12-ffi.md), [13-targets](13-targets.md)).

**impl.abi.unstable** — The internal ABI of Anlang is not stable between compiler versions. Since every package is always compiled from source, no compiled form of a package is ever reused across compiler versions; the build cache key includes the compiler version (22.7).

**impl.bootstrap.archive** — The compiler version written in Go MUST be preserved, so that the toolchain can be bootstrapped from scratch and checked for compiler backdoors (22.9).

## 22.2 Implementation language

The prototype is a single executable `anlang` written in Go without cgo. After the self-hosting steps (22.9), the compiler is written in Anlang. The compiler sees the whole program — all code lives in the project and the standard library — but caches results per package.

## 22.3 Pipeline

1. **Parsing** of each file into a syntax tree with canonical formatting (see [01-lexical](01-lexical.md)).
2. **Typed tree**: name resolution, type inference and checking, generics, `enum`, exhaustive `switch`.
3. **Effects**: per-package effect summaries, then whole-program substitution (22.5).
4. **Checks**: effects against profiles, `anlang.mod` and the target; contracts with the built-in solver; exclusivity of `inout`; peripheral ownership; `nil` safety analysis.
5. **Shared SSA representation** with effects, the same for all targets.
6. **Architecture-independent optimizations**.
7. **Lowering** to the architecture by the declarative instruction selection rules of its description.
8. **Writing the binary format**: flat, MZ, ELF, PE, Mach-O, wasm, Intel HEX, UF2, BIN.
9. **Reports**: size by package, stack depth, memory map.

## 22.4 Implementation packages

| Package | Responsibility |
| --- | --- |
| `syntax` | lexer, parser, tree, canonical formatting |
| `types` | name resolution, type checking, generics, `enum`, exhaustive `switch` |
| `effects` | effect inference, boundary checks |
| `verify` | proof obligations from contracts, built-in solver, certificate checker |
| `ir` | shared SSA representation with effects |
| `lower` | value semantics, copy-on-write, `enum`, overflow and bounds checks |
| `arch/...` | architecture descriptions; from them: instruction selection, assembler, instruction packages, interpreters |
| `link` | linker and format writers: flat, MZ, ELF, PE/COFF, Mach-O, wasm, Intel HEX, UF2 |
| `interp` | built-in interpreters for tests |
| `diff` | semantic diff of two versions of code |
| `server` | environment protocol and LSP |
| `cmd/anlang` | command line |

## 22.5 Effect inference

The normative semantics of effects is in [07-effects](07-effects.md). The algorithm has two steps.

1. **Per-package summaries.** The call graph of a package is split into strongly connected components (recursion) and traversed bottom-up, joining effect sets until a fixed point is reached. A call through a parameter (function value, interface method of a parameter, method of a type parameter) adds a parametric reference to the summary instead of a concrete set. A summary depends only on the package and the summaries of the packages it imports, so it is cached by their hashes.
2. **Whole-program substitution.** At build time, parametric references are replaced with the effects of the concrete functions and implementations passed, and open references to interfaces stored in data structures are replaced with the union of the implementations stored there. This step is cheap; it checks the constraints of profiles, targets, `anlang.mod` and `interrupt` handlers.

## 22.6 Lowering of value semantics

| Construct | Compiled as |
| --- | --- |
| `inout` | pointer for the duration of the access; the compiler guarantees no other access to the value meanwhile |
| large value as argument | passed by pointer without a copy if the callee does not modify it |
| synchronization types | pointer to the object; the object lives in the frame of the creating function if the handle does not escape into `task.Detach`, otherwise on the heap with an atomic reference count |
| strings, slices, maps | header and buffer with a reference count; a write to a shared buffer copies it first; the count is atomic only for values passed between tasks |
| `enum` | tag and union of variants; `Option[T]` without a tag when `T` has an invalid bit pattern |
| slice | pointer, length and capacity, each one machine word |
| overflow and bounds checks | branch to the target's panic handler; removed when the solver has proved safety |

## 22.7 Build cache

The cache key of a package is the hash of its sources, the hashes of the packages it imports, the target and the compiler version. A rebuild without changes reduces to linking.

## 22.8 Solver

The solver is written in Go together with the prototype, then in Anlang. The normative interface — determinism by step limit, certificates, counterexamples — is in [08-contracts](08-contracts.md). Its capabilities grow in stages:

| Stage | What it proves | Typical contracts |
| --- | --- | --- |
| 1 | linear integer arithmetic | ranges, array bounds, `lo <= hi` |
| 2 | bit vectors | absence of overflow, `bits.WrappingAdd`, register masks |
| 3 | arrays and slices | contents after copying, loop invariants |
| 4 | `enum` and `Option` | which variant is possible at a point |
| 5 | bounded quantifiers | "for all i in a range" |

Proven conditions are cached locally by the hash of the condition and its context; unchanged code is not re-proved.

## 22.9 Bootstrapping

1. **Stage 0.** The compiler is written in Go. Anlang's syntax is based on Go, so the parser, data structures and most logic later move to Anlang almost line by line. Go gives fast builds, simple cross-compilation, one dependency-free executable, and purego as a proven model for calling external code.
2. **Stage 1.** The compiler is ported from Go to Anlang (with `anlang port`) and built by the Go version.
3. **Stage 2.** The stage 1 compiler builds its own sources again; the resulting executable MUST be byte-identical to the stage 1 executable (see [23-conformance](23-conformance.md), **conf.self-hosting**).
4. **Stage 3.** The Go version is archived for bootstrapping from scratch and for checking the compiler for backdoors (**impl.bootstrap.archive**).

Stage 1 requires host targets (x86-64 and arm64) and the `app` allocator; see [24-roadmap](24-roadmap.md).

## 22.10 Open issues


1. **Textual IR format.** Deferred to implementation — step 6. Until then the IR interpreter and tests use an implementation-defined text form.
2. **Format of architecture descriptions.** Deferred to implementation — step 11; owned by [13-targets](13-targets.md).
3. **General-purpose allocator for `app`.** Deferred to implementation — step 25. Own or ported; any allocator that satisfies [06-memory](06-memory.md) and passes the fuzzing of [23-conformance](23-conformance.md).
