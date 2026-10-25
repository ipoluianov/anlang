# 21. Environment Protocol

All capabilities of `anlang` are available through one machine-readable protocol. AI tools and editors use it instead of parsing text output.

## 21.1 Transport

**proto.transport** — The protocol MUST be JSON-RPC 2.0 over the standard input and output of the `anlang` process, with the base protocol framing of the Language Server Protocol: each message is preceded by a `Content-Length` header and an empty line.

**proto.servers** — `anlang serve` MUST implement the environment protocol (21.3–21.5). `anlang lsp` MUST implement the Language Server Protocol (21.6). Both MUST operate on the project that contains the working directory of the process (see [20-toolchain](20-toolchain.md), **tool.project.root**).

**proto.cancel** — Both servers MUST support the `$/cancelRequest` notification. A cancelled request MUST return the JSON-RPC error `RequestCancelled` (code -32800).

**proto.concurrency** — A server MAY process requests concurrently. Results MUST be the same as if requests were processed one at a time in the order received.

## 21.2 Common types

The shapes below use TypeScript interface notation, as the Language Server Protocol does. Fields marked `?` are optional.

```typescript
interface Position {
    line: number;      // 1-based
    column: number;    // 1-based, in bytes of the UTF-8 encoding of the line
}

interface Range {
    start: Position;
    end: Position;     // exclusive
}

interface Location {
    file: string;      // path relative to the project root, with "/" separators
    range: Range;
}

interface TextEdit {
    file: string;
    range: Range;
    newText: string;
}

interface Fix {
    title: string;
    edits: TextEdit[];
}

interface Diagnostic {
    rule: string;                  // rule identifier from the specification, e.g. "mem.inout.exclusive"
    severity: "error" | "warning" | "info";
    location: Location;
    message: string;               // human-readable reason
    chain?: Location[];            // call chain that explains the violation, outermost first
    related?: { location: Location; message: string }[];
    fixes: Fix[];                  // may be empty
}
```

**proto.diag.shape** — Every diagnostic produced by `anlang` MUST have the shape `Diagnostic`. The `rule` field MUST be the identifier of the violated rule in this specification.

**proto.diag.chain** — A diagnostic about an effect that is not allowed MUST carry in `chain` the call chain from the boundary to the source of the effect (for example, `TIM2 → buffer.Push → grow → Alloc`, see [07-effects](07-effects.md)).

**proto.diag.fix** — Applying all edits of any one fix of a diagnostic MUST remove that diagnostic and MUST NOT introduce a new diagnostic of severity `error`.

**proto.paths** — File paths in the protocol MUST be relative to the project root and use `/` as separator. Standard library files MUST be referred to with the prefix `an/` followed by the package path and the file name.

## 21.3 Session

**proto.initialize** — The first request of a session MUST be `initialize`. Its result MUST give the `anlang` version, the project's edition and the version of the protocol schema:

```typescript
// request: initialize, params: {}
interface InitializeResult {
    anlangVersion: string;
    edition: string;         // from anlang.mod
    schemaVersion: string;
}
```

**proto.shutdown** — The request `shutdown` MUST end the session; the server then exits with status 0.

## 21.4 Operations

**proto.ops** — `anlang serve` MUST provide the following methods.

| Method | Purpose |
| --- | --- |
| `check` | Types, effects, contracts; diagnostics with rule identifiers and fixes |
| `query` | Callers, sources of effects, impact of a change, unproven conditions |
| `prove` | Prove a contract condition or return a counterexample |
| `test` | Run tests, including in built-in emulators |
| `run` | Run a program on a target or in an emulator and return observable behavior |
| `debug/*` | Breakpoints, stepping, reading state |
| `diff` | Semantic diff of two versions of code |
| `requirements` | Links between requirements and tests |

### 21.4.1 check

```typescript
// params
interface CheckParams { targets?: string[]; packages?: string[] }
// result
interface CheckResult { diagnostics: Diagnostic[] }
```

**proto.op.check** — `check` MUST perform the checks of `anlang check` (see [20-toolchain](20-toolchain.md), **tool.cmd.check**) on the current state of the project files and return all diagnostics, sorted by file, then position, then rule identifier.

### 21.4.2 query

```typescript
interface QueryParams {
    kind: "callers" | "effects" | "impact" | "unproven";
    symbol?: string;         // qualified name, e.g. "weather/server.Handle"
    location?: Location;
}
interface QueryResult {
    items: { location: Location; symbol?: string; effect?: string; chain?: Location[]; message?: string }[];
}
```

**proto.op.query** — `query` MUST answer:

- `callers`: every call site of the symbol;
- `effects`: every effect reachable from the symbol or location, each with the call chain that introduces it, and parametric references as separate items (see [07-effects](07-effects.md));
- `impact`: every declaration and call site that would fail to compile if the symbol's signature changed;
- `unproven`: every proof obligation in the range of the location that the solver did not prove, with the reason.

### 21.4.3 prove

```typescript
interface ProveParams { location: Location }       // the contract clause or assertion
interface ProveResult {
    status: "proved" | "refuted" | "unknown";
    counterexample?: { name: string; value: string }[];
    steps: number;           // solver steps used
    reason?: string;         // for "unknown": e.g. step limit reached
}
```

**proto.op.prove** — `prove` MUST run the solver on the proof obligation at the location (see [08-contracts](08-contracts.md)) and report the outcome. `refuted` MUST carry a counterexample.

### 21.4.4 test

```typescript
interface TestParams { packages?: string[]; target?: string; emu?: string; filter?: string }
interface TestResult {
    tests: {
        package: string;
        name: string;
        status: "pass" | "fail" | "skip";
        failure?: {
            message: string;
            location?: Location;
            counterexample?: string;   // property-based tests
            seed?: string;
            interleaving?: string[];   // deterministic scheduler
        };
    }[];
    diagnostics: Diagnostic[];         // compile errors and requirement checks
}
```

**proto.op.test** — `test` MUST behave as `anlang test` with the same selection (see [20-toolchain](20-toolchain.md), 20.9) and return per-test results sorted by package and name.

### 21.4.5 run

```typescript
interface RunParams { target?: string; emu?: string; args?: string[]; stdin?: string; maxSteps?: number }
interface RunResult {
    exitStatus?: number;
    stdout: string;
    stderr: string;
    observations: { kind: string; time?: string; data: unknown }[];  // pins, UART output, screens
    diagnostics: Diagnostic[];
}
```

**proto.op.run** — `run` MUST build and run the program. In an emulator, it MUST return the observations of [20-toolchain](20-toolchain.md), **tool.emu.observable**. `maxSteps` bounds execution in an emulator; when it is reached, the run stops and the result says so in `stderr`.

### 21.4.6 debug

**proto.op.debug** — The debugger MUST be available through these methods:

| Method | Effect |
| --- | --- |
| `debug/start` | Start a program under the debugger (params as `RunParams`, plus `probe?: string`, `replay?: string`); returns a session id |
| `debug/breakpoints` | Set the full list of breakpoints for a session (locations or function names) |
| `debug/continue` | Run until a breakpoint, exit or panic |
| `debug/step` | Step `into`, `over` or `out` |
| `debug/state` | Return the call stack and the values of variables in a frame, displayed as in **tool.debug.values** |
| `debug/stop` | End the session |

### 21.4.7 diff

```typescript
interface DiffParams { old: string; new: string }   // directories
interface DiffResult {
    packages: {
        path: string;
        added: string[]; removed: string[];
        changed: { symbol: string; old: string; new: string }[];
        implementationChanged: string[];
        effects: { symbol: string; old: string[]; new: string[] }[];
        contracts: { symbol: string; change: "weakened-ensures" | "strengthened-ensures" | "weakened-requires" | "strengthened-requires" | "other"; old: string; new: string }[];
        external: { symbol: string; old: string; new: string }[];
    }[];
}
```

**proto.op.diff** — `diff` MUST return the content of [20-toolchain](20-toolchain.md), **tool.diff.content**.

### 21.4.8 requirements

```typescript
interface RequirementsResult {
    requirements: { id: string; tests: { ref: string; resolved?: string; status?: "pass" | "fail" | "missing" }[] }[];
    diagnostics: Diagnostic[];
}
```

**proto.op.requirements** — `requirements` MUST perform the checks of [20-toolchain](20-toolchain.md), 20.5, running the referenced tests, and return the result per requirement.

## 21.5 Determinism and stability

**proto.determinism** — For the same project files, the same `anlang` version and the same request, the result MUST be byte-identical. Arrays in results MUST be sorted as stated for each operation, or by location when no order is stated. Results MUST NOT contain timestamps or durations.

**proto.determinism.run** — **proto.determinism** applies to `run`, `test` and `debug/*` when the program runs in an emulator or under the deterministic scheduler. When the program runs on the host or on a device, outputs that depend on the environment are reported as observed.

**proto.stability** — Within an edition, the same violation MUST be reported with the same rule identifier. Rule identifiers are never reused for a different meaning (see [README](README.md)).

**proto.schema** — The JSON Schema of every request, result and notification of `anlang serve`, and of the `-json` output of every command, MUST be published with the specification and versioned with the edition. `initialize` reports its version.

## 21.6 Language Server Protocol

**proto.lsp.version** — `anlang lsp` MUST implement the Language Server Protocol, version 3.17.

**proto.lsp.features** — `anlang lsp` MUST provide at least:

- diagnostics, with `code` set to the rule identifier;
- code actions of kind `quickfix` for every fix of a diagnostic;
- go to definition;
- hover that shows the signature, the inferred effects (including parametric references) and the contracts of a function;
- formatting, with the result of `anlang fmt`.

**proto.lsp.encoding** — `anlang lsp` MUST support the UTF-16 position encoding and SHOULD offer UTF-8 when the client supports it.
