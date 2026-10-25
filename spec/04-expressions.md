# 04. Expressions

This file specifies how Anlang expressions differ from Go expressions: removed operators, addressability, integer overflow, wrapping and other compiler-known types, comparisons, `nil`, calls, `...any` arguments, closures, composite literals and `enum` variant construction.

Where this file is silent, expressions behave as in the Go specification (see [README](README.md)).

**expr.go** — An expression not changed by this file has the syntax, typing rules and meaning of the Go specification. A program that violates a typing rule of the Go specification for an expression MUST be rejected.

## 1. Removed operators

**expr.removed.address** — The unary address operator `&` does not exist. A program MUST NOT contain a unary `&` expression.

**expr.removed.deref** — The unary indirection operator `*` does not exist. A program MUST NOT contain a unary `*` expression.

```
unary_op = "+" | "-" | "!" | "^" | "<-" .    (replaces Go)
```

**Note** — Pointer types do not exist outside the package `an/unsafe` ([02-types](02-types.md), [06-memory](06-memory.md)). Selectors and method calls therefore never dereference implicitly.

## 2. Addressability

Addressability decides which operands can receive an `inout` access ([06-memory](06-memory.md), **mem.inout.access**). It replaces the Go notion, which is defined in terms of pointers.

**expr.addr.def** — An operand is *addressable* if and only if it is one of:

1. a variable declared in a function body, including a parameter or a result;
2. an `inout` parameter or an `inout` receiver;
3. an `inout` loop variable ([05-statements](05-statements.md), **stmt.range.inout**);
4. a field selector `x.f` where `x` is an addressable struct operand;
5. an index expression `a[i]` where `a` is an addressable array operand;
6. an index expression `s[i]` where `s` is an addressable slice operand;
7. a slice expression `s[lo:hi]` or `s[lo:hi:max]` where `s` is an addressable slice or array operand; as an `inout` argument it denotes the subrange of `s` ([06-memory](06-memory.md), **mem.inout.subslice**);
8. a package-level hardware mapping in the `core` profile ([06-memory](06-memory.md), **mem.unsafe.map**).

Package-level variables of synchronization types are not addressable; they are used only through their operations ([06-memory](06-memory.md), **mem.global.kinds**).

**expr.addr.map** — A map index expression `m[k]` is not addressable. A program MUST NOT pass it as an `inout` argument, call an `inout`-receiver method on it, or assign to one of its fields or elements. Assigning to `m[k]` itself is permitted, as in Go.

**Note** — In Go a slice index expression is always addressable because a slice refers to shared memory. In Anlang a slice is a value ([06-memory](06-memory.md), **mem.cow.slice**), so its elements are addressable only through an addressable slice.

## 3. Integer arithmetic

### 3.1 Checked arithmetic

The integer types are listed in [02-types](02-types.md). In this section, *checked integer type* means any integer type that is not a type of the package `an/wrap`.

**expr.int.checked** — For operands of a checked integer type, the operators binary `+`, `-`, `*`, unary `-`, and the statements `++`, `--` and the corresponding assignment operations (`+=`, `-=`, `*=`) produce the mathematical result. If the mathematical result is not representable in the operand type, the operation *overflows*.

**expr.int.div** — For checked integer types, `x / y` and `x % y` behave as in Go, except that `x / -1` and `x % -1` with `x` equal to the minimum value of a signed type overflow. Division by zero panics, as in Go.

**expr.int.overflow** — An overflowing operation MUST NOT produce a value. An implementation MUST either prove at compile time that the operation cannot overflow, or insert a run-time check that panics on overflow. A function containing such a check has the effect `Panic` ([07-effects](07-effects.md)).

**expr.int.overflow-forbidden** — Where the effect `Panic` is forbidden (for example in `interrupt` and `trap` handlers, or by a profile or `anlang.mod` constraint), an operation whose absence of overflow is not proven is a compile-time error. The diagnostic MUST name the operation.

**expr.int.shift** — Shift operators on checked integer types behave as in Go: bits shifted out are discarded and the operation does not overflow.

**expr.int.const** — Constant expressions are evaluated exactly, as in Go. A typed constant whose value is not representable in its type is a compile-time error.

**Note** — The effect of a panic is defined by the target (`OnPanic`, [13-targets](13-targets.md)).

### 3.2 Index and slice bounds

**expr.bounds.check** — Index and slice expressions on arrays, slices and strings require the indices to be in range, as in Go. The rule **expr.int.overflow** applies to them in the same way: the implementation MUST either prove the indices in range or insert a check that panics, and the containing function then has the effect `Panic`.

### 3.3 Explicit wrapping, saturating and checked operations

**expr.bits.functions** — The generic functions of the package `an/bits` accept operands of any checked integer type `T` and return:

| Function | Result |
| --- | --- |
| `bits.WrappingAdd(a, b T) T`, `WrappingSub`, `WrappingMul` | the mathematical result reduced modulo 2ⁿ, where n is the bit size of `T`, interpreted in `T` |
| `bits.SaturatingAdd(a, b T) T`, `SaturatingSub`, `SaturatingMul` | the mathematical result clamped to the range of `T` |
| `bits.CheckedAdd(a, b T) (T, bool)`, `CheckedSub`, `CheckedMul` | the mathematical result and `true` if it is representable; the zero value and `false` otherwise |

These functions never panic and have no effects.

### 3.4 Wrapping types

**expr.wrap.ops** — For operands of a type of the package `an/wrap` (`wrap.U8` … `wrap.U64`, `wrap.I8` … `wrap.I64`), the operators `+`, `-`, `*`, unary `-` and `<<`, and the corresponding assignment operations and `++`, `--`, produce the mathematical result reduced modulo 2ⁿ and interpreted in the operand type. These operations never panic.

**expr.wrap.other-ops** — The operators `&`, `|`, `^`, `&^`, `>>`, `/`, `%` and comparisons on wrapping types behave as on the integer type of the same size and signedness. Division by zero panics.

**expr.wrap.mixing** — Both operands of a binary operator on a wrapping type MUST have the same wrapping type. An untyped constant operand is converted to that type and MUST be representable in the integer type of the same size and signedness. Mixing a wrapping type with any other integer type requires an explicit conversion.

**expr.wrap.convert** — A wrapping type and the integer type of the same size and signedness convert to each other explicitly without a range check; the bit pattern is preserved.

```go
var h wrap.U64 = 0xcbf29ce484222325
h = (h ^ wrap.U64(b)) * 0x100000001b3   // wraps, never panics
return u64(h)
```

## 4. Floating-point arithmetic

**expr.float.ieee** — Operations on `f32` and `f64` follow IEEE 754 binary32 and binary64 with round-to-nearest-even, as in Go.

**expr.float.target** — If the target declares `Floats: none`, a program MUST NOT contain an expression of type `f32` or `f64`. If the target declares `Floats: soft`, the implementation MUST emulate the operations with results identical to **expr.float.ieee** ([13-targets](13-targets.md)).

**expr.float.to-int** — Converting a floating-point value to an integer type truncates toward zero. If the value is NaN or the truncated value is not representable in the target type, the conversion overflows and **expr.int.overflow** applies.

## 5. Compiler-known types with operators

**expr.known.closed** — Operators are defined only for the predeclared types, as in Go, and for the types listed in this section. A program cannot define operators for its own types.

**expr.known.simd** — Vector types of the package `an/simd` (`simd.F32x4`, `simd.U8x16` and others) support the arithmetic operators of their element type, applied element-wise to operands of the same vector type. The implementation maps them to vector instructions of the target or to scalar code; the result MUST be the same.

**expr.known.fixed** — Fixed-point types of the package `an/fixed` (`fixed.Q16`, `fixed.Q8` and others) support `+`, `-`, `*`, `/`, unary `-` and comparisons. `+`, `-` and unary `-` follow **expr.int.checked** on the underlying representation; `*` and `/` produce the exact result truncated toward zero to the type's precision and overflow if it is out of range.

**expr.known.wrap** — The wrapping types are specified in §3.4.

## 6. Comparisons

**expr.cmp.go** — Comparison operators and comparability follow Go, with the additions in this section.

**expr.cmp.enum** — A value of an `enum` type is comparable if the payload types of all its variants are comparable. Two values are equal if they hold the same variant and the payloads are equal field by field.

**expr.cmp.secret** — Values of type `Secret[T]` are not comparable; see **safe.secret.no-eq** in [11-data-safety](11-data-safety.md).

**expr.cmp.sync** — Values of synchronization types ([02-types](02-types.md)) are not comparable, except that a channel may be compared with `nil`.

**expr.cmp.nil** — A value may be compared with `nil` if its type is an interface, slice, map, channel or function type.

**expr.cmp.nil-empty** — For a slice or a map, `x == nil` is `true` if and only if `len(x) == 0`.

**Note** — In Anlang `nil` for a slice or a map is just the empty value ([02-types](02-types.md)), so Go's distinction between a nil slice and an empty slice does not exist.

## 7. The identifier `nil`

**expr.nil.context** — The predeclared identifier `nil` MAY appear only where its type can be determined from context as an interface, slice, map, channel or function type. It denotes the zero value of that type.

**expr.nil.call** — Calling a method on an interface operand, or calling an operand of function type, requires the operand to be proven non-nil; see **mem.nil.call** in [06-memory](06-memory.md).

## 8. Selectors and method values

**expr.sel.no-deref** — A selector `x.f` selects a field or method of `x` itself. There is no implicit dereference.

**expr.method.inout-call** — A method whose receiver is declared `inout` MAY be called only on an addressable operand ([§2](#2-addressability)). The call `x.M(args)` is equivalent to `T.M(x, args)` with `x` passed as the `inout` receiver.

**expr.method.inout-value** — A method value `x.M` or method expression `T.M` whose receiver is declared `inout` MAY be used only as the callee of an immediate call. A program MUST NOT store, pass, return or convert such a method value.

**expr.method.interface-inout** — A method with an `inout` receiver belongs to the method set of its receiver type `T`. Calling it through an interface value modifies the copy held by the interface value. Whether the dynamic method has an `inout` receiver is not known statically, so the interface operand need not be addressable; a call on a temporary interface value modifies a copy that is then discarded.

## 9. Calls

### 9.1 Arguments

**expr.call.value-arg** — An argument for a value parameter is a logical copy of the argument value ([06-memory](06-memory.md), **mem.value.copy**).

**expr.call.inout-arg** — An argument for an `inout` parameter MUST be an addressable operand. No marker is written at the call site. The `inout` access to the argument begins when the call begins and ends when the call returns ([06-memory](06-memory.md), **mem.inout.access**).

**expr.call.inout-overlap** — Two `inout` arguments of the same call MUST NOT overlap ([06-memory](06-memory.md), **mem.inout.overlap**). An operand passed both as an `inout` argument and as a value argument of the same call is permitted: the value argument is copied before the call begins.

**expr.call.order** — Arguments are evaluated in the order specified by Go. The operand path of an `inout` argument (for example the index in `xs[i]`) is evaluated in that order as well.

### 9.2 Variadic `...any`

**expr.call.any-variadic** — A final parameter of the form `args ...any` is a *compile-time tuple*. At each call site, the tuple consists of the remaining arguments with their static types. A function with such a parameter is instantiated separately for each distinct sequence of argument types, as for type parameters. The mechanics of instantiation and of iterating the tuple are specified in [10-compile-time](10-compile-time.md).

**expr.call.any-forward** — Inside a function with a parameter `args ...any`, the parameter MAY be used only:

1. as the range expression of a `for const` statement ([10-compile-time](10-compile-time.md));
2. as the operand of `len`, which is then a constant;
3. as the final argument `args...` of a call to another function whose final parameter is `...any`; the tuple is forwarded unchanged.

**expr.call.any-spread** — A slice MUST NOT be passed as `xs...` to a parameter `...any`.

**expr.call.variadic** — A final parameter `...T` where `T` is not `any` behaves as in Go.

### 9.3 Results

**expr.call.error-result** — A call whose result includes an `error` value is subject to **stmt.error.unused** ([05-statements](05-statements.md)).

**expr.call.type-assert** — The single-value form of a type assertion `x.(T)` panics when it fails, as in Go, and gives the containing function the effect `Panic`. The comma-ok form `v, ok := x.(T)` never panics.

## 10. Closures

**expr.closure.capture-copy** — A function literal captures each variable of an enclosing function that it uses by value, at the moment the function literal is evaluated. Later assignments to the variable in the enclosing function are not visible to the closure.

**expr.closure.captured-readonly** — A function literal MUST NOT assign to a captured variable, apply `++` or `--` to it, take an `inout` access to it, or call an `inout`-receiver method on it.

**expr.closure.loop** — Each iteration of a `for` loop declares new loop variables, as in Go 1.22 and later; a closure created in an iteration captures that iteration's values.

**expr.closure.sync** — A captured value of a synchronization type is a handle to the same object ([06-memory](06-memory.md), **mem.sync.handle**). Operations on it through the closure affect the shared object.

**expr.closure.capture-inout** — A function literal that uses an `inout` parameter, an `inout` receiver or an `inout` loop variable of an enclosing function captures it by reference instead of by copy and MAY modify it. Such a function value is *second-class* ([06-memory](06-memory.md), **mem.ref.second-class**): it MAY only be called directly in the function where it is created, and a program MUST NOT store, pass, return, send or convert it.

```go
func Count(n inout int, xs []i32) {
    add := func(v i32) { n += int(v) }   // captures n by reference
    for _, x := range xs {
        add(x)                           // permitted: direct call
    }
    // go add(1)                         // error: second-class closure
}
```

## 11. Composite literals and `enum` variants

**expr.lit.go** — Composite literals behave as in Go, except as specified below.

**expr.lit.no-zero** — If a struct literal omits a field whose type has no zero value ([02-types](02-types.md)), the literal is a compile-time error.

**expr.lit.type-invariant** — A struct literal of a type with a type invariant is a boundary at which the invariant is checked ([08-contracts](08-contracts.md)).

**expr.variant.scope** — The names of the variants of an `enum` type are declared in the block where the type is declared. A variant of an `enum` declared in another package is referred to by a qualified identifier (`shapes.Circle`).

**expr.variant.construct** — A variant value is written as:

1. the variant name, for a variant without payload: `Inc`, `None`;
2. a composite literal with the variant name as its type, for a variant with payload: `Circle{r: 1.0}` or `Circle{1.0}`. Keyed and positional forms follow the rules of struct literals;
3. a call-like conversion `V(x)`, for a variant whose payload has exactly one field: `Some(root)` is equivalent to `Some{root}`.

The type of a variant value is the `enum` type, not a separate type.

```
VariantLit = VariantName | VariantName LiteralValue | VariantName "(" Expression [ "," ] ")" .   (new)
VariantName = identifier | QualifiedIdent .                                                       (new)
```

**Note** — Syntactically, each form of `VariantLit` is also an `Operand`, `CompositeLit` or `Conversion` of the Go grammar. The parser accepts it as such; it is interpreted as a variant literal during type checking, when the name resolves to a variant.

**expr.variant.infer** — For a generic `enum` type, the type arguments of a variant value are inferred from the payload, or, if the variant has no payload, from the type required by the context. If they cannot be inferred, the expression is a compile-time error.

```go
var p Option[arena.Ref[Node]] = None       // inferred from the context
child := Node{parent: Some(root)}           // inferred from root
```
