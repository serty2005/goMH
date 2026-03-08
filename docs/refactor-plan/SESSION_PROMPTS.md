# Очередь Промптов

Ниже короткие версии промптов для запуска этапов в отдельных сессиях. Полные версии лежат в каталоге `prompts/`.

## Этап 1

Используй [prompts/01-runtime-and-logging.md](/c:/self/repos/goMH/docs/refactor-plan/prompts/01-runtime-and-logging.md).

Кратко:
- вынеси `console_queue.go` из корня;
- переделай `logging` в централизованный сервис с конфигом `logging.file_enabled`;
- убери `os.Exit`/`panic` из нижних слоев и верни управление в `main`;
- обеспечь запись стартов/завершений модулей в общий лог.

## Этап 2

Используй [prompts/02-task-runtime-and-log-streaming.md](/c:/self/repos/goMH/docs/refactor-plan/prompts/02-task-runtime-and-log-streaming.md).

Кратко:
- введи нормальный task runtime с `context.Context`;
- убери приватную модель отмены через ad-hoc интерфейсы;
- вынеси просмотр логов в отдельный параллельный сервис;
- сделай поддержку sink'ов: UI, консоль, файл, чистый `stdout`.

## Этап 3

Используй [prompts/03-module-contract-and-registry.md](/c:/self/repos/goMH/docs/refactor-plan/prompts/03-module-contract-and-registry.md).

Кратко:
- введи единый контракт модуля для конфигурации, сборки task spec и выполнения;
- убери type-switch по concrete modules;
- переведи регистрацию модулей на единый реестр;
- сделай одну точку постановки задач в очередь.

## Этап 4

Используй [prompts/04-single-queue-engine.md](/c:/self/repos/goMH/docs/refactor-plan/prompts/04-single-queue-engine.md).

Кратко:
- оставь один движок очереди для TUI и GUI;
- мигрируй GUI c собственного `TaskManager` на общий runtime;
- сохрани поведение `Exclusive`/параллельных задач;
- не потеряй текущий UX в очереди.

## Этап 5

Используй [prompts/05-cleanup-and-verification.md](/c:/self/repos/goMH/docs/refactor-plan/prompts/05-cleanup-and-verification.md).

Кратко:
- удалить дубли, мертвые API и глобальные output-hook’и;
- упростить крупные участки `tui` и queue plumbing;
- закрыть тестами ключевые сценарии;
- подготовить итоговый список миграционных решений.
