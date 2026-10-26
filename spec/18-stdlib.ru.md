# 18. Стандартная библиотека

Этот файл задаёт устройство стандартной библиотеки и состав её пакетов. Поведение отдельных пакетов описано в файлах, которым принадлежит соответствующая возможность (например, `an/sync` — в [09-concurrency](09-concurrency.ru.md), `an/secret` — в [11-data-safety](11-data-safety.ru.md)).

## 18.1 Расположение и версии

**std.path.root** — Путь импорта каждого пакета стандартной библиотеки ДОЛЖЕН начинаться с `an/`. Элемент пути `an` зарезервирован за стандартной библиотекой (см. [03-declarations](03-declarations.ru.md)).

**std.version.toolchain** — Стандартная библиотека поставляется вместе с инструментарием `anlang` и версионируется вместе с ним. Каждая версия `anlang` ДОЛЖНА предоставлять ровно одну версию каждого пакета стандартной библиотеки. Проект НЕ ДОЛЖЕН загружать, заменять или подменять стандартную библиотеку.

**std.source** — Стандартная библиотека ДОЛЖНА быть доступна компилятору в виде исходного кода на Anlang. Вывод эффектов, проверка контрактов и семантические запросы обрабатывают код стандартной библиотеки так же, как код проекта.

**Примечание** — Пакетного менеджера и внешних источников пакетов нет. Программа импортирует только пакеты `an/...` и пакеты своего проекта (см. [03-declarations](03-declarations.ru.md)).

## 18.2 Принципы

**std.profile.declared** — Каждый пакет стандартной библиотеки ДОЛЖЕН объявлять поддерживаемые профили строкой `profile` (см. [07-effects](07-effects.ru.md)).

**std.profile.effect-free** — Пакет стандартной библиотеки, у экспортируемых функций которого нет эффектов, ДОЛЖЕН объявлять все три профиля: `core`, `app` и `ui`.

**std.io.common** — Интерфейсы ввода-вывода `io.Reader` и `io.Writer` (пакет `an/io`) ДОЛЖНЫ быть одними и теми же типами во всех профилях. Их реализации зависят от цели.

**std.core.no-hidden-alloc** — В профиле `core` функция стандартной библиотеки НЕ ДОЛЖНА неявно выделять память. Функция, которой нужна память, ДОЛЖНА получать буфер от вызывающего.

**std.hw.ordinary** — Пакеты, описывающие железо и платформы (описания целей, описания архитектур, описания устройств, пакеты команд архитектур, привязки к платформам), ДОЛЖНЫ быть обычными пакетами на Anlang. Они НЕ ДОЛЖНЫ требовать особой поддержки в компиляторе сверх того, что задают [13-targets](13-targets.ru.md) и [14-embedded](14-embedded.ru.md).

**std.target.availability** — На конкретной цели пакет стандартной библиотеки можно использовать, только если его эффекты разрешены списками `Provides` и `Lacks` цели и профилем импортирующего пакета (см. [07-effects](07-effects.ru.md), [13-targets](13-targets.ru.md)).

## 18.3 Каталог пакетов

**std.catalog.required** — Реализация ДОЛЖНА предоставлять пакеты из следующей таблицы как минимум с указанной доступностью по профилям.

| Пакеты | Назначение | `core` | `app` | `ui` |
| --- | --- | --- | --- | --- |
| `an/math`, `an/bits`, `an/wrap`, `an/conv`, `an/decimal`, `an/fixed`, `an/simd` | числа | да | да | да |
| `an/hash`, `an/crc`, `an/crypto` | хэши и криптография | да | да | да |
| `an/strings`, `an/unicode`, `an/encoding` | текст и кодировки | да | да | да |
| `an/errors` | значения ошибок | да | да | да |
| `an/fmt` | форматирование | без аллокаций | да | да |
| `an/console` | стандартный ввод | да | да | да |
| `an/containers` | массивы, очереди, словари | фиксированного размера | да | да |
| `an/json`, `an/cbor`, `an/binary` | форматы данных | потоковые | да | да |
| `an/task`, `an/sync`, `an/atomic` | конкурентность | статические задачи | да | да |
| `an/time`, `an/rand` | время и случайность | если цель их даёт | да | да |
| `an/secret`, `an/validate` | безопасность данных | да | да | да |
| `an/fs`, `an/os` | файлы и процессы | только DOS | да | — |
| `an/net`, `an/http`, `an/tls` | сеть | — | да | через платформу |
| `an/sql` | базы данных | — | да | — |
| `an/ui`, `an/camera`, `an/location` | интерфейс и устройство | — | — | да |
| `an/device`, `an/gpio`, `an/uart` | периферия микроконтроллеров | да | — | — |
| `an/dos`, `an/bios` | DOS и BIOS | да | — | — |
| `an/kernel`, `an/mm`, `an/sched` | строительные блоки ОС | да | — | — |
| `an/ffi`, `an/js`, `an/meta` | взаимодействие и рефлексия | да | да | да |

В таблице:

- «без аллокаций», «фиксированного размера», «потоковые» и «статические задачи» означают, что в `core` пакет предоставляет ту часть своего API, которая удовлетворяет **std.core.no-hidden-alloc**.
- «если цель их даёт» означает, что в `core` пакетом можно пользоваться только на целях, описание которых даёт эффект `Clock` или `Random`.
- «только DOS» означает, что в `core` пакетами `an/fs` и `an/os` можно пользоваться только на платформе DOS.
- «через платформу» означает, что в `ui` сеть доступна средствами платформы на данной цели (например, сетевыми средствами браузера на `web`).

### 18.3.1 Пакеты, описанные в других файлах

Спецификация ссылается и на другие пакеты стандартной библиотеки. Они входят в стандартную библиотеку и описаны в указанных файлах.

| Пакет | Где описан |
| --- | --- |
| `an/io` | этот файл, **std.io.common** |
| `an/gen` | [10-compile-time](10-compile-time.ru.md) |
| `an/untrusted` | [11-data-safety](11-data-safety.ru.md) |
| `an/effect` | [07-effects](07-effects.ru.md) |
| `an/wcet` | [08-contracts](08-contracts.ru.md) |
| `an/arena` | [06-memory](06-memory.ru.md) |
| `an/unsafe` | [06-memory](06-memory.ru.md) |
| `an/target`, `an/arch`, `an/platform`, `an/arch/<архитектура>` | [13-targets](13-targets.ru.md) |
| `an/svd`, `an/interrupts`, `an/timer`, `an/cpu` | [14-embedded](14-embedded.ru.md) |
| `an/abi` | [15-os](15-os.ru.md) |
| `an/gpu` | [16-gpu](16-gpu.ru.md) |
| `an/app`, `an/cmd` | [17-ui](17-ui.ru.md) |
| `an/html` | [11-data-safety](11-data-safety.ru.md) |
| `an/log` | [11-data-safety](11-data-safety.ru.md) |
| `an/testing` | [20-toolchain](20-toolchain.ru.md) |
| `an/runtime` | [19-runtime](19-runtime.ru.md) |

### 18.3.2 Генерируемые пакеты

**std.generated.arch** — Для каждого описания архитектуры из этого описания генерируется пакет `an/arch/<архитектура>` (например, `an/arch/i8086`) (см. [13-targets](13-targets.ru.md)).

**std.generated.device** — Для цели, в описании которой есть поле `Device`, из описания устройства этой цели генерируется пакет `an/device` (см. [14-embedded](14-embedded.ru.md)). На целях без описания устройства импорт `an/device` — ошибка.

**std.generated.readable** — Генерируемые пакеты ДОЛЖНЫ быть доступны в виде читаемого исходного кода на Anlang, как любой другой пакет стандартной библиотеки.

### 18.3.3 Обязательные идентификаторы

**std.api.required** — Пакеты ниже ДОЛЖНЫ экспортировать как минимум перечисленные идентификаторы. Их семантика задана в указанном файле. Идентификаторы с пометкой *предварительно* отложены до реализации; шаг плана, на котором они фиксируются, указан в [open-issues](open-issues.ru.md).

| Пакет | Идентификаторы | Где задано |
| --- | --- | --- |
| `an/task` | `Go`, `Config`, `Low`, `Normal`, `High`, `Critical`, `Wait`, `ErrDeadline`, `Detach`, `Deadline`, `Cancel`, `Cancelled`, `Sleep`, `Yield`, `Run`, `LockThread` | [09-concurrency](09-concurrency.ru.md), [14-embedded](14-embedded.ru.md) |
| `an/sync` | `Mutex[T]` с `Lock`, `Load`; `RWMutex[T]` с `Lock`, `RLock`, `Load`; спин-блокировка для ядер (*предварительно* `SpinLock[T]`) | [09-concurrency](09-concurrency.ru.md), [15-os](15-os.ru.md) |
| `an/atomic` | `I8`…`I64`, `U8`…`U64`, `Bool` с `Load`, `Store`, `Add`, `Swap`, `CompareAndSwap` и порядками памяти | [09-concurrency](09-concurrency.ru.md), [15-os](15-os.ru.md) |
| `an/arena` | `Arena[T]` с `New`, `Edit`, `Get`, `Delete`; `Ref[T]` | [06-memory](06-memory.ru.md) |
| `an/effect` | `Effect`; константы базовых эффектов (`Alloc`, `Network`, …); `Declare`, `Mark`, `Defer`, `Without` | [07-effects](07-effects.ru.md) |
| `an/wcet` | `Within` | [08-contracts](08-contracts.ru.md), [14-embedded](14-embedded.ru.md) |
| `an/meta` | `Name`, `Fields`, `Variants`, `Field`, `Variant`, `Get`, `Set`, `Is`, `Make`, `Tag`, `Reject` | [10-compile-time](10-compile-time.ru.md) |
| `an/gen` | эффекты `ReadInput`, `WriteOutput` для генераторов; `ReadFile` | [10-compile-time](10-compile-time.ru.md) |
| `an/svd` | `Load`, `Parse`: описания устройств (`target.Device`) из SVD-файлов | [14-embedded](14-embedded.ru.md) |
| `an/secret` | `New`, `Equal`, `Expose`, `Map`, `ConstantTime`; эффект `ExposeSecret` | [11-data-safety](11-data-safety.ru.md) |
| `an/untrusted` | `Accept` | [11-data-safety](11-data-safety.ru.md) |
| `an/interrupts` | `Enable` | [14-embedded](14-embedded.ru.md) |
| `an/cpu` | `WaitForInterrupt` | [14-embedded](14-embedded.ru.md) |
| `an/bios` | `BootDrive`; `ClearScreen`, `SetCursor`, `Cursor`, `PutChar`, `WriteChar` (int 10h), `ReadKey`, `KeyReady` (int 16h), `Ticks` (int 1Ah) | [15-os](15-os.ru.md), [20-toolchain](20-toolchain.ru.md) |
| `an/dos` | `Print`, `PrintChar`, `Write`, `ReadChar`, `CharReady`, `Now`, `Time`, `Exit` (int 21h) | [20-toolchain](20-toolchain.ru.md) |
| `an/encoding` | `CP866` | [10-compile-time](10-compile-time.ru.md) |
| `an/unsafe` | `Map`, `Data`; `Flat`, `Near`, `Far`, `Program` | [06-memory](06-memory.ru.md) |
| `an/mm` | `PhysAddr`, `VirtAddr` | [15-os](15-os.ru.md) |
| `an/sched`, `an/kernel` | тип потока ядра с объявленным размером стека, данные на процессор (*предварительно*, имена не заданы) | [15-os](15-os.ru.md) |
| `an/gpu` | `Out[T]` с конструктором по длине и методом чтения результата (*предварительно*, имена не заданы), `Dispatch`, `GlobalID`, `GlobalSize`; `Atomic[T]`, `Shared[T]` — на будущее | [16-gpu](16-gpu.ru.md) |
| `an/ui` | `Node`, конструкторы узлов, узел нативного вида (*предварительно*) | [17-ui](17-ui.ru.md) |
| `an/app` | `Desc` и соответствие эффектов разрешениям платформ | [17-ui](17-ui.ru.md) |
| `an/errors` | `New`, `Is`, `As` | этот файл, §18.3.5 |
| `an/fmt` | `Print`, `Println`, `Printf`; `Fprint`, `Fprintln`, `Fprintf`; `Sprint`, `Sprintln`, `Sprintf` | этот файл, §18.3.4 |
| `an/console` | `ReadByte` | этот файл, §18.3.4 |
| `an/math` | функции контрактов `Sum`, `ForAll`, `Exists` | этот файл, §18.3.6 |

### 18.3.4 Печать

**std.fmt.print** — `fmt.Print`, `fmt.Println` и `fmt.Printf` пишут в стандартный вывод программы и ничего не возвращают. Если запись не удалась, они вызывают панику. Поэтому их эффекты включают `Console` и `Panic`.

**std.fmt.fprint** — `fmt.Fprint`, `fmt.Fprintln` и `fmt.Fprintf` принимают `io.Writer` первым аргументом и возвращают `(n int, err error)`, как в Go. На ошибку распространяется **stmt.error.unused** ([05-statements](05-statements.ru.md)).

**std.fmt.sprint** — `fmt.Sprint`, `fmt.Sprintln` и `fmt.Sprintf` возвращают `string` и не имеют эффекта `Console`.

**std.console.read** — `console.ReadByte()` читает следующий байт стандартного ввода программы и возвращает его с `true` или `0` и `false` в конце ввода. Она ждёт, пока байт появится, поэтому её эффекты — `Console` и `Block`. Ввод — клавиатура в DOS, консоль отладчика на платформе `bare`, дескриптор файла 0 на платформе `web` (**target.platform.web**) и стандартный ввод в интерпретаторе IR.

**Примечание** — Печать в стандартный вывод — самый частый случай, а сбой записи там редок и обычно неисправим, поэтому он вызывает панику, а не возвращает ошибку, которую каждому вызывающему пришлось бы отбрасывать. Запись в произвольный `io.Writer`, например в файл или сетевое соединение, возвращает ошибку, потому что там её нужно обрабатывать.

```go
fmt.Println("Hello, World!")                // no result; panics if stdout fails

if _, err := fmt.Fprintln(f, "line"); err != nil {
    return err
}
```

### 18.3.5 Ошибки

**std.errors.api** — `errors.New` и `errors.Is` ведут себя как в Go. `fmt.Errorf` с `%w` оборачивает ошибки, как в Go.

**std.errors.as** — Указателей в Anlang нет, поэтому `errors.As` имеет форму «значение, ok»: `func As[T error](err error) (T, bool)`. Она возвращает первую ошибку в цепочке `err`, динамический тип которой — `T`, и `true`; иначе — нулевое значение `T` и `false`. Поэтому `T` требует нулевого значения (**type.zero.param-constraint**, [02-types](02-types.ru.md)).

**std.errors.const** — `errors.New` с константным аргументом вычислима при компиляции (**ct.eval.evaluable**, [10-compile-time](10-compile-time.ru.md)). Поэтому ошибки-маркеры объявляются константами: переменные уровня пакета ограничены (**decl.var.package-kinds**, [03-declarations](03-declarations.ru.md)).

**std.errors.value** — У ошибки, которую возвращает `errors.New`, нет скрытой идентичности: две такие ошибки равны тогда и только тогда, когда равны их тексты. `errors.Is` сравнивает через `==`, разворачивая обёртки, как в Go.

```go
const ErrEmptyWord = errors.New("mydictionary: empty word")
```

**Примечание** — Одинаковые тексты в разных пакетах дают равные ошибки. Как и в Go, принято начинать текст с имени пакета.

**std.errors.enum** — Рекомендуемая форма ошибок, которые пакет возвращает вызывающим, — тип `enum` с методом `Error() string`. Идентичность ему даёт тип: значения разных типов `enum` никогда не равны, значения одного типа равны по **type.enum.comparable** ([02-types](02-types.ru.md)). Вызывающий проверяет вариант через `errors.Is` или разбирает все варианты исчерпывающим `switch` после `errors.As`; тогда новый вариант ломает каждый неполный `switch`.

```go
// Error is an error of package mydictionary.
type Error enum {
    EmptyWord
    TooLong{word string, max int}
}

func (e Error) Error() string {
    switch e {
    case EmptyWord:          return "mydictionary: empty word"
    case TooLong{word, max}: return fmt.Sprintf("mydictionary: %q is longer than %d", word, max)
    }
}

// caller
if errors.Is(err, mydictionary.EmptyWord) { ... }

if de, ok := errors.As[mydictionary.Error](err); ok {
    switch de { ... }             // exhaustive over the variants
}
```

**Примечание** — `anlang port` переводит Go-шную ошибку-маркер `var ErrX = errors.New("...")` в `const ErrX = errors.New("...")`; смысл не меняется, пока тексты маркеров не совпадают.

### 18.3.6 Функции контрактов

**std.math.contract** — `math.Sum(xs)` — сумма элементов среза или массива целых как математическое целое (**contract.expr.math**). Своего типа у неё нет: как нетипизированная константа, она принимает тип другого операнда, так что сравнивается со значением любого целого типа. Она МОЖЕТ встречаться только в выражениях контрактов и в аргументе `assert`; в других местах это ошибка. Пока стандартная библиотека не компилируется из исходников на Anlang, в `an/math` есть только функции контрактов.

**std.math.quantifier** — `math.ForAll(lo, hi, func(i T) bool { return p })` истинно, если p выполняется для каждого целого i с lo ≤ i < hi, а `math.Exists(lo, hi, func(i T) bool { return p })` — если для одного из них. T — целый тип, lo и hi преобразуются в T, а предикат — функциональный литерал, тело которого состоит из одного оператора `return`. Обе — функции контрактов (**std.math.contract**).

```go
func find(xs []int, x int) int {
    ensures result != -1 || math.ForAll(0, len(xs), func(j int) bool { return xs[j] != x })
    …
}
```
