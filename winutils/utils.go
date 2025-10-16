package winutils

import (
	"context"
	"encoding/csv"
	"fmt"
	"goMH/dependencies"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/mholt/archives"
	"go.bug.st/serial/enumerator"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
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

	// 2. Создаем временный файл для XML внутри текущей рабочей директории
	tempDir := filepath.Join(".", "temp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать временную директорию: %w", err)
	}
	tempFile, err := os.CreateTemp(tempDir, "task-*.xml")
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

// scannerInfo - это локальная, неэкспортируемая структура для внутреннего использования.
type scannerInfo struct {
	Port        string
	Caption     string
	PNPDeviceID string
}

// GetScanners теперь возвращает срез локальных структур и не зависит от пакета core.// Использует нативную библиотеку для перечисления ВСЕХ COM-портов, включая виртуальные.
func GetScanners() ([]scannerInfo, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, fmt.Errorf("не удалось получить список портов через нативную библиотеку: %w", err)
	}

	if len(ports) == 0 {
		return []scannerInfo{}, nil
	}

	var scanners []scannerInfo
	for _, port := range ports {
		// Нас интересуют только USB-устройства
		if port.IsUSB {
			// Формируем Caption, похожий на тот, что в Диспетчере устройств
			caption := port.Product

			// Формируем PNPDeviceID из доступной информации
			pnpDeviceID := fmt.Sprintf("USB\\VID_%s&PID_%s", port.VID, port.PID)

			scanners = append(scanners, scannerInfo{
				Port:        port.Name,   // e.g., "COM5"
				Caption:     caption,     // e.g., "ATOL USB (COM5)"
				PNPDeviceID: pnpDeviceID, // e.g., "USB\VID_2912&PID_0005"
			})
		}
	}

	return scanners, nil
}

// GetFileVersion читает информацию о версии файла напрямую через WinAPI.
func GetFileVersion(path string) (string, error) {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil {
		return "", fmt.Errorf("GetFileVersionInfoSize failed for %s: %w", path, err)
	}
	if size == 0 {
		return "", fmt.Errorf("no version info found in %s", path)
	}

	buffer := make([]byte, size)
	err = windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buffer[0]))
	if err != nil {
		return "", fmt.Errorf("GetFileVersionInfo failed for %s: %w", path, err)
	}

	var fixedInfo *windows.VS_FIXEDFILEINFO
	var len uint32
	err = windows.VerQueryValue(unsafe.Pointer(&buffer[0]), "\\", unsafe.Pointer(&fixedInfo), &len)
	if err != nil {
		return "", fmt.Errorf("VerQueryValue failed: could not find fixed file info block: %w", err)
	}
	if fixedInfo == nil {
		return "", fmt.Errorf("не найдена структура VS_FIXEDFILEINFO")
	}

	if fixedInfo.Signature != 0xFEEF04BD {
		return "", fmt.Errorf("invalid fixed file info signature")
	}

	major := uint16(fixedInfo.FileVersionMS >> 16)
	minor := uint16(fixedInfo.FileVersionMS & 0xffff)
	patch := uint16(fixedInfo.FileVersionLS >> 16)
	build := uint16(fixedInfo.FileVersionLS & 0xffff)

	return fmt.Sprintf("%d.%d.%d.%d", major, minor, patch, build), nil
}

// ListArchiveContents возвращает список путей файлов внутри архива.
func ListArchiveContents(archivePath string) ([]string, error) {
	var filePaths []string

	// Создаем виртуальную файловую систему из архива
	fsys, err := archives.FileSystem(context.Background(), archivePath, nil)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть архив %s как файловую систему: %w", archivePath, err)
	}

	// Проходим по всем файлам в виртуальной ФС
	err = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // Прерываем обход при ошибке
		}
		if d.IsDir() {
			return nil // Пропускаем директории
		}
		filePaths = append(filePaths, path)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать содержимое архива %s: %w", archivePath, err)
	}

	return filePaths, nil
}

// FindFileRecursive ищет файл по шаблону, начиная с корневой директории.
func FindFileRecursive(root, pattern string) (string, error) {
	var foundPath string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			matched, _ := filepath.Match(pattern, d.Name())
			if matched {
				foundPath = path
				return fs.ErrExist // Прерываем поиск, как только нашли
			}
		}
		return nil
	})
	if err == fs.ErrExist {
		return foundPath, nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("файл по шаблону '%s' не найден в '%s'", pattern, root)
}

// GetStartupFolders возвращает пути к папкам автозагрузки для текущего пользователя и для всех пользователей.
func GetStartupFolders() (user, common string, err error) {
	// Папка автозагрузки текущего пользователя
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	user = filepath.Join(userConfigDir, "..", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup")

	// Общая папка автозагрузки
	common = os.ExpandEnv(`%ProgramData%\Microsoft\Windows\Start Menu\Programs\StartUp`)
	return user, common, nil
}

// DeleteFile просто удаляет файл.
func DeleteFile(path string) error {
	return os.Remove(path)
}

// CleanDirectory удаляет все содержимое директории, не удаляя саму директорию.
func CleanDirectory(path string) error {
	dir, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Если папки нет, считать задачу выполненной
		}
		return err
	}
	for _, d := range dir {
		err := os.RemoveAll(filepath.Join(path, d.Name()))
		if err != nil {
			return err
		}
	}
	return nil
}

// FindScheduledTaskByPath ищет задачу в планировщике по пути к исполняемому файлу.
func FindScheduledTaskByPath(exePath string) (string, error) {
	out, err := RunCommand("schtasks", "/Query", "/V", "/FO", "CSV")
	if err != nil {
		return "", err
	}

	r := csv.NewReader(strings.NewReader(out))
	records, err := r.ReadAll()
	if err != nil {
		return "", err
	}

	absExePath, _ := filepath.Abs(exePath)

	for _, record := range records {
		if len(record) > 8 {
			taskName := record[0]
			taskToRun := record[8]
			// Сравниваем абсолютные пути, чтобы избежать неоднозначности
			absTaskPath, _ := filepath.Abs(strings.Trim(taskToRun, `"`))
			if strings.EqualFold(absTaskPath, absExePath) {
				return taskName, nil
			}
		}
	}
	return "", nil // Не найдено - не ошибка
}

// DeleteScheduledTaskByName удаляет задачу по имени.
func DeleteScheduledTaskByName(taskName string) error {
	_, err := RunCommand("schtasks", "/Delete", "/TN", taskName, "/F")
	return err
}

// FindNewestFileByPattern ищет самый новый файл по паттерну в указанной директории и всех поддиректориях.
// Возвращает путь к файлу с самой поздней датой последнего изменения или ошибку если файлы не найдены.
func FindNewestFileByPattern(root, pattern string) (string, error) {
	var newestFile string
	var newestModTime time.Time
	found := false

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Продолжаем поиск даже при ошибках доступа
		}

		if d.IsDir() {
			return nil // Пропускаем директории
		}

		// Проверяем соответствие паттерну
		matched, err := filepath.Match(pattern, d.Name())
		if err != nil {
			return nil // Продолжаем поиск при ошибке сопоставления паттерна
		}

		if matched {
			fileInfo, err := d.Info()
			if err != nil {
				return nil // Продолжаем поиск при ошибке получения информации о файле
			}

			// Если это первый найденный файл или файл новее предыдущего
			if !found || fileInfo.ModTime().After(newestModTime) {
				newestModTime = fileInfo.ModTime()
				newestFile = path
				found = true
			}
		}

		return nil
	})

	if err != nil {
		return "", fmt.Errorf("ошибка при обходе директории %s: %w", root, err)
	}

	if !found {
		return "", fmt.Errorf("файлы по паттерну '%s' не найдены в '%s'", pattern, root)
	}

	return newestFile, nil
}

// GetServiceStatus возвращает статус службы (например, "RUNNING", "STOPPED").
// Функция ищет непереводимые английские ключевые слова статуса,
// что делает ее нечувствительной к языку операционной системы.
func GetServiceStatus(serviceName string) (string, error) {
	out, err := RunCommand("sc.exe", "query", serviceName)
	if err != nil {
		if strings.Contains(err.Error(), "1060") { // Служба не существует
			return "NOT_FOUND", nil
		}
		return "", err
	}

	// Этот шаблон ищет одно из стандартных, непереводимых состояний службы.
	// Они всегда выводятся в верхнем регистре на английском языке.
	re := regexp.MustCompile(`(STOPPED|START_PENDING|STOP_PENDING|RUNNING|CONTINUE_PENDING|PAUSE_PENDING|PAUSED)`)
	matches := re.FindStringSubmatch(out)

	if len(matches) > 1 {
		// Возвращаем первое найденное совпадение, например, "RUNNING"
		return matches[1], nil
	}

	return "UNKNOWN", fmt.Errorf("не удалось определить статус службы из вывода sc.exe")
}

// CopyFile копирует файл из src в dst.
// Эта версия специально адаптирована для xcopy: в качестве dst передается только директория.
func CopyFile(src, dst string) error {
	// Для xcopy нужно указать только целевую ДИРЕКТОРИЮ, чтобы избежать вопроса "File or Directory?".
	destDir := filepath.Dir(dst)

	// Копируем с сохранением атрибутов (/H скрытые, /K атрибуты), /I предполагает, что назначение - это каталог.
	_, err := RunCommand("xcopy", src, destDir, "/Y", "/H", "/K", "/I")
	return err
}

// CopyDir рекурсивно копирует директорию из src в dst с сохранением атрибутов
func CopyDir(src, dst string) error {
	// Используем robocopy для копирования директорий с сохранением всех атрибутов
	// /E - копировать поддиректории включая пустые
	// /COPYALL - копировать все атрибуты (время, атрибуты, владелец, аудиты)
	// /R:0 - не повторять при ошибках
	// /W:0 - не ждать при ошибках
	output, err := RunCommand("robocopy", src, dst, "/E", "/COPYALL", "/R:0", "/W:0")
	if err != nil {
		// Robocopy возвращает коды выхода: 0-7 успешные, 8+ ошибки
		// Но RunCommand возвращает ошибку только если команда не запустилась
		// Проверим код выхода, но поскольку RunCommand не возвращает код, проверим output
		if strings.Contains(output, "ERROR") || strings.Contains(output, "не удалось") {
			return fmt.Errorf("ошибка копирования директории: %s", output)
		}
	}
	return nil
}

// MoveDir перемещает директорию из src в dst (копирует и удаляет)
func MoveDir(src, dst string) error {
	if err := CopyDir(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// ReadRegistryKey читает строковое значение из указанного ключа реестра.
func ReadRegistryKey(rootKey registry.Key, path, valueName string) (string, error) {
	k, err := registry.OpenKey(rootKey, path, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()

	s, _, err := k.GetStringValue(valueName)
	if err != nil {
		return "", err
	}
	return s, nil
}

// ExtractArchive распаковывает архив в указанную директорию с помощью 7-Zip.
// fullPaths: true использует флаг 'x' (сохранение структуры папок), false - флаг 'e' (плоская распаковка).
func ExtractArchive(archivePath, destDir string, fullPaths bool) error {
	// Создаем клиент 7-Zip
	client, err := dependencies.NewClient(nil, nil)
	if err != nil {
		return fmt.Errorf("не удалось создать клиент 7-Zip: %w", err)
	}

	// Распаковываем архив
	return client.Extract(archivePath, destDir, fullPaths)
}
