# 12. Foreign Function Interface

Area: `ffi`.

Anlang calls external code in the style of Go's purego: no C compiler, no header parsing, no binding generators. An external function is declared with an ordinary Anlang signature and bound to a symbol taken from a dynamic library at program start, from an object file at build time, or from a fixed address. The toolchain remains the single `anlang` executable, and cross-compilation needs no other tools.

## 12.1 External function declarations

The declaration form `func` *FunctionName* *Signature* `=` *Expression* — a function without a body bound to a symbol — is defined in [03-declarations](03-declarations.md). This section defines the binding expression and the rules for such functions.

**ffi.decl.binding** — The expression after `=` MUST be a compile-time evaluable call (see [10-compile-time](10-compile-time.md)) of one of:

| Binding | Symbol source |
| --- | --- |
| `lib.Symbol(name [, effects])` where `lib` is an `ffi.Library` constant | dynamic library, loaded at program start |
| `obj.Symbol(name [, effects])` where `obj` is an `ffi.Static` constant | object file, linked into the program at build time |
| `ffi.At(address [, effects])` | fixed address in ROM, BIOS or a separately flashed stack |
| `js.Import(module, name [, effects])` | WebAssembly import, on the `web` platform |

`name` and `module` are string constants; `effects` is an `ffi.Effects` constant.

**ffi.decl.no-generics** — An external function MUST NOT have type parameters, MUST NOT be a method and MUST NOT have a variadic parameter.

**ffi.decl.package-local** — An external function MAY be called, and used as a function value, only inside the package that declares it. A call from another package is a compile error regardless of the function name. The package exposes ordinary Anlang wrappers instead.

**ffi.decl.variadic-c** — A C function with a variable number of arguments is declared once for each argument list the program uses, each declaration with its own Anlang name and fixed signature.

```go
package sqlite

import "an/ffi"

const lib = ffi.Library{
    Linux:   "libsqlite3.so.0",
    MacOS:   "/usr/lib/libsqlite3.dylib",
    Windows: "sqlite3.dll",
    Android: "libsqlite3.so",
}

func sqlite3_open(path cstring, db inout ffi.Handle) i32 = lib.Symbol("sqlite3_open", ffi.Effects{File})
func sqlite3_close(db ffi.Handle) i32                    = lib.Symbol("sqlite3_close", ffi.Effects{File})
```

## 12.2 Effects of external functions

**ffi.effects.declared** — The effects of an external function are the effects listed in its binding's `ffi.Effects` argument. The compiler cannot verify them.

**ffi.effects.foreign** — If the binding has no `ffi.Effects` argument, the function has the single effect `Foreign`.

**ffi.effects.foreign-deny** — `Foreign` means "may do anything". A boundary that denies any effect — a profile, a target, a `anlang.mod` constraint, `effect.Without(...)`, an `interrupt` or `trap` handler — also rejects `Foreign` (see [07-effects](07-effects.md)).

**ffi.effects.unverified** — Implementations MUST mark declared effects of external functions as unverified in effect reports, in `anlang query` answers and in `anlang diff` output, and MUST report changes to them separately from inferred effects (see [20-toolchain](20-toolchain.md)).

**ffi.effects.callback** — When an `ffi.Callback(f)` value is passed to an external function, the effects of `f` are added to the effects of that call and attributed to the caller, as for any function passed as an argument (see [07-effects](07-effects.md)).

## 12.3 Symbol sources

### 12.3.1 `ffi.Library`: dynamic libraries

**ffi.lib.desc** — `ffi.Library` is a struct type with one `string` field per platform that supports dynamic loading — `Linux`, `MacOS`, `IOS`, `Windows`, `Android` — and a `bool` field `Optional`. A field holds the library name or path on that platform. A library descriptor MUST be declared as a constant.

**ffi.lib.platforms** — `ffi.Library` bindings are available on Linux, Android, macOS, iOS and Windows. If a function bound to a library is reachable in a build for a platform whose field is empty or which has no dynamic loading, the build fails.

**ffi.lib.load** — Every non-optional library with a reachable bound function MUST be loaded, and every such symbol resolved, before `main` starts. If a library or symbol is missing, the program MUST NOT start: it reports every missing library and symbol and exits with a non-zero status.

**ffi.lib.optional** — For a library with `Optional: true`, missing library or symbols do not prevent start. `lib.Available() bool` reports whether the library and all its reachable symbols were loaded. A call to a function of an unavailable optional library panics; such functions have effect `Panic` in addition to their declared effects.

### 12.3.2 `ffi.Static`: object files

**ffi.static.desc** — `ffi.Static` is a struct type with one `string` field per architecture, named as the architecture constant (`I8086`, `ARMv7M`, `X86_64`, `ARM64`, `RISCV32`, …; see [13-targets](13-targets.md)). A field holds the path of an object file or archive for that architecture, relative to the module root. A descriptor MUST be declared as a constant.

**ffi.static.availability** — `ffi.Static` bindings are available on all targets, including DOS, microcontrollers and kernels. If a function bound to an `ffi.Static` descriptor is reachable in a build for an architecture whose field is empty, the build fails.

**ffi.static.formats** — The `anlang` toolchain MUST include a linker for relocatable object files and archives in ELF, Mach-O and COFF formats. No C compiler or external linker is used.

**ffi.static.abi-check** — The toolchain MUST check at build time that each object file matches the architecture and C ABI of the target (machine type, word size, endianness, floating-point ABI). A mismatch is a build error.

**ffi.static.unresolved** — Undefined symbols of a linked object file are resolved against other `ffi.Static` objects of the program. Any symbol left unresolved is a build error naming the object file and the symbol.

**ffi.static.location** — Object files are part of the project. Because their code cannot be read, adding or replacing one is a decision for a human reviewer; the toolchain does not track their origin or license.

### 12.3.3 `ffi.At`: fixed addresses

**ffi.at.platforms** — `ffi.At` bindings are available only on platforms without dynamic loading: `dos` and `bare` (see [13-targets](13-targets.md)). On other platforms they are a compile error.

**ffi.at.address** — The address MUST be a compile-time constant in the code address form of the target architecture: a `usize` on flat-address architectures, a far address such as `i8086.Far(segment, offset)` on segmented ones.

### 12.3.4 `js.Import`: WebAssembly imports

**ffi.js.platform** — `js.Import` bindings are available only on the `web` platform. Each becomes an import of the generated WebAssembly module with the given module and field names.

### 12.3.5 Platform summary *(informative)*

| Platform | Symbol source |
| --- | --- |
| Linux, Android | `dlopen` / `dlsym` |
| macOS, iOS | `dlopen` / `dlsym`; system frameworks; the Objective-C runtime through `objc_msgSend`, declared per signature |
| Windows | `LoadLibrary` / `GetProcAddress` |
| Android, Java APIs | function pointers from the JNI function table |
| web | WebAssembly imports: `= js.Import("console", "log")` |
| DOS, microcontrollers, kernels | no dynamic loading: `ffi.Static`, `ffi.At` for ROM and loader tables; software interrupts through `asm` or the architecture instruction package |

## 12.4 Calling convention

**ffi.abi.c** — External functions and callbacks use the C ABI of the target, taken from the architecture and platform descriptions (see [13-targets](13-targets.md)). The internal Anlang ABI MAY differ.

**ffi.abi.blocking** — A call to an external function may block. The scheduler's treatment of long blocking calls is defined in [09-concurrency](09-concurrency.md).

## 12.5 Passable types

**ffi.types.params** — A parameter of an external function MUST have one of the following types:

| Anlang type | External side |
| --- | --- |
| `i8` … `i64`, `u8` … `u64`, `int`, `uint`, `usize` | integer type of the same width and signedness |
| `f32`, `f64` | `float`, `double` |
| `bool` | C `_Bool` |
| `ffi.Handle` | opaque pointer to a library object; the zero value is the null pointer |
| `cstring` | `const char*`, NUL-terminated |
| struct with `layout c` whose fields are passable | struct passed by value |
| `[]T`, `T` passable and not a slice | pointer to the first element, read-only |
| `inout T`, `T` a passable non-slice type | pointer to the value for the duration of the call |
| `inout []T` | pointer to the first element, writable |
| `ffi.Callback` | pointer to a function |

**ffi.types.results** — A result of an external function MUST be a numeric type, `bool`, `ffi.Handle`, or a struct with `layout c` whose fields are numeric, `bool` or `ffi.Handle`. An external function MAY have no result. Multiple results are not allowed.

**ffi.types.no-pointers** — There are no pointer types `*T` and no address operator `&x` in external function declarations or calls.

**ffi.types.no-wrappers** — `Secret[T]`, `Untrusted[T]`, synchronization types, interfaces, maps, strings (other than `cstring`), function values (other than `ffi.Callback`) and `enum` types are not passable.

### 12.5.1 Memory during a call

**ffi.mem.pinned** — Memory passed through a slice, `inout` or `cstring` parameter MUST stay at the same address for the duration of the call.

**ffi.mem.readonly** — External code MUST NOT write through a pointer obtained from a `[]T` or `cstring` parameter. Value semantics depend on it; the compiler cannot check it.

**ffi.mem.unique** — Before a call with an `inout []T` argument, the implementation MUST make the slice's buffer unique (copy-on-write, see [06-memory](06-memory.md)), so that writes by external code are not visible through other copies.

**ffi.mem.no-retain** — External code MUST NOT keep a pointer obtained from a parameter after the call returns. The compiler cannot check this either.

**ffi.mem.empty-slice** — For an empty slice, the pointer value is unspecified and MUST NOT be dereferenced by external code; the length is passed separately by the program.

**ffi.mem.cstring** — `ffi.CString(s string) cstring` creates a NUL-terminated copy of `s`; it is equivalent to the conversion `cstring(s)` (**type.string.terminator-conv**), has effect `Alloc` and panics if `s` contains a NUL byte. `ffi.GoString(h ffi.Handle) string` copies a NUL-terminated string from external memory.

### 12.5.2 Callbacks

**ffi.callback.make** — `ffi.Callback(f)` creates a function pointer that external code can call. `f` MUST be a top-level, non-generic function whose parameter and result types are passable.

**ffi.callback.lifetime** — A callback stays valid until the program exits. The number of callbacks that can be created is limited by an implementation-defined constant of at least 1024; creating more panics.

**ffi.callback.context** — When external code calls a callback from a thread that the Anlang runtime did not create, the runtime MUST run the callback as a task with `Normal` priority and return its result to the caller.

```go
type DB struct {
    h ffi.Handle
}

func Open(path string) (DB, error) {
    var h ffi.Handle
    rc := sqlite3_open(ffi.CString(path), h)        // inout: address passed for the call
    if rc != 0 {
        return DB{}, sqliteError(rc)
    }
    return DB{h}, nil
}
```

## 12.6 Not supported

**ffi.unsupported** — The following are not part of Anlang:

- parsing C headers — declarations are written by hand, usually by an AI from the library's documentation;
- C macros and `inline` functions — their logic is rewritten in Anlang;
- compiling C source — the project contains only Anlang code and ready object files;
- exporting Anlang code as a C library — callbacks cover the first stage.

**Note** — The toolchain contains no C compiler, external linker or SDK of other languages. The exception is code signing of iOS applications with Apple's tools (see [17-ui](17-ui.md)).

## Open issues


1. **Binding JNI function-table entries.** Deferred to implementation — step 65. `objc_msgSend` is declared per signature with `lib.Symbol`; the binding form for JNI table entries is fixed with the Android bridge.
