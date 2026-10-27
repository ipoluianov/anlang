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
| 16 | 08-contracts | Entry value of `inout` parameters (`old(x)`) | Predeclared function `old(e)` in `ensures` clauses, loop invariants and `assert`, over the parameters and the receiver; after a call `old` gives the arguments before it (**contract.expr.old**); decided after step 24 |
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
| 35 | 13-targets | Descriptions of architectures with halfword encodings, such as ARMv7-M | `Unit` 16; bit ranges `name[hi:lo]` of operands in encodings, and fields that appear twice; operand kinds with `Registers`, `Scale`, `Transform`, `Syntax` and `List`; up to four operands; `PCRead` for a program counter that reads ahead, and a `Zero` mask of register bits; addressing modes as alternatives per base register (**target.arch.schema**, **target.arch.encoding**, **target.arch.execution**, **target.arch.asm-syntax**); decided at step 15 with `arch.ARMv7M` |
| 36 | 13-targets, 20-toolchain | The platform `bare` and the target `stm32f103c8` | A vector table written by the platform rules `vectors` and `vector` (from step 17; before it, by rule `start`); the console and the exit status through semihosting, so that `stm32f103c8` provides `Console`; formats `target.ELF`, `target.BIN`, `target.HEX`; a Cortex-M emulator with the regions of the target and semihosting; QEMU as an oracle on a board with flash at the same address (**target.platform.bare**, **target.format.kind**, **tool.emu.cortexm**, **conf.oracle.use**); decided at step 15 |
| 37 | 13-targets, 22-compiler | Frames and templates of a load-store architecture | `ABI.FrameBottom`: the frame register is the stack pointer at the bottom of the frame and slots have offsets that are not negative; the placeholders `{x.h0}`, `{x.h1}` and `{x.off}`, register lists in braces and the directive `.word`; writable data in RAM, cleared by the entry code (**target.arch.abi**, **target.arch.template**, **comp.gen.frame**, **target.platform.entry**, **comp.gen.link**); decided at step 15 |
| 38 | 13-targets, 22-compiler | Floating point on targets without hardware for it | Software floating point in the runtime package on the bits of the values, with correctly rounded IEEE 754 results; decimal formatting by exact arithmetic on natural numbers, shortest digits for `%v` (**target.floats.soft**, **comp.gen.float**); decided at step 15 |
| 39 | 14-embedded, 10-compile-time, 13-targets | Device descriptions and the package of the device | `target.Device` in `an/target`, read by `svd.Load` of package `an/svd` (profile `app`) from an SVD file with `gen.ReadFile` at compile time; the SVD file of `stm32f103c8` is written for Anlang from the reference manual; `an/device` has a struct of registers per group, mapped variables per peripheral, a type per register and field, methods to read a field and `With…` to replace it, enum types for allowed values (**emb.device.schema**, **emb.device.svd**, **emb.device.api**, **ct.eval.read-input**); decided at step 16 |
| 40 | 20-toolchain | Peripheral models of the Cortex-M emulator | Registers of the device description with their reset values; the STM32F1 clock tree, GPIO, general-purpose timers, USART, SysTick and the interrupt controller; time from the cycles at the selected clock; `WFI` advances the time to the next event that sets an enabled interrupt pending; `anlang run -gpio` and `-for` (**tool.emu.cortexm**); decided at step 16 |
| 41 | 14-embedded, 22-compiler | Interrupt handlers and the vector table | `interrupt` declarations bound by name to the interrupts of the device; the platform rules `vectors` and `vector`; `enter.handler` and `leave.handler`; a default handler that panics with `unhandled interrupt`; one priority, without preemption (**emb.irq.vectors**, **comp.gen.handler**); decided at step 17 |
| 42 | 14-embedded | Critical sections and the initialization phase before flow analysis | A critical section is the body of a range statement over the method `Lock` of a peripheral, which disables the interrupts with `irq.save` and restores them with `irq.restore`; the initialization phase is the top-level statements of `main` before the first top-level call of `interrupts.Enable` (**emb.lock.form**, **emb.init.end**); decided at step 17 |
| 43 | 09-concurrency, 06-memory | Atomic values before handles | Package-level variables of the types of `an/atomic` without initializers, with `inout` receivers, whose operations disable the interrupts on single-core targets (**conc.sync.atomic**, **mem.global.kinds**, **comp.gen.static**); decided at step 17 |
| 44 | 14-embedded, 22-compiler | Stack analysis | The depth from the frames, `ABI.Params` and `ABI.InterruptFrame`; indirect calls by signature among the functions whose address is taken; main plus the deepest handler; the panic routines count once; recursion rejected on targets with `Stack` (**emb.stack.compute**, **comp.gen.stack**); decided at step 17 |
| 45 | 13-targets, 20-toolchain | WebAssembly as an architecture of descriptions | `wasm32` is a description with the encodings of WebAssembly (LEB128 immediates as one instruction form per length) and its text syntax, but without semantics; its interpreter is a built-in WebAssembly 2.0 interpreter, checked against the official test suite and V8, which also runs the rules test (**target.arch.structured**, **tool.emu.wasm**); decided at step 18 |
| 46 | 22-compiler, 13-targets | Structured control flow in the code generator | Ramsey's method on the graph of the parts between labels: merge nodes are blocks, loop headers are loops, label operands become depths; the operations `block`, `loop` and `end` are rules; every routine is a function `[] → []` with its frame in the linear memory, the address of a function is its index in a table of all functions (**comp.gen.structured**, **comp.gen.link-structured**, **target.format.wasm**); decided at step 18 |
| 47 | 13-targets | The platform web | A module that imports `fd_write`, `proc_exit` and `fd_read` of WASI, so that it runs in Node.js and the runtimes of WASI as well as in browsers; `main` in the profiles `core`, `app` and `ui`; an HTML page format that embeds the module and reads input with JavaScript Promise Integration (**target.platform.web**, **target.format.html**); decided at step 18 |
| 48 | 18-stdlib, 13-targets | Reading standard input | `console.ReadByte` of the new package `an/console`, with the effects `Console` and `Block`, over the platform operation `read` on `dos`, `bare` and `web` (**std.console.read**); decided at step 18 |
| 49 | 14-embedded, 19-runtime | Which programs the stack analysis checks | Only programs whose `main` runs in profile `core`; others check their stack when they use it (**comp.gen.stack**); decided at step 18 |
| 50 | 08-contracts, 22-compiler | The method and the certificates of solver stage 1 | Case splits, Fourier–Motzkin elimination with tightening, and branches on variables with a value that is not an integer, at most 64 deep; certificates of splits, branches and refutations by positive combinations with tightening, over a normal form that the checker computes itself; 200,000 steps per obligation in edition 1.0 (**contract.solver.normal-form**, **contract.solver.certificate-form**, **contract.solver.steps**, **comp.solver.stage1**); decided at step 20 |
| 51 | 02-types | Narrowing conversions before contracts | The solver proves them from linear terms over the variables of the program, with the facts of the old intervals, exact checked arithmetic and the ranges of the types of the subexpressions; a refutation shows its counterexample (**type.conv.int-narrow-facts**); decided at step 20 |
| 52 | 08-contracts, 22-compiler | How the obligations get their facts | Symbolic execution of the syntax tree: joins by disjunctions of the facts of the branches, loops from a state with the assigned variables unknown, the index of `range` loops and induction variables of `for` loops as built-in facts; run-time checks with 5,000 steps (**comp.vc**, **contract.loop.default**, **contract.loop.induction**, **contract.solver.steps**); decided at step 21 |
| 53 | 08-contracts, 02-types | Proven checks and narrowing conversions with the facts of the program point | The checks of index and slice bounds, overflow, division and shift counts are removed when proven, marked in the IR; a narrowing conversion that the facts of the expression do not prove is proven with the facts of its point, with a counterexample if refuted (**contract.check.proven**, **type.conv.int-narrow-facts**); decided at step 21 |
| 54 | 14-embedded | `emb.reg.values` for fields without allowed values | The `With…` method of a field of n bits whose type is wider has the precondition `requires v <= 2ⁿ − 1`, which the solver proves at each call; a field type of exactly n bits is not needed (**emb.reg.values**, **emb.device.api**); decided at step 22 |
| 55 | 08-contracts, 22-compiler | Bit vectors in solver stage 2 | Encodings in linear integer arithmetic: wrapping as x − 2ⁿ·q in the range, masks and shifts by constants as quotients by powers of two, other bitwise operations bit by bit over chains of halvings, with the definitions shared per term; bounds propagation in the solver; bitwise operations in contracts on mathematical integers in two's complement (**comp.solver.stage2**, **contract.expr.bitwise**); decided at step 22 |
| 56 | 19-runtime, 14-embedded | Proven bounds on recursion | A function that calls only itself, with an integer parameter that the solver proves smaller at each such call; the depth from the range of its type narrowed by constant preconditions, at most 2²⁰; the recursion is checked on every target, the stack only with a `Stack` size (**rt.stack.core-recursion**, **emb.stack.compute**, **comp.gen.stack**); decided at step 22 |
| 57 | 08-contracts, 22-compiler, 18-stdlib | Loop invariants and sums of elements | Initiation before the loop, preservation after the body and the post statement, the invariants assumed at the header and so at the exit; a range loop has the number of completed iterations at its header; the integer elements of a sequence by its prefix sum with congruence facts, stores as updates of it; `math.Sum` of package `an/math` only in contracts, untyped like a constant; elements outside the bounds of an operand in a contract unknown (**contract.loop.obligations**, **contract.expr.elements**, **std.math.contract**); decided at step 23 |
| 58 | 08-contracts, 22-compiler | Type invariants in the symbolic execution | The fields of local struct variables are tracked, up to 64 values; the invariants are proven at the boundaries and assumed for parameters, results of calls, fields, elements and package-level variables; the zero value is decided once per type by the solver (**contract.type.boundaries**, **contract.type.assume**, **contract.type.zero**); decided at step 23 |
| 59 | 08-contracts, 22-compiler | `enum` and `Option` in the symbolic execution | An enum value is the index of its variant with the fields of each variant; variant switches, comparisons, constructors and `Option.Get` give facts of the index; equality of fields beyond integers, booleans and structs of them gives only the equal index (**comp.vc**); decided at step 24 |
| 60 | 18-stdlib, 08-contracts | Syntax and meaning of bounded quantifiers | Contract functions `math.ForAll(lo, hi, pred)` and `math.Exists(lo, hi, pred)` over [lo, hi) with a predicate literal of one integer parameter whose body is one `return`; a new variable where the quantifier is needed for every value, otherwise instances at the indices of the elements read (**std.math.quantifier**); decided at step 24 |
| 61 | 22-compiler | The search of the solver beyond stage 1 | Splits of the disjunctions that the model violates, with the fewest live disjuncts first; cases whose refutation does not use them are dropped; equalities eliminated by substitution; congruences and instances added in rounds from the counterexamples (**comp.solver.search**, **comp.vc**); decided at step 24 |
| 62 | 08-contracts, 23-conformance | Which obligations every implementation proves, and solver versions | A guaranteed fragment of edition 1.0: at most 16 variables, 64 inequalities with at most two variables and coefficients ±1, and 2 disjunctions, without a rational solution; outside it a later solver version SHOULD prove what an earlier one proved and MUST accept the conformance suite (**contract.solver.guaranteed**, **contract.solver.versions**, **conf.solver.guaranteed**); decided after step 24 |
| 63 | 06-memory, 07-effects | Writes into the elements of `inout` slices in `core` | The buffer of an `inout` slice is unique during the call; the caller copies an argument that is not unique, by its form: a slice of an array or an `inout` slice parameter needs no copy (**mem.inout.unique**, **eff.source.alloc-ops**); decided after step 24 |
| 64 | 06-memory, 04-expressions | Containers that give `inout` access to their parts | Borrowing functions: a declared function or method with an `inout` parameter or receiver that returns an iterator may return a closure capturing them; the call must be a range expression and holds its arguments until the loop ends; the closure record is in the caller's frame (**mem.ref.borrowing**); decided after step 24 |
| 65 | 09-concurrency, 08-contracts | Cancellation and panics inside a critical section | No wait point in the body of a loop over `m.Lock()`, so cancellation cannot interrupt it; a panic there poisons the mutex; `ensures` says nothing about calls ended by cancellation (**conc.sync.lock-no-wait**, **conc.sync.poison**, **contract.obligation.ensures**); decided after step 24 |
| 66 | 11-data-safety | Ways out of `Untrusted` and `Secret` | Functions that format into a value of the program reject `Untrusted` arguments; the function of `secret.Map` has only `Alloc` and `Panic`, captures no handles or function values, and its panics are reported as `***` (**safe.untrusted.print**, **safe.secret.map**); decided after step 24 |
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
| 16 | 22-compiler | Code density and speed: values kept in registers across machine operations instead of frame slots, so that programs with software floating point fit images of 64 KiB, and frames beyond the displacement range of loads (4 KiB on armv7m) | 26 |
| 17 | 13-targets | ARMv7-M: the IT instruction, `SVC` and fault handlers, register operands shifted by a constant, loads and stores with writeback, exclusive accesses | 17 |
| 18 | 13-targets, 19-runtime | The panic actions `Reset` and `Halt` on bare targets; a panic now writes its message and exits through semihosting | 33 |
| 19 | 22-compiler | `fmt.Printf` `%b`, `%x`, `%X` and the flag `#` for floating-point values; precisions that need more than 400 digits | 25 |
| 20 | 14-embedded, 10-compile-time | Vendor SVD files: clusters and `dim` arrays, and files larger than the compile-time evaluation limits allow | 52 |
| 21 | 14-embedded | Interrupt priorities and preemption; SysTick and the other system exceptions as handlers | 33 |
| 22 | 09-concurrency, 06-memory | Atomic values as handles (**conc.sync.handle**) and constant initializers of package-level variables | 21 |
| 23 | 14-embedded | The initialization phase and the ownership check along the control flow; pins as ownership units and the HAL packages `an/gpio`, `an/uart`, `an/timer` | 21 |
| 24 | 13-targets, 22-compiler | Floating point on `wasm32` with the instructions of WebAssembly instead of software floating point, and the vector instructions for `an/simd` | 25 |
| 25 | 19-runtime | A stack overflow on `web` as a panic with its message instead of a WebAssembly trap | 25 |
| 26 | 12-ffi, 13-targets | `js.Import` and the imports of a project on `web`; events of the page delivered to the program without a blocking read | 62 |
| 27 | 20-toolchain | The proposals after WebAssembly 2.0 in the interpreter (tail calls, exceptions, garbage collection, multiple memories) | 62 |
| 28 | 22-compiler | Integer models of equalities with coefficients other than ±1 (elimination of equalities as in the Omega test) and cutting planes for problems without bounds, which the branches of stage 1 may give up on | 29 |
| 29 | 08-contracts, 22-compiler | The cache of proven obligations (**contract.solver.cache**) and incremental solving | 37 |
| 30 | 08-contracts | Contract expressions with calls of pure functions; the facts of elements that are not integers, of structs with more than 64 values and of maps in the symbolic execution | 29 |
| 31 | 08-contracts | Contracts and narrowing conversions in generic functions and in functions with `goto` | 29 |
| 32 | 08-contracts, 22-compiler | Identities of bitwise operations that need case analysis on the bits, such as `(a\|b)&a == a`: the linear constraints of the bits leave them to the branches of the solver, which give up | 29 |
