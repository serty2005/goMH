package bubbletea

import (
	"fmt"
	"strings"

	"goMH/config"
	"goMH/core"
	"goMH/modules/distro"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Состояния модели дистрибутива
type distroState int

const (
	choosingBrand distroState = iota
	choosingComponent
	choosingVersion
	installing
	completed
	errorState
)

// DistroModel представляет модель для установки дистрибутивов iiko/Syrve
type DistroModel struct {
	// Зависимости
	assetManager core.AssetManager
	winUtils     core.WinUtils

	// Состояние модели
	state distroState

	// Данные для установки
	selectedBrand     string
	selectedComponent config.DistroComponent
	availableVersions []string
	selectedVersion   string
	selectedPatch     core.PatchInfo
	patchSelected     bool

	// UI компоненты
	brandList     list.Model
	componentList list.Model
	versionList   list.Model
	spinner       spinner.Model

	// Состояние установки
	isInstalling bool
	installError error
	currentStep  string

	// Размеры окна
	width  int
	height int

	// Стиль для спиннера
	spinnerStyle lipgloss.Style
}

// NewDistroModel создает новую модель для установки дистрибутивов
func NewDistroModel(am core.AssetManager, wu core.WinUtils) DistroModel {
	// Инициализируем спиннер
	s := spinner.New()
	s.Spinner = spinner.Dot
	spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("69"))

	// Создаем модель с настройками по умолчанию
	model := DistroModel{
		assetManager: am,
		winUtils:     wu,
		state:        choosingBrand,
		spinner:      s,
		spinnerStyle: spinnerStyle,
		width:        80,
		height:       24,
		currentStep:  "Готов к установке дистрибутива",
	}

	// Инициализируем списки для выбора
	model.initBrandList()
	model.initComponentList()
	model.initVersionList()

	return model
}

// initBrandList инициализирует список брендов для выбора
func (m *DistroModel) initBrandList() {
	items := []list.Item{
		item{text: "iiko", desc: "Система автоматизации ресторанов iiko"},
		item{text: "Syrve", desc: "Платформа для автоматизации ресторанного бизнеса"},
	}

	m.brandList = list.New(items, list.NewDefaultDelegate(), 0, 0)
	m.brandList.Title = "Выберите продукт для установки"
	m.brandList.SetShowStatusBar(false)
	m.brandList.SetShowHelp(false)
}

// initComponentList инициализирует список компонентов для выбранного бренда
func (m *DistroModel) initComponentList() {
	var items []list.Item

	if m.selectedBrand == "iiko" {
		components := m.assetManager.Cfg().DistroConfig.Iiko.Components
		for _, comp := range components {
			items = append(items, item{
				text: comp.MenuText,
				desc: fmt.Sprintf("ID: %s", comp.ID),
			})
		}
	} else if m.selectedBrand == "syrve" {
		components := m.assetManager.Cfg().DistroConfig.Syrve.Components
		for _, comp := range components {
			items = append(items, item{
				text: comp.MenuText,
				desc: fmt.Sprintf("ID: %s", comp.ID),
			})
		}
	}

	m.componentList = list.New(items, list.NewDefaultDelegate(), 0, 0)
	m.componentList.Title = "Выберите компонент для установки"
	m.componentList.SetShowStatusBar(false)
	m.componentList.SetShowHelp(false)
}

// initVersionList инициализирует список доступных версий
func (m *DistroModel) initVersionList() {
	items := make([]list.Item, len(m.availableVersions))
	for i, version := range m.availableVersions {
		items[i] = item{
			text: version,
			desc: fmt.Sprintf("Версия %s", version),
		}
	}

	m.versionList = list.New(items, list.NewDefaultDelegate(), 0, 0)
	m.versionList.Title = "Выберите версию для установки"
	m.versionList.SetShowStatusBar(false)
	m.versionList.SetShowHelp(false)
}

// item представляет элемент списка для Bubble Tea
type item struct {
	text string
	desc string
}

func (i item) FilterValue() string { return i.text }

// Init инициализирует модель и возвращает команду для начала работы
func (m DistroModel) Init() bubbletea.Cmd {
	return bubbletea.Batch(
		m.spinner.Tick,
	)
}

// Update обрабатывает входящие сообщения и обновляет состояние модели
// Работает как конечный автомат, переключая состояния в зависимости от действий пользователя
func (m DistroModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	var cmd bubbletea.Cmd

	switch msg := msg.(type) {
	case spinner.TickMsg:
		// Обновляем спиннер
		var cmd1 bubbletea.Cmd
		m.spinner, cmd1 = m.spinner.Update(msg)
		cmd = cmd1

	case bubbletea.KeyMsg:
		switch msg.String() {
		case "enter":
			// Обработка выбора в зависимости от текущего состояния
			switch m.state {
			case choosingBrand:
				selected := m.brandList.SelectedItem()
				if selected != nil {
					m.selectedBrand = selected.FilterValue()
					m.initComponentList()
					m.state = choosingComponent
				}
			case choosingComponent:
				selected := m.componentList.SelectedItem()
				if selected != nil {
					component := m.getComponentByText(selected.FilterValue())
					if component.ID != "" {
						m.selectedComponent = component
						// Проверяем, нужно ли выбирать версию
						if m.needsVersionSelection() {
							return m, m.loadVersionsCmd()
						} else {
							// Компонент без версий, сразу переходим к установке
							return m, m.startInstallCmd()
						}
					}
				}
			case choosingVersion:
				selected := m.versionList.SelectedItem()
				if selected != nil {
					m.selectedVersion = selected.FilterValue()
					return m, m.startInstallCmd()
				}
			}
		case "esc":
			// Возврат в предыдущее состояние или выход
			switch m.state {
			case choosingBrand:
				return m, func() bubbletea.Msg { return NewBackToMenuMsg() }
			case choosingComponent:
				m.state = choosingBrand
			case choosingVersion:
				m.state = choosingComponent
			case installing:
				// Во время установки Esc не работает
			}
		case "up", "k":
			// Навигация вверх в списках
			switch m.state {
			case choosingBrand:
				m.brandList, cmd = m.brandList.Update(msg)
			case choosingComponent:
				m.componentList, cmd = m.componentList.Update(msg)
			case choosingVersion:
				m.versionList, cmd = m.versionList.Update(msg)
			}
		case "down", "j":
			// Навигация вниз в списках
			switch m.state {
			case choosingBrand:
				m.brandList, cmd = m.brandList.Update(msg)
			case choosingComponent:
				m.componentList, cmd = m.componentList.Update(msg)
			case choosingVersion:
				m.versionList, cmd = m.versionList.Update(msg)
			}
		}

	case VersionsLoadedMsg:
		// Получены доступные версии
		m.availableVersions = msg.Versions
		m.initVersionList()
		m.state = choosingVersion

	case InstallStartMsg:
		// Начало процесса установки
		m.isInstalling = true
		m.installError = nil
		m.state = installing
		m.currentStep = "Начало установки..."

	case ProgressMsg:
		// Обновление прогресса установки
		m.currentStep = msg.Text

	case InstallCompleteMsg:
		// Завершение установки
		m.isInstalling = false
		m.state = completed
		if msg.Err != nil {
			m.installError = msg.Err
			m.state = errorState
		} else {
			m.currentStep = "Установка успешно завершена!"
		}

	case bubbletea.WindowSizeMsg:
		// Обработка изменения размера окна
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, cmd
}

// View отображает текущее состояние модели
func (m DistroModel) View() string {
	var view strings.Builder

	switch m.state {
	case choosingBrand:
		view.WriteString("Выбор продукта для установки\n\n")
		view.WriteString(m.brandList.View())
		view.WriteString("\n↑/↓ - навигация • Enter - выбор • Esc - назад")

	case choosingComponent:
		view.WriteString(fmt.Sprintf("Выбор компонента (%s)\n\n", m.selectedBrand))
		view.WriteString(m.componentList.View())
		view.WriteString("\n↑/↓ - навигация • Enter - выбор • Esc - назад")

	case choosingVersion:
		view.WriteString(fmt.Sprintf("Выбор версии (%s - %s)\n\n", m.selectedBrand, m.selectedComponent.MenuText))
		view.WriteString(m.versionList.View())
		view.WriteString("\n↑/↓ - навигация • Enter - выбор • Esc - назад")

	case installing:
		view.WriteString("Установка дистрибутива\n\n")
		view.WriteString(fmt.Sprintf("%s %s\n\n", m.spinner.View(), m.currentStep))
		view.WriteString("⏳ Установка в процессе...")

	case completed:
		view.WriteString("Установка завершена\n\n")
		view.WriteString(fmt.Sprintf("✅ %s\n\n", m.currentStep))
		view.WriteString("Нажмите Esc для возврата в меню")

	case errorState:
		view.WriteString("Ошибка установки\n\n")
		if m.installError != nil {
			view.WriteString(fmt.Sprintf("❌ Ошибка: %s\n\n", m.installError.Error()))
		}
		view.WriteString("Нажмите Esc для возврата в меню")
	}

	return view.String()
}

// Вспомогательные методы

// getComponentByText находит компонент по тексту отображения
func (m *DistroModel) getComponentByText(text string) config.DistroComponent {
	if m.selectedBrand == "iiko" {
		components := m.assetManager.Cfg().DistroConfig.Iiko.Components
		for _, comp := range components {
			if comp.MenuText == text {
				return comp
			}
		}
	} else if m.selectedBrand == "syrve" {
		components := m.assetManager.Cfg().DistroConfig.Syrve.Components
		for _, comp := range components {
			if comp.MenuText == text {
				return comp
			}
		}
	}
	return config.DistroComponent{}
}

// needsVersionSelection проверяет, нужно ли выбирать версию для компонента
func (m *DistroModel) needsVersionSelection() bool {
	// Компоненты без версий не имеют URLTemplate или имеют его без {{VERSION}}
	return m.selectedComponent.URLTemplate != "" && strings.Contains(m.selectedComponent.URLTemplate, "{{VERSION}}")
}

// SetSize устанавливает размеры терминала
func (m *DistroModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// loadVersionsCmd создает команду для загрузки доступных версий
func (m *DistroModel) loadVersionsCmd() bubbletea.Cmd {
	return func() bubbletea.Msg {
		var versions []string
		var err error

		if m.selectedBrand == "iiko" {
			versions, err = discoverIikoOfficialVersions()
		} else if m.selectedBrand == "syrve" {
			versions, err = discoverSyrveVersions()
		}

		if err != nil {
			return NewVersionsLoadedMsg(nil, err)
		}

		return NewVersionsLoadedMsg(versions, nil)
	}
}

// startInstallCmd создает команду для начала процесса установки
func (m *DistroModel) startInstallCmd() bubbletea.Cmd {
	return func() bubbletea.Msg {
		// Отправляем сообщение о начале установки
		return NewInstallStartMsg()
	}
}

// discoverIikoOfficialVersions получает список доступных версий iiko
func discoverIikoOfficialVersions() ([]string, error) {
	// Используем чистые функции данных из пакета distro
	return distro.GetAvailableVersions("iiko")
}

// discoverSyrveVersions получает список доступных версий Syrve
func discoverSyrveVersions() ([]string, error) {
	// Используем чистые функции данных из пакета distro
	return distro.GetAvailableVersions("syrve")
}

// installProcessCmd создает команду для выполнения процесса установки в фоне
func (m *DistroModel) installProcessCmd() bubbletea.Cmd {
	return func() bubbletea.Msg {
		// Выполняем установку в горутине
		go func() {
			var err error

			// Определяем, нужна ли портативная установка
			isPortable := m.selectedComponent.PortableArchiveKey != ""

			if isPortable {
				err = distro.InstallPortableData(m.selectedBrand, m.selectedComponent, m.selectedVersion, m.assetManager, m.winUtils)
			} else {
				err = distro.InstallComponentData(m.selectedBrand, m.selectedComponent, m.selectedVersion, m.assetManager, m.winUtils)
			}

			// Отправляем результат через канал обновлений
			if err != nil {
				// В реальном приложении здесь должен быть канал для отправки сообщений
				// Пока просто логируем ошибку
				fmt.Printf("Ошибка установки: %v\n", err)
			} else {
				fmt.Println("Установка успешно завершена")
			}
		}()

		// Возвращаем nil, так как установка выполняется асинхронно
		return nil
	}
}
