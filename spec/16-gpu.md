# 16. GPU Programming

This file specifies GPU functions: shaders and compute kernels written in Kern, their data access rules, dispatch and shader targets.

A GPU function is executed by many GPU threads at the same time. The rules below make data races between those threads impossible by construction. The syntax of the `gpu` declaration is the production `GpuFunctionDecl` in [03-declarations](03-declarations.md) §3.10.

## 1. GPU functions

**gpu.decl.kind** — A function declared with `gpu` is a *GPU function*. It is compiled for a shader target (section 4), not for the CPU of the program's target.

**gpu.decl.no-call** — A GPU function MUST NOT be called directly from CPU code. It is executed only through `gpu.Dispatch` (section 3).

**gpu.subset.calls** — A GPU function MAY call only ordinary functions that satisfy all rules of this section; such functions are compiled for the shader target as well. The body of a GPU function and of every function it calls MUST NOT contain:

1. recursion, direct or indirect;
2. calls through function values or interface values;
3. `go` statements, `select`, channel operations or `defer`;
4. `asm` or use of `unsafe`.

**gpu.subset.effects** — A GPU function and every function it calls MUST have an empty effect set, except for the effects introduced by the `kern/gpu` operations of this file. In particular they MUST NOT have `Alloc`, `Block`, `Tasks` or `Panic`; overflow and bounds checks MUST therefore be proven or handled explicitly ([08-contracts](08-contracts.md)).

**gpu.subset.profile** — Every package that contains a GPU function or a function called from one MUST allow the `core` profile ([07-effects](07-effects.md)).

**gpu.result** — A GPU function has no results.

## 2. Data access

### 2.1 Parameter types

**gpu.param.types** — Each parameter of a GPU function MUST be of one of these types:

1. a *GPU data type*: `bool`, a fixed-size integer type, `f32`, `f64`, a `kern/simd` vector type, a `kern/fixed` type, or an array or struct type whose elements or fields are GPU data types;
2. a slice of a GPU data type (an *input buffer*);
3. `gpu.Out[T]` where `T` is a GPU data type (an *output buffer*).

**gpu.param.layout** — The memory layout of a GPU data type in buffers MUST be the same on the CPU and on the GPU. The implementation chooses a layout that satisfies the rules of the shader target and uses it for the type on both sides. A program MUST NOT need to describe the layout twice.

### 2.2 Inputs

**gpu.in.readonly** — Parameters other than output buffers are read-only inside a GPU function and the functions it calls: assigning to a parameter, to an element of an input buffer or to a field of a parameter is an error.

### 2.3 Outputs

**gpu.out.write-only** — An output buffer is write-only: the only permitted operation on a value of type `gpu.Out[T]` inside a GPU function is the assignment `out[i] = v`.

**gpu.out.own-index** — In an assignment `out[i] = v`, the index `i` MUST be the thread's own global index: the expression `gpu.GlobalID().x`, or a variable whose value the solver proves equal to it. Any other index is an error.

**Note** — As a result, each element of an output buffer is written by at most one thread, and the threads cannot race.

**gpu.out.bounds** — An index equal to the thread's global index may still be outside the output buffer. The assignment is subject to the usual bounds rules: it MUST be proven in bounds or guarded by a check in the body.

### 2.4 Thread identity

**gpu.id.global** — `gpu.GlobalID()` returns the global index of the executing thread, a struct with fields `x`, `y`, `z` of type `u32`. `gpu.GlobalSize()` returns the number of threads of the dispatch in the same form.

**gpu.id.no-requires** — A `requires` clause of a GPU function MUST NOT refer to `gpu.GlobalID()`: the thread index is assigned by the GPU, and no caller can establish a condition on it. Conditions on the thread index are checked in the body.

## 3. Dispatch

```go
gpu.Dispatch(Blur, len(img), img, out)
```

**gpu.dispatch.form** — `gpu.Dispatch(f, n, args...)` executes the GPU function `f` with `n` threads, where `n` is a non-negative integer. `args` MUST match the parameters of `f` in number and type.

**gpu.dispatch.requires** — `requires` clauses of `f` MAY refer to its parameters and to `gpu.GlobalSize()`. They are proof obligations at the `gpu.Dispatch` call site, with the arguments substituted for the parameters and `n` for `gpu.GlobalSize().x` ([08-contracts](08-contracts.md)).

**gpu.dispatch.sync** — `gpu.Dispatch` returns after all threads have finished. When it returns, the elements written to output buffers are visible to the calling code.

**gpu.dispatch.effect** — `gpu.Dispatch` has effect `GPU`. The effect is permitted only in the `app` and `ui` profiles and only on targets whose platform provides it.

## 4. Shader targets

**gpu.target.kinds** — The implementation MUST support the following shader targets:

| Shader target | Used with |
| --- | --- |
| `spirv` | Vulkan, OpenCL |
| `wgsl` | WebGPU in the browser |
| `msl` | Metal on iOS and macOS |

**gpu.target.select** — The shader target is determined by the platform of the program's target: `msl` for iOS and macOS, `wgsl` for the web, `spirv` for the other platforms that provide `GPU`.

**gpu.target.embed** — The compiled GPU functions MUST be embedded in the program's output. Building them MUST NOT require external shader compilers.

## 5. Example

```go
gpu func Blur(img []simd.F32x4, out gpu.Out[simd.F32x4]) {
    i := gpu.GlobalID().x
    if i == 0 || i+1 >= u32(len(img)) {
        return
    }
    out[i] = (img[i-1] + img[i] + img[i+1]) / simd.F32x4Splat(3)
}
```

## 6. Future work (informative)

- Indexes that are injective functions of the thread index, proven by the solver.
- `gpu.Atomic[T]` and workgroup shared memory `gpu.Shared[T]` for reductions and histograms.

## Open issues

1. **Creating and reading output buffers.** `kern.md` does not say how a `gpu.Out[T]` is created on the CPU side and how results are read back. Provisional: a constructor with a length, and a method that returns the elements as a slice after dispatch; names to be fixed in [18-stdlib](18-stdlib.md).
2. **Launch size in contracts.** `kern.md` says that kernel preconditions concern the launch size but does not name it. Provisional: `gpu.GlobalSize()` (**gpu.dispatch.requires**).
3. **Floating point semantics and `f64`.** GPU floating point may differ from IEEE 754, and not all GPUs support `f64`. Provisional: `f64` in a GPU function is an error on shader targets without `f64` support; the exact floating point guarantees are open.
4. **Windows.** `kern.md` mentions Direct3D for rendering on Windows. Provisional: compute on Windows uses `spirv` with Vulkan; Direct3D shader targets are not specified.
5. **Packages with GPU functions.** Provisional: a GPU function may be declared in any package, provided that package allows `core` (**gpu.subset.profile**).
