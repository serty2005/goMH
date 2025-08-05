package winutils

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// IsAdmin остается без изменений
func IsAdmin() bool {
	_, err := os.Open("\\\\.\\PHYSICALDRIVE0")
	return err == nil
}

// RunCommand остается без изменений
func RunCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LANG=en_US.UTF-8")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ошибка выполнения '%s %v': %v, вывод: %s", name, args, err, string(output))
	}
	return strings.TrimSpace(string(output)), nil
}

func RunCommandWithEnv(env map[string]string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)

	// Собираем переменные окружения
	newEnv := os.Environ() // Начинаем с существующих
	for key, value := range env {
		newEnv = append(newEnv, fmt.Sprintf("%s=%s", key, value))
	}
	cmd.Env = newEnv // Устанавливаем их для команды

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ошибка выполнения '%s %v' с кастомным env: %v, вывод: %s", name, args, err, string(output))
	}
	return strings.TrimSpace(string(output)), nil
}

// CreateScheduledTask создает или обновляет задачу в Планировщике Windows через импорт XML.
func CreateScheduledTask(taskName, executablePath, workingDir string) error {
	fmt.Printf("Создание/обновление задачи '%s' через XML...\n", taskName)

	// 1. Генерируем XML-содержимое для задачи
	xmlContent, err := generateTaskXML(taskName, executablePath, workingDir)
	if err != nil {
		return fmt.Errorf("не удалось сгенерировать XML для задачи: %w", err)
	}

	// 2. Создаем временный файл для XML
	tempFile, err := os.CreateTemp("", "task-*.xml")
	if err != nil {
		return fmt.Errorf("не удалось создать временный XML-файл: %w", err)
	}
	// Гарантируем удаление временного файла после завершения функции
	defer os.Remove(tempFile.Name())

	// 3. Записываем XML во временный файл
	if _, err := tempFile.Write([]byte(xmlContent)); err != nil {
		tempFile.Close() // Закрываем файл перед попыткой удаления
		return fmt.Errorf("не удалось записать XML во временный файл: %w", err)
	}
	tempFile.Close() // Важно закрыть файл перед тем, как его прочитает schtasks

	// 4. Используем schtasks для создания/обновления задачи из XML
	// Флаг /F (Force) автоматически перезаписывает задачу, если она уже существует.
	output, err := RunCommand("schtasks", "/Create", "/TN", taskName, "/XML", tempFile.Name(), "/F")
	if err != nil {
		return fmt.Errorf("не удалось создать задачу из XML: %w", err)
	}

	fmt.Printf("Задача '%s' успешно создана/обновлена. Вывод schtasks: %s\n", taskName, output)
	return nil
}

// generateTaskXML создает строку с XML-описанием задачи.
func generateTaskXML(taskName, executablePath, workingDir string) (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("не удалось определить текущего пользователя: %w", err)
	}
	// SID пользователя в формате S-1-5-21... или "BUILTIN\Administrators"
	// Для современных систем лучше использовать его имя.
	userID := currentUser.Username

	absExecutablePath, err := filepath.Abs(executablePath)
	if err != nil {
		return "", err
	}

	absWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		return "", err
	}

	// Шаблон XML для задачи
	xmlTemplate := `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Date>%s</Date>
    <Author>%s</Author>
    <Description>Autostart for %s</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>"%s"</Command>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>`

	// Форматируем XML с нужными данными
	currentTime := time.Now().Format(time.RFC3339)
	return fmt.Sprintf(xmlTemplate,
		currentTime,
		userID,
		taskName,
		userID,
		userID,
		absExecutablePath, // Команда
		absWorkingDir,     // Рабочая директория
	), nil
}

// GetComPorts остается без изменений
func GetComPorts() ([]string, error) {
	out, err := RunCommand("wmic", "path", "Win32_SerialPort", "get", "DeviceID")
	if err != nil {
		return nil, fmt.Errorf("не удалось получить список COM портов через WMIC: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	ports := []string{}
	re := regexp.MustCompile(`(COM\d+)`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if re.MatchString(line) {
			ports = append(ports, line)
		}
	}
	return ports, nil
}

func IsProcessRunning(processName string) (bool, error) {
	// 1. Получаем список ВСЕХ процессов в формате CSV без заголовков.
	// Эта команда завершится успешно, даже если процессов много или мало.
	out, err := RunCommand("tasklist", "/NH", "/FO", "CSV")
	if err != nil {
		// Если сама команда tasklist не смогла выполниться, это серьезная ошибка.
		return false, fmt.Errorf("не удалось выполнить tasklist: %w", err)
	}

	// 2. Используем встроенный в Go CSV-парсер для анализа вывода.
	r := csv.NewReader(strings.NewReader(out))
	records, err := r.ReadAll()
	if err != nil {
		return false, fmt.Errorf("не удалось распарсить CSV-вывод tasklist: %w", err)
	}

	// 3. Итерируем по списку процессов и ищем нужный.
	for _, record := range records {
		// Первая колонка в выводе tasklist - это "Image Name".
		// Например, "iikoFront.Net.exe"
		if len(record) > 0 {
			imageName := record[0]
			// Проверяем, начинается ли имя процесса с искомой строки.
			// Это покрывает случаи вроде "iikoFront.exe", "iikoFront.Net.exe" и т.д.
			if strings.HasPrefix(strings.ToLower(imageName), strings.ToLower(processName)) {
				// Нашли!
				return true, nil
			}
		}
	}

	// Если прошли весь список и не нашли, значит, процесс не запущен.
	return false, nil
}

func ManageService(action, serviceName string) error {
	_, err := RunCommand("sc.exe", action, serviceName)
	if err != nil {
		// Ошибки от sc.exe часто не являются критичными (например, попытка остановить уже остановленную службу).
		// Мы просто логируем их как предупреждение.
		fmt.Printf("Предупреждение при выполнении 'sc %s %s': %v\n", action, serviceName, err)
		// Возвращаем nil, чтобы не прерывать выполнение скрипта.
		return nil
	}
	fmt.Printf("Команда 'sc %s %s' выполнена.\n", action, serviceName)
	return nil
}

func AddDefenderExclusion(path string) error {
	// Эта команда PowerShell требует запуска от имени администратора.
	powerShellCommand := fmt.Sprintf("Add-MpPreference -ExclusionPath '%s'", path)
	_, err := RunCommand("powershell", "-NoProfile", "-Command", powerShellCommand)
	if err != nil {
		// Ошибка может означать, что Defender не активен, или исключение уже существует.
		// Логируем как предупреждение.
		fmt.Printf("Предупреждение при добавлении исключения для Defender: %v\n", err)
		return nil
	}
	fmt.Printf("Путь '%s' добавлен в исключения Defender (или уже был там).\n", path)
	return nil
}

// Is64BitOS проверяет, является ли операционная система 64-битной.
func Is64BitOS() bool {
	// runtime.GOARCH вернет "amd64" для 64-битных систем
	// и "386" для 32-битных.
	return runtime.GOARCH == "amd64"
}

func ServiceExists(serviceName string) (bool, error) {
	// sc.exe query <serviceName> вернет ошибку, если служба не найдена.
	// Мы ищем конкретный текст ошибки, чтобы отличить "не найдено" от других проблем.
	_, err := RunCommand("sc.exe", "query", serviceName)
	if err == nil {
		// Команда выполнилась без ошибок, значит служба существует.
		return true, nil
	}

	// Проверяем, является ли ошибка именно той, что нам нужна.
	// Код 1060: The specified service does not exist as an installed service.
	// Текст может быть локализован, но поиск по коду или стандартной английской фразе надежен.
	if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "1060") {
		// Это ожидаемая "ошибка", если службы нет. Для нас это не ошибка.
		return false, nil
	}

	// Если произошла другая, непредвиденная ошибка (например, нет прав), возвращаем ее.
	return false, fmt.Errorf("не удалось выполнить проверку службы '%s': %w", serviceName, err)
}

// SetServiceTriggers устанавливает триггеры запуска для службы Windows.
// triggers - это слайс строк, например ["start/machinepolicy", "start/userpolicy"]
func SetServiceTriggers(serviceName string, triggers []string) error {
	args := []string{"triggerinfo", serviceName}
	args = append(args, triggers...)

	output, err := RunCommand("sc.exe", args...)
	if err != nil {
		// Ошибка здесь может быть критичной, поэтому возвращаем ее.
		return fmt.Errorf("не удалось установить триггеры для службы '%s': %s. Ошибка: %w", serviceName, output, err)
	}
	fmt.Printf("Триггеры для службы '%s' успешно установлены.\n", serviceName)
	return nil
}
