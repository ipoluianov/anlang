# Anlang Specification

This directory holds the normative specification of the Anlang language and environment. Each file has a Russian translation next to it (`NN-name.ru.md`).

## Status

Draft. The specification formalizes the design in `anlang.md` as of 2026-10-25. Places where `anlang.md` left a question open are listed in an **Open issues** subsection at the end of the relevant file.

## Relation to Go

Anlang is specified as a delta over **The Go Programming Language Specification, version go1.23** (hereafter *the Go specification*). Version go1.23 is the reference because it includes range-over-func.

1. Where this specification is silent, a construct has the syntax and meaning given by the Go specification.
2. Every Go construct that Anlang removes or changes is listed explicitly. Pointer types, the address operator `&`, `init` functions and values of type `any` outside the permitted positions are removed (see [02-types](02-types.md), [03-declarations](03-declarations.md)).
3. When this specification and the Go specification conflict, this specification wins.

## Conventions

### Requirement keywords

The keywords **MUST**, **MUST NOT**, **SHOULD**, **SHOULD NOT** and **MAY** are used as described in RFC 2119.

- **Program requirements** constrain Anlang programs. A program that violates one is rejected at compile time unless the rule says otherwise.
- **Implementation requirements** constrain the `anlang` toolchain.

### Rule identifiers

Each normative rule has a stable identifier, written in bold before the rule text:

> **mem.inout.exclusive** — While an `inout` access to a value is live, the program MUST NOT access that value through any other path.

- An identifier is `<area>.<topic>.<name>`, in lowercase ASCII with dots and hyphens.
- Areas match files: `lex`, `type`, `decl`, `expr`, `stmt`, `mem`, `eff`, `contract`, `conc`, `ct`, `safe`, `ffi`, `target`, `emb`, `os`, `gpu`, `ui`, `std`, `rt`, `tool`, `proto`, `impl`, `conf`.
- Identifiers never change meaning. A removed rule's identifier is not reused.
- Compiler diagnostics reference the identifier of the violated rule. Conformance tests are named after the rules they check.

### Notation

Syntax is given in the EBNF of the Go specification:

```
Production  = production_name "=" [ Expression ] "." .
Expression  = Term { "|" Term } .
Term        = Factor { Factor } .
Factor      = production_name | token [ "…" token ] | Group | Option | Repetition .
Group       = "(" Expression ")" .
Option      = "[" Expression "]" .
Repetition  = "{" Expression "}" .
```

- Production names are global across all files. A production defined in the Go specification and not redefined here keeps its Go definition.
- A redefined production is marked *(replaces Go)*; a new one is marked *(new)*.

### Examples and notes

- Code examples are non-normative unless a rule says otherwise.
- Text marked **Note** is non-normative.
- Files or sections marked *informative* are non-normative as a whole.

## Files

| File | Area | Content |
| --- | --- | --- |
| [01-lexical](01-lexical.md) | `lex` | Source text, tokens, keywords, contextual words, literals, canonical formatting |
| [02-types](02-types.md) | `type` | Types, `enum`, `Option`, numeric and string types, synchronization types, zero values, `nil`, conversions, `layout` |
| [03-declarations](03-declarations.md) | `decl` | Packages, import paths, `profile`, file selection, declarations, package-level variables, methods, external functions, `interrupt`, `trap`, `gpu` declarations |
| [04-expressions](04-expressions.md) | `expr` | Expressions, arithmetic and overflow, calls, `...any`, closures, composite literals |
| [05-statements](05-statements.md) | `stmt` | Statements, exhaustive `switch` and patterns, `range` including range-over-func with `inout`, `for const`, `go`, `select`, error discarding, `asm` |
| [06-memory](06-memory.md) | `mem` | Value semantics, `inout` and exclusivity, copy-on-write, arenas, sharing of synchronization types, `nil` safety analysis, `unsafe`, pointer kinds |
| [07-effects](07-effects.md) | `eff` | Effects, inference, parametric summaries, attribution, profiles, constraints, custom effects |
| [08-contracts](08-contracts.md) | `contract` | `requires`, `ensures`, `invariant`, `assert`, proof obligations, the solver interface, `wcet.Within`, `secret.ConstantTime` |
| [09-concurrency](09-concurrency.md) | `conc` | Structured tasks, channels, cancellation, synchronization, `inout` in `go`, `task.Detach`, priorities |
| [10-compile-time](10-compile-time.md) | `ct` | Compile-time evaluation, `meta`, `for const`, `...any` expansion, generators |
| [11-data-safety](11-data-safety.md) | `safe` | `Secret[T]`, `Untrusted[T]`, sinks and validators |
| [12-ffi](12-ffi.md) | `ffi` | External functions: `ffi.Library`, `ffi.Static`, `ffi.At`, passable types, callbacks |
| [13-targets](13-targets.md) | `target` | Targets, architecture descriptions, platforms, binary formats, target resolution |
| [14-embedded](14-embedded.md) | `emb` | Device descriptions, peripheral ownership, interrupts, stack analysis, static tasks, WCET model |
| [15-os](15-os.md) | `os` | Privilege, typed addresses, traps, system call tables, multiprocessing, boot targets |
| [16-gpu](16-gpu.md) | `gpu` | GPU functions, `gpu.Out`, dispatch, shader targets |
| [17-ui](17-ui.md) | `ui` | The `ui` profile, rendering, layout, accessibility, mobile targets, manifests, permissions, hot reload |
| [18-stdlib](18-stdlib.md) | `std` | Standard library structure and package catalog |
| [19-runtime](19-runtime.md) | `rt` | Runtime composition, panics, task stacks |
| [20-toolchain](20-toolchain.md) | `tool` | Projects, `anlang.mod`, `REQUIREMENTS`, commands, builds, tests, emulators, debugging, `anlang port`, `anlang diff` |
| [21-protocol](21-protocol.md) | `proto` | The environment protocol, diagnostics, LSP |
| [22-compiler](22-compiler.md) | `impl` | Compiler pipeline, IR, effect inference algorithm, lowering, ABI, build cache, bootstrapping (*informative* except where marked) |
| [23-conformance](23-conformance.md) | `conf` | Testing of the environment and the conformance suite |
| [24-roadmap](24-roadmap.md) | — | Implementation plan (*informative*) |
| [glossary](glossary.md) | — | Terms in English and Russian |
