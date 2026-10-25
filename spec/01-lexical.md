# 1. Lexical Structure

This file specifies source text, tokens, keywords, contextual words and literals, and the closed lists of differences between Kern and Go. Everything not stated here is as in the Go specification, sections "Source code representation" and "Lexical elements".

## 1.1 Source text

**lex.source.utf8** — A source file MUST be Unicode text encoded in UTF-8, as in Go.

**lex.source.extension** — Kern source files have the extension `.kn`. Files with other extensions in a package directory are not source files.

**lex.source.canonical** — A source file MUST be in canonical form: the byte sequence that `kern fmt` produces for it. An implementation MUST report a non-canonical file as an error in `kern build` and `kern check`, and MUST provide `kern fmt` that rewrites any syntactically valid file into canonical form. Canonical formatting is idempotent: formatting a canonical file returns it unchanged.

**Note** — The canonical form follows `gofmt`. Its exact rules are defined by the reference implementation of `kern fmt` and its tests (see [20-toolchain](20-toolchain.md)).

## 1.2 Tokens, comments and semicolons

**lex.token.go** — Tokens, white space, comments and automatic semicolon insertion are as in the Go specification.

**lex.comment.inert** — Comments never affect the meaning of a program. Directives inside comments (such as Go's `//go:build` or `//go:` pragmas) have no effect. Build constraints are expressed by profiles, inferred effects and file name suffixes ([03-declarations](03-declarations.md), **decl.file.suffix**).

## 1.3 Identifiers

**lex.ident.go** — Identifiers are as in Go.

**lex.ident.contextual** — The contextual words of §1.5 are identifiers. Outside the positions listed in §1.5 they may be used as any other identifier.

## 1.4 Keywords

**lex.keyword.set** — The following keywords are reserved and MUST NOT be used as identifiers:

```
break        default      func         interface    select
case         defer        go           map          struct
chan         else         goto         package      switch
const        fallthrough  if           range        type
continue     for          import       return       var

enum         inout        profile
```

The first 25 are the keywords of Go. The last three are new:

| Keyword | Use | Specified in |
| --- | --- | --- |
| `enum` | sum type with variants that may carry data | [02-types](02-types.md) §2.5 |
| `inout` | mutable parameter, receiver or iteration variable without pointers | [03-declarations](03-declarations.md) §3.8, [06-memory](06-memory.md) |
| `profile` | profile clause of a source file | [03-declarations](03-declarations.md) §3.3 |

**Note** — Go code that uses `enum`, `inout` or `profile` as identifiers is renamed by `kern port`.

## 1.5 Contextual words

A contextual word has special meaning only in the positions listed below. In those positions it is never an identifier.

**lex.context.positions** — The contextual words and the positions where they are recognized are:

| Word | Position | Meaning | Specified in |
| --- | --- | --- | --- |
| `requires` | first token of a statement in the leading contract region of a function body | precondition | [08-contracts](08-contracts.md) |
| `ensures` | same as `requires` | postcondition or function constraint | [08-contracts](08-contracts.md) |
| `invariant` | first token of a statement in the leading contract region of a `for` statement body | loop invariant | [08-contracts](08-contracts.md) |
| `invariant` | first token of an entry in the leading clause region of a struct type's field list | type invariant | [02-types](02-types.md) §2.4, [08-contracts](08-contracts.md) |
| `layout` | first token of an entry in the leading clause region of a struct type's field list | struct layout | [02-types](02-types.md) §2.13 |
| `interrupt` | first token of a top-level declaration | interrupt handler declaration | [03-declarations](03-declarations.md) §3.10 |
| `trap` | first token of a top-level declaration | trap handler declaration | [03-declarations](03-declarations.md) §3.10 |
| `gpu` | first token of a top-level declaration, followed by `func` | GPU function declaration | [03-declarations](03-declarations.md) §3.10 |
| `asm` | first token of a statement, followed by `(`; or after `=` in an assignment statement | inline assembly | [05-statements](05-statements.md) §10 |

**lex.context.contract-region** — The *leading contract region* of a function body or `for` statement body is the maximal sequence of statements at the start of the block whose first token is the identifier `requires`, `ensures` (function bodies) or `invariant` (loop bodies). A statement in that region whose first token is one of these words is a contract clause, even if it could otherwise be parsed as an expression statement (for example `requires(x)`).

**lex.context.struct-region** — The *leading clause region* of a struct field list is the maximal sequence of entries at the start of the list whose first token is `invariant` or `layout`. Each such entry is a clause, not a field declaration. A field named `invariant` or `layout` MAY be declared after the leading clause region.

**Note** — At the top level Go permits only keyword-introduced declarations, so an identifier as the first token of a top-level declaration is unambiguous. Inside blocks, an identifier immediately followed by an expression is not valid Go, which is why contract clauses do not need reserved words.

## 1.6 Operators and punctuation

**lex.operator.go** — The set of operators and punctuation is that of Go. The tokens `*` and `&` remain lexically, but their unary pointer meanings are removed ([04-expressions](04-expressions.md)).

## 1.7 Literals

**lex.literal.go** — Integer, floating-point, rune and string literals are as in Go, including digit separators (`0x0800_0000`).

**lex.literal.no-prefix** — There are no literal prefixes or suffixes beyond those of Go. Text in a specific encoding or with a terminator is produced by ordinary compile-time functions (`encoding.CP866("…")`) and by conversion to `cstring` or `dstring` ([02-types](02-types.md) §2.3).

**lex.literal.no-imaginary** — Imaginary literals are removed.

**lex.literal.default-type** — The default type of an untyped integer constant is `int`, of an untyped floating-point constant `f64`, of an untyped rune constant `i32`, of an untyped string constant `string`.

## 1.8 Novelty inventory

The novelty inventory is the closed list of everything Kern adds to Go. Adding an entry is a design decision of the same weight as adding a keyword and MUST be made by changing this table.

**lex.novelty.closed** — A Kern feature that is not an entry of the table below, and not a removal or restriction of §1.9, is not part of the language.

| Kind | Entries |
| --- | --- |
| keywords | `enum`, `inout`, `profile` |
| contextual words | `requires`, `ensures`, `invariant`, `interrupt`, `trap`, `gpu`, `layout`, `asm` |
| syntactic constructs | `for const`; external function `func f(...) T = expr`; `asm` block; patterns in `case` (`case Circle{r}:`); variants in an `enum` body |
| semantics without new syntax | `nil` without dereference; `inout` iteration variable in range-over-func; `...any` as a compile-time tuple; synchronization types as handles; `inout` arguments in `go`; `const` from function calls |
| compiler-known types with operators | `simd`, `fixed`, `wrap` |
| decided 2026-10-25 ([open-issues](open-issues.md)) | predeclared function `assert` ([08-contracts](08-contracts.md)); variants with data as constructor functions ([02-types](02-types.md)); static type switch on type-parameter values and `...any` elements, and `meta.Reject` ([10-compile-time](10-compile-time.md)); constants of composite types ([10-compile-time](10-compile-time.md)); predeclared `Secret` and `Untrusted` ([11-data-safety](11-data-safety.md)); deferred effects of `Cmd` constructors ([07-effects](07-effects.md), [17-ui](17-ui.md)) |

## 1.9 Removed and restricted Go features

The following Go features are removed or restricted. Each is specified in the file listed.

| Feature | Change | Specified in |
| --- | --- | --- |
| pointer types `*T`, unary `&`, unary `*`, `new` | removed; `inout`, slices and handles instead | [02-types](02-types.md) §2.7, [04-expressions](04-expressions.md) |
| `uintptr`, `complex64`, `complex128`, `complex`, `real`, `imag` | removed | [02-types](02-types.md) §2.2 |
| predeclared Go numeric names (`int8`…`float64`, `byte`, `rune`) | replaced by `i8`…`f64` | [02-types](02-types.md) §2.2 |
| values of type `any` / `interface{}` | only as a type constraint and as `...any` parameters | [02-types](02-types.md) §2.9 |
| `nil` | only the empty value of interfaces, slices, maps and channels | [02-types](02-types.md) §2.11 |
| zero values | absent for some types | [02-types](02-types.md) §2.12 |
| `init` functions and import side effects | removed | [03-declarations](03-declarations.md) §3.7 |
| blank and dot imports | removed | [03-declarations](03-declarations.md) §3.2 |
| import paths | only `kern/...` and `<module>/...` | [03-declarations](03-declarations.md) §3.2 |
| package-level variables | only synchronization types and hardware mappings | [03-declarations](03-declarations.md) §3.6 |
| build constraints in comments | removed | §1.2 |
| integer overflow | error instead of wraparound | [04-expressions](04-expressions.md) |
| narrowing conversions | only when proven | [02-types](02-types.md) §2.14 |
| unused error values | compile-time error | [05-statements](05-statements.md) |
| goroutines | structured tasks | [09-concurrency](09-concurrency.md) |
| run-time reflection | compile-time only | [10-compile-time](10-compile-time.md) |
| cgo | external functions in the style of purego | [12-ffi](12-ffi.md) |

## Open issues

1. **Non-canonical formatting as an error.** Should a non-canonical file be an error in every build or only a warning during development? Provisional: error (**lex.source.canonical**).
2. **Contextual word in leading position used as an ordinary name.** A call `requires(x)` at the start of a function body is parsed as a contract clause. Provisional: accepted; such a call must be written elsewhere or as `(requires)(x)`.
3. **Position of `layout`.** `kern.md` does not say where `layout packed` is written. Provisional: in the leading clause region of the struct field list, like a type invariant.
4. **Default type of untyped rune constants.** Provisional: `i32`, since `rune` is not predeclared.
