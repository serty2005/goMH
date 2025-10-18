package bubbletea

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	bubbletea "github.com/charmbracelet/bubbletea"
	"goMH/core"
)

// Screen представляет различные экраны приложения
type Screen int

const (
	// MainMenuScreen главный экран с меню модулей
	MainMenuScreen Screen = iota
	// ModuleScreen экран выполнения выбранного модуля
	ModuleScreen
	// ProgressScreen экран отображения прогресса операции
	ProgressScreen
	// ErrorScreen экран отображения ошибки
	ErrorScreen
)

// MainModel основная модель приложения Bubble Tea
type MainModel struct {
	// modules список доступных модулей для установки
	modules []core.Installer
	// currentScreen текущий активный экран
	currentScreen Screen
	// selectedModule индекс выбранного модуля в меню
	selectedModule int
	// progressTasks карта активных задач с прогрессом
	progressTasks map[string]*ProgressInfo
	// errorMsg сообщение об ошибке для отображения
	errorMsg error
	// width ширина терминала
	width int
	// height высота терминала
	height int
	// pendingModule модуль, ожидающий подтверждения выполнения
	pendingModule *core.Installer
	// moduleConfirmed флаг подтверждения выполнения модуля
	moduleConfirmed bool
	// activeModuleModel активная модель модуля для выполнения
	activeModuleModel bubbletea.Model
	// activeModuleID ID активного модуля
	activeModuleID string
	// assetManager менеджер ассетов для скачивания файлов
	assetManager core.AssetManager
	// winUtils утилиты для работы с Windows
	winUtils core.WinUtils
}

// ProgressInfo содержит информацию о прогрессе задачи
type ProgressInfo struct {
	// ID уникальный идентификатор задачи
	ID string
	// Progress текущее значение прогресса (0.0-1.0)
	Progress float64
	// Text описание текущего этапа
	Text string
	// StartTime время начала задачи
	StartTime int64
}

// NewMainModel создает новую основную модель с заданным списком модулей и зависимостями
func NewMainModel(modules []core.Installer, am core.AssetManager, wu core.WinUtils) MainModel {
	return MainModel{
		modules:           modules,
		currentScreen:     MainMenuScreen,
		selectedModule:    0,
		progressTasks:     make(map[string]*ProgressInfo),
		width:             80,
		height:            24,
		pendingModule:     nil,
		moduleConfirmed:   false,
		activeModuleModel: nil,
		activeModuleID:    "",
		assetManager:      am,
		winUtils:          wu,
	}
}

// GetModules возвращает список доступных модулей
func (m MainModel) GetModules() []core.Installer {
	return m.modules
}

// GetCurrentScreen возвращает текущий активный экран
func (m MainModel) GetCurrentScreen() Screen {
	return m.currentScreen
}

// GetSelectedModule возвращает индекс выбранного модуля
func (m MainModel) GetSelectedModule() int {
	return m.selectedModule
}

// GetSelectedModuleInstance возвращает экземпляр выбранного модуля
func (m MainModel) GetSelectedModuleInstance() core.Installer {
	if m.selectedModule >= 0 && m.selectedModule < len(m.modules) {
		return m.modules[m.selectedModule]
	}
	return nil
}

// SetScreen устанавливает активный экран
func (m *MainModel) SetScreen(screen Screen) {
	m.currentScreen = screen
}

// SetSelectedModule устанавливает выбранный модуль по индексу
func (m *MainModel) SetSelectedModule(index int) error {
	if index < 0 || index >= len(m.modules) {
		return fmt.Errorf("недопустимый индекс модуля: %d", index)
	}
	m.selectedModule = index
	return nil
}

// SetSize устанавливает размеры терминала
func (m *MainModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// GetSize возвращает текущие размеры терминала
func (m MainModel) GetSize() (int, int) {
	return m.width, m.height
}

// AddProgressTask добавляет новую задачу с прогрессом
func (m *MainModel) AddProgressTask(taskID, text string) {
	m.progressTasks[taskID] = &ProgressInfo{
		ID:   taskID,
		Text: text,
	}
}

// UpdateProgress обновляет прогресс задачи
func (m *MainModel) UpdateProgress(taskID string, progress float64, text string) {
	if task, exists := m.progressTasks[taskID]; exists {
		task.Progress = progress
		task.Text = text
	}
}

// RemoveProgressTask удаляет задачу из списка активных
func (m *MainModel) RemoveProgressTask(taskID string) {
	delete(m.progressTasks, taskID)
}

// GetProgressTasks возвращает все активные задачи с прогрессом
func (m MainModel) GetProgressTasks() map[string]*ProgressInfo {
	return m.progressTasks
}

// SetError устанавливает сообщение об ошибке
func (m *MainModel) SetError(err error) {
	m.errorMsg = err
	m.currentScreen = ErrorScreen
}

// ClearError очищает сообщение об ошибке
func (m *MainModel) ClearError() {
	m.errorMsg = nil
}

// SetPendingModule устанавливает модуль, ожидающий подтверждения
func (m *MainModel) SetPendingModule(module core.Installer) {
	m.pendingModule = &module
	m.moduleConfirmed = false
}

// GetPendingModule возвращает модуль, ожидающий подтверждения
func (m *MainModel) GetPendingModule() *core.Installer {
	return m.pendingModule
}

// ConfirmModule подтверждает выполнение модуля
func (m *MainModel) ConfirmModule() {
	m.moduleConfirmed = true
}

// IsModuleConfirmed проверяет, подтвержден ли модуль для выполнения
func (m *MainModel) IsModuleConfirmed() bool {
	return m.moduleConfirmed
}

// ClearModuleState очищает состояние модуля (используется при возврате в меню)
func (m *MainModel) ClearModuleState() {
	m.pendingModule = nil
	m.moduleConfirmed = false
	m.activeModuleModel = nil
	m.activeModuleID = ""
}

// GetError возвращает текущее сообщение об ошибке
func (m MainModel) GetError() error {
	return m.errorMsg
}

// SetActiveModuleModel устанавливает активную модель модуля
func (m *MainModel) SetActiveModuleModel(model bubbletea.Model, moduleID string) {
	m.activeModuleModel = model
	m.activeModuleID = moduleID
}

// GetActiveModuleModel возвращает активную модель модуля
func (m MainModel) GetActiveModuleModel() bubbletea.Model {
	return m.activeModuleModel
}

// GetActiveModuleID возвращает ID активного модуля
func (m MainModel) GetActiveModuleID() string {
	return m.activeModuleID
}

// HasActiveModuleModel проверяет, есть ли активная модель модуля
func (m MainModel) HasActiveModuleModel() bool {
	return m.activeModuleModel != nil
}

// CreateModuleModel создает модель модуля по его ID
func (m *MainModel) CreateModuleModel(moduleID string, am core.AssetManager, wu core.WinUtils, moduleIndex int) (bubbletea.Model, error) {
	switch moduleID {
	case "utm":
		return NewUTMModel(am, wu, moduleIndex), nil
	case "distro":
		return NewDistroModel(am, wu), nil
	case "fiscal-drivers":
		// Здесь будет создание модели для фискальных драйверов
		return nil, fmt.Errorf("модель для модуля %s еще не реализована", moduleID)
	case "frpc":
		// Здесь будет создание модели для FRPC
		return nil, fmt.Errorf("модель для модуля %s еще не реализована", moduleID)
	case "iiko-plugins":
		// Здесь будет создание модели для плагинов iiko
		return nil, fmt.Errorf("модель для модуля %s еще не реализована", moduleID)
	case "regime":
		// Здесь будет создание модели для режима
		return nil, fmt.Errorf("модель для модуля %s еще не реализована", moduleID)
	case "remoteaccess":
		// Здесь будет создание модели для удаленного доступа
		return nil, fmt.Errorf("модель для модуля %s еще не реализована", moduleID)
	case "serviceutils":
		// Здесь будет создание модели для сервисных утилит
		return nil, fmt.Errorf("модель для модуля %s еще не реализована", moduleID)
	case "vcomcaster":
		// Здесь будет создание модели для VCOMCaster
		return nil, fmt.Errorf("модель для модуля %s еще не реализована", moduleID)
	default:
		return nil, fmt.Errorf("неизвестный модуль: %s", moduleID)
	}
}

// IsModuleScreen проверяет, находится ли модель на экране модуля
func (m MainModel) IsModuleScreen() bool {
	return m.currentScreen == ModuleScreen
}

// IsMainMenu проверяет, находится ли модель в главном меню
func (m MainModel) IsMainMenu() bool {
	return m.currentScreen == MainMenuScreen
}

// IsProgressScreen проверяет, находится ли модель на экране прогресса
func (m MainModel) IsProgressScreen() bool {
	return m.currentScreen == ProgressScreen
}

// IsErrorScreen проверяет, находится ли модель на экране ошибки
func (m MainModel) IsErrorScreen() bool {
	return m.currentScreen == ErrorScreen
}

// GetModuleMenuText возвращает текст меню для указанного модуля
func (m MainModel) GetModuleMenuText(index int) string {
	if index >= 0 && index < len(m.modules) {
		return m.modules[index].MenuText()
	}
	return ""
}

// GetModuleID возвращает ID модуля по индексу
func (m MainModel) GetModuleID(index int) string {
	if index >= 0 && index < len(m.modules) {
		return m.modules[index].ID()
	}
	return ""
}

// SetModules устанавливает список доступных модулей
func (m *MainModel) SetModules(modules []core.Installer) {
	m.modules = modules
	// Сбрасываем выбранный модуль, если он выходит за границы
	if m.selectedModule >= len(modules) {
		m.selectedModule = 0
	}
}

// SelectNextModule выбирает следующий модуль в списке
func (m *MainModel) SelectNextModule() {
	if len(m.modules) == 0 {
		return
	}
	m.selectedModule = (m.selectedModule + 1) % len(m.modules)
}

// SelectPrevModule выбирает предыдущий модуль в списке
func (m *MainModel) SelectPrevModule() {
	if len(m.modules) == 0 {
		return
	}
	m.selectedModule = (m.selectedModule - 1 + len(m.modules)) % len(m.modules)
}

// Init инициализирует модель при запуске приложения
// Возвращает команду для первоначальной настройки (может быть nil)
func (m MainModel) Init() bubbletea.Cmd {
	// Если есть активная модель модуля, инициализируем её
	if m.HasActiveModuleModel() {
		return m.activeModuleModel.Init()
	}

	// Инициализация модели - дополнительных команд не требуется
	return nil
}

// Update обрабатывает входящие сообщения и обновляет состояние модели
// Это основной метод обработки событий в Bubble Tea
func (m MainModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	var cmd bubbletea.Cmd

	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		// Обработка нажатий клавиш
		switch msg.String() {
		case "q", "ctrl+c":
			// Выход из приложения
			return m, bubbletea.Quit
		case "up", "k":
			// Переход к предыдущему модулю в главном меню
			if m.currentScreen == MainMenuScreen {
				m.SelectPrevModule()
			}
		case "down", "j":
			// Переход к следующему модулю в главном меню
			if m.currentScreen == MainMenuScreen {
				m.SelectNextModule()
			}
		case "enter":
			// Выбор модуля или подтверждение действия
			if m.currentScreen == MainMenuScreen && len(m.modules) > 0 {
				// Выбор модуля - устанавливаем pending модуль для подтверждения
				selectedModule := m.GetSelectedModuleInstance()
				if selectedModule != nil {
					m.SetPendingModule(selectedModule)
					m.SetScreen(ModuleScreen)
				}
			} else if m.currentScreen == ModuleScreen && m.pendingModule != nil {
				// Подтверждение выполнения модуля
				m.ConfirmModule()
				selectedModule := m.GetSelectedModuleInstance()
				if selectedModule != nil {
					cmd = func() bubbletea.Msg {
						return NewModuleConfirmMsg(selectedModule.ID(), m.selectedModule)
					}
				}
			}
		case "esc":
			// Возврат в главное меню или отмена подтверждения модуля
			if m.currentScreen == ModuleScreen && m.pendingModule != nil && !m.moduleConfirmed {
				// В экране модуля без подтверждения - возврат в меню
				m.SetScreen(MainMenuScreen)
				m.ClearModuleState()
				cmd = func() bubbletea.Msg {
					return NewBackToMenuMsg()
				}
			} else if m.currentScreen != MainMenuScreen {
				// В других экранах - возврат в главное меню
				m.SetScreen(MainMenuScreen)
				m.ClearModuleState()
				cmd = func() bubbletea.Msg {
					return NewBackToMenuMsg()
				}
			}
		case "n", "N":
			// Отмена выполнения модуля (только в экране модуля)
			if m.currentScreen == ModuleScreen && m.pendingModule != nil && !m.moduleConfirmed {
				cmd = func() bubbletea.Msg {
					return NewModuleCancelMsg()
				}
			}
		}

	case bubbletea.WindowSizeMsg:
		// Обработка изменения размера окна
		m.SetSize(msg.Width, msg.Height)

	case ErrMsg:
		// Обработка ошибок
		m.SetError(msg.Err)

	case ProgressMsg:
		// Обработка обновлений прогресса
		m.UpdateProgress(msg.TaskID, msg.Progress, msg.Text)

	case DownloadCompleteMsg:
		// Обработка завершения скачивания
		if msg.Err != nil {
			m.SetError(msg.Err)
		} else {
			// Обновляем прогресс задачи скачивания
			m.UpdateProgress(msg.TaskID, 1.0, "Скачивание завершено")
		}

	case InstallCompleteMsg:
		// Обработка завершения установки
		if msg.Err != nil {
			m.SetError(msg.Err)
		} else {
			// Возврат в главное меню после успешной установки
			m.SetScreen(MainMenuScreen)
			m.UpdateProgress("install_"+msg.ModuleName, 1.0, "Установка завершена")
		}

	case ModuleConfirmMsg:
		// Обработка подтверждения модуля - создаем модель модуля и начинаем выполнение
		if m.pendingModule != nil {
			// Создаем модель модуля на основе его ID
			moduleModel, err := m.CreateModuleModel(msg.ModuleName, m.assetManager, m.winUtils, msg.ModuleIndex)
			if err != nil {
				m.SetError(fmt.Errorf("ошибка создания модели модуля: %w", err))
				return m, nil
			}

			// Устанавливаем активную модель модуля
			m.SetActiveModuleModel(moduleModel, msg.ModuleName)

			// Переключаемся на экран модуля для выполнения
			m.SetScreen(ModuleScreen)
		}

	case ModuleCancelMsg:
		// Обработка отмены модуля - возврат в главное меню
		m.SetScreen(MainMenuScreen)
		m.ClearModuleState()
		m.ClearError()

	case BackToMenuMsg:
		// Обработка возврата в меню
		m.SetScreen(MainMenuScreen)
		m.ClearModuleState()
		m.ClearError()

		// Если есть активная модель модуля, очищаем её
		if m.HasActiveModuleModel() {
			m.activeModuleModel = nil
			m.activeModuleID = ""
		}
	}

	// Если есть активная модель модуля, передаем сообщение ей для обработки
	if m.HasActiveModuleModel() && m.currentScreen == ModuleScreen {
		var moduleCmd bubbletea.Cmd
		m.activeModuleModel, moduleCmd = m.activeModuleModel.Update(msg)

		// Объединяем команды, если есть
		if moduleCmd != nil {
			if cmd != nil {
				cmd = bubbletea.Batch(cmd, moduleCmd)
			} else {
				cmd = moduleCmd
			}
		}
	}

	// Возвращаем обновленную модель и команду (может быть nil)
	return m, cmd
}

// View отрисовывает текущее состояние модели в виде строки
// Это основной метод для отображения интерфейса пользователя
func (m MainModel) View() string {
	switch m.currentScreen {
	case MainMenuScreen:
		// Отображение главного меню с модулями
		var view strings.Builder

		view.WriteString("Добро пожаловать в goMH - менеджер модулей\n\n")
		view.WriteString("Выберите модуль для установки:\n\n")

		for i, module := range m.modules {
			cursor := " "
			if i == m.selectedModule {
				cursor = ">"
			}

			marker := " "
			if i == m.selectedModule {
				marker = "•"
			}

			view.WriteString(fmt.Sprintf("%s %s %s\n", cursor, marker, module.MenuText()))
		}

		view.WriteString("\n\n")
		view.WriteString("↑/↓ - навигация • Enter - выбор • Esc - выход • q - выход")

		return view.String()

	case ModuleScreen:
		// Отображение экрана модуля
		if m.HasActiveModuleModel() {
			// Если есть активная модель модуля, отображаем её
			return m.activeModuleModel.View()
		} else if m.pendingModule != nil {
			// Экран подтверждения модуля (если модель еще не создана)
			var view strings.Builder

			view.WriteString(fmt.Sprintf("Модуль: %s\n\n", (*m.pendingModule).MenuText()))
			view.WriteString("Вы действительно хотите выполнить этот модуль?\n\n")
			view.WriteString("Enter - выполнить • N - отменить • Esc - вернуться в меню")

			return view.String()
		}

		return "Ошибка: модуль не выбран"

	case ProgressScreen:
		// Отображение экрана прогресса
		var view strings.Builder

		view.WriteString("Прогресс операций:\n\n")

		if len(m.progressTasks) == 0 {
			view.WriteString("Нет активных задач\n")
		} else {
			for _, task := range m.progressTasks {
				// Создаем визуальный индикатор прогресса
				progressBar := strings.Repeat("█", int(task.Progress*20))
				progressBar += strings.Repeat("░", 20-int(task.Progress*20))

				view.WriteString(fmt.Sprintf("%s [%s] %.1f%%\n",
					task.Text,
					progressBar,
					task.Progress*100))
			}
		}

		view.WriteString("\n\nEsc - вернуться в меню")

		return view.String()

	case ErrorScreen:
		// Отображение экрана ошибки
		var view strings.Builder

		view.WriteString("Ошибка:\n\n")
		if m.errorMsg != nil {
			view.WriteString(m.errorMsg.Error())
		} else {
			view.WriteString("Неизвестная ошибка")
		}

		view.WriteString("\n\nEsc - вернуться в меню")

		return view.String()

	default:
		return "Неизвестное состояние экрана"
	}
}

// SelectionModel модель для выбора элементов из списка с поиском
type SelectionModel struct {
	// UI компоненты
	list      list.Model
	textInput textinput.Model

	// Данные
	items         []SelectionItem
	filteredItems []SelectionItem
	query         string

	// Состояние
	state    SelectionState
	selected int

	// Конфигурация
	title      string
	showSearch bool

	// Размеры окна
	width  int
	height int
}

// SelectionState состояния модели выбора
type SelectionState int

const (
	SelectionBrowsing SelectionState = iota
	SelectionSearching
	SelectionSelected
	SelectionCancelled
)

// SelectionItem элемент для выбора
type SelectionItem struct {
	Text        string
	Description string
	Value       interface{}
}

// NewSelectionModel создает новую модель выбора
func NewSelectionModel(title string, items []SelectionItem, showSearch bool) SelectionModel {
	// Создаем элементы списка
	listItems := make([]list.Item, len(items))
	for i, item := range items {
		listItems[i] = item
	}

	// Создаем список
	itemList := list.New(listItems, list.NewDefaultDelegate(), 0, 0)
	itemList.Title = title
	itemList.SetShowStatusBar(false)
	itemList.SetShowHelp(false)

	// Создаем текстовый ввод для поиска
	ti := textinput.New()
	ti.Placeholder = "Поиск..."
	ti.Focus()

	model := SelectionModel{
		list:          itemList,
		textInput:     ti,
		items:         items,
		filteredItems: items,
		state:         SelectionBrowsing,
		selected:      0,
		title:         title,
		showSearch:    showSearch,
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
	if m.showSearch {
		return textinput.Blink
	}
	return nil
}

// Update обрабатывает сообщения для модели выбора
func (m SelectionModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	var cmd bubbletea.Cmd

	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		switch msg.String() {
		case "enter":
			if m.state == SelectionSearching {
				// В режиме поиска Enter выбирает текущий элемент
				m.state = SelectionSelected
				m.selected = m.list.Index()
				return m, nil
			} else if m.state == SelectionBrowsing {
				// В режиме просмотра Enter выбирает элемент
				m.state = SelectionSelected
				m.selected = m.list.Index()
				return m, nil
			}
		case "esc":
			if m.state == SelectionSearching {
				// Выход из режима поиска
				m.state = SelectionBrowsing
				m.query = ""
				m.updateFilteredItems()
			} else {
				// Отмена выбора
				m.state = SelectionCancelled
				return m, nil
			}
		case "ctrl+c":
			// Отмена выбора
			m.state = SelectionCancelled
			return m, nil
		case "/":
			if m.showSearch && m.state == SelectionBrowsing {
				// Вход в режим поиска
				m.state = SelectionSearching
				return m, nil
			}
		case "up", "k":
			if m.state == SelectionBrowsing {
				m.list, cmd = m.list.Update(msg)
			}
		case "down", "j":
			if m.state == SelectionBrowsing {
				m.list, cmd = m.list.Update(msg)
			}
		}
	case bubbletea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	// Обработка ввода текста в режиме поиска
	if m.state == SelectionSearching {
		var tiCmd bubbletea.Cmd
		m.textInput, tiCmd = m.textInput.Update(msg)
		cmd = tiCmd

		// Обновляем фильтр при изменении текста
		if m.textInput.Value() != m.query {
			m.query = m.textInput.Value()
			m.updateFilteredItems()
		}
	}

	return m, cmd
}

// View отображает модель выбора
func (m SelectionModel) View() string {
	var view strings.Builder

	// Заголовок
	view.WriteString(fmt.Sprintf("%s\n\n", m.title))

	if m.state == SelectionSearching {
		// Режим поиска
		view.WriteString("Поиск: ")
		view.WriteString(m.textInput.View())
		view.WriteString("\n\n")

		// Показываем отфильтрованные результаты
		if len(m.filteredItems) == 0 {
			view.WriteString("Нет результатов для поиска\n")
		} else {
			// Создаем временный список для отображения
			tempItems := make([]list.Item, len(m.filteredItems))
			for i, item := range m.filteredItems {
				tempItems[i] = item
			}

			tempList := list.New(tempItems, list.NewDefaultDelegate(), 0, 0)
			tempList.SetShowStatusBar(false)
			tempList.SetShowHelp(false)

			// Устанавливаем курсор на первый элемент
			if len(tempItems) > 0 {
				tempList.Select(0)
			}

			view.WriteString(tempList.View())
		}
	} else {
		// Режим просмотра
		view.WriteString(m.list.View())
	}

	// Помощь
	view.WriteString("\n\n")
	if m.state == SelectionSearching {
		view.WriteString("Enter - выбрать • Esc - назад • ↑/↓ - навигация")
	} else {
		if m.showSearch {
			view.WriteString("Enter - выбрать • / - поиск • Esc - назад • ↑/↓ - навигация")
		} else {
			view.WriteString("Enter - выбрать • Esc - назад • ↑/↓ - навигация")
		}
	}

	return view.String()
}

// updateFilteredItems обновляет отфильтрованные элементы на основе запроса
func (m *SelectionModel) updateFilteredItems() {
	if m.query == "" {
		m.filteredItems = m.items
	} else {
		m.filteredItems = make([]SelectionItem, 0)
		queryLower := strings.ToLower(m.query)

		for _, item := range m.items {
			if strings.Contains(strings.ToLower(item.Text), queryLower) ||
				strings.Contains(strings.ToLower(item.Description), queryLower) {
				m.filteredItems = append(m.filteredItems, item)
			}
		}
	}
}

// GetSelectedItem возвращает выбранный элемент
func (m SelectionModel) GetSelectedItem() (SelectionItem, bool) {
	if m.state == SelectionSelected && m.selected >= 0 && m.selected < len(m.filteredItems) {
		return m.filteredItems[m.selected], true
	}
	return SelectionItem{}, false
}

// IsSelected проверяет, выбран ли элемент
func (m SelectionModel) IsSelected() bool {
	return m.state == SelectionSelected
}

// IsCancelled проверяет, отменен ли выбор
func (m SelectionModel) IsCancelled() bool {
	return m.state == SelectionCancelled
}

// SetSize устанавливает размеры окна
func (m *SelectionModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// FilterValue возвращает значение для фильтрации (реализация list.Item)
func (i SelectionItem) FilterValue() string {
	return i.Text
}

// InputModel модель для ввода текста
type InputModel struct {
	// UI компонент
	textInput textinput.Model

	// Данные
	prompt   string
	value    string
	password bool

	// Состояние
	state InputState

	// Размеры окна
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
func NewInputModel(prompt string, password bool) InputModel {
	ti := textinput.New()
	ti.Placeholder = prompt
	ti.Focus()
	if password {
		ti.EchoMode = textinput.EchoPassword
	}

	return InputModel{
		textInput: ti,
		prompt:    prompt,
		password:  password,
		state:     InputWaiting,
		width:     80,
		height:    24,
	}
}

// Init инициализирует модель ввода
func (m InputModel) Init() bubbletea.Cmd {
	return textinput.Blink
}

// Update обрабатывает сообщения для модели ввода
func (m InputModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	var cmd bubbletea.Cmd

	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		switch msg.String() {
		case "enter":
			m.value = m.textInput.Value()
			m.state = InputEntered
			return m, nil
		case "esc", "ctrl+c":
			m.state = InputCancelled
			return m, nil
		}
	case bubbletea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	// Обработка ввода текста
	var tiCmd bubbletea.Cmd
	m.textInput, tiCmd = m.textInput.Update(msg)
	cmd = tiCmd

	return m, cmd
}

// View отображает модель ввода
func (m InputModel) View() string {
	var view strings.Builder

	view.WriteString(fmt.Sprintf("%s: ", m.prompt))
	view.WriteString(m.textInput.View())
	view.WriteString("\n\n")
	view.WriteString("Enter - подтвердить • Esc - отменить")

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

// SetSize устанавливает размеры окна
func (m *InputModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}
