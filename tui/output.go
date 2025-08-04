package tui

import "fmt"

// Используем простые ANSI-коды, они хорошо работают в современных терминалах Windows.
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
)

// Info выводит информационное сообщение (голубой цвет).
func Info(msg string) {
	fmt.Println(ColorCyan + msg + ColorReset)
}

// InfoF форматирует и выводит информационное сообщение.
func InfoF(format string, a ...interface{}) {
	Info(fmt.Sprintf(format, a...))
}

// Success выводит сообщение об успехе (зеленый цвет).
func Success(msg string) {
	fmt.Println(ColorGreen + msg + ColorReset)
}

// SuccessF форматирует и выводит сообщение об успехе.
func SuccessF(format string, a ...interface{}) {
	Success(fmt.Sprintf(format, a...))
}

// Warn выводит предупреждение (желтый цвет).
func Warn(msg string) {
	fmt.Println(ColorYellow + msg + ColorReset)
}

// Error выводит сообщение об ошибке (красный цвет).
func Error(msg string) {
	fmt.Println(ColorRed + msg + ColorReset)
}

// Title выводит заголовок (синий цвет).
func Title(msg string) {
	fmt.Println(ColorBlue + msg + ColorReset)
}
