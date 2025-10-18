package bubbletea

import (
	"fmt"
	"strings"

	"goMH/core"
	"goMH/modules/utm"
	"goMH/tui"

	"github.com/charmbracelet/bubbles/spinner"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Стиль для спиннера
var spinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("69"))

// InstallUTMCmd представляет команду для установки УТМ
// Запускает горутину для выполнения бизнес-логики установки модуля УТМ
func InstallUTMCmd(am core.AssetManager, wu core.WinUtils) bubbletea.Cmd {
	return func() bubbletea.Msg {
		// Запускаем установку в горутине
		go func() {
			// Выполняем основную логику установки
			err := utm.InstallUTMCore(am, wu)

			// Отправляем результат в главный поток через канал обновлений
			if err != nil {
				// В реальном приложении здесь должен быть канал для отправки сообщений
				// Пока просто логируем ошибку
				tui.Error(fmt.Sprintf("Ошибка установки УТМ: %v", err))
			} else {
				tui.Success("Установка УТМ успешно завершена")
			}
		}()

		// Отправляем сообщение о начале установки
		return NewInstallStartMsg()
	}
}

// UTMModel представляет модель для установки модуля УТМ (ЕГАИС)
// Обеспечивает интерфейс для подтверждения, выполнения и отслеживания процесса установки
type UTMModel struct {
	// assetManager менеджер ассетов для скачивания файлов
	assetManager core.AssetManager
	// winUtils утилиты для работы с Windows
	winUtils core.WinUtils
	// spinner анимированный индикатор загрузки
	spinner spinner.Model
	// isInstalling флаг, указывающий на процесс установки
	isInstalling bool
	// installError ошибка установки, если она произошла
	installError error
	// currentStep текущий этап установки
	currentStep string
	// width ширина терминала
	width int
	// height высота терминала
	height int
	// isConfirmed флаг подтверждения выполнения модуля
	isConfirmed bool
	// moduleInfo информация о модуле для отображения в экране подтверждения
	moduleInfo string
	// moduleIndex индекс модуля в списке (для корректной отправки сообщений)
	moduleIndex int
}

// NewUTMModel создает новую модель для установки УТМ
// Инициализирует модель с начальными значениями и настройками интерфейса
func NewUTMModel(am core.AssetManager, wu core.WinUtils, moduleIndex int) UTMModel {
	// Инициализируем спиннер с настройками
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	return UTMModel{
		assetManager: am,
		winUtils:     wu,
		spinner:      s,
		isInstalling: false,
		width:        80,
		height:       24,
		currentStep:  "Ожидание подтверждения",
		isConfirmed:  false,
		moduleInfo:   "Модуль УТМ (ЕГАИС)\n\nУстановка программного обеспечения для работы с системой ЕГАИС (Единая государственная автоматизированная информационная система).\n\nВключает в себя:\n• Установка драйверов для транспортного модуля\n• Настройка параметров подключения\n• Проверка работоспособности",
		moduleIndex:  moduleIndex,
	}
}

// Init инициализирует модель и возвращает команду для запуска спиннера
// Запускает анимацию спиннера для индикации готовности интерфейса
func (m UTMModel) Init() bubbletea.Cmd {
	return m.spinner.Tick
}

// Update обрабатывает входящие сообщения и обновляет состояние модели
func (m UTMModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	var cmd bubbletea.Cmd

	switch msg := msg.(type) {
	case spinner.TickMsg:
		// Обновляем спиннер
		var cmd1 bubbletea.Cmd
		m.spinner, cmd1 = m.spinner.Update(msg)
		cmd = cmd1

	case bubbletea.KeyMsg:
		// Обработка нажатий клавиш для отмены/возврата
		switch msg.String() {
		case "enter":
			// Подтверждение выполнения модуля (только в экране подтверждения)
			if !m.isConfirmed && !m.isInstalling && m.installError == nil {
				cmd = func() bubbletea.Msg {
					return NewModuleConfirmMsg("utm", m.moduleIndex)
				}
			}
		case "n", "N":
			// Отмена выполнения модуля (только в экране подтверждения)
			if !m.isConfirmed && !m.isInstalling && m.installError == nil {
				cmd = func() bubbletea.Msg {
					return NewModuleCancelMsg()
				}
			}
		case "esc":
			// Возврат в меню из любого состояния
			if m.isInstalling {
				// Во время установки - отменяем и возвращаемся в меню
				m.isInstalling = false
				m.isConfirmed = false
				m.installError = nil
				m.currentStep = "Ожидание подтверждения"
			}
			cmd = func() bubbletea.Msg {
				return NewBackToMenuMsg()
			}
		}

	case ModuleConfirmMsg:
		// Подтверждение выполнения модуля - начинаем установку
		if msg.ModuleName == "utm" {
			m.isConfirmed = true
			m.isInstalling = true
			m.installError = nil
			m.currentStep = "Начало установки УТМ..."
			cmd = InstallUTMCmd(m.assetManager, m.winUtils)
		}

	case ModuleCancelMsg:
		// Отмена выполнения модуля - очищаем состояние и возвращаемся в меню
		m.isConfirmed = false
		m.isInstalling = false
		m.installError = nil
		m.currentStep = "Ожидание подтверждения"
		cmd = func() bubbletea.Msg {
			return NewBackToMenuMsg()
		}

	case InstallStartMsg:
		// Начало процесса установки
		m.currentStep = "Начало установки УТМ..."

	case ProgressMsg:
		// Обновление прогресса установки
		m.currentStep = msg.Text

	case InstallCompleteMsg:
		// Завершение установки
		m.isInstalling = false
		if msg.Err != nil {
			m.installError = msg.Err
			m.currentStep = "Ошибка установки УТМ"
		} else {
			m.currentStep = "Установка УТМ успешно завершена!"
		}

	case bubbletea.WindowSizeMsg:
		// Обработка изменения размера окна
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, cmd
}

// View отображает текущее состояние модели
func (m UTMModel) View() string {
	var view strings.Builder

	// Экран подтверждения модуля (до подтверждения)
	if !m.isConfirmed && !m.isInstalling && m.installError == nil {
		view.WriteString("Подтверждение выполнения модуля\n\n")
		view.WriteString(m.moduleInfo)
		view.WriteString("\n\n")
		view.WriteString("⚠️  Внимание: Убедитесь, что транспортный модуль ЕГАИС подключен к компьютеру перед установкой.\n\n")
		view.WriteString("Enter - подтвердить и начать установку\n")
		view.WriteString("N - отменить и вернуться в меню\n")
		view.WriteString("Esc - вернуться в меню")
	} else if m.installError != nil {
		// Отображение ошибки с возможностью возврата в меню
		view.WriteString("Установка УТМ (ЕГАИС)\n\n")
		view.WriteString(fmt.Sprintf("❌ Ошибка: %s\n\n", m.installError.Error()))
		view.WriteString("Нажмите Esc для возврата в меню")
	} else if m.isInstalling {
		// Отображение процесса установки со спиннером и возможностью отмены
		view.WriteString("Установка УТМ (ЕГАИС)\n\n")
		view.WriteString(fmt.Sprintf("%s %s\n\n", m.spinner.View(), m.currentStep))
		view.WriteString("⏳ Установка в процессе...\n")
		view.WriteString("Нажмите Esc для отмены и возврата в меню")
	} else {
		// Состояние завершения (успех или иная ситуация)
		view.WriteString("Установка УТМ (ЕГАИС)\n\n")
		view.WriteString(fmt.Sprintf("✅ %s\n\n", m.currentStep))
		view.WriteString("Нажмите Esc для возврата в меню")
	}

	return view.String()
}

// IsInstalling возвращает true, если установка в процессе
// Используется для проверки состояния модели извне
func (m UTMModel) IsInstalling() bool {
	return m.isInstalling
}

// GetError возвращает ошибку установки, если она произошла
// Позволяет получить информацию об ошибке для обработки в других компонентах
func (m UTMModel) GetError() error {
	return m.installError
}

// SetSize устанавливает размеры терминала
// Обновляет внутренние размеры для корректного отображения интерфейса
func (m *UTMModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}
