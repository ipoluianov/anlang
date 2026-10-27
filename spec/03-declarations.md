# 3. Packages and Declarations

This file specifies source files, packages, import paths, the profile clause, file selection and declarations, as a delta over the Go specification, sections "Declarations and scope", "Packages" and "Program initialization and execution". The project layout and `anlang.mod` are specified in [20-toolchain](20-toolchain.md).

## 3.1 Source files and packages

```
SourceFile    = PackageClause ";" [ ProfileClause ";" ] { ImportDecl ";" } { TopLevelDecl ";" } .   (replaces Go)
TopLevelDecl  = Declaration | FunctionDecl | MethodDecl | HandlerDecl | GpuFunctionDecl .            (replaces Go)
```

**decl.package.dir** — A package is the set of source files in one directory that are selected for the current target (§3.4). All of them MUST have the same package clause.

**decl.package.name** — The name in the package clause MUST equal the last element of the package's import path (§3.2), except for package `main`. Hyphens in the last element are dropped: a target package in directory `an/target/dos-com` is `package doscom` (**target.pkg.name**); such a directory cannot be imported (**decl.import.paths**).

**decl.package.main** — A program is built from a package named `main`, which MUST declare `func main()` with no parameters and no results, as in Go. Package `main` cannot be imported.

## 3.2 Import paths

**decl.import.module** — The *module name* is declared in `anlang.mod` ([20-toolchain](20-toolchain.md)). It MUST match `[a-z][a-z0-9_]*` and MUST NOT be `an`.

**decl.import.paths** — An import path MUST have one of two forms:

1. `an/<path>` — a package of the standard library, shipped with the toolchain ([18-stdlib](18-stdlib.md));
2. `<module>` or `<module>/<path>` — a package of the current project, where `<module>` is the module name and `<path>` is the directory relative to the project root.

`<path>` is a sequence of elements separated by `/`; each element MUST match `[a-z][a-z0-9_]*`. Any other import path, including a network address such as `github.com/acme/jsonparse`, MUST be rejected.

```go
import (
    "an/arch/i8086"
    "an/fmt"

    "weather/server"
)
```

**decl.import.name** — The package name used in code is the last element of the import path (by **decl.package.name**), unless an alias is given: `import f "an/fmt"`.

**decl.import.no-blank-dot** — Blank imports (`import _ "p"`) and dot imports (`import . "p"`) MUST be rejected.

**decl.import.used** — A source file MUST refer to every package it imports, as in Go.

**decl.import.no-cycle** — The import graph MUST be acyclic, as in Go.

**decl.import.internal** — An import path containing an element `internal` is importable only by packages rooted at the parent of that element, as in Go. This applies to both `an/...` and project paths.

**decl.import.unsafe** — The package `an/unsafe` MAY be imported only by a package whose profile set is exactly `{core}` (§3.3). Importing it is the marker of unsafe code; there are no `unsafe` blocks ([06-memory](06-memory.md)).

**decl.import.arch** — An architecture instruction package `an/arch/<a>` MAY be imported only in source files whose architecture suffix is `<a>` (§3.4).

## 3.3 Profile clause

```
ProfileClause = "profile" ProfileName { "," ProfileName } .   (new)
ProfileName   = identifier .
```

```go
package crc

profile core, app, ui
```

**decl.profile.names** — Each `ProfileName` MUST be one of `core`, `app`, `ui`, and MUST NOT be repeated in the clause.

**decl.profile.package** — The profile set is a property of the package. Every source file of a package that contains a profile clause MUST list the same set. A package in which no file contains a profile clause has the profile set `{app}`.

**decl.profile.semantics** — The meaning of profiles — permitted effects, the calling rule between profiles and the condition for declaring several profiles — is specified in [07-effects](07-effects.md).

## 3.4 File selection

The target of a build is chosen with `-target`, never in code ([13-targets](13-targets.md)). Code specific to a platform or architecture lives in files with suffixes, as in Go.

**decl.file.ext** — Source files have the extension `.an`. Files with other extensions in a package directory are not source files.

**decl.file.ignored** — Files whose names begin with `.` or `_` are ignored.

**decl.file.suffix** — Let *N* be the file name without `.an`. Suffixes are matched against the end of *N*, not by splitting at `_`, because architecture names may contain `_` (`x86_64`). Then:

1. If *N* ends with `_test`, the file is a *test file* and is included only when tests are built ([20-toolchain](20-toolchain.md)). The suffix is removed from *N* before the next steps.
2. If *N* ends with `_<platform>_<architecture>` (`_dos_i8086`, `_linux_x86_64`), the file is included only for targets with that platform and architecture.
3. Otherwise, if *N* ends with `_<architecture>` (`_i8086`, `_x86_64`) or `_<platform>` (`_linux`, `_dos`), the file is included only for targets with that architecture or platform.
4. Otherwise the file is included for every target.

**decl.file.names** — The names of platforms and architectures are those of the target descriptions known to the build: the standard library and the project ([13-targets](13-targets.md)). A platform name and an architecture name MUST NOT coincide.

**decl.file.profile** — The profile set of a package additionally limits the targets for which it can be built: a package MUST be compiled for a target only if the target is compatible with one of its profiles ([07-effects](07-effects.md)). A program that imports a package incompatible with the target MUST be rejected.

## 3.5 Constant declarations

**decl.const.go** — Constant declarations are as in Go, including `iota`, with the extensions below.

**decl.const.compile-time** — The initializer of a constant MAY be any expression that the compiler can evaluate at compile time, including calls of effect-free functions ([10-compile-time](10-compile-time.md)). The value is computed once at compile time.

**decl.const.types** — A constant MAY have any type except handle types ([02-types](02-types.md) §2.10), function types and types that contain them. In particular, arrays, structs, slices, maps and enum values are allowed. A constant of an interface type, or containing interface values, holds dynamic values that satisfy the same rule.

```go
const crcTable = makeCRCTable(0xEDB88320)    // [256]u32, computed at compile time

const Description = target.Desc{
    Arch:    arch.I8086,
    Formats: []target.Format{target.Flat{Origin: 0x100, MaxSize: 65280}},
}
```

**decl.const.immutable** — A constant of a composite type is immutable: it is not addressable, and no `inout` access to it can be obtained.

## 3.6 Variable declarations

**decl.var.local** — Variable declarations inside functions are as in Go, subject to the zero-value rules of [02-types](02-types.md) §2.12.

**decl.var.unused** — A variable declared in a function body MUST be used, as the Go compiler requires: a variable that is only assigned to is not used. Parameters, results, `inout` loop variables, an assignment to which writes through their access, and the blank identifier are exempt.

**decl.var.package-kinds** — A package-level variable MUST have one of these types:

1. a type of package `an/atomic`;
2. `sync.Mutex[T]` or `sync.RWMutex[T]` of package `an/sync`;
3. a *hardware mapping*: the type of a value produced by `unsafe.Map` ([06-memory](06-memory.md)), or a peripheral mapping generated from a device description ([14-embedded](14-embedded.md)). Hardware mappings are allowed only in packages whose profile set is exactly `{core}`.

Any other package-level variable, including one of an ordinary type or of a channel type, MUST be rejected.

```go
var ticks atomic.U32                                          // allowed
var stats sync.Mutex[Stats]                                   // allowed
var screen = unsafe.Map[[2000]Cell](i8086.Far(0xB800, 0))     // allowed in core
var count int                                                 // rejected
```

**decl.var.package-init** — The initializer of a package-level variable, if present, MUST be evaluable at compile time ([10-compile-time](10-compile-time.md)). Without an initializer, the variable holds the zero value of its type. There is no package initialization order to define.

**decl.var.global-effect** — Access to a package-level variable gives the accessing function the effect `Global` ([07-effects](07-effects.md)).

## 3.7 Function declarations

**decl.func.go** — Function and method declarations are as in Go, with the changes in this section and in §3.8–§3.10.

**decl.func.body** — Every function declaration MUST have either a body or a binding (§3.9). Go's body-less declarations for assembly implementations do not exist.

**decl.func.no-init** — A package-level function named `init` MUST be rejected. There are no import side effects.

**Note** — Rejecting `init` instead of treating it as an ordinary function prevents Go code ported without changes from silently losing its initialization.

**decl.func.contracts** — A function body MAY begin with contract clauses (**lex.context.contract-region**, [01-lexical](01-lexical.md)), specified in [08-contracts](08-contracts.md).

## 3.8 Parameters, receivers and inout

```
ParameterDecl = [ IdentifierList ] [ "inout" ] [ "..." ] Type .   (replaces Go)
Receiver      = Parameters .
```

**decl.param.inout** — A parameter marked `inout` gives the function temporary exclusive mutable access to the caller's value. The meaning of `inout` is specified in [06-memory](06-memory.md); the requirements on arguments are in [04-expressions](04-expressions.md).

**decl.param.inout-variadic** — `inout` and `...` MUST NOT be combined in one parameter.

**decl.param.inout-result** — Results MUST NOT be marked `inout`.

**decl.receiver.base** — A receiver has exactly one parameter. Its type MUST be a defined type `T` or an instantiation of a generic defined type, declared in the same package; `T` MUST NOT be an interface type. A receiver is either a value receiver or an `inout` receiver:

```go
func (r Range) Len() int            { return r.hi - r.lo }
func (s inout Server) Handle(r Request) { s.count++ }
```

**decl.receiver.method-set** — The method set of `T` contains methods with value receivers and with `inout` receivers. A method with an `inout` receiver modifies the caller's value and can be called only on an addressable operand ([04-expressions](04-expressions.md)).

**decl.param.func-types** — `inout` MAY appear in parameters of function types and function literals: `func(s inout Stats)`, iterators yielding `inout` access ([05-statements](05-statements.md)).

## 3.9 External function declarations

```
FunctionDecl = "func" FunctionName [ TypeParameters ] Signature [ FunctionBody | Binding ] .   (replaces Go)
Binding      = "=" Expression .                                                           (new)
```

```go
func sqlite3_open(path cstring, db inout ffi.Handle) i32 = lib.Symbol("sqlite3_open", ffi.Effects{File})
```

**decl.extern.binding** — A function declaration with a `Binding` and no body is an *external function*. The binding expression MUST be evaluable at compile time and MUST have a binding type of package `an/ffi` or `an/js` (`lib.Symbol(...)`, `ffi.At(...)`, `js.Import(...)`), as specified in [12-ffi](12-ffi.md).

**decl.extern.no-generic** — An external function MUST NOT have type parameters.

**decl.extern.no-method** — A method declaration MUST NOT have a binding.

**decl.extern.semantics** — Permitted parameter types, effects, calling restrictions and loading are specified in [12-ffi](12-ffi.md).

## 3.10 Handler and GPU function declarations

```
HandlerDecl     = ( "interrupt" | "trap" ) HandlerName Signature FunctionBody .   (new)
HandlerName     = identifier .
GpuFunctionDecl = "gpu" FunctionDecl .                                            (new)
```

```go
interrupt TIM2() {
    device.TIM2.ClearUpdateFlag()
    ticks.Add(1)
}

trap PageFault(frame inout TrapFrame, code PageFaultCode) { … }

gpu func Blur(img []simd.F32x4, out gpu.Out[simd.F32x4]) { … }
```

Recognition of `interrupt`, `trap` and `gpu` follows **lex.context.positions** ([01-lexical](01-lexical.md)).

**decl.handler.name** — A `HandlerName` names an interrupt or exception vector of the target; it is not declared in any scope and cannot be called or referenced. Valid names and signatures are defined by the target and device descriptions.

**decl.handler.no-generic** — Handler declarations have no type parameters and no receiver.

**decl.handler.semantics** — The semantics of `interrupt` handlers are specified in [14-embedded](14-embedded.md), those of `trap` handlers in [15-os](15-os.md).

**decl.gpu.body** — A `GpuFunctionDecl` MUST have a body; it MUST NOT be a method or an external function. Its semantics are specified in [16-gpu](16-gpu.md).

## 3.11 Visibility

**decl.visibility.go** — Exported identifiers and the scope rules are as in Go. Enum variants follow **type.enum.variant-export** ([02-types](02-types.md)).
