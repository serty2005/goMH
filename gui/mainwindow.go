package gui

import (
	"goMH/config"
	"goMH/core"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// GuiModuleItem описывает пункт меню в GUI
type GuiModuleItem struct {
	ID    string
	Title string
}

func Run(cfg *config.Config, am core.AssetManager, wu core.WinUtils) {
	var mw *walk.MainWindow
	var logText *walk.TextEdit
	var statusBar *walk.StatusBarItem
	var progressBar *walk.ProgressBar
	var contentArea *walk.Composite
	var modulesList *walk.ListBox

	// Формируем список модулей специально для GUI
	// Мы игнорируем порядок в config.json и строим удобное меню
	guiModules := []GuiModuleItem{
		{ID: "iiko", Title: "iiko / Syrve (Дистрибутивы)"},
		{ID: "CombinedInstallers", Title: "Установка ДТО, УТМ, ЛМ ЧЗ"},
		{ID: "VComCaster", Title: "Сканеры ШК (VComCaster)"},
		{ID: "RemoteAccess", Title: "Удаленный доступ"},
		{ID: "FRPC", Title: "Проброс портов (FRPC)"},
		{ID: "ServiceUtils", Title: "Утилиты обслуживания"},
	}

	modListModel := NewModuleListModel(guiModules)

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "goMH - MyHoreca Tool (GUI Mode)",
		MinSize:  Size{Width: 600, Height: 600}, // Чуть увеличим минимальный размер
		Size:     Size{Width: 600, Height: 600},
		Layout:   VBox{},
		Children: []Widget{
			HSplitter{
				Children: []Widget{
					ListBox{
						AssignTo: &modulesList,
						Model:    modListModel,
						// Увеличиваем шрифт, чтобы элементы были крупнее (x3 визуально от стандарта)
						Font: Font{PointSize: 14, Family: "Segoe UI"},
						// MaxSize: Size{Width: 300}, // Немного шире панель меню
					},
					Composite{
						AssignTo: &contentArea,
						Layout:   HBox{},
						Children: []Widget{
							Label{Text: "Выберите модуль слева"},
						},
					},
				},
			},
			Composite{
				// Фиксированная высота для лога (150px)
				MinSize: Size{Height: 150},
				MaxSize: Size{Height: 150},
				Layout:  VBox{Margins: Margins{Top: 5, Bottom: 5, Left: 5, Right: 5}},
				Children: []Widget{
					Label{Text: "Журнал операций:"},
					TextEdit{
						AssignTo: &logText,
						ReadOnly: true,
						VScroll:  true,
					},
					ProgressBar{
						AssignTo: &progressBar,
						MaxValue: 100,
					},
				},
			},
		},
		StatusBarItems: []StatusBarItem{
			{AssignTo: &statusBar, Text: "Готов к работе", Width: 300},
		},
	}.Create()); err != nil {
		panic(err)
	}

	ctx := NewGuiContext(mw, logText, statusBar, progressBar)
	tm := NewTaskManager(mw, progressBar)
	tm.Start()

	modulesList.CurrentIndexChanged().Attach(func() {
		idx := modulesList.CurrentIndex()
		if idx >= 0 {
			modItem := guiModules[idx]
			loadModuleForm(contentArea, modItem.ID, am, wu, tm, ctx)
		}
	})

	mw.Run()
}

func loadModuleForm(parent *walk.Composite, moduleID string, am core.AssetManager, wu core.WinUtils, tm *TaskManager, ctx *GuiContext) {
	parent.SetSuspended(true)
	defer parent.SetSuspended(false)

	children := parent.Children()
	var toDispose []walk.Widget
	for i := 0; i < children.Len(); i++ {
		toDispose = append(toDispose, children.At(i))
	}
	for _, w := range toDispose {
		w.Dispose()
	}

	factory, ok := Registry[moduleID]
	if !ok {
		Label{Text: "GUI для модуля '" + moduleID + "' еще не реализован."}.Create(NewBuilder(parent))
		return
	}

	_, err := factory(parent, am, wu, tm, ctx)
	if err != nil {
		Label{Text: "Ошибка загрузки формы: " + err.Error()}.Create(NewBuilder(parent))
	}
}

// --- Module List Model ---

type ModuleListModel struct {
	walk.ListModelBase
	Items []GuiModuleItem
}

func NewModuleListModel(items []GuiModuleItem) *ModuleListModel {
	return &ModuleListModel{Items: items}
}

func (m *ModuleListModel) ItemCount() int {
	return len(m.Items)
}

func (m *ModuleListModel) Value(index int) interface{} {
	return m.Items[index].Title // Теперь отображаем Title
}
