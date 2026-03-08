# Миграционные Решения После Финальной Зачистки

## Что Зафиксировано

- Единая очередь выполнения остается в `taskqueue` и используется и TUI, и GUI.
- Единый runtime модулей остается в `app/modruntime`.
- Просмотр логов остается отдельным механизмом через `logstream`, а не типом queue-задачи.
- Основной формат логирования в конфиге: `logging.level` и `logging.file_enabled`.
- `log_level` сохранен как временный алиас для совместимости со старыми конфигами.

## Что Удалено

- Глобальный mutable output-hook в `assetmgr`:
  `SetConsoleOutput`, `SetProgressCallbacks`, общий `console_output.go`.
- Глобальный mutable output-hook в `dependencies`:
  `SetConsoleOutput`, общий `console_output.go`.
- Глобальный mutable output-hook в `winutils`:
  `SetConsoleOutput`, общий `console_output.go`.
- Дублирующий GUI-контекст задачи:
  `gui.TaskGuiContext`.
- Дублирующие silent-контексты в runtime/queue:
  локальные `newSilentContext(...)` переведены на `core.NewSilentTaskContext(...)`.
- Мертвые экспортированные mutator-API очереди:
  `Queue.UpdateStatus`, `Queue.UpdateProgress`, `Queue.AppendLog`, `Queue.AppendLogLine`.
- Лишний console-wrapper в `app/consolequeue`:
  `consoleModule`.
- Глобальный `SharedLogStreamService` в `modules/serviceutils`.

## Что Еще Оставлено Временно

- `main.RealWinUtils` остается адаптером над пакетом `winutils`, потому что `core.WinUtils` по-прежнему задает интерфейс приложения, а не прямую зависимость на `winutils.Runtime`.
- Режим возобновления `Regime` логируется отдельно от общей очереди, потому что запускается вне queue-runtime и должен переживать рестарт процесса.
- Поле `Config.LogLevel` остается в структуре конфигурации как совместимый alias поверх `Config.Logging.Level`.

## Практический Эффект

- Логи задач больше не зависят от глобального состояния пакетов, а при `logging.file_enabled=false` продолжают идти через общий `slog` в консольный fallback.
- GUI и TUI используют один и тот же queue/task runtime.
- Queue-runtime больше не считает любую активную задачу отменяемой: безопасная отмена включается только на этапе скачивания.
- Очередь и лог-стрим теперь можно развивать независимо.
