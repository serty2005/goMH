package gui

import (
	"fmt"
	"time"

	"github.com/lxn/walk"
)

// GuiContext is used by GUI forms for immediate UI feedback.
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
	line := fmt.Sprintf("[%s] [%s] %s", time.Now().Format("15:04:05"), prefix, msg)
	c.AppendRawLog(line)
}

func (c *GuiContext) AppendRawLog(line string) {
	c.mw.Synchronize(func() {
		c.logText.AppendText(line + "\r\n")
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
			c.progress.SetValue(0)
			return
		}
		c.progress.SetValue(percent)
	})
}

// TaskGuiContext is created per task and forwards telemetry to TaskManager.
type TaskGuiContext struct {
	tm     *TaskManager
	taskID string
	module string
}

func (c *TaskGuiContext) log(level, msg string) {
	line := fmt.Sprintf("[%s][%s][%s][%s] %s", time.Now().Format("15:04:05"), c.module, c.taskID, level, msg)
	c.tm.appendLog(c.taskID, line)
}

func (c *TaskGuiContext) Info(msg string) {
	c.log("INFO", msg)
}

func (c *TaskGuiContext) Warn(msg string) {
	c.log("WARN", msg)
}

func (c *TaskGuiContext) Error(msg string) {
	c.log("ERROR", msg)
}

func (c *TaskGuiContext) Success(msg string) {
	c.log("SUCCESS", msg)
}

func (c *TaskGuiContext) SetStatus(text string) {
	c.tm.updateStatus(c.taskID, text)
	c.log("STAGE", text)
}

func (c *TaskGuiContext) SetProgress(percent int) {
	c.tm.updateProgress(c.taskID, percent)
}
