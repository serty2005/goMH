package networkdiag

import (
	"errors"
	"fmt"
	"goMH/core"
	"os"

	"goMH/tui"
)

func (m *Module) ExecuteImmediate(ctx core.TaskContext, services core.ModuleServices, config any) (core.ModuleActionResult, error) {
	cfg, err := toConfig(config)
	if err != nil {
		return core.ModuleActionResult{}, err
	}
	if cfg.Action != ActionTemporarySubnetAccess {
		return core.ModuleActionResult{}, errors.New("immediate action NetworkDiag поддерживается только для временного IPv4")
	}
	if services.WinUtils == nil {
		return core.ModuleActionResult{}, errors.New("WinUtils не инициализирован")
	}
	executablePath, err := os.Executable()
	if err != nil {
		return core.ModuleActionResult{}, fmt.Errorf("путь goMH.exe: %w", err)
	}

	session, err := tui.RunWithSpinnerStatus(
		"Временный доступ к подсети устройства",
		"Подготовка безопасной сетевой операции...",
		func(setStatus func(string)) (*TemporaryAccessSession, error) {
			spinnerCtx := &spinnerTaskContext{TaskContext: ctx, setStatus: setStatus}
			return StartTemporaryAccess(spinnerCtx, services.WinUtils, TemporaryAccessConfig{
				Interface:         cfg.TemporaryInterface,
				TargetIP:          cfg.TemporaryTargetIP,
				PreferredIP:       cfg.TemporaryIP,
				ExcludedAddresses: cfg.ExcludedAddresses,
				TTL:               defaultTemporaryAccessTTL,
				StateDir:          cfg.TransactionStateDir,
				ExecutablePath:    executablePath,
			})
		})
	if err != nil {
		return core.ModuleActionResult{}, err
	}
	ctx.Success(fmt.Sprintf("Временный адрес %s/24 активен. %s", session.Transaction.TemporaryIP, describeReachability(session.Reachability)))

	for {
		choice, selectErr := tui.SelectItem(temporarySessionMenuItems(), tui.SelectionConfig{
			Title:            "Временный доступ к подсети устройства",
			Subtitle:         temporarySessionSubtitle(&session.Transaction, session.Reachability),
			DisableShortcuts: true,
		})
		if selectErr != nil || choice < 0 {
			cleanupErr := runCleanupWithSpinner(ctx, services.WinUtils, session.TransactionPath, "user_cancelled")
			return core.ModuleActionResult{}, errors.Join(selectErr, cleanupErr)
		}

		switch choice {
		case 0:
			if err := services.WinUtils.OpenURL("http://" + session.Transaction.TargetIP.String()); err != nil {
				ctx.Warn("Не удалось открыть HTTP: " + err.Error())
			}
		case 1:
			result, err := tui.RunWithSpinner("Временный доступ к подсети устройства", "Проверка DAD, маршрута и доступности...", func() (refreshResult, error) {
				entry, reachability, refreshErr := RefreshTemporaryAccess(ctx.Context(), services.WinUtils, session.TransactionPath)
				return refreshResult{entry: entry, reachability: reachability}, refreshErr
			})
			if err != nil {
				ctx.Warn("Проверка временного доступа: " + err.Error())
				continue
			}
			session.Transaction.Entry = &result.entry
			session.Reachability = result.reachability
			ctx.Info("Результат проверки: " + describeReachability(result.reachability))
		case 2:
			transaction, err := tui.RunWithSpinner("Временный доступ к подсети устройства", "Перенос watchdog и продление временного IP...", func() (*temporaryAccessTransaction, error) {
				return ExtendTemporaryAccess(services.WinUtils, session.TransactionPath, executablePath, defaultTemporaryAccessTTL)
			})
			if err != nil {
				ctx.Warn("Не удалось продлить временный доступ: " + err.Error())
				continue
			}
			session.Transaction = *transaction
			ctx.Success("Временный доступ продлён на 30 минут.")
		case 3:
			if err := runCleanupWithSpinner(ctx, services.WinUtils, session.TransactionPath, "user_finished"); err != nil {
				return core.ModuleActionResult{}, err
			}
			ctx.Success("Временный IPv4 удалён. DHCP, gateway и DNS не изменялись.")
			return core.ModuleActionResult{Note: "Временный доступ завершён."}, nil
		}
	}
}

func temporarySessionMenuItems() []tui.ChoiceItem {
	return []tui.ChoiceItem{
		{Title: "Открыть HTTP", Description: "Открыть веб-интерфейс устройства"},
		{Title: "Проверить снова", Description: "Повторить DAD/route/reachability verification"},
		{Title: "Продлить на 30 минут", Description: "Сначала перенести watchdog, затем продлить NetIO lifetime"},
		{Title: "Завершить временный доступ", Description: "Удалить только созданный goMH IPv4"},
	}
}

func temporarySessionSubtitle(transaction *temporaryAccessTransaction, reachability ReachabilityResult) string {
	return fmt.Sprintf("Устройство: %s · интерфейс: %s\nВременный IP: %s/24 · DAD: %s · %s\nАвтоматическое удаление временного IP: %s",
		transaction.TargetIP,
		transaction.InterfaceAlias,
		transaction.TemporaryIP,
		dadStateLabel(transaction.Entry),
		describeReachability(reachability),
		transaction.ExpiresAt.Local().Format("02.01.2006 15:04:05"),
	)
}

type spinnerTaskContext struct {
	core.TaskContext
	setStatus func(string)
}

func (ctx *spinnerTaskContext) SetStatus(status string) {
	ctx.setStatus(status)
}

type refreshResult struct {
	entry        core.TemporaryIPv4Info
	reachability ReachabilityResult
}

func runCleanupWithSpinner(ctx core.TaskContext, system temporaryAccessSystem, transactionPath, reason string) error {
	_, err := tui.RunWithSpinnerStatus(
		"Завершение временного доступа",
		"Подготовка безопасного удаления...",
		func(setStatus func(string)) (struct{}, error) {
			spinnerCtx := &spinnerTaskContext{TaskContext: ctx, setStatus: setStatus}
			return struct{}{}, CleanupTemporaryAccess(spinnerCtx, system, transactionPath, reason)
		},
	)
	return err
}

func dadStateLabel(entry *core.TemporaryIPv4Info) string {
	if entry == nil {
		return "неизвестно"
	}
	switch entry.DADState {
	case core.IPAddressDADTentative:
		return "проверка"
	case core.IPAddressDADDuplicate:
		return "конфликт"
	case core.IPAddressDADDeprecated:
		return "deprecated"
	case core.IPAddressDADPreferred:
		return "Preferred"
	default:
		return "Invalid"
	}
}
