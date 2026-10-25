# Anlang

Anlang (short: an) is a programming language and development environment where intent, behavior and the proof that they match are written side by side and checked by the machine. Site: [anlang.dev](https://anlang.dev).

Source files have the extension `.an`, the standard library lives under `an/...`, the command is `anlang` and a project is described by `anlang.mod`.

- [spec/](spec/) — the specification (English, with Russian translations).
- [examples/](examples/) — example programs.
- `anlang.md` — the original design document (Russian).

## Status

- **Step 1 of the roadmap is done:** the lexer and parser accept the whole Anlang grammar (Go plus the novelty inventory), and `anlang fmt` prints the canonical form. Both are checked against the Go sources: all 6230 `.go` files of the Go 1.25 distribution parse and are formatted byte for byte as `gofmt` formats them, and formatting is fuzzed for idempotence and preservation of the syntax tree.
- **Step 2 is done:** the type checker covers the Go subset: the numeric and string types of Anlang, constants with exact untyped arithmetic, arrays, slices, maps, channels, structs with embedded fields, function types, function literals (closures, with captured variables read-only in them), methods, interfaces with embedding, type assertions and type switches, generic functions and types with constraints, type sets and type inference, project packages imported by the program (checked in dependency order, with exported names, `internal` and import cycles), declarations at package and function level, expressions, conversions, built-in functions and all statements. It is checked against Go: the test programs of the Go distribution are renamed into Anlang (`int8` → `i8` and so on) and the checker must accept or reject each one as go/types does; of 2965 files, 1916 fall into the supported subset and all but one known case agree. Other constructs are reported as not supported yet, with the roadmap step that adds them ([spec/24-roadmap.md](spec/24-roadmap.md)).
- **Step 3 is done:** `enum` types with variants and fields, generic enums and the predeclared `Option` with `None`, `Some` and `Get`; variant values, constructors and literals with type inference; variant switches with patterns and bindings, exhaustiveness (adding a variant breaks every switch without `default`), duplicates and terminating switches; types without a zero value and every operation that would produce one implicitly; the inferred constraint "requires a zero value" of type parameters, propagated through generic calls and reported at the instantiation with the chain to the operation.
- **Step 4 is done:** `inout` parameters and receivers, function types with `inout` parameters; `inout` arguments must be addressable operands of the parameter's type, and the `inout` accesses of one call must not overlap (paths compared by fields, constant indices and constant subranges until the solver proves more); methods with `inout` receivers are called only directly on addressable operands; function literals capture `inout` variables by reference and are then second-class: they may only be called directly; iterators yielding `inout` access, with the loop holding the iterator's operand while the body runs.
- **A walking skeleton:** `anlang build` compiles `examples/hello` into a static Linux x86-64 executable without external tools. Code generation covers functions without parameters or results that call each other and `fmt.Print` / `fmt.Println` with constant arguments, until the IR of step 6.

## Build and run

Requires Go 1.25.

```
go build -o anlang ./cmd/anlang
cd examples/hello
../../anlang run
```

`anlang build -target linux-x86_64` writes the executable to `out/linux-x86_64/`. `anlang fmt` rewrites the project's files in canonical form; `anlang build` rejects files that are not.

## Layout

| Package | Content |
| --- | --- |
| `cmd/anlang` | the `anlang` command |
| `build` | project loading, file selection, build driver |
| `modfile` | `anlang.mod` parser |
| `syntax` | lexer, parser and syntax tree |
| `format` | canonical formatting (`anlang fmt`), based on go/printer |
| `types` | type checker |
| `lower` | translation of the checked program to IR |
| `ir` | intermediate representation |
| `arch/x86_64` | x86-64 code generation and the Linux runtime routines |
| `link/elf` | ELF writer |
| `conformance` | the conformance suite (`conformance/testdata/<rule>/`, spec/23-conformance.md, **conf.suite.layout**) and checks that tie the code to the specification |

Tests: `go test ./...` (add `-short` to skip the corpus tests over the Go sources). Rules without conformance tests: `go test ./conformance -run Coverage -v`. The comparison with Go (`TestGoOracle`) reads the Go sources in `GOROOT`. Fuzzing: `go test ./format -run '^$' -fuzz FuzzFormat`.

## License

MIT, with a runtime exception: programs compiled with Anlang may be distributed without the Anlang license notice, even though they contain parts of the Anlang runtime and standard library. See [LICENSE](LICENSE).
