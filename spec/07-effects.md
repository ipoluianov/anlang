# 7. Effects and profiles

This file specifies effects: what they are, where they come from, how the compiler infers them, how they are attributed and where they are constrained. It also specifies profiles, which are named sets of effect constraints on packages.

Effects never appear in function signatures. Programs constrain effects only at boundaries (§7.6).

## 7.1 Effect model

**eff.model.set** — An *effect* is a name that denotes a kind of action a function may perform. The *effects of a function* form a finite set of effects. The empty set means the function performs no action besides computation on its arguments and local values.

**eff.model.identity** — Two effects are the same if and only if they are declared by the same declaration: a base effect (§7.2), an effect declared with `effect.Declare` (§7.7), or a peripheral instance effect (§7.8).

**eff.model.names** — In Anlang source, effects are values of type `effect.Effect` from package `an/effect`. Base effects are exported constants of that package (`effect.Alloc`, `effect.Network`). In `anlang.mod`, in diagnostics and in tool output, an effect is written by its bare name (`Alloc`, `SQL`, `TIM2`); if two effects in a program share a bare name, the tools qualify it with the declaring package path.

**eff.model.foreign** — The effect `Foreign` means "may perform any action". For every constraint check (§7.6) a set that contains `Foreign` is treated as containing every effect.

**Note** — `anlang.md` writes base effects without a qualifier (`effect.Without(Alloc, Panic)`, `ffi.Effects{File}`). Inside Anlang source the qualified form is canonical (**eff.model.names**).

## 7.2 Base effects

**eff.base.set** — Package `an/effect` declares the following base effects:

| Group | Effects | Meaning |
| --- | --- | --- |
| Memory | `Alloc` | Dynamic (heap) allocation, including copies made by copy-on-write ([06-memory](06-memory.md)) |
| | `StaticAlloc` | Use of statically sized pools and regions |
| Input and output | `Console`, `File`, `Network`, `Video`, `Keyboard` | Console I/O, file system, network, video output, keyboard input |
| Execution | `Panic` | The function may panic |
| | `Block` | The function may wait at a wait point ([09-concurrency](09-concurrency.md), **conc.wait.points**) |
| | `Tasks` | The function starts tasks with `go` or `task.Go` |
| | `Interrupts` | The function enables, disables or configures interrupts |
| Hardware | `Gpio`, `Uart`, `Timers` and others | Peripheral classes, declared by the device description ([14-embedded](14-embedded.md)) |
| Nondeterminism | `Clock`, `Random` | Reading time; obtaining random values |
| Shared state | `Global` | Access to a package-level variable ([06-memory](06-memory.md)) |
| External code | `Foreign` | Call of an external function without declared effects (§7.3) |

**eff.base.library** — The standard library declares further effects with `effect.Declare` (§7.7). They are listed in the files that define them:

| Effect | Declared by | Specified in |
| --- | --- | --- |
| `Detach` | `an/task` | [09-concurrency](09-concurrency.md) |
| `ExposeSecret` | `an/secret` | [11-data-safety](11-data-safety.md) |
| `SQL` | `an/sql` | [18-stdlib](18-stdlib.md) |
| `Privileged` | architecture packages `an/arch/...` | [15-os](15-os.md) |
| `GPU` | `an/gpu` | [16-gpu](16-gpu.md) |
| `ReadInput`, `WriteOutput` | `an/gen` | [10-compile-time](10-compile-time.md) |
| `Camera`, `Location`, `Notifications`, `Contacts`, `Photos`, `Bluetooth`, `Background` | device packages of the `ui` profile | [17-ui](17-ui.md) |

## 7.3 Sources of effects

An effect enters a program only at a *source*. Every other function obtains effects from the functions it calls (§7.4).

**eff.source.primitive** — The implementation-provided primitives of the standard library (system calls, interrupt instructions, memory allocation, clock and random-number access) carry fixed effects documented with each primitive.

**eff.source.alloc** — An operation that may allocate dynamic memory has the effect `Alloc` ([06-memory](06-memory.md), **mem.alloc.effect**). The operations are listed in **eff.source.alloc-ops**.

**eff.source.alloc-ops** — The operations with the effect `Alloc` are exactly:

1. `make`; a slice or map composite literal with at least one element; `append` with at least one value or with `...`;
2. string concatenation with `+` or `+=` that is not constant; a conversion that is not constant between a string type and `[]u8` or `[]i32`, from an integer type to a string type, and to `cstring` or `dstring`;
3. an assignment to a map element, including assignment operations, `++` and `--`; `delete`; `clear` of a map;
4. an operation that may copy a shared buffer (**mem.cow.slice**, **mem.inout.unique**): an assignment, assignment operation, `++` or `--` whose target is reached through an index expression on a slice operand that is not unique, and an `inout` access to such a target, as an argument or a receiver; a slice operand that is not unique passed to an `inout` parameter or receiver, including the first operand of `copy` and `clear` of a slice; an assignment of an operand that is not unique to an `inout` parameter of a slice type;
5. a conversion of a value that is not constant to an interface type, unless its type is a boolean or numeric type, a struct type without fields or an `enum` type none of whose variants carry data;
6. a function literal that uses local variables or parameters of an enclosing function, except one that a borrowing function returns (**mem.ref.borrowing**); a method value;
7. `fmt.Sprint`, `fmt.Sprintln` and `fmt.Sprintf`.

**eff.source.panic** — An operation that may panic has the effect `Panic`. This includes integer overflow checks, index and slice bounds checks, checks of conversions from floating-point to integer types, arena generation checks and explicit calls of `panic`; the operations are listed in **eff.source.panic-ops**. A check that the compiler removes because the solver proved it cannot fail ([08-contracts](08-contracts.md), **contract.check.runtime**) contributes no effect.

**eff.source.panic-ops** — The operations with the effect `Panic` are exactly:

1. binary `+`, `-`, `*` and unary `-` on operands of a checked integer type or a type parameter, and the corresponding assignment operations, `++` and `--`, unless the operation is constant (**expr.int.checked**);
2. `/` and `%` on operands of an integer type, including the wrapping types, or of a type parameter, unless the divisor is a constant other than 0 and, for a checked signed type, other than -1 (**expr.int.div**);
3. a shift whose count is not constant and has a signed type;
4. an index expression on a slice, a string or a value of a type parameter, and on an array unless the index is constant; a slice expression with at least one index, on an array unless all its indices are constant (**expr.bounds.check**);
5. a conversion that is not constant from a floating-point type to an integer type (**type.conv.float-int**), from a slice to an array, and to `cstring` or `dstring`;
6. a type assertion that does not have the comma-ok form;
7. `==` and `!=` on operands whose type is or contains an interface type or is a type parameter, unless an operand is `nil`; an index expression, element assignment, composite literal entry or `delete` on a map whose key type is or contains an interface type;
8. `make` with a length or capacity that is not constant; `panic`; `close`; a channel send;
9. an assignment to an `inout` parameter or receiver of a slice type, which panics if it changes the length of a subslice passed to it (**mem.inout.subslice**); in a function whose only parameter is a function with a `bool` result and which has no results, a call of that parameter, which panics if an iterator calls `yield` after it returned `false` (**stmt.range.yield-after-false**).

**eff.source.builtin** — Until the standard library is compiled from Anlang source (roadmap steps 9–10 and 29), the following functions are primitives (**eff.source.primitive**): `print` and `println` have the effect `Console`; `fmt.Print`, `fmt.Println` and `fmt.Printf` have the effects `Console` and `Panic` (**std.fmt.print**). The functions of `an/fmt` call the methods `Error` and `String` of their arguments: for an argument of concrete type `T` they add the summaries of `T.Error` and `T.String`, and for an argument of an interface type with such a method they add the open reference to it.

**eff.source.wait** — A wait point ([09-concurrency](09-concurrency.md), **conc.wait.points**) has the effect `Block`. In the language these are a channel receive or send that is not a case of a `select` statement, a `select` statement without a `default` case and a `range` over a channel.

**eff.source.go** — A `go` statement and a call of `task.Go` have the effect `Tasks`.

**eff.source.global** — Reading or writing a package-level variable, including a hardware mapping, has the effect `Global`.

**eff.source.asm** — An `asm` block has the effects declared for the instructions it contains by the architecture description ([13-targets](13-targets.md)).

**eff.source.instruction** — A call of a function of an architecture instruction package (`an/arch/...`) has the effects that the architecture description declares for that instruction.

**eff.source.external** — An external function ([12-ffi](12-ffi.md)) has exactly the effects listed in its `ffi.Effects` argument. If no `ffi.Effects` argument is given, it has the effect `Foreign`. The compiler cannot verify declared effects of external functions; tools MUST report them as unverified ([20-toolchain](20-toolchain.md)).

**eff.source.mark** — A call `effect.Mark(e)` adds the effect `e` to the function that contains the call (§7.7).

## 7.4 Inference and summaries

The compiler infers effects. A program never writes the effects of a function.

**eff.infer.summary** — The *effect summary* of a function `f` is a triple Σ(f) = (C, P, Q):

- C — the *concrete effects* of `f`: a set of effects;
- P — the *parametric references* of `f`: references of the form "effects of calling parameter p", "effects of method M of parameter p" and "effects of method M of type parameter T";
- Q — the *open references* of `f`: references of the form "effects of method M of interface type I" and "effects of calling a value of function type F", for values that do not come from a parameter.

**eff.infer.rules** — Σ(f) is the least triple, under component-wise set inclusion, that satisfies all of the following:

1. Each source (§7.3) in the body of `f` adds its effects to C.
2. A static call `g(a1, …, an)` adds C(g) and Q(g) to C(f) and Q(f). For each reference in P(g) that names parameter `pi` of `g`:
   - if the argument `ai` is itself a parameter of `f` (or a method value of one), the reference becomes the corresponding reference in P(f);
   - otherwise the argument is a concrete function value or a value of a concrete type, and the summary of the referenced function or method is added to Σ(f) by rule 2 recursively.
3. A call through a function value or an interface method whose receiver value is a parameter of `f` adds the corresponding reference to P(f).
4. A call through a function value or an interface method whose value does not come from a parameter of `f` (it is read from a field, element, map entry, channel, package-level variable or result) adds the corresponding open reference to Q(f).
5. A call of a method on a value whose type is a type parameter `T` adds "effects of method M of T" to P(f).
6. A conversion in `f` of a value of concrete type `T` to an interface type `I` adds, for each method M of `I`, C and Q of `T.M` to Σ(f), and records `T.M` as an implementation of the open reference "effects of method M of I" (§7.5).
7. The creation in `f` of a function value (function literal, method value or function name used as a value) of type `F` adds C and Q of the created function to Σ(f), and records that function as an implementation of the open reference "effects of calling a value of type F" (§7.5). References of a function literal to parameters of `f` that it captures become references in P(f).

**eff.infer.close** — Where a parametric reference cannot be replaced by an argument — the argument is not one of the forms of **eff.infer.rules** 2, or the function is reached through an open reference, so its arguments are unknown — a reference to calling parameter `p` of function type `F` stands for the open reference "effects of calling a value of type `F`", and a reference to method M of parameter `p` of interface type `I` for "effects of method M of `I`". A reference of a function literal to a parameter of an enclosing function becomes a reference of the enclosing function (**eff.infer.rules** 7); in the summary of the literal itself it stands for the open reference.

**eff.infer.recursion** — For mutually recursive functions the summaries are the least fixed point of the rules of **eff.infer.rules**.

**eff.infer.generic** — A generic function has one summary. At each instantiation, references to methods of type parameters are replaced by the summaries of the methods of the type arguments.

**eff.infer.modular** — The summary of a function depends only on the source of its package and on the summaries of the functions of the packages it imports. It MUST NOT depend on callers of the function.

**eff.infer.determinism** — Effect inference MUST be exactly as precise as these rules. An implementation MUST NOT remove an effect by additional analysis, except for the removal of `Panic` sources by solver proofs under **eff.source.panic**. Two conforming implementations therefore compute the same summaries.

**Note** — Examples of summaries:

```go
func Fetch(url string) ([]u8, error)                     // C = {Network, Alloc, Block}
func Hash(data []u8) u64                                 // C = {}
func Slice[T any](xs inout []T, less func(a, b T) bool)  // C = {}, P = {less}
func Serve(l Listener, h Handler) error                  // C = {Network, Block, …}, P = {h.ServeHTTP}
```

## 7.5 Attribution and execution effects

Effects are used in two different ways, and the specification distinguishes them.

**eff.attr.own** — The *own effects* of a function `f` are C(f). An effect of a function passed as an argument is attributed to the caller who passed it, not to the callee that calls it (**eff.infer.rules** 2). An effect of an implementation stored in a data structure is attributed to the function that converted it to an interface or created the function value (**eff.infer.rules** 6, 7).

**eff.attr.resolve** — Over the whole program, an open reference "effects of method M of I" resolves to the union of the execution effects of all methods `T.M` recorded as its implementations; an open reference "effects of calling a value of type F" resolves to the union of the execution effects of all functions recorded as its implementations.

**eff.attr.flow** — A conversion of a value of interface type `I1` to another interface type `I2`, including a type assertion to `I2`, makes every implementation recorded for "method M of `I1`" an implementation of "method M of `I2`" for each method M of `I2`; a conversion between function types does the same for "calling a value of type F". A method value of an interface value is an implementation of its function type through the open reference to the method.

**eff.attr.exec** — The *execution effects* of a call are the effects that may occur while the call runs:

- for a function without parametric references: X(f) = C(f) ∪ resolve(Q(f));
- for a call with arguments: X is computed after replacing each parametric reference by the execution effects of the corresponding argument.

**eff.attr.use** — Own effects are checked by attribution constraints; execution effects are checked by execution constraints. Each constraint in §7.6 states which kind it uses.

**Note** — Attribution keeps package summaries honest: a JSON parser that calls a callback with network access has no `Network` in its own effects. Execution effects keep runtime guarantees sound: an interrupt handler that calls a stored callback with `Alloc` violates its constraint, wherever the callback came from.

## 7.6 Constraints

Constraints are written only at boundaries. Each constraint is a set of effects that MUST NOT occur in the constrained set.

| Boundary | Constrained set | Kind | Rule |
| --- | --- | --- | --- |
| Package profile | own effects of every function of the package | attribution | **eff.profile.allowed** |
| `anlang.mod` `effects` | own effects of every function of the matching packages | attribution | **eff.mod.deny** |
| `ensures effect.Without(...)` | execution effects of each call of the function | execution | **eff.without.check** |
| `interrupt`, `trap` | execution effects of the handler | execution | **eff.handler.implicit** |
| Target `Provides`, `Lacks` | execution effects of the program | execution | **eff.target.check** |
| Peripheral contexts | execution effects of each execution context | execution | [14-embedded](14-embedded.md) |
| Application manifest | execution effects of the program | execution | [17-ui](17-ui.md) |

**eff.constraint.report** — When a constraint is violated, the diagnostic MUST name the constraint, the violating effect and a call chain from the constrained function to a source of the effect, for example `TIM2 → buffer.Push → grow → Alloc`. For an attribution constraint, which every function of a package must satisfy, the violation is reported at each function where the effect enters the package: from a source in its body or from a function of another package. The functions of the same package that receive the effect from such a function are not reported again.

### 7.6.1 `ensures effect.Without`

**eff.without.form** — `effect.Without(e1, …, en)` is a compiler-known function of package `an/effect`. It MAY appear only as the whole expression of an `ensures` clause ([08-contracts](08-contracts.md), **contract.constraint.forms**). Its arguments MUST be constant expressions of type `effect.Effect`.

**eff.without.check** — For a function `f` with `ensures effect.Without(E)`:

1. at the declaration of `f`, X(f) computed with parametric references left out MUST NOT contain any effect of E;
2. at each call of `f`, the execution effects of that call MUST NOT contain any effect of E.

### 7.6.2 Interrupt and trap handlers

**eff.handler.implicit** — A function declared with `interrupt` or `trap` has the implicit constraint `ensures effect.Without(Alloc, Block, Panic)`. Its execution effects are checked under **eff.without.check**.

### 7.6.3 Target

**eff.target.check** — Let X be the execution effects of the program: the union of the execution effects of `main`, of every function started as a task, and of every `interrupt` and `trap` handler, together with the deferred effects of the program (**eff.deferred.program**). X MUST satisfy the target rules of [13-targets](13-targets.md) §13.3: every environment effect in X is listed in the target's `Provides` (**target.effects.provides**), no effect of X is listed in `Lacks` (**target.effects.lacks**), and `Foreign` follows **target.effects.foreign**. Which effects are environment effects is given by **target.effects.environment**; the other effects (`Panic`, `Block`, `Global` and custom effects) are not checked against the target (**target.effects.computational**).

## 7.7 Custom effects

**eff.custom.declare** — `effect.Declare(name)` is a compiler-known function. It MAY appear only as the initializer of a package-level constant. `name` MUST be a constant string. Each call declares a distinct effect, whose identity is the constant (§7.1, **eff.model.identity**) and whose bare name is `name`.

```go
package sql

import "an/effect"

const SQL = effect.Declare("SQL")

func (db DB) Query(q string, args ...any) (Rows, error) {
    effect.Mark(SQL)
    …
}
```

**eff.custom.mark** — `effect.Mark(e)` is a compiler-known function whose argument MUST be a constant of type `effect.Effect`. It adds `e` to the concrete effects of the function whose body contains the call. It has no run-time behavior.

**eff.custom.additive** — A custom effect never hides other effects. The effects of a function that calls `effect.Mark` are still the union of all its sources; a network call behind `SQL` keeps `Network`.

### 7.7.1 Deferred effects

A function MAY return a description of work that the runtime performs later, such as a `Cmd` of the `ui` profile ([17-ui](17-ui.md)). The effects of that later work are *deferred effects* of the function.

**eff.deferred.declare** — `effect.Defer(e1, …, en)` is a compiler-known function whose arguments MUST be constants of type `effect.Effect`. It adds `e1, …, en` to the deferred effects of the function whose body contains the call. It has no run-time behavior and does not add concrete effects.

**eff.deferred.attribution** — Deferred effects of a callee are charged to the caller like own effects (**eff.attr.own**): profiles and `anlang.mod` rules see them. They are not execution effects of the caller: `effect.Without` and handler constraints do not see them.

**eff.deferred.program** — The deferred effects of every function reachable in the program are part of the program's effect set. They are checked against the target (§7.6.3) and used to derive permissions ([17-ui](17-ui.md)).

## 7.8 Peripheral instance effects

**eff.periph.instance** — The device description ([14-embedded](14-embedded.md)) declares one effect for each peripheral instance (`TIM2`, `USART1`) and, where the hardware allows independent access, for each pin. Any access to the instance, including through a copy of the value that denotes it, has that effect in addition to its class effect (`Timers`, `Uart`, `Gpio`).

**eff.periph.ownership** — The rules that use instance effects to check exclusive use per execution context are specified in [14-embedded](14-embedded.md).

## 7.9 Profiles

A profile is a named set of effect constraints and language restrictions declared for a package by the `profile` clause ([03-declarations](03-declarations.md)).

**eff.profile.names** — There are three profiles, ordered from strictest to least strict: `core` ⊑ `app` ⊑ `ui`.

**eff.profile.allowed** — The own effects (**eff.attr.own**) of every function of a package with profile p MUST NOT contain an effect that p forbids:

| Profile | Forbidden effects | Additional restrictions |
| --- | --- | --- |
| `core` | `Alloc`, `Detach` | — |
| `app` | `Interrupts`, `Privileged`, hardware effects (class and instance effects of **eff.base.set** group Hardware) | MUST NOT import `an/unsafe`; MUST NOT contain `asm`, `interrupt` or `trap` declarations |
| `ui` | same as `app` | same as `app` |

**eff.profile.privileged** — `Privileged` is closed by default in every profile: a function of a package with own effect `Privileged` is rejected unless `anlang.mod` allows it for that package (**eff.mod.allow**).

**eff.profile.import** — The profiles of a package P and of a package Q that P imports MUST satisfy: for every profile p declared by P, Q declares some profile q with q ⊑ p. A stricter package is usable from a less strict one; the reverse is rejected.

**eff.profile.multiple** — A package MAY declare several profiles. It is checked against each of them; equivalently, it MUST satisfy the strictest one it declares and every import rule for each declared profile.

**eff.profile.default** — A package without a `profile` clause has profile `app`.

```go
package crc

profile core, app, ui
```

**Note** — `crc` above is usable from firmware and from a web page. A package `httpclient` with profile `app` cannot be imported by a `core` package.

**eff.profile.ui** — The additional rules of the `ui` profile (structure of `Update` and `View`, accessibility in types, rendering) are specified in [17-ui](17-ui.md).

## 7.10 Effect constraints in `anlang.mod`

**eff.mod.syntax** — The `effects` block of `anlang.mod` contains entries `<pattern> deny {<effects>}` and `<pattern> allow {<effects>}`. The full grammar of `anlang.mod` is in [20-toolchain](20-toolchain.md).

**eff.mod.pattern** — A pattern is a package path relative to the module root. A pattern `path` matches the package `path`; a pattern `path/...` matches `path` and every package below it; the pattern `...` matches every package of the project.

**eff.mod.deny** — For each `deny` entry, the own effects of every function of every matching package MUST NOT contain any of the listed effects.

**eff.mod.allow** — An `allow` entry opens effects that are closed by default (**eff.profile.privileged**) for the matching packages. An `allow` entry MUST NOT open an effect that the package profile forbids.

**eff.mod.precedence** — If several entries match a package, entries with longer patterns take precedence over entries with shorter ones; for one pattern, `deny` takes precedence over `allow`.

```
effects (
    jsonparse/...  deny  {Network, File, Panic}
    kernel/...     allow {Privileged}
)
```

## 7.11 Visibility of effects

**eff.report.tools** — The implementation MUST make the effect summary of every function available through `anlang doc`, `anlang query` and the environment protocol ([20-toolchain](20-toolchain.md), [21-protocol](21-protocol.md)), including for each effect the call chain to its source.

**eff.report.diff** — `anlang diff` MUST report effects that one version of a function has and the other does not ([20-toolchain](20-toolchain.md)).
