# 2. Types

This file specifies the Anlang type system as a delta over the Go specification, sections "Types", "Properties of types and values", "The zero value" and "Conversions". The meaning of values at run time (copying, sharing, `inout`) is specified in [06-memory](06-memory.md).

## 2.1 Overview

**type.go** — Every type of the Go specification is an Anlang type with the same meaning, except as changed by this file.

The changes are:

- new numeric, string and byte-order types (§2.2, §2.3, §2.13);
- clauses in struct types (§2.4);
- `enum` types (§2.5) and `Option[T]` (§2.6);
- removal of pointer types (§2.7);
- restrictions on `any` (§2.9);
- handle types (§2.10);
- a restricted `nil` (§2.11);
- types without a zero value (§2.12);
- proof-based narrowing conversions (§2.14);
- compiler-known library types with operators (§2.15).

## 2.2 Numeric types

**type.int.fixed** — The predeclared fixed-size integer types are:

| Type | Values |
| --- | --- |
| `i8`, `i16`, `i32`, `i64` | two's-complement signed integers of 8, 16, 32, 64 bits |
| `u8`, `u16`, `u32`, `u64` | unsigned integers of 8, 16, 32, 64 bits |

**type.int.word** — The predeclared types `int` and `uint` are signed and unsigned integers whose size is the machine word of the target (field `Word` of the target description, [13-targets](13-targets.md)). `int` is 16 bits on `i8086`, 32 bits on `armv7m` and `wasm32`, 64 bits on `x86_64` and `arm64`.

**type.int.usize** — The predeclared type `usize` is an unsigned integer whose size equals the size of the target's address space. Lengths, capacities and indices of arrays, slices and strings have type `int` as in Go; `usize` is the type of sizes and offsets of memory.

**type.int.distinct** — All integer types are distinct. No two of them are aliases, including `int` and the fixed-size type of the same size on a given target.

**type.float** — The predeclared floating-point types are `f32` and `f64`, IEEE 754 binary32 and binary64.

**type.float.target** — A target description declares floating-point support as `hard`, `soft` or `none` ([13-targets](13-targets.md)). With `soft`, operations on `f32` and `f64` are implemented in software with identical results. With `none`, a program that uses `f32` or `f64` in code reachable for that target MUST be rejected.

**type.numeric.removed** — The Go predeclared types `int8`, `int16`, `int32`, `int64`, `uint8`, `uint16`, `uint32`, `uint64`, `uintptr`, `float32`, `float64`, `complex64`, `complex128`, `byte` and `rune` do not exist in Anlang.

**Note** — `anlang port` rewrites them: `byte` → `u8`, `rune` → `i32`, `float64` → `f64`, and so on.

**type.numeric.methods** — Predeclared types have no methods. Operations beyond the built-in operators are provided by generic functions of the standard library (`bits.WrappingAdd`, `bits.CheckedAdd`, `conv.Narrow`).

## 2.3 String types

**type.string** — `string` is an immutable sequence of bytes that holds UTF-8 text, as in Go. Ranging over a `string` yields code points of type `i32`.

**type.string.cstring** — `cstring` is an immutable sequence of bytes terminated by a zero byte, for external functions ([12-ffi](12-ffi.md)). The bytes before the terminator MUST NOT contain a zero byte.

**type.string.dstring** — `dstring` is an immutable sequence of bytes terminated by `$` (0x24), for DOS functions. The bytes before the terminator MUST NOT contain `$`.

**type.string.terminator-const** — A constant of type `string` or a constant of type `[]u8` (the result of compile-time evaluation, [10-compile-time](10-compile-time.md)) is assignable to `cstring` and to `dstring`. The terminator is appended at compile time. If the constant contains the terminator byte, the program MUST be rejected.

```go
dos.Print(encoding.CP866("Hello from Anlang!\r\n"))   // constant []u8; "$" appended at compile time
```

**type.string.terminator-conv** — A non-constant `string` or `[]u8` value is converted to `cstring` or `dstring` only explicitly, with `cstring(x)` or `dstring(x)`. The conversion copies the bytes and appends the terminator. If the value contains the terminator byte, the conversion panics; the conversion therefore has the effects `Alloc` and `Panic` unless the absence of the terminator is proven ([07-effects](07-effects.md)).

## 2.4 Struct types

**type.struct.go** — Struct types, embedded fields and field tags are as in Go.

A struct field list MAY begin with clauses (*(replaces Go)*):

```
StructType     = "struct" "{" { StructClause ";" } { FieldDecl ";" } "}" .
StructClause   = InvariantClause | LayoutClause .
LayoutClause   = "layout" LayoutKind .
LayoutKind     = "packed" | "c" .
```

`InvariantClause` is defined in [08-contracts](08-contracts.md). Recognition of `invariant` and `layout` as clause words follows **lex.context.struct-region** ([01-lexical](01-lexical.md)). `packed` and `c` in `LayoutKind` are identifiers recognized only in this position.

**type.struct.invariant** — An `InvariantClause` declares a type invariant. The expression MUST have type `bool` and MAY refer to the fields of the struct by name. When and how the invariant is checked is specified in [08-contracts](08-contracts.md).

**type.struct.layout-once** — A struct type MUST contain at most one `LayoutClause`.

```go
type Range struct {
    invariant lo <= hi
    lo, hi int
}

type Header struct {
    layout packed
    magic   u32be
    version u16le
}
```

## 2.5 Enum types

An enum type is a sum type: a value of the type is exactly one of its variants, and a variant may carry data.

```
TypeLit      = … | EnumType .                                   (replaces Go)
EnumType     = "enum" "{" { VariantDecl ";" } "}" .               (new)
VariantDecl  = identifier [ "{" [ VariantField { "," VariantField } [ "," ] ] "}" ] .
VariantField = IdentifierList Type .
```

**type.enum.fields** — The fields of a variant are written like a parameter list: groups of names with a type, separated by commas, as in `Rect{w, h f64}` and `TooLong{word string, max int}`. Every field has a name; `inout` and `...` are not allowed.

```go
type Shape enum {
    Circle{r f64}
    Rect{w, h f64}
}

type Lifecycle enum {
    Launching
    Active
    Inactive
    Background{deadline Duration}
    Terminating
}
```

**type.enum.nonempty** — An enum type MUST declare at least one variant. Variant names within one enum type MUST be distinct, and field names within one variant MUST be distinct.

**type.enum.defined** — An enum type literal MAY appear only as the underlying type in a type declaration. Every enum type is therefore a defined type.

**type.enum.generic** — An enum type MAY have type parameters, as a generic type declaration in Go.

```go
type Option[T any] enum {
    None
    Some{value T}
}
```

**type.enum.variant-scope** — Each variant name is declared in the scope that contains the enum type declaration: for a package-level enum, the package block. Two enum types declared in the same block MUST NOT have variants with the same name, and a variant name MUST NOT conflict with another declaration in that block. Outside the declaring package, a variant is referred to with a qualified identifier, like any other exported name: `lifecycle.Active`.

**type.enum.variant-export** — A variant is exported if its name is exported. A variant of an exported enum type with an unexported name can be matched only inside the declaring package; outside it, a `switch` over such a type cannot be exhaustive and MUST therefore contain a `default` case ([05-statements](05-statements.md)).

**type.enum.value** — A variant without data denotes a value of the enum type.

**type.enum.constructor** — A variant with data is a constructor: a function whose parameters are the variant's fields in declaration order and whose result is the enum type. It may be called (`Some(root)`, `Rect(2, 3)`) or used as a function value (`camera.ScanBarcode(Scanned)`). For a generic enum, the type arguments are inferred as for a generic function call, or from the assignment context.

**type.enum.patterns** — Values of an enum type are inspected with an exhaustive `switch` and patterns ([05-statements](05-statements.md)). Fields of a variant are not accessible by selector.

**type.enum.comparable** — An enum type is comparable if the types of all fields of all its variants are comparable. Two values are equal if they are the same variant and their corresponding fields are equal.

**type.enum.zero** — The zero value of an enum type is its first variant if that variant carries no data. If the first variant carries data, the enum type has no zero value (§2.12).

**Note** — The representation of enum values is specified in [22-compiler](22-compiler.md). `Option[T]` needs no tag when `T` has an invalid bit pattern.

## 2.6 Option and the comma-ok form

**type.option.decl** — `Option` is a predeclared generic enum type, declared as if by

```go
type Option[T any] enum {
    None
    Some{value T}
}

func (o Option[T]) Get() (T, bool)
```

`None` and `Some` are predeclared in the universe block (§2.17).

**type.option.get** — `o.Get()` returns the value of `Some` and `true`, or the zero value of `T` and `false` for `None`. `Get` uses the zero value of `T`, so `T` requires a zero value (**type.zero.param-constraint**): for an `Option[T]` whose `T` has no zero value, `Get` is rejected and `o` MUST be inspected with `switch`.

**type.option.none-assign** — `None` is assignable to every type `Option[T]`. Its type argument is taken from the assignment context; if there is none, the program MUST be rejected.

**type.absence.comma-ok** — A possibly absent result of a function SHOULD be expressed as the comma-ok form `(T, bool)`, as for map lookups in Go; `Option[T]` SHOULD be used for fields and elements of collections.

**Note** — This is a convention for library design; the standard library follows it.

```go
user, ok := Find(users, "alice")
if !ok {
    return ErrNotFound
}

if p, ok := n.parent.Get(); ok {
    visit(p)
}
```

## 2.7 Pointer types and other composite types

**type.pointer.removed** — There are no pointer types. The type syntax `*T` is invalid, and the built-in function `new` does not exist.

**Note** — Mutation of a caller's value uses `inout` ([03-declarations](03-declarations.md) §3.8, [06-memory](06-memory.md)); buffers are slices; foreign objects are `ffi.Handle`. Raw pointers exist only inside the package `an/unsafe` ([06-memory](06-memory.md)).

**type.composite.go** — Array, slice, map, channel and function types are as in Go, with these changes:

- **type.func.inout** — A parameter of a function type MAY be marked `inout` (`func(s inout Stats)`), with the syntax of [03-declarations](03-declarations.md) §3.8.
- **type.map.value** — Map elements are not addressable, as in Go.
- **type.chan.handle** — Channel types are handle types (§2.10).

## 2.8 Interface types and error

**type.interface.go** — Interface types, method sets, type sets, embedding, implementation and type assertions are as in Go.

**type.interface.copy** — An interface value holds a copy of the dynamic value assigned to it. Changing the original afterwards does not change the copy, and vice versa.

**type.interface.inout-method** — A method with an `inout` receiver belongs to the method set of its receiver type. Calling it through an interface value modifies the copy held by the interface value; the interface operand MUST then be addressable ([04-expressions](04-expressions.md)).

**type.interface.nil** — The zero value of an interface type is `nil`. Calling a method on an interface value is allowed only where the value is proven non-`nil` (**mem.nil.call**, [06-memory](06-memory.md)).

**type.error** — `error` is the predeclared interface type of Go:

```go
type error interface {
    Error() string
}
```

## 2.9 The type any

**type.any.positions** — `any` (and an equivalent `interface{}` with an empty method set) MAY be used only

1. as a type constraint (`[T any]`), and
2. as the element type of a final variadic parameter `...any`, whose arguments form a tuple known at compile time ([10-compile-time](10-compile-time.md)).

A variable, field, result, element or non-variadic parameter of type `any` MUST be rejected.

**type.interface.empty-value** — The same restriction applies to every interface type with an empty method set.

## 2.10 Handle types

**type.handle.set** — The handle types are:

- channel types `chan T`, `chan<- T`, `<-chan T`;
- `sync.Mutex[T]` and `sync.RWMutex[T]` of package `an/sync`;
- the types of package `an/atomic` (`atomic.U32` and the like).

**type.handle.copy** — A copy of a value of a handle type refers to the same object as the original. All other types have value semantics. The rules for sharing, placement and access are in [06-memory](06-memory.md) and [09-concurrency](09-concurrency.md).

**type.handle.zero** — The zero value of `sync.Mutex[T]`, `sync.RWMutex[T]` and the `atomic` types is a ready object whose content is the zero value of its content type. The zero value of a channel type is `nil`.

**Note** — `ffi.Handle` is not a handle type in this sense: it is an opaque value that names a foreign object ([12-ffi](12-ffi.md)).

## 2.11 nil

**type.nil.types** — `nil` is a predeclared identifier that denotes the zero value of interface, slice, map and channel types. It is assignable to those types and comparable with their values. It is not a value of any other type.

**type.nil.empty** — A `nil` slice and a `nil` map are empty values: `len` returns 0, `append` to a `nil` slice and assignment to an element of a `nil` map work. A `nil` channel blocks forever on send and receive, as in Go.

**type.nil.no-deref** — No operation dereferences `nil`. The only operation that could do so in Go — a method call on a `nil` interface value — is rejected at compile time unless the value is proven non-`nil` (**mem.nil.call**).

## 2.12 Zero values

**type.zero.go** — Every type has a zero value as in Go, except the types listed in **type.zero.none**. The zero values of new types are:

| Type | Zero value |
| --- | --- |
| `i8`…`u64`, `int`, `uint`, `usize`, byte-order types | `0` |
| `f32`, `f64` | `0.0` |
| `cstring`, `dstring` | empty string (terminator only) |
| enum type | first variant, if it carries no data |
| `Option[T]` | `None` |
| handle types | §2.10 |
| interface, slice, map, channel | `nil` |
| `Secret[T]`, `Untrusted[T]` | wrapper around the zero value of `T` ([11-data-safety](11-data-safety.md)) |
| `wrap`, `fixed`, `simd` types | all bits zero |

**type.zero.none** — The following types have no zero value:

1. an enum type whose first variant carries data;
2. a struct type with a type invariant that does not hold for the struct's zero value, or that the compiler cannot prove to hold for it ([08-contracts](08-contracts.md));
3. an array type whose element type has no zero value;
4. a struct type with a field whose type has no zero value;
5. `Secret[T]` and `Untrusted[T]` where `T` has no zero value;
6. an instantiated generic type that falls under 1–5 after substitution.

**type.zero.uses** — For a type `T` without a zero value, a program MUST NOT contain an operation that produces the zero value of `T` implicitly. In particular:

- **type.zero.var** — `var x T` without an initializer is rejected.
- **type.zero.literal** — A composite literal of a struct type MUST give every field whose type has no zero value.
- **type.zero.make** — `make([]T, n)` and `make([]T, n, m)` are rejected unless `n` is the constant 0; `make` with a capacity remains allowed.
- **type.zero.map-index** — For a map with element type `T`, the single-value index expression `m[k]` is rejected; only the comma-ok form `v, ok := m[k]` is allowed, and `v` is usable only where `ok` is true.
- **type.zero.chan-receive** — For a channel with element type `T`, the single-value receive `<-ch` is rejected; only the comma-ok form `v, ok := <-ch` is allowed, because a receive from a closed channel yields the zero value.

**Zero values of type parameters.** No constraint expresses "has a zero value"; the requirement is inferred, like an effect summary ([07-effects](07-effects.md)).

**type.zero.param-use** — Inside a generic function or method, operations that produce the zero value of a type parameter `P` (`var x P` without initializer, a composite literal omitting a field of type `P`, `make([]P, n)`, the single-value forms of map index and channel receive, `Option[P].Get`) are allowed.

**type.zero.param-constraint** — Such an operation gives `P` the inferred constraint *requires a zero value*. The constraint propagates: if a generic passes its own type parameter `Q` as the argument for a parameter that requires a zero value, `Q` requires a zero value too. The constraint is part of the generic's summary and is computed per package, like effect summaries.

**type.zero.param-instantiation** — Instantiating a type parameter that requires a zero value with a type that has no zero value (**type.zero.none**) is a compile error. The diagnostic is reported at the instantiation site and MUST show the chain from the instantiation to the operation that uses the zero value.

**type.zero.param-tools** — `anlang doc` and `anlang query` MUST show the inferred constraint of each type parameter ([20-toolchain](20-toolchain.md)).

```go
func Find[T any](xs []T, pred func(T) bool) (T, bool) {
    for _, x := range xs {
        if pred(x) {
            return x, true
        }
    }
    var zero T                 // T: requires a zero value (inferred)
    return zero, false
}

_, _ = Find(shapes, isBig)     // error: Shape has no zero value; Find uses the zero value of T at find.an:7
```

**Note** — For enum types, the author controls this through the order of variants.

## 2.13 Byte order and layout

**type.endian** — The predeclared byte-order types are `u16le`, `u16be`, `u32le`, `u32be`, `u64le`, `u64be`, `i16le`, `i16be`, `i32le`, `i32be`, `i64le`, `i64be`. Each has the value set and operations of the integer type of the same signedness and size; in memory it is stored in the named byte order. Each is a distinct type; conversion to and from other integer types is explicit and follows §2.14.

**type.layout.default** — Without a `LayoutClause`, the compiler chooses field order, alignment and padding of a struct. A program MUST NOT rely on them.

**type.layout.c** — With `layout c`, the struct has the layout of the equivalent C struct under the C ABI of the target ([13-targets](13-targets.md)): fields in declaration order, with the target's alignment rules.

**type.layout.packed** — With `layout packed`, the fields are placed in declaration order without padding.

**type.layout.fields** — A struct with a `LayoutClause` MUST have fields only of integer, byte-order, floating-point, array, and `layout`-struct types, and of enum types with no data. Fields of other types (slices, strings, maps, interfaces, handle types, functions) are rejected.

**Note** — Data layout of `layout` structs for GPU buffers and external functions is used in [12-ffi](12-ffi.md) and [16-gpu](16-gpu.md).

## 2.14 Assignability and conversions

**type.assign.go** — Assignability is as in Go, with these additions:

- `nil` is assignable only per **type.nil.types**;
- `None` is assignable per **type.option.none-assign**;
- constants are assignable to `cstring` and `dstring` per **type.string.terminator-const**;
- an untyped integer constant is assignable to a `wrap` type if it is representable by the corresponding integer type;
- an untyped numeric constant is assignable to a `fixed` type; the value is rounded to the nearest representable value at compile time, and the program is rejected if it is out of range.

**type.conv.go** — Conversions not covered by this section are as in Go.

**type.conv.int-widen** — A conversion between integer types (including byte-order and `wrap` types' underlying integers) whose target type can represent every value of the source type is always allowed.

**type.conv.int-narrow** — Any other conversion `T(x)` between integer types is allowed only if the compiler proves that the value of `x` is representable in `T` ([08-contracts](08-contracts.md)). Otherwise the program MUST be rejected, and the diagnostic SHOULD suggest `conv.Narrow[T](x)`, which returns `(T, bool)`.

```go
func PutChar(x, y u16, c u8, attr u8) { … }

for i, c := range []u8("HELLO") {
    PutChar(u16(i)+10, 5, c, 0x1E)        // allowed: i < 5 is proven
}

v, ok := conv.Narrow[u8](n)               // n not known to fit
```

**type.conv.wrap** — A conversion from any integer type to a `wrap` type, and from a `wrap` type to any `wrap` type, is always allowed and truncates modulo 2ⁿ. A conversion from a `wrap` type to an ordinary integer type follows **type.conv.int-widen** and **type.conv.int-narrow** for the corresponding integer type.

**type.conv.float-int** — A conversion from a floating-point type to an integer type panics if the truncated value is not representable in the target type, unless representability is proven. Conversions from integer to floating-point types are as in Go.

**type.conv.constant** — Constant conversions are as in Go: the value MUST be representable in the target type.

**type.conv.string** — Conversions between `string` and `[]u8` are as in Go. Conversions to `cstring` and `dstring` follow §2.3.

**type.conv.no-implicit** — There are no implicit conversions between distinct non-constant types.

## 2.15 Compiler-known library types

Some types of the standard library have operators. They are ordinary declared types for every other purpose; the compiler knows their operator semantics. This is not operator overloading: user-defined types cannot have operators.

**type.known.wrap** — Package `an/wrap` declares `wrap.U8`, `wrap.U16`, `wrap.U32`, `wrap.U64`, `wrap.I8`, `wrap.I16`, `wrap.I32`, `wrap.I64`. Each has the representation of the corresponding integer type. The operators `+`, `-`, `*`, `<<`, unary `-` and the bitwise operators compute modulo 2ⁿ and never panic. `/` and `%` are as for the corresponding integer type, including the panic on division by zero. Comparison operators are defined. Both operands of a binary operator MUST have the same `wrap` type, except shift counts, which follow Go.

**type.known.simd** — Package `an/simd` declares vector types such as `simd.F32x4`, `simd.F64x2`, `simd.I32x4`, `simd.U8x16`. The arithmetic operators apply element-wise to two operands of the same vector type. Integer element arithmetic follows the rules of the element type (overflow checked) unless stated otherwise in [18-stdlib](18-stdlib.md). Scalars are converted to vectors explicitly (`simd.F32x4Splat(3)`).

**type.known.fixed** — Package `an/fixed` declares fixed-point types such as `fixed.Q16` (16.16) and `fixed.Q8` (8.8). The operators `+`, `-`, `*`, `/` and the comparison operators are defined for two operands of the same fixed type; overflow is checked as for integers.

**type.known.closed** — The set of library types with operators is exactly `wrap`, `simd` and `fixed` (**lex.novelty.closed**). `decimal` and other library types have methods only.

## 2.16 Secret and Untrusted

**type.safety.ref** — `Secret[T]` and `Untrusted[T]` are generic types of the standard library whose restrictions (no `==`, printing, sinks) are specified in [11-data-safety](11-data-safety.md). For the purposes of this file they are struct-like types with one field of type `T`.

## 2.17 Predeclared identifiers

**type.universe** — The universe block contains *(replaces Go)*:

```
Types:
    any bool comparable error string
    i8 i16 i32 i64 u8 u16 u32 u64 int uint usize f32 f64
    cstring dstring
    u16le u16be u32le u32be u64le u64be
    i16le i16be i32le i32be i64le i64be
    Option

Constants:
    true false iota

Zero value:
    nil

Variants:
    None Some

Functions:
    append cap clear close copy delete len make max min panic print println
```

**type.universe.removed** — The Go built-ins `new`, `complex`, `real`, `imag` and `recover` are not predeclared.

**Note** — Recovery from panics is not part of the language: a panic is handled as the target's `OnPanic` specifies, and a panic in a task propagates to its parent ([09-concurrency](09-concurrency.md), [19-runtime](19-runtime.md)).
