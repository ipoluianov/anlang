# 11. Data Safety

Area: `safe`.

Two wrapper types turn whole classes of vulnerabilities into compile errors:

- `Secret[T]` for passwords, keys and tokens;
- `Untrusted[T]` for data that comes from outside the program.

SQL injection, cross-site scripting, path traversal and passwords in logs become impossible without an explicit, searchable action.

## 11.1 The wrapper types

**safe.types.predeclared** — `Secret` and `Untrusted` are generic types predeclared in the universe block, like `Option`. Their operations are provided by packages `an/secret` and `an/untrusted`.

**safe.types.zero** — The zero value of `Secret[T]` and of `Untrusted[T]` wraps the zero value of `T` (see [02-types](02-types.md)).

**safe.types.distinct** — `Secret[T]` and `Untrusted[T]` are distinct from `T`. A value of a wrapper type is not assignable or convertible to `T`, and a value of `T` is not assignable or convertible to the wrapper type. The only ways in and out are the functions listed in this file.

**safe.types.opaque** — A wrapper value has no accessible fields, methods of `T`, operators or indexing. In particular `+`, `==`, `<`, `len`, `range`, slicing and field selection are compile errors on a wrapper value.

**safe.types.meta** — Compile-time reflection treats both wrapper types as having no fields (**ct.meta.opaque** in [10-compile-time](10-compile-time.md)). Generic serializers therefore cannot read their contents.

**safe.types.static-switch** — A static type switch (**ct.typeswitch.static**) MAY match `Secret[T]` and `Untrusted[T]`. This is how `...any` functions recognize wrapper arguments.

## 11.2 `Secret[T]`

| Restriction | What it prevents |
| --- | --- |
| printed by `fmt` and loggers as `***` | passwords in logs |
| no `==`; comparison only by `secret.Equal` in constant time | timing attacks |
| storage is overwritten with zeros when released | secrets in memory dumps |
| not passed to network, files or UI without `secret.Expose`, which has effect `ExposeSecret` | accidental disclosure |

### 11.2.1 Creating secrets

**safe.secret.new** — `secret.New(x T) Secret[T]` wraps a value. The standard library functions that produce secret material — password prompts, key generation, decryption of key files — return `Secret[T]`.

### 11.2.2 Deriving secrets

**safe.secret.map** — `secret.Map(s Secret[T], f func(T) U) Secret[U]` applies `f` to the contents and wraps the result. The effect set of `f` MUST NOT contain I/O, hardware, `Global`, `Foreign` or `ExposeSecret` effects. `secret.Map` itself has no `ExposeSecret` effect: the derived value stays secret.

**Note** — Hashing and key derivation are written with `secret.Map`: `crypto.Hash(pass)` returns `Secret[[]u8]` and exposes nothing.

### 11.2.3 Comparing secrets

**safe.secret.no-eq** — `Secret[T]` is not comparable. It MUST NOT be an operand of `==` or `!=`, a map key, or a `switch` tag.

**safe.secret.equal** — `secret.Equal(a, b Secret[T]) bool` compares the contents. `T` MUST be `string`, `[]u8` or an array of `u8`. The implementation MUST take time that depends only on the lengths of `a` and `b`, not on their contents.

```go
func Login(user string, pass Secret[string]) (Session, error) {
    hash, err := db.PasswordHash(user)              // Secret[[]u8]
    if err != nil {
        return Session{}, err
    }
    if !secret.Equal(crypto.Hash(pass), hash) {
        return Session{}, ErrDenied
    }
    log.Info("login", user, pass)                   // logged: login alice ***
    return newSession(user), nil
}
```

### 11.2.4 Printing secrets

**safe.secret.print** — Every standard library function that formats values — `fmt`, `log`, string conversion in `strconv`, encoders in `json` and other format packages — MUST format a `Secret[T]` argument as the three characters `***`, regardless of `T` and of the format verb.

**safe.secret.debugger** — Debuggers and crash reports MUST show a `Secret[T]` value as `***` (see [20-toolchain](20-toolchain.md), [19-runtime](19-runtime.md)).

### 11.2.5 Exposing secrets

**safe.secret.expose** — `secret.Expose(s Secret[T]) T` returns the contents. Every call of `secret.Expose` has effect `ExposeSecret`, which propagates like any other effect (see [07-effects](07-effects.md)). `ExposeSecret` is a custom effect declared with `effect.Declare` in package `an/secret`.

**safe.secret.sinks** — Because of **safe.types.distinct**, a `Secret[T]` cannot reach network, file, UI or external-function parameters of type `T`. A standard library function that accepts `...any` and writes to such a sink MUST reject `Secret[T]` arguments at compile time with `meta.Reject` (**ct.reject.call**), except formatting functions, which print `***` by **safe.secret.print**.

**safe.secret.ffi** — `Secret[T]` is not a passable type for external functions (see [12-ffi](12-ffi.md)). Secret material is passed to external code only after `secret.Expose`.

### 11.2.6 Erasing secrets

**safe.secret.zero** — When storage that holds the representation of a `Secret[T]` value is released or reused, the implementation MUST overwrite it with zeros first. This covers stack slots at the end of their lifetime, copy-on-write buffers when their last reference is released, arena slots and task stacks. The overwrite MUST NOT be removed by optimization.

**safe.secret.copies** — Copies made by value semantics are erased under the same rule. When the compiler moves or copies a secret representation, it MUST erase the source storage when that storage dies.

**Note** — Values held only in processor registers, or copied by external code after `secret.Expose`, cannot be guaranteed erased.

## 11.3 `Untrusted[T]`

Everything that comes from outside the program is returned by the standard library as `Untrusted[T]`.

### 11.3.1 Sources

**safe.untrusted.sources** — The following standard library functions MUST return their external data wrapped in `Untrusted`:

- HTTP request bodies, query parameters, headers, cookies and path parameters;
- data read from files, sockets, standard input and pipes, when read through text or structured decoders (`json`, `csv`, `bufio` line readers);
- environment variables and command-line arguments;
- user input from UI text fields.

**Note** — Raw byte I/O through `io.Reader` returns `[]u8` for performance. Decoders that turn bytes into strings or structures wrap their results.

### 11.3.2 Validators

A *validator* turns an `Untrusted[T]` into a meaningful type or an error.

**safe.untrusted.validator** — The standard library provides validators for common types, for example `validate.Email(u Untrusted[string]) (Email, error)` and `fs.SafePath(root Path, u Untrusted[string]) (Path, error)`.

**safe.untrusted.accept** — `untrusted.Accept(u Untrusted[T]) T` returns the contents without checking. It is the only way to write a new validator. Its calls are found by searching for `untrusted.Accept` and are listed by `anlang query`.

```go
func ParseAge(u Untrusted[string]) (Age, error) {
    s := untrusted.Accept(u)
    n, err := strconv.Atoi(s)
    if err != nil || n < 0 || n > 150 {
        return 0, ErrBadAge
    }
    return Age(n), nil
}
```

### 11.3.3 Sinks

A *sink* is a function whose argument is interpreted as code or as a resource name: SQL text, a file path, HTML, a shell command.

**safe.untrusted.sink** — A sink's text parameter has type `string` (or a dedicated type such as `fs.Path` or `html.Trusted`). By **safe.types.distinct**, an `Untrusted[string]` cannot be passed to it, and string concatenation with an `Untrusted[string]` operand is a compile error (**safe.types.opaque**).

**safe.untrusted.params** — Parameters of a sink that are never part of the interpreted text MAY accept `Untrusted[T]`: SQL query parameters, HTML template values and the argument vector of a process. The sink MUST pass such values out of band: as bound parameters, escaped text or separate process arguments.

**safe.untrusted.html** — HTML rendering in the standard library MUST escape every interpolated value for its context. Inserting unescaped markup requires a value of type `html.Trusted`, which cannot be built from `Untrusted[T]` except through `untrusted.Accept`.

**safe.untrusted.shell** — The standard library MUST NOT provide a function that runs a shell command line given as a single string. Processes are started with a program path and an argument vector; arguments MAY be `Untrusted[string]`, the program path MUST NOT.

```go
func Search(r http.Request) (http.Response, error) {
    q := r.Query("q")                                          // Untrusted[string]

    rows, err := db.Query("SELECT title FROM docs WHERE title LIKE ?", q)   // parameter: allowed
    // db.Query("SELECT ... '" + q + "'")                      // compile error: + on Untrusted

    if err != nil {
        return http.Response{}, err
    }
    return html.Render(resultsPage, rows), nil                 // escaped automatically
}
```

**safe.untrusted.print** — Formatting functions MAY print `Untrusted[T]` values. Loggers MUST escape control characters in them.

## 11.4 Constant-time code

The constraint `ensures secret.ConstantTime()` is written like any function constraint; the mechanism of constraints in `ensures` is defined in [08-contracts](08-contracts.md). This section defines what the constraint requires.

**safe.ct.dependent** — A value is *secret-dependent* if it is computed from the contents of a `Secret[T]`: the result of `secret.Expose`, the argument of the function passed to `secret.Map`, and every value computed from these by data flow.

**safe.ct.branch** — In a function with `ensures secret.ConstantTime()`, the program MUST NOT branch on a secret-dependent value. This covers the conditions of `if`, `for` and `switch`, the operands of `&&` and `||`, `select`, type switches, and calls through function values or interfaces chosen by a secret-dependent value.

**safe.ct.callees** — Every function called from a constant-time function with a secret-dependent argument MUST satisfy **safe.ct.branch** for that argument. The check is transitive and uses the whole program (see [07-effects](07-effects.md) for the call graph).

**safe.ct.index** *(future stage)* — In a function with `ensures secret.ConstantTime()`, a memory access whose index or address is secret-dependent will be a compile error. Implementations of the first stage MUST NOT reject such accesses; they SHOULD warn.

**safe.ct.instructions** *(future stage)* — Operations whose execution time on the target depends on operand values (for example division on some processors) will be rejected for secret-dependent operands, using the timing model of the architecture description (see [13-targets](13-targets.md)).

## 11.5 Packages `an/log` and `an/html`

This file owns the data-safety behavior of the logging and HTML packages; [18-stdlib](18-stdlib.md) lists their profiles.

**safe.pkg.log** — `an/log` MUST format its arguments by the rules of `an/fmt`, including **safe.secret.print**. In `core` it MUST NOT allocate (**std.core.no-hidden-alloc**).

**safe.pkg.html** — `an/html` MUST render by **safe.untrusted.html**. `html.Trusted` is its type for markup that is inserted without escaping.
