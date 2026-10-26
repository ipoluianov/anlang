# 10. Compile-Time Evaluation

Area: `ct`.

Anlang has no macros. Code that Go produces with `go generate`, reflection or text templates is written in Anlang itself and run by the compiler:

- **compile-time evaluation** of ordinary functions to produce constants;
- **compile-time reflection** through package `an/meta`;
- **`for const`** loops that are unrolled by the compiler;
- **`...any` parameters**, which are compile-time tuples rather than slices of interfaces;
- **generators** — Anlang programs that the build runs with access only to declared files.

No construct in this file adds a keyword. `for const` is a combination of existing keywords; its syntax belongs to [05-statements](05-statements.md).

## 10.1 Compile-time evaluable functions

**ct.eval.evaluable** — A function call is *compile-time evaluable* when, after effect substitution for the call site (see [07-effects](07-effects.md)), the effect set of the call contains no effects other than:

- `Alloc` and `StaticAlloc` — allocation is meaningless at compile time and is performed in compiler memory;
- `Panic` — a panic during evaluation is a compile error (**ct.eval.panic**);
- `ReadInput`, restricted as in **ct.eval.read-input**.

**ct.eval.no-env** — A compile-time evaluated call MUST NOT have any other effect. In particular `Clock`, `Random`, `Global`, `Tasks`, `Block`, `Foreign`, any I/O effect and any hardware effect make a call non-evaluable.

**ct.eval.read-input** — During compile-time evaluation, `ReadInput` permits reading only files located inside the directory of the package that contains the constant declaration being evaluated, or its subdirectories. The path MUST be a compile-time constant. The content of every file read is part of the build cache key (see [22-compiler](22-compiler.md)). A file is read with `gen.ReadFile(name)` of package `an/gen`, which has effect `ReadInput` and returns the bytes of the file as `[]u8`; a name that is not a slash-separated path within the directory, or a file that cannot be read, makes the evaluation panic.

**Note** — This is what makes `svd.Load("STM32F103.svd")` in a target description possible (see [13-targets](13-targets.md)).

**ct.eval.panic** — If compile-time evaluation panics, the program is rejected. The diagnostic MUST include the panic message and the evaluation call stack.

## 10.2 Constants computed by functions

Go restricts constants to boolean, numeric and string values given by constant expressions. Anlang extends constant declarations without changing their syntax.

**ct.const.call** — The expression of a constant declaration MAY contain calls to compile-time evaluable functions, composite literals and other computed constants; such a constant is a *computed constant*. The constant's value is the result of evaluating the expression at compile time. The expression MUST NOT use variables.

**ct.const.types** — A constant MAY have any type whose values consist only of:

- boolean, numeric and string types, `cstring`, `dstring`;
- arrays, slices, structs, maps, `enum` and `Option` values of such types;
- interface values whose dynamic value is of such a type.

A constant MUST NOT have, or contain, a value of a synchronization type, a function type, `ffi.Handle`, `Secret[T]` or `Untrusted[T]`.

**ct.const.typed** — A constant computed by a function call is a *typed constant* of the result type. The untyped constant rules of the Go specification apply only to constant expressions without calls, exactly as in Go.

**ct.const.immutable** — A constant computed by a function is immutable. It MUST NOT be the operand of `inout` access, assignment to an element or field, or any other modification. Reading it yields a copy, as for any value.

**ct.const.placement** — An implementation MUST place a constant computed by a function in read-only storage of the target. On targets with an executable non-writable memory region (flash), it SHOULD be placed there.

**ct.const.order** — Constants MAY depend on other constants in any order that is free of cycles. A cycle through constant initializers is a compile error.

```go
const crcTable = makeCRCTable(0xEDB88320)     // [256]u32 in flash

func makeCRCTable(poly u32) [256]u32 {
    var t [256]u32
    for i := range 256 {
        c := u32(i)
        for range 8 {
            if c&1 == 1 { c = poly ^ (c >> 1) } else { c >>= 1 }
        }
        t[i] = c
    }
    return t
}
```

## 10.3 Evaluation limits

**ct.limit.bounded** — An implementation MUST bound each compile-time evaluation by a number of evaluation steps and by memory. Limits MUST NOT depend on wall-clock time.

**ct.limit.deterministic** — For a given compiler version and language edition, the limits and the step count of every evaluation MUST be identical on all host machines. Whether a program compiles MUST NOT depend on the host.

**ct.limit.values** — In language edition 1.0, one evaluation is bounded by 10,000,000 executed instructions of the intermediate representation ([22-compiler](22-compiler.md), 22.10) and by 16,777,216 values created in slices, arrays, structs and maps. The limits are fixed for the edition; a project cannot change them.

**ct.limit.exceeded** — If a limit is exceeded, the program is rejected. The diagnostic MUST name the constant being evaluated, the limit, and the call stack at the point the limit was reached.

**ct.eval.semantics** — Compile-time evaluation MUST produce the same value as executing the same code on the target, including integer overflow checks, the integer widths of the target (`int`, `uint`, `usize`), and floating-point behavior per IEEE 754.

**Note** — Because `int` differs between targets, a constant computed with `int` arithmetic may differ between targets. It is evaluated separately for each target of a build.

## 10.4 Compile-time reflection: package `an/meta`

**ct.meta.compile-time-only** — Every function of package `an/meta` that takes a type or produces a type descriptor is evaluated at compile time. Its results are compile-time values. There is no run-time type information (see [19-runtime](19-runtime.md)).

The minimal API is:

| Function or method | Result |
| --- | --- |
| `meta.Name(T)` | `string` — the type name as written in its package; empty for unnamed types |
| `meta.Fields(T)` | compile-time sequence of `meta.Field`, one per field of struct type `T`, in declaration order |
| `meta.Variants(E)` | compile-time sequence of `meta.Variant`, one per variant of `enum` type `E`, in declaration order |
| `f.Name` | `string` — field name |
| `f.Index` | `int` — position of the field |
| `f.Exported` | `bool` |
| `f.Tag` | `string` — the field's tag, as in Go |
| `f.Type` | type descriptor usable wherever a type argument is expected |
| `f.Get(v)` | value of the field `f` of struct value `v`, with static type `f.Type` |
| `f.Set(v, x)` | assigns `x` to field `f` of `v`; `v` is an `inout` parameter |
| `x.Name`, `x.Index` | name and position of variant `x` |
| `x.Fields` | compile-time sequence of `meta.Field` for the data carried by variant `x` |
| `x.Is(v)` | `bool` — whether `enum` value `v` holds variant `x`; evaluated at run time |
| `x.Make(...)` | constructs variant `x` from values of its fields, in order |

**ct.meta.arg-type** — The first argument of `meta.Name`, `meta.Fields` and `meta.Variants` is a type, written as a type argument would be. `meta.Fields(T)` is a compile error unless `T` is a struct type; `meta.Variants(E)` is a compile error unless `E` is an `enum` type.

**ct.meta.field-access** — `f.Get(v)` and `f.Set(v, x)` are compile errors if `f` is a field descriptor of a type other than the static type of `v`.

**ct.meta.variant-get** — Inside code guarded by `x.Is(v)`, the fields of the variant are read with `f.Get(v)` for `f` in `x.Fields`. Reading a field of a variant that `v` does not hold panics.

**ct.meta.visibility** — `meta.Fields(T)` MUST include unexported fields only when evaluated inside the package that declares `T`. Outside it, only exported fields are listed. `meta` never gives more access than ordinary code.

**ct.meta.opaque** — `meta.Fields` and `meta.Variants` MUST treat `Secret[T]` and `Untrusted[T]` as having no fields. Generic code built on `meta` cannot read their contents (see [11-data-safety](11-data-safety.md)).

```go
func Encode[T any](w inout json.Writer, v T) {
    w.BeginObject()
    for const f := range meta.Fields(T) {
        w.Key(f.Name)
        Encode(w, f.Get(v))
    }
    w.EndObject()
}
```

## 10.5 `for const` loops

The syntax `for const` *IdentifierList* `:=` `range` *Expression* *Block* is defined in [05-statements](05-statements.md). This section defines its meaning.

**ct.for.sequence** — The range expression of a `for const` loop MUST be one of:

1. a compile-time sequence produced by `an/meta` (`meta.Fields(T)`, `meta.Variants(E)`, `x.Fields`);
2. a parameter declared `...any` (see 10.6);
3. an integer constant `n`, giving the values `0` … `n-1`;
4. a constant of array or slice type whose initializer is a composite literal of constant expressions, giving its elements; for const is unrolled when the program is type-checked, before computed constants are evaluated.

**ct.for.unroll** — The loop is unrolled at compile time: the body is instantiated once per element of the sequence, in order. In each instance the iteration variable is bound to that element.

**ct.for.typecheck** — Each instance of the body is type-checked separately, with the iteration variable having the static type of its element. An error in any instance is a compile error that names the element.

**ct.for.binding** — For cases 1, 3 and 4 the iteration variable is a constant. For case 2 it is an immutable value with the static type of the corresponding argument.

**ct.for.control** — `continue` ends the current instance. `break` skips all remaining instances. `return`, `goto` and labelled statements behave as in ordinary code. A `defer` in an instance runs when the enclosing function returns, as in Go.

**ct.for.nesting** — `for const` loops MAY be nested. Ordinary loops MAY appear inside `for const` and vice versa.

## 10.6 `...any` parameters

The syntax of a variadic parameter is unchanged from Go. The meaning of `...any` changes.

**ct.any.tuple** — A final parameter declared `name ...any` is a *compile-time tuple*. At each call site, its arity and the static type of each argument are fixed by the call.

**ct.any.instance** — A function with a `...any` parameter is instantiated once per distinct sequence of argument types used at its call sites, in the same way as a generic function is instantiated per type argument list.

**ct.instance.check** — The body of a function with a `...any` parameter, and of a generic function whose body uses `an/meta`, `for const` or a type switch on a value of a type parameter, is type-checked once for each instance, with the type parameters denoting the type arguments and the tuple denoting the argument types; the diagnostics of an instance name it and the call that produced it. An instance that no call produces is not checked. In such an instance, and in an instance of the body of `for const`, an `if` statement whose condition is a constant is checked and compiled with the branch that the condition takes only.

**ct.any.use** — Inside the function, the tuple parameter MAY be used only:

1. as the range expression of `for const`;
2. as the final argument of a call to another function with a `...any` parameter, written `name...`;
3. as the operand of `len(name)`, which is a constant.

Any other use — indexing, assignment, storing, conversion to a slice — is a compile error.

**ct.any.no-slice** — A `[]T` value MUST NOT be passed to a `...any` parameter with `s...`. Each argument is a separate element of the tuple.

**ct.any.effects** — Effects of a call are computed for its instance, so each call site gets exactly the effects of the argument types it uses (see [07-effects](07-effects.md)).

### 10.6.1 Static type switch

A `...any` element or a value whose type is a type parameter has a static type that is known in each instance. To branch on it, Anlang permits a type switch on such values.

**ct.typeswitch.static** — The operand of a type switch MAY be a value whose type is a type parameter or the iteration variable of `for const` over a `...any` tuple. Such a type switch is resolved at compile time in each instance: only the matching case is compiled, and only that case is type-checked with the case type. The other cases are not type-checked for that instance.

**ct.typeswitch.binding** — In a static type switch, the variable bound by the switch guard has the concrete type of the operand in that instance, also in cases that list several types and in the `default` case.

**ct.typeswitch.default** — If no case matches and there is no `default` case, the instance is a compile error.

```go
func writeValue[T any](b inout strings.Builder, v T) {
    switch x := v.(type) {
    case string:
        b.WriteString(x)
    case int, i64:
        b.WriteString(strconv.Itoa(int(x)))
    default:
        writeStruct(b, x)
    }
}
```

### 10.6.2 Compile-time rejection

**ct.reject.call** — A call `meta.Reject(msg)`, where `msg` is a string constant, is a compile error if it is reachable in an instance after compile-time resolution of static type switches, `for const` unrolling and constant conditions. The error is reported at the call site that produced the instance, with message `msg`.

```go
func (db DB) Query(q string, args ...any) (Rows, error) {
    for const a := range args {
        switch a.(type) {
        case Secret[string], Secret[[]u8]:
            meta.Reject("sql: pass secret.Expose(x) explicitly")
        }
    }
    ...
}
```

**ct.reject.precondition** — A `requires` clause of a function with a `...any` parameter MAY call compile-time evaluable functions on constant arguments and on `meta` descriptors of the tuple. When the arguments it depends on are constants at the call site, the clause is evaluated at compile time; if it is false, the call is a compile error. When they are not constants, the clause is an ordinary proof obligation (see [08-contracts](08-contracts.md)).

**Note** — This is how `fmt` rejects `%d` for a string argument: `Sprintf` has `requires fmt.Valid(format, meta.Types(args))`. A non-constant format string cannot be checked at compile time and therefore fails to prove the precondition, which matches Go's `vet` advice against non-constant formats.

### 10.6.3 Code size *(informative)*

Each distinct argument type sequence creates an instance. To keep code small, the body of a `...any` function should be thin: the `for const` loop should only dispatch each argument to a shared non-generic core function. The standard library functions `fmt.Sprint`, `log.Info` and `sql.Query` are written this way.

## 10.7 Compile-time string encoding

Encoding of constant text is ordinary compile-time evaluation; string literals have no prefixes.

**ct.encoding.const** — When the argument of an `encoding` package function such as `encoding.CP866` is a constant, the call is compile-time evaluable and its result is a `[]u8` constant.

**ct.encoding.unmappable** — If the constant text contains a character that the target encoding cannot represent, the evaluation panics and the program is rejected (**ct.eval.panic**).

**ct.encoding.dstring** — A constant of type `[]u8` that does not contain the byte `'$'` is assignable to `dstring`. The assignment produces the bytes followed by `'$'`. A constant containing `'$'` is not assignable to `dstring`. The `dstring` type itself is defined in [02-types](02-types.md).

```go
dos.Print(encoding.CP866("Hello from Anlang!\r\n"))    // CP866 bytes, '$' appended
```

## 10.8 Generators

A generator turns an external description — an SVD file, a system call table — into Anlang source code during the build. Bindings to external libraries do not use generators; they are declared by hand (see [12-ffi](12-ffi.md)).

**ct.gen.program** — A generator is a `main` package of the project in profile `app`. The build runs it on the host.

**ct.gen.declared** — Every generator MUST be declared together with its input files and its output package by a `generate` directive in `anlang.mod` (see [20-toolchain](20-toolchain.md), **tool.mod.generate**).

**ct.gen.effects** — A generator's effect set MUST NOT contain I/O effects other than `ReadInput` and `WriteOutput`, and MUST NOT contain `Clock`, `Random` or `Foreign`. `ReadInput` permits reading only the declared input files; `WriteOutput` permits writing only the files of the declared output package. Violations are compile errors of the generator, and an implementation MUST also enforce them when the generator runs.

**ct.gen.deterministic** — A generator MUST produce byte-identical output for identical inputs. An implementation MUST rerun a generator only when the hash of its source, its inputs or the compiler version changes, and MUST reuse cached output otherwise.

**ct.gen.output** — Generator output MUST be Anlang source in canonical formatting (see [01-lexical](01-lexical.md)). Each generated file MUST begin with a comment that names the generator and the hash of its inputs. Generated files are ordinary source code: they are type-checked, effect-checked and readable like any other file.

**ct.gen.cache** — Generator output is not part of the project and is not committed. The build produces it into the build cache, keyed by the hash of the generator's source, its inputs and the compiler version (**ct.gen.deterministic**).

**ct.gen.package** — The output package is importable by its declared import path like a project package. `anlang gen -show <package>` prints its source ([20-toolchain](20-toolchain.md), **tool.cmd.gen**).

**ct.gen.conflict** — A directory in the project with the path of a declared output package is a compile error (**tool.mod.generate.unique**).
