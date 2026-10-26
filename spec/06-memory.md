# 06. Memory

This file specifies the Anlang memory model: value semantics, `inout` access and exclusivity, second-class references, access to parts of values, copy-on-write, arenas, synchronization types as shared handles, `nil` safety, global state, the package `an/unsafe`, pointer kinds and allocation per profile.

Anlang uses mutable value semantics, after the language Hylo: memory safety without lifetimes in the syntax and without a garbage collector.

## 1. Values and copies

**mem.value.copy** — The following operations produce a *logical copy* of a value: assignment, initialization of a variable, passing an argument to a value parameter, returning a result, sending on a channel, storing into a field or an element, capturing by a closure ([04-expressions](04-expressions.md), **expr.closure.capture-copy**), and conversion to an interface type. After the copy, a modification of either the original or the copy is not observable through the other.

**mem.value.sync-exception** — Values of synchronization types are the only exception to **mem.value.copy**: a copy refers to the same object (§8).

**mem.value.elision** — An implementation MAY avoid physical copies when the difference is not observable. In particular, it MAY pass a large value by address to a callee that does not modify it, and MAY share buffers under copy-on-write (§6).

**mem.value.handles** — A value of type `arena.Ref[T]` or `ffi.Handle` is plain data. Copying it copies the handle; both copies designate the same arena slot or external object by design. Such handles are not references in the sense of §3.

## 2. `inout` access

### 2.1 Definition

**mem.inout.param** — A parameter or method receiver declared `inout` ([03-declarations](03-declarations.md)) gives the callee mutable access to the caller's value. A modification through the parameter is a modification of the caller's value.

**mem.inout.only-way** — A function can modify a value of its caller only through an `inout` parameter or receiver. There is no other mechanism in safe code.

**mem.inout.access** — An *`inout` access* to a storage location is live:

1. for an `inout` argument: from the start of the call until the call returns ([04-expressions](04-expressions.md), **expr.call.inout-arg**);
2. for an `inout` loop variable: during each execution of the loop body ([05-statements](05-statements.md), **stmt.range.inout**);
3. for an `inout` argument of a `go` statement: until the end of the function that executes the `go` statement ([09-concurrency](09-concurrency.md));
4. for an `inout` argument of a deferred call: while the deferred call runs ([05-statements](05-statements.md), **stmt.defer.inout-args**).

**mem.inout.addressable** — An `inout` access MAY be taken only to an addressable operand ([04-expressions](04-expressions.md), **expr.addr.def**).

**mem.inout.builtin** — The built-in functions `copy`, `delete` and `clear` modify their first operand: it is passed with an `inout` access for the duration of the call, and the other operands are passed as values. The first operand MUST therefore be addressable (**mem.inout.addressable**; a map element is not, **expr.addr.map**). In Go the operand may be any expression, because slices and maps refer to shared memory; in Anlang a modification of a temporary value would be lost.

```go
copy(dst, src)        // dst is a variable: permitted
delete(m, k)          // permitted
copy(f(), src)        // error: the result of f() is not addressable
delete(groups[g], k)  // error: a map element is not addressable
```

### 2.2 Exclusivity

**mem.inout.exclusive** — While an `inout` access to a value is live, the program MUST NOT access that value, or any part of it, or any value containing it, through any other path. This applies to reads and writes, to the code of the caller, to other `inout` accesses, to closures and to tasks.

**mem.inout.overlap** — Two access paths *overlap* unless the implementation proves them disjoint. Paths are compared as follows:

1. a path overlaps every path of which it is a prefix: `x` overlaps `x.f` and `x[i]`;
2. distinct fields of the same struct do not overlap: `p.a` and `p.b`;
3. `a[i]` and `a[j]` do not overlap if `i != j` is proven;
4. slice expressions `s[lo1:hi1]` and `s[lo2:hi2]` do not overlap if `hi1 <= lo2` or `hi2 <= lo1` is proven;
5. paths with different root variables do not overlap.

Proofs use the solver ([08-contracts](08-contracts.md)).

**mem.inout.static** — Violations of **mem.inout.exclusive** MUST be detected at compile time. There is no run-time exclusivity check.

**Note** — Exclusivity is the reason data races cannot occur: a mutable value is never reachable from two tasks at once ([09-concurrency](09-concurrency.md)).

### 2.3 Subslices

**mem.inout.subslice** — When a slice expression `s[lo:hi]` of an addressable slice or array `s` is passed as an `inout` argument, the parameter denotes the elements `lo` to `hi-1` of `s`. Modifying elements through the parameter modifies those elements of `s`. Changing the length or capacity of the parameter, by assignment or by `append`, is a run-time error that panics if it would change the length.

```go
func SortParallel(xs inout []i32) {
    mid := len(xs) / 2
    go Sort(xs[:mid])        // disjoint subranges of xs
    go Sort(xs[mid:])
}
```

## 3. Second-class references

**mem.ref.second-class** — The following are *second-class references*: `inout` parameters and receivers, `inout` loop variables, and closures that capture them ([04-expressions](04-expressions.md), **expr.closure.capture-inout**). A program MUST NOT:

1. store a second-class reference in a variable, field or element that outlives it;
2. return it from a function;
3. send it on a channel;
4. convert it to an interface type;
5. capture it in a closure other than one permitted by **expr.closure.capture-inout**;
6. pass it to a `go` statement, except as permitted by [09-concurrency](09-concurrency.md), or to `task.Detach`.

Reading the value of an `inout` parameter or loop variable produces a copy, which is an ordinary value.

**mem.ref.no-other** — Safe code has no other kind of reference. Consequently no lifetime annotations exist, and a value cannot refer to itself.

## 4. Parts of values

**mem.part.go** — Elements of arrays and slices and fields of structs are reached through addressable operands as in Go: `xs[i].Increment()` and `p.pos.x = 0` take a temporary `inout` access to the part ([04-expressions](04-expressions.md), **expr.addr.def**).

**mem.part.map** — Values in a map are not addressable ([04-expressions](04-expressions.md), **expr.addr.map**). To modify a value in a map, the program reads it, modifies the copy and stores it back.

```go
u := users["alice"]
u.SetAge(31)
users["alice"] = u
```

**mem.part.container** — A container type gives access to its parts through methods that return iterator functions yielding `inout` access ([05-statements](05-statements.md), **stmt.range.inout-iter**). Containers of the standard library MUST use this mechanism (`sync.Mutex[T].Lock`, `arena.Arena[T].Edit`).

```go
for n := range a.Edit(root) {
    n.AddChild(child)
}
```

**mem.part.no-projections** — There is no other mechanism for access into a value: no projections, no user-defined indexing.

**Note** — The cost of this simplicity is that a container cannot offer indexing that behaves like the built-in one (`a[root].AddChild(child)`).

## 5. No garbage collector

**mem.gc.none** — An implementation MUST NOT use a tracing garbage collector. Storage of a value is released when the last copy of it ceases to exist: when its variable goes out of scope, when it is overwritten, or when its containing value is released.

**Note** — Cycles cannot be created: references are second-class (§3) and a value cannot refer to itself. Graphs with arbitrary links are built with arenas (§7).

## 6. Copy-on-write

**mem.cow.string** — A `string` value is immutable, as in Go.

**mem.cow.slice** — Slices and maps are values. A slice expression `s[lo:hi]`, an assignment `t := s`, `append` and every other operation that produces a slice or map value produce a value that is logically independent of its source: a later modification of one is not observable through the other.

**Note** — This differs from Go, where `t := s[1:3]; t[0] = 9` modifies `s`. In Anlang, only an `inout` access (§2.3) lets a modification of a subslice reach its source.

**mem.cow.impl** — An implementation SHOULD implement **mem.cow.slice** by sharing buffers between copies and copying a buffer before the first write while it is shared. Reference counts used for this are not observable.

**mem.cow.atomic** — An implementation MUST use atomic reference counts for a buffer that may be shared between tasks, and SHOULD use non-atomic counts otherwise.

## 7. Arenas

The package `an/arena` provides storage for graphs, trees with parent links and other structures with arbitrary links.

**mem.arena.type** — `arena.Arena[T]` is a value that owns nodes of type `T`. `arena.Ref[T]` is a handle consisting of a slot index and a generation.

**mem.arena.new** — `a.New(v T) arena.Ref[T]` requires an `inout` access to `a`, stores `v` as a new node and returns its handle.

**mem.arena.edit** — `a.Edit(r arena.Ref[T])` returns an iterator that yields an `inout` access to the node designated by `r` once ([05-statements](05-statements.md), **stmt.range.inout**).

**mem.arena.get** — `a.Get(r arena.Ref[T]) (T, bool)` returns a copy of the node and `true`, or the zero value and `false` if `r` does not designate a live node.

**mem.arena.delete** — `a.Delete(r arena.Ref[T])` removes the node and increments the slot's generation.

**mem.arena.stale** — Using a handle whose generation does not match its slot is detected at run time and MUST NOT access memory of another node. `Get` reports it with `false`; `Edit` panics.

**mem.arena.release** — All nodes of an arena are released together when the arena value is released (§5).

```go
func BuildTree(a inout arena.Arena[Node]) arena.Ref[Node] {
    root := a.New(Node{name: "root"})
    child := a.New(Node{name: "child", parent: Some(root)})
    for n := range a.Edit(root) {
        n.AddChild(child)
    }
    return root
}
```

## 8. Synchronization types as handles

The synchronization types are `chan T`, `sync.Mutex[T]`, `sync.RWMutex[T]` and the types of the package `an/atomic` ([02-types](02-types.md)).

**mem.sync.handle** — A value of a synchronization type is a handle to a shared object. Copying the value — by assignment, passing, capturing in a closure, storing in a struct or passing to a task — produces a handle to the same object.

**mem.sync.access** — The content of a synchronization object is accessible only through its operations: send and receive for channels, `Lock`, `RLock` and `Load` for mutexes, the atomic operations for atomic types. A mutex's content is reached through an iterator that yields `inout` access while the lock is held.

**mem.sync.zero** — The zero value of `sync.Mutex[T]`, `sync.RWMutex[T]` and an atomic type is a new object holding the zero value of its content. The zero value of `chan T` is `nil`; a channel object is created by `make`.

**mem.sync.struct** — A struct that contains a field of a synchronization type is still a value; copying the struct copies the handle, so both copies share that object.

**mem.sync.frame** — If no handle to a synchronization object can outlive the function that created the object, the object MUST be placed in that function's frame without heap allocation or reference counting. Structured concurrency guarantees that tasks started by the function end before it returns ([09-concurrency](09-concurrency.md)).

**mem.sync.heap** — If a handle can outlive the creating function — it is returned, stored in a longer-lived value, sent to a longer-lived task, or passed to `task.Detach` — the object is placed on the heap with an atomic reference count. This requires the effect `Alloc` and is therefore not available in the `core` profile, where such a program is a compile-time error unless the object is a package-level variable.

## 9. `nil` safety

**mem.nil.call** — Calling a method on an operand of interface type, or calling an operand of function type, MUST be proven at compile time not to happen when the operand is `nil`. Otherwise the call is a compile-time error.

**mem.nil.facts** — The implementation establishes that an operand is non-`nil` at a program point by flow analysis over its access path (a variable, a parameter, or a field path rooted in one). An operand is known non-`nil` if, on every path to the point:

1. it was assigned or initialized from a value known non-`nil`: a conversion of a non-interface value to an interface type, a function literal, a named function, or another operand known non-`nil`; or
2. a comparison `x != nil` evaluated to `true`, or `x == nil` evaluated to `false`, as the condition of an `if` statement or a `for` statement whose branch contains the point, or of an `if` statement whose `x == nil` branch ends in a terminating statement; or
3. a contract proves it ([08-contracts](08-contracts.md)), for example `ensures result != nil` of the function that produced it.

**mem.nil.func-param** — A parameter of function type is known non-`nil` in the body of its function or function literal, until it is assigned. Correspondingly, an argument for a parameter of function type MUST be proven non-`nil` at the call by **mem.nil.facts**: a function literal, a named function, a method value, or an operand known non-`nil`; passing `nil` or a value not known non-`nil` is a compile-time error. A `range` statement over an iterator function passes a non-`nil` `yield`. Parameters of interface type have no such rule.

```go
func Each(xs []int, f func(int)) {
    for _, x := range xs {
        f(x)             // permitted: f is a parameter of function type
    }
}

Each(xs, nil)            // error: the argument for f may be nil
```

**mem.nil.invalidate** — A fact about a path is invalidated by an assignment to the path or to any of its prefixes, and by an `inout` access to the path or to any of its prefixes. No other event invalidates it.

**Note** — Value semantics make this analysis sound even for field paths: no other code can change a local value between the check and the use.

```go
var w io.Writer          // nil
w.Write(buf)             // error: w may be nil

if w != nil {
    w.Write(buf)         // permitted
}
```

## 10. Global state

**mem.global.kinds** — A package-level variable MUST have one of the following types: an atomic type, a mutex type (`sync.Mutex[T]`, `sync.RWMutex[T]`), or — in the `core` profile only — a hardware mapping created by `unsafe.Map` (§11). Its initializer MUST be a constant expression. The declaration rules are in [03-declarations](03-declarations.md); access to such variables gives the effect `Global` ([07-effects](07-effects.md)).

```go
var ticks atomic.U32                    // permitted
var stats sync.Mutex[Stats]             // permitted: content only through Lock
var count int                           // error: ordinary mutable variable
```

## 11. The package `an/unsafe`

**mem.unsafe.package** — Unsafe operations exist only as functions and types of the package `an/unsafe`. There are no `unsafe` blocks. Importing `an/unsafe` is the marker of unsafe code; every unsafe place in a program is found by searching for its imports.

**mem.unsafe.profile** — The package `an/unsafe` MAY be imported only by packages of the `core` profile.

**mem.unsafe.pointers** — Raw pointer types exist only in the package `an/unsafe` and in the architecture packages `an/arch/...`. Code outside them uses `inout`, slices and `ffi.Handle` instead ([12-ffi](12-ffi.md)).

**mem.unsafe.map** — `unsafe.Map[T](addr)` declares that memory at `addr` holds a value of type `T` and returns an addressable value of type `T` bound to that memory. It MAY be used only as the initializer of a package-level variable. Every access to the mapped value is a memory access that the implementation MUST NOT elide, merge or reorder with respect to other accesses to mapped values. Indices into the mapped value are checked or proven as for any other value (**expr.bounds.check**).

```go
var screen = unsafe.Map[[2000]Cell](i8086.Far(0xB800, 0))   // DOS text video memory
```

**mem.unsafe.api** — The package `an/unsafe` declares the pointer kinds of §11.1 as types — `Flat` and `Near` with the width of `usize`, `Far` and `Program` of 32 bits — and the functions `Map` and `Data`. The address of `Map` MUST be a value of one of these types; it is evaluated at compile time (**decl.var.package-init**). An architecture package MAY declare constructors of addresses, such as `i8086.Far(segment, offset)`.

**mem.unsafe.data** — `unsafe.Data(x)` returns, as a `usize`, the address of the first byte of `x`, a string, a `cstring`, a `dstring` or a slice. It lets code pass data to the instructions of its architecture, such as `i8086.Int(0x21, i8086.Regs{AX: 0x0900, DX: u16(unsafe.Data(s))})`.

**Note** — Device registers are mapped by code generated from SVD files ([14-embedded](14-embedded.md)), so user code for microcontrollers does not import `an/unsafe`.

### 11.1 Pointer kinds

**mem.unsafe.kinds** — The kinds of raw pointers are defined by the target's architecture description ([13-targets](13-targets.md)):

| Architecture | Pointer kinds |
| --- | --- |
| i8086 | `near` — 16-bit offset within a segment; `far` — segment and offset, 32 bits |
| AVR | pointer to RAM; pointer to flash, read with a dedicated instruction |
| 32- and 64-bit targets | one flat kind |

**mem.unsafe.kind-convert** — Conversions between pointer kinds MUST be explicit and are available only in `an/unsafe` and architecture packages.

## 12. Allocation per profile

**mem.alloc.profile** — The allocation model depends on the package profile ([07-effects](07-effects.md)):

| Profile | Model |
| --- | --- |
| `core` | static memory and the stack only; explicit regions and fixed-size pools; no heap |
| `app` | general allocation, regions, arenas, copy-on-write for large values |
| `ui` | as `app`, plus immutable state trees with structural sharing |

**mem.alloc.effect** — An operation that may allocate heap memory — growing a slice or map beyond capacity, creating a copy of a shared buffer, `arena.Arena[T].New` when the arena must grow, placing a synchronization object on the heap — gives the containing function the effect `Alloc`. In the `core` profile such an operation is a compile-time error unless the implementation proves that it does not allocate.
