# Open issues

Consolidated index of the "Open issues" subsections of all spec files. Section 1 records all decisions; section 2 lists questions deferred to implementation. No design question is open.

## 1. Resolved

Decisions of 2026-10-25. The rule text in the spec files is normative; these items have been removed from the files' "Open issues" subsections.

### 1.1 Conflicts with anlang.md

| # | File | Question | Decision |
| --- | --- | --- | --- |
| 1 | 09-concurrency, 05-statements | Errors returned by a task | A function in `go` MAY return `error`; `task.Wait()` waits for the tasks, the first error cancels the rest and is returned; without `task.Wait()` or `task.Cancel()` after tasks with error results the program is rejected; a panic cancels sibling tasks and propagates to the parent (**conc.task.error**, **conc.struct.wait**, **conc.struct.error-wait**, **conc.struct.panic**, **stmt.error.go**) |
| 2 | 09-concurrency | Early return with running tasks | Any exit without waiting cancels unfinished tasks and waits until they have stopped; `FetchAll` cancels the requests by an early `return` (**conc.struct.exit**, **conc.cancel.explicit**) |
| 3 | 10-compile-time | Effects allowed in compile-time evaluation | `Alloc`, `StaticAlloc`, `Panic` and package-local `ReadInput` are allowed (**ct.eval.evaluable**, **ct.eval.read-input**) |
| 4 | 02-types | Type of lengths and indices | `int`; `usize` is for memory sizes and offsets (**type.int.usize**) |
| 5 | 12-ffi | Library descriptors | `const`, exported fields `Linux`, `MacOS`, `ARMv7M`, `Optional` (**ffi.lib.desc**, **ffi.static.desc**) |
| 6 | 11-data-safety | Comparing a secret with a stored hash | `secret.Equal` takes two secrets; the stored hash is `Secret[[]u8]` |
| 7 | 09-concurrency, 14-embedded | Priority of a task started with `go` | Inherits the parent's priority; `main` is `Normal`; in `core` every interrupt is above every task (**conc.prio.inherit**, **emb.prio.order**) |
| 8 | 03-declarations, 20-toolchain | Module name and path element syntax | 03's rules: `[a-z][a-z0-9_]*`; the `anlang.mod` grammar in 20 references them (**decl.import.module**, **decl.import.paths**, **tool.mod.module**) |

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
| 11 | 07-effects | Spelling of base effects in source | Qualified (`effect.Alloc`); bare in `anlang.mod` and diagnostics (**eff.model.names**) |
| 12 | 07-effects | Several `anlang.mod` rules match a package | Longest pattern wins; `deny` over `allow` for one pattern (**eff.mod.precedence**) |
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
| 24 | 10-compile-time | When the body of a function with `...any`, `an/meta` or a static type switch is checked | Once per instance, on a copy with the type arguments and tuple types; an instance no call produces is not checked; constant `if` conditions select the branch that is checked and compiled; `for const` over an array constant needs a composite literal of constants, because unrolling precedes the evaluation of computed constants (**ct.instance.check**, **ct.for.sequence**); found while implementing compile-time tuples |
| 25 | 13-targets | Format of architecture descriptions and notation of instruction semantics | A constant `arch.Arch` of package `an/arch` with registers, operand kinds, fragments, prefixes and instruction forms; encodings are bit patterns with fields and fragments with alternatives, so that the ModRM byte of the 8086 is data rather than compiler code; semantics are Sail-like statements over bit vectors in string fields, checked for widths; of several matching forms the most specific applies (**target.arch.schema**, **target.arch.encoding**, **target.arch.specific**, **target.arch.semantics**, **target.arch.widths**, **target.arch.execution**, **target.arch.check**); decided at step 11 with the i8086 description |
| 26 | 05-statements, 13-targets | `asm` syntax and the syntax of the generated assembler | The provisional `asm(regs) `...`` form is kept; its text has one instruction per line in a uniform Intel-like syntax derived from the description: prefixes, mnemonic, operands; memory operands with `byte`/`word` size and addresses that a fragment alternative can encode; labels and `;` comments; the assembler picks the shortest encoding (**stmt.asm.syntax**, **target.arch.asm-syntax**, **target.arch.round-trip**); decided at step 12 |
| 27 | 13-targets | The instruction package and the register parts | `Regs` holds the whole registers (`AX`, not `AH`); a function exists for each instruction form whose operands are immediates, constants and fixed registers and that does not set the program counter directly (**target.arch.instr-package**); decided at step 12 |
| 28 | 13-targets | Field names of platform and target descriptions | `platform.Platform` has `Name`, `Provides`, `Dynamic`, `Rules`, `Formats`, `Profiles` and `OnPanic`; `target.Desc` and its field types are declared in `an/target`, and a field at its zero value takes its default (`target.DefaultFloats`, `target.DefaultPanic`) (**target.platform.schema**, **target.platform.entry**, **target.desc.types**); decided at step 13 with `platform.DOS` and `an/target/dos-com` |
| 29 | 13-targets, 22-compiler | Form of instruction selection rules and of the code generator | A rule maps one machine operation of a given width to an instruction template: assembler text with placeholders for operands in frame slots or constants, their word-sized parts, labels and local labels; every IR value lives in a frame slot, arguments and results pass through the argument area at the bottom of the caller's frame; operations wider than a word without a rule call the runtime package; until step 22 the rules are tested in the generated interpreter, exhaustively for 8 bits (**target.arch.rule**, **target.arch.ops**, **target.arch.template**, **target.arch.select**, **target.arch.abi**, **target.arch.rules-test**, **comp.gen.layout**, **comp.gen.frame**, **comp.gen.values**, **comp.gen.lower**, **comp.gen.runtime**, **comp.gen.link**); decided at step 13 |
| 30 | 13-targets | Binary formats before link-time evaluation | Until step 26, `target.Format` has the method `Kind`, and the toolchain writes the format of each kind (**target.format.kind**); decided at step 13 |
| 31 | 20-toolchain | Running a program in its built-in emulator | `anlang run -target T` for a target that cannot run on the host, or `-emu builtin`; the DOS model provides `int 20h` and functions `02h`, `09h`, `40h` and `4Ch` of `int 21h` (**tool.emu.target**, **tool.emu.dos**); decided at step 13 |
| 32 | 06-memory | API of `an/unsafe` | The pointer kinds are the types `Flat`, `Near` (of the width of `usize`), `Far` and `Program` (32 bits); `Map[T](addr)` takes an address of a pointer kind, evaluated at compile time; `Data(x)` returns the address of the bytes of a string or slice as a `usize`; an architecture package declares constructors such as `i8086.Far(segment, offset)` (**mem.unsafe.api**, **mem.unsafe.data**); decided at step 14 |
| 33 | 13-targets, 05-statements | Registers of calls of the instruction package and of `asm` statements | The registers named by the fields are loaded except the reserved registers of the ABI and the flags, which are only stored; the immediates of a call are constants (**target.arch.instr-package**, **target.arch.abi**, **stmt.asm.syntax**); decided at step 14 |
| 34 | 20-toolchain, 23-conformance | The DOS emulator and its observable behavior | A text screen at `0xB8000` written by `int 10h` and the console output of DOS, keys from the standard input, time from the executed cycles from midnight of 1 January 2026; `anlang run -screen`; tests with `.screen` and `.in` files, run only in the emulator if they use the architecture directly (**tool.emu.dos**, **conf.suite.layout**, **conf.backend.arch**); decided at step 14 |
| 24 | 10-compile-time | `meta` API | `Name`, `Fields`, `Variants`, `Get`, `Set`, `Is`, `Make`, `Tag` (10.4) |
| 25 | 11-data-safety | Where `Secret` and `Untrusted` live | Predeclared types; operations in `an/secret` and `an/untrusted` (**safe.types.predeclared**) |
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

### 1.3 Accepted technical decisions

Provisional technical choices accepted as written on 2026-10-25. The rule text in the spec files is normative.

#### 01-lexical
- Non-canonical formatting: error in every build (**lex.source.canonical**).
- A call `requires(x)` at the start of a body is a contract clause; write `(requires)(x)` otherwise.
- `layout` is written in the leading clause region of the struct field list.
- Untyped rune constants default to `i32`.

#### 02-types
- Unexported variants of exported enums allowed; external `switch` needs `default` (**type.enum.variant-export**).
- Byte-order types usable anywhere (**type.endian**).
- Run-time conversion to `cstring`/`dstring` copies and panics on an embedded terminator; `ffi.CString(s)` is equivalent to `cstring(s)` (**type.string.terminator-conv**, **ffi.mem.cstring**).
- Float-to-integer conversion panics when not representable (**type.conv.float-int**).
- `fixed` and integer `simd` overflow checked as for integers.
- `layout` structs hold only fixed-size plain data (**type.layout.fields**).

#### 03-declarations
- No channels at package level (**decl.var.package-kinds**).
- Handler names are not declared in any scope (**decl.handler.name**).

#### 04-expressions
- `<<` on checked integers discards bits as in Go (**expr.int.shift**).
- Float-to-integer conversion out of range is overflow (**expr.float.to-int**).
- Integer `simd` lanes checked; `fixed` `*` and `/` truncate toward zero (**expr.known.fixed**).
- A `...any` parameter is usable only in `for const`, `len` and forwarding (**expr.call.any-forward**).

#### 05-statements
- `default` permitted in variant switches (**stmt.switch.variant.default**).
- Deferred calls returning errors need an explicit discard.
- Dead stores of errors not covered by **stmt.error.unused**.
- `inout` operand path in `defer` fixed at the statement; access begins when the call runs (**stmt.defer.inout-args**).

#### 06-memory
- Changing the length of an `inout` subslice panics (**mem.inout.subslice**).
- Arena API adds `Get`, `Delete`; stale handles panic.
- Arenas in `core` need `Alloc` to grow; a fixed-capacity arena may be added.
- Values are released no later than the end of the scope of their last copy.

#### 07-effects
- Exact forbidden effects per profile (**eff.profile.allowed**).
- Function values stored in data structures resolved by function type over the whole program.
- `Alloc` added wherever an operation may copy (**eff.infer.determinism**).
- `Without` on functions with parametric references checked at declaration and at each call (**eff.without.check**).

#### 08-contracts
- `result` only for exactly one unnamed result (**contract.expr.result**).
- Type invariants with exported fields checked at boundaries.

#### 09-concurrency
- `task.LockThread` lasts until the task ends (**conc.os.lock-thread**).
- `RWMutex.RLock()` yields a copy (**conc.sync.rwmutex**).

#### 11-data-safety
- Text and structured decoders return `Untrusted`; raw byte reads do not (**safe.untrusted.sources**).
- No shell-string API (**safe.untrusted.shell**).
- `ExposeSecret` declared with `effect.Declare` in `an/secret`.

#### 12-ffi
- Calling an unavailable optional library panics (**ffi.lib.optional**).
- Unresolved C-library symbols in object files are a build error (**ffi.static.unresolved**).
- C strings returned as `ffi.Handle`, copied with `ffi.GoString`.
- At least 1024 callbacks, never freed; foreign threads run callbacks as tasks (**ffi.callback.lifetime**, **ffi.callback.context**).

#### 13-targets
- All formats in `Formats` are produced (**target.format.all**).
- Placement: first `Exec` and first non-`Exec` region.
- `main` profiles per platform: table in 13.4.
- Project target packages found by directory name (**target.select.resolve**).
- `target.Pointers` fields `Flat`, `Near`, `Far`, `Program`.

#### 14-embedded
- Unhandled vectors panic via `OnPanic`.
- CPU clock: `Clock` field of `target.Desc`.

#### 15-os
- `PhysAddr`, `VirtAddr` in `an/mm`; width per target.
- Default memory order `SeqCst` (**os.smp.atomic**).
- Boot entry signatures per boot platform (**os.boot.entry**).

#### 16-gpu
- Launch size in contracts: `gpu.GlobalSize()` (**gpu.dispatch.requires**).
- GPU functions allowed in any package that allows `core` (**gpu.subset.profile**).

#### 17-ui
- Background limits compared with the platform description (**ui.cap.background**).
- Effect-to-permission table informative; normative mapping in `an/app`.

#### 18-stdlib
- Required set: 18.3 plus 18.3.1 and 18.3.3.
- `an/runtime` not importable.
- `an/log` in all profiles, `an/html` in `app` and `ui`.
- `an/simd` in all profiles.

#### 19-runtime
- Release vs development builds (**rt.dev.builds**).
- Panic exit status non-zero.
- Stack size rounded to target alignment.

#### 20-toolchain
- Exit statuses 0 / 1 / 2 (**tool.exit-status**).
- Test references in `REQUIREMENTS`: bare names unique, otherwise qualified.
- Requirement identifiers like `AUTH-3`.
- `anlang diff` takes two directories.
- Edition migration via `anlang fmt -edition`.
- `anlang flash` builds first (**tool.cmd.flash**).

#### 21-protocol
- Positions: 1-based line, 1-based byte column.
- Fix guarantee: diagnostic disappears and no new error appears (**proto.diag.fix**).
- Own debugger methods, no Debug Adapter Protocol.

### 1.4 New decisions

| # | File | Question | Decision |
| --- | --- | --- | --- |
| 1 | 02-types, 01-lexical | Zero value of a type parameter | `var x T` and other zero-value uses are allowed; they give `T` the inferred constraint "requires a zero value", propagated like effect summaries; instantiating it with a type without a zero value is an error at the instantiation site with the chain to the use; `anlang doc` and `anlang query` show the constraint (**type.zero.param-use**, **type.zero.param-constraint**, **type.zero.param-instantiation**, **type.zero.param-tools**) |
| 2 | 10-compile-time, 20-toolchain | Generators | Declared in `anlang.mod` by `generate <generator> <inputs> -> <output package>`; output is not committed, it is produced into the build cache keyed by the hash of generator and inputs; the output package is importable like a project package; `anlang gen -show` prints it; a project directory with the output path is an error (**tool.mod.generate**, **tool.mod.generate.unique**, **tool.cmd.gen**, **ct.gen.declared**, **ct.gen.cache**, **ct.gen.package**, **ct.gen.conflict**) |
| 3 | 18-stdlib | Printing to standard output | `fmt.Print`, `Println`, `Printf` return no results and panic if the write fails (effects `Console`, `Panic`); `fmt.Fprint*` to an `io.Writer` return `(int, error)` as in Go (**std.fmt.print**, **std.fmt.fprint**, **std.fmt.sprint**) |
| 4 | 18-stdlib | Error identity | `errors.New` errors compare by value (equal text, equal error); `errors.As` has the comma-ok form `As[T](err) (T, bool)`; the recommended form for a package's errors is an `enum` with `Error() string`, identity from the type (**std.errors.value**, **std.errors.as**, **std.errors.enum**) |
| 5 | 13-targets, 15-os | BIOS boot sector target | Target `an/target/bios-boot` (i8086, `core`); format `target.BootSector`: image at `0x7C00`, at most 510 bytes, padded, signature `0x55 0xAA`, 512-byte file; entry `func main()` with `DS=ES=SS=0`, `SP=0x7C00`; `bios.BootDrive()`; returning from `main` halts (**os.boot.bios**) |
| 6 | 13-targets | Standard targets and their names | Naming rules (OS: `<platform>-<arch>`; DOS/BIOS by output; boot: `<environment>-<arch>`; microcontrollers by chip; mobile by platform; `web`) and the list of 22 required targets with the roadmap step of each; architecture `armv6m` added for `rp2040` (**target.std.name**, **target.std.required**) |
| 7 | 03-declarations | File suffixes with `_` in architecture names | Suffixes are matched against the end of the file name instead of splitting at `_`, so `main_x86_64.an` and `main_linux_x86_64.an` select correctly (**decl.file.suffix**); found while implementing the build driver |
| 8 | 02-types | Separator of variant fields | Commas, as in a parameter list: `TooLong{word string, max int}` (**type.enum.fields**) |
| 9 | 20-toolchain | Canonical formatting | gofmt style with tabs for the part shared with Go, imports sorted, layout of Anlang constructs; the canonical form is a fixed point of formatting; heuristics decide by the output layout where gofmt is not idempotent (**tool.fmt.style**, **tool.fmt.fixed-point**, **tool.fmt.differences**); anlang.mod values aligned at column 9 (**tool.mod.format**) |
| 10 | 23-conformance | Layout of the conformance suite on disk | `conformance/testdata/<rule>/`: single-file tests `<name>.an` of module `conf` and project tests `<name>/`; the expected rule set in one `// want:` comment, further rules in `// checks:`; diagnostics without a rule never match (**conf.suite.layout**) |
| 11 | 02-types, 03-declarations, 04-expressions, 05-statements | Rule identifiers for errors inherited from Go | General rules for what is "as in Go": expressions (**expr.go**), statements, including labels and missing returns (**stmt.go**), conversions (**type.conv.go**); unused imports (**decl.import.used**) and unused local variables (**decl.var.unused**) are errors, as in the Go compiler; found while implementing the checker |
| 12 | 02-types | Receive with a discarded value from a channel of a type without a zero value | Permitted: a receive statement `<-ch` and `case <-ch:` in `select` do not use the zero value (**type.zero.chan-receive**); found while implementing the checker |
| 13 | 02-types, 04-expressions | Calling an `inout` method through a non-addressable interface value | Permitted: whether the dynamic method has an `inout` receiver is not known statically; the call modifies the copy held by the interface value, which is discarded for a temporary (**type.interface.inout-method**, **expr.method.interface-inout**); found while implementing the checker |
| 14 | 06-memory | Calling parameters of function type, such as `yield` of iterators | The obligation is at the call: a parameter of function type is known non-nil in its function, and the argument for it must be proven non-nil; `range` passes a non-nil `yield`; interface parameters keep **mem.nil.call** (**mem.nil.func-param**); found while implementing the checker |
| 15 | 22-compiler, 20-toolchain | Form of the IR, its textual format and how to run it | SSA with storage cells for variables, explicit `copy` at logical copies and monomorphization of generics; the textual format of 22.10.2, printed by `anlang build -ir`; `anlang run -emu ir` runs the IR interpreter for any target, and a panicking program exits with status 2 (**tool.cmd.build.ir**, **tool.emu.ir**) |
| 16 | 23-conformance | Expected behavior of reference programs and the comparison with Go | A file `<name>.out` next to a positive test holds standard output and standard error in order and a final `exit status N` line; the "run" programs of the Go distribution are compared in the IR interpreter, except programs whose behavior depends on a listed deviation of Anlang (**conf.suite.layout**, **conf.reference**, **conf.go.common-subset**) |
| 17 | 06-memory | A non-addressable first operand of `copy`, `delete` or `clear`, such as `copy(f(), src)` | Rejected: the first operand is passed with an `inout` access and must be addressable; a map element is not (**mem.inout.builtin**, **expr.addr.map**); found while implementing the IR interpreter |
| 18 | 04-expressions | Constant expressions of wrapping types; `x / -1` for the minimum value of a signed wrapping type | Constants of wrapping types are reduced modulo 2ⁿ and a constant conversion to a wrapping type truncates, as for values; an untyped constant without a conversion must still be representable; `min / -1` wraps to `min` and `min % -1` is 0, as in Go, so division by zero is the only panic of wrapping arithmetic (**expr.wrap.const**, **expr.wrap.other-ops**); found while implementing `an/wrap` |
| 19 | 07-effects | Exactly which operations are sources of `Alloc`, `Panic` and `Block`, and what a parametric reference means when the argument is unknown | Closed lists of operations decided syntactically on the typed tree, so that two implementations compute the same summaries: writes into slice elements have `Alloc` because the buffer may be shared, conversions to interfaces have `Alloc` except for scalars, empty structs and enums without data, channel operations of `select` cases wait only as part of the `select`; an unknown argument closes a parametric reference to the open reference of the parameter type, and conversions between interface types let implementations flow (**eff.source.alloc-ops**, **eff.source.panic-ops**, **eff.source.wait**, **eff.source.builtin**, **eff.infer.close**, **eff.attr.flow**); found while implementing effect inference |
| 20 | 07-effects | At which functions a violated attribution constraint is reported | At each function where the effect enters the package, from a source in its body or from a function of another package; callers in the same package are not reported again, so one source gives one diagnostic per package (**eff.constraint.report**); found while implementing profile checks |
| 21 | 10-compile-time | Concrete compile-time evaluation limits; whether a project may raise them | 10,000,000 IR instructions and 16,777,216 values per evaluation, fixed per edition, not configurable (**ct.limit.values**) |
| 22 | 02-types, 07-effects, 08-contracts | Narrowing conversions: compile-time rejection (**type.conv.int-narrow**) or run-time checks (**contract.check.runtime**, **eff.source.panic**) | Rejection unless proven; the run-time check lists name conversions from floating-point to integer types instead. Until the solver, proofs use a closed set of interval facts: constants, type ranges, `&` and `%` with constants, `>>` by constants, and range keys over known lengths (**type.conv.int-narrow-facts**); found while implementing compile-time evaluation, where the CRC example of 10.2 needs `u32(i)` |
| 23 | 10-compile-time | Composite literals in constant declarations | Permitted: a composite literal makes the constant a computed constant, like a call (**ct.const.call**) |

## 2. Deferred to implementation

Questions that are not design decisions. Each is decided at the roadmap step given ([24-roadmap](24-roadmap.md)); the file's own "Open issues" subsection has the details.

| # | File | Question | Step |
| --- | --- | --- | --- |
| 1 | 12-ffi | Binding form for JNI table entries | 65 |
| 2 | 14-embedded | Overriding interrupt priorities | 33 |
| 3 | 14-embedded | Multi-core microcontrollers | 52 |
| 4 | 14-embedded | Response-time analysis | 53 |
| 5 | 15-os | Names and APIs of kernel thread, per-CPU and spin lock types | 54 |
| 6 | 15-os | User memory in system calls and `Untrusted` | 54 |
| 7 | 16-gpu | Creating and reading `gpu.Out[T]` buffers | 64 |
| 8 | 16-gpu | Floating point guarantees and `f64` on GPUs | 64 |
| 9 | 16-gpu | Direct3D shader targets | 64 |
| 10 | 17-ui | Native view API | 62 |
| 11 | 17-ui | Form of the state migration function | 63 |
| 12 | 17-ui | `Background` on Android | 65 |
| 13 | 22-compiler | General-purpose allocator for `app` | 25 |
| 14 | 23-conformance | Performance thresholds | 26 |
| 15 | 20-toolchain | Reformatting of doc comments by anlang fmt (gofmt reformats them with go/doc/comment) | 40 |
