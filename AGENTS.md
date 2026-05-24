# AGENTS.md

Инструкции для Codex и других coding agents, работающих с goMH.

## Назначение проекта

goMH - Windows-first мультитул для сотрудников техподдержки iiko/Syrve. Цель проекта: собрать установку дистрибутивов, обслуживание, диагностику, удаленный доступ, драйверы и вспомогательные утилиты в одном приложении, где актуализация версий и ссылок в основном делается через `config.json`.

Приложение рассчитано на запуск с правами администратора в Windows 10/11. Многие операции реально меняют систему: службы Windows, планировщик, реестр, Defender exclusions, COM-порты, установщики, файлы iiko/Syrve.

## Быстрый старт для агента

- Перед изменениями проверь состояние: `git status --short`.
- Не перетирай пользовательский `config.json`: это рабочий конфиг с актуальными ссылками и, возможно, чувствительными данными.
- Основная проверка после изменений: `go test ./...`.
- Сборка Windows-приложения: `go build -v -o goMH.exe .`.
- Automation smoke-команды:
  - `go run . automation list-operations`
  - `go run . automation run --config .\config.json --request .\request.json`
- Интерактивный TUI/GUI требует Windows и прав администратора; в обычной агентской сессии не запускай установочные действия без явного запроса.

## Текущая архитектура

- `main.go` - точка входа. Обрабатывает режимы:
  - `automation ...` без интерактивного интерфейса;
  - обычный TUI dashboard;
  - `-gui` для GUI;
  - `-module ... -resume ...` для возобновления iiko/Syrve и Regime после перезапуска.
- `config/` - структуры `config.json`, загрузка локального/удаленного конфига, дефолты логирования.
- `core/` - общие интерфейсы: `TaskContext`, `WinUtils`, `AssetManager`, `QueueModule`, `ModuleServices`.
- `modules/registry` - реестр модулей. Новый пользовательский модуль должен быть добавлен в `NewDefault()`.
- `modules/*` - доменные модули: дистрибутивы, FRPC, удаленный доступ, драйверы ФР, плагины iiko, обслуживание, UTM, VComCaster, Regime.
- `assetmgr/` - загрузка и кеширование ресурсов из `asset_catalog`, HTTP/FTP, прогресс, отмена загрузок.
- `taskqueue/` - очередь задач, статусы, прогресс, task-log, cancel/remove/clear.
- `app/modruntime` - общий runtime для TUI/GUI/automation. Он превращает `QueueModule` в задачу очереди или immediate action и прокидывает task-aware `AssetManager`/`WinUtils`.
- `app/consolequeue` - TUI dashboard поверх `taskqueue` и `modruntime.Service`.
- `gui/` - Walk GUI. Сейчас GUI частично включен: основной рабочий путь остается TUI.
- `app/automation` - non-interactive JSON contract для внешних оркестраторов. Документация: `docs/automation-cli.md`.
- `logging/` и `logstream/` - общий лог приложения и отдельный live-log просмотр.
- `app/platform` и `winutils/` - реальные Windows-операции. В тестах и новых слоях предпочитай зависеть от `core.WinUtils`, а не от конкретной реализации.

## Контракт модулей

Основной контракт - `core.QueueModule`:

- `ID()` - стабильный идентификатор модуля. Он используется в `config.modules`, registry, очереди и automation mappings.
- `MenuText()` - текст для UI.
- `ConfigureTask(ctx, services)` - собирает пользовательскую или automation-конфигурацию.
- `BuildTask(config)` - возвращает `core.ModuleTaskPlan`: queue/immediate, title, signature, exclusive, result, confirmation.
- `ExecuteTask(ctx, services, config)` - выполняет работу через `TaskContext`, `AssetManager`, `WinUtils`.

Правила для новых модулей:

- Держи UI-вопросы в `ConfigureTask`, а системные действия в `ExecuteTask`.
- Для долгих операций используй queue mode и пиши прогресс через `TaskContext`.
- Делай `Signature` достаточно уникальным, чтобы очередь могла ловить дубликаты.
- Для операций, которые нельзя ставить в очередь, используй `ModuleRunModeImmediate`; если нужен особый UI hook, реализуй `core.ImmediateModuleAction`.
- Не вызывай `winutils` напрямую из модуля, если можно использовать `services.WinUtils`.

## Automation CLI

Automation предназначен для saga/adapters и других внешних запусков:

- `stdout` - только финальный JSON response.
- `stderr` - диагностические/status JSON-lines.
- Контракт версии: `gomh.automation/v1`.
- Зарегистрированные операции находятся в `app/automation/registry.go`.
- Runner использует тот же `modules/registry`, `modruntime.Service` и `taskqueue`, что TUI/GUI.

При добавлении automation-операции:

- Добавь `OperationDefinition` в `NewDefaultRegistry()`.
- Добавь typed параметры и строгую валидацию.
- Не выводи human-only текст в `stdout`.
- Покрой happy path, validation error и dry-run тестами.
- Обнови `docs/automation-cli.md`.

## Конфигурация и ресурсы

`config.json` управляет списком модулей, путями, self-update, FTP, каталогом ресурсов и настройками доменных модулей.

Важные поля:

- `root_path` - рабочий корень, обычно `C:\MH`.
- `assets_cache_path` - кеш скачанных ресурсов.
- `modules` - какие модули доступны в TUI.
- `asset_catalog` - ключи ресурсов для `AssetManager`.
- `distro_config`, `frpc_config`, `fiscal_drivers_config`, `TeamViewerConfig`, `MaintenanceConfig`, `utm_config`.

Правила безопасности:

- Не печатай и не коммить реальные FTP/API credentials.
- Не меняй ссылки, версии, хеши и install args в `config.json` без явной задачи.
- Если нужен тестовый конфиг, создай отдельный fixture или временный файл, а не правь рабочий конфиг.

## Тестирование

Базовая команда:

```powershell
go test ./...
```

Дополнительные проверки по ситуации:

```powershell
go test ./app/automation ./app/modruntime ./taskqueue
go test ./modules/distro ./modules/serviceutils ./assetmgr
go build -v -o goMH.exe .
```

Для изменений в Windows-интеграциях проверяй не только unit-тесты, но и риск реального выполнения команд. Если действие может установить ПО, удалить файлы, изменить службы или реестр, сначала делай dry-run, mock/fake `WinUtils` или отдельный тестовый контур.

## CodeGraph

В проекте настроен CodeGraph MCP (`codegraph_*`). Используй его для структурных вопросов:

- карта проекта: `codegraph_files`
- контекст задачи: `codegraph_context`
- где определен символ: `codegraph_search`
- кто вызывает символ: `codegraph_callers`
- что вызывает символ: `codegraph_callees`
- последствия изменения: `codegraph_impact`
- исходник нескольких связанных символов: `codegraph_explore`

Не начинай с grep для поиска символов. `rg` оставь для буквального текста, лог-сообщений, комментариев и строковых констант.

Индекс может отставать примерно на 500 мс после записи файла; не делай мгновенный re-query сразу после apply_patch.

## Стиль изменений

- Следуй существующим пакетным границам. Не тащи UI в бизнес-логику и не тащи Windows-реализацию туда, где есть интерфейс.
- Go-версия проекта: `go 1.25.1`. Используй современные идиомы Go, но не делай массовых рефакторингов без задачи.
- После Go-правок запускай `gofmt` для измененных файлов.
- Комментарии оставляй только там, где они объясняют неочевидное поведение или Windows-specific причину.
- Логи и сообщения пользователю в проекте в основном на русском; сохраняй этот стиль.
- Ошибки оборачивай через `%w`, когда вызывающий код может сохранить причину.

## Зоны повышенного риска

- `main.go` сейчас совмещает запуск, self-update, resume, cleanup, signals и выбор UI. Править точечно.
- Self-update меняет исполняемый файл и оставляет `.old`; не трогай без отдельной проверки сценария перезапуска.
- Resume для `distro` и `regime` завязан на задачи планировщика и cleanup temp.
- `assetmgr` управляет повторными загрузками, прогрессом и cancelability; изменения могут затронуть TUI/GUI/automation одновременно.
- `config.json` может содержать реальные URL и учетные данные.
- GUI на `github.com/lxn/walk` Windows-specific; не ожидай полноценной проверки GUI вне Windows.

## Первичная карта качества

Что уже хорошо:

- Есть единый runtime для TUI/GUI/automation.
- `core` содержит интерфейсы, удобные для тестов и fake-реализаций.
- Automation contract документирован и покрыт тестами.
- Очередь задач и task-log выделены отдельно.
- Базовый `go test ./...` проходит.

Что стоит улучшать постепенно:

- Разгрузить `main.go` на отдельные пакеты запуска/bootstrapping.
- Увеличить покрытие модулей без тестов: FRPC, fiscal-drivers, Regime, registry, UTM, VComCaster, selfupdate.
- Нормализовать JSON naming в конфиге: часть полей использует snake_case, часть PascalCase.
- Завести безопасные fixtures для config-driven сценариев, чтобы не использовать рабочий `config.json` в тестах.
- Расширить automation operations для частых задач техподдержки, сохраняя стабильность `gomh.automation/v1`.
