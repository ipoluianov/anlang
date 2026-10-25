# 14. Embedded Systems

This file specifies how Kern programs for microcontrollers and other bare-metal targets access hardware: device descriptions, peripheral ownership, interrupt handlers, stack analysis, static tasks and worst-case execution time (WCET).

Firmware is written in the `core` profile. Nothing here introduces new syntax except the `interrupt` declaration, whose syntax is the production `HandlerDecl` in [03-declarations](03-declarations.md) §3.10.

## 1. Device descriptions

A *device description* describes the peripherals, registers and interrupt vectors of a concrete chip. It is part of the target description ([13-targets](13-targets.md)) through the `Device` field of `target.Desc`.

**emb.device.source** — A device description MUST be produced at compile time. The standard source is a vendor SVD file loaded with `svd.Load(path)`; `svd.Load` runs under the rules for compile-time evaluation and generators ([10-compile-time](10-compile-time.md)).

**emb.device.package** — For a target whose description has a `Device`, the implementation MUST provide the package `kern/device`, generated from that description. The content of `kern/device` depends on the selected target. Importing `kern/device` when the selected target has no `Device` is an error.

**emb.device.granularity** — The device description defines the *ownership units*: each peripheral instance (for example `TIM2`, `USART1`) is a unit. Where the hardware allows parts of a peripheral to be changed independently without read-modify-write (for example GPIO pins with atomic set/reset registers), the description MAY declare each part (each pin) as a separate unit.

### 1.1 Registers

**emb.reg.types** — For each register and bit field of the device description the generated package MUST declare a distinct type. A field whose description lists its allowed values MUST have a type whose values are exactly those values.

**emb.reg.values** — Writing a value that is not an allowed value of a field is a compile-time error. A non-constant value of a wider type MUST be converted under the narrowing rules of [02-types](02-types.md) (the conversion compiles only if the range is proven, otherwise it yields a comma-ok form).

**emb.reg.volatile** — Every read and write of a device register is a *volatile access*: the implementation MUST NOT remove, duplicate, merge or reorder volatile accesses relative to other volatile accesses of the same execution context.

**emb.reg.unsafe** — The register mapping MUST be generated using `unsafe.Map` inside `kern/device` ([06-memory](06-memory.md)). Program code that accesses peripherals through `kern/device` MUST NOT need to import `unsafe`.

### 1.2 Memory regions and constants

**emb.mem.regions** — The memory regions of the target (`target.Desc.Memory`, see [13-targets](13-targets.md)) are the only memory the program may occupy. If the code, constants, static data, task stacks or the main stack do not fit their regions, the build MUST fail, and the diagnostic MUST give the size of each region's contents broken down by package.

**emb.mem.const** — A package-level constant whose value is computed at compile time (tables, CRC tables, clock divisors) MUST be placed in a read-only region (flash) when the target has one, and MUST NOT be copied to RAM.

## 2. Peripheral ownership

### 2.1 Peripherals as values

**emb.periph.global** — Each ownership unit is available as a package-level value of `kern/device`: `device.TIM2`, `device.USART1`, `device.GPIOC.Pin13`. These values are hardware mappings and are permitted as package-level variables in the `core` profile ([06-memory](06-memory.md)).

**emb.periph.identity** — Every ownership unit has a distinct type of size zero. A value derived from a unit (for example `led := device.GPIOC.Pin13.Output(gpio.PushPull)`) has a type that statically records the unit. Copying such a value does not create a new unit: all copies denote the same unit.

**emb.periph.no-take** — There is no operation that transfers ownership of a unit. Uniqueness of access is established by the analysis in 2.2–2.4, not by the type system.

### 2.2 Instance effects

**emb.periph.effect** — Each ownership unit `U` defines an effect, also named `U`. Every operation on `U` or on a value derived from `U` (register access, method call) has effect `U`. Instance effects propagate like any other effect ([07-effects](07-effects.md)), including through parametric references.

**Note** — Instance effects are in addition to the class effects provided by the target (`Gpio`, `Uart`, `Timers`, …).

### 2.3 Execution contexts

**emb.ctx.def** — An *execution context* is one of:

1. the *main context*: the execution of `main` and everything it calls;
2. a *task context*: one running task instance started by `go` or `task.Go` ([09-concurrency](09-concurrency.md));
3. an *interrupt context*: the execution of one `interrupt` handler;
4. a *trap context*: the execution of one `trap` handler ([15-os](15-os.md)).

**emb.ctx.instances** — A `go` statement that can start more than one task instance that is live at the same time defines that many task contexts. Two instances of the same `go` statement are distinct contexts of each other.

**emb.ctx.reach** — For every ownership unit `U` the implementation MUST compute the set `C(U)` of execution contexts in which effect `U` is reachable, excluding accesses made during the initialization phase (2.5).

### 2.4 Single and multiple contexts

**emb.ctx.single** — If `C(U)` has at most one element, the program MAY access `U` without synchronization.

**emb.ctx.multi** — If `C(U)` has more than one element, every access to `U` outside the initialization phase MUST occur inside a critical section of `U`:

```go
for t := range device.TIM2.Lock() {
    t.ClearUpdateFlag()
}
```

Any other access is an error. The diagnostic MUST name at least two contexts in `C(U)` and the access path in each, for example: `TIM2 is used in main (after interrupts.Enable) and in interrupt TIM2 without a critical section`.

**emb.lock.form** — `Lock()` of an ownership unit returns an iterator that yields `inout` access to the unit exactly once, as specified for range-over-func with `inout` in [05-statements](05-statements.md).

**emb.lock.ceiling** — The *priority ceiling* of `U` is the highest priority among the contexts in `C(U)`. While the body of a critical section of `U` executes, the implementation MUST prevent preemption by every context whose priority is not above the ceiling (for example by raising the interrupt priority mask, or by disabling interrupts where the architecture has no priority masking). The ceiling MUST be computed at compile time.

**emb.lock.no-block** — The body of a critical section MUST NOT have effect `Block`.

**emb.prio.order** — For ceiling computation, every interrupt context has a higher priority than every task context and the main context. Among interrupt contexts, priorities come from the device description. Among task contexts, priorities are the task priority levels of [09-concurrency](09-concurrency.md). The main context runs at the task priority `task.Normal` (**conc.prio.inherit**).

### 2.5 Initialization phase

**emb.init.start** — On targets with interrupts, `main` begins execution with interrupts globally disabled and the task scheduler not running.

**emb.init.end** — The *initialization phase* of `main` consists of the program points reachable from the entry of `main` along a path that does not pass through a call of `interrupts.Enable()` or `task.Run()`. An access to an ownership unit inside the initialization phase does not add the main context to `C(U)`.

**emb.init.calls** — `interrupts.Enable()` and `task.Run()` MUST be called only directly from `main`.

### 2.6 Shared variables

**emb.shared.var** — A package-level variable reachable from more than one execution context MUST be of an atomic type or be accessed only inside a critical section. Package-level variables are restricted to these kinds anyway ([06-memory](06-memory.md)); this rule extends the restriction to interrupt and trap contexts.

**emb.shared.mutex** — On a single-core target in the `core` profile, `sync.Mutex[T]` MUST be implemented by the priority ceiling protocol: its `Lock()` raises the priority to the ceiling of the mutex computed over all its users and never waits. On such targets `Lock()` does not have effect `Block`, so a mutex MAY be used from interrupt handlers.

## 3. Interrupt handlers

**emb.irq.name** — The name of an `interrupt` handler MUST be the name of an interrupt vector in the device description. A vector MUST have at most one handler.

**emb.irq.signature** — An `interrupt` handler has no parameters and no results.

**emb.irq.effects** — An `interrupt` handler has the implicit constraint `ensures effect.Without(Alloc, Block, Panic)` ([07-effects](07-effects.md), [08-contracts](08-contracts.md)). Overflow and bounds checks in a handler MUST therefore be proven or handled explicitly.

**emb.irq.profile** — `interrupt` declarations are permitted only in packages whose profile is `core` alone.

**emb.irq.no-call** — An `interrupt` handler MUST NOT be called, referenced as a function value or passed as an argument.

**emb.irq.vectors** — The implementation MUST generate the interrupt vector table from the device description and the declared handlers. Vectors without a handler MUST be bound to a default handler that behaves as a panic with the target's `OnPanic` behavior ([19-runtime](19-runtime.md)).

## 4. Static tasks

**emb.task.static** — In the `core` profile every task is static: the maximum number of simultaneously live instances of each `go` statement and the stack size of each instance MUST be known at compile time. A `go` statement whose number of live instances is not bounded at compile time is an error.

**Note** — Scheduling of static tasks (preemption between priority levels, round robin within a level) is specified in [09-concurrency](09-concurrency.md).

## 5. Stack analysis

**emb.stack.depth** — The implementation MUST compute the maximum stack depth of the main context, of every task context and of every interrupt and trap handler. Calls through function values and interfaces are resolved to the set of possible targets in the program ([07-effects](07-effects.md)).

**emb.stack.nesting** — The stack requirement of a stack shared by several contexts (on most microcontrollers, the main stack is shared with interrupt handlers) is the depth of its base context plus the largest sum of handler depths along any chain of nested preemptions permitted by the interrupt priorities.

**emb.stack.limit** — If a stack requirement exceeds the size available for it (the target's `Stack` for the main stack, the computed task stack for a task), the build MUST fail, and the diagnostic MUST show the deepest call chain.

**emb.stack.recursion** — Recursion in the `core` profile is subject to [19-runtime](19-runtime.md): recursion without a proven depth bound in a task or handler is an error.

## 6. Worst-case execution time

**emb.wcet.contract** — A function, including an `interrupt` handler, MAY state an upper bound on its execution time with `ensures wcet.Within(d)`, where `d` is a compile-time constant duration ([08-contracts](08-contracts.md)).

**emb.wcet.model** — The bound is checked against the cycle model of the target's architecture description ([13-targets](13-targets.md)) and the CPU clock frequency of the target. If the target provides no cycle model or frequency, `wcet.Within` is an error.

**emb.wcet.isolation** — `wcet.Within(d)` bounds the execution time of the function in isolation: time spent in handlers that preempt it is not included.

**emb.wcet.loops** — Every loop on a path of a function with `wcet.Within` MUST have an iteration bound that the solver can prove; otherwise the bound is not proven.

**emb.wcet.sound** — The computed bound MUST NOT be lower than any actual execution time permitted by the cycle model. For processors with caches or pipelines the analysis MUST be conservative.

**emb.wcet.report** — If the bound is not proven, the diagnostic MUST show the longest execution path found with its cycle count per basic block.

## 7. Example

```go
package main

profile core

import (
    "kern/atomic"
    "kern/cpu"
    "kern/device"
    "kern/gpio"
    "kern/interrupts"
    "kern/timer"
)

var ticks atomic.U32

func main() {
    led := device.GPIOC.Pin13.Output(gpio.PushPull)               // initialization phase
    device.TIM2.Start(timer.Config{Freq: Hz(2), Interrupt: true})
    interrupts.Enable()                                           // end of initialization phase

    for {                                                         // C(TIM2) = {interrupt TIM2}
        if ticks.Load()%2 == 0 { led.Low() } else { led.High() }
        cpu.WaitForInterrupt()
    }
}

interrupt TIM2() {
    device.TIM2.ClearUpdateFlag()
    ticks.Add(1)
}
```

## 8. Device families (informative)

| Family | Architecture | Notes |
| --- | --- | --- |
| AVR (Arduino Uno) | 8-bit Harvard | 2 KiB RAM; separate pointer kinds for flash and RAM |
| STM32, RP2040, nRF52 | ARM Cortex-M | Most common class |
| ESP32-C3, CH32V | RISC-V 32 | Open architecture with a formal specification |
| MSP430 | 16-bit | Ultra-low power |

## Open issues


1. **Overriding interrupt priorities.** Deferred to implementation — step 33. Priorities come from the device description; a way to override them is designed with task priorities.
2. **Multi-core microcontrollers (RP2040, ESP32).** Deferred to implementation — step 52. **emb.shared.mutex** applies to single-core targets; multi-core `core` targets are specified with the RP2040 target.
3. **Response-time analysis.** Deferred to implementation — step 53. Checking that all tasks meet their deadlines (WCET plus task periods and preemption) is designed together with WCET.
