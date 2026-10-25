# 21. Протокол среды

Все возможности `kern` доступны через один машиночитаемый протокол. ИИ-инструменты и редакторы пользуются им вместо разбора текстового вывода.

## 21.1 Транспорт

**proto.transport** — Протокол ДОЛЖЕН быть JSON-RPC 2.0 поверх стандартного ввода и вывода процесса `kern` с базовым кадрированием Language Server Protocol: перед каждым сообщением идут заголовок `Content-Length` и пустая строка.

**proto.servers** — `kern serve` ДОЛЖЕН реализовывать протокол среды (21.3–21.5). `kern lsp` ДОЛЖЕН реализовывать Language Server Protocol (21.6). Оба ДОЛЖНЫ работать с проектом, содержащим рабочий каталог процесса (см. [20-toolchain](20-toolchain.ru.md), **tool.project.root**).

**proto.cancel** — Оба сервера ДОЛЖНЫ поддерживать уведомление `$/cancelRequest`. Отменённый запрос ДОЛЖЕН возвращать ошибку JSON-RPC `RequestCancelled` (код -32800).

**proto.concurrency** — Сервер МОЖЕТ обрабатывать запросы параллельно. Результаты ДОЛЖНЫ быть такими же, как если бы запросы обрабатывались по одному в порядке поступления.

## 21.2 Общие типы

Структуры ниже записаны в нотации интерфейсов TypeScript, как в Language Server Protocol. Поля с `?` необязательны.

```typescript
interface Position {
    line: number;      // 1-based
    column: number;    // 1-based, in bytes of the UTF-8 encoding of the line
}

interface Range {
    start: Position;
    end: Position;     // exclusive
}

interface Location {
    file: string;      // path relative to the project root, with "/" separators
    range: Range;
}

interface TextEdit {
    file: string;
    range: Range;
    newText: string;
}

interface Fix {
    title: string;
    edits: TextEdit[];
}

interface Diagnostic {
    rule: string;                  // rule identifier from the specification, e.g. "mem.inout.exclusive"
    severity: "error" | "warning" | "info";
    location: Location;
    message: string;               // human-readable reason
    chain?: Location[];            // call chain that explains the violation, outermost first
    related?: { location: Location; message: string }[];
    fixes: Fix[];                  // may be empty
}
```

**proto.diag.shape** — Каждая диагностика `kern` ДОЛЖНА иметь структуру `Diagnostic`. Поле `rule` ДОЛЖНО содержать идентификатор нарушенного правила этой спецификации.

**proto.diag.chain** — Диагностика о недопустимом эффекте ДОЛЖНА содержать в `chain` цепочку вызовов от границы до источника эффекта (например, `TIM2 → buffer.Push → grow → Alloc`, см. [07-effects](07-effects.ru.md)).

**proto.diag.fix** — Применение всех правок любого одного исправления диагностики ДОЛЖНО устранять эту диагностику и НЕ ДОЛЖНО порождать новую диагностику уровня `error`.

**proto.paths** — Пути файлов в протоколе ДОЛЖНЫ быть относительны корня проекта и использовать разделитель `/`. На файлы стандартной библиотеки ДОЛЖНЫ ссылаться с префиксом `kern/`, за которым следуют путь пакета и имя файла.

## 21.3 Сеанс

**proto.initialize** — Первым запросом сеанса ДОЛЖЕН быть `initialize`. Его результат ДОЛЖЕН сообщать версию `kern`, редакцию проекта и версию схемы протокола:

```typescript
// request: initialize, params: {}
interface InitializeResult {
    kernVersion: string;
    edition: string;         // from kern.mod
    schemaVersion: string;
}
```

**proto.shutdown** — Запрос `shutdown` ДОЛЖЕН завершать сеанс; после этого сервер завершается с кодом 0.

## 21.4 Операции

**proto.ops** — `kern serve` ДОЛЖЕН предоставлять следующие методы.

| Метод | Назначение |
| --- | --- |
| `check` | Типы, эффекты, контракты; диагностики с идентификаторами правил и исправлениями |
| `query` | Кто вызывает, откуда эффект, что затронет изменение, недоказанные условия |
| `prove` | Доказать условие контракта или вернуть контрпример |
| `test` | Запуск тестов, в том числе во встроенных эмуляторах |
| `run` | Запуск программы на цели или в эмуляторе с возвратом наблюдаемого поведения |
| `debug/*` | Точки останова, пошаговое выполнение, чтение состояния |
| `diff` | Семантический дифф двух версий кода |
| `requirements` | Связь требований с тестами |

### 21.4.1 check

```typescript
// params
interface CheckParams { targets?: string[]; packages?: string[] }
// result
interface CheckResult { diagnostics: Diagnostic[] }
```

**proto.op.check** — `check` ДОЛЖЕН выполнять проверки `kern check` (см. [20-toolchain](20-toolchain.ru.md), **tool.cmd.check**) для текущего состояния файлов проекта и возвращать все диагностики, отсортированные по файлу, затем по позиции, затем по идентификатору правила.

### 21.4.2 query

```typescript
interface QueryParams {
    kind: "callers" | "effects" | "impact" | "unproven";
    symbol?: string;         // qualified name, e.g. "weather/server.Handle"
    location?: Location;
}
interface QueryResult {
    items: { location: Location; symbol?: string; effect?: string; chain?: Location[]; message?: string }[];
}
```

**proto.op.query** — `query` ДОЛЖЕН отвечать:

- `callers`: все места вызова символа;
- `effects`: все эффекты, достижимые из символа или места, каждый с цепочкой вызовов, которая его вносит; параметрические ссылки — отдельными элементами (см. [07-effects](07-effects.ru.md));
- `impact`: все объявления и места вызова, которые перестанут компилироваться при изменении сигнатуры символа;
- `unproven`: все обязательства доказательства в пределах указанного места, которые решатель не доказал, с причиной.

### 21.4.3 prove

```typescript
interface ProveParams { location: Location }       // the contract clause or assertion
interface ProveResult {
    status: "proved" | "refuted" | "unknown";
    counterexample?: { name: string; value: string }[];
    steps: number;           // solver steps used
    reason?: string;         // for "unknown": e.g. step limit reached
}
```

**proto.op.prove** — `prove` ДОЛЖЕН запускать решатель для обязательства доказательства в указанном месте (см. [08-contracts](08-contracts.ru.md)) и сообщать результат. Результат `refuted` ДОЛЖЕН содержать контрпример.

### 21.4.4 test

```typescript
interface TestParams { packages?: string[]; target?: string; emu?: string; filter?: string }
interface TestResult {
    tests: {
        package: string;
        name: string;
        status: "pass" | "fail" | "skip";
        failure?: {
            message: string;
            location?: Location;
            counterexample?: string;   // property-based tests
            seed?: string;
            interleaving?: string[];   // deterministic scheduler
        };
    }[];
    diagnostics: Diagnostic[];         // compile errors and requirement checks
}
```

**proto.op.test** — `test` ДОЛЖЕН работать как `kern test` с тем же выбором тестов (см. [20-toolchain](20-toolchain.ru.md), 20.9) и возвращать результаты по тестам, отсортированные по пакету и имени.

### 21.4.5 run

```typescript
interface RunParams { target?: string; emu?: string; args?: string[]; stdin?: string; maxSteps?: number }
interface RunResult {
    exitStatus?: number;
    stdout: string;
    stderr: string;
    observations: { kind: string; time?: string; data: unknown }[];  // pins, UART output, screens
    diagnostics: Diagnostic[];
}
```

**proto.op.run** — `run` ДОЛЖЕН собирать и запускать программу. В эмуляторе он ДОЛЖЕН возвращать наблюдения из [20-toolchain](20-toolchain.ru.md), **tool.emu.observable**. `maxSteps` ограничивает выполнение в эмуляторе; при достижении предела выполнение останавливается, и результат сообщает об этом в `stderr`.

### 21.4.6 debug

**proto.op.debug** — Отладчик ДОЛЖЕН быть доступен через следующие методы:

| Метод | Действие |
| --- | --- |
| `debug/start` | Запустить программу под отладчиком (параметры как `RunParams`, плюс `probe?: string`, `replay?: string`); возвращает идентификатор сеанса |
| `debug/breakpoints` | Задать полный список точек останова сеанса (места или имена функций) |
| `debug/continue` | Выполнять до точки останова, завершения или паники |
| `debug/step` | Шаг `into`, `over` или `out` |
| `debug/state` | Вернуть стек вызовов и значения переменных в кадре, показанные по **tool.debug.values** |
| `debug/stop` | Завершить сеанс |

### 21.4.7 diff

```typescript
interface DiffParams { old: string; new: string }   // directories
interface DiffResult {
    packages: {
        path: string;
        added: string[]; removed: string[];
        changed: { symbol: string; old: string; new: string }[];
        implementationChanged: string[];
        effects: { symbol: string; old: string[]; new: string[] }[];
        contracts: { symbol: string; change: "weakened-ensures" | "strengthened-ensures" | "weakened-requires" | "strengthened-requires" | "other"; old: string; new: string }[];
        external: { symbol: string; old: string; new: string }[];
    }[];
}
```

**proto.op.diff** — `diff` ДОЛЖЕН возвращать содержимое из [20-toolchain](20-toolchain.ru.md), **tool.diff.content**.

### 21.4.8 requirements

```typescript
interface RequirementsResult {
    requirements: { id: string; tests: { ref: string; resolved?: string; status?: "pass" | "fail" | "missing" }[] }[];
    diagnostics: Diagnostic[];
}
```

**proto.op.requirements** — `requirements` ДОЛЖЕН выполнять проверки из [20-toolchain](20-toolchain.ru.md), 20.5, запуская упомянутые тесты, и возвращать результат по каждому требованию.

## 21.5 Детерминизм и стабильность

**proto.determinism** — При одинаковых файлах проекта, одинаковой версии `kern` и одинаковом запросе результат ДОЛЖЕН быть побайтово одинаковым. Массивы в результатах ДОЛЖНЫ быть отсортированы так, как указано для каждой операции, а если порядок не указан — по месту в коде. Результаты НЕ ДОЛЖНЫ содержать меток времени и длительностей.

**proto.determinism.run** — **proto.determinism** распространяется на `run`, `test` и `debug/*`, когда программа выполняется в эмуляторе или под детерминированным планировщиком. Когда программа выполняется на машине разработчика или на устройстве, зависящие от окружения выходные данные сообщаются так, как они наблюдались.

**proto.stability** — В пределах редакции одно и то же нарушение ДОЛЖНО сообщаться с одним и тем же идентификатором правила. Идентификаторы правил никогда не используются повторно в другом смысле (см. [README](README.ru.md)).

**proto.schema** — JSON Schema каждого запроса, результата и уведомления `kern serve`, а также вывода `-json` каждой команды ДОЛЖНА публиковаться вместе со спецификацией и версионироваться вместе с редакцией. `initialize` сообщает её версию.

## 21.6 Language Server Protocol

**proto.lsp.version** — `kern lsp` ДОЛЖЕН реализовывать Language Server Protocol версии 3.17.

**proto.lsp.features** — `kern lsp` ДОЛЖЕН предоставлять как минимум:

- диагностики, у которых `code` равен идентификатору правила;
- действия с кодом вида `quickfix` для каждого исправления диагностики;
- переход к определению;
- подсказку при наведении с сигнатурой, выведенными эффектами (включая параметрические ссылки) и контрактами функции;
- форматирование с результатом `kern fmt`.

**proto.lsp.encoding** — `kern lsp` ДОЛЖЕН поддерживать кодировку позиций UTF-16, и ЕМУ СЛЕДУЕТ предлагать UTF-8, если клиент её поддерживает.
