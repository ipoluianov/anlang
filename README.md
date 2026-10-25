# Kern

Kern is a programming language and development environment where intent, behavior and the proof that they match are written side by side and checked by the machine.

- [spec/](spec/) — the specification (English, with Russian translations).
- [examples/](examples/) — example programs.
- `kern.md` — the original design document (Russian).

## Status

- **Step 1 of the roadmap is done:** the lexer and parser accept the whole Kern grammar (Go plus the novelty inventory), and `kern fmt` prints the canonical form. Both are checked against the Go sources: all 6230 `.go` files of the Go 1.25 distribution parse and are formatted byte for byte as `gofmt` formats them, and formatting is fuzzed for idempotence and preservation of the syntax tree.
- **Beyond that, a walking skeleton:** `kern build` compiles `examples/hello` into a static Linux x86-64 executable without external tools. The checker accepts package `main` with functions without parameters or results that call each other and `fmt.Print` / `fmt.Println` with string literal arguments. Everything else is reported as not supported yet, with the roadmap step that adds it ([spec/24-roadmap.md](spec/24-roadmap.md)).

## Build and run

Requires Go 1.25.

```
go build -o kern ./cmd/kern
cd examples/hello
../../kern run
```

`kern build -target linux-x86_64` writes the executable to `out/linux-x86_64/`. `kern fmt` rewrites the project's files in canonical form; `kern build` rejects files that are not.

## Layout

| Package | Content |
| --- | --- |
| `cmd/kern` | the `kern` command |
| `build` | project loading, file selection, build driver |
| `modfile` | `kern.mod` parser |
| `syntax` | lexer, parser and syntax tree |
| `format` | canonical formatting (`kern fmt`), based on go/printer |
| `types` | checker and lowering to IR |
| `ir` | intermediate representation |
| `arch/x86_64` | x86-64 code generation and the Linux runtime routines |
| `link/elf` | ELF writer |
| `conformance` | checks that tie the code to the specification |

Tests: `go test ./...` (add `-short` to skip the corpus tests over the Go sources). Fuzzing: `go test ./format -run '^$' -fuzz FuzzFormat`.

## License

MIT, with a runtime exception: programs compiled with Kern may be distributed without the Kern license notice, even though they contain parts of the Kern runtime and standard library. See [LICENSE](LICENSE).
