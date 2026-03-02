package gui

import (
	"errors"
	"fmt"
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

	brand         string
	components    []config.DistroComponent
	patches       []core.PatchInfo
	manualPatches []core.PatchInfo

	componentCB    *walk.ComboBox
	versionLE      *walk.LineEdit
	oldVersionLE   *walk.LineEdit
	uninstallOldCB *walk.CheckBox
	autoPluginsCB  *walk.CheckBox
	loadPatchesBtn *walk.PushButton
	applyPatchCB   *walk.CheckBox
	patchCB        *walk.ComboBox
	patchModel     *PatchListModel

	portableComponentCB *walk.ComboBox
	portableSourceCB    *walk.ComboBox
	portableVersionLE   *walk.LineEdit
	portableComponents  []config.DistroComponent
	portableCompModel   *ComponentListModel

	manualVersionLE  *walk.LineEdit
	manualPatchCB    *walk.ComboBox
	manualPatchModel *PatchListModel
	rbIiko           *walk.RadioButton
}

func NewDistroForm(parent walk.Container, am core.AssetManager, wu core.WinUtils, tm *TaskManager, ctx *GuiContext) (walk.Widget, error) {
	f := &DistroForm{
		am:                am,
		wu:                wu,
		tm:                tm,
		ctx:               ctx,
		brand:             "iiko",
		patchModel:        NewPatchListModel(),
		portableCompModel: NewComponentListModel(),
		manualPatchModel:  NewPatchListModel(),
	}

	var composite *walk.Composite
	err := Composite{
		AssignTo: &composite,
		Layout:   VBox{},
		Children: []Widget{
			GroupBox{
				Title:  "Бренд",
				Layout: HBox{},
				Children: []Widget{
					RadioButton{AssignTo: &f.rbIiko, Text: "iiko", OnClicked: func() { f.setBrand("iiko") }},
					RadioButton{Text: "Syrve", OnClicked: func() { f.setBrand("syrve") }},
				},
			},
			TabWidget{
				Pages: []TabPage{
					{
						Title:  "Install",
						Layout: VBox{},
						Children: []Widget{
							Composite{
								Layout: Grid{Columns: 2},
								Children: []Widget{
									Label{Text: "Компонент:"},
									ComboBox{AssignTo: &f.componentCB, Model: NewComponentListModel(), OnCurrentIndexChanged: f.onInstallComponentChanged},
									Label{Text: "Версия:"},
									LineEdit{AssignTo: &f.versionLE, Text: ""},
									Label{Text: "Старая версия (для uninstall):"},
									LineEdit{AssignTo: &f.oldVersionLE},
								},
							},
							Composite{
								Layout: HBox{},
								Children: []Widget{
									CheckBox{AssignTo: &f.uninstallOldCB, Text: "Удалять старую версию"},
									CheckBox{AssignTo: &f.autoPluginsCB, Text: "Автообновление плагинов (iikoFront)", Checked: true},
								},
							},
							GroupBox{
								Title:  "Патч (опционально)",
								Layout: VBox{},
								Children: []Widget{
									Composite{Layout: HBox{}, Children: []Widget{
										PushButton{AssignTo: &f.loadPatchesBtn, Text: "Загрузить патчи", OnClicked: f.onLoadInstallPatches},
										CheckBox{AssignTo: &f.applyPatchCB, Text: "Применить выбранный патч"},
									}},
									ComboBox{AssignTo: &f.patchCB, Model: f.patchModel},
								},
							},
							PushButton{Text: "Добавить в очередь: Установка", MinSize: Size{Height: 40}, OnClicked: f.onQueueInstall},
						},
					},
					{
						Title:  "Portable",
						Layout: VBox{},
						Children: []Widget{
							Composite{Layout: Grid{Columns: 2}, Children: []Widget{
								Label{Text: "Компонент:"},
								ComboBox{AssignTo: &f.portableComponentCB, Model: f.portableCompModel},
								Label{Text: "Версия:"},
								LineEdit{AssignTo: &f.portableVersionLE},
								Label{Text: "Источник:"},
								ComboBox{AssignTo: &f.portableSourceCB, Model: []string{"auto", "http", "ftp"}, CurrentIndex: 0},
							}},
							PushButton{Text: "Добавить в очередь: Portable", MinSize: Size{Height: 40}, OnClicked: f.onQueuePortable},
						},
					},
					{
						Title:  "Manual Patch",
						Layout: VBox{},
						Children: []Widget{
							Label{Text: "Только для iiko Front.Net"},
							Composite{Layout: Grid{Columns: 2}, Children: []Widget{
								Label{Text: "Версия iikoFront:"},
								LineEdit{AssignTo: &f.manualVersionLE},
							}},
							Composite{Layout: HBox{}, Children: []Widget{
								PushButton{Text: "Загрузить патчи", OnClicked: f.onLoadManualPatches},
								ComboBox{AssignTo: &f.manualPatchCB, Model: f.manualPatchModel},
							}},
							PushButton{Text: "Добавить в очередь: Manual Patch", MinSize: Size{Height: 40}, OnClicked: f.onQueueManualPatch},
						},
					},
					{
						Title:  "Plugins",
						Layout: VBox{},
						Children: []Widget{
							Label{Text: "Установка/обновление плагинов iikoFront"},
							PushButton{Text: "Добавить в очередь: Plugins", MinSize: Size{Height: 40}, OnClicked: f.onQueuePlugins},
						},
					},
				},
			},
		},
	}.Create(NewBuilder(parent))
	if err != nil {
		return nil, err
	}

	f.rbIiko.SetChecked(true)
	f.setBrand("iiko")
	return composite, nil
}

func (f *DistroForm) setBrand(brand string) {
	f.brand = brand
	if brand == "iiko" {
		f.components = f.am.Cfg().DistroConfig.Iiko.Components
	} else {
		f.components = f.am.Cfg().DistroConfig.Syrve.Components
	}

	compModel := NewComponentListModel()
	compModel.Items = f.components
	f.componentCB.SetModel(compModel)
	if len(f.components) > 0 {
		f.componentCB.SetCurrentIndex(0)
	}

	f.portableComponents = f.portableComponents[:0]
	for _, c := range f.components {
		if c.PortableArchiveKey != "" {
			f.portableComponents = append(f.portableComponents, c)
		}
	}
	f.portableCompModel.Items = f.portableComponents
	f.portableCompModel.PublishItemsReset()
	if len(f.portableComponents) > 0 {
		f.portableComponentCB.SetCurrentIndex(0)
	} else {
		f.portableComponentCB.SetCurrentIndex(-1)
	}

	f.onInstallComponentChanged()
}

func (f *DistroForm) onInstallComponentChanged() {
	idx := f.componentCB.CurrentIndex()
	if idx < 0 || idx >= len(f.components) {
		f.loadPatchesBtn.SetEnabled(false)
		f.applyPatchCB.SetEnabled(false)
		f.patchCB.SetEnabled(false)
		return
	}
	comp := f.components[idx]
	isIikoFront := comp.ID == "iiko_front" && f.brand == "iiko"
	f.loadPatchesBtn.SetEnabled(isIikoFront)
	f.applyPatchCB.SetEnabled(isIikoFront)
	f.patchCB.SetEnabled(isIikoFront)
	if !isIikoFront {
		f.patches = nil
		f.patchModel.Items = nil
		f.patchModel.PublishItemsReset()
	}
}

func (f *DistroForm) onLoadInstallPatches() {
	idx := f.componentCB.CurrentIndex()
	if idx < 0 || idx >= len(f.components) {
		f.ctx.Warn("Компонент не выбран")
		return
	}
	comp := f.components[idx]
	if f.brand != "iiko" || comp.ID != "iiko_front" {
		f.ctx.Warn("Патчи доступны только для iiko Front")
		return
	}
	version := strings.TrimSpace(f.versionLE.Text())
	if version == "" {
		f.ctx.Warn("Введите версию перед загрузкой патчей")
		return
	}

	patches, err := distro.FindPatches(f.am.Cfg().DistroConfig.Iiko.PatchesBaseURL, version)
	if err != nil {
		f.ctx.Error(fmt.Sprintf("Ошибка загрузки патчей: %v", err))
		return
	}
	if len(patches) == 0 {
		f.ctx.Warn("Патчи для этой версии не найдены")
		return
	}

	f.patches = patches
	f.patchModel.Items = patches
	f.patchModel.PublishItemsReset()
	f.patchCB.SetCurrentIndex(0)
	f.ctx.Info(fmt.Sprintf("Загружено патчей: %d", len(patches)))
}

func (f *DistroForm) onQueueInstall() {
	idx := f.componentCB.CurrentIndex()
	if idx < 0 || idx >= len(f.components) {
		f.ctx.Warn("Компонент не выбран")
		return
	}
	comp := f.components[idx]
	cfg := &distro.DistroInstallConfig{
		Action:    distro.ActionInstallComponent,
		Brand:     f.brand,
		Component: comp,
		Version:   strings.TrimSpace(f.versionLE.Text()),
	}

	if f.uninstallOldCB.Checked() {
		cfg.UninstallOldVersion = true
		cfg.OldVersionString = strings.TrimSpace(f.oldVersionLE.Text())
		if cfg.OldVersionString == "" {
			f.ctx.Warn("Укажите старую версию для uninstall")
			return
		}
	}

	if f.brand == "iiko" && comp.ID == "iiko_front" {
		cfg.RunAutoUpdatePlugins = f.autoPluginsCB.Checked()
		if f.applyPatchCB.Checked() {
			pidx := f.patchCB.CurrentIndex()
			if pidx < 0 || pidx >= len(f.patches) {
				f.ctx.Warn("Выберите патч или отключите флаг применения патча")
				return
			}
			selected := f.patches[pidx]
			cfg.Patch = &selected
		}
	}

	title := fmt.Sprintf("Install %s %s", comp.MenuText, cfg.Version)
	sig := fmt.Sprintf("install|%s|%s|%s", f.brand, comp.ID, cfg.Version)
	f.enqueueDistroTask("iiko", title, sig, cfg)
}

func (f *DistroForm) onQueuePortable() {
	idx := f.portableComponentCB.CurrentIndex()
	if idx < 0 || idx >= len(f.portableComponents) {
		f.ctx.Warn("Portable компонент не выбран")
		return
	}
	comp := f.portableComponents[idx]
	version := strings.TrimSpace(f.portableVersionLE.Text())
	if version == "" {
		f.ctx.Warn("Введите версию portable")
		return
	}

	cfg, err := f.buildPortableConfig(comp, version)
	if err != nil {
		f.ctx.Error(err.Error())
		return
	}

	title := fmt.Sprintf("Portable %s %s", comp.MenuText, version)
	sig := fmt.Sprintf("portable|%s|%s|%s", f.brand, comp.ID, version)
	f.enqueueDistroTask("iiko", title, sig, cfg)
}

func (f *DistroForm) onLoadManualPatches() {
	if f.brand != "iiko" {
		f.ctx.Warn("Manual patch поддерживается только для iiko")
		return
	}
	version := strings.TrimSpace(f.manualVersionLE.Text())
	if version == "" {
		f.ctx.Warn("Введите версию iikoFront")
		return
	}
	patches, err := distro.FindPatches(f.am.Cfg().DistroConfig.Iiko.PatchesBaseURL, version)
	if err != nil {
		f.ctx.Error(fmt.Sprintf("Ошибка загрузки патчей: %v", err))
		return
	}
	if len(patches) == 0 {
		f.ctx.Warn("Патчи не найдены")
		return
	}
	f.manualPatches = patches
	f.manualPatchModel.Items = patches
	f.manualPatchModel.PublishItemsReset()
	f.manualPatchCB.SetCurrentIndex(0)
	f.ctx.Info(fmt.Sprintf("Загружено патчей: %d", len(patches)))
}

func (f *DistroForm) onQueueManualPatch() {
	if f.brand != "iiko" {
		f.ctx.Warn("Manual patch поддерживается только для iiko")
		return
	}
	pidx := f.manualPatchCB.CurrentIndex()
	if pidx < 0 || pidx >= len(f.manualPatches) {
		f.ctx.Warn("Выберите патч")
		return
	}
	version := strings.TrimSpace(f.manualVersionLE.Text())
	if version == "" {
		f.ctx.Warn("Введите версию iikoFront")
		return
	}
	selected := f.manualPatches[pidx]
	cfg := &distro.DistroInstallConfig{
		Action:  distro.ActionManualPatch,
		Brand:   "iiko",
		Version: version,
		Patch:   &selected,
	}
	title := fmt.Sprintf("Manual Patch %s", selected.ShortName)
	sig := fmt.Sprintf("manual-patch|%s|%s", version, selected.ShortName)
	f.enqueueDistroTask("iiko", title, sig, cfg)
}

func (f *DistroForm) onQueuePlugins() {
	if f.brand != "iiko" {
		f.ctx.Warn("Plugins action доступна только для iiko")
		return
	}
	cfg := &distro.DistroInstallConfig{Action: distro.ActionPlugins, Brand: "iiko"}
	f.enqueueDistroTask("iiko", "Plugins AutoUpdate", "plugins|autoupdate", cfg)
}

func (f *DistroForm) enqueueDistroTask(moduleID, title, signature string, cfg *distro.DistroInstallConfig) {
	_, err := f.tm.Enqueue(TaskSpec{
		ModuleID:  moduleID,
		Title:     title,
		Signature: signature,
		Run: func(taskCtx core.TaskContext) error {
			mod := &distro.Module{}
			return mod.Execute(taskCtx, f.am, f.wu, cfg)
		},
	})
	if err != nil {
		if errors.Is(err, ErrDuplicateTask) {
			f.ctx.Warn("Такая же задача уже в очереди или выполняется")
			return
		}
		f.ctx.Error(fmt.Sprintf("Не удалось поставить задачу в очередь: %v", err))
		return
	}
	f.ctx.Info("Задача добавлена в очередь: " + title)
}

func (f *DistroForm) buildPortableConfig(comp config.DistroComponent, version string) (*distro.DistroInstallConfig, error) {
	if comp.PortableArchiveKey == "" {
		return nil, errors.New("для компонента не настроен portable_archive_key")
	}

	portable := f.am.Cfg().DistroConfig.IikoPortable
	if f.brand == "syrve" {
		portable = f.am.Cfg().DistroConfig.SyrvePortable
	}

	source := strings.ToLower(strings.TrimSpace(f.portableSourceCB.Text()))
	if source == "" {
		source = "auto"
	}

	chooseHTTP := false
	chooseFTP := false
	switch source {
	case "auto":
		if portable.HttpSource.Enabled {
			chooseHTTP = true
		} else if portable.FtpSource.Enabled {
			chooseFTP = true
		}
	case "http":
		chooseHTTP = portable.HttpSource.Enabled
	case "ftp":
		chooseFTP = portable.FtpSource.Enabled
	}
	if !chooseHTTP && !chooseFTP {
		return nil, errors.New("подходящий portable источник недоступен")
	}

	cfg := &distro.DistroInstallConfig{
		Action:    distro.ActionInstallPortable,
		Brand:     f.brand,
		Component: comp,
		Version:   version,
	}

	if chooseHTTP {
		tpl := portable.HttpSource.ArchiveNames[comp.PortableArchiveKey]
		if tpl == "" {
			return nil, fmt.Errorf("не найден http шаблон архива для %s", comp.PortableArchiveKey)
		}
		archive := strings.Replace(tpl, "{{version}}", version, 1)
		cfg.PortableSourceType = "http"
		cfg.PortableArchiveName = archive
		cfg.PortableDownloadURL = strings.TrimRight(portable.HttpSource.URL, "/") + "/" + archive
		return cfg, nil
	}

	tpl := portable.FtpSource.ArchiveNames[comp.PortableArchiveKey]
	if tpl == "" {
		return nil, fmt.Errorf("не найден ftp шаблон архива для %s", comp.PortableArchiveKey)
	}
	archive := strings.Replace(tpl, "{{version}}", version, 1)
	if len(f.am.Cfg().FTP) == 0 {
		return nil, errors.New("в config отсутствует ftp_config")
	}
	cfg.PortableSourceType = "ftp"
	cfg.PortableArchiveName = archive
	cfg.PortableFTPConfig = f.am.Cfg().FTP[0]
	cfg.PortableFTPPath = strings.TrimRight(portable.FtpSource.Directory, "/") + "/" + archive
	return cfg, nil
}

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

type PatchListModel struct {
	walk.ListModelBase
	Items []core.PatchInfo
}

func NewPatchListModel() *PatchListModel {
	return &PatchListModel{}
}

func (m *PatchListModel) ItemCount() int {
	return len(m.Items)
}

func (m *PatchListModel) Value(index int) interface{} {
	p := m.Items[index]
	if p.ShortName == "" {
		return p.Description
	}
	return p.ShortName + " - " + p.Description
}

func init() {
	Register("iiko", NewDistroForm)
}
