# 18. Standard Library

This file specifies how the standard library is organized and which packages it contains. The behavior of individual packages is specified in the files that own the corresponding feature (for example, `kern/sync` in [09-concurrency](09-concurrency.md), `kern/secret` in [11-data-safety](11-data-safety.md)).

## 18.1 Location and versioning

**std.path.root** — Every standard library package MUST have an import path that starts with `kern/`. The import path element `kern` is reserved for the standard library (see [03-declarations](03-declarations.md)).

**std.version.toolchain** — The standard library is shipped with the `kern` toolchain and versioned with it. A given version of `kern` MUST provide exactly one version of each standard library package. The standard library MUST NOT be fetched, replaced or overridden by a project.

**std.source** — The standard library MUST be available to the compiler as Kern source code. Effect inference, contract checking and semantic queries treat standard library code like project code.

**Note** — There is no package manager and no external package source. A program imports only `kern/...` packages and packages of its own project (see [03-declarations](03-declarations.md)).

## 18.2 Principles

**std.profile.declared** — Every standard library package MUST declare the profiles it supports with a `profile` clause (see [07-effects](07-effects.md)).

**std.profile.effect-free** — A standard library package whose exported functions have no effects MUST declare all three profiles: `core`, `app` and `ui`.

**std.io.common** — The I/O interfaces `io.Reader` and `io.Writer` (package `kern/io`) MUST be the same types in all profiles. Their implementations depend on the target.

**std.core.no-hidden-alloc** — In the `core` profile, a standard library function MUST NOT allocate memory implicitly. A function that needs memory MUST take a buffer from the caller.

**std.hw.ordinary** — Packages that describe hardware and platforms (target descriptions, architecture descriptions, device descriptions, architecture instruction packages, platform bindings) MUST be ordinary Kern packages. They MUST NOT require special support in the compiler beyond what [13-targets](13-targets.md) and [14-embedded](14-embedded.md) specify.

**std.target.availability** — On a given target, a standard library package is usable only if its effects are allowed by the target's `Provides` and `Lacks` lists and by the profile of the importing package (see [07-effects](07-effects.md), [13-targets](13-targets.md)).

## 18.3 Package catalog

**std.catalog.required** — An implementation MUST provide the packages in the following table, with at least the profile availability shown.

| Packages | Purpose | `core` | `app` | `ui` |
| --- | --- | --- | --- | --- |
| `kern/math`, `kern/bits`, `kern/wrap`, `kern/conv`, `kern/decimal`, `kern/fixed`, `kern/simd` | numbers | yes | yes | yes |
| `kern/hash`, `kern/crc`, `kern/crypto` | hashes and cryptography | yes | yes | yes |
| `kern/strings`, `kern/unicode`, `kern/encoding` | text and encodings | yes | yes | yes |
| `kern/fmt` | formatting | without allocation | yes | yes |
| `kern/containers` | arrays, queues, maps | fixed size | yes | yes |
| `kern/json`, `kern/cbor`, `kern/binary` | data formats | streaming | yes | yes |
| `kern/task`, `kern/sync`, `kern/atomic` | concurrency | static tasks | yes | yes |
| `kern/time`, `kern/rand` | time and randomness | if the target provides them | yes | yes |
| `kern/secret`, `kern/validate` | data safety | yes | yes | yes |
| `kern/fs`, `kern/os` | files and processes | DOS only | yes | — |
| `kern/net`, `kern/http`, `kern/tls` | networking | — | yes | through the platform |
| `kern/sql` | databases | — | yes | — |
| `kern/ui`, `kern/camera`, `kern/location` | interface and device | — | — | yes |
| `kern/device`, `kern/gpio`, `kern/uart` | microcontroller peripherals | yes | — | — |
| `kern/dos`, `kern/bios` | DOS and BIOS | yes | — | — |
| `kern/kernel`, `kern/mm`, `kern/sched` | operating system building blocks | yes | — | — |
| `kern/ffi`, `kern/js`, `kern/meta` | interoperation and reflection | yes | yes | yes |

In the table:

- "without allocation", "fixed size", "streaming" and "static tasks" mean that in `core` the package provides the subset of its API that satisfies **std.core.no-hidden-alloc**.
- "if the target provides them" means the package is usable in `core` only on targets whose description provides the `Clock` or `Random` effect.
- "DOS only" means that in `core`, `kern/fs` and `kern/os` are usable only on the DOS platform.
- "through the platform" means that in `ui`, networking is available through the platform's facilities on that target (for example, browser networking on `web`).

### 18.3.1 Packages specified elsewhere

The specification refers to further standard library packages. They are part of the standard library and are specified in the files listed.

| Package | Specified in |
| --- | --- |
| `kern/io` | this file, **std.io.common** |
| `kern/gen` | [10-compile-time](10-compile-time.md) |
| `kern/untrusted` | [11-data-safety](11-data-safety.md) |
| `kern/effect` | [07-effects](07-effects.md) |
| `kern/wcet` | [08-contracts](08-contracts.md) |
| `kern/arena` | [06-memory](06-memory.md) |
| `kern/unsafe` | [06-memory](06-memory.md) |
| `kern/target`, `kern/arch`, `kern/platform`, `kern/arch/<architecture>` | [13-targets](13-targets.md) |
| `kern/svd`, `kern/interrupts`, `kern/timer`, `kern/cpu` | [14-embedded](14-embedded.md) |
| `kern/abi` | [15-os](15-os.md) |
| `kern/gpu` | [16-gpu](16-gpu.md) |
| `kern/app`, `kern/cmd` | [17-ui](17-ui.md) |
| `kern/html` | [11-data-safety](11-data-safety.md) |
| `kern/log` | [11-data-safety](11-data-safety.md) |
| `kern/testing` | [20-toolchain](20-toolchain.md) |
| `kern/runtime` | [19-runtime](19-runtime.md) |

### 18.3.2 Generated packages

**std.generated.arch** — For each architecture description, the package `kern/arch/<architecture>` (for example, `kern/arch/i8086`) is generated from that description (see [13-targets](13-targets.md)).

**std.generated.device** — For a target whose description has a `Device` field, the package `kern/device` is generated from the device description for that target (see [14-embedded](14-embedded.md)). On targets without a device description, importing `kern/device` is an error.

**std.generated.readable** — Generated packages MUST be available as readable Kern source, like any other standard library package.

### 18.3.3 Required identifiers

**std.api.required** — The packages below MUST export at least the identifiers listed. Their semantics are specified in the file named. Identifiers marked *provisional* are pending decision ([open-issues](open-issues.md)).

| Package | Identifiers | Specified in |
| --- | --- | --- |
| `kern/task` | `Go`, `Config`, `Low`, `Normal`, `High`, `Critical`, `Wait`, `ErrDeadline`, `Detach`, `Deadline`, `Cancel`, `Cancelled`, `Sleep`, `Yield`, `Run`, `LockThread` | [09-concurrency](09-concurrency.md), [14-embedded](14-embedded.md) |
| `kern/sync` | `Mutex[T]` with `Lock`, `Load`; `RWMutex[T]` with `Lock`, `RLock`, `Load`; spin lock for kernels (*provisional* `SpinLock[T]`) | [09-concurrency](09-concurrency.md), [15-os](15-os.md) |
| `kern/atomic` | `I8`…`I64`, `U8`…`U64`, `Bool` with `Load`, `Store`, `Add` and memory orders | [09-concurrency](09-concurrency.md), [15-os](15-os.md) |
| `kern/arena` | `Arena[T]` with `New`, `Edit`, `Get`, `Delete`; `Ref[T]` | [06-memory](06-memory.md) |
| `kern/effect` | `Effect`; base effect constants (`Alloc`, `Network`, …); `Declare`, `Mark`, `Defer`, `Without` | [07-effects](07-effects.md) |
| `kern/wcet` | `Within` | [08-contracts](08-contracts.md), [14-embedded](14-embedded.md) |
| `kern/meta` | `Name`, `Fields`, `Variants`, `Field`, `Variant`, `Get`, `Set`, `Is`, `Make`, `Tag`, `Reject` | [10-compile-time](10-compile-time.md) |
| `kern/gen` | effects `ReadInput`, `WriteOutput` for generators | [10-compile-time](10-compile-time.md) |
| `kern/secret` | `New`, `Equal`, `Expose`, `Map`, `ConstantTime`; effect `ExposeSecret` | [11-data-safety](11-data-safety.md) |
| `kern/untrusted` | `Accept` | [11-data-safety](11-data-safety.md) |
| `kern/interrupts` | `Enable` | [14-embedded](14-embedded.md) |
| `kern/mm` | `PhysAddr`, `VirtAddr` (*provisional* location) | [15-os](15-os.md) |
| `kern/sched`, `kern/kernel` | kernel thread type with declared stack size, per-CPU data (*provisional* names) | [15-os](15-os.md) |
| `kern/gpu` | `Out[T]` with a constructor taking a length and a read-back method (*provisional* names), `Dispatch`, `GlobalID`, `GlobalSize`; `Atomic[T]`, `Shared[T]` as future work | [16-gpu](16-gpu.md) |
| `kern/ui` | `Node`, constructors of nodes, native view node (*provisional*) | [17-ui](17-ui.md) |
| `kern/app` | `Desc` and the mapping from effects to platform permissions | [17-ui](17-ui.md) |

## 18.4 Open issues

1. **Full package list.** `kern.md` leaves the composition of the standard library open. Provisional choice: the packages in 18.3 and 18.3.1 are the complete required set; others MAY be added in later editions.
2. **Location of `kern/runtime`.** `kern.md` says the runtime is shipped as source but gives no import path. Provisional choice: `kern/runtime`, not importable by user code (see [19-runtime](19-runtime.md)).
3. **`kern/html` and `kern/log` profiles.** They are used in examples but not listed in the profile table. Provisional choice: `kern/log` in all profiles (without allocation in `core`); `kern/html` in `app` and `ui`.
4. **`kern/simd`** is used in [02-types](02-types.md) but missing from the profile table in `kern.md`. Provisional choice: available in all profiles, as shown in 18.3.
