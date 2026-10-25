# 05. Statements

This file specifies how Anlang statements differ from Go statements: `switch` over `enum` types with patterns, terminating statements, `range` with `inout` loop variables, `for const`, `go`, `defer` with `inout` arguments, the unused-error rule and `asm`.

Where this file is silent, statements behave as in the Go specification (see [README](README.md)). Contract clauses at the start of function, loop and struct bodies are specified in [08-contracts](08-contracts.md).

**stmt.go** — A statement not changed by this file has the syntax, rules and meaning of the Go specification, including the rules for labels, `break`, `continue`, `goto` and `fallthrough` and the requirement that the body of a function with results end in a terminating statement. A program that violates them MUST be rejected.

## 1. Switch over `enum` types

### 1.1 Syntax

A `switch` whose tag expression has an `enum` type is a *variant switch*. Its case clauses contain *variant patterns* instead of expressions.

```
VariantSwitchStmt = "switch" [ SimpleStmt ";" ] Expression "{" { VariantCaseClause } "}" .   (new)
VariantCaseClause = VariantSwitchCase ":" StatementList .                                     (new)
VariantSwitchCase = "case" VariantPattern { "," VariantPattern } | "default" .                (new)
VariantPattern    = VariantName [ "{" [ PatternBinding { "," PatternBinding } [ "," ] ] "}" ] . (new)
PatternBinding    = identifier .                                                              (new)
```

`VariantName` is defined in [04-expressions](04-expressions.md).

**stmt.switch.variant.parse** — A variant switch is parsed as an `ExprSwitchStmt` of the Go grammar: each `VariantPattern` is syntactically an `Expression` (an identifier, a qualified identifier or a composite literal whose elements are identifiers). It is interpreted as a variant switch during type checking, when the tag expression has an `enum` type.

**stmt.switch.variant.tag** — The tag expression of a variant switch MUST be present. A `switch` with an `enum` tag MUST NOT contain ordinary case expressions.

### 1.2 Patterns and bindings

**stmt.switch.variant.name** — Each `VariantName` in a pattern MUST denote a variant of the tag's `enum` type.

**stmt.switch.variant.bindings** — A pattern without braces matches the variant and binds nothing. A pattern with braces MUST contain exactly one `PatternBinding` per payload field of the variant, in the order of the field declarations. Each binding declares a new variable, scoped to the case clause, whose value is a copy of the corresponding field. The blank identifier `_` binds nothing.

**stmt.switch.variant.multi** — A case with more than one pattern MUST NOT contain bindings.

```go
switch s {
case Circle{r}:      return math.Pi * r * r
case Rect{w, h}:     return w * h
}

switch m {
case Inc, Dec:       return step(s, m), cmd.None   // several patterns, no bindings
case Scanned{code}:  return add(s, code), cmd.None
case Clear:          return State{}, cmd.None
}
```

### 1.3 Exhaustiveness

**stmt.switch.variant.exhaustive** — A variant switch without a `default` clause MUST name every variant of the `enum` type in some pattern. Adding a variant to an `enum` type therefore makes every non-exhaustive variant switch over it a compile-time error.

**stmt.switch.variant.duplicate** — A variant MUST NOT appear in more than one pattern of the same variant switch.

**stmt.switch.variant.default** — A `default` clause is permitted.

**stmt.switch.variant.no-fallthrough** — A variant switch MUST NOT contain a `fallthrough` statement.

### 1.4 Terminating statements

**stmt.terminating.variant** — In addition to the terminating statements of the Go specification, a variant switch is a terminating statement if:

1. it is exhaustive without a `default` clause, or it has a `default` clause;
2. it contains no `break` statement referring to it; and
3. the statement list of every case clause ends in a terminating statement.

**Note** — This rule lets `func Area(s Shape) f64` end with an exhaustive `switch` and no `return` after it.

## 2. `if` and `for`

**stmt.if-for.go** — `if` statements and the three-clause and condition forms of `for` behave as in Go. A loop body MAY start with `invariant` clauses: the body of a `ForStmt` is a `LoopBody` (*(replaces Go)*, defined in [08-contracts](08-contracts.md)) instead of a `Block`. This applies to every form of `for`, including `range` and range-over-func. The body of a range-over-func loop is a block of the enclosing function, not a function literal: `return`, `break`, `continue` and `go` inside it belong to the enclosing function.

## 3. `range`

### 3.1 Go forms

**stmt.range.go** — `range` over arrays, slices, strings, maps, channels, integers and iterator functions behaves as in Go 1.23, with the extension in §3.2.

**stmt.range.copy** — The iteration values of a `range` over an array, slice, string or map are copies of the elements ([06-memory](06-memory.md), **mem.value.copy**). Modifying an element requires indexing the addressable operand: `xs[i].Increment()`.

### 3.2 Iterators that yield `inout` access

An iterator function may yield an `inout` access to a value. This is the mechanism by which containers give access to their parts ([06-memory](06-memory.md), **mem.part.container**).

**stmt.range.inout-iter** — In addition to the iterator function types of Go 1.23, the range expression MAY have a type of the form:

```go
func(yield func(inout V) bool)
func(yield func(K, inout V) bool)
```

**stmt.range.inout** — When the iterator yields `inout V`, the corresponding loop variable is an *`inout` loop variable*:

1. it MUST be declared with `:=` in the `range` clause; the `=` form is not permitted for it;
2. within the loop body it is addressable and denotes the value yielded by the iterator; modifications through it modify that value;
3. it is second-class ([06-memory](06-memory.md), **mem.ref.second-class**): the body MUST NOT store it, return it, send it, convert it to an interface, capture it in a closure other than one allowed by **expr.closure.capture-inout**, or pass it to `go` or `task.Detach`. Reading it produces a copy, which has no such restriction.

**stmt.range.inout-exclusive** — While the loop body runs, the iterator holds an `inout` access to the yielded value. **mem.inout.exclusive** applies: the body MUST NOT access the yielded value other than through the loop variable. In particular, the body MUST NOT access the operand from which the iterator was obtained (for example `stats` in `range stats.Lock()`) in a way that overlaps the yielded value.

**stmt.range.exit** — Leaving the loop body by `break`, `return`, `goto`, a labeled `continue` of an outer loop or a panic makes the pending `yield` call return `false`, as in Go 1.23. The iterator function then runs to completion, including its deferred calls. A completion action of the iterator, such as releasing a lock, is therefore executed on every exit from the loop.

```go
for s := range stats.Lock() {
    if s.count == MaxRecords {
        return ErrFull           // the iterator releases the lock before Record returns
    }
    s.count++
}
```

**stmt.range.yield-after-false** — If an iterator calls `yield` again after it has returned `false`, the program panics, as in Go 1.23.

**Note** — Whether an iterator yields once (`Lock`, `Edit`) or many times (all elements of a container) is a property of the iterator, not of the language.

## 4. `for const`

**stmt.for-const.syntax** — The syntax of the compile-time loop is:

```
ForConstStmt = "for" "const" ( IdentifierList ":=" ) "range" Expression Block .   (new)
```

Its semantics, including which range expressions are permitted, are specified in [10-compile-time](10-compile-time.md).

## 5. `go`

**stmt.go.syntax** — The `go` statement has the syntax of Go. Its semantics — structured tasks, effects, `inout` arguments, error propagation — are specified in [09-concurrency](09-concurrency.md).

## 6. `select`

**stmt.select.go** — `select` behaves as in Go. A receive from or send to a `nil` channel blocks forever, so a `nil` channel disables its case, as in Go.

## 7. `defer`

**stmt.defer.go** — `defer` behaves as in Go, with the rules below.

**stmt.defer.value-args** — Arguments for value parameters of the deferred call are evaluated and copied when the `defer` statement executes, as in Go.

**stmt.defer.inout-args** — The operand path of an `inout` argument of a deferred call (for example the index in `xs[i]`) is evaluated when the `defer` statement executes. The `inout` access itself begins only when the deferred call runs. Between the `defer` statement and the deferred call, the program MAY use the operand normally.

**Note** — This matches Go's behavior with pointer receivers: `defer f.Close()` sees the state of `f` at the time `Close` runs.

## 8. Errors and discarded results

**stmt.error.unused** — A value of type `error`, or of any other type that implements `error`, produced by a call MUST NOT be discarded implicitly. Specifically, the following are compile-time errors:

1. an expression statement consisting of a call that returns at least one error value;
2. a `defer` statement whose call returns at least one error value;
3. an assignment or short variable declaration that receives fewer results than the call returns, where a dropped result is an error value.

**stmt.error.discard** — An error value is discarded explicitly by assigning it to the blank identifier `_`.

```go
Login("alice", wrongPassword)                 // error: result discarded implicitly
_, _ = Login("alice", wrongPassword)          // permitted: explicit discard
defer func() { _ = f.Close() }()              // permitted
```

**stmt.error.go** — A call in a `go` statement does not discard its error result: the error is delivered to the owning invocation through `task.Wait`, and the program must collect or explicitly abandon it ([09-concurrency](09-concurrency.md), **conc.task.error**, **conc.struct.error-wait**).

## 9. Assignments

**stmt.assign.go** — Assignments behave as in Go. Assignment logically copies the value ([06-memory](06-memory.md), **mem.value.copy**). The left operand of an assignment to a part of a value MUST be addressable ([04-expressions](04-expressions.md), **expr.addr.def**), except for a map index expression `m[k] = v`.

**stmt.assign.overflow** — `++`, `--` and arithmetic assignment operators follow **expr.int.checked** and **expr.wrap.ops**.

## 10. `asm`

**stmt.asm.restrictions** — An `asm` statement MAY appear only:

1. in a package of the `core` profile;
2. in a file whose name has an architecture suffix ([03-declarations](03-declarations.md)); the instruction text uses the syntax of that architecture.

**stmt.asm.syntax** — Provisional syntax: `asm` is a contextual word that begins an `asm` statement when it is followed by `(` at the start of a statement:

```
AsmStmt = [ ExpressionList "=" ] "asm" "(" [ Expression ] ")" raw_string_lit .   (new, provisional)
```

The optional expression is a struct value whose fields name the input registers. The optional left-hand side receives a struct value whose fields name the output registers. The instruction text is a raw string literal parsed by the assembler generated from the architecture description ([13-targets](13-targets.md)).

**stmt.asm.effects** — The effects of an `asm` statement are the effects of its instructions as given by the architecture description ([07-effects](07-effects.md)).

## 11. No `unsafe` blocks

**stmt.unsafe.none** — Anlang has no `unsafe` block statement. Unsafe operations are functions of the package `an/unsafe` ([06-memory](06-memory.md), **mem.unsafe.package**).

## Open issues


1. **`asm` syntax.** Deferred to implementation — step 12. The block form of **stmt.asm.syntax** is provisional until the assembler is generated from the first architecture description.
