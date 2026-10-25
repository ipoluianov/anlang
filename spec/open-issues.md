# Open issues

Consolidated index of the "Open issues" subsections of all spec files. Section 1 records the decisions taken during integration; section 2 lists what is still open. The file's own "Open issues" subsection is authoritative for details.

## 1. Resolved

Decisions of 2026-10-25. The rule text in the spec files is normative; these items have been removed from the files' "Open issues" subsections.

### 1.1 Conflicts with kern.md

| # | File | Question | Decision |
| --- | --- | --- | --- |
| 1 | 09-concurrency, 05-statements | Errors returned by a task | A function in `go` MAY return `error`; `task.Wait()` waits for the tasks, the first error cancels the rest and is returned; without `task.Wait()` or `task.Cancel()` after tasks with error results the program is rejected; a panic cancels sibling tasks and propagates to the parent (**conc.task.error**, **conc.struct.wait**, **conc.struct.error-wait**, **conc.struct.panic**, **stmt.error.go**) |
| 2 | 09-concurrency | Early return with running tasks | Any exit without waiting cancels unfinished tasks and waits until they have stopped; `FetchAll` cancels the requests by an early `return` (**conc.struct.exit**, **conc.cancel.explicit**) |
| 3 | 10-compile-time | Effects allowed in compile-time evaluation | `Alloc`, `StaticAlloc`, `Panic` and package-local `ReadInput` are allowed (**ct.eval.evaluable**, **ct.eval.read-input**) |
| 4 | 02-types | Type of lengths and indices | `int`; `usize` is for memory sizes and offsets (**type.int.usize**) |
| 5 | 12-ffi | Library descriptors | `const`, exported fields `Linux`, `MacOS`, `ARMv7M`, `Optional` (**ffi.lib.desc**, **ffi.static.desc**) |
| 6 | 11-data-safety | Comparing a secret with a stored hash | `secret.Equal` takes two secrets; the stored hash is `Secret[[]u8]` |
| 7 | 09-concurrency, 14-embedded | Priority of a task started with `go` | Inherits the parent's priority; `main` is `Normal`; in `core` every interrupt is above every task (**conc.prio.inherit**, **emb.prio.order**) |
| 8 | 03-declarations, 20-toolchain | Module name and path element syntax | 03's rules: `[a-z][a-z0-9_]*`; the `kern.mod` grammar in 20 references them (**decl.import.module**, **decl.import.paths**, **tool.mod.module**) |

### 1.2 Confirmed decisions

| # | File | Question | Decision |
| --- | --- | --- | --- |
| 1 | 03-declarations, 07-effects | Profile of a package without `profile` | `app` (**decl.profile.package**, **eff.profile.default**) |
| 2 | 03-declarations | Package name vs import path | Name MUST equal the last path element (**decl.package.name**) |
| 3 | 01, 02, 03 | Further removals from Go | `recover`, complex numbers and imaginary literals, `uintptr`, blank and dot imports removed; package-level `init` is an error (**type.universe.removed**, **decl.func.no-init**) |
| 4 | 02-types, 04-expressions | Construction and scope of enum variants | Variants with data are constructor functions (`Some(root)`, `Scanned` as a function value); variant names live in the enclosing block, unqualified (**type.enum.constructor**, **type.enum.variant-scope**, **expr.variant.construct**) |
| 5 | 05-statements | Pattern bindings | Positional, any identifier, bound as copies (**stmt.switch.variant.bindings**) |
| 6 | 04-expressions | Captured variables in closures | Read-only in the closure; a closure capturing an `inout` parameter may only be called directly (**expr.closure.captured-readonly**, **expr.closure.capture-inout**) |
| 7 | 04-expressions | `nil` beyond the agreed list | Function types also have zero value `nil` with the interface call rule; `x == nil` on slices and maps means `len(x) == 0` (**expr.cmp.nil**, **expr.cmp.nil-empty**) |
| 8 | 02, 04 | `inout` methods through interfaces | Allowed on addressable interface operands; modify the copy held by the interface (**type.interface.inout-method**, **expr.method.interface-inout**) |
| 9 | 06-memory | Heap placement of synchronization objects | Any handle that outlives the creating function, not only `task.Detach` (**mem.sync.heap**) |
| 10 | 06-memory | `unsafe.Map` | All mapped accesses volatile; only at package level (**mem.unsafe.map**) |
| 11 | 07-effects | Spelling of base effects in source | Qualified (`effect.Alloc`); bare in `kern.mod` and diagnostics (**eff.model.names**) |
| 12 | 07-effects | Several `kern.mod` rules match a package | Longest pattern wins; `deny` over `allow` for one pattern (**eff.mod.precedence**) |
| 13 | 07-effects, 17-ui | How effects of `Cmd` reach the program | Deferred effects declared with `effect.Defer` (**eff.deferred.declare**, **ui.cmd.deferred**) |
| 14 | 08-contracts | `assert` | Predeclared function `assert(cond bool)` (**contract.assert.form**) |
| 15 | 08-contracts | Arithmetic in contracts | Unbounded mathematical integers (**contract.expr.math**) |
| 16 | 08-contracts | Entry value of `inout` parameters (`old(x)`) | Not available; copy into a local (**contract.expr.post-state**) |
| 17 | 08-contracts | Contracts and indirect calls | Functions with non-trivial `requires` cannot be used as function values or interface implementations (**contract.obligation.indirect**) |
| 18 | 08-contracts | Solver step limit | Fixed per edition, no override (**contract.solver.determinism**) |
| 19 | 09-concurrency | Concurrency in `core` | `task.Detach` forbidden; `go` only where the number of executions is bounded; channel buffer sizes constant (**conc.detach.profile**, **conc.model.core**, **conc.model.core-chan**) |
| 20 | 09-concurrency | Observing cancellation | Unwinding at the next wait point, plus `task.Cancelled()` (**conc.cancel.delivery**) |
| 21 | 03, 10 | Constants of composite types | Any type built from scalars, arrays, slices, structs, maps, `enum`, `Option` and interfaces over them, without handles and functions (**decl.const.types**, **ct.const.types**) |
| 22 | 10-compile-time | Per-type code for `...any` elements | Static type switch on type-parameter values and tuple elements (**ct.typeswitch.static**) |
| 23 | 10-compile-time | Rejecting arguments at compile time | `meta.Reject` and compile-time evaluation of `requires` on constant arguments (**ct.reject.call**, **ct.reject.precondition**) |
| 24 | 10-compile-time | `meta` API | `Name`, `Fields`, `Variants`, `Get`, `Set`, `Is`, `Make`, `Tag` (10.4) |
| 25 | 11-data-safety | Where `Secret` and `Untrusted` live | Predeclared types; operations in `kern/secret` and `kern/untrusted` (**safe.types.predeclared**) |
| 26 | 11-data-safety | Using a secret without exposing it | `secret.Map` with an effect-restricted function (**safe.secret.map**) |
| 27 | 11-data-safety | Writing validators | `untrusted.Accept`, without an effect |
| 28 | 12-ffi | Writable buffers | `[]T` read-only, `inout []T` writable (**ffi.types.params**, **ffi.mem.unique**) |
| 29 | 13-targets | Meaning of `Lacks` | Only improves diagnostics and queries (**target.effects.lacks**) |
| 30 | 14-embedded | Initialization phase | `interrupts.Enable()` and `task.Run()` only directly from `main` (**emb.init.calls**); blocking inside critical sections forbidden (**emb.lock.no-block**) |
| 31 | 15-os | Who may declare `trap` | Only packages allowed `Privileged` (**os.trap.profile**) |
| 32 | 16-gpu | Panics and dispatch | Panic forbidden in GPU code; `gpu.Dispatch` synchronous; one-dimensional dispatch only (**gpu.subset.effects**, **gpu.dispatch.sync**) |
| 33 | 17-ui | Initial state and lifecycle | Zero value of `State` or `Init`; optional `OnLifecycle` (**ui.app.init**, **ui.lifecycle.delivery**) |
| 34 | 21-protocol | Protocol method names and shapes | Invented names in 21.4 (`initialize`, `check`, `query`, `prove`, `test`, `run`, `debug/*`, `diff`, `requirements`, `shutdown`) |
| 35 | 01-lexical | Novelty inventory | Provisional entries added to §1.8: `assert`, variant constructors, static type switch, `meta.Reject`, composite constants, predeclared `Secret`/`Untrusted`, deferred effects |

## 2. Open: minor and technical

### 01-lexical
- Non-canonical formatting: error in every build (**lex.source.canonical**).
- A call `requires(x)` at the start of a body is a contract clause; write `(requires)(x)` otherwise.
- `layout` is written in the leading clause region of the struct field list.
- Untyped rune constants default to `i32`.

### 02-types
- Unexported variants of exported enums allowed; external `switch` needs `default` (**type.enum.variant-export**).
- Byte-order types usable anywhere (**type.endian**).
- Run-time conversion to `cstring`/`dstring` copies and panics on an embedded terminator; relation to `ffi.CString` open (**type.string.terminator-conv**).
- Float-to-integer conversion panics when not representable (**type.conv.float-int**).
- `fixed` and integer `simd` overflow checked as for integers.
- No constraint guarantees a zero value; `var x P` for a type parameter is rejected (**type.zero.generic**).
- `layout` structs hold only fixed-size plain data (**type.layout.fields**).

### 03-declarations
- No channels at package level (**decl.var.package-kinds**).
- Handler names are not declared in any scope (**decl.handler.name**).

### 04-expressions
- `<<` on checked integers discards bits as in Go (**expr.int.shift**).
- Float-to-integer conversion out of range is overflow (**expr.float.to-int**).
- Integer `simd` lanes checked; `fixed` `*` and `/` truncate toward zero (**expr.known.fixed**).
- A `...any` parameter is usable only in `for const`, `len` and forwarding (**expr.call.any-forward**).

### 05-statements
- `default` permitted in variant switches (**stmt.switch.variant.default**).
- Deferred calls returning errors need an explicit discard.
- Dead stores of errors not covered by **stmt.error.unused**.
- `asm` syntax provisional (**stmt.asm.syntax**).
- `inout` operand path in `defer` fixed at the statement; access begins when the call runs (**stmt.defer.inout-args**).

### 06-memory
- Changing the length of an `inout` subslice panics (**mem.inout.subslice**).
- Arena API adds `Get`, `Delete`; stale handles panic.
- Arenas in `core` need `Alloc` to grow; a fixed-capacity arena may be added.
- Values are released no later than the end of the scope of their last copy.

### 07-effects
- Exact forbidden effects per profile (**eff.profile.allowed**).
- Function values stored in data structures resolved by function type over the whole program.
- `Alloc` added wherever an operation may copy (**eff.infer.determinism**).
- `Without` on functions with parametric references checked at declaration and at each call (**eff.without.check**).

### 08-contracts
- `result` only for exactly one unnamed result (**contract.expr.result**).
- Type invariants with exported fields checked at boundaries.

### 09-concurrency
- `task.LockThread` lasts until the task ends (**conc.os.lock-thread**).
- `RWMutex.RLock()` yields a copy (**conc.sync.rwmutex**).

### 10-compile-time
- Concrete evaluation limits implementation-defined; project override open.
- Generator declaration in `kern.mod` and whether generated output is committed; `kern.mod` grammar in 20-toolchain does not include generators yet.

### 11-data-safety
- Text and structured decoders return `Untrusted`; raw byte reads do not (**safe.untrusted.sources**).
- No shell-string API (**safe.untrusted.shell**).
- `ExposeSecret` declared with `effect.Declare` in `kern/secret`.

### 12-ffi
- Calling an unavailable optional library panics (**ffi.lib.optional**).
- Unresolved C-library symbols in object files are a build error (**ffi.static.unresolved**).
- C strings returned as `ffi.Handle`, copied with `ffi.GoString`.
- Binding form for JNI table entries open.
- At least 1024 callbacks, never freed; foreign threads run callbacks as tasks (**ffi.callback.lifetime**, **ffi.callback.context**).

### 13-targets
- Formats of architecture and platform descriptions only by required content.
- All formats in `Formats` are produced (**target.format.all**).
- Placement: first `Exec` and first non-`Exec` region.
- `main` profiles per platform: table in 13.4.
- Project target packages found by directory name (**target.select.resolve**).
- `target.Pointers` fields `Flat`, `Near`, `Far`, `Program`.

### 14-embedded
- Interrupt priorities from the device description; override unspecified.
- Unhandled vectors panic via `OnPanic`.
- Multi-core microcontrollers not specified; **emb.shared.mutex** applies to single-core targets.
- CPU clock: `Clock` field of `target.Desc`.
- Response-time analysis is future work.

### 15-os
- `PhysAddr`, `VirtAddr` in `kern/mm`; width per target.
- Names of kernel thread, per-CPU and spin lock types.
- Validation of user memory in system calls; whether kernels receive it as `Untrusted`.
- Default memory order `SeqCst` (**os.smp.atomic**).
- Boot entry signatures per boot platform (**os.boot.entry**).

### 16-gpu
- Creating and reading `gpu.Out[T]` buffers: names open.
- Launch size in contracts: `gpu.GlobalSize()` (**gpu.dispatch.requires**).
- `f64` and floating point guarantees on GPUs.
- Windows compute via Vulkan/`spirv`; Direct3D shaders unspecified.
- GPU functions allowed in any package that allows `core` (**gpu.subset.profile**).

### 17-ui
- Background limits compared with the platform description (**ui.cap.background**).
- Form of the state migration function.
- Native view API.
- Effect-to-permission table informative; normative mapping in `kern/app`.
- `Background` on Android.

### 18-stdlib
- Required set: 18.3 plus 18.3.1 and 18.3.3.
- `kern/runtime` not importable.
- `kern/log` in all profiles, `kern/html` in `app` and `ui`.
- `kern/simd` in all profiles.

### 19-runtime
- Release vs development builds (**rt.dev.builds**).
- Panic exit status non-zero.
- Stack size rounded to target alignment.

### 20-toolchain
- Exit statuses 0 / 1 / 2 (**tool.exit-status**).
- Test references in `REQUIREMENTS`: bare names unique, otherwise qualified.
- Requirement identifiers like `AUTH-3`.
- `kern diff` takes two directories.
- Edition migration via `kern fmt -edition`.
- `kern flash` builds first (**tool.cmd.flash**).

### 21-protocol
- Positions: 1-based line, 1-based byte column.
- Fix guarantee: diagnostic disappears and no new error appears (**proto.diag.fix**).
- Own debugger methods, no Debug Adapter Protocol.

### 22-compiler
- Textual IR format unspecified.
- General-purpose allocator for `app` unspecified.

### 23-conformance
- Suite layout on disk implementation-defined.
- Performance thresholds set per benchmark.
