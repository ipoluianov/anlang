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
| `link` | linker and format writers: flat, MZ, ELF, PE/COFF, Mach-O, wasm and its HTML page, Intel HEX, UF2 |
| `interp` | built-in interpreters for tests |
| `emu` | built-in emulators: the 8086 with DOS, Cortex-M, the WebAssembly interpreter |
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

**comp.solver.stage1** — Stage 1 (roadmap step 20) is the package `verify`. It refutes the conjunction of **contract.solver.normal-form** by a search that splits its disjunctions in order, skipping one that an inequality of the conjunction satisfies, and decides each conjunction of inequalities by Fourier–Motzkin elimination over the integers: each derived inequality is tightened (**contract.solver.certificate-form**), and the elimination of a variable takes the pairs of its upper and lower bounds. A contradiction gives the steps of a refutation; otherwise substitution in the reverse order of elimination gives a rational solution, with the integer nearest to 0 for each variable that has one in its bounds. A solution with integer values is a counterexample; otherwise the search branches on the first variable whose value v is not an integer, at ⌊v⌋, at most 64 branches deep: a deeper branch gives up, but a counterexample found in another branch is still one. The checker is the package `verify/check`, which shares only the data types.

**comp.solver.stage2** — Stage 2 (roadmap step 22) adds bit vectors, encoded in linear integer arithmetic by `verify/vc`, and bounds propagation to the solver. Before the elimination, the solver bounds each variable of a row by the row and the bounds of its other variables, tightened, in rounds until no bound changes, at most 64 rounds; a variable with equal bounds is substituted in the other rows. Every such bound is a step of the certificate. The encodings for a value x of an integer type of n bits with the range [lo, hi]:

1. *Wrapping.* An arithmetic operation of a wrapping type, a conversion to a wrapping type and `x << k` by a constant k are the term r = x − 2ⁿ·q with a new variable q and the fact lo ≤ r ≤ hi.
2. *Masks and shifts.* `x & (2ᵐ − 1)` is x − 2ᵐ·q with 0 ≤ x − 2ᵐ·q ≤ 2ᵐ − 1, and `x >> k` by a constant k is q with 2ᵏ·q ≤ x ≤ 2ᵏ·q + 2ᵏ − 1.
3. *Bits.* The bits of x are new variables b₀ … bₙ₋₁ in [0, 1] with a chain of halvings xᵢ = 2·xᵢ₊₁ + bᵢ from x₀ = x, where each xᵢ is in the range of the bits left, and the last one is bₙ₋₁, or −bₙ₋₁ for a signed type. The other bitwise operations combine the bits one by one: z = a ∧ b by z ≤ a, z ≤ b, z ≥ a + b − 1, z ≥ 0; z = a ∨ b by z ≥ a, z ≥ b, z ≤ a + b, z ≤ 1; z = a ⊕ b by z = a + b − 2·c with c = a ∧ b; a bit of a constant gives the result directly. The result is Σ 2ⁱ·zᵢ, with weight −2ⁿ⁻¹ for the top bit of a signed type.

The quotients of masks and shifts and the bits of a term are defined once per term at a program point, and the bits of the result of a bitwise operation are its zᵢ, so that equal terms have the same variables and the solver need not prove them equal. A bitwise operation in a contract expression on values outside the range of the type of the operation has an unknown result (**contract.expr.bitwise**).

**comp.solver.search** — From roadmap step 24, the search splits only a disjunction that the rational model of the inequalities violates: of those, the one with the fewest disjuncts that bounds propagation does not refute within 2,000 steps, which count. If the model satisfies every disjunction, the search branches on a value that is not an integer. A refutation of a case that uses neither the inequalities nor the disjunctions that the case adds refutes the conjunction without the split, and the other cases are not searched. The elimination takes first a variable with the coefficient ±1 in an equality, a pair of rows e ≤ c and −e ≤ −c, and combines every other row that has the variable with the half of the equality that cancels it.

**comp.vc** — The package `verify/vc` creates the proof obligations of a program after effect inference and compile-time evaluation. It executes each function body symbolically on the syntax tree: an integer value is a linear term over the variables of the solver, a boolean value a formula, a struct the values of its fields, and a slice, an array or a string the term of its length with, for integer elements, a sequence of elements and the offset of its first element in it. The elements of a sequence are given by its prefix sum, a function P with P(0) = 0 of which element j is P(j+1) − P(j): an application of P is a variable per term, with the facts that it equals the other applications at equal arguments; `math.Sum` of a part of a sequence is the difference of two applications; storing v into element j gives a new sequence whose prefix sum is P up to j and P plus the change of the element after j. The prefix sums of a slice or an array literal of integer constants are constants at constant arguments and, for at most 64 elements, at another argument one of them, or unknown outside [0, n]. A variable that a function literal captures, an element of another type, a field of a value that is not tracked and a package-level variable have an unknown value at each read, with the range of their type and, except for a captured variable, the invariants of its type (**contract.type.assume**); so do the parameters at entry and the results of calls. The facts of a point are the preconditions, the conditions of the path, the postconditions of the calls before it and the checks before it. After a branch the states join: the facts of each branch since the branch point form a disjunct, and a variable with different values gets a new variable equal to the value of each disjunct. A loop runs its body once from the state before it in which the variables that the loop assigns have unknown values (**contract.loop.default**, **contract.loop.induction**). A loop with invariants proves them before the loop and after the body and the post statement along every path that continues the loop, and assumes them at the header, so also at the exit; a range loop over n or xs has at the header the number of completed iterations k, 0 ≤ k ≤ n, with the key k in the body, k + 1 in the proof after the body and n at the exit (**contract.loop.obligations**, **contract.loop.range**). The invariants of struct types are proven at the boundaries of **contract.type.boundaries**: composite literals, the `inout` parameters and receiver at each return, arguments and receivers of calls, results, values sent on channels and stores into fields, elements and package-level variables; a struct of more than 64 values, counting the fields of its fields, is not tracked, and a boundary of such a value is rejected as not supported yet. Before the functions of a package, the solver decides for each struct type with invariants whether its zero value, with the zero values of the fields, satisfies them (**contract.type.zero**). An enum value is the index of its variant and the values of the fields of each variant: a variant switch assumes the index in each case and gives the bindings the fields, and `Option.Get` gives the value of `Some` and whether the index is that of `Some`. A quantifier in a contract is the predicate for a new variable where the formula needs it for every value — a `ForAll` in a goal or an `Exists` in a fact, or the other way under a negation — and otherwise a new boolean whose instances, for `ForAll` the boolean and i in [lo, hi) implying the predicate at i, are taken at the indices of the elements that the state reads, less the offsets of the sequences and the constants d of the index expressions x[i+d] of the predicate, at most 32 (**std.math.quantifier**). The congruences of the prefix sums hold in every state. A proof gives the solver the facts of the state first; the congruences and the instances join them in rounds, at most 16, each of those that the counterexample of the solver violates or does not decide while it has a variable of the counterexample. A call proves the preconditions of the callee with the arguments, gives the variables passed `inout` unknown values and assumes the postconditions. A function with `goto` or type parameters is not executed: its contracts are rejected as not supported yet, and its narrowing conversions need the facts of the expression only. A call of a function in its own body records the state and the arguments for the bound on the depth of its recursion (**rt.stack.core-recursion**), which the IR keeps in `RecursionDepth` of the function. The results of a package are reused while the sources of the package and of the packages before it are the same. The proven checks are marked in the IR (`Proven` of the index, slice and arithmetic instructions), and the code generator emits no test for them (**contract.check.proven**).

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
| `f32`, `f64` | IEEE 754 binary32 and binary64: the bits as the unsigned integer of the width |
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

**comp.gen.frame** — Each function has a stack frame. Below the frame base, it holds a slot for each IR value and each storage cell, and at the lowest addresses the argument area of its calls. An argument area holds the arguments in order, each aligned to `ABI.Align`, followed by the results. A function addresses its parameters and results at its frame base plus `ABI.Params` (**target.arch.abi**) and writes its results there before `leave`. An `inout` parameter is passed as an address. The caller copies the results out of its argument area after the call. With `ABI.FrameBottom`, the frame register holds the bottom of the frame, and a slot, a parameter or a result is at the frame size plus its offset from the frame base. The entry code has a frame too (**target.platform.entry**).

**comp.gen.values** — An IR value is computed into its slot once and not changed afterwards; a constant is an operand of the template, and a string or a constant of a composite type is stored in the constant data of the image. `load` copies the value from the cell into its slot, `store` copies it into the cell, and `copy` shares the slot of its operand. A `slice` of an array in a storage cell first copies the array into the slot of the result, so that later stores into the cell do not change the slice (**mem.value.copy**).

**comp.gen.escape** — A slice whose elements lie in a stack frame MUST NOT outlive the frame. Until escape analysis (roadmap step 25), the code generator rejects a function that returns a value containing such a slice, as not supported yet.

**comp.gen.handler** — An interrupt handler ([14-embedded](14-embedded.md), **emb.irq.name**) is a function that begins with `enter.handler` and returns with `leave.handler` (**target.arch.ops**). The code generator binds it to the vector of its interrupt (**emb.irq.vectors**).

**comp.gen.static** — A package-level variable of an atomic type is a storage cell in the writable data of the image, which the entry code clears (**target.platform.entry**); its address is a constant operand.

**comp.gen.stack** — For a program whose package `main` runs in profile `core`, the code generator computes the stack the program needs from the frames of its functions (**emb.stack.compute**). It rejects a build with recursion without a proven bound (**emb.stack.recursion**) with the cycle of calls, and on a target with a `Stack` size a build that needs more (**emb.stack.limit**) with the deepest chain of calls and the frame size of each. A program in another profile uses its stack without this analysis; on `wasm32`, the template of `enter` traps when the stack pointer falls below the lowest address of the stack (**rt.stack.reserve**).

**comp.gen.structured** — On an architecture with structured control flow (**target.arch.structured**), the code generator places the code of each routine in blocks and loops by the method of N. Ramsey, "Beyond Relooper" (2022). The parts of the routine between its labels are the nodes of its control flow graph; the edges are the labels in the templates of a node and, unless the node ends with `jump`, `leave`, `leave.handler` or `exit`, the fall to the next node. Nodes that the entry does not reach are left out. With the reverse postorder and the dominator tree of the graph, an edge to a node that is not later in the order is a back edge, and its target is a *loop header*; a node that is the target of a label from an earlier node, or of more than one edge from earlier nodes, is a *merge node*. The code of a node `x` is: if `x` is a loop header, `loop`, then the code within, then `end`; the code within is, for the merge nodes that `x` immediately dominates in decreasing order, `block`, the code within for the rest, `end` and the code of that merge node, and at the end the templates of `x`, followed, if it falls to node `s`, by `jump` to `s` if `s` is a merge node or a loop header and otherwise by the code of `s`. A label operand becomes the number of enclosing blocks and loops up to the one of its target, its *label depth*; an irreducible graph, which no Anlang routine has, is a build error.

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
| arithmetic, comparisons, conversions, `min` and `max` of `f32` and `f64` | calls of the floating-point functions of the runtime package (**comp.gen.float**) |

### 22.11.4 The runtime package

**comp.gen.runtime** — The runtime package `an/runtime` is Anlang source in the `core` profile (**rt.lang.anlang**). It provides the functions the code generator calls: the printing of values for `fmt.Print`, `fmt.Println`, `print` and `println` in the formats of 22.10.3, the panic functions, which run the deferred calls and then perform the panic action of the target with the messages of the IR interpreter, and the arithmetic of the operations without rules (**target.arch.select**). Its functions `write`, `read` and `exit` are primitives that the code generator implements with the platform operations of the same names, `read` with a count of 1 into its `inout` byte, which `ReadByte` returns for `console.ReadByte` (**std.console.read**), and `DisableInterrupts` and `RestoreInterrupts` with the operations `irq.save` and `irq.restore`, or as nothing on a target without them. `PanicInterrupt` is the default handler of the vector table (**emb.irq.vectors**). A function of the runtime package is in the image only if the program calls it (**rt.link.piecewise**).

**comp.gen.float** — Floating-point values are computed in software on every target (**target.floats.soft**) until a target with floating-point hardware has rules for them. The runtime package provides, for f64 on the bits as `u64` and for f32 on the bits as `u32`: `AddF64`, `SubF64`, `MulF64`, `DivF64`, `NegF64`, `EqF64`, `LtF64`, `LeF64`, `MinF64`, `MaxF64`, the conversions `F64FromI64`, `F64FromU64`, `F64FromF32`, `F32FromF64`, `F32FromI64`, `F32FromU64`, `I64FromF64`, `U64FromF64` (with the width of the integer, returning whether the truncated value fits, so that a value that does not fit calls `PanicOverflow`, **type.conv.float-int**), and the same with `F32`. The results are those of IEEE 754 with rounding to nearest, ties to even; a NaN result is the quiet NaN without payload. An f32 operation computes in binary64 and rounds once, which gives the binary32 result for sums, differences, products and quotients. `PrintFloat` prints for `fmt.Print` the shortest digits that read back as the value, `PrintFloatF` the verbs `e`, `E`, `f`, `F`, `g`, `G` and `v` of `fmt.Printf` with width, precision and the flags `+`, `-`, space and `0`, with correctly rounded digits, and `PrintF64` prints for `print` as the run-time printing of Go; the digits come from exact arithmetic on natural numbers of up to 1280 bits.

**comp.gen.panic** — Until the debug side tables (roadmap step 41), the action `PrintAndExit` writes `panic: `, the reason and a line break to standard error, without a stack trace (**rt.panic.trace**), and exits with status 2.

### 22.11.5 Linking

**comp.gen.link** — The linker places the entry code (**target.platform.entry**), the functions and the constant data, in this order, at the origin of the binary format, or at the start of the first `Exec` region of a target with memory regions; the writable data follows them, or lies at the start of the first non-`Exec` region (**target.memory.placement**). It assembles each instruction with the generated assembler at its address; since the length of an instruction may depend on the addresses of labels, it repeats the placement until no length changes, and an instruction that assembles at no address of a placement gets its length in the next one. A build whose image exceeds the size limit of its format or its region is rejected with the size of the code of each package, of the constant data and of the static data (**target.format.size**, **target.memory.overflow**). The linker also gives the address of each function, the memory map of the image.

**comp.gen.link-structured** — On an architecture with structured control flow, the linker assembles the code of each routine as one function, in the order of generation after the imports of the platform (**target.platform.imports**); the address of a routine is the index of its function. It places the constant data and then the writable data, which ends with the stack of `main` (**target.memory.stack**), from the origin of the first format. The image holds the functions, the data, the index of the entry code and the size of the memory, which the format writes as a module (**target.format.wasm**).

### 22.11.6 Coverage

**comp.gen.coverage** — The code generator of roadmap step 13 translates every construct of the `core` profile except the following, which it reports as not supported yet with the roadmap step that adds them: `defer` statements in loops, `inout` subslices, `inout` access to hardware mappings at far addresses, the directives of `fmt.Printf` other than `%v`, `%d`, `%s`, `%t`, `%c`, `%x`, `%X`, `%o`, `%b` with flags, width and precision, and for floating-point values other than `%e`, `%E`, `%f`, `%F`, `%g`, `%G` and `%v` without the flag `#` (step 25).

## 22.12 Open issues

1. **General-purpose allocator for `app`.** Deferred to implementation — step 25. Own or ported; any allocator that satisfies [06-memory](06-memory.md) and passes the fuzzing of [23-conformance](23-conformance.md).
