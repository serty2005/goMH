package tui

import (
	"errors"
	"fmt"
	"goMH/core"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var (
	ErrExitToMainMenu        = errors.New("exit_to_main_menu")
	errEmptyItems            = errors.New("список элементов пуст")
	errSelectionCancelled    = errors.New("выбор отменен")
	selectSearchSubtitle     = "Введите часть названия для фильтрации"
	selectPatchSubtitle      = "Введите часть названия или build"
	defaultFilterPlaceholder = "Фильтр"
	showMenuTitle            = "Выберите модуль"
	showMenuSubtitle         = "Esc для выхода"
	errUserSelectedExit      = "пользователь выбрал выход"
	selectPatchOverlayLabel  = "патч-нот"
)

type Installer core.Installer

func ClearScreen() {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		_ = cmd.Run()
	} else {
		fmt.Print("\033[H\033[2J")
	}
}

func ShowMenu(modules []Installer) (Installer, error) {
	items := make([]ChoiceItem, 0, len(modules))
	for _, mod := range modules {
		items = append(items, ChoiceItem{Title: mod.MenuText()})
	}

	index, err := SelectItem(items, SelectionConfig{
		Title:       showMenuTitle,
		Subtitle:    showMenuSubtitle,
		Placeholder: defaultFilterPlaceholder,
		Search:      true,
	})
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(modules) {
		return nil, errors.New(errUserSelectedExit)
	}
	return modules[index], nil
}

func PrintMenu(title string, items []string) (int, error) {
	index, err := SelectStrings(cleanTitle(title), "", items, false)
	if err != nil {
		if err == ErrExitToMainMenu {
			return -1, nil
		}
		return -1, err
	}
	return index, nil
}

func SelectWithSearch(items []string, label string) (string, error) {
	if len(items) == 0 {
		return "", errEmptyItems
	}

	choices := make([]ChoiceItem, 0, len(items))
	for _, item := range items {
		filterValue := strings.ToLower(item + " " + strings.ReplaceAll(item, ".", ""))
		choices = append(choices, ChoiceItem{
			Title:       item,
			FilterValue: filterValue,
		})
	}

	index, err := SelectItem(choices, SelectionConfig{
		Title:       cleanTitle(label),
		Subtitle:    selectSearchSubtitle,
		Placeholder: defaultFilterPlaceholder,
		Search:      true,
	})
	if err != nil {
		if err == ErrExitToMainMenu {
			return "", ErrExitToMainMenu
		}
		return "", errSelectionCancelled
	}
	if index < 0 || index >= len(items) {
		return "", errSelectionCancelled
	}
	return items[index], nil
}

func SelectVersionWithSearch(items []string, label string) (string, error) {
	if len(items) == 0 {
		return "", errEmptyItems
	}

	choices := make([]ChoiceItem, 0, len(items))
	for _, item := range items {
		filterValue := strings.ToLower(item + " " + strings.ReplaceAll(item, ".", ""))
		choices = append(choices, ChoiceItem{
			Title:       item,
			FilterValue: filterValue,
		})
	}

	index, err := SelectItem(choices, SelectionConfig{
		Title:            cleanTitle(label),
		Subtitle:         selectSearchSubtitle,
		Placeholder:      defaultFilterPlaceholder,
		Search:           true,
		DisableShortcuts: true,
	})
	if err != nil {
		if err == ErrExitToMainMenu {
			return "", ErrExitToMainMenu
		}
		return "", errSelectionCancelled
	}
	if index < 0 || index >= len(items) {
		return "", errSelectionCancelled
	}
	return items[index], nil
}

func SelectSimple(items []string, label string) (string, error) {
	if len(items) == 0 {
		return "", errEmptyItems
	}

	index, err := SelectStrings(cleanTitle(label), "", items, false)
	if err != nil {
		if err == ErrExitToMainMenu {
			return "", ErrExitToMainMenu
		}
		return "", errSelectionCancelled
	}
	if index < 0 || index >= len(items) {
		return "", errSelectionCancelled
	}
	return items[index], nil
}

func SelectPatch(patches []core.PatchInfo, label string) (core.PatchInfo, error) {
	if len(patches) == 0 {
		return core.PatchInfo{}, errors.New("список патчей пуст")
	}

	var patchInfos []PatchDisplayInfo
	for _, patch := range patches {
		patchInfo := PatchDisplayInfo{
			Patch:       patch,
			DisplayText: patch.ShortName + " " + patch.Description,
		}
		patchInfos = append(patchInfos, patchInfo)
	}

	items := make([]ChoiceItem, 0, len(patchInfos))
	for _, info := range patchInfos {
		items = append(items, ChoiceItem{
			Title:       info.Patch.ShortName,
			Description: info.Patch.Description,
			FilterValue: strings.ToLower(info.DisplayText + " " + info.Patch.Description + " " + info.Patch.ShortName),
		})
	}

	index, err := SelectItem(items, SelectionConfig{
		Title:        cleanTitle(label),
		Subtitle:     selectPatchSubtitle,
		Placeholder:  defaultFilterPlaceholder,
		Search:       true,
		OverlayLabel: selectPatchOverlayLabel,
		Overlay: func(index int) (SelectionOverlayContent, bool, error) {
			if index < 0 || index >= len(patchInfos) {
				return SelectionOverlayContent{}, false, nil
			}
			patch := patchInfos[index].Patch
			if patch.ShortName == "SKIP" || strings.TrimSpace(patch.ChangeNote) == "" {
				return SelectionOverlayContent{}, false, nil
			}
			return SelectionOverlayContent{
				Title:    "Patch-note: " + patch.ShortName,
				Subtitle: patch.Description,
				Lines:    strings.Split(patch.ChangeNote, "\n"),
			}, true, nil
		},
	})
	if err != nil {
		if err == ErrExitToMainMenu {
			return core.PatchInfo{}, ErrExitToMainMenu
		}
		return core.PatchInfo{}, errSelectionCancelled
	}
	if index < 0 || index >= len(patchInfos) {
		return core.PatchInfo{}, errSelectionCancelled
	}
	return patchInfos[index].Patch, nil
}

type PatchDisplayInfo struct {
	Patch       core.PatchInfo
	DisplayText string
}

func buildPatchDisplayText(patch core.PatchInfo) string {
	if patch.ShortName == "SKIP" {
		return patch.Description
	}
	var parts []string
	parts = append(parts, patch.ShortName)
	if patch.BuildNumber > 0 {
		parts = append(parts, fmt.Sprintf("(build %d)", patch.BuildNumber))
	}
	if patch.Description != "" && patch.Description != patch.ShortName {
		desc := patch.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		parts = append(parts, desc)
	}
	return strings.Join(parts, " ")
}

func cleanTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.TrimPrefix(title, "---")
	title = strings.TrimSuffix(title, "---")
	return strings.TrimSpace(title)
}
