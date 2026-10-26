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
| `lib` | the Anlang sources of the standard library, embedded in the executable (**impl.single-binary**) |
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

## 22.10 Intermediate representation

The intermediate representation (IR) is the shared SSA form of step 5 of the pipeline (22.3). It is built by package `lower` from the typed tree of the whole program and executed by the IR interpreter of package `interp`, which is the reference for every backend (see [23-conformance](23-conformance.md), **conf.backend.ir**).

### 22.10.1 Structure

- A *program* is a set of functions with a designated function `main`, the size of `int` for the target, and the method sets of the concrete types converted to interfaces, used by dynamic calls and type assertions.
- A *function* has parameters, free variables (for function literals) and a graph of basic blocks; the first block is the entry. Every block ends in exactly one control instruction: `jump`, `if`, `return` or `panic`.
- Every value is defined once (SSA). Values are instruction results, parameters, free variables, constants, functions and built-in functions.
- Local variables are storage cells: `alloc` yields the address of a new cell, `load` reads it and `store` writes it. Variables captured by function literals and variables whose parts are modified are therefore handled without φ-functions; a later pass may promote cells to SSA values.
- An `inout` parameter is an address. `fieldaddr`, `indexaddr` and `subsliceaddr` yield addresses of parts of values; an `inout` argument passes such an address (**mem.inout.param**, **mem.inout.subslice**).
- Types are the types of the checker with all type parameters substituted: generic functions and types are translated by monomorphization, one function per list of type arguments.
- Value semantics is explicit: `copy` marks every point where a value is logically copied (assignment, argument passing, return, storing into a part, capture by a closure). A backend implements it by copy-on-write (22.6); the interpreter shares the value and copies it on the first write through an address.
- A hardware mapping (**mem.unsafe.map**) is the value `global`: the address of its memory, printed by its qualified name. An `asm` statement is the instruction `asm(regs) "text"`, whose result holds the output registers (**stmt.asm.syntax**).
- Constructs whose translation belongs to later roadmap steps (channels, `go`, `select`, compile-time evaluation, the standard library) are reported by the IR builder as not supported yet, with the step number.

### 22.10.2 Textual format

`anlang build -ir` prints the IR of the program in the following textual format (see [20-toolchain](20-toolchain.md)). The format is for reading and for tests; it is not an input of the toolchain. For example, the function

```go
func Sum(xs []int) int {
    t := 0
    for _, x := range xs {
        t += x
    }
    return t
}
```

of package `main` of module `conf` is printed as:

```
func conf.Sum(xs []int) int
b0: // entry
	t0: *[]int = alloc // xs
	store t0, xs
	t1: *int = alloc // t
	store t1, 0:int
	t2: []int = load t0
	t3: []int = copy t2
	t4: int = call len(t3)
	t5: *int = alloc // index
	store t5, 0:int
	jump b1
b1: // range.head; preds: b0, b3
	t6: int = load t5
	t7: bool = t6 < t4
	if t7 b2 b4
b2: // range.body; preds: b1
	t8: int = index t3, t6
	t9: *int = alloc // x
	store t9, t8
	t10: int = load t9
	t11: int = load t1
	t12: int = t11 + t10
	store t1, t12
	jump b3
b3: // range.post; preds: b2
	t13: int = load t5
	t14: int = t13 + 1:int
	store t5, t14
	jump b1
b4: // range.done; preds: b1
	t15: int = load t1
	return t15
```

1. A function starts with a header `func NAME(PARAMS) RESULTS`, followed by `free(...)` for a function with free variables. `NAME` is the package path and the name: `conf.main`, `conf.Point.Add` for a method, `conf.Map[int,string]` for an instance of a generic function, `conf.Stack.Push[string]` for a method of an instance of a generic type, `conf.main$lit1` for a function literal, `conf.main$yield2` for the body of a range-over-func loop, `Option[int].Some` for the constructor of a variant, `conf.Print(int,string)` for an instance of a function with a `...any` parameter, whose tuple elements are separate parameters `args$0`, `args$1`, …. An `inout` parameter is written `NAME inout TYPE`.
2. Each block starts with a line `bN:`, optionally followed by a comment with the origin of the block and its predecessors.
3. Each instruction is on its own line, indented by a tab. An instruction with a result is written `tK: TYPE = OPERATION`; an instruction without a result is written `OPERATION`. Result numbers are unique within a function.
4. An operand is written as a result name `tK`, the name of a parameter or free variable, the name of a function or built-in function, or a constant `VALUE:TYPE`; the zero value of a type is written `zero:TYPE`.
5. Types are written as in Anlang source; an address of a value of type `T` is written `*T`. This is the only place where `*T` appears: it is an IR type, not an Anlang type.

The operations are:

| Operation | Meaning |
| --- | --- |
| `alloc` | address of a new storage cell holding the zero value; the comment names the variable |
| `load a`, `store a, v` | read and write the value at address `a` |
| `copy v` | logical copy of `v` (**mem.value.copy**) |
| `fieldaddr a, i`, `indexaddr a, i` | address of field `i` / element `i` of the value at address `a`; `indexaddr` checks the bounds |
| `subsliceaddr a[lo:hi]` | address of the elements `lo` to `hi-1` of the slice or array at `a`, for an `inout` argument (**mem.inout.subslice**) |
| `field v, i`, `index v, i` | field / element of a value; `index` checks the bounds |
| `lookup m, k [, ok]` | map element, or the zero value; with `ok`, the comma-ok pair |
| `mapupdate a, k, v` | sets an element of the map at address `a` |
| `slice v[lo:hi:max]` | slice expression; omitted bounds are empty |
| `x OP y`, `OP x` | arithmetic, comparison and logical operations; integer operations of checked types panic on overflow (**expr.int.checked**) |
| `convert v`, `makeinterface v` | conversion to the result type; conversion of a concrete value to an interface type |
| `typeassert v.(T) [, ok]` | type assertion |
| `makeslice n, c`, `makemap`, `makestruct {...}`, `makearray [...]` | construction of values; `makearray` constructs an array or the elements of a slice literal |
| `makevariant #i {...}`, `variantindex v`, `variantfield v, #i.j` | construction of variant `i` of an `enum` value, its variant number, field `j` of variant `i` |
| `closure F [bindings]` | function value of `F` with values for its free variables |
| `call f(args)`, `invoke v.M(args)` | call of a function, function value or built-in function; dynamic call of method `M` of an interface value |
| `extract t, i` | result `i` of a call with several results |
| `jump bN`, `if v bT bF`, `return [vs]`, `panic v` | control instructions |
| `defer CALL` | deferred call; the arguments are evaluated when `defer` executes (**stmt.defer.go**) |

Built-in functions keep their Anlang names (`len`, `append`, `copy`, `delete`, `print`); the functions of `an/fmt` and `an/bits` that are built into the compiler until roadmap steps 9–10 are named `fmt.Print`, `fmt.Println`, `fmt.Sprint` and `fmt.Sprintln`, and those of `an/bits` `bits.WrappingAdd` … `bits.CheckedMul`; helpers introduced by the IR builder have names that are not Anlang identifiers of the program (`appendslice`, `appendstring`, `mapkeys`, `decoderune`, `Option.Get`).

### 22.10.3 IR interpreter

`anlang run -emu ir` builds the IR of the program and executes it in the IR interpreter (**tool.emu.ir**). The interpreter:

- implements value semantics by copy-on-write of shared values, so its observable behavior is the behavior defined by [06-memory](06-memory.md);
- computes the wrapping types of `an/wrap` modulo 2ⁿ (**expr.wrap.ops**) and the functions of `an/bits` (**expr.bits.functions**);
- performs the run-time checks of the language, with the panic messages of Go: integer overflow (**expr.int.checked**, **expr.int.div**), division by zero, bounds, float-to-integer conversions (**type.conv.float-int**), the length of an `inout` subslice (**mem.inout.subslice**);
- prints `fmt.Print` and `fmt.Println` output to standard output and `print`, `println` and panic messages to standard error, in the formats of Go;
- has no addresses: in `print` output, every non-nil map, slice, function or interface value prints its pointer as `0x1`;
- does not execute channels, `go` and `select` (roadmap step 30);
- does not execute `asm` statements and calls of the functions of instruction packages, which belong to one architecture: a program that reaches them stops with an error, and `unsafe.Data` returns 0.

## 22.11 Code generation

The code generator (package `codegen`) is the backend of every architecture. It is driven by the architecture description and the platform description and contains nothing specific to one architecture (**target.arch.no-compiler-change**). It translates the IR of the program, together with the functions of the runtime package `an/runtime` that the program uses, into machine operations, replaces each operation by the template of an instruction selection rule ([13-targets](13-targets.md), 13.5.7), and links the result into one image that the binary format writes.

### 22.11.1 Data layout

**comp.gen.layout** — Values are laid out in memory as follows; a value is aligned to the smaller of its size and `ABI.Align`, and multi-byte integers are stored in the byte order of the architecture.

| Type | Layout |
| --- | --- |
| `bool` | 1 byte, 0 or 1 |
| integer and wrapping types | their width |
| `string` | the address of the bytes, then the length (`int`) |
| `cstring`, `dstring` | the address of the bytes, which end with the terminator |
| slice | the address of the elements, the length and the capacity (`int`) |
| array | the elements in order |
| struct | the fields in order, each at the next offset aligned for it |
| `enum`, `Option` | the variant number (1 byte for at most 256 variants, otherwise 2), then the fields of the variant laid out as a struct; the size is that of the largest variant |
| function | the address of its code |
| interface | a type number of the word width, 0 for `nil`, then 8 bytes: a dynamic value of at most 8 bytes in place, a larger one by its address |

The type numbers of interfaces are assigned per program to the dynamic types that occur; there is no other run-time type information (**rt.no-rtti**).

### 22.11.2 Frames and calls

**comp.gen.frame** — Each function has a stack frame. Below the frame base, it holds a slot for each IR value and each storage cell, and at the lowest addresses the argument area of its calls. An argument area holds the arguments in order, each aligned to `ABI.Align`, followed by the results. A function addresses its parameters and results at its frame base plus `ABI.Params` (**target.arch.abi**) and writes its results there before `leave`. An `inout` parameter is passed as an address. The caller copies the results out of its argument area after the call.

**comp.gen.values** — An IR value is computed into its slot once and not changed afterwards; a constant is an operand of the template, and a string or a constant of a composite type is stored in the constant data of the image. `load` copies the value from the cell into its slot, `store` copies it into the cell, and `copy` shares the slot of its operand. A `slice` of an array in a storage cell first copies the array into the slot of the result, so that later stores into the cell do not change the slice (**mem.value.copy**).

**comp.gen.escape** — A slice whose elements lie in a stack frame MUST NOT outlive the frame. Until escape analysis (roadmap step 25), the code generator rejects a function that returns a value containing such a slice, as not supported yet.

### 22.11.3 Lowering of IR instructions

**comp.gen.lower** — The code generator expresses the IR instructions as machine operations (**target.arch.ops**):

| IR | Machine operations |
| --- | --- |
| arithmetic on a checked integer type | `add.s`, `add.u`, `sub.s`, … with the trap label calling `PanicOverflow` (**expr.int.checked**) |
| arithmetic on a wrapping type | `add`, `sub`, `mul`, … (**expr.wrap.ops**) |
| `/` and `%` | a test of the divisor calling `PanicDivide` for 0; for a checked signed type, a test of minimum by −1 calling `PanicOverflow`, and for a wrapping signed type the result of **expr.wrap.div** without dividing; then `div.s`, `div.u`, `rem.s` or `rem.u` |
| shifts | a test of a signed count calling `PanicShift` if it is negative; a count wider than a word is limited to the largest word value; then `shl`, `shr.u` or `shr.s` |
| comparisons | `eq` … `le.u` on integers; on strings and on composite values, calls of runtime functions or comparisons of the parts |
| conversions between integer types | `ext.s` or `ext.u` to a wider type, `move` of the low part to a narrower one |
| `fieldaddr`, `indexaddr`, `field`, `index` | address arithmetic with `add` and `mul`; the bounds test calls `PanicIndex` (**expr.bounds.check**) |
| `slice`, `subsliceaddr` | address arithmetic and the bounds tests of Go, calling the `PanicSlice…` functions |
| `alloc`, `makestruct`, `makearray`, `makevariant` | `zero`, `store` and `copy` into the slot |
| `call` | the arguments copied into the argument area, `call` or `callind`, the results copied out |
| `invoke` | a dispatch on the type number of the interface value to the method of each dynamic type |
| `defer` | a record of the deferred call in the frame, linked into a chain that `return` and the panic functions run, newest first (**stmt.defer.go**) |
| `if`, `jump` | `branch`, `jump` |
| `return` | the deferred calls of the frame, then `leave` |
| `panic` | the panic functions with the value printed in the format of the IR interpreter |
| a hardware mapping | memory operations at its address: `load.far` and `store.far` at a far address |
| calls of the instruction package, `asm` | `regin`, the instructions, `regout` and `flagsout` (**target.arch.instr-package**) |
| `fmt.Printf` | the format, a constant, parsed at compile time; a call of a printing function of the runtime package for each directive |

### 22.11.4 The runtime package

**comp.gen.runtime** — The runtime package `an/runtime` is Anlang source in the `core` profile (**rt.lang.anlang**). It provides the functions the code generator calls: the printing of values for `fmt.Print`, `fmt.Println`, `print` and `println` in the formats of 22.10.3, the panic functions, which run the deferred calls and then perform the panic action of the target with the messages of the IR interpreter, and the arithmetic of the operations without rules (**target.arch.select**). Its functions `write` and `exit` are primitives that the code generator implements with the platform operations of the same names. A function of the runtime package is in the image only if the program calls it (**rt.link.piecewise**).

**comp.gen.panic** — Until the debug side tables (roadmap step 41), the action `PrintAndExit` writes `panic: `, the reason and a line break to standard error, without a stack trace (**rt.panic.trace**), and exits with status 2.

### 22.11.5 Linking

**comp.gen.link** — The linker places the entry code (**target.platform.entry**), the functions and the constant data, in this order, at the origin of the binary format. It assembles each instruction with the generated assembler at its address; since the length of an instruction may depend on the addresses of labels, it repeats the placement until no length changes. A build whose image exceeds the size limit of its format is rejected (**target.format.size**).

### 22.11.6 Coverage

**comp.gen.coverage** — The code generator of roadmap step 13 translates every construct of the `core` profile except the following, which it reports as not supported yet with the roadmap step that adds them: floating-point types (step 15); `defer` statements in loops, `inout` subslices, `inout` access to hardware mappings at far addresses, and the directives of `fmt.Printf` other than `%v`, `%d`, `%s`, `%t`, `%c`, `%x`, `%X`, `%o`, `%b` with flags, width and precision (step 25).

## 22.12 Open issues

1. **General-purpose allocator for `app`.** Deferred to implementation — step 25. Own or ported; any allocator that satisfies [06-memory](06-memory.md) and passes the fuzzing of [23-conformance](23-conformance.md).
