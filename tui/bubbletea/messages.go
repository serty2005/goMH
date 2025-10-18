package bubbletea

// ErrMsg представляет сообщение об ошибке для показа пользователю
type ErrMsg struct {
	Err error
}

// Error возвращает строковое представление ошибки
func (e ErrMsg) Error() string {
	return e.Err.Error()
}

// ProgressMsg представляет сообщение об обновлении прогресса длительной операции
type ProgressMsg struct {
	// TaskID уникальный идентификатор задачи
	TaskID string
	// Progress значение прогресса от 0.0 до 1.0
	Progress float64
	// Text текстовое описание текущего этапа операции
	Text string
}

// DownloadCompleteMsg представляет сообщение о завершении скачивания
type DownloadCompleteMsg struct {
	// TaskID уникальный идентификатор задачи скачивания
	TaskID string
	// Path путь к скачанному файлу
	Path string
	// Err ошибка, если скачивание завершилось неудачно
	Err error
}

// InstallCompleteMsg представляет сообщение о завершении установки модуля
type InstallCompleteMsg struct {
	// ModuleName название установленного модуля
	ModuleName string
	// Success флаг успешности установки
	Success bool
	// Err ошибка, если установка завершилась неудачно
	Err error
}

// ModuleSelectedMsg представляет сообщение о выборе модуля в главном меню
type ModuleSelectedMsg struct {
	// ModuleName название выбранного модуля
	ModuleName string
	// ModuleIndex индекс модуля в списке
	ModuleIndex int
}

// BackToMenuMsg представляет сообщение о возврате в главное меню
type BackToMenuMsg struct{}

// NewErrMsg создает новое сообщение об ошибке
func NewErrMsg(err error) ErrMsg {
	return ErrMsg{Err: err}
}

// NewProgressMsg создает новое сообщение о прогрессе
func NewProgressMsg(taskID string, progress float64, text string) ProgressMsg {
	return ProgressMsg{
		TaskID:   taskID,
		Progress: progress,
		Text:     text,
	}
}

// NewDownloadCompleteMsg создает новое сообщение о завершении скачивания
func NewDownloadCompleteMsg(taskID, path string, err error) DownloadCompleteMsg {
	return DownloadCompleteMsg{
		TaskID: taskID,
		Path:   path,
		Err:    err,
	}
}

// NewInstallCompleteMsg создает новое сообщение о завершении установки
func NewInstallCompleteMsg(moduleName string, success bool, err error) InstallCompleteMsg {
	return InstallCompleteMsg{
		ModuleName: moduleName,
		Success:    success,
		Err:        err,
	}
}

// NewModuleSelectedMsg создает новое сообщение о выборе модуля
func NewModuleSelectedMsg(moduleName string, moduleIndex int) ModuleSelectedMsg {
	return ModuleSelectedMsg{
		ModuleName:  moduleName,
		ModuleIndex: moduleIndex,
	}
}

// NewBackToMenuMsg создает новое сообщение о возврате в меню
func NewBackToMenuMsg() BackToMenuMsg {
	return BackToMenuMsg{}
}

// VersionsLoadedMsg представляет сообщение о загрузке доступных версий
type VersionsLoadedMsg struct {
	// Versions список доступных версий
	Versions []string
	// Err ошибка загрузки версий
	Err error
}

// NewVersionsLoadedMsg создает новое сообщение о загрузке версий
func NewVersionsLoadedMsg(versions []string, err error) VersionsLoadedMsg {
	return VersionsLoadedMsg{
		Versions: versions,
		Err:      err,
	}
}

// InstallStartMsg представляет сообщение о начале установки дистрибутива
type InstallStartMsg struct{}

// NewInstallStartMsg создает новое сообщение о начале установки
func NewInstallStartMsg() InstallStartMsg {
	return InstallStartMsg{}
}

// ModuleConfirmMsg представляет сообщение о подтверждении выполнения модуля
type ModuleConfirmMsg struct {
	// ModuleName название модуля для выполнения
	ModuleName string
	// ModuleIndex индекс модуля в списке
	ModuleIndex int
}

// ModuleCancelMsg представляет сообщение об отмене выполнения модуля
type ModuleCancelMsg struct{}

// NewModuleConfirmMsg создает новое сообщение о подтверждении модуля
func NewModuleConfirmMsg(moduleName string, moduleIndex int) ModuleConfirmMsg {
	return ModuleConfirmMsg{
		ModuleName:  moduleName,
		ModuleIndex: moduleIndex,
	}
}

// NewModuleCancelMsg создает новое сообщение об отмене модуля
func NewModuleCancelMsg() ModuleCancelMsg {
	return ModuleCancelMsg{}
}
