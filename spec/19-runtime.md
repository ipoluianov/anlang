# 19. Runtime

The runtime is the code that the toolchain links into a program in addition to the program's own code: startup, memory management, task scheduling, panic handling, loading of external libraries and, in the `ui` profile, the interface subsystem.

## 19.1 General requirements

**rt.lang.anlang** — The runtime MUST be written in Anlang, in the `core` profile, and shipped as source code with the standard library (see [18-stdlib](18-stdlib.md)).

**rt.lang.not-importable** — User code MUST NOT import the runtime package `an/runtime`. Its facilities are reached only through the language and the standard library.

**rt.no-gc** — The runtime MUST NOT contain a garbage collector. Memory is managed by value semantics, copy-on-write and arenas (see [06-memory](06-memory.md)).

**rt.no-rtti** — The runtime MUST NOT contain run-time type information or run-time reflection. Reflection exists only at compile time (see [10-compile-time](10-compile-time.md)). Debug tables are emitted in a separate file and are not part of the program image (see [20-toolchain](20-toolchain.md), **tool.debug.side-table**).

**rt.link.piecewise** — The runtime MUST be linked piecewise: a runtime component MUST be present in the output only if the program uses it.

- A program that starts no tasks MUST NOT contain a task scheduler.
- A DOS program that starts no tasks MUST contain only the startup code and the panic handler from the runtime.

**rt.cow.inline** — Reference counting for copy-on-write values MUST be implemented as code emitted at the points of use, not as a separate runtime service. The counter MUST be atomic only for values passed between tasks (see [22-compiler](22-compiler.md)).

## 19.2 Differences from the Go runtime

*Informative.*

| Go runtime component | Anlang |
| --- | --- |
| garbage collector | none |
| run-time reflection | none; compile time only (`meta`) |
| run-time type information | none; debug tables in a separate file only |
| goroutine scheduler | one scheduler per profile |
| stacks grown by copying | none: stack size is computed statically, otherwise a reserve with a guard page |
| reference counting | inline copy-on-write code, not a separate service |

## 19.3 Composition by profile

**rt.profile.composition** — The runtime of a program in a given profile MUST consist of the components in the following table, subject to **rt.link.piecewise**.

| Component | `core` | `app` | `ui` |
| --- | --- | --- | --- |
| startup | from the target description: reset vector, .COM entry point, Multiboot entry | OS entry point | platform entry point |
| memory | static only, fixed-size pools | general-purpose allocator and arenas | as `app` |
| tasks | static scheduler with priorities | M:N scheduler over OS threads with asynchronous I/O | as `app`, plus the interface main thread |
| panic | as the target's `OnPanic`: reset, halt, exit to DOS | message with stack trace, then process exit | as `app` |
| external functions | `ffi.At`, static linking | plus loading of dynamic libraries at startup | as `app` |
| interface | — | — | renderer, layout, accessibility, text input |

The behavior of each component is specified elsewhere: memory in [06-memory](06-memory.md), tasks in [09-concurrency](09-concurrency.md), external functions in [12-ffi](12-ffi.md), the startup sequence and the choice of `OnPanic` in [13-targets](13-targets.md), the panic actions in §19.4, the interface in [17-ui](17-ui.md).

## 19.4 Panics

**rt.panic.core** — In the `core` profile, a panic MUST perform the action given by the target description's `OnPanic` field (see [13-targets](13-targets.md)).

**rt.panic.actions** — The values of `target.PanicAction` behave as follows:

1. `PrintAndExit` — write the panic reason and a stack trace (**rt.panic.trace**) to the platform's console, then return control to the environment with a non-zero status: process exit on an operating system, return to DOS on `dos` (`int 21h`, function `4Ch`).
2. `Reset` — reset the device through the mechanism given by the architecture or device description. If the target has a debug channel, the reason MAY be written there first.
3. `Halt` — disable interrupts and stop executing in a low-power wait loop. If the target has a debug channel, the reason MAY be written there first.

**rt.start.data** — On targets whose memory regions separate code from writable data (**target.memory.placement**), the startup code MUST copy the initial values of writable data from the `Exec` region to RAM and zero the remaining writable data before any Anlang code runs.

**rt.panic.app** — In the `app` and `ui` profiles, a panic MUST write a message with the panic reason and a stack trace to the standard error stream, then terminate the process with a non-zero exit status.

**rt.panic.trace** — Stack traces MUST name functions, files and lines on all targets. On targets where the debug table is not part of the image, the trace MUST contain addresses that `anlang` resolves with the side table (see [20-toolchain](20-toolchain.md), **tool.debug.side-table**).

**Note** — Which operations can panic, and how the `Panic` effect propagates, is specified in [04-expressions](04-expressions.md) and [07-effects](07-effects.md).

## 19.5 Task stacks

**rt.stack.static** — For each task, interrupt handler and `main`, the compiler MUST compute the maximum stack depth when it is bounded: no recursion, and indirect calls resolved to a known set of functions. The stack of such a task MUST have exactly that size, rounded to the target's alignment.

**rt.stack.reserve** — In the `app` and `ui` profiles, a task whose maximum stack depth cannot be computed (recursion, unbounded depth) MUST receive a reserve of virtual memory with a guard page. Physical memory is committed on use. A stack overflow MUST be detected and reported as a panic; it MUST NOT corrupt memory.

**rt.stack.report** — The compiler MUST report every task that needed a reserve under **rt.stack.reserve**, as part of `anlang size` and of the `-json` build report (see [20-toolchain](20-toolchain.md)).

**rt.stack.core-recursion** — In the `core` profile, recursion in a task, an interrupt handler or `main` without a proven bound on depth MUST be rejected at compile time (see [14-embedded](14-embedded.md) for the stack analysis against the target's `Stack` size).

## 19.6 Development-only components

**rt.dev.only** — The deterministic test scheduler (see [09-concurrency](09-concurrency.md), [20-toolchain](20-toolchain.md)) and the hot reload interpreter (see [17-ui](17-ui.md)) MUST NOT be present in a release build.

**rt.dev.builds** — A *release build* is the output of `anlang build`. A *development build* is a build made by `anlang test`, `anlang dev`, `anlang debug` or `anlang run` with development options. Development-only components MAY be present only in development builds.
