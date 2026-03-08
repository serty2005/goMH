# План Рефакторинга

Этот каталог подготовлен как стартовый пакет для следующей сессии разработки.

Цели пакета:
- зафиксировать согласованный план рефакторинга;
- разложить работу на независимые этапы;
- дать готовые промпты для запуска каждого этапа в новой сессии;
- не потерять архитектурные договоренности и ограничения.

## Базовые договоренности

- Текущая целевая версия языка: `Go 1.25.1`.
- Функциональность ломать нельзя; допускается только управляемая миграция с промежуточной совместимостью.
- Логи не используются как источник состояния или бизнес-логики.
- Общий лог утилиты обязателен, но должен уметь отключаться в файл через конфиг.
- Просмотр логов должен стать отдельным параллельным механизмом, а не частным случаем queue-задачи.
- Код из `console_queue.go` должен уехать из корня проекта.
- Добавление новых модулей должно происходить через единый контракт и единый реестр, без type-switch по concrete types.
- Очередь выполнения должна иметь одну основную реализацию для TUI и GUI.

## Рекомендуемый порядок выполнения

1. `01-runtime-and-logging.md`
2. `02-task-runtime-and-log-streaming.md`
3. `03-module-contract-and-registry.md`
4. `04-single-queue-engine.md`
5. `05-cleanup-and-verification.md`

## Состав каталога

- [SESSION_PROMPTS.md](/c:/self/repos/goMH/docs/refactor-plan/SESSION_PROMPTS.md) — сводный список готовых промптов.
- [prompts/01-runtime-and-logging.md](/c:/self/repos/goMH/docs/refactor-plan/prompts/01-runtime-and-logging.md) — этап 1.
- [prompts/02-task-runtime-and-log-streaming.md](/c:/self/repos/goMH/docs/refactor-plan/prompts/02-task-runtime-and-log-streaming.md) — этап 2.
- [prompts/03-module-contract-and-registry.md](/c:/self/repos/goMH/docs/refactor-plan/prompts/03-module-contract-and-registry.md) — этап 3.
- [prompts/04-single-queue-engine.md](/c:/self/repos/goMH/docs/refactor-plan/prompts/04-single-queue-engine.md) — этап 4.
- [prompts/05-cleanup-and-verification.md](/c:/self/repos/goMH/docs/refactor-plan/prompts/05-cleanup-and-verification.md) — этап 5.

## Сквозные критерии качества

- `main` остается единственной точкой принятия решения о завершении процесса.
- Любой модуль пишет результат выполнения в общий лог утилиты.
- Любая отменяемая операция использует нормальный `context.Context`.
- Просмотр логов умеет работать как минимум с TUI/GUI и чистым `stdout`.
- Конфиг читает новый формат логирования и сохраняет совместимость с текущим `log_level` на переходный период.
- После каждого этапа проходят `go test ./...` и релевантные точечные проверки.
