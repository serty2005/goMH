package tui

import "fmt"

// ConsoleContext реализует core.TaskContext для консольного интерфейса.
type ConsoleContext struct{}

func NewConsoleContext() *ConsoleContext {
	return &ConsoleContext{}
}

func (c *ConsoleContext) Info(msg string) {
	Info(msg)
}

func (c *ConsoleContext) Warn(msg string) {
	Warn(msg)
}

func (c *ConsoleContext) Error(msg string) {
	Error(msg)
}

func (c *ConsoleContext) Success(msg string) {
	Success(msg)
}

func (c *ConsoleContext) SetStatus(text string) {
	// В консоли статус часто дублирует Info, но выделим цветом заголовка
	fmt.Printf("\n--- %s ---\n", text)
}

func (c *ConsoleContext) SetProgress(percent int) {
	// В простом консольном выводе мы не будем спамить процентами,
	// так как assetmgr (пока что) использует свой progressbar.
	// Здесь можно реализовать текстовый прогресс, если нужно.
}
