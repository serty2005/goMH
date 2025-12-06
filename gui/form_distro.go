package gui

import (
	"goMH/config"
	"goMH/core"
	"goMH/modules/distro"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

type DistroForm struct {
	am  core.AssetManager
	wu  core.WinUtils
	tm  *TaskManager
	ctx *GuiContext

	// Данные для UI
	brandGB   *walk.GroupBox // Исправлено с RadioButtonGroup на GroupBox
	rbIiko    *walk.RadioButton
	compCB    *walk.ComboBox
	versionCB *walk.ComboBox

	// Модели данных
	components   []config.DistroComponent
	compModel    *ComponentListModel
	versionModel *VersionListModel

	currentBrand string
}

func NewDistroForm(parent walk.Container, am core.AssetManager, wu core.WinUtils, tm *TaskManager, ctx *GuiContext) (walk.Widget, error) {
	form := &DistroForm{
		am:           am,
		wu:           wu,
		tm:           tm,
		ctx:          ctx,
		compModel:    NewComponentListModel(),
		versionModel: NewVersionListModel(),
		currentBrand: "iiko", // default
	}

	var composite *walk.Composite

	// Описываем интерфейс
	err := Composite{
		AssignTo: &composite,
		Layout:   VBox{},
		Children: []Widget{
			GroupBox{
				AssignTo: &form.brandGB, // Привязываем к GroupBox
				Title:    "Бренд",
				Layout:   HBox{},
				Children: []Widget{
					RadioButton{
						AssignTo: &form.rbIiko, // Привязываем для установки Checked программно
						Text:     "iiko",
						Value:    "iiko",
						OnClicked: func() {
							form.currentBrand = "iiko"
							form.refreshComponents()
						},
					},
					RadioButton{
						Text:  "Syrve",
						Value: "syrve",
						OnClicked: func() {
							form.currentBrand = "syrve"
							form.refreshComponents()
						},
					},
				},
			},
			Composite{
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "Компонент:"},
					ComboBox{
						AssignTo:              &form.compCB,
						Model:                 form.compModel,
						DisplayMember:         "Name",
						OnCurrentIndexChanged: form.onComponentChanged,
					},

					Label{Text: "Версия:"},
					ComboBox{
						AssignTo: &form.versionCB,
						Model:    form.versionModel,
						Editable: true,
					},
				},
			},
			PushButton{
				Text:      "Установить",
				OnClicked: form.onInstall,
			},
			VSpacer{},
		},
	}.Create(NewBuilder(parent)) // Исправлено: передаем parent напрямую

	if err != nil {
		return nil, err
	}

	// Инициализация начального состояния
	form.rbIiko.SetChecked(true)
	form.refreshComponents()

	return composite, nil
}

func (f *DistroForm) refreshComponents() {
	var comps []config.DistroComponent
	if f.currentBrand == "iiko" {
		comps = f.am.Cfg().DistroConfig.Iiko.Components
	} else {
		comps = f.am.Cfg().DistroConfig.Syrve.Components
	}
	f.components = comps
	f.compModel.Items = comps

	// Обновляем модель и сбрасываем выбор
	f.compCB.SetModel(f.compModel)
	if len(comps) > 0 {
		f.compCB.SetCurrentIndex(0)
		f.onComponentChanged()
	} else {
		f.compCB.SetCurrentIndex(-1)
	}
}

func (f *DistroForm) onComponentChanged() {
	idx := f.compCB.CurrentIndex()
	if idx < 0 || idx >= len(f.components) {
		return
	}

	f.versionModel.Items = []string{"Впишите версию вручную или нажмите 'Установить'"}
	f.versionCB.SetModel(f.versionModel)
	f.versionCB.SetCurrentIndex(0)
}

func (f *DistroForm) onInstall() {
	idx := f.compCB.CurrentIndex()
	if idx < 0 {
		f.ctx.Warn("Не выбран компонент")
		return
	}
	comp := f.components[idx]

	rawVer := f.versionCB.Text()
	version := strings.TrimSpace(rawVer)
	if strings.Contains(version, "Впишите") {
		version = ""
	}

	cfg := &distro.DistroInstallConfig{
		Action:    distro.ActionInstallComponent,
		Brand:     f.currentBrand,
		Component: comp,
		Version:   version,
	}

	f.tm.AddTask(func() error {
		mod := &distro.Module{}
		return mod.Execute(f.ctx, f.am, f.wu, cfg)
	})
}

// --- Models ---

type ComponentListModel struct {
	walk.ListModelBase
	Items []config.DistroComponent
}

func NewComponentListModel() *ComponentListModel {
	return &ComponentListModel{}
}

func (m *ComponentListModel) ItemCount() int {
	return len(m.Items)
}

func (m *ComponentListModel) Value(index int) interface{} {
	return m.Items[index].MenuText
}

type VersionListModel struct {
	walk.ListModelBase
	Items []string
}

func NewVersionListModel() *VersionListModel {
	return &VersionListModel{}
}

func (m *VersionListModel) ItemCount() int {
	return len(m.Items)
}

func (m *VersionListModel) Value(index int) interface{} {
	return m.Items[index]
}
