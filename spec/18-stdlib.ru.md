# 18. Стандартная библиотека

Этот файл задаёт устройство стандартной библиотеки и состав её пакетов. Поведение отдельных пакетов описано в файлах, которым принадлежит соответствующая возможность (например, `kern/sync` — в [09-concurrency](09-concurrency.ru.md), `kern/secret` — в [11-data-safety](11-data-safety.ru.md)).

## 18.1 Расположение и версии

**std.path.root** — Путь импорта каждого пакета стандартной библиотеки ДОЛЖЕН начинаться с `kern/`. Элемент пути `kern` зарезервирован за стандартной библиотекой (см. [03-declarations](03-declarations.ru.md)).

**std.version.toolchain** — Стандартная библиотека поставляется вместе с инструментарием `kern` и версионируется вместе с ним. Каждая версия `kern` ДОЛЖНА предоставлять ровно одну версию каждого пакета стандартной библиотеки. Проект НЕ ДОЛЖЕН загружать, заменять или подменять стандартную библиотеку.

**std.source** — Стандартная библиотека ДОЛЖНА быть доступна компилятору в виде исходного кода на Kern. Вывод эффектов, проверка контрактов и семантические запросы обрабатывают код стандартной библиотеки так же, как код проекта.

**Примечание** — Пакетного менеджера и внешних источников пакетов нет. Программа импортирует только пакеты `kern/...` и пакеты своего проекта (см. [03-declarations](03-declarations.ru.md)).

## 18.2 Принципы

**std.profile.declared** — Каждый пакет стандартной библиотеки ДОЛЖЕН объявлять поддерживаемые профили строкой `profile` (см. [07-effects](07-effects.ru.md)).

**std.profile.effect-free** — Пакет стандартной библиотеки, у экспортируемых функций которого нет эффектов, ДОЛЖЕН объявлять все три профиля: `core`, `app` и `ui`.

**std.io.common** — Интерфейсы ввода-вывода `io.Reader` и `io.Writer` (пакет `kern/io`) ДОЛЖНЫ быть одними и теми же типами во всех профилях. Их реализации зависят от цели.

**std.core.no-hidden-alloc** — В профиле `core` функция стандартной библиотеки НЕ ДОЛЖНА неявно выделять память. Функция, которой нужна память, ДОЛЖНА получать буфер от вызывающего.

**std.hw.ordinary** — Пакеты, описывающие железо и платформы (описания целей, описания архитектур, описания устройств, пакеты команд архитектур, привязки к платформам), ДОЛЖНЫ быть обычными пакетами на Kern. Они НЕ ДОЛЖНЫ требовать особой поддержки в компиляторе сверх того, что задают [13-targets](13-targets.ru.md) и [14-embedded](14-embedded.ru.md).

**std.target.availability** — На конкретной цели пакет стандартной библиотеки можно использовать, только если его эффекты разрешены списками `Provides` и `Lacks` цели и профилем импортирующего пакета (см. [07-effects](07-effects.ru.md), [13-targets](13-targets.ru.md)).

## 18.3 Каталог пакетов

**std.catalog.required** — Реализация ДОЛЖНА предоставлять пакеты из следующей таблицы как минимум с указанной доступностью по профилям.

| Пакеты | Назначение | `core` | `app` | `ui` |
| --- | --- | --- | --- | --- |
| `kern/math`, `kern/bits`, `kern/wrap`, `kern/conv`, `kern/decimal`, `kern/fixed`, `kern/simd` | числа | да | да | да |
| `kern/hash`, `kern/crc`, `kern/crypto` | хэши и криптография | да | да | да |
| `kern/strings`, `kern/unicode`, `kern/encoding` | текст и кодировки | да | да | да |
| `kern/errors` | значения ошибок | да | да | да |
| `kern/fmt` | форматирование | без аллокаций | да | да |
| `kern/containers` | массивы, очереди, словари | фиксированного размера | да | да |
| `kern/json`, `kern/cbor`, `kern/binary` | форматы данных | потоковые | да | да |
| `kern/task`, `kern/sync`, `kern/atomic` | конкурентность | статические задачи | да | да |
| `kern/time`, `kern/rand` | время и случайность | если цель их даёт | да | да |
| `kern/secret`, `kern/validate` | безопасность данных | да | да | да |
| `kern/fs`, `kern/os` | файлы и процессы | только DOS | да | — |
| `kern/net`, `kern/http`, `kern/tls` | сеть | — | да | через платформу |
| `kern/sql` | базы данных | — | да | — |
| `kern/ui`, `kern/camera`, `kern/location` | интерфейс и устройство | — | — | да |
| `kern/device`, `kern/gpio`, `kern/uart` | периферия микроконтроллеров | да | — | — |
| `kern/dos`, `kern/bios` | DOS и BIOS | да | — | — |
| `kern/kernel`, `kern/mm`, `kern/sched` | строительные блоки ОС | да | — | — |
| `kern/ffi`, `kern/js`, `kern/meta` | взаимодействие и рефлексия | да | да | да |

В таблице:

- «без аллокаций», «фиксированного размера», «потоковые» и «статические задачи» означают, что в `core` пакет предоставляет ту часть своего API, которая удовлетворяет **std.core.no-hidden-alloc**.
- «если цель их даёт» означает, что в `core` пакетом можно пользоваться только на целях, описание которых даёт эффект `Clock` или `Random`.
- «только DOS» означает, что в `core` пакетами `kern/fs` и `kern/os` можно пользоваться только на платформе DOS.
- «через платформу» означает, что в `ui` сеть доступна средствами платформы на данной цели (например, сетевыми средствами браузера на `web`).

### 18.3.1 Пакеты, описанные в других файлах

Спецификация ссылается и на другие пакеты стандартной библиотеки. Они входят в стандартную библиотеку и описаны в указанных файлах.

| Пакет | Где описан |
| --- | --- |
| `kern/io` | этот файл, **std.io.common** |
| `kern/gen` | [10-compile-time](10-compile-time.ru.md) |
| `kern/untrusted` | [11-data-safety](11-data-safety.ru.md) |
| `kern/effect` | [07-effects](07-effects.ru.md) |
| `kern/wcet` | [08-contracts](08-contracts.ru.md) |
| `kern/arena` | [06-memory](06-memory.ru.md) |
| `kern/unsafe` | [06-memory](06-memory.ru.md) |
| `kern/target`, `kern/arch`, `kern/platform`, `kern/arch/<архитектура>` | [13-targets](13-targets.ru.md) |
| `kern/svd`, `kern/interrupts`, `kern/timer`, `kern/cpu` | [14-embedded](14-embedded.ru.md) |
| `kern/abi` | [15-os](15-os.ru.md) |
| `kern/gpu` | [16-gpu](16-gpu.ru.md) |
| `kern/app`, `kern/cmd` | [17-ui](17-ui.ru.md) |
| `kern/html` | [11-data-safety](11-data-safety.ru.md) |
| `kern/log` | [11-data-safety](11-data-safety.ru.md) |
| `kern/testing` | [20-toolchain](20-toolchain.ru.md) |
| `kern/runtime` | [19-runtime](19-runtime.ru.md) |

### 18.3.2 Генерируемые пакеты

**std.generated.arch** — Для каждого описания архитектуры из этого описания генерируется пакет `kern/arch/<архитектура>` (например, `kern/arch/i8086`) (см. [13-targets](13-targets.ru.md)).

**std.generated.device** — Для цели, в описании которой есть поле `Device`, из описания устройства этой цели генерируется пакет `kern/device` (см. [14-embedded](14-embedded.ru.md)). На целях без описания устройства импорт `kern/device` — ошибка.

**std.generated.readable** — Генерируемые пакеты ДОЛЖНЫ быть доступны в виде читаемого исходного кода на Kern, как любой другой пакет стандартной библиотеки.

### 18.3.3 Обязательные идентификаторы

**std.api.required** — Пакеты ниже ДОЛЖНЫ экспортировать как минимум перечисленные идентификаторы. Их семантика задана в указанном файле. Идентификаторы с пометкой *предварительно* отложены до реализации; шаг плана, на котором они фиксируются, указан в [open-issues](open-issues.ru.md).

| Пакет | Идентификаторы | Где задано |
| --- | --- | --- |
| `kern/task` | `Go`, `Config`, `Low`, `Normal`, `High`, `Critical`, `Wait`, `ErrDeadline`, `Detach`, `Deadline`, `Cancel`, `Cancelled`, `Sleep`, `Yield`, `Run`, `LockThread` | [09-concurrency](09-concurrency.ru.md), [14-embedded](14-embedded.ru.md) |
| `kern/sync` | `Mutex[T]` с `Lock`, `Load`; `RWMutex[T]` с `Lock`, `RLock`, `Load`; спин-блокировка для ядер (*предварительно* `SpinLock[T]`) | [09-concurrency](09-concurrency.ru.md), [15-os](15-os.ru.md) |
| `kern/atomic` | `I8`…`I64`, `U8`…`U64`, `Bool` с `Load`, `Store`, `Add` и порядками памяти | [09-concurrency](09-concurrency.ru.md), [15-os](15-os.ru.md) |
| `kern/arena` | `Arena[T]` с `New`, `Edit`, `Get`, `Delete`; `Ref[T]` | [06-memory](06-memory.ru.md) |
| `kern/effect` | `Effect`; константы базовых эффектов (`Alloc`, `Network`, …); `Declare`, `Mark`, `Defer`, `Without` | [07-effects](07-effects.ru.md) |
| `kern/wcet` | `Within` | [08-contracts](08-contracts.ru.md), [14-embedded](14-embedded.ru.md) |
| `kern/meta` | `Name`, `Fields`, `Variants`, `Field`, `Variant`, `Get`, `Set`, `Is`, `Make`, `Tag`, `Reject` | [10-compile-time](10-compile-time.ru.md) |
| `kern/gen` | эффекты `ReadInput`, `WriteOutput` для генераторов | [10-compile-time](10-compile-time.ru.md) |
| `kern/secret` | `New`, `Equal`, `Expose`, `Map`, `ConstantTime`; эффект `ExposeSecret` | [11-data-safety](11-data-safety.ru.md) |
| `kern/untrusted` | `Accept` | [11-data-safety](11-data-safety.ru.md) |
| `kern/interrupts` | `Enable` | [14-embedded](14-embedded.ru.md) |
| `kern/bios` | `BootDrive` | [15-os](15-os.ru.md) |
| `kern/mm` | `PhysAddr`, `VirtAddr` | [15-os](15-os.ru.md) |
| `kern/sched`, `kern/kernel` | тип потока ядра с объявленным размером стека, данные на процессор (*предварительно*, имена не заданы) | [15-os](15-os.ru.md) |
| `kern/gpu` | `Out[T]` с конструктором по длине и методом чтения результата (*предварительно*, имена не заданы), `Dispatch`, `GlobalID`, `GlobalSize`; `Atomic[T]`, `Shared[T]` — на будущее | [16-gpu](16-gpu.ru.md) |
| `kern/ui` | `Node`, конструкторы узлов, узел нативного вида (*предварительно*) | [17-ui](17-ui.ru.md) |
| `kern/app` | `Desc` и соответствие эффектов разрешениям платформ | [17-ui](17-ui.ru.md) |
| `kern/errors` | `New`, `Is`, `As` | этот файл, §18.3.5 |
| `kern/fmt` | `Print`, `Println`, `Printf`; `Fprint`, `Fprintln`, `Fprintf`; `Sprint`, `Sprintln`, `Sprintf` | этот файл, §18.3.4 |

### 18.3.4 Печать

**std.fmt.print** — `fmt.Print`, `fmt.Println` и `fmt.Printf` пишут в стандартный вывод программы и ничего не возвращают. Если запись не удалась, они вызывают панику. Поэтому их эффекты включают `Console` и `Panic`.

**std.fmt.fprint** — `fmt.Fprint`, `fmt.Fprintln` и `fmt.Fprintf` принимают `io.Writer` первым аргументом и возвращают `(n int, err error)`, как в Go. На ошибку распространяется **stmt.error.unused** ([05-statements](05-statements.ru.md)).

**std.fmt.sprint** — `fmt.Sprint`, `fmt.Sprintln` и `fmt.Sprintf` возвращают `string` и не имеют эффекта `Console`.

**Примечание** — Печать в стандартный вывод — самый частый случай, а сбой записи там редок и обычно неисправим, поэтому он вызывает панику, а не возвращает ошибку, которую каждому вызывающему пришлось бы отбрасывать. Запись в произвольный `io.Writer`, например в файл или сетевое соединение, возвращает ошибку, потому что там её нужно обрабатывать.

```go
fmt.Println("Hello, World!")                // no result; panics if stdout fails

if _, err := fmt.Fprintln(f, "line"); err != nil {
    return err
}
```

### 18.3.5 Ошибки

**std.errors.api** — `errors.New` и `errors.Is` ведут себя как в Go. `fmt.Errorf` с `%w` оборачивает ошибки, как в Go.

**std.errors.as** — Указателей в Kern нет, поэтому `errors.As` имеет форму «значение, ok»: `func As[T error](err error) (T, bool)`. Она возвращает первую ошибку в цепочке `err`, динамический тип которой — `T`, и `true`; иначе — нулевое значение `T` и `false`. Поэтому `T` требует нулевого значения (**type.zero.param-constraint**, [02-types](02-types.ru.md)).

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

**Примечание** — `kern port` переводит Go-шную ошибку-маркер `var ErrX = errors.New("...")` в `const ErrX = errors.New("...")`; смысл не меняется, пока тексты маркеров не совпадают.
