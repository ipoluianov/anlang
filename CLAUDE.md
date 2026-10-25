# Kern

Kern is a programming language and development environment. This repository holds its design and specification.

## Language rules

- Talk to the developer in Russian.
- Write all code comments, specifications, commit messages and other repository text in English.
- The only Russian text in the repository is:
  - `kern.md` — the original design document with rationale;
  - Russian translations of the specification: `spec/*.ru.md`.

## Specification

- `spec/` holds the normative specification in English. Start with `spec/README.md`: it lists the files, the notation and the conventions.
- Every English spec file `spec/NN-name.md` has a Russian translation next to it: `spec/NN-name.ru.md`.
- After any change to an English spec file, update its Russian translation in the same change. The translation follows the English text section by section; code, grammar and rule identifiers stay identical.
- Use the terms from `spec/glossary.md` in both languages. Add a term there before using a new one.
- When a design decision changes, update the specification first, then the translation. `kern.md` records the original design and rationale; if it contradicts `spec/`, `spec/` wins.
