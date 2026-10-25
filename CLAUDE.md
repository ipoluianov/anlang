# Anlang

Anlang is a programming language and development environment. This repository holds its design and specification.

## Language rules

- Talk to the developer in Russian.
- Write all code comments, specifications, commit messages and other repository text in English.
- The only Russian text in the repository is:
  - `anlang.md` — the original design document with rationale;
  - Russian translations of the specification: `spec/*.ru.md`.

## Licenses

The project is licensed under MIT (`LICENSE`). Respect the licenses of everything taken from elsewhere: code, data, test suites, documentation.

- Reading other projects to learn how something is done is always fine. Write our own code; ideas and algorithms are free to use, text is not.
- Before copying or adapting anything, check its license.
  - **Light obligations — comply and use it.** Keeping a copyright notice, adding the license text as a file, an attribution line in a file header, a `NOTICE` file. Typical licenses: MIT, BSD, ISC, Zlib, Apache 2.0. Put third-party license texts in `third_party/<name>/LICENSE` and say in the header of each adapted file where it comes from.
  - **Heavy obligations — do not use it.** Anything that would require releasing our code under another license or publishing extra sources (GPL, LGPL, AGPL, MPL for modified files, SSPL), non-commercial or field-of-use restrictions, or code with no license at all (all rights reserved). Find another source or write it ourselves, and tell the developer.
  - If unsure which case applies, ask the developer before using it.
- **Using Anlang must not burden its users.** Code that ends up inside programs built with Anlang — the runtime, the standard library, code the compiler inserts — must be our own, or come from a license with no obligations for binary distribution: 0BSD, MIT-0, Unlicense, CC0, or Zlib (which asks for notices only in source form). Do not adapt MIT, BSD (including Go), Apache or similar code into these parts: their notices would have to ship with every user's program. Code of the toolchain itself (compiler, `anlang` command) may use light-obligation licenses; those obligations fall on whoever distributes Anlang.
- External test suites and data used only in tests are downloaded by scripts and not committed, so their licenses do not attach to the repository.

### Go sources

- The Go sources in `/usr/local/go/src` may be read as a reference (for example `cmd/compile/internal/syntax`, `go/types`, `cmd/compile/internal/ssa`, `cmd/internal/obj/x86`, `cmd/link`, `runtime`).
- Go uses a BSD 3-clause license, which has light obligations. Adapted Go code may go into the toolchain only, never into the runtime or the standard library. If a substantial fragment is adapted, say so in the file header ("Based on <path> from the Go sources, Copyright The Go Authors, BSD-style license") and keep the Go license text in `third_party/go/LICENSE`.

## Specification

- `spec/` holds the normative specification in English. Start with `spec/README.md`: it lists the files, the notation and the conventions.
- Every English spec file `spec/NN-name.md` has a Russian translation next to it: `spec/NN-name.ru.md`.
- After any change to an English spec file, update its Russian translation in the same change. The translation follows the English text section by section; code, grammar and rule identifiers stay identical.
- Use the terms from `spec/glossary.md` in both languages. Add a term there before using a new one.
- When a design decision changes, update the specification first, then the translation. `anlang.md` records the original design and rationale; if it contradicts `spec/`, `spec/` wins.
