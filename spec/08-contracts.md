# 8. Contracts

This file specifies contracts — `requires`, `ensures`, `invariant` — and `assert`, the proof obligations they create, the compiler-known constraint functions that are written as `ensures` clauses, and the requirements on the built-in solver.

Contracts are checked only at compile time. They are never evaluated at run time.

## 8.1 Syntax

Contract clauses occupy the leading contract region of a function body or a loop body, and the leading clause region of a struct type ([01-lexical](01-lexical.md), **lex.context.contract-region**, **lex.context.struct-region**).

```
FunctionBody    = "{" { ContractClause ";" } StatementList "}" .         (replaces Go)
ContractClause  = RequiresClause | EnsuresClause .                       (new)
RequiresClause  = "requires" Expression .                                (new)
EnsuresClause   = "ensures" Expression .                                 (new)

LoopBody        = "{" { InvariantClause ";" } StatementList "}" .        (new)
InvariantClause = "invariant" Expression .                               (new)
```

`LoopBody` replaces `Block` as the body of a `ForStmt` ([05-statements](05-statements.md)). The struct-level `InvariantClause` is part of `StructType` ([02-types](02-types.md)).

**contract.syntax.order** — In a function body, all `requires` clauses and `ensures` clauses MUST precede every other statement. They MAY appear in any order among themselves.

**contract.syntax.bool** — The expression of a `requires`, `ensures` or `invariant` clause MUST have an untyped boolean or `bool` type, except for the constraint forms of §8.6.

**contract.syntax.where** — `requires` and `ensures` MAY appear in function and method declarations and in function literals. `invariant` MAY appear in `for` statement bodies and in struct types.

### 8.1.1 `assert`

**contract.assert.form** — `assert` is a predeclared function `func assert(cond bool)`. A call of `assert` MUST be an expression statement.

**contract.assert.meaning** — `assert(c)` creates the proof obligation that `c` holds at that point (§8.3). After the call, the solver MAY assume `c`.

## 8.2 Contract expressions

**contract.expr.pure** — A contract expression and the argument of `assert` MUST have no effects ([07-effects](07-effects.md)): every function it calls MUST have empty execution effects, except `Panic`. A call in a contract expression that could panic is itself a proof obligation that it does not panic.

**contract.expr.math** — Integer arithmetic in contract expressions is performed on unbounded mathematical integers; it never overflows and never wraps. Operands of fixed-size integer types are converted to mathematical integers before the operation. Floating-point arithmetic in contract expressions follows IEEE 754 as at run time.

**contract.expr.scope** — A `requires` expression MAY refer to the parameters and receiver of the function, to package-level constants and to package-level variables. An `ensures` expression MAY refer additionally to `result` and to named results.

**contract.expr.result** — Inside an `ensures` expression, the identifier `result` denotes the single result of a function with exactly one unnamed result. A function with several results MUST name its results to refer to them in `ensures`; in such a function `result` is not defined. Outside `ensures`, `result` is an ordinary identifier.

**contract.expr.post-state** — In an `ensures` expression, a parameter or receiver passed as `inout` denotes its value at the moment of return.

**contract.expr.result-only** — `result` is the only special name in contracts. Constraints that are not postconditions are written as calls of compiler-known functions (§8.6).

## 8.3 Proof obligations

A *proof obligation* is a condition that the solver must prove at a program point, given the facts known there.

**contract.obligation.requires** — At each call of a function `f` with `requires c`, `c` with the arguments substituted for the parameters is a proof obligation of the caller.

**contract.obligation.ensures** — At each `return` of a function with `ensures c`, and at the end of a function body without results, `c` is a proof obligation of the function. An `ensures` clause states nothing about a call that panics.

**contract.obligation.assume** — In the body of a function, the solver MAY assume every `requires` clause of the function at entry. After a call of a function `g`, the caller MAY assume every `ensures` clause of `g`, with arguments and results substituted.

**contract.obligation.indirect** — A function with `requires` clauses other than `requires true` MUST NOT be used as a function value or as a method that satisfies an interface. Calls through function values and interfaces carry no preconditions.

**contract.obligation.unproven** — If the solver does not prove a proof obligation created by `requires`, `ensures`, `invariant` or `assert`, the program is rejected. The diagnostic MUST name the clause, the program point, and a counterexample if the solver found one (**contract.solver.counterexample**).

## 8.4 Loop invariants

**contract.loop.header** — An `invariant` clause in the leading region of a loop body states a condition at the loop header: it MUST hold each time the loop condition is evaluated, including the last time, when the loop exits. It is written in the body only because a `for` header has no place for it.

**contract.loop.obligations** — For a loop with invariant I, the solver MUST prove:

1. **initiation** — I holds when control first reaches the loop header;
2. **preservation** — if I and the loop condition hold at the header, I holds at the header again after one execution of the body along every path that continues the loop (including `continue`).

After the loop, the solver MAY assume I and the negation of the loop condition. A `break` path carries only the facts known at the `break`.

**contract.loop.range** — For `for i := range n` and `for i := range xs` (and the two-variable forms), the loop header is the point before the index variable takes its next value. At that point the invariant is instantiated with the index variable equal to the number of completed iterations; on exit it is instantiated with the index equal to `n` or `len(xs)`.

**contract.loop.default** — For a loop without an invariant, the solver knows after the loop only the facts about variables that the loop does not assign, and the negation of the loop condition.

```go
func Sum(xs []i32) i64 {
    ensures result == math.Sum(xs)

    var s i64
    for i := range xs {
        invariant s == math.Sum(xs[:i])
        s += i64(xs[i])
    }
    return s                          // on exit: i == len(xs), so s == math.Sum(xs)
}
```

## 8.5 Type invariants

**contract.type.clause** — An `invariant` clause in the leading clause region of a struct type states a condition on every value of the type. In the expression, the fields of the struct are in scope by their names.

**contract.type.boundaries** — The invariant of a type T MUST hold at each of the following *boundaries* for a value v of type T:

1. after evaluation of a composite literal of type T;
2. at the end of every function that received v through an `inout` parameter or receiver, including methods;
3. at every point where v is passed as an argument, returned, sent on a channel, or stored into a field, element, map entry or package-level variable.

Each boundary is a proof obligation. Between boundaries, inside one function, the invariant MAY be violated.

**contract.type.assume** — A function MAY assume the invariant of T for every value of type T that it receives as a parameter, as a result of a call, from a channel, or by reading a field, element, map entry or package-level variable.

**contract.type.zero** — If the zero value of T violates the invariant of T, T has no zero value: a variable declaration of type T without initializer, and a composite literal that omits a field of type T, are rejected. A type that contains a field of such a type also has no zero value. The same rule applies to `enum` types without a zero value ([02-types](02-types.md)).

```go
type Range struct {
    invariant lo <= hi
    lo, hi int
}

func (r inout Range) Shift(d int) {
    r.lo += d                         // invariant may be violated here
    r.hi += d                         // and holds again at the end: boundary 2
}
```

## 8.6 Constraint forms

Some constraints of a function are not postconditions. They are written as an `ensures` clause whose whole expression is a call of a compiler-known function.

**contract.constraint.forms** — The constraint forms are:

| Form | Meaning | Specified in |
| --- | --- | --- |
| `effect.Without(e1, …, en)` | the function does not have the listed effects | [07-effects](07-effects.md), **eff.without.check** |
| `secret.ConstantTime()` | the function's control flow and memory access do not depend on `Secret` values | [11-data-safety](11-data-safety.md) |
| `wcet.Within(d)` | every execution of the function completes within duration `d` | §8.6.1 |

**contract.constraint.only** — A constraint form MUST be the whole expression of an `ensures` clause. It MUST NOT be combined with other expressions by operators and MUST NOT appear anywhere else.

### 8.6.1 `wcet.Within`

**contract.wcet.form** — `wcet.Within(d)` is a compiler-known function of package `kern/wcet`. `d` MUST be a constant expression of type `time.Duration`.

**contract.wcet.check** — For a function with `ensures wcet.Within(d)`, the compiler MUST compute an upper bound of the execution time of the function on the target, using the cycle model of the architecture description and the clock frequency of the target ([13-targets](13-targets.md), [14-embedded](14-embedded.md)), and MUST reject the program if the bound exceeds `d`.

**contract.wcet.bound** — The bound MUST be sound: no execution on the modelled hardware takes longer. Every loop on a path of the function MUST have an iteration bound that follows from the solver's facts; otherwise the bound cannot be computed and the program is rejected.

**contract.wcet.report** — If the check fails, the diagnostic MUST show the longest execution path found and its estimated time.

**Note** — For processors with caches and pipelines the bound is conservative. Exact analysis is a goal for later versions.

```go
interrupt ADC1() {
    ensures wcet.Within(20 * time.Microsecond)
    sample := device.ADC1.Read()
    buffer.Push(sample)
}
```

## 8.7 Run-time checks

**contract.check.runtime** — Integer overflow, index and slice bounds, narrowing conversions and arena generation checks ([04-expressions](04-expressions.md), [06-memory](06-memory.md)) are proof obligations of a different kind: if the solver proves that the check cannot fail, the check is removed; otherwise the check remains in the program, fails at run time with a panic, and gives the function the effect `Panic` ([07-effects](07-effects.md), **eff.source.panic**). Such checks do not make a program invalid by themselves.

**contract.check.forbidden-panic** — Where `Panic` is forbidden (in `interrupt` and `trap` handlers, under `effect.Without(Panic)`, or by `kern.mod`), every such check MUST be proven or the operation MUST be written in a form that cannot fail (`bits.WrappingAdd`, `conv.Narrow`, `wrap` types).

## 8.8 Solver

The solver is built into `kern`. External solvers are not used.

**contract.solver.builtin** — An implementation MUST prove obligations with its own built-in solver and MUST NOT depend on external solver programs or services.

**contract.solver.determinism** — The solver's work on an obligation is limited by a number of steps, not by time. The result for an obligation MUST depend only on the obligation, the facts given to the solver, the step limit and the solver version. The default step limit is fixed by the language edition ([20-toolchain](20-toolchain.md)).

**contract.solver.results** — For each obligation the solver returns one of: *proved*, *refuted* with a counterexample, or *unknown* (step limit reached). *Refuted* and *unknown* both mean "not proved".

**contract.solver.counterexample** — When the solver refutes an obligation, the diagnostic MUST include the counterexample as values of the program variables involved.

**contract.solver.certificate** — For every obligation reported as *proved*, the solver MUST produce a certificate, and an independent checker that is part of `kern` MUST verify it. If the checker rejects a certificate, the obligation is treated as not proved and the implementation MUST report an internal error.

**contract.solver.cache** — The implementation MAY cache results by a hash of the normalized obligation, its facts, the step limit and the solver version. A cached *proved* result MUST have a certificate verified by the checker.

### 8.8.1 Solver capability levels (informative)

The solver's capabilities grow in stages. Each stage widens the set of obligations it proves; none changes the meaning of a program that was already accepted.

| Stage | Theory | Typical obligations |
| --- | --- | --- |
| 1 | linear integer arithmetic | ranges, array bounds, `lo <= hi` |
| 2 | bit vectors | absence of overflow, `bits.WrappingAdd`, register masks |
| 3 | arrays and slices | contents after copying, loop invariants |
| 4 | `enum` and `Option` | which variant is possible at a point |
| 5 | bounded quantifiers | "for all i in a range" |
