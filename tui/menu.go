package tui

import (
	"errors"
	"fmt"
	"goMH/core"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/chzyer/readline"
	"github.com/manifoldco/promptui"
)

// ErrExitToMainMenu специальная ошибка для обозначения выхода в главное меню по вводу "00"
var ErrExitToMainMenu = errors.New("exit_to_main_menu")

// noBellWriter фильтрует bell символ (ASCII 7) для отключения системных звуков
type noBellWriter struct {
	writer io.Writer
}

func (n *noBellWriter) Write(b []byte) (int, error) {
	filtered := make([]byte, 0, len(b))
	for _, ch := range b {
		if ch != 7 { // ASCII 7 = bell
			filtered = append(filtered, ch)
		}
	}
	if len(filtered) == 0 {
		return len(b), nil
	}
	return n.writer.Write(filtered)
}

func (n *noBellWriter) Close() error {
	if closer, ok := n.writer.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

var originalReadlineStdout io.WriteCloser

func DisableConsoleBeep() {
	originalReadlineStdout = readline.Stdout
	readline.Stdout = &noBellWriter{writer: readline.Stdout}
}

func RestoreConsoleBeep() {
	if originalReadlineStdout != nil {
		readline.Stdout = originalReadlineStdout
		originalReadlineStdout = nil
	}
}

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

// ShowMenu - Главное меню (возвращает интерфейс модуля)
func ShowMenu(modules []Installer) (Installer, error) {
	for {
		ClearScreen()
		fmt.Println(ColorYellow + "==================================================" + ColorReset)
		fmt.Println(ColorYellow + "      МЕНЮ УСТАНОВЩИКА MYHORECA (golang)          " + ColorReset)
		fmt.Println(ColorYellow + "==================================================" + ColorReset)
		fmt.Println()

		for i, mod := range modules {
			key := strconv.Itoa(i + 1)
			if i >= 9 {
				key = string(rune('A' + (i - 9)))
			}
			fmt.Printf(" %s. %s\n", key, mod.MenuText())
		}
		fmt.Println()
		fmt.Println(" Q. Выход")
		fmt.Println()
		fmt.Print("Нажмите клавишу для выбора: ")

		key, err := ReadKey()
		if err != nil {
			return nil, fmt.Errorf("ошибка чтения ввода: %w", err)
		}

		if strings.EqualFold(key, "q") {
			return nil, fmt.Errorf("пользователь выбрал выход")
		}

		var choiceInt int = -1
		if val, err := strconv.Atoi(key); err == nil {
			choiceInt = val
		}

		if choiceInt < 1 || choiceInt > len(modules) {
			continue
		}

		return modules[choiceInt-1], nil
	}
}

// PrintMenu - Универсальное текстовое меню для выбора из списка строк.
// Возвращает индекс выбранного элемента (0..N-1) или -1, если выбрано "Назад" (0).
func PrintMenu(title string, items []string) (int, error) {
	for {
		ClearScreen()
		if title != "" {
			Title(title)
		}

		for i, item := range items {
			// i+1 для отображения (1..N)
			fmt.Printf(" %d. %s\n", i+1, item)
		}
		fmt.Println("\n 0. Назад")
		fmt.Print("Выберите пункт: ")

		key, err := ReadKey()
		if err != nil {
			return -1, err
		}

		if key == "0" {
			return -1, nil // Назад
		}

		choice, err := strconv.Atoi(key)
		if err != nil || choice < 1 || choice > len(items) {
			continue
		}

		return choice - 1, nil
	}
}

// SelectWithSearch создает стрелочный интерфейс с умным поиском (игнорирует точки для версий)
// ОСТАВЛЯЕМ ДЛЯ ВЫБОРА ВЕРСИЙ
func SelectWithSearch(items []string, label string) (string, error) {
	if len(items) == 0 {
		return "", errors.New("список элементов пуст")
	}

	searcher := func(input string, index int) bool {
		if input == "00" {
			return false
		}
		item := items[index]

		// 1. Обычный поиск подстроки
		if strings.Contains(strings.ToLower(item), strings.ToLower(input)) {
			return true
		}

		// 2. Поиск без точек (для версий типа 928 -> 9.2.8)
		inputClean := strings.ReplaceAll(input, ".", "")
		itemClean := strings.ReplaceAll(item, ".", "")
		return strings.Contains(strings.ToLower(itemClean), strings.ToLower(inputClean))
	}

	DisableConsoleBeep()
	defer RestoreConsoleBeep()

	prompt := promptui.Select{
		Label:             label + " (Ctrl+C для выхода в главное меню)",
		Items:             items,
		StartInSearchMode: true,
		Searcher:          searcher,
	}

	_, result, err := prompt.Run()
	if err != nil {
		if strings.Contains(err.Error(), "interrupt") {
			return "", ErrExitToMainMenu
		}
		return "", errors.New("выбор отменен")
	}

	return result, nil
}

// SelectSimple создает простой стрелочный интерфейс.
// Оставляем для простых выборов (Да/Нет), но не для главных меню.
func SelectSimple(items []string, label string) (string, error) {
	if len(items) == 0 {
		return "", errors.New("список элементов пуст")
	}

	DisableConsoleBeep()
	defer RestoreConsoleBeep()

	prompt := promptui.Select{
		Label: label + " (Ctrl+C для выхода в главное меню)",
		Items: items,
	}

	_, result, err := prompt.Run()
	if err != nil {
		if strings.Contains(err.Error(), "interrupt") {
			return "", ErrExitToMainMenu
		}
		return "", errors.New("выбор отменен")
	}

	return result, nil
}

// SelectPatch создает стрелочный интерфейс для выбора патча с поиском
func SelectPatch(patches []core.PatchInfo, label string) (core.PatchInfo, error) {
	if len(patches) == 0 {
		return core.PatchInfo{}, errors.New("список патчей пуст")
	}

	var patchInfos []PatchDisplayInfo
	for _, patch := range patches {
		patchInfo := PatchDisplayInfo{
			Patch:       patch,
			DisplayText: buildPatchDisplayText(patch),
		}
		patchInfos = append(patchInfos, patchInfo)
	}

	itemStrings := make([]string, len(patchInfos))
	for i, info := range patchInfos {
		itemStrings[i] = info.DisplayText
	}

	// Функция поиска по патчам
	searcher := func(input string, index int) bool {
		patch := patchInfos[index].Patch
		inputLower := strings.ToLower(input)
		return strings.Contains(strings.ToLower(patch.ShortName), inputLower) ||
			strings.Contains(strings.ToLower(patch.Description), inputLower) ||
			strings.Contains(strings.ToLower(patchInfos[index].DisplayText), inputLower)
	}

	DisableConsoleBeep()
	defer RestoreConsoleBeep()

	prompt := promptui.Select{
		Label:             label + " (Ctrl+C для выхода в главное меню)",
		Items:             itemStrings,
		StartInSearchMode: true,
		Searcher:          searcher,
		Size:              10,
	}

	index, _, err := prompt.Run()
	if err != nil {
		if strings.Contains(err.Error(), "interrupt") {
			return core.PatchInfo{}, ErrExitToMainMenu
		}
		return core.PatchInfo{}, errors.New("выбор отменен")
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
