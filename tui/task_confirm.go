package tui

import (
	"goMH/core"
	"strings"
)

func ConfirmTaskPlan(plan core.ModuleTaskPlan, config any) (bool, error) {
	confirmLabel := "Подтвердить"
	if plan.Mode == core.ModuleRunModeQueue {
		confirmLabel = "Добавить в очередь"
	} else if plan.Mode == core.ModuleRunModeImmediate {
		confirmLabel = "Запустить"
	}

	details := []string{"Проверьте параметры и подтвердите выполнение."}
	if provider, ok := config.(core.TaskConfirmationProvider); ok {
		confirmation := provider.TaskConfirmation()
		if len(confirmation.Details) > 0 {
			details = confirmation.Details
		}
		if strings.TrimSpace(confirmation.ConfirmLabel) != "" {
			confirmLabel = strings.TrimSpace(confirmation.ConfirmLabel)
		}
	}

	index, err := SelectItem([]ChoiceItem{
		{Title: confirmLabel},
		{Title: "Отмена"},
	}, SelectionConfig{
		Title:    plan.Task.Title,
		Subtitle: strings.Join(details, "\n"),
	})
	if err == ErrExitToMainMenu {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return index == 0, nil
}
