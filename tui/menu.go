package tui

import (
	"errors"
	"fmt"
	"goMH/config"
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
	// Фильтруем bell символ (ASCII 7) который вызывает системные звуки
	filtered := make([]byte, 0, len(b))
	for _, ch := range b {
		if ch != 7 { // ASCII 7 = bell
			filtered = append(filtered, ch)
		}
	}
	if len(filtered) == 0 {
		return len(b), nil // Возвращаем оригинальную длину для корректного учета
	}
	return n.writer.Write(filtered)
}

func (n *noBellWriter) Close() error {
	if closer, ok := n.writer.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// originalReadlineStdout сохраняет оригинальный readline.Stdout для восстановления
var originalReadlineStdout io.WriteCloser

// DisableConsoleBeep отключает системные звуки в консоли
func DisableConsoleBeep() {
	originalReadlineStdout = readline.Stdout
	readline.Stdout = &noBellWriter{writer: readline.Stdout}
}

// RestoreConsoleBeep восстанавливает оригинальный readline.Stdout
func RestoreConsoleBeep() {
	if originalReadlineStdout != nil {
		readline.Stdout = originalReadlineStdout
		originalReadlineStdout = nil
	}
}

// Installer - локальный интерфейс, чтобы не импортировать main
type Installer core.Installer

// ClearScreen очищает консоль
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
	for {
		ClearScreen()
		fmt.Println(ColorYellow + "==================================================" + ColorReset)
		fmt.Println(ColorYellow + "      МЕНЮ УСТАНОВЩИКА MYHORECA (golang)          " + ColorReset)
		fmt.Println(ColorYellow + "==================================================" + ColorReset)
		fmt.Println()

		for i, mod := range modules {
			// Используем i+1 для нумерации 1..9
			// Если модулей > 9, то A, B, C... но пока предположим до 9
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

		// Эхо нажатой клавиши (опционально, но приятно видеть, что нажал)
		// fmt.Println(key)

		if strings.EqualFold(key, "q") {
			return nil, fmt.Errorf("пользователь выбрал выход")
		}

		var choiceInt int = -1

		// Пытаемся распарсить число
		if val, err := strconv.Atoi(key); err == nil {
			choiceInt = val
		} else {
			// Если это буква (для меню > 9)
			// Пока не реализуем сложную логику букв, так как модулей < 10
		}

		if choiceInt < 1 || choiceInt > len(modules) {
			// Неверный выбор, просто перерисовываем меню
			continue
		}

		return modules[choiceInt-1], nil
	}
}

// SelectWithSearch создает стрелочный интерфейс с поиском для выбора из списка строк
func SelectWithSearch(items []string, label string) (string, error) {
	if len(items) == 0 {
		return "", errors.New("список элементов пуст")
	}

	searcher := func(input string, index int) bool {
		// Проверяем ввод "00" для выхода в главное меню
		if input == "00" {
			return false
		}
		return strings.Contains(strings.ToLower(items[index]), strings.ToLower(input))
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
		// Проверяем, была ли нажата комбинация для выхода в главное меню
		if strings.Contains(err.Error(), "interrupt") {
			return "", ErrExitToMainMenu
		}
		return "", errors.New("выбор отменен")
	}

	return result, nil
}

// SelectSimple создает простой стрелочный интерфейс для выбора из списка строк
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
		// Проверяем, была ли нажата комбинация для выхода в главное меню
		if strings.Contains(err.Error(), "interrupt") {
			return "", ErrExitToMainMenu
		}
		return "", errors.New("выбор отменен")
	}

	return result, nil
}

// SelectComponent создает стрелочный интерфейс для выбора компонента дистрибутива
func SelectComponent(components []config.DistroComponent, label string) (config.DistroComponent, error) {
	if len(components) == 0 {
		return config.DistroComponent{}, errors.New("список компонентов пуст")
	}

	// Создаем список для отображения с дополнительной информацией
	type ComponentDisplay struct {
		Component   config.DistroComponent
		DisplayText string
	}

	var displayItems []ComponentDisplay
	for _, comp := range components {
		displayText := comp.MenuText
		if comp.PortableArchiveKey != "" {
			displayText += " (portable)"
		}
		displayItems = append(displayItems, ComponentDisplay{
			Component:   comp,
			DisplayText: displayText,
		})
	}

	// Создаем список строк для отображения
	itemStrings := make([]string, len(displayItems))
	for i, item := range displayItems {
		itemStrings[i] = item.DisplayText
	}

	searcher := func(input string, index int) bool {
		comp := displayItems[index].Component
		return strings.Contains(strings.ToLower(comp.MenuText), strings.ToLower(input)) ||
			strings.Contains(strings.ToLower(comp.ID), strings.ToLower(input))
	}

	DisableConsoleBeep()
	defer RestoreConsoleBeep()

	prompt := promptui.Select{
		Label:             label + " (Ctrl+C для выхода в главное меню)",
		Items:             itemStrings,
		StartInSearchMode: true,
		Searcher:          searcher,
	}

	index, _, err := prompt.Run()
	if err != nil {
		// Проверяем, была ли нажата комбинация для выхода в главное меню
		if strings.Contains(err.Error(), "interrupt") {
			return config.DistroComponent{}, ErrExitToMainMenu
		}
		return config.DistroComponent{}, errors.New("выбор отменен")
	}

	return displayItems[index].Component, nil
}

// SelectPatch создает стрелочный интерфейс для выбора патча с поиском
func SelectPatch(patches []core.PatchInfo, label string) (core.PatchInfo, error) {
	if len(patches) == 0 {
		return core.PatchInfo{}, errors.New("список патчей пуст")
	}

	// Создаем расширенную информацию о патчах для отображения
	var patchInfos []PatchDisplayInfo
	for _, patch := range patches {
		patchInfo := PatchDisplayInfo{
			Patch:       patch,
			DisplayText: buildPatchDisplayText(patch),
		}
		patchInfos = append(patchInfos, patchInfo)
	}

	// Создаем список строк для отображения
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
		Size:              10, // Ограничение количества отображаемых элементов
	}

	index, _, err := prompt.Run()
	if err != nil {
		// Проверяем, была ли нажата комбинация для выхода в главное меню
		if strings.Contains(err.Error(), "interrupt") {
			return core.PatchInfo{}, ErrExitToMainMenu
		}
		return core.PatchInfo{}, errors.New("выбор отменен")
	}

	return patchInfos[index].Patch, nil
}

// PatchDisplayInfo представляет информацию о патче для отображения в интерфейсе
type PatchDisplayInfo struct {
	Patch       core.PatchInfo
	DisplayText string
}

// buildPatchDisplayText создает форматированную строку для отображения патча
func buildPatchDisplayText(patch core.PatchInfo) string {
	var parts []string

	// Специальная обработка для SKIP
	if patch.ShortName == "SKIP" {
		return patch.Description
	}

	// Название патча
	parts = append(parts, patch.ShortName)

	// Номер сборки
	if patch.BuildNumber > 0 {
		parts = append(parts, fmt.Sprintf("(build %d)", patch.BuildNumber))
	}

	// Описание, если оно есть
	if patch.Description != "" && patch.Description != patch.ShortName {
		// Ограничиваем длину описания
		desc := patch.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		parts = append(parts, desc)
	}

	return strings.Join(parts, " ")
}
