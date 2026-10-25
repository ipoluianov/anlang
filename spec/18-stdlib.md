# 18. Standard Library

This file specifies how the standard library is organized and which packages it contains. The behavior of individual packages is specified in the files that own the corresponding feature (for example, `an/sync` in [09-concurrency](09-concurrency.md), `an/secret` in [11-data-safety](11-data-safety.md)).

## 18.1 Location and versioning

**std.path.root** — Every standard library package MUST have an import path that starts with `an/`. The import path element `an` is reserved for the standard library (see [03-declarations](03-declarations.md)).

**std.version.toolchain** — The standard library is shipped with the `anlang` toolchain and versioned with it. A given version of `anlang` MUST provide exactly one version of each standard library package. The standard library MUST NOT be fetched, replaced or overridden by a project.

**std.source** — The standard library MUST be available to the compiler as Anlang source code. Effect inference, contract checking and semantic queries treat standard library code like project code.

**Note** — There is no package manager and no external package source. A program imports only `an/...` packages and packages of its own project (see [03-declarations](03-declarations.md)).

## 18.2 Principles

**std.profile.declared** — Every standard library package MUST declare the profiles it supports with a `profile` clause (see [07-effects](07-effects.md)).

**std.profile.effect-free** — A standard library package whose exported functions have no effects MUST declare all three profiles: `core`, `app` and `ui`.

**std.io.common** — The I/O interfaces `io.Reader` and `io.Writer` (package `an/io`) MUST be the same types in all profiles. Their implementations depend on the target.

**std.core.no-hidden-alloc** — In the `core` profile, a standard library function MUST NOT allocate memory implicitly. A function that needs memory MUST take a buffer from the caller.

**std.hw.ordinary** — Packages that describe hardware and platforms (target descriptions, architecture descriptions, device descriptions, architecture instruction packages, platform bindings) MUST be ordinary Anlang packages. They MUST NOT require special support in the compiler beyond what [13-targets](13-targets.md) and [14-embedded](14-embedded.md) specify.

**std.target.availability** — On a given target, a standard library package is usable only if its effects are allowed by the target's `Provides` and `Lacks` lists and by the profile of the importing package (see [07-effects](07-effects.md), [13-targets](13-targets.md)).

## 18.3 Package catalog

**std.catalog.required** — An implementation MUST provide the packages in the following table, with at least the profile availability shown.

| Packages | Purpose | `core` | `app` | `ui` |
| --- | --- | --- | --- | --- |
| `an/math`, `an/bits`, `an/wrap`, `an/conv`, `an/decimal`, `an/fixed`, `an/simd` | numbers | yes | yes | yes |
| `an/hash`, `an/crc`, `an/crypto` | hashes and cryptography | yes | yes | yes |
| `an/strings`, `an/unicode`, `an/encoding` | text and encodings | yes | yes | yes |
| `an/errors` | error values | yes | yes | yes |
| `an/fmt` | formatting | without allocation | yes | yes |
| `an/containers` | arrays, queues, maps | fixed size | yes | yes |
| `an/json`, `an/cbor`, `an/binary` | data formats | streaming | yes | yes |
| `an/task`, `an/sync`, `an/atomic` | concurrency | static tasks | yes | yes |
| `an/time`, `an/rand` | time and randomness | if the target provides them | yes | yes |
| `an/secret`, `an/validate` | data safety | yes | yes | yes |
| `an/fs`, `an/os` | files and processes | DOS only | yes | — |
| `an/net`, `an/http`, `an/tls` | networking | — | yes | through the platform |
| `an/sql` | databases | — | yes | — |
| `an/ui`, `an/camera`, `an/location` | interface and device | — | — | yes |
| `an/device`, `an/gpio`, `an/uart` | microcontroller peripherals | yes | — | — |
| `an/dos`, `an/bios` | DOS and BIOS | yes | — | — |
| `an/kernel`, `an/mm`, `an/sched` | operating system building blocks | yes | — | — |
| `an/ffi`, `an/js`, `an/meta` | interoperation and reflection | yes | yes | yes |

In the table:

- "without allocation", "fixed size", "streaming" and "static tasks" mean that in `core` the package provides the subset of its API that satisfies **std.core.no-hidden-alloc**.
- "if the target provides them" means the package is usable in `core` only on targets whose description provides the `Clock` or `Random` effect.
- "DOS only" means that in `core`, `an/fs` and `an/os` are usable only on the DOS platform.
- "through the platform" means that in `ui`, networking is available through the platform's facilities on that target (for example, browser networking on `web`).

### 18.3.1 Packages specified elsewhere

The specification refers to further standard library packages. They are part of the standard library and are specified in the files listed.

| Package | Specified in |
| --- | --- |
| `an/io` | this file, **std.io.common** |
| `an/gen` | [10-compile-time](10-compile-time.md) |
| `an/untrusted` | [11-data-safety](11-data-safety.md) |
| `an/effect` | [07-effects](07-effects.md) |
| `an/wcet` | [08-contracts](08-contracts.md) |
| `an/arena` | [06-memory](06-memory.md) |
| `an/unsafe` | [06-memory](06-memory.md) |
| `an/target`, `an/arch`, `an/platform`, `an/arch/<architecture>` | [13-targets](13-targets.md) |
| `an/svd`, `an/interrupts`, `an/timer`, `an/cpu` | [14-embedded](14-embedded.md) |
| `an/abi` | [15-os](15-os.md) |
| `an/gpu` | [16-gpu](16-gpu.md) |
| `an/app`, `an/cmd` | [17-ui](17-ui.md) |
| `an/html` | [11-data-safety](11-data-safety.md) |
| `an/log` | [11-data-safety](11-data-safety.md) |
| `an/testing` | [20-toolchain](20-toolchain.md) |
| `an/runtime` | [19-runtime](19-runtime.md) |

### 18.3.2 Generated packages

**std.generated.arch** — For each architecture description, the package `an/arch/<architecture>` (for example, `an/arch/i8086`) is generated from that description (see [13-targets](13-targets.md)).

**std.generated.device** — For a target whose description has a `Device` field, the package `an/device` is generated from the device description for that target (see [14-embedded](14-embedded.md)). On targets without a device description, importing `an/device` is an error.

**std.generated.readable** — Generated packages MUST be available as readable Anlang source, like any other standard library package.

### 18.3.3 Required identifiers

**std.api.required** — The packages below MUST export at least the identifiers listed. Their semantics are specified in the file named. Identifiers marked *provisional* are deferred to implementation; the roadmap step that fixes them is listed in [open-issues](open-issues.md).

| Package | Identifiers | Specified in |
| --- | --- | --- |
| `an/task` | `Go`, `Config`, `Low`, `Normal`, `High`, `Critical`, `Wait`, `ErrDeadline`, `Detach`, `Deadline`, `Cancel`, `Cancelled`, `Sleep`, `Yield`, `Run`, `LockThread` | [09-concurrency](09-concurrency.md), [14-embedded](14-embedded.md) |
| `an/sync` | `Mutex[T]` with `Lock`, `Load`; `RWMutex[T]` with `Lock`, `RLock`, `Load`; spin lock for kernels (*provisional* `SpinLock[T]`) | [09-concurrency](09-concurrency.md), [15-os](15-os.md) |
| `an/atomic` | `I8`…`I64`, `U8`…`U64`, `Bool` with `Load`, `Store`, `Add` and memory orders | [09-concurrency](09-concurrency.md), [15-os](15-os.md) |
| `an/arena` | `Arena[T]` with `New`, `Edit`, `Get`, `Delete`; `Ref[T]` | [06-memory](06-memory.md) |
| `an/effect` | `Effect`; base effect constants (`Alloc`, `Network`, …); `Declare`, `Mark`, `Defer`, `Without` | [07-effects](07-effects.md) |
| `an/wcet` | `Within` | [08-contracts](08-contracts.md), [14-embedded](14-embedded.md) |
| `an/meta` | `Name`, `Fields`, `Variants`, `Field`, `Variant`, `Get`, `Set`, `Is`, `Make`, `Tag`, `Reject` | [10-compile-time](10-compile-time.md) |
| `an/gen` | effects `ReadInput`, `WriteOutput` for generators | [10-compile-time](10-compile-time.md) |
| `an/secret` | `New`, `Equal`, `Expose`, `Map`, `ConstantTime`; effect `ExposeSecret` | [11-data-safety](11-data-safety.md) |
| `an/untrusted` | `Accept` | [11-data-safety](11-data-safety.md) |
| `an/interrupts` | `Enable` | [14-embedded](14-embedded.md) |
| `an/bios` | `BootDrive` | [15-os](15-os.md) |
| `an/mm` | `PhysAddr`, `VirtAddr` | [15-os](15-os.md) |
| `an/sched`, `an/kernel` | kernel thread type with declared stack size, per-CPU data (*provisional* names) | [15-os](15-os.md) |
| `an/gpu` | `Out[T]` with a constructor taking a length and a read-back method (*provisional* names), `Dispatch`, `GlobalID`, `GlobalSize`; `Atomic[T]`, `Shared[T]` as future work | [16-gpu](16-gpu.md) |
| `an/ui` | `Node`, constructors of nodes, native view node (*provisional*) | [17-ui](17-ui.md) |
| `an/app` | `Desc` and the mapping from effects to platform permissions | [17-ui](17-ui.md) |
| `an/errors` | `New`, `Is`, `As` | this file, §18.3.5 |
| `an/fmt` | `Print`, `Println`, `Printf`; `Fprint`, `Fprintln`, `Fprintf`; `Sprint`, `Sprintln`, `Sprintf` | this file, §18.3.4 |

### 18.3.4 Printing

**std.fmt.print** — `fmt.Print`, `fmt.Println` and `fmt.Printf` write to the standard output of the program and return no results. If the write fails, they panic. Their effects therefore include `Console` and `Panic`.

**std.fmt.fprint** — `fmt.Fprint`, `fmt.Fprintln` and `fmt.Fprintf` take an `io.Writer` as the first argument and return `(n int, err error)`, as in Go. The error is subject to **stmt.error.unused** ([05-statements](05-statements.md)).

**std.fmt.sprint** — `fmt.Sprint`, `fmt.Sprintln` and `fmt.Sprintf` return a `string` and have no `Console` effect.

**Note** — Printing to standard output is the common case, and a write error there is rare and rarely recoverable, so it panics instead of returning an error that every caller would have to discard. Writing to an arbitrary `io.Writer`, such as a file or a network connection, returns the error, because there it must be handled.

```go
fmt.Println("Hello, World!")                // no result; panics if stdout fails

if _, err := fmt.Fprintln(f, "line"); err != nil {
    return err
}
```

### 18.3.5 Errors

**std.errors.api** — `errors.New` and `errors.Is` behave as in Go. `fmt.Errorf` with `%w` wraps errors as in Go.

**std.errors.as** — Anlang has no pointers, so `errors.As` has the comma-ok form `func As[T error](err error) (T, bool)`: it returns the first error in the chain of `err` whose dynamic type is `T`, and `true`; otherwise the zero value of `T` and `false`. `T` therefore requires a zero value (**type.zero.param-constraint**, [02-types](02-types.md)).

**std.errors.const** — `errors.New` with a constant argument is compile-time evaluable (**ct.eval.evaluable**, [10-compile-time](10-compile-time.md)). Sentinel errors are therefore declared as constants, because package-level variables are restricted (**decl.var.package-kinds**, [03-declarations](03-declarations.md)).

**std.errors.value** — The error returned by `errors.New` has no hidden identity: two such errors are equal if and only if their texts are equal. `errors.Is` compares with `==`, unwrapping as in Go.

```go
const ErrEmptyWord = errors.New("mydictionary: empty word")
```

**Note** — Equal texts in different packages give equal errors. The convention, as in Go, is to start the text with the package name.

**std.errors.enum** — The recommended form for errors that a package returns to its callers is an `enum` type with an `Error() string` method. Its identity comes from the type: values of different enum types are never equal, values of one type are equal under **type.enum.comparable** ([02-types](02-types.md)). Callers test for a variant with `errors.Is` or handle all variants with an exhaustive `switch` after `errors.As`; adding a variant then breaks every incomplete `switch`.

```go
// Error is an error of package mydictionary.
type Error enum {
    EmptyWord
    TooLong{word string, max int}
}

func (e Error) Error() string {
    switch e {
    case EmptyWord:          return "mydictionary: empty word"
    case TooLong{word, max}: return fmt.Sprintf("mydictionary: %q is longer than %d", word, max)
    }
}

// caller
if errors.Is(err, mydictionary.EmptyWord) { ... }

if de, ok := errors.As[mydictionary.Error](err); ok {
    switch de { ... }             // exhaustive over the variants
}
```

**Note** — `anlang port` translates a Go sentinel `var ErrX = errors.New("...")` into `const ErrX = errors.New("...")`; the meaning is unchanged as long as no two sentinels share a text.
