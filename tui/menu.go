package tui

import (
	"bufio"
	"fmt"
	"goMH/core"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Installer - локальный интерфейс, чтобы не импортировать main
type Installer core.Installer

func clearScreen() {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		_ = cmd.Run()
	} else {
		fmt.Print("\033[H\033[2J")
	}
}

func ShowMenu(modules []Installer) (Installer, error) {
	reader := bufio.NewReader(os.Stdin)

	for {
		clearScreen()
		fmt.Println(ColorYellow + "==================================================" + ColorReset)
		fmt.Println(ColorYellow + "      МЕНЮ УСТАНОВЩИКА MYHORECA (golang)          " + ColorReset)
		fmt.Println(ColorYellow + "==================================================" + ColorReset)
		fmt.Println()

		for i, mod := range modules {
			// Используем стандартный fmt.Printf, но можем добавить цвет, если хотим
			fmt.Printf(" %d. %s\n", i+1, mod.MenuText())
		}
		fmt.Println()
		fmt.Println(" Q. Выход")
		fmt.Println()
		fmt.Print("Введите номер пункта и нажмите Enter: ")

		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		if strings.EqualFold(choiceStr, "q") {
			return nil, fmt.Errorf("пользователь выбрал выход")
		}

		choiceInt, err := strconv.Atoi(choiceStr)
		if err != nil || choiceInt < 1 || choiceInt > len(modules) {
			Error("\nНеверный выбор. Нажмите Enter, чтобы попробовать снова.")
			_, _ = reader.ReadString('\n') // Ожидаем нажатия Enter
			continue
		}

		return modules[choiceInt-1], nil
	}
}
