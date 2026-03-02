package gui

import (
	"fmt"
	"goMH/config"
	"goMH/core"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

type GuiModuleItem struct {
	ID      string
	Title   string
	Enabled bool
}

func Run(cfg *config.Config, am core.AssetManager, wu core.WinUtils) {
	var mw *walk.MainWindow
	var logText *walk.TextEdit
	var statusBar *walk.StatusBarItem
	var progressBar *walk.ProgressBar
	var contentArea *walk.Composite
	var modulesList *walk.ListBox
	var taskTable *walk.TableView
	var runningLabel *walk.Label
	var queuedLabel *walk.Label

	guiModules := []GuiModuleItem{
		{ID: "iiko", Title: "iiko / Syrve (Дистрибутивы)", Enabled: true},
		{ID: "CombinedInstallers", Title: "Установка ДТО, УТМ, ЛМ ЧЗ", Enabled: false},
		{ID: "VComCaster", Title: "Сканеры ШК (VComCaster)", Enabled: false},
		{ID: "RemoteAccess", Title: "Удаленный доступ", Enabled: false},
		{ID: "FRPC", Title: "Проброс портов (FRPC)", Enabled: false},
		{ID: "ServiceUtils", Title: "Утилиты обслуживания", Enabled: false},
	}

	modListModel := NewModuleListModel(guiModules)
	taskModel := NewTaskListModel()

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "goMH - MyHoreca Tool (GUI Mode)",
		MinSize:  Size{Width: 980, Height: 760},
		Size:     Size{Width: 1080, Height: 800},
		Layout:   VBox{},
		Children: []Widget{
			HSplitter{
				Children: []Widget{
					ListBox{
						AssignTo: &modulesList,
						Model:    modListModel,
						Font:     Font{PointSize: 11, Family: "Segoe UI"},
						MinSize:  Size{Width: 280},
					},
					Composite{
						AssignTo: &contentArea,
						Layout:   VBox{},
						Children: []Widget{
							Label{Text: "Выберите модуль слева"},
						},
					},
				},
			},
			GroupBox{
				Title:   "Очередь задач",
				Layout:  VBox{},
				MinSize: Size{Height: 180},
				Children: []Widget{
					TableView{
						AssignTo:            &taskTable,
						Model:               taskModel,
						AlternatingRowBG:    true,
						ColumnsOrderable:    false,
						LastColumnStretched: true,
						MultiSelection:      false,
						ColumnsSizable:      true,
						Columns: []TableViewColumn{
							{Title: "Статус", Width: 90},
							{Title: "Модуль", Width: 90},
							{Title: "Задача", Width: 260},
							{Title: "Этап", Width: 320},
							{Title: "Прогресс", Width: 90},
							{Title: "Время", Width: 120},
						},
					},
					Composite{
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							Label{AssignTo: &runningLabel, Text: "Running: 0"},
							Label{AssignTo: &queuedLabel, Text: "Queued: 0"},
						},
					},
				},
			},
			Composite{
				MinSize: Size{Height: 170},
				Layout:  VBox{Margins: Margins{Top: 5, Bottom: 5, Left: 5, Right: 5}},
				Children: []Widget{
					Label{Text: "Журнал операций:"},
					TextEdit{AssignTo: &logText, ReadOnly: true, VScroll: true},
					ProgressBar{AssignTo: &progressBar, MinValue: 0, MaxValue: 100, Value: 0},
				},
			},
		},
		StatusBarItems: []StatusBarItem{
			{AssignTo: &statusBar, Text: "Готов к работе", Width: 500},
		},
	}.Create()); err != nil {
		panic(err)
	}

	ctx := NewGuiContext(mw, logText, statusBar, progressBar)
	tm := NewTaskManager()

	refreshQueue := func() {
		taskModel.Replace(tm.Snapshots())
		running, queued := tm.Stats()
		runningLabel.SetText(fmt.Sprintf("Running: %d", running))
		queuedLabel.SetText(fmt.Sprintf("Queued: %d", queued))
	}

	tm.OnTaskEnqueued(func(s TaskSnapshot) {
		mw.Synchronize(func() {
			ctx.AppendRawLog(fmt.Sprintf("[%s][%s][%s][QUEUE] %s", time.Now().Format("15:04:05"), s.ModuleID, s.ID, s.Title))
			refreshQueue()
		})
	})
	tm.OnTaskStarted(func(s TaskSnapshot) {
		mw.Synchronize(func() {
			ctx.SetStatus(fmt.Sprintf("Выполняется: %s", s.Title))
			progressBar.SetValue(0)
			refreshQueue()
		})
	})
	tm.OnTaskProgress(func(s TaskSnapshot) {
		mw.Synchronize(func() {
			ctx.SetStatus(s.StageText)
			progressBar.SetValue(s.Progress)
			refreshQueue()
		})
	})
	tm.OnTaskLog(func(_ TaskSnapshot, line string) {
		mw.Synchronize(func() {
			ctx.AppendRawLog(line)
		})
	})
	tm.OnTaskFinished(func(s TaskSnapshot) {
		mw.Synchronize(func() {
			if s.State == TaskSuccess {
				ctx.SetStatus("Задача завершена успешно")
			} else {
				ctx.SetStatus("Задача завершена с ошибкой")
			}
			progressBar.SetValue(s.Progress)
			refreshQueue()
		})
	})

	tm.Start()

	modulesList.CurrentIndexChanged().Attach(func() {
		idx := modulesList.CurrentIndex()
		if idx < 0 || idx >= len(guiModules) {
			return
		}
		modItem := guiModules[idx]
		if !modItem.Enabled {
			loadDisabledModuleForm(contentArea, modItem)
			ctx.Warn("Раздел пока недоступен в GUI: " + modItem.Title)
			return
		}
		loadModuleForm(contentArea, modItem.ID, am, wu, tm, ctx)
	})

	if len(guiModules) > 0 {
		modulesList.SetCurrentIndex(0)
	}

	mw.Run()
}

func loadDisabledModuleForm(parent *walk.Composite, item GuiModuleItem) {
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

	_ = Composite{
		Layout: VBox{},
		Children: []Widget{
			Label{Text: item.Title, Font: Font{PointSize: 12, Bold: true}},
			Label{Text: "Скоро: модуль пока доступен только в TUI режиме."},
		},
	}.Create(NewBuilder(parent))
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
	item := m.Items[index]
	if item.Enabled {
		return item.Title
	}
	return item.Title + " (скоро)"
}
