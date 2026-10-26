# 13. Targets

Area: `target`.

A build target combines three independent descriptions: an **architecture**, a **platform** and one or more **binary formats**. All three are declarative data written as Anlang constants, in the standard library or in the project. Adding an architecture does not require changing the compiler.

| Layer | Describes | Examples |
| --- | --- | --- |
| Architecture | instructions, registers, word size, pointer kinds, calling conventions, timing | `i8086`, `x86_64`, `armv6m`, `armv7m`, `avr`, `msp430`, `riscv32`, `riscv64`, `arm64`, `wasm32` |
| Platform | what the environment provides: effects, ABI, entry point, dynamic loading | `dos`, `bare`, `linux`, `windows`, `macos`, `ios`, `android`, `web` |
| Format | structure of the output file | `flat` (.COM), `mz` (.EXE), ELF, PE, Mach-O, wasm, BIN, Intel HEX, UF2 |

Shader targets for GPU functions (`spirv`, `wgsl`, `msl`) are defined in [16-gpu](16-gpu.md).

## 13.1 Target packages

**target.pkg.description** — A *target package* is a package that declares an exported constant `Description` of type `target.Desc` (package `an/target`). The constant is evaluated at compile time (see [10-compile-time](10-compile-time.md)); a misspelled field or a value of the wrong type is an ordinary compile error.

**target.pkg.name** — The *target name* is the last element of the target package's directory path. A directory name MAY contain hyphens, for example `an/target/dos-com` with package clause `package doscom`.

**target.pkg.location** — Standard targets are the packages under `an/target/`. A project MAY declare its own target packages anywhere in the module, for example `myprogram/boards/myboard`.

**target.pkg.pure** — A target package MUST NOT have effects other than those permitted in compile-time evaluation (**ct.eval.evaluable**). It MAY read files in its own directory, such as an SVD file (**ct.eval.read-input**).

```go
package doscom

import (
    "an/arch"
    "an/effect"
    "an/platform"
    "an/target"
)

const Description = target.Desc{
    Arch:     arch.I8086,
    Platform: platform.DOS,
    Word:     16,
    Pointers: target.Pointers{Near: 16, Far: 32},
    Formats:  []target.Format{target.Flat{Origin: 0x100, MaxSize: 65280}},
    Floats:   target.SoftFloat,
    Provides: []effect.Effect{effect.Console, effect.Video, effect.File, effect.Keyboard, effect.Clock, effect.StaticAlloc},
    Lacks:    []effect.Effect{effect.Network},
    OnPanic:  target.PrintAndExit,
}
```

## 13.2 The `target.Desc` schema

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `Arch` | `arch.Arch` | yes | architecture description (13.5) |
| `Platform` | `platform.Platform` | yes | platform description (13.4) |
| `Word` | `int` | no; default from `Arch` | width in bits of `int` and `uint`: 16, 32 or 64 |
| `Pointers` | `target.Pointers` | no; default from `Arch` | widths of pointer kinds (13.6) |
| `Formats` | `[]target.Format` | yes, at least one | output formats (13.9) |
| `Floats` | `target.FloatSupport` | no; default from `Arch` | `target.HardFloat`, `target.SoftFloat` or `target.NoFloat` (13.7) |
| `Memory` | `[]target.Region` | yes on `bare`; otherwise no | memory regions (13.8) |
| `Stack` | size in bytes | yes on `bare`; otherwise no | size of the stack of `main` (13.8) |
| `Device` | `target.Device` | no | peripherals, registers and interrupts, usually `svd.Load(file)` (see [14-embedded](14-embedded.md), **emb.device.schema**) |
| `Provides` | `[]Effect` | yes | environment effects available on the target (13.3) |
| `Lacks` | `[]Effect` | no | environment effects explicitly absent (13.3) |
| `OnPanic` | `target.PanicAction` | no; default from `Platform` | what a panic does (13.10) |
| `Clock` | frequency in Hz | no; required for `wcet.Within` | CPU clock frequency used with the architecture's timing model for WCET (see [14-embedded](14-embedded.md), **emb.wcet.model**) |

**target.desc.types** — Package `an/target` declares `Desc` and the types of its fields. `target.Pointers` is `arch.Pointers` and `target.PanicAction` is `platform.PanicAction` (13.4.1). A field left at its zero value takes its default: `Word` and `Pointers` from `Arch`, `Floats` (zero value `target.DefaultFloats`) from `Arch`, and `OnPanic` (zero value `target.DefaultPanic`) from `Platform`. Environment effects are the constants of package `an/effect`. The type of `Device` is `target.Device` (**emb.device.schema**). A target package that calls `svd.Load` is in profile `app`, since `an/svd` allocates; target packages are read by the toolchain only.

**target.desc.valid** — An implementation MUST reject a target description that violates this schema: a missing required field, `Word` outside {16, 32, 64}, a `Word` or `Pointers` value the architecture does not support, `Provides` and `Lacks` that intersect, overlapping memory regions, or a format that the platform does not support.

## 13.3 Effects provided by a target

**target.effects.environment** — The *environment effects* are the effects that only the target can make real: memory allocation (`Alloc`, `StaticAlloc`), input and output (`Console`, `File`, `Network`, `Video`, `Keyboard`, `ReadInput`, `WriteOutput`), execution (`Tasks`, `Interrupts`), hardware effects from the device description, nondeterminism (`Clock`, `Random`), `Privileged`, `GPU`, and the device capability effects of the `ui` profile (`Camera`, `Location`, …). This classification is normative; the meaning of each effect is given in [07-effects](07-effects.md) (**eff.base.set**), which delegates the target check to this section (**eff.target.check**).

**target.effects.provides** — Every environment effect reachable from the program's entry points in the build for a target MUST be listed in the target's `Provides`. A reachable environment effect that is not listed is a compile error that shows the call chain to its source.

**target.effects.lacks** — `Lacks` lists environment effects the target explicitly does not have. A reachable effect listed in `Lacks` is a compile error with a diagnostic that says the platform lacks it. `Lacks` does not change which programs compile; it changes the diagnostic and the answers of `anlang query`.

**target.effects.foreign** — `Foreign` is accepted on a target only if `Lacks` is empty (**ffi.effects.foreign-deny** in [12-ffi](12-ffi.md)).

**target.effects.computational** — Effects that are not environment effects — `Panic`, `Global`, `Block` and custom effects declared with `effect.Declare` — need not be listed. The environment effects underneath a custom effect are checked as usual.

## 13.4 Platforms

**target.platform.desc** — A platform is described by a constant of type `platform.Platform` in an Anlang package. The description states:

1. the environment effects the platform can provide;
2. whether it has dynamic loading (`ffi.Library`, see [12-ffi](12-ffi.md));
3. its system call mechanism, if any, and its C ABI variant;
4. its entry point and start-up sequence (see [19-runtime](19-runtime.md));
5. the binary formats it accepts;
6. the profiles allowed for the `main` package;
7. the default `OnPanic` action;
8. limits on background execution, for platforms that restrict it (see [17-ui](17-ui.md));
9. for a platform implemented in Anlang, its system call table (`abi.Table`, see [15-os](15-os.md)).

**target.platform.profile** — The profile of the `main` package MUST be one of the profiles the platform allows. Packages in stricter profiles are callable from any profile (see [07-effects](07-effects.md)).

| Platform | Dynamic loading | `main` profiles | Default `OnPanic` |
| --- | --- | --- | --- |
| `bare` | no | `core` | `Halt` |
| `dos` | no | `core` | `PrintAndExit` |
| `linux`, `windows`, `macos` | yes | `core`, `app`, `ui` | `PrintAndExit` |
| `ios`, `android` | yes | `ui` | `PrintAndExit` |
| `web` | no (WebAssembly imports) | `core`, `app`, `ui` | `PrintAndExit` |

### 13.4.1 The `platform.Platform` schema

**target.platform.schema** — A platform description is a constant of type `platform.Platform` of package `an/platform`. Its fields are:

| Field | Type | Meaning (item of **target.platform.desc**) |
| --- | --- | --- |
| `Name` | `string` | the platform name used in targets and file suffixes |
| `Provides` | `[]effect.Effect` | the environment effects the platform can provide (1); a target of the platform MUST NOT provide others |
| `Dynamic` | `bool` | whether the platform has dynamic loading (2) |
| `Rules` | `[]arch.Rule` | the platform operations `start`, `exit`, `write`, `read`, `vectors` and `vector` as instruction selection rules (13.5.7): the system call mechanism (3) and the start-up sequence (4) |
| `Imports` | `[]platform.Import` | the functions of the environment that the image imports, which the rules call (3) (**target.platform.imports**) |
| `Formats` | `[]string` | the kinds of the binary formats the platform accepts (5), such as `"flat"` |
| `Profiles` | `[]string` | the profiles allowed for the `main` package (6) |
| `OnPanic` | `platform.PanicAction` | the default panic action (7): `platform.PrintAndExit`, `platform.Reset` or `platform.Halt` |

The fields for the C ABI variant (3), background execution (8) and the system call table (9) are declared with the roadmap steps that introduce foreign functions on operating systems, the `ui` platforms and platforms implemented in Anlang.

**target.platform.entry** — The entry point of a program is code generated from the platform rules: the instructions of rule `start`, the reservation of the frame of the entry code, the clearing of the writable data of a target with memory regions (**target.memory.placement**), a call of `main`, and the instructions of rule `exit` with status 0. Rule `start` receives the initial stack pointer as `{sp}` and the lowest address of the stack as `{sb}`: on a target with memory regions, the end of the first non-`Exec` region and that address less `Stack`; on a target without memory regions and with a `Stack` size, the end and the start of the stack that follows the writable data (**target.memory.stack**). A panic calls rule `exit` with status 2 after the panic action has written its message (**rt.panic.actions**).

**target.platform.bare** — On platform `bare` (`platform.Bare`), the image starts with the vector table that rules `vectors` and `vector` write on `armv7m` (**emb.irq.vectors**): the initial stack pointer, the reset vector `{f}` and the vectors of the system exceptions, which receive the default handler `{d}`, then the vector of each interrupt of the device. Rule `start` disables the interrupts (`CPSID`); `interrupts.Enable` enables them ([14-embedded](14-embedded.md), **emb.init.start**). Standard output and standard error, and the exit status, reach a debugger through semihosting (`BKPT 0xAB`): rule `write` opens the console `:tt` for writing (standard output) or appending (standard error), writes and closes it; rule `read` opens it for reading (standard input), reads and closes it; rule `exit` calls `SYS_EXIT_EXTENDED` and then requests a system reset, which takes effect when no debugger stopped the program. A target of the platform provides `Console` through semihosting.

**target.platform.imports** — A `platform.Import` has the fields `Module` and `Name`, the names of an imported function, and `Params` and `Results`, the widths in bits of its parameters and results. An image of an architecture with structured control flow (**target.arch.structured**) imports the functions of `Imports` in their order; a rule calls import `i` as the function of index `i`.

**target.platform.web** — On platform `web` (`platform.Web`), the image is a WebAssembly module (**target.format.wasm**) that imports the functions `fd_write`, `proc_exit` and `fd_read` of module `wasi_snapshot_preview1` of the WebAssembly System Interface (WASI), in this order, and exports its entry code as `_start`. Standard input, standard output and standard error are the file descriptors 0, 1 and 2: rule `write` calls `fd_write`, rule `read` calls `fd_read` and rule `exit` calls `proc_exit`; the bytes 0 to 15 of the memory are the scratch area of these rules. The module runs in the runtimes of WASI, in the WebAssembly interpreter of the toolchain (**tool.emu.wasm**) and, through the page of **target.format.html**, in a browser. A target of the platform provides `Console`.

## 13.5 Architectures

### 13.5.1 Architecture descriptions

An *architecture description* consists of three components, all Anlang data:

1. **Machine model** — registers and register classes, word size, endianness, alignment, pointer kinds and address spaces, stack direction, calling conventions (the internal Anlang ABI and the C ABI), interrupt and trap entry, the names and handler signatures of the exceptions available to `trap` (see [15-os](15-os.md)), which instructions are privileged (effect `Privileged`, see [15-os](15-os.md)), and a timing model per instruction used for WCET (see [14-embedded](14-embedded.md)).
2. **Instruction selection rules** — declarative rules that map patterns of IR operations to instruction sequences, with operand constraints. Example: addition of two 16-bit values → `ADD r16, r/m16`.
3. **Formal instruction semantics** — the effect of each instruction on machine state, in the style of Sail.

**target.arch.value** — An architecture is identified by a constant of type `arch.Arch` in package `an/arch` (for example `arch.I8086`, `arch.ARMv7M`, `arch.X86_64`) or in a project package. The constant refers to the three components.

**target.arch.no-compiler-change** — An implementation MUST derive the code generator for an architecture from its instruction selection rules. Adding an architecture MUST NOT require changing the compiler.

**target.arch.rules-checked** — An implementation MUST check every instruction selection rule against the formal semantics: the instruction sequence must compute what the IR pattern computes, for all operand values permitted by the rule's constraints. The check uses the solver or exhaustive testing where the operand space allows it (see [22-compiler](22-compiler.md), [23-conformance](23-conformance.md)).

**target.arch.generated** — From the formal semantics an implementation MUST generate:

1. an assembler and disassembler for the architecture;
2. the instruction package `an/arch/<name>` (13.5.2);
3. an interpreter used by the built-in emulators (see [20-toolchain](20-toolchain.md)).

An architecture with structured control flow is the exception: its description has encodings and the assembler syntax but no semantics, and its interpreter is a built-in interpreter that is checked against an independent test suite (**target.arch.structured**).

**target.arch.structured** — An architecture has *structured control flow* when its `ABI.Structured.End` is not empty, as `wasm32`: a branch does not go to an address, it leaves an enclosing block, continuing after its end, or repeats an enclosing loop, and a module consists of functions instead of an image of instructions at addresses. The code generator places the code of each routine in the blocks and loops of the operations `block`, `loop` and `end` (**comp.gen.structured**); a label placeholder of a template (**target.arch.template**) becomes the number of blocks between the instruction and the target, which counts the blocks that the template itself opens with the mnemonics of `ABI.Structured.Open` and closes with that of `ABI.Structured.End`. The address of a function is its index; constant and writable data are placed from the origin of the first format. The instructions of such a description have no semantics: the interpreter of the architecture is a built-in interpreter, the WebAssembly interpreter for `wasm32` (**tool.emu.wasm**), whose oracle is the official test suite of the architecture (**conf.oracle.use**).

### 13.5.2 Instruction packages

**target.arch.instr-package** — The instruction package `an/arch/<name>` exposes the architecture's instructions and registers as ordinary Anlang functions and types, for example `i8086.Int(0x21, i8086.Regs{AX: 0x0900})`, `armv7m.Wfi(armv7m.Regs{})`, `x86_64.ReadCR2()`. Named operands are passed as a struct, since Anlang has no named arguments. The name of a function is its mnemonic with the first letter in upper case and the others in lower case, followed, for a mnemonic with several such forms, by the names of the operand kinds. A function of an instruction form whose operands are immediates, constants and fixed registers takes the immediates and a struct `Regs` with the whole registers of the register classes and the flags register of the ABI, and returns the registers after the instruction; instructions that change the program counter directly have no function. The immediates of a call MUST be constants. A call loads the registers from `Regs` except the reserved registers and the flags register of the ABI (**target.arch.abi**), executes the instruction, and returns `Regs` with the registers after it; the reserved registers keep their values from the argument. An architecture with far pointers has a function `Far(segment, offset)` that returns a value of type `unsafe.Far` (**mem.unsafe.api**).

**target.arch.instr-effects** — The effects of each instruction function are derived from the formal semantics: an instruction that changes interrupt state has `Interrupts`, a privileged instruction has `Privileged`, and so on. The compiler knows which registers each instruction clobbers.

**target.arch.import-suffix** — A file MAY import `an/arch/<name>` only if its name carries the architecture suffix `_<name>` (for example `print_i8086.an`), as defined in [03-declarations](03-declarations.md). Architecture description packages themselves are exempt.

**target.arch.asm** — The assembler for `asm` blocks (see [05-statements](05-statements.md)) is the one generated from the same description.

### 13.5.3 The `arch.Arch` schema

**target.arch.schema** — An architecture description is a constant of type `arch.Arch`. Its fields are:

| Field | Type | Meaning |
| --- | --- | --- |
| `Name` | `string` | the architecture name used in targets and file suffixes |
| `Word` | `int` | the default width of `int` in bits (13.6) |
| `Endian` | `arch.Endian` | `arch.Little` or `arch.Big`: the byte order of memory and of encoding units |
| `Unit` | `int` | the width in bits of the units in which encodings are stored |
| `AddressBits` | `int` | the width of a physical address |
| `Pointers` | `arch.Pointers` | the widths of the pointer kinds (13.6) |
| `Address` | `string` | the physical address of a memory operand with segment value `seg` and offset `off`, in the semantics notation; `"off"` on architectures without segments |
| `PC` | `string` | the program counter register |
| `PCRead` | `int` | if not 0, what the program counter reads as in the semantics of an instruction, and the base of its relative operands: the address of the instruction plus `PCRead` (**target.arch.execution**) |
| `Fetch` | `string` | the physical address of the next instruction, in the semantics notation |
| `Registers` | `[]arch.Register` | registers and named parts of registers |
| `Operands` | `[]arch.Operand` | the kinds of instruction operands |
| `Fragments` | `[]arch.Fragment` | parts of encodings with alternatives |
| `Prefixes` | `[]arch.Prefix` | instruction prefixes |
| `Instructions` | `[]arch.Instruction` | instruction forms |
| `Semantics` | `string` | functions shared by the semantics of the instructions, in the semantics notation |
| `ABI` | `arch.ABI` | the machine model of the code generator: the frame register, the argument area and the alignment (13.5.7) |
| `Rules` | `[]arch.Rule` | the instruction selection rules (13.5.7) |

- A **register** has a `Name`, a `Width` in bits and optionally a `Class`, a `Parent` with an `Offset`, a `Fixed` mask of bits that read as 1 and a `Zero` mask of bits that read as 0, such as bit 0 of a program counter of halfword instructions. A part of a register (a flag, a low byte) names its parent and its bit offset; writing a part changes the parent. The registers of a class are numbered in the order of `Registers`; an operand of the class is encoded by the number. Registers of class `"prefix"` hold the state that prefixes set; they are cleared after each instruction.
- An **operand kind** is exactly one of: a fixed `Register`; a register of a `Class`; a `Memory` operand of a width in bits (`-1` for an operand whose address only is used), encoded by a `Fragment`, which together with a `Class` gives an operand that is a register or memory; an immediate of `Imm` bits, optionally `Signed` (sign-extended to `Width`) and `Relative` (a displacement from the end of the instruction, or from the address of the instruction plus `PCRead`, which the semantics sees as the target address); or a `Constant` with a `Value`, which appears only in the assembler syntax. A register operand with `Registers` names one of the first `Registers` registers of its class, in fewer bits. An immediate with `Scale` has that many zero bits below its field; with `Transform`, an expression over the field `v` that is its own inverse, its bits are the transformed field; with `Syntax`, an expression over the field `v` of at most 16 bits, the assembler writes that number, such as an immediate expanded from a compact encoding, and the semantics see the field. A `List` of a `Class` is a register list: a mask whose bit `i` selects register number `i`.
- A **fragment** has `Alternatives`, each an `Encoding` whose fields are fields of the fragment, and either a `Register` field that holds the register number of the operand, or an `Address` and a `Segment` in the semantics notation using the fields, with the `Cycles` that computing the address adds.
- A **prefix** has an `Encoding` and `Semantics` that assign the prefix state.
- An **instruction form** has a `Mnemonic`, operand kinds `Operands` in assembler order, an `Encoding`, `Semantics`, the `Cycles` of its register form and its `Effects` beyond registers and memory (**target.arch.instr-effects**). An instruction has at most four operands; in the semantics they are named `a`, `b`, `c` and `d`.

**target.arch.check** — An implementation MUST reject a description in which a name is undefined, a register part lies outside its parent, bits of a register are both `Fixed` and `Zero`, the registers of a class differ in width, an operand kind is not exactly one kind, an operand kind with `Syntax` is not an immediate of at most 16 bits without `Signed`, `Relative`, `Scale` and `Transform`, an instruction form has more than four operands, an encoding does not encode an operand that needs it or encodes it in a field of the wrong width, a pattern is not a whole number of units, or the semantics violate **target.arch.widths**.

### 13.5.4 Encoding notation

**target.arch.encoding** — An encoding is a sequence of items separated by spaces:

| Item | Meaning |
| --- | --- |
| `HH` | a byte in hexadecimal, 8 fixed bits |
| `%0101` | fixed bits in binary |
| `name:width` | a field: an operand `a`, `b`, `c` or `d` (a register number, an immediate or a register list), or a field of a fragment |
| `name[hi:lo]`, `name[i]` | bits `hi` to `lo`, or bit `i`, of an operand; the bit ranges of an operand together cover its bits, except that a register list may leave out registers, which its encoding cannot list |
| `<frag op field=value …>` | fragment `frag` encoding operand `op`; each `field=value` binds a field of the fragment to an operand or to a constant |

Bits fill each unit of `Unit` bits from its most significant bit; the units are stored in the byte order `Endian`. A field that consists of whole units and starts at a unit boundary is stored in the byte order `Endian`, like an immediate in memory. A field may appear twice in an encoding; an encoding matches only if both hold the same value. With units wider than a byte, a fragment may cover part of a unit; the instruction forms that use it are whole units.

**target.arch.specific** — When several instruction forms match an encoding, the most specific one applies: the one with the most fixed bits, a constant binding counting as one. A description in which two forms with the same number of fixed bits match one encoding is ambiguous and MUST be rejected for that encoding.

```go
{Mnemonic: "ADD", Operands: []string{"r/m16", "imm16"}, Encoding: "81 <rm a reg=0> b:16", Semantics: "a = add(a, b, 0);", Cycles: 4},
{Mnemonic: "INC", Operands: []string{"r16"}, Encoding: "%01000 a:3", Semantics: "a = inc(a);", Cycles: 2},
```

### 13.5.5 Semantics notation

**target.arch.semantics** — The semantics of an instruction is a sequence of statements over bit vectors, in the style of Sail:

```
Stmt   = "let" name "=" Expr ";" | Target "=" Expr ";" | Call ";"
       | "if" Expr Block [ "else" ( Block | IfStmt ) ] | "while" Expr Block
       | "break" ";" | "return" [ Expr ] ";" .
Block  = "{" { Stmt } "}" .
Target = name | "mem8" "[" Expr "]" | "mem16" "[" Expr "]" | "mem32" "[" Expr "]" .
Def    = "fn" name "(" [ name { "," name } ] ")" Block .
```

A name denotes a local variable, a parameter, a register or an operand (`a`, `b`, `c`); assigning a register part changes its parent; assigning an operand writes its register or its memory. `memN[addr]` is the N-bit value at physical address `addr` in the byte order `Endian`. Expressions use the operators of Go with Go's precedence: `|| && == != < <= > >= + - | ^ * & << >>`, and `>>>` for the arithmetic shift right; comparisons are unsigned. `x[i]` is bit `i` and `x[hi:lo]` the bits `hi` to `lo`. The built-in functions are `zx`, `sx` and `tr` (zero-extend, sign-extend and truncate to a constant width), `width`, `msb`, `lts`, `les`, `gts`, `ges` (signed comparisons), `divu`, `remu`, `divs`, `rems`, `in8`, `in16`, `out8`, `out16` (input and output ports), `halt`, `ea` (the offset of a memory operand) and `segof` (its segment value). The `Semantics` field of `arch.Arch` defines further functions with `fn`; they are not recursive.

**target.arch.widths** — Every value has a width from 1 to 64 bits. The operands of an arithmetic, bitwise or comparison operator have the same width; a literal, or a value computed from literals and `width`, is unsized and takes the width of the other operand. A comparison, `!`, `&&`, `||` and a bit have width 1; conditions have width 1. Functions defined with `fn` have no declared widths: each call is checked with the widths of its arguments, and all values a function returns have one width. An assignment keeps the width of its target. Arithmetic is modulo 2ⁿ of the width.

**target.arch.execution** — An instruction executes with `IP` (or the architecture's program counter) already advanced past it; a relative operand denotes the target address. With `PCRead`, the program counter reads, in the semantics of the instruction, as an operand and in the addresses of its memory operands, as the address of the instruction plus `PCRead`, and relative operands are relative to that address. A memory operand reads and writes at the physical address `Address` computed from the `Segment` and `Address` of its fragment alternative. The bits of a register in its `Zero` mask read as 0 whatever is written. An access to a physical address where the machine has no memory fails the instruction; an interpreter of a target has the memory of the regions of the target (**target.memory.region**).

### 13.5.6 Assembler syntax

**target.arch.asm-syntax** — The assembler and the disassembler generated from a description (**target.arch.generated**) use this syntax for one instruction: the names of its prefixes, the mnemonic, and the operands separated by commas. A register operand is the register name; an immediate is a number in decimal or with the prefix `0x`; a relative operand is the target address; a far pointer is `segment:offset`; a memory operand is `byte [address]`, `word [address]`, `dword [address]` or `qword [address]` by its width, or `[address]` for an operand whose address only is used, where the address is a sum of registers and a displacement that one of the alternatives of its fragment can encode; a register masked by a constant in an address, such as a program counter aligned down, is written as the register, and a displacement written as `-0` selects an alternative that subtracts its field. A register list is the registers in braces in the order of their numbers, such as `{R4, R5, LR}`. An immediate with `Syntax` is the number of its syntax. Names are not case-sensitive. Of several encodings of one text, the assembler chooses the shortest, and of equally short ones the instruction form and the fragment alternative described first, preferring an alternative with the registers of the address in the order written.

**target.arch.round-trip** — For every encoding that the description defines, assembling the disassembled text MUST give an encoding whose disassembled text is the same.

### 13.5.7 Instruction selection rules

**target.arch.rule** — An instruction selection rule is a value of type `arch.Rule` with the fields `Op` (a machine operation), `Width` (the width in bits of its operands, 0 for an operation without one), `From` (for `ext.s` and `ext.u`, the width of the operand) and `Code` (an instruction template). The code generator expresses every IR instruction as a sequence of machine operations and replaces each operation by the template of a rule for it.

**target.arch.ops** — The machine operations are the following. `r` is the result; `x` and `y` are operands; `p` and `q` are addresses (values of the default pointer width); `k` and `n` are constants: an offset and a size in bytes; `L` and `f` are labels. A value of width `Width` is an integer modulo 2^Width; signed operations read it in two's complement.

| `Op` | Operands | Meaning |
| --- | --- | --- |
| `move` | `r`, `x` | `r = x`; `Width` is 8, 16 or the word width |
| `add`, `sub`, `mul`, `and`, `or`, `xor` | `r`, `x`, `y` | the operation modulo 2^Width |
| `not`, `neg` | `r`, `x` | bitwise complement; negation modulo 2^Width |
| `add.s`, `sub.s`, `mul.s`, `neg.s` | `r`, `x`, `y` | as `add` … `neg`, but if the exact result read as a signed integer does not fit in `Width` bits, control goes to the trap label and `r` is not written |
| `add.u`, `sub.u`, `mul.u` | `r`, `x`, `y` | the same for unsigned integers |
| `div.s`, `rem.s` | `r`, `x`, `y` | the quotient truncated toward zero and the remainder of signed integers; `y` ≠ 0, and `x` is not the minimum when `y` is −1 |
| `div.u`, `rem.u` | `r`, `x`, `y` | the quotient and remainder of unsigned integers; `y` ≠ 0 |
| `shl`, `shr.u`, `shr.s` | `r`, `x`, `y` | shifts of `x` by the count `y`, an unsigned value of the word width; a count of `Width` or more gives 0, or for `shr.s` the sign of `x` in every bit |
| `eq`, `ne`, `lt.s`, `lt.u`, `le.s`, `le.u` | `r`, `x`, `y` | `r` is an 8-bit 1 if the relation holds, 0 otherwise |
| `ext.s`, `ext.u` | `r`, `x` | `x` of width `From` sign- or zero-extended to `r` of width `Width` |
| `addr` | `r`, `x` | `r` is the address of the slot of `x` |
| `load` | `r`, `p`, `k` | `r` is the value at address `p + k`; `Width` is 8 or the word width |
| `store` | `p`, `k`, `x` | writes `x` at address `p + k` |
| `load.far`, `store.far` | as `load`, `store` | the same with `p` a far address (`unsafe.Far`): the memory of a hardware mapping (**mem.unsafe.map**) |
| `copy` | `p`, `q`, `n` | copies `n` bytes from address `q` to address `p`; the two do not overlap |
| `zero` | `p`, `n` | sets `n` bytes at address `p` to 0 |
| `jump` | `L` | continues at `L` |
| `branch` | `x`, `L` | continues at `L` if the 8-bit `x` is not 0, otherwise with the next operation |
| `call` | `f` | calls the function at label `f` |
| `callind` | `x` | calls the function whose address is `x` |
| `enter` | `n` | the start of a function: makes the frame base such that the stack pointer of the caller at the call was the frame base plus `ABI.Params`, and reserves `n` bytes below the frame base |
| `leave` | — | returns from a function to its caller, restoring the caller's frame base and stack pointer |
| `regin`, `regout` | `x` or `r`, `reg` | moves `x` into register `reg`, or register `reg` into `r`, for calls of the instruction package and `asm` statements; a register for which no template assembles is not moved |
| `flagsout` | `r` | stores the flags register `ABI.Flags` into `r` |
| `start` | — | platform rule: instructions before the call of `main` |
| `exit` | `x` | platform rule: ends the program with the status `x` of the word width |
| `write` | `x`, `p`, `y` | platform rule: writes `y` bytes at address `p` to the stream `x`: 1 for standard output, 2 for standard error |
| `read` | `r`, `x`, `p`, `y` | platform rule: reads at most `y` bytes of the stream `x`, 0 for standard input, to address `p`; `r`, of the word width, is the number of bytes read, 0 at the end of the stream |
| `block`, `loop`, `end` | — | on an architecture with structured control flow: the start of a block whose end a branch reaches, the start of a loop that a branch repeats, and the end of either (**target.arch.structured**) |
| `vectors` | `{sp}`, `{f}`, `{d}` | platform rule: the start of the vector table, with the initial stack pointer, the reset vector `f` and the default handler `d` |
| `vector` | `f` | platform rule: the vector of the next interrupt of the device, with the handler `f` |
| `enter.handler`, `leave.handler` | `n` | as `enter` and `leave`, for an interrupt handler: they also save and restore the registers that the processor does not save on an exception, and `leave.handler` returns from the exception |
| `irq.save` | `r` | stores the state of the interrupts into `r` and disables them |
| `irq.restore` | `x` | restores the state of the interrupts from `x`, a value of `irq.save` |

**target.arch.template** — An instruction template is assembler text in the syntax of **target.arch.asm-syntax**, one instruction per line, with placeholders in braces:

1. `{x}`, `{y}`, `{r}`, `{p}`, `{q}` — the operand. An operand in a slot is a memory operand `<size> [<frame register><offset>]` with the size of `Width` (`byte`, `word`, `dword`, `qword`), where the frame register is `ABI.Frame`; a constant operand, including the address of a function or of constant data, is a number.
2. `{x.i}` — part `i` of the operand, a value of the word width: part 0 is the least significant. Templates of operations wider than a word name the parts.
3. `{k}`, `{n}` — the constant offset or size; `{L}`, `{f}` — the label; `{trap}` — the trap label of a checked operation.
4. `@name` — a label local to one use of the template; a line `@name:` defines it.
5. `{reg}` — the register of `regin` and `regout`.
6. `{x.h0}`, `{x.h1}` — the low and the high 16 bits of a constant operand of the word width, or of the address of a label; of an operand in a slot, the memory operand, so that the template does not assemble.
7. `{x.off}` — the offset of the slot of `x` from the frame register, as a number.

Braces around a register name, such as `{LR}`, are a register list, not a placeholder. A line `.word v`, where `v` is a sum of numbers and labels, stores `v` in a word of the word width, such as an entry of a vector table.

**target.arch.select** — For an operation with given `Op`, `Width` and `From`, the code generator uses the first rule of the architecture, then of the platform, with the same three values whose template assembles with the operands substituted. If no template assembles because an operand is a constant, the constant is first moved into a slot. If there is no rule for `mul`, `mul.s`, `mul.u`, `div.s`, `div.u`, `rem.s`, `rem.u`, `shl`, `shr.s` or `shr.u` of a width greater than the word width, the operation is a call of the function of the runtime package whose name is the operation name in camel case followed by the width, such as `DivU32` for `div.u` of 32 bits ([22-compiler](22-compiler.md), 22.11). A missing rule for any other operation the program needs is a build error that names the operation and the architecture.

**target.arch.abi** — `arch.ABI` has the fields `Frame` (the register that holds the frame base; slots are addressed relative to it), `FrameBottom` (the frame register holds the bottom of the frame instead: the stack pointer after `enter`, so that slot offsets are not negative), `Params` (the offset from the frame base of the argument area of the caller), `Align` (the alignment in bytes of slots and of the argument area), `Reserved` (the registers whose values the generated code relies on, which calls of the instruction package and `asm` statements do not load), `Flags` (the flags register, which they store but do not load), `InterruptFrame` (the number of bytes that the processor and `enter.handler` push on an interrupt before the frame of the handler, for the stack analysis, **emb.stack.compute**) and `Structured` (for structured control flow, **target.arch.structured**, the mnemonics `Open` of the instructions that open a block and `End` of the one that closes it). The stack grows toward lower addresses. The layout of frames and argument areas is specified in [22-compiler](22-compiler.md), 22.11.

**target.arch.rules-test** — Until the solver handles bit vectors (roadmap step 22), the check of **target.arch.rules-checked** executes each rule of an operation other than `jump`, `branch`, `call`, `callind`, `enter`, `leave`, `regin`, `regout`, `flagsout`, `enter.handler`, `leave.handler`, `irq.save`, `irq.restore`, `block`, `loop`, `end` and the platform operations in the interpreter generated from the description: exhaustively for operands of 8 bits, and for wider operands on the boundary values (0, 1, the maxima and minima and their neighbors) and on random values, comparing the result, and whether control reached the trap label, with the meaning in **target.arch.ops**. On an architecture with structured control flow, the template runs in its built-in interpreter, in a block that the trap label leaves. The other rules are checked by running the reference programs (**conf.backend.ir**).

## 13.6 Integer widths and pointer kinds

**target.word.int** — `int` and `uint` have the width given by `Word`.

**target.word.usize** — `usize` has the width of a data address of the default pointer kind: the flat pointer width, or the near pointer width on segmented architectures.

**target.pointers.kinds** — `target.Pointers` has the fields `Flat`, `Near`, `Far` and `Program`, each a width in bits; zero means the kind does not exist. Pointer kinds appear only inside `unsafe` and architecture packages (see [06-memory](06-memory.md)).

| Architecture | `int` | `usize` | Pointer kinds |
| --- | --- | --- | --- |
| `i8086` | 16 | 16 | `Near` 16, `Far` 32 (segment:offset) |
| `avr` | 16 | 16 | `Near` 16 (RAM), `Program` 16 or 24 (flash, read by special instructions) |
| `msp430` | 16 | 16 | `Flat` 16 |
| `armv6m`, `armv7m`, `riscv32`, `wasm32` | 32 | 32 | `Flat` 32 |
| `x86_64`, `arm64`, `riscv64` | 64 | 64 | `Flat` 64 |

**Note** — Portable code uses sized types (`i8` … `i64`, `u8` … `u64`) by default.

## 13.7 Floating point

**target.floats.hard** — With `HardFloat`, `f32` and `f64` operations use the processor's floating-point instructions.

**target.floats.soft** — With `SoftFloat`, they use software emulation linked from the standard library, with the same IEEE 754 results.

**target.floats.none** — With `NoFloat`, a function reachable in the build that has a value or operation of type `f32`, `f64` or a floating-point `simd` type is a compile error.

## 13.8 Memory regions and stack

**target.memory.region** — `target.Region` has the fields `Name string`, `At` (start address), `Size` (bytes) and `Exec bool`. A region with `Exec: true` holds code and read-only data and is not written at run time; a region with `Exec: false` holds writable data, zero-initialized data and stacks.

**target.memory.placement** — Code and constants (**ct.const.placement**) are placed in the first `Exec` region; initial values of writable data are stored there and copied to RAM at start-up (see [19-runtime](19-runtime.md)). Writable data and stacks are placed in the first non-`Exec` region. Until writable data with initial values exists, writable data starts as zero bytes, which the entry code writes (**target.platform.entry**).

**target.memory.overflow** — If the contents assigned to any region exceed its size, the build fails. The diagnostic MUST show the size of the region, the total used, and a breakdown per package of code, constants, static data and stack reserve.

**target.memory.stack** — `Stack` is the stack size of `main`. Stack analysis compares it with the computed maximum depth (see [14-embedded](14-embedded.md)). On a target without memory regions, the stack of `main` follows the writable data.

```go
const Description = target.Desc{
    Arch:     arch.ARMv7M,
    Platform: platform.Bare,
    Word:     32,
    Memory: []target.Region{
        {Name: "flash", At: 0x0800_0000, Size: 64 * KiB, Exec: true},
        {Name: "ram",   At: 0x2000_0000, Size: 20 * KiB},
    },
    Stack:    8 * KiB,
    Formats:  []target.Format{target.ELF, target.BIN, target.HEX},
    Device:   svd.Load("STM32F103.svd"),
    Floats:   target.SoftFloat,
    Provides: []Effect{Console, Gpio, Uart, Timers, Interrupts, StaticAlloc, Tasks},
    Lacks:    []Effect{File},
    OnPanic:  target.Reset,
}
```

## 13.9 Binary formats

**target.format.interface** — A binary format is a value implementing interface `target.Format`. The linker hands it the linked image — segments with addresses and contents, the entry point and symbols — and the format produces the output file. Formats are Anlang code like architectures; adding one does not change the compiler.

**target.format.kind** — Until compile-time evaluation can call methods on the linked image (roadmap step 26), interface `target.Format` has the method `Kind() string`, and the toolchain writes the format of each kind: `"flat"` for `target.Flat`, `"boot"` for `target.BootSector`, `"elf"` for `target.ELF`, `"bin"` for `target.BIN`, `"hex"` for `target.HEX`, `"wasm"` for `target.Wasm` and `"html"` for `target.Page`. `target.ELF`, `target.BIN`, `target.HEX` and `target.Page` are constants of the types `target.ELFFormat`, `target.BINFormat`, `target.HEXFormat` and `target.HTML`.

**target.format.all** — A build produces one output file for each format listed in `Formats`, in `out/<target>/`.

| Format | Value | Requirements |
| --- | --- | --- |
| flat (.COM) | `target.Flat{Origin, MaxSize}` | raw image without header or relocations, loaded at `Origin`; execution starts at the first byte; the image MUST NOT exceed `MaxSize` bytes; for DOS `.COM`, `Origin` is `0x100` and `MaxSize` is `65280` |
| boot sector | `target.BootSector` | raw image loaded at `0x7C00`, at most 510 bytes, padded with zero bytes to 510 bytes and followed by the boot signature `0x55 0xAA`; the file is exactly 512 bytes; execution starts at the first byte |
| MZ (.EXE) | `target.MZ` | DOS executable with MZ header and relocation table |
| ELF | `target.ELF` | executable on Linux; on `bare`, load segments at region addresses for debuggers and flashing tools: the image at the start of the `Exec` region, with the entry point at the reset vector |
| PE | `target.PE` | Windows executable |
| Mach-O | `target.MachO` | macOS and iOS executable |
| wasm | `target.Wasm{Origin}` | WebAssembly module (**target.format.wasm**) |
| HTML page | `target.Page` | HTML page that runs the WebAssembly module in a browser (**target.format.html**) |
| BIN | `target.BIN` | raw image of the `Exec` region contents, starting at the region's start address |
| Intel HEX | `target.HEX` | Intel HEX records of the same contents as BIN |
| UF2 | `target.UF2{FamilyID}` | UF2 blocks for USB mass-storage bootloaders |

**target.format.size** — Exceeding a format's size limit is a build error with the same per-package breakdown as **target.memory.overflow**.

**target.format.wasm** — A `target.Wasm` module of a structured image has: type 0, the type `[] → []` of the functions of the image, and the types of the imports; the imports of the platform (**target.platform.imports**); the functions, whose index is their address; a table of type `funcref` that holds every function at its index, for indirect calls; one memory, exported as `memory`, large enough for the data and the stack; a mutable `i32` global for each register of the architecture, starting as 0; the export `_start` of the entry code; and one data segment with the constant data and the initial writable data at `Origin`. The module is valid WebAssembly 2.0.

**target.format.html** — A `target.Page` file is an HTML page that holds the WebAssembly module of the first `target.Wasm` format of the target and runs it in a browser with the functions of WASI of **target.platform.web**: standard output and standard error appear in the console of the page, and standard input is what the user types into its input field, each character as it is typed and Enter as a line feed. A read waits for input with JavaScript Promise Integration; in a browser without it, a read sees only the input given in the query parameter `input` of the page. The page needs no server: it runs from a file.

**target.format.macos-sign** — A Mach-O executable for macOS on `arm64` MUST carry an ad-hoc code signature, because the operating system does not run unsigned arm64 code. Signing iOS applications for devices is described in [17-ui](17-ui.md).

## 13.10 Panic action

**target.panic.action** — `target.PanicAction` has the values `target.PrintAndExit`, `target.Reset` and `target.Halt`. Their behavior — message, stack trace, reset, halt — is defined in [19-runtime](19-runtime.md).

## 13.11 Selecting targets

**target.select.flag** — The target is chosen at build time with `-target`, never in source code. A package header contains only `profile` (see [03-declarations](03-declarations.md)).

**target.select.resolve** — A target argument is resolved as follows:

1. If it contains `/`, it is the import path of a target package.
2. Otherwise it is a target name. The implementation searches the module for target packages with that name (**target.pkg.name**). If exactly one is found, it is used; if more than one, the build fails.
3. If none is found in the module, `an/target/<name>` is used; if it does not exist, the build fails.

**target.select.default** — Without `-target`, the targets listed in `anlang.mod` are built (see [20-toolchain](20-toolchain.md)).

**target.select.files** — For each target, source files are selected by their platform and architecture suffixes as defined in [03-declarations](03-declarations.md).

## 13.12 Multi-target builds

**target.multi.flag** — `anlang build -target a,b,c` builds all listed targets in one run.

**target.multi.equivalent** — The result for each target MUST be identical to building that target alone. An implementation MAY share work between targets — for example type, effect and contract checking of packages whose selected file sets are the same — but MUST NOT let one target's result depend on another's.

**target.multi.output** — The outputs of target `t` are placed in `out/<t>/`.

## 13.13 Standard targets

**target.std.name** — Target names of the standard library follow these rules:

1. A target for an operating system is named `<platform>-<architecture>`: `linux-x86_64`, `windows-x86_64`.
2. DOS and BIOS targets are named after the kind of output: `dos-com`, `dos-exe`, `bios-boot`.
3. Other boot targets are named `<boot environment>-<architecture>`: `multiboot-x86_64`, `uefi-arm64`, `devicetree-riscv64`.
4. A microcontroller target is named after the chip, in lower case without separators: `stm32f103c8`, `rp2040`.
5. A mobile target is named after the platform and builds all its architectures into one package: `android`, `ios`, `ios-sim`.
6. The browser target is `web`.

Architecture names inside target names keep their own spelling (`x86_64`).

**target.std.required** — The implementation MUST provide the following target packages under `an/target/`. The step column is the roadmap step that introduces the target ([24-roadmap](24-roadmap.md)); a target is required from the release that completes that step.

| Target | Architecture | Platform | Formats | Step |
| --- | --- | --- | --- | --- |
| `dos-com` | `i8086` | `dos` | `Flat{Origin: 0x100, MaxSize: 65280}` | 13 |
| `stm32f103c8` | `armv7m` (Cortex-M3) | `bare` | ELF, BIN, Intel HEX | 15–16 |
| `web` | `wasm32` | `web` | wasm, HTML page | 18 |
| `linux-x86_64` | `x86_64` | `linux` | ELF | 26 |
| `linux-arm64` | `arm64` | `linux` | ELF | 27 |
| `macos-arm64` | `arm64` | `macos` | Mach-O | 27 |
| `windows-x86_64` | `x86_64` | `windows` | PE | 28 |
| `atmega328p` | `avr` | `bare` | ELF, Intel HEX | 51 |
| `dos-exe` | `i8086` | `dos` | MZ | 52 |
| `rp2040` | `armv6m` (Cortex-M0+) | `bare` | ELF, UF2 | 52 |
| `esp32c3` | `riscv32` | `bare` | ELF, BIN | 52 |
| `msp430g2553` | `msp430` | `bare` | ELF, Intel HEX | 52 |
| `bios-boot` | `i8086` | BIOS boot sector | `BootSector` | 55 |
| `multiboot-x86_64` | `x86_64` | Multiboot | ELF | 55 |
| `uefi-x86_64`, `uefi-arm64`, `uefi-riscv64` | as named | UEFI | PE (UEFI application) | 55 |
| `devicetree-arm64`, `devicetree-riscv64` | as named | Device Tree | ELF, BIN | 55 |
| `android` | `arm64`, `x86_64` | `android` | APK, AAB | 65 |
| `ios`, `ios-sim` | `arm64`; `arm64`, `x86_64` | `ios` | `.ipa`; `.app` | 66 |

**Note** — The `rp2040` has two cores; until multi-core microcontrollers are specified ([14-embedded](14-embedded.md)), the target runs on core 0 only. A project adds other chips and boards as its own target packages (**target.pkg.location**).
