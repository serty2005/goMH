package tui

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// ReadKey читает ровно одно нажатие клавиши от пользователя без необходимости нажимать Enter.
// Возвращает строковое представление символа.
// Обрабатывает Ctrl+C (код 3) как ошибку "interrupt".
func ReadKey() (string, error) {
	// Получаем дескриптор stdin
	fd := int(os.Stdin.Fd())

	// Переключаем терминал в Raw mode
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("не удалось переключить терминал в raw mode: %w", err)
	}
	// Обязательно восстанавливаем состояние терминала при выходе
	defer term.Restore(fd, state)

	b := make([]byte, 1)
	_, err = os.Stdin.Read(b)
	if err != nil {
		return "", err
	}

	// Обработка Ctrl+C (ETX - End of Text)
	if b[0] == 3 {
		return "", fmt.Errorf("interrupt")
	}

	return string(b), nil
}

// WaitForAnyKey ждет нажатия любой клавиши
func WaitForAnyKey() {
	fmt.Print("\nНажмите любую клавишу для продолжения...")
	_, _ = ReadKey()
	fmt.Println() // Перенос строки для красоты
}
