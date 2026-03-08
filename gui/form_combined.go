package gui

import (
	"errors"
	"fmt"
	"goMH/app/modruntime"
	"goMH/config"
	"goMH/core"
	fiscaldrivers "goMH/modules/fiscal-drivers"
	"goMH/modules/regime"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

type CombinedForm struct {
	am           core.AssetManager
	wu           core.WinUtils
	queueService *modruntime.Service
	ctx          *GuiContext

	// Fiscal
	fiscalCombo *walk.ComboBox
	fiscalModel *FiscalDriverModel

	// Regime
	regimeUser *walk.LineEdit
	regimePass *walk.LineEdit
	regimeGB   *walk.GroupBox // Чтобы скрывать/показывать поля ввода
}

func NewCombinedForm(parent walk.Container, am core.AssetManager, wu core.WinUtils, queueService *modruntime.Service, ctx *GuiContext) (walk.Widget, error) {
	form := &CombinedForm{
		am:           am,
		wu:           wu,
		queueService: queueService,
		ctx:          ctx,
		fiscalModel:  NewFiscalDriverModel(am.Cfg().FiscalDriversConfig),
	}

	// Проверка статуса Regime для UI (нужно ли показывать поля ввода)
	regimeInstalled, _ := wu.ServiceExists("regime")

	var composite *walk.Composite

	err := Composite{
		AssignTo: &composite,
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{
				Pages: []TabPage{
					// --- ВКЛАДКА 1: Фискальные драйверы ---
					{
						Title:  "Фискальные драйверы (ДТО)",
						Layout: VBox{},
						Children: []Widget{
							Label{
								Text: "Выберите производителя ККТ:",
								Font: Font{PointSize: 10, Bold: true},
							},
							ComboBox{
								AssignTo:      &form.fiscalCombo,
								Model:         form.fiscalModel,
								DisplayMember: "Text",
								CurrentIndex:  0,
							},
							VSpacer{Size: 10},
							PushButton{
								Text:      "Установить драйвер",
								OnClicked: form.onInstallFiscal,
								MinSize:   Size{Height: 40}, // Кнопка покрупнее
							},
							VSpacer{},
							Label{
								Text:      "Примечание: Для Poscenter и Штрих выполняется удаление старых версий.",
								TextColor: walk.RGB(100, 100, 100),
							},
						},
					},

					// --- ВКЛАДКА 2: УТМ (ЕГАИС) ---
					{
						Title:  "УТМ (ЕГАИС)",
						Layout: VBox{},
						Children: []Widget{
							Label{
								Text: "Универсальный Транспортный Модуль",
								Font: Font{PointSize: 10, Bold: true},
							},
							Label{
								Text: "Будет установлена версия из конфигурации (silent install).",
							},
							VSpacer{Size: 10},
							PushButton{
								Text:      "Установить УТМ",
								OnClicked: form.onInstallUTM,
								MinSize:   Size{Height: 40},
							},
							VSpacer{},
						},
					},

					// --- ВКЛАДКА 3: Regime (Честный Знак) ---
					{
						Title:  "Regime (Честный Знак)",
						Layout: VBox{},
						Children: []Widget{
							Label{
								Text: "Модуль маркировки Regime",
								Font: Font{PointSize: 10, Bold: true},
							},
							// Группа с логином/паролем показывается только если службы нет
							GroupBox{
								AssignTo: &form.regimeGB,
								Title:    "Данные администратора (для первичной установки)",
								Layout:   Grid{Columns: 2},
								Visible:  !regimeInstalled, // Скрываем, если уже есть
								Children: []Widget{
									Label{Text: "Логин:"},
									LineEdit{AssignTo: &form.regimeUser},
									Label{Text: "Пароль:"},
									LineEdit{AssignTo: &form.regimePass, PasswordMode: true},
								},
							},
							Label{
								Text:      "Служба 'regime' уже установлена. Будет выполнено обновление.",
								Visible:   regimeInstalled,
								TextColor: walk.RGB(0, 128, 0),
							},
							VSpacer{Size: 10},
							PushButton{
								Text:      "Установить / Обновить Regime",
								OnClicked: func() { form.onInstallRegime(regimeInstalled) },
								MinSize:   Size{Height: 40},
							},
							VSpacer{},
						},
					},
				},
			},
		},
	}.Create(NewBuilder(parent))

	if err != nil {
		return nil, err
	}

	return composite, nil
}

// --- Handlers ---

func (f *CombinedForm) onInstallFiscal() {
	idx := f.fiscalCombo.CurrentIndex()
	if idx < 0 {
		f.ctx.Warn("Драйвер не выбран")
		return
	}
	driver := f.fiscalModel.Items[idx]

	cfg := &fiscaldrivers.DriverInstallConfig{
		Driver: driver,
	}
	f.enqueuePrepared("FiscalDrivers", cfg, "Установка драйвера: "+driver.MenuText)
}

func (f *CombinedForm) onInstallUTM() {
	f.enqueuePrepared("UTM", struct{}{}, "Установка УТМ")
}

func (f *CombinedForm) onInstallRegime(isReinstall bool) {
	username := f.regimeUser.Text()
	password := f.regimePass.Text()

	// Валидация только если это новая установка
	if !isReinstall {
		if username == "" || password == "" {
			f.ctx.Warn("Для первичной установки необходимо указать логин и пароль администратора.")
			return
		}
	}

	cfg := regime.RegimeInstallConfig{
		IsReinstall: isReinstall,
		Username:    username,
		Password:    password,
	}

	f.enqueuePrepared("Regime", cfg, "Установка Regime")
}

func (f *CombinedForm) enqueuePrepared(moduleID string, config any, successTitle string) {
	if f.queueService == nil {
		f.ctx.Error("Сервис очереди не инициализирован")
		return
	}

	result, err := f.queueService.EnqueuePrepared(moduleID, config)
	if err != nil {
		if errors.Is(err, ErrDuplicateTask) {
			f.ctx.Warn("Такая же задача уже в очереди или выполняется")
			return
		}
		f.ctx.Error(fmt.Sprintf("Не удалось поставить задачу в очередь: %v", err))
		return
	}

	message := result.Note
	if message == "" {
		message = "Задача добавлена в очередь: " + successTitle
	}
	f.ctx.Info(message)
}

// --- Models ---

type FiscalDriverModel struct {
	walk.ListModelBase
	Items []config.FiscalDriver
}

func NewFiscalDriverModel(items []config.FiscalDriver) *FiscalDriverModel {
	return &FiscalDriverModel{Items: items}
}

func (m *FiscalDriverModel) ItemCount() int {
	return len(m.Items)
}

func (m *FiscalDriverModel) Value(index int) interface{} {
	return m.Items[index].MenuText
}

func init() {
	Register("CombinedInstallers", NewCombinedForm)
}
