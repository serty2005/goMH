package tui

import "testing"

func TestLoadingModelUpdatesVisibleStatus(t *testing.T) {
	model := &loadingModel[string]{subtitle: "Подготовка", spinnerFrames: []string{"|"}}
	updated, _ := model.Update(loadingStatusMsg("Проверка DAD"))
	result := updated.(*loadingModel[string])
	if result.subtitle != "Проверка DAD" {
		t.Fatalf("subtitle = %q, want %q", result.subtitle, "Проверка DAD")
	}
}
