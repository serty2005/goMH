package core

import "context"

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
