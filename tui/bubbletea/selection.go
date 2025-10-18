package bubbletea

import (
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"goMH/config"
	"goMH/core"
)

// SelectWithSearch создает интерфейс выбора с поиском используя Bubble Tea
func SelectWithSearch(items []string, label string) (string, error) {
	// Конвертируем строки в SelectionItem
	selectionItems := make([]SelectionItem, len(items))
	for i, item := range items {
		selectionItems[i] = SelectionItem{
			Text:        item,
			Description: fmt.Sprintf("Элемент %d", i+1),
			Value:       item,
		}
	}

	// Создаем модель выбора с поиском
	model := NewSelectionModel(label, selectionItems, true)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(&model)
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("ошибка запуска интерфейса: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModel.(*SelectionModel)
	if resultModel.IsCancelled() {
		return "", fmt.Errorf("выбор отменен")
	}

	if !resultModel.IsSelected() {
		return "", fmt.Errorf("элемент не выбран")
	}

	selectedItem, ok := resultModel.GetSelectedItem()
	if !ok {
		return "", fmt.Errorf("не удалось получить выбранный элемент")
	}

	result, ok := selectedItem.Value.(string)
	if !ok {
		return "", fmt.Errorf("неверный тип выбранного значения")
	}

	return result, nil
}

// SelectSimple создает простой интерфейс выбора используя Bubble Tea
func SelectSimple(items []string, label string) (string, error) {
	// Конвертируем строки в SelectionItem
	selectionItems := make([]SelectionItem, len(items))
	for i, item := range items {
		selectionItems[i] = SelectionItem{
			Text:        item,
			Description: fmt.Sprintf("Элемент %d", i+1),
			Value:       item,
		}
	}

	// Создаем модель выбора без поиска
	model := NewSelectionModel(label, selectionItems, false)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(&model)
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("ошибка запуска интерфейса: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModel.(*SelectionModel)
	if resultModel.IsCancelled() {
		return "", fmt.Errorf("выбор отменен")
	}

	if !resultModel.IsSelected() {
		return "", fmt.Errorf("элемент не выбран")
	}

	selectedItem, ok := resultModel.GetSelectedItem()
	if !ok {
		return "", fmt.Errorf("не удалось получить выбранный элемент")
	}

	result, ok := selectedItem.Value.(string)
	if !ok {
		return "", fmt.Errorf("неверный тип выбранного значения")
	}

	return result, nil
}

// SelectComponent создает интерфейс выбора компонента дистрибутива используя Bubble Tea
func SelectComponent(components []config.DistroComponent, label string) (config.DistroComponent, error) {
	// Конвертируем компоненты в SelectionItem
	selectionItems := make([]SelectionItem, len(components))
	for i, comp := range components {
		displayText := comp.MenuText
		if comp.PortableArchiveKey != "" {
			displayText += " (portable)"
		}

		selectionItems[i] = SelectionItem{
			Text:        displayText,
			Description: fmt.Sprintf("ID: %s", comp.ID),
			Value:       comp,
		}
	}

	// Создаем модель выбора с поиском
	model := NewSelectionModel(label, selectionItems, true)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(&model)
	finalModel, err := p.Run()
	if err != nil {
		return config.DistroComponent{}, fmt.Errorf("ошибка запуска интерфейса: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModel.(*SelectionModel)
	if resultModel.IsCancelled() {
		return config.DistroComponent{}, fmt.Errorf("выбор отменен")
	}

	if !resultModel.IsSelected() {
		return config.DistroComponent{}, fmt.Errorf("компонент не выбран")
	}

	selectedItem, ok := resultModel.GetSelectedItem()
	if !ok {
		return config.DistroComponent{}, fmt.Errorf("не удалось получить выбранный компонент")
	}

	result, ok := selectedItem.Value.(config.DistroComponent)
	if !ok {
		return config.DistroComponent{}, fmt.Errorf("неверный тип выбранного значения")
	}

	return result, nil
}

// SelectPatch создает интерфейс выбора патча используя Bubble Tea
func SelectPatch(patches []core.PatchInfo, label string) (core.PatchInfo, error) {
	// Конвертируем патчи в SelectionItem
	selectionItems := make([]SelectionItem, len(patches))
	for i, patch := range patches {
		displayText := buildPatchDisplayText(patch)
		selectionItems[i] = SelectionItem{
			Text:        displayText,
			Description: patch.Description,
			Value:       patch,
		}
	}

	// Создаем модель выбора с поиском
	model := NewSelectionModel(label, selectionItems, true)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(&model)
	finalModel, err := p.Run()
	if err != nil {
		return core.PatchInfo{}, fmt.Errorf("ошибка запуска интерфейса: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModel.(*SelectionModel)
	if resultModel.IsCancelled() {
		return core.PatchInfo{}, fmt.Errorf("выбор отменен")
	}

	if !resultModel.IsSelected() {
		return core.PatchInfo{}, fmt.Errorf("патч не выбран")
	}

	selectedItem, ok := resultModel.GetSelectedItem()
	if !ok {
		return core.PatchInfo{}, fmt.Errorf("не удалось получить выбранный патч")
	}

	result, ok := selectedItem.Value.(core.PatchInfo)
	if !ok {
		return core.PatchInfo{}, fmt.Errorf("неверный тип выбранного значения")
	}

	return result, nil
}

// buildPatchDisplayText создает форматированную строку для отображения патча
func buildPatchDisplayText(patch core.PatchInfo) string {
	var parts []string

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

// GetUserInput получает ввод от пользователя используя Bubble Tea
func GetUserInput(prompt string) (string, error) {
	model := NewInputModel(prompt, false)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(&model)
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("ошибка запуска интерфейса ввода: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModel.(*InputModel)
	if resultModel.IsCancelled() {
		return "", fmt.Errorf("ввод отменен")
	}

	if !resultModel.IsEntered() {
		return "", fmt.Errorf("значение не введено")
	}

	return resultModel.GetValue(), nil
}

// GetPasswordInput получает пароль от пользователя используя Bubble Tea
func GetPasswordInput(prompt string) (string, error) {
	model := NewInputModel(prompt, true)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(&model)
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("ошибка запуска интерфейса ввода пароля: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModel.(*InputModel)
	if resultModel.IsCancelled() {
		return "", fmt.Errorf("ввод отменен")
	}

	if !resultModel.IsEntered() {
		return "", fmt.Errorf("пароль не введен")
	}

	return resultModel.GetValue(), nil
}
