# 23. Conformance and Testing of the Environment

Most of the compiler is expected to be written by AI. Checks of `anlang` itself are therefore as mechanical and decisive as the checks of user code. This file specifies the test suites an implementation of `anlang` MUST maintain and pass.

## 23.1 Conformance suite

**conf.suite.per-rule** — For every normative program requirement of this specification, the conformance suite MUST contain at least one program that violates the rule and is expected to be rejected with a diagnostic naming that rule identifier. Where a rule permits something, the suite MUST also contain at least one program that is expected to compile.

**conf.suite.naming** — Every conformance test MUST be named after the rule identifier it checks. A test that checks several rules MUST list all of them.

**conf.suite.expectation** — A negative test passes only if compilation fails and the set of rule identifiers in the error diagnostics equals the expected set. A positive test passes only if compilation succeeds without errors.

**conf.suite.shared** — The same conformance suite MUST be passed by the compiler written in Go and by the compiler written in Anlang.

**conf.suite.implementation-rules** — For every normative implementation requirement (rules that constrain the toolchain), the suite SHOULD contain an automated check, unless the requirement is checked by another kind of test in this file.

**conf.suite.layout** — The suite is stored in the directory `conformance/testdata/` of the toolchain sources, in the following form, so that both compilers of **conf.suite.shared** read the same files:

1. Each subdirectory of `testdata/` is named by a rule identifier and holds the tests filed under that rule.
2. A file `<name>.an` in a rule directory is a single-file test: the source file of package `main` of a project whose module name is `conf`, built for the target `linux-x86_64`.
3. A subdirectory `<name>/` of a rule directory is a project test: a project root with its `anlang.mod` and source files, built for the first target listed in `anlang.mod`.
4. The expectation is written exactly once among the test's files, as a line comment `// want: ok` for a positive test or `// want: <rule> {<rule>}` for a negative test, listing the expected set of rule identifiers of **conf.suite.expectation**.
5. A comment `// checks: <rule> {<rule>}` lists further rules the test checks (**conf.suite.naming**). A negative test MUST name the rule of its directory in `want` or `checks`.
6. A diagnostic without a rule identifier never matches an expectation, so a negative test passes only if every error names a rule.
7. Test files MUST be in canonical form (**lex.source.canonical**) unless they test that rule.
8. A file `<name>.out` next to a positive test `<name>.an` or `<name>/` makes the test a reference program (**conf.reference**). The file holds the expected observable behavior: everything the program writes to standard output and standard error, in order, followed by a line `exit status N` if the exit status `N` is not 0.

```go
// want: decl.func.no-init

package main

func init() {}

func main() {}
```

## 23.2 Reference programs

**conf.reference** — For each supported target, the suite MUST contain reference programs with their expected observable behavior (output, pin changes with timing, UART output, screen contents, exit status). Each MUST produce that behavior in the built-in emulator of the target (see [20-toolchain](20-toolchain.md), 20.10).

Reference programs that do not depend on the target are stored as described in **conf.suite.layout**, item 8; those that test no particular rule are filed under `conf.reference`. They are run in the IR interpreter (**tool.emu.ir**) and, for each target with a backend, in the emulator of the target.

## 23.3 Backend cross-check

**conf.backend.ir** — For every reference program and target, the output of the backend, executed in the architecture interpreter, MUST produce the same observable behavior as the interpretation of the program's IR.

## 23.4 Comparison with Go

**conf.go.common-subset** — Programs written in the common subset of Anlang and Go (without `enum`, `inout`, contracts and other constructs of the novelty inventory) MUST produce the same output when compiled by Go and by Anlang, unless their behavior depends on a semantic deviation of Anlang that Go accepts silently: value semantics of slices and maps (**mem.value.copy**), copy capture by closures (**expr.closure.capture-copy**), an empty slice equal to `nil` (**expr.cmp.nil-empty**), checked integer arithmetic (**expr.int.checked**). The comparison runs the test programs of the Go distribution in the IR interpreter; each program excluded by a deviation is listed with the rule that explains the difference.

## 23.5 Fuzzing

**conf.fuzz** — The parser, the type checker, the solver and the linker MUST be fuzzed. For any input, `anlang` MUST NOT crash, hang beyond its configured limits or produce an internal error; it MUST either succeed or report diagnostics.

**conf.fuzz.allocator** — The `app` allocator MUST be fuzzed, and the memory-checking mode of tests MUST find no errors.

## 23.6 Solver checking

**conf.solver.certificate** — Every result "proved" of the solver MUST be accompanied by a certificate, and a small independent checker MUST confirm it (see [08-contracts](08-contracts.md)). A proof whose certificate is rejected MUST be treated as not proved, and the event MUST be reported as an internal error.

**conf.solver.exhaustive** — On small finite domains, the solver's results MUST agree with exhaustive enumeration.

## 23.7 Self-hosting

**conf.self-hosting** — Let *B1* be the executable produced by the Go version of the compiler from the Anlang sources of the compiler, and *B2* the executable produced by *B1* from the same sources, for the same target. *B1* and *B2* MUST be byte-identical (see [22-compiler](22-compiler.md), 22.9).

## 23.8 External oracles

**conf.oracle.use** — Where an independent implementation exists, it SHOULD be used as an oracle:

- architecture interpreters against QEMU and processor test suites;
- the WebAssembly interpreter against the official WebAssembly specification test suite;
- the type checker against go/types of the Go distribution, on its test programs renamed into Anlang, for the common subset (**conf.go.common-subset**);
- the IR interpreter against the expected output of the "run" test programs of the Go distribution, renamed into Anlang, for the common subset (**conf.go.common-subset**);
- linker output against standard format inspection tools;
- text shaping against a reference shaping test suite;
- GPU output against format validators.

**conf.oracle.scope** — External tools MAY be used only in the tests of the environment. They MUST NOT be part of the toolchain or required by any `anlang` command (see [22-compiler](22-compiler.md), **impl.single-binary**).

## 23.9 Performance

**conf.perf** — Compile time, binary size and code speed MUST be measured per target on a fixed benchmark set. A regression beyond the configured threshold MUST block merging the change.

## 23.10 Regression policy

**conf.regression** — Every test in this file, once added, MUST stay in the suite. A change that makes any previously passing test fail MUST NOT be merged.

## 23.11 Open issues


1. **Performance thresholds.** Deferred to implementation — step 26. Set per benchmark once host targets exist.
