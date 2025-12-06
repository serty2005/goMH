package gui

import (
	"goMH/core"

	"github.com/lxn/walk"
)

// ModuleFormFactory - функция, создающая виджет настроек модуля.
type ModuleFormFactory func(parent walk.Container, am core.AssetManager, wu core.WinUtils, tm *TaskManager, ctx *GuiContext) (walk.Widget, error)

// Registry хранит соответствие ID модуля и его формы.
var Registry = make(map[string]ModuleFormFactory)

// Register добавляет модуль в реестр GUI.
func Register(id string, factory ModuleFormFactory) {
	Registry[id] = factory
}
