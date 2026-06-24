package core

import (
	"context"
	"log/slog"
)

type silentTaskContext struct {
	runtime context.Context
}

func NewSilentTaskContext(runtimeCtx context.Context) TaskContext {
	if runtimeCtx == nil {
		runtimeCtx = context.Background()
	}
	return &silentTaskContext{runtime: runtimeCtx}
}

func (c *silentTaskContext) Context() context.Context { return c.runtime }
func (c *silentTaskContext) Info(string)              {}
func (c *silentTaskContext) Warn(string)              {}
func (c *silentTaskContext) Error(string)             {}
func (c *silentTaskContext) Success(string)           {}
func (c *silentTaskContext) SetStatus(string)         {}
func (c *silentTaskContext) SetProgress(int)          {}
func (c *silentTaskContext) SetCancelable(bool)       {}

// slogTaskContext маршрутизирует все сообщения TaskContext в slog.
// Используется там, где нет живого UI-канала (конфигурация, немедленные задачи),
// чтобы ни одно сообщение жизненного цикла не пропало.
type slogTaskContext struct {
	runtime context.Context
}

func NewSlogTaskContext(runtimeCtx context.Context) TaskContext {
	if runtimeCtx == nil {
		runtimeCtx = context.Background()
	}
	return &slogTaskContext{runtime: runtimeCtx}
}

func (c *slogTaskContext) Context() context.Context { return c.runtime }
func (c *slogTaskContext) Info(msg string)          { slog.Info(msg) }
func (c *slogTaskContext) Warn(msg string)          { slog.Warn(msg) }
func (c *slogTaskContext) Error(msg string)         { slog.Error(msg) }
func (c *slogTaskContext) Success(msg string)       { slog.Info(msg, "result", "success") }
func (c *slogTaskContext) SetStatus(text string)    { slog.Debug(text, "stage", "status") }
func (c *slogTaskContext) SetProgress(int)          {}
func (c *slogTaskContext) SetCancelable(bool)       {}
