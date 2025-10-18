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
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/chzyer/readline"
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

// MenuModel модель главного меню с использованием Bubble Tea
type MenuModel struct {
	modules  []Installer
	selected int
	width    int
	height   int
}

// NewMenuModel создает новую модель главного меню
func NewMenuModel(modules []Installer) MenuModel {
	return MenuModel{
		modules:  modules,
		selected: 0,
		width:    80,
		height:   24,
	}
}

// Init инициализирует модель меню
func (m MenuModel) Init() bubbletea.Cmd {
	return nil
}

// Update обрабатывает сообщения для модели меню
func (m MenuModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.modules)-1 {
				m.selected++
			}
		case "enter":
			// Возвращаем выбранный модуль
			return m, func() bubbletea.Msg {
				return MenuSelectedMsg{Module: m.modules[m.selected]}
			}
		case "q", "ctrl+c":
			return m, bubbletea.Quit
		}
	case bubbletea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

// View отображает меню
func (m MenuModel) View() string {
	var view strings.Builder

	view.WriteString(ColorYellow + "==================================================\n" + ColorReset)
	view.WriteString(ColorYellow + "      МЕНЮ УСТАНОВЩИКА MYHORECA (golang)          \n" + ColorReset)
	view.WriteString(ColorYellow + "==================================================\n\n" + ColorReset)

	for i, mod := range m.modules {
		if i == m.selected {
			view.WriteString(ColorCyan + fmt.Sprintf(" ► %s\n", mod.MenuText()) + ColorReset)
		} else {
			view.WriteString(fmt.Sprintf("   %s\n", mod.MenuText()))
		}
	}

	view.WriteString("\n")
	view.WriteString(ColorYellow + " Q. Выход\n\n" + ColorReset)
	view.WriteString("↑/↓ - навигация • Enter - выбор • q - выход")

	return view.String()
}

// MenuSelectedMsg сообщение о выборе модуля
type MenuSelectedMsg struct {
	Module Installer
}

// SelectionModel модель для выбора элементов из списка
type SelectionModel struct {
	items         []string
	selected      int
	query         string
	filteredItems []string
	state         SelectionState
	width         int
	height        int
}

// SelectionState состояния модели выбора
type SelectionState int

const (
	SelectionBrowsing SelectionState = iota
	SelectionSearching
	SelectionSelected
	SelectionCancelled
)

// NewSelectionModel создает новую модель выбора
func NewSelectionModel(label string, items []string, showSearch bool) SelectionModel {
	model := SelectionModel{
		items:         items,
		filteredItems: items,
		selected:      0,
		state:         SelectionBrowsing,
		width:         80,
		height:        24,
	}

	if showSearch {
		model.state = SelectionSearching
	}

	return model
}

// Init инициализирует модель выбора
func (m SelectionModel) Init() bubbletea.Cmd {
	return nil
}

// Update обрабатывает сообщения для модели выбора
func (m SelectionModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		switch msg.String() {
		case "enter":
			if m.state == SelectionSearching {
				// В режиме поиска выбираем текущий элемент
				m.state = SelectionSelected
				return m, nil
			} else {
				// В режиме просмотра выбираем элемент
				m.state = SelectionSelected
				return m, nil
			}
		case "esc":
			if m.state == SelectionSearching {
				m.state = SelectionBrowsing
				m.query = ""
				m.updateFilteredItems()
			} else {
				m.state = SelectionCancelled
				return m, nil
			}
		case "ctrl+c":
			m.state = SelectionCancelled
			return m, nil
		case "up", "k":
			if m.state == SelectionBrowsing && m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.state == SelectionBrowsing && m.selected < len(m.filteredItems)-1 {
				m.selected++
			}
		}
	case bubbletea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

// View отображает модель выбора
func (m SelectionModel) View() string {
	var view strings.Builder

	if m.state == SelectionSearching {
		view.WriteString(fmt.Sprintf("Поиск: %s\n\n", m.query))

		if len(m.filteredItems) == 0 {
			view.WriteString("Нет результатов\n")
		} else {
			for i, item := range m.filteredItems {
				if i == m.selected {
					view.WriteString(fmt.Sprintf("► %s\n", item))
				} else {
					view.WriteString(fmt.Sprintf("  %s\n", item))
				}
			}
		}
	} else {
		view.WriteString("Выберите элемент:\n\n")
		for i, item := range m.filteredItems {
			if i == m.selected {
				view.WriteString(fmt.Sprintf("► %s\n", item))
			} else {
				view.WriteString(fmt.Sprintf("  %s\n", item))
			}
		}
	}

	view.WriteString("\n↑/↓ - навигация • Enter - выбор • Esc - назад")
	return view.String()
}

// updateFilteredItems обновляет отфильтрованные элементы
func (m *SelectionModel) updateFilteredItems() {
	if m.query == "" {
		m.filteredItems = m.items
	} else {
		m.filteredItems = make([]string, 0)
		queryLower := strings.ToLower(m.query)

		for _, item := range m.items {
			if strings.Contains(strings.ToLower(item), queryLower) {
				m.filteredItems = append(m.filteredItems, item)
			}
		}
	}
}

// GetSelectedItem возвращает выбранный элемент
func (m SelectionModel) GetSelectedItem() (string, bool) {
	if m.state == SelectionSelected && m.selected >= 0 && m.selected < len(m.filteredItems) {
		return m.filteredItems[m.selected], true
	}
	return "", false
}

// IsSelected проверяет, выбран ли элемент
func (m SelectionModel) IsSelected() bool {
	return m.state == SelectionSelected
}

// IsCancelled проверяет, отменен ли выбор
func (m SelectionModel) IsCancelled() bool {
	return m.state == SelectionCancelled
}

// ComponentSelectionModel модель для выбора компонентов дистрибутива
type ComponentSelectionModel struct {
	components    []config.DistroComponent
	selected      int
	query         string
	filteredItems []ComponentDisplay
	state         SelectionState
	width         int
	height        int
}

// ComponentDisplay представляет компонент для отображения
type ComponentDisplay struct {
	Component   config.DistroComponent
	DisplayText string
}

// NewComponentSelectionModel создает новую модель выбора компонентов
func NewComponentSelectionModel(label string, components []config.DistroComponent) ComponentSelectionModel {
	// Создаем элементы для отображения
	displayItems := make([]ComponentDisplay, len(components))
	for i, comp := range components {
		displayText := comp.MenuText
		if comp.PortableArchiveKey != "" {
			displayText += " (portable)"
		}
		displayItems[i] = ComponentDisplay{
			Component:   comp,
			DisplayText: displayText,
		}
	}

	return ComponentSelectionModel{
		components:    components,
		filteredItems: displayItems,
		selected:      0,
		state:         SelectionBrowsing,
		width:         80,
		height:        24,
	}
}

// Init инициализирует модель выбора компонентов
func (m ComponentSelectionModel) Init() bubbletea.Cmd {
	return nil
}

// Update обрабатывает сообщения для модели выбора компонентов
func (m ComponentSelectionModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		switch msg.String() {
		case "enter":
			m.state = SelectionSelected
			return m, nil
		case "esc":
			m.state = SelectionCancelled
			return m, nil
		case "ctrl+c":
			m.state = SelectionCancelled
			return m, nil
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.filteredItems)-1 {
				m.selected++
			}
		}
	case bubbletea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

// View отображает модель выбора компонентов
func (m ComponentSelectionModel) View() string {
	var view strings.Builder

	view.WriteString("Выберите компонент:\n\n")

	for i, item := range m.filteredItems {
		if i == m.selected {
			view.WriteString(fmt.Sprintf("► %s\n", item.DisplayText))
		} else {
			view.WriteString(fmt.Sprintf("  %s\n", item.DisplayText))
		}
	}

	view.WriteString("\n↑/↓ - навигация • Enter - выбор • Esc - назад")
	return view.String()
}

// GetSelectedComponent возвращает выбранный компонент
func (m ComponentSelectionModel) GetSelectedComponent() config.DistroComponent {
	if m.state == SelectionSelected && m.selected >= 0 && m.selected < len(m.filteredItems) {
		return m.filteredItems[m.selected].Component
	}
	return config.DistroComponent{}
}

// IsSelected проверяет, выбран ли компонент
func (m ComponentSelectionModel) IsSelected() bool {
	return m.state == SelectionSelected
}

// IsCancelled проверяет, отменен ли выбор
func (m ComponentSelectionModel) IsCancelled() bool {
	return m.state == SelectionCancelled
}

// PatchSelectionModel модель для выбора патчей
type PatchSelectionModel struct {
	patches       []core.PatchInfo
	selected      int
	query         string
	filteredItems []PatchDisplay
	state         SelectionState
	width         int
	height        int
}

// PatchDisplay представляет патч для отображения
type PatchDisplay struct {
	Patch       core.PatchInfo
	DisplayText string
}

// NewPatchSelectionModel создает новую модель выбора патчей
func NewPatchSelectionModel(label string, patches []core.PatchInfo) PatchSelectionModel {
	// Создаем элементы для отображения
	displayItems := make([]PatchDisplay, len(patches))
	for i, patch := range patches {
		displayItems[i] = PatchDisplay{
			Patch:       patch,
			DisplayText: buildPatchDisplayText(patch),
		}
	}

	return PatchSelectionModel{
		patches:       patches,
		filteredItems: displayItems,
		selected:      0,
		state:         SelectionBrowsing,
		width:         80,
		height:        24,
	}
}

// Init инициализирует модель выбора патчей
func (m PatchSelectionModel) Init() bubbletea.Cmd {
	return nil
}

// Update обрабатывает сообщения для модели выбора патчей
func (m PatchSelectionModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		switch msg.String() {
		case "enter":
			m.state = SelectionSelected
			return m, nil
		case "esc":
			m.state = SelectionCancelled
			return m, nil
		case "ctrl+c":
			m.state = SelectionCancelled
			return m, nil
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.filteredItems)-1 {
				m.selected++
			}
		}
	case bubbletea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

// View отображает модель выбора патчей
func (m PatchSelectionModel) View() string {
	var view strings.Builder

	view.WriteString("Выберите патч:\n\n")

	for i, item := range m.filteredItems {
		if i == m.selected {
			view.WriteString(fmt.Sprintf("► %s\n", item.DisplayText))
		} else {
			view.WriteString(fmt.Sprintf("  %s\n", item.DisplayText))
		}
	}

	view.WriteString("\n↑/↓ - навигация • Enter - выбор • Esc - назад")
	return view.String()
}

// GetSelectedPatch возвращает выбранный патч
func (m PatchSelectionModel) GetSelectedPatch() core.PatchInfo {
	if m.state == SelectionSelected && m.selected >= 0 && m.selected < len(m.filteredItems) {
		return m.filteredItems[m.selected].Patch
	}
	return core.PatchInfo{}
}

// IsSelected проверяет, выбран ли патч
func (m PatchSelectionModel) IsSelected() bool {
	return m.state == SelectionSelected
}

// IsCancelled проверяет, отменен ли выбор
func (m PatchSelectionModel) IsCancelled() bool {
	return m.state == SelectionCancelled
}

// InputModel модель для ввода текста
type InputModel struct {
	prompt string
	value  string
	state  InputState
	width  int
	height int
}

// InputState состояния модели ввода
type InputState int

const (
	InputWaiting InputState = iota
	InputEntered
	InputCancelled
)

// NewInputModel создает новую модель ввода текста
func NewInputModel(prompt string) InputModel {
	return InputModel{
		prompt: prompt,
		state:  InputWaiting,
		width:  80,
		height: 24,
	}
}

// Init инициализирует модель ввода
func (m InputModel) Init() bubbletea.Cmd {
	return nil
}

// Update обрабатывает сообщения для модели ввода
func (m InputModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		switch msg.String() {
		case "enter":
			m.state = InputEntered
			return m, nil
		case "esc", "ctrl+c":
			m.state = InputCancelled
			return m, nil
		case "backspace":
			if len(m.value) > 0 {
				m.value = m.value[:len(m.value)-1]
			}
		default:
			// Добавляем обычные символы
			if len(msg.String()) == 1 {
				m.value += msg.String()
			}
		}
	case bubbletea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

// View отображает модель ввода
func (m InputModel) View() string {
	var view strings.Builder

	view.WriteString(fmt.Sprintf("%s: %s", m.prompt, m.value))
	view.WriteString("\n\n")
	view.WriteString("Enter - подтвердить • Esc - отменить • Backspace - удалить символ")

	return view.String()
}

// GetValue возвращает введенное значение
func (m InputModel) GetValue() string {
	return m.value
}

// IsEntered проверяет, введено ли значение
func (m InputModel) IsEntered() bool {
	return m.state == InputEntered
}

// IsCancelled проверяет, отменен ли ввод
func (m InputModel) IsCancelled() bool {
	return m.state == InputCancelled
}

// GetUserInput получает ввод от пользователя используя Bubble Tea
func GetUserInput(prompt string) (string, error) {
	model := NewInputModel(prompt)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(model)
	finalModelInterface, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("ошибка запуска интерфейса ввода: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModelInterface.(InputModel)
	if resultModel.IsCancelled() {
		return "", fmt.Errorf("ввод отменен")
	}

	if !resultModel.IsEntered() {
		return "", fmt.Errorf("значение не введено")
	}

	return resultModel.GetValue(), nil
}

// ShowMenu отображает меню с использованием Bubble Tea
func ShowMenu(modules []Installer) (Installer, error) {
	if len(modules) == 0 {
		return nil, errors.New("список модулей пуст")
	}

	model := NewMenuModel(modules)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(model)
	_, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("ошибка запуска меню: %w", err)
	}

	// Извлекаем выбранный модуль из сообщения
	// Пока возвращаем первый модуль как заглушку
	// TODO: Реализовать правильную обработку выбора
	return modules[0], nil
}

// SelectWithSearch создает интерфейс выбора с поиском используя Bubble Tea
func SelectWithSearch(items []string, label string) (string, error) {
	if len(items) == 0 {
		return "", errors.New("список элементов пуст")
	}

	// Создаем модель выбора с поиском
	model := NewSelectionModel(label, items, true)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(model)
	finalModelInterface, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("ошибка запуска интерфейса выбора: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModelInterface.(SelectionModel)
	if resultModel.IsCancelled() {
		return "", ErrExitToMainMenu
	}

	selectedItem, ok := resultModel.GetSelectedItem()
	if !ok {
		return "", errors.New("элемент не выбран")
	}

	return selectedItem, nil
}

// SelectSimple создает простой интерфейс выбора используя Bubble Tea
func SelectSimple(items []string, label string) (string, error) {
	if len(items) == 0 {
		return "", errors.New("список элементов пуст")
	}

	// Создаем модель выбора без поиска
	model := NewSelectionModel(label, items, false)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(model)
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("ошибка запуска интерфейса выбора: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModel.(SelectionModel)
	if resultModel.IsCancelled() {
		return "", ErrExitToMainMenu
	}

	selectedItem, ok := resultModel.GetSelectedItem()
	if !ok {
		return "", errors.New("элемент не выбран")
	}

	return selectedItem, nil
}

// SelectComponent создает интерфейс выбора компонента дистрибутива используя Bubble Tea
func SelectComponent(components []config.DistroComponent, label string) (config.DistroComponent, error) {
	if len(components) == 0 {
		return config.DistroComponent{}, errors.New("список компонентов пуст")
	}

	// Создаем модель выбора с поиском
	model := NewComponentSelectionModel(label, components)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(model)
	finalModel, err := p.Run()
	if err != nil {
		return config.DistroComponent{}, fmt.Errorf("ошибка запуска интерфейса выбора: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModel.(ComponentSelectionModel)
	if resultModel.IsCancelled() {
		return config.DistroComponent{}, ErrExitToMainMenu
	}

	return resultModel.GetSelectedComponent(), nil
}

// SelectPatch создает интерфейс выбора патча используя Bubble Tea
func SelectPatch(patches []core.PatchInfo, label string) (core.PatchInfo, error) {
	if len(patches) == 0 {
		return core.PatchInfo{}, errors.New("список патчей пуст")
	}

	// Создаем модель выбора патчей
	model := NewPatchSelectionModel(label, patches)

	// Запускаем Bubble Tea приложение
	p := bubbletea.NewProgram(model)
	finalModel, err := p.Run()
	if err != nil {
		return core.PatchInfo{}, fmt.Errorf("ошибка запуска интерфейса выбора: %w", err)
	}

	// Извлекаем результат
	resultModel := finalModel.(PatchSelectionModel)
	if resultModel.IsCancelled() {
		return core.PatchInfo{}, ErrExitToMainMenu
	}

	return resultModel.GetSelectedPatch(), nil
}

// PatchDisplayInfo представляет информацию о патче для отображения в интерфейсе
type PatchDisplayInfo struct {
	Patch       core.PatchInfo
	DisplayText string
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
