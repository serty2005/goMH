package gui

import (
	"fmt"

	"github.com/lxn/walk"
)

// GuiContext реализует core.TaskContext для графического интерфейса.
// Все методы используют Synchronize для потокобезопасного обновления UI.
type GuiContext struct {
	mw        *walk.MainWindow
	logText   *walk.TextEdit
	statusBar *walk.StatusBarItem
	progress  *walk.ProgressBar
}

func NewGuiContext(mw *walk.MainWindow, logText *walk.TextEdit, status *walk.StatusBarItem, progress *walk.ProgressBar) *GuiContext {
	return &GuiContext{
		mw:        mw,
		logText:   logText,
		statusBar: status,
		progress:  progress,
	}
}

func (c *GuiContext) appendLog(prefix, msg string) {
	c.mw.Synchronize(func() {
		// Добавляем время и сообщение в лог
		text := fmt.Sprintf("[%s] %s\r\n", prefix, msg)
		c.logText.AppendText(text)
		// Прокрутка вниз
		c.logText.SetTextSelection(len(c.logText.Text()), len(c.logText.Text()))
	})
}

func (c *GuiContext) Info(msg string) {
	c.appendLog("INFO", msg)
	c.SetStatus(msg)
}

func (c *GuiContext) Warn(msg string) {
	c.appendLog("WARN", msg)
}

func (c *GuiContext) Error(msg string) {
	c.appendLog("ERROR", msg)
	c.mw.Synchronize(func() {
		walk.MsgBox(c.mw, "Ошибка", msg, walk.MsgBoxIconError)
	})
}

func (c *GuiContext) Success(msg string) {
	c.appendLog("SUCCESS", msg)
	c.mw.Synchronize(func() {
		walk.MsgBox(c.mw, "Успех", msg, walk.MsgBoxIconInformation)
	})
}

func (c *GuiContext) SetStatus(text string) {
	c.mw.Synchronize(func() {
		c.statusBar.SetText(text)
	})
}

func (c *GuiContext) SetProgress(percent int) {
	c.mw.Synchronize(func() {
		if percent < 0 {
			// Неопределенный прогресс (marquee) сложен для стандартного walk ProgressBar без стилей,
			// поэтому просто ставим 0 или 50.
			c.progress.SetValue(0)
		} else {
			c.progress.SetValue(percent)
		}
	})
}
