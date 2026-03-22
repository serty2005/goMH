# Automation CLI Contract

## Назначение

`goMH automation` — это non-interactive CLI-режим для внешнего вызова из saga/adapters и других оркестраторов.

Цели режима:

- принимать стабильный JSON request;
- запускать существующие операции goMH без TUI/GUI;
- возвращать структурированный JSON result в `stdout`;
- писать только диагностические/status-сообщения в `stderr`;
- использовать текущий `app/modruntime` и `taskqueue`, а не отдельный runtime.

Текущая версия контракта: `gomh.automation/v1`.

## Запуск

Из файла:

```powershell
goMH.exe automation run --request .\request.json
```

Через `stdin`:

```powershell
Get-Content .\request.json | goMH.exe automation run --stdin
```

С явным конфигом:

```powershell
goMH.exe automation run --config .\config.json --request .\request.json
```

Список зарегистрированных операций:

```powershell
goMH.exe automation list-operations
```

## STDIO Contract

- `stdout`: только один финальный JSON response.
- `stderr`: только диагностические/status-сообщения.
- В automation mode не используются интерактивные prompt, `WaitForAnyKey`, GUI/TUI overlays и human-only текст в `stdout`.

Статус в `stderr` пишется JSON-line событиями вида:

```json
{"type":"automation_status","request_id":"req-123","operation_id":"serviceutils.collect_logs","status":"progress","message":"Сбор логов","progress":35}
```

## Request

Минимальный request:

```json
{
  "contract_version": "gomh.automation/v1",
  "request_id": "req-123",
  "operation_id": "serviceutils.collect_logs",
  "parameters": {
    "log_days": 7
  }
}
```

Поддерживаются поля:

```json
{
  "contract_version": "gomh.automation/v1",
  "request_id": "req-123",
  "correlation_id": "saga-step-42",
  "operation_id": "serviceutils.collect_logs",
  "module": "serviceutils",
  "action": "collect_logs",
  "parameters": {},
  "timeout_seconds": 600,
  "dry_run": false,
  "working_dir": "C:\\work\\job-42",
  "root_override": "C:\\MH",
  "log_options": {
    "include_entries": true,
    "task_log_path": "C:\\work\\job-42\\gomh-task-log.json"
  },
  "metadata": {
    "saga_id": "saga-001"
  }
}
```

Правила:

- `request_id` обязателен.
- Нужно указать либо `operation_id`, либо пару `module` + `action`.
- Если `contract_version` не передан, используется `gomh.automation/v1`.
- `parameters` строго валидируются под конкретную операцию.
- `dry_run=true` строит и валидирует plan без выполнения операции.

## Response

Успешный response:

```json
{
  "contract_version": "gomh.automation/v1",
  "status": "success",
  "request_id": "req-123",
  "correlation_id": "saga-step-42",
  "operation_id": "serviceutils.collect_logs",
  "module": "serviceutils",
  "action": "collect_logs",
  "started_at": "2026-03-23T10:00:00Z",
  "completed_at": "2026-03-23T10:00:05Z",
  "duration_ms": 5123,
  "exit_code": 0,
  "summary": "Операция успешно выполнена.",
  "result": {
    "mode": "queue",
    "dry_run": false,
    "plan": {
      "mode": "queue",
      "title": "Сбор логов за 7 дн.",
      "signature": "serviceutils|collect|7|...",
      "result": {
        "note": ""
      }
    },
    "module_result": {
      "note": "Задача добавлена в очередь.",
      "task_id": "task-000001"
    },
    "task": {
      "id": "task-000001",
      "module_id": "ServiceUtils",
      "title": "Сбор логов за 7 дн.",
      "state": "success",
      "progress": 100,
      "status": "Успешно завершено"
    }
  },
  "logs": {
    "entries": [
      {
        "timestamp": "2026-03-23T10:00:01Z",
        "level": "INFO",
        "message": "Начало задачи",
        "source": "taskqueue"
      }
    ]
  }
}
```

Ошибочный response:

```json
{
  "contract_version": "gomh.automation/v1",
  "status": "error",
  "request_id": "req-124",
  "operation_id": "serviceutils.view_log",
  "started_at": "2026-03-23T10:10:00Z",
  "completed_at": "2026-03-23T10:10:00Z",
  "duration_ms": 4,
  "exit_code": 4,
  "summary": "операция требует интерактивного либо долгоживущего сценария и пока не поддерживается в automation mode",
  "error": {
    "code": "unsupported_operation",
    "message": "операция требует интерактивного либо долгоживущего сценария и пока не поддерживается в automation mode"
  }
}
```

## Exit Codes

- `0`: успех.
- `2`: невалидный request или аргументы CLI.
- `3`: неизвестная операция.
- `4`: операция известна, но не поддерживается в automation mode.
- `5`: ошибка выполнения операции.
- `6`: timeout.
- `7`: внутренняя ошибка automation runtime.

## Versioning И Совместимость

- `contract_version` фиксирует внешний JSON-контракт.
- Текущая ветка совместимости: `gomh.automation/v1`.
- Для `v1` допускается только additive evolution:
  - добавление новых операций;
  - добавление новых необязательных полей;
  - расширение `result.metadata`;
  - расширение `logs`.
- Ломающие изменения должны оформляться новой версией контракта.

## Готовые Операции

Сейчас зарегистрированы:

- `serviceutils.clean_temp`
- `serviceutils.collect_logs`
- `distro.install_component`

Также явно помечена как unsupported в automation mode:

- `serviceutils.view_log`

## Параметры Операций

### `serviceutils.clean_temp`

`parameters` можно не передавать:

```json
{}
```

### `serviceutils.collect_logs`

```json
{
  "log_days": 7,
  "log_dirs": [
    "C:\\MH\\logs",
    "C:\\Users\\user\\AppData\\Roaming\\iiko\\CashServer\\logs"
  ]
}
```

Если `log_dirs` не передан, goMH попробует определить директории автоматически из конфигурации.

### `distro.install_component`

```json
{
  "brand": "iiko",
  "component_id": "iiko_front",
  "version": "9.4.6046.0",
  "patch": "front-101",
  "uninstall_old_version": false,
  "old_version": "",
  "run_auto_update_plugins": true
}
```

Пояснения:

- `brand`: `iiko` или `syrve`.
- `component_id`: ID компонента из `config.json`.
- `version`: обязателен для компонентов с шаблонным `URLTemplate`.
- `patch`: пока поддержан только для `brand=iiko`.

## Dry Run

Пример:

```json
{
  "request_id": "req-dry-run",
  "operation_id": "distro.install_component",
  "dry_run": true,
  "parameters": {
    "brand": "iiko",
    "component_id": "iiko_front",
    "version": "9.4.6046.0"
  }
}
```

В этом режиме goMH:

- валидирует request;
- резолвит operation;
- строит `ModuleTaskPlan`;
- не выполняет саму операцию.
