# Glossary / Глоссарий

Terms used in the specification and their Russian equivalents. Use these terms in both languages; add a term here before using a new one.

Термины спецификации и их русские соответствия. Используйте их в обоих языках; новый термин сначала добавьте сюда.

| English | Русский | Meaning |
| --- | --- | --- |
| addressable | адресуемый | Operand that can receive temporary `inout` access: variables, slice and array elements, struct fields |
| architecture description | описание архитектуры | Anlang package with the machine model, instruction selection rules and formal instruction semantics |
| arena | арена | Container that owns graph nodes; nodes are referenced by `arena.Ref[T]` handles with generation checks |
| argument area | область аргументов | Lowest part of a stack frame, where a function places the arguments and receives the results of its calls |
| attribution (of effects) | атрибуция (эффектов) | Rule that an effect of a function passed as an argument is charged to the caller who passed it |
| basic block | базовый блок | Sequence of IR instructions entered only at its start and ending in a control instruction |
| binary format | формат бинарника | Layout of the output file: flat, MZ, ELF, PE, Mach-O, wasm, Intel HEX, UF2, BIN |
| borrow | заимствование | Exclusive `inout` access held for a longer region, as for `inout` arguments of `go` |
| boundary (of a type invariant) | граница (инварианта типа) | Point where a type invariant must hold: construction, end of an `inout` function, passing, returning, storing |
| built-in emulator | встроенный эмулятор | Architecture interpreter with peripheral and platform models, built into `anlang` |
| callback | колбэк | Anlang function passed to external code via `ffi.Callback` |
| canonical formatting | каноническое форматирование | The single source layout produced by `anlang fmt` |
| certificate (proof) | сертификат (доказательства) | Solver output that an independent checker verifies |
| comma-ok form | пара «значение, ok» | Result pair `(T, bool)` expressing a possibly absent value, as in Go |
| compile-time evaluation | вычисление при компиляции | Execution of an effect-free function by the compiler to produce a constant |
| compile-time tuple | кортеж времени компиляции | Arguments of a `...any` parameter, whose types are known at compile time |
| computed constant | вычисляемая константа | Constant whose value the compiler computes by evaluating calls and composite literals at compile time |
| conformance suite | набор соответствия | Tests that check each normative rule: "compiles" and "error with rule X" |
| constraint (on effects) | ограничение (на эффекты) | Set of effects allowed or denied at a boundary |
| contextual word | контекстное слово | Word with special meaning only in a specific position; otherwise an ordinary identifier |
| contract | контракт | `requires`, `ensures` or `invariant` clause |
| copy-on-write (COW) | копирование при записи | Sharing a buffer between copies until one of them is modified |
| critical section | критическая секция | Region with exclusive access to a resource shared between execution contexts |
| deadline | дедлайн | Time limit after which a task tree is cancelled |
| debug probe | отладочный адаптер | SWD/JTAG adapter connecting `anlang debug` to a device |
| deferred effect | отложенный эффект | Effect of work that a function describes and the runtime performs later, such as a `Cmd` |
| development build | отладочная сборка | Build by `anlang test`, `anlang dev`, `anlang debug` or `anlang run` with development options |
| diagnostic | диагностика | Structured compiler message: location, rule identifier, reason, fixes |
| edition | редакция | Language version fixed in `anlang.mod` |
| effect | эффект | Property of what a function may do: `Network`, `Alloc`, `Panic`, … |
| effect summary | сводка эффектов | Effects of a function as computed per package, including parametric references |
| enum variant | вариант enum | One alternative of an `enum` type, possibly carrying data |
| environment effect | эффект окружения | Effect that the target must provide: `File`, `Network`, `Console`, hardware effects, … |
| environment protocol | протокол среды | JSON-RPC 2.0 interface to all `anlang` tools |
| exclusivity | исключительность | Rule that a value under `inout` access is reachable through no other path |
| execution context | контекст исполнения | `main`, a task or an interrupt handler, for peripheral ownership |
| execution effects | эффекты исполнения | All effects that executing a function may have, including those of functions passed to it |
| exhaustive switch | исчерпывающий switch | `switch` over an `enum` that handles every variant |
| external function | внешняя функция | Function without a body bound to a foreign symbol with `=` |
| formal instruction semantics | формальная семантика команд | Machine-readable meaning of each instruction, in the style of Sail |
| frame slot | слот кадра | Location in a stack frame that holds one IR value or one storage cell |
| generator | генератор | Anlang program run at build time with access only to declared files |
| handle | дескриптор | Value that refers to a shared object; copies refer to the same object |
| handle type | тип-дескриптор | Type whose values are handles: synchronization types, `ffi.Handle` |
| hardware mapping | отображение железа | Value created by `unsafe.Map` that denotes memory-mapped hardware |
| hot reload | горячая перезагрузка | Applying code changes to a running program without restart |
| import path | путь импорта | `an/...` for the standard library, `<module>/...` for the project |
| initialization phase | фаза инициализации | Part of `main` before interrupts and tasks are enabled |
| inout loop variable | `inout`-переменная цикла | Range-over-func loop variable that receives `inout` access from the iterator |
| `inout` access | `inout`-доступ | Temporary exclusive mutable access to a caller's value |
| instantiation | инстанцирование | Substitution of type arguments for the type parameters of a generic function or type |
| instruction selection rules | правила выбора команд | Declarative mapping from IR operations to machine instructions |
| instruction template | шаблон команд | Assembler text of an instruction selection rule with placeholders for operands and labels |
| intermediate representation (IR) | промежуточное представление (IR) | The shared SSA form of a program, the same for all targets, between type checking and lowering |
| interrupt handler | обработчик прерывания | Function declared with `interrupt` |
| invariant (loop / type) | инвариант (цикла / типа) | Condition that holds at the loop head / at value boundaries |
| IR interpreter | интерпретатор IR | Program that executes the IR directly; the reference for backends (`anlang run -emu ir`) |
| layout engine | движок раскладки | Component that positions UI nodes in pixels or character cells |
| leading clause region | начальная область объявлений | Clauses (`invariant`, `layout`) at the start of a struct field list |
| leading contract region | начальная контрактная область | Contract clauses at the start of a function or loop body |
| logical copy | логическая копия | Copy required by value semantics, which the compiler may elide |
| lowering | понижение | Translation of IR to architecture-specific form |
| machine operation | машинная операция | Operation of the code generator, such as `add` of 16-bit values, that instruction selection rules implement |
| manifest | манифест | Application description constant (`app.Desc`) |
| module | модуль | Project root, named in `anlang.mod` |
| monomorphization | мономорфизация | Translation of a generic function or type into a separate copy for each list of type arguments |
| negative test | отрицательный тест | Conformance test that is expected to be rejected with a given set of rule identifiers |
| novelty inventory | перечень новизны | Closed list of all differences from Go |
| open reference | открытая ссылка | Part of an effect summary that stands for the effects of all implementations stored in data structures |
| oracle | оракул | Independent implementation used as a reference in tests |
| own effects | собственные эффекты | Effects charged to a function by attribution; checked by profiles and `anlang.mod` |
| ownership unit | единица владения | Peripheral instance or pin whose use is checked per execution context |
| package | пакет | Directory of source files; unit of visibility, profile, effects and caching |
| parametric reference | параметрическая ссылка | Part of an effect summary that stands for the effects of a parameter |
| peripheral | периферия | Hardware block of a device: timer, UART, GPIO pin |
| permission | разрешение | Platform permission derived from effects |
| platform | платформа | Environment description: effects provided, ABI, entry point |
| positive test | положительный тест | Conformance test that is expected to compile without errors |
| precondition / postcondition | предусловие / постусловие | `requires` / `ensures` clause |
| priority ceiling | потолок приоритета | Highest priority of all users of a lock, computed at compile time |
| priority ceiling protocol | протокол потолка приоритетов | Locking discipline where a lock raises its holder to the lock's priority ceiling |
| profile | профиль | Package-level set of effect restrictions: `core`, `app`, `ui` |
| project test | тест-проект | Conformance test that is a whole project directory with its own `anlang.mod` |
| proof obligation | обязательство доказательства | Condition the solver must prove |
| range-over-func | range-over-func | `for … range f` over an iterator function, as in Go 1.23 |
| reference program | эталонная программа | Program with known observable behavior on a target |
| release build | выпускная сборка | Build by `anlang build` without development options |
| renderer | рендер | Component that draws the UI tree: GPU, software or text |
| requirement | требование | Entry in `REQUIREMENTS` linked to tests |
| runtime package | пакет времени выполнения | `an/runtime`: Anlang functions that generated code calls for printing, panics and operations without rules |
| second-class reference | ссылка второго класса | Reference that cannot be stored or returned, such as `inout` access |
| secret-dependent | зависящий от секрета | Value or control flow computed from the contents of a `Secret[T]` |
| semantic diff | семантический дифф | Difference in API, effects and contracts between two versions of code |
| side table (debug) | отдельная отладочная таблица | Debug information kept outside the binary image, used to resolve stack traces |
| single-file test | однофайловый тест | Conformance test that is one source file of package `main` |
| sink | приёмник | Function that accepts only validated data: SQL text, path, HTML, shell command |
| solver | решатель | Built-in prover for contracts |
| stack frame | кадр стека | Memory of one function call: its slots and its argument area |
| standard library | стандартная библиотека | Packages under `an/`, shipped with the toolchain |
| static type switch | статический switch по типу | Type switch on a type-parameter value or `...any` element, resolved at compile time |
| storage cell | ячейка | IR location of a variable: created by `alloc`, read by `load`, written by `store` |
| structured concurrency | структурированная конкурентность | Rule that a function returns only after all tasks it started have finished |
| synchronization type | тип синхронизации | `chan T`, `sync.Mutex[T]`, `sync.RWMutex[T]`, `atomic.*` |
| target | цель | Combination of architecture, platform and binary format |
| target package | пакет цели | Package that declares a target description constant `Description` |
| task | задача | Unit of concurrent execution started with `go` |
| trap handler | обработчик ловушки | Function declared with `trap` |
| trap label | метка ловушки | Label to which the template of a checked operation jumps on overflow |
| type parameter | параметр-тип | Type name declared in the type parameter list of a generic function or type |
| type set | множество типов | The set of types that satisfy a type constraint |
| type without a zero value | тип без нулевого значения | Type for which `var x T` is rejected, because its zero value is invalid |
| validator | проверка (функция) | Function converting `Untrusted[T]` into a meaningful type |
| value semantics | семантика значений | Assignment and passing logically copy values |
| variant pattern | шаблон варианта | `case` pattern that matches an `enum` variant and binds its data |
| variant switch | switch по вариантам | `switch` over an `enum` value with variant patterns |
| wait point | точка ожидания | Place where a task may yield: channel operations, locks, sleeps, blocking calls |
| WCET | WCET (наихудшее время выполнения) | Worst-case execution time |
| wrapping type | циклический тип | Integer type of package `an/wrap` whose arithmetic is reduced modulo 2ⁿ and never overflows |
| zero value | нулевое значение | Value of `var x T` without initializer |
