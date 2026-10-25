# 9. Concurrency

This file specifies tasks, structured concurrency, channels, synchronization types, cancellation, priorities and the execution models of the profiles.

Concurrency has the syntax of Go — `go`, channels, `select` — with structured semantics: a function returns only after all tasks it started have finished. Data races are impossible by construction: values are copied between tasks, and shared state exists only inside synchronization types.

## 9.1 Tasks

**conc.task.unit** — A *task* is the unit of concurrent execution. Operating-system threads are not visible in the language.

**conc.task.go** — The `go` statement has the syntax of Go. It evaluates the function value and the arguments in the starting task, as in Go, and starts a new task that executes the call. A `go` statement has the effect `Tasks` ([07-effects](07-effects.md)).

**conc.task.owner** — Each task started by a `go` statement or `task.Go` is *owned* by the invocation of the function whose body contains the statement. Function literals are functions for this rule; the body of a range-over-func loop is not a function literal and belongs to the enclosing function ([05-statements](05-statements.md)).

**conc.task.config** — `task.Go(cfg task.Config, f F)` starts a task like `go f()` with the parameters in `cfg` (§9.8); `F` is `func()` or `func() error`. `go f(args)` is equivalent to `task.Go(task.Config{}, …)` with the priority of the starting task.

**conc.task.error** — The call in a `go` statement or passed to `task.Go` MAY have a last result of type `error`. That error is the task's *error result* and is delivered to the owning invocation through `task.Wait` (§9.2). Other results are discarded, as in Go.

## 9.2 Structured concurrency

**conc.struct.wait** — `task.Wait() error` blocks until every task owned by the calling invocation has finished.

1. If a task finishes with a non-nil error result, the first such error in order of completion is recorded, every other task owned by the invocation is cancelled (§9.4), and `Wait` returns the recorded error after those tasks have stopped.
2. If no error is recorded and the deadline of the invocation (**conc.cancel.deadline**) has expired, `Wait` returns `task.ErrDeadline`.
3. Otherwise `Wait` returns `nil`.

After `Wait` returns, the invocation owns no running tasks; later `go` statements start new tasks owned by it.

**conc.struct.exit** — When an invocation exits — by `return`, by reaching the end of its body or by panicking — while it owns unfinished tasks, those tasks are cancelled (§9.4), and the exit completes only after they have stopped. Error results of tasks stopped this way are discarded. No invocation returns before every task it owns has finished.

**conc.struct.error-wait** — If a function contains a `go` statement or `task.Go` call that can start a task with an error result, then on every path from such a statement to a non-panicking exit of the function, the program MUST call `task.Wait()` or `task.Cancel()` after the last such statement on that path. The error returned by `task.Wait()` is subject to **stmt.error.unused** ([05-statements](05-statements.md)). This extends the unused-error rule: an error result of a task is never lost silently.

**conc.struct.panic** — If a task panics, the runtime cancels all other tasks owned by the same invocation (§9.4), waits for them to finish, and then the owning invocation panics with the same value at its next wait point (including `task.Wait`) or at its exit, whichever comes first.

**conc.struct.nested** — The rules apply recursively: a task that owns tasks cancels or waits for them before it finishes.

**conc.struct.no-leak** — As a consequence, no task outlives the invocation that owns it, except tasks started with `task.Detach` (§9.3).

```go
func Serve(reqs []Request) (Stats, error) {
    var stats sync.Mutex[Stats]
    for _, r := range reqs {
        go process(r, stats)              // process returns error
    }
    if err := task.Wait(); err != nil {
        return Stats{}, err               // the other tasks have been cancelled
    }
    return stats.Load(), nil
}
```

## 9.3 Detached tasks

**conc.detach.call** — `task.Detach(f func())` starts a task that is not owned by the calling invocation and MAY outlive it. A call of `task.Detach` has the effect `Detach`, declared by `kern/task`.

**conc.detach.profile** — `Detach` is forbidden in the `core` profile ([07-effects](07-effects.md), **eff.profile.allowed**).

**conc.detach.no-inout** — A detached task MUST NOT receive `inout` access (§9.6). Values captured by `f` are copies; synchronization objects reachable from `f` are placed on the heap (**conc.sync.placement**).

## 9.4 Cancellation and deadlines

**conc.cancel.tree** — Cancelling an invocation cancels every task it owns, and recursively every task those tasks own. Cancellation propagates from parent to children automatically; programs do not pass a context value.

**conc.cancel.delivery** — A cancelled task is not interrupted between wait points. At its next wait point (§9.5) the task unwinds: deferred calls run and iterators release what they hold (§9.7), as during a panic, but the unwinding is not an error and does not propagate to the owner. `task.Cancelled() bool` reports whether the current task has been cancelled, for computations without wait points.

**conc.cancel.deadline** — `task.Deadline(d time.Duration)` sets a deadline `d` from now for the calling invocation and returns a channel that becomes ready when the deadline expires. When the deadline expires, every task owned by the invocation is cancelled. A deadline set by an owner applies to all tasks below it; a task's effective deadline is the earliest of its own and its owners' deadlines.

**conc.cancel.explicit** — `task.Cancel()` cancels every task owned by the calling invocation, waits until they have stopped and discards their error results. The invocation itself is not cancelled. It is the explicit way to abandon tasks with error results before an exit (**conc.struct.error-wait**); tasks without error results are cancelled at exit anyway (**conc.struct.exit**).

```go
func FetchAll(urls []string) ([]Page, error) {
    results := make(chan result, len(urls))
    for _, u := range urls {
        go func() {
            page, err := http.Get(u)
            results <- result{page, err}
        }()
    }

    pages := []Page{}
    for range urls {
        select {
        case r := <-results:
            if r.err != nil {
                return nil, r.err            // the exit cancels the remaining requests
            }
            pages = append(pages, r.page)
        case <-task.Deadline(5 * time.Second):
            return nil, ErrTimeout           // the deadline has cancelled the remaining tasks
        }
    }
    return pages, nil
}
```

## 9.5 Wait points

**conc.wait.points** — A *wait point* is an operation at which a task may be suspended:

1. a channel send or receive, and a `select` statement without a ready `default`;
2. acquiring a lock (§9.7), except on single-core `core` targets, where locks follow the priority ceiling protocol and never wait ([14-embedded](14-embedded.md), **emb.shared.mutex**);
3. `task.Sleep` and `task.Yield`;
4. a blocking input/output operation of the standard library;
5. `task.Wait`, `task.Cancel`, and an exit while owned tasks are stopping (**conc.struct.wait**, **conc.struct.exit**);
6. a call of an external function that the target treats as blocking (**conc.os.blocking-ffi**).

A wait point has the effect `Block` ([07-effects](07-effects.md), **eff.source.wait**).

## 9.6 Values between tasks

**conc.value.copy** — Arguments of a `go` call and values captured by a function literal are copied, as by assignment ([06-memory](06-memory.md)). Sending a value on a channel copies it: the sender keeps its copy, and the receiver does not see later changes to it.

**conc.value.cow** — An implementation MUST NOT copy large values physically when it can share them by copy-on-write. Copy-on-write buffers reachable from more than one task MUST use atomic reference counts.

**conc.value.no-shared-mutable** — Apart from synchronization objects (§9.7), no mutable state is shared between tasks.

**conc.inout.borrow** — If a `go` statement passes an argument as `inout`, the argument is *borrowed* by the new task until the end of the owning invocation. From the `go` statement to the end of the invocation, the owning invocation MUST NOT access the borrowed value or any part of it.

**conc.inout.disjoint** — Two `go` statements of the same invocation MAY borrow parts of the same value only if the parts are disjoint. For slices and arrays, disjointness of the index ranges is a proof obligation ([08-contracts](08-contracts.md)); if the solver does not prove it, the program is rejected.

**conc.inout.capture** — A function literal used in a `go` statement or passed to `task.Go` MUST NOT capture an `inout` parameter of the enclosing function. `inout` access reaches a task only as an explicit `inout` argument.

```go
func SortParallel(xs inout []i32) {
    mid := len(xs) / 2
    go Sort(xs[:mid])        // parts are disjoint:
    go Sort(xs[mid:])        // proved by the solver
    // xs[0] = 1             // rejected: xs is borrowed by tasks
}
```

## 9.7 Synchronization types

**conc.sync.types** — The synchronization types are `chan T`, `sync.Mutex[T]`, `sync.RWMutex[T]` and the types of package `kern/atomic` (`atomic.U32`, `atomic.I64`, `atomic.Bool` and others).

**conc.sync.handle** — A value of a synchronization type is a *handle*: assignment, passing, capture by a function literal and sending on a channel copy the handle, and all copies refer to the same object ([06-memory](06-memory.md)). This is the only exception to value semantics.

**conc.sync.zero** — The zero value of `sync.Mutex[T]`, `sync.RWMutex[T]` and of the atomic types is a ready object holding the zero value of its contents. The zero value of `chan T` is `nil`; channels are created with `make`. A `nil` channel blocks forever on send and receive, as in Go.

**conc.sync.lock** — The value inside `sync.Mutex[T]` is accessible only through `m.Lock()`, an iterator for range-over-func that acquires the lock, yields one `inout T` access, and releases the lock when the loop body exits by any path — normal completion, `break`, `return`, panic or cancellation ([05-statements](05-statements.md)).

**conc.sync.load** — `m.Load() T` returns a copy of the value under the lock.

**conc.sync.rwmutex** — `sync.RWMutex[T]` provides `Lock()` as `sync.Mutex[T]` does, and `RLock()`, an iterator that yields a copy of the value under a shared lock.

**conc.sync.atomic** — Atomic types provide `Load`, `Store`, `Add`, `Swap` and `CompareAndSwap` as the atomic types of Go's `sync/atomic`. Explicit memory orders are specified in [15-os](15-os.md).

**conc.sync.placement** — A synchronization object is placed:

- in the frame of the invocation that creates it, if no handle to it can reach a task started with `task.Detach`; structured concurrency guarantees that all users finish before the frame is released;
- otherwise on the heap, with an atomic reference count; this requires the effect `Alloc` and is therefore not available in `core`.

Package-level synchronization variables have static storage.

```go
var stats sync.Mutex[Stats]

func Record(d time.Duration) error {
    for s := range stats.Lock() {
        if s.count == MaxRecords {
            return ErrFull           // the lock is released on every exit
        }
        s.count++
        s.total += d
    }
    return nil
}
```

## 9.8 Priorities

**conc.prio.levels** — Task priorities are `task.Low`, `task.Normal` and `task.High`. In the `core` profile the level `task.Critical` also exists; using `task.Critical` in another profile is rejected.

**conc.prio.inherit** — A task started with `go` has the priority of the starting task. `task.Go` with `task.Config{Priority: p}` starts a task with priority `p`. `main` runs with priority `task.Normal`.

**conc.prio.meaning** — The meaning of priorities depends on the execution model (§9.9):

| Execution model | Meaning |
| --- | --- |
| `core` | A ready task of a higher level preempts a task of a lower level; tasks of one level run in turn |
| `app` | A hint to the scheduler; a task locked to an OS thread gets the corresponding thread priority |
| `ui` | The main UI task is above all others; background tasks run at `Low` |
| cooperative (DOS, browser without threads) | The order in which the next ready task is chosen |

**conc.prio.inversion** — In `app` and `ui`, a task holding a lock on a `sync.Mutex` or `sync.RWMutex` runs at least at the priority of the highest-priority task waiting for that lock (priority inheritance).

**conc.prio.ceiling** — In `core`, the compiler computes for every lock the *priority ceiling*: the highest priority of all tasks and interrupt handlers that use it. Acquiring the lock raises the holder to the ceiling until release. On a single processor this excludes priority inversion and deadlock on these locks. The relative priority of interrupt, task and main contexts used for the ceiling is given by **emb.prio.order** ([14-embedded](14-embedded.md)).

**conc.prio.starvation** — In `app` and `ui`, the scheduler SHOULD gradually raise the priority of tasks that have been ready and not run for a long time. In `core`, the compiler MUST warn about a task of priority `High` or `Critical` that has a loop without a wait point.

## 9.9 Execution models

**conc.model.core** — In `core`, the set of tasks is static: the compiler determines every task the program can start, and its stack size ([19-runtime](19-runtime.md)). A `go` statement MUST be executed a number of times that the compiler can bound: it MUST NOT occur in a loop without a proven iteration bound or in a recursive function. Scheduling is preemptive between priority levels and round-robin within a level. Tasks started before `task.Run()` begin to run when `task.Run()` is called; `task.Run()` does not return.

**conc.model.core-chan** — In `core`, the buffer size in `make(chan T, n)` MUST be a constant expression.

**conc.model.app** — In `app`, tasks run on an M:N scheduler over OS threads with asynchronous input/output (epoll, kqueue, IOCP, io_uring), and run in parallel where the hardware allows.

**conc.model.ui** — In `ui`, there is one main UI task and background tasks. The UI state is updated only on the main UI task ([17-ui](17-ui.md)).

**conc.model.web** — On the `web` platform, tasks run on the browser event loop, and on WebAssembly threads where the browser provides them.

**conc.model.cooperative** — Where there are no OS threads or several cores (DOS, browser without threads), tasks run cooperatively: a task is switched only at a wait point. The same program has the same meaning; only its speed differs.

**conc.model.cooperative-warning** — When compiling for a cooperative model, the compiler MUST warn about a loop inside a task body whose iteration count is not proven to be below an implementation-defined bound and whose body contains no wait point.

## 9.10 Tasks and OS threads

**conc.os.lock-thread** — `task.LockThread()` binds the calling task to its current OS thread until the task finishes. It is intended for external libraries with thread-bound state, graphics contexts and the main UI thread.

**conc.os.blocking-ffi** — An implementation with OS threads MUST run a call of an external function that may block for a long time so that it does not stop other tasks, for example by moving it to a separate thread.

## 9.11 Deterministic scheduling in tests

**conc.test.deterministic** — `kern test` MUST provide a deterministic scheduler that replaces the target scheduler in tests. It MUST explore interleavings of tasks at wait points, and every interleaving it reports MUST be reproducible from its identifier.

**conc.test.findings** — The deterministic scheduler MUST report deadlocks (all remaining tasks waiting) and MUST allow tests to check ordering properties across interleavings.

**Note** — Real-time schedulability analysis — proving at compile time, from WCET bounds and task periods, that all tasks meet their deadlines — is a goal for later versions ([14-embedded](14-embedded.md)).

## 9.12 Open issues

1. **Duration of `task.LockThread`.** Provisional: until the task finishes; no unlock function (**conc.os.lock-thread**).
2. **`RWMutex` read access.** Provisional: `RLock()` yields a copy, consistent with value semantics (**conc.sync.rwmutex**).
