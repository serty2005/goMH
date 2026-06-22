package winutils

import (
	"debug/pe"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/windows/registry"
)

var reExecLevel = regexp.MustCompile(`(?i)requestedExecutionLevel[^>]*level="([^"]+)"`)

// RequiresAdminElevation проверяет, требует ли exe прав администратора.
// Проверяет три источника: PE-манифест (.rsrc), внешний .manifest-файл
// и флаг AppCompatFlags\Layers (вкладка "Совместимость → Запускать от имени администратора").
func RequiresAdminElevation(exePath string) (bool, error) {
	if level, _ := readPEManifestElevationLevel(exePath); isElevatedLevel(level) {
		return true, nil
	}
	if level, _ := readExternalManifestElevationLevel(exePath); isElevatedLevel(level) {
		return true, nil
	}
	return checkCompatLayerRunAsAdmin(exePath), nil
}

// AddScheduledAutostartTask создаёт задачу планировщика Windows, которая запускает
// exePath при входе пользователя с наивысшими доступными правами (HighestAvailable).
func AddScheduledAutostartTask(name, exePath, arguments string) error {
	workingDir := filepath.Dir(exePath)
	return NewRuntime().CreateScheduledTask(name, exePath, arguments, workingDir)
}

func isElevatedLevel(level string) bool {
	l := strings.ToLower(strings.TrimSpace(level))
	return l == "requireadministrator" || l == "highestavailable"
}

// readPEManifestElevationLevel читает requestedExecutionLevel из секции .rsrc PE-файла.
func readPEManifestElevationLevel(exePath string) (string, error) {
	f, err := pe.Open(exePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	section := f.Section(".rsrc")
	if section == nil {
		return "", fmt.Errorf("no .rsrc section")
	}
	data, err := section.Data()
	if err != nil {
		return "", err
	}
	if m := reExecLevel.FindSubmatch(data); m != nil {
		return string(m[1]), nil
	}
	return "", fmt.Errorf("requestedExecutionLevel not found")
}

// readExternalManifestElevationLevel читает requestedExecutionLevel из файла exePath.manifest.
// .NET-приложения часто хранят манифест в отдельном файле рядом с exe.
func readExternalManifestElevationLevel(exePath string) (string, error) {
	data, err := os.ReadFile(exePath + ".manifest")
	if err != nil {
		return "", err
	}
	if m := reExecLevel.FindSubmatch(data); m != nil {
		return string(m[1]), nil
	}
	return "", fmt.Errorf("requestedExecutionLevel not found in .manifest")
}

// checkCompatLayerRunAsAdmin проверяет AppCompatFlags\Layers — флаг RUNASADMIN,
// который выставляется через вкладку "Совместимость → Запускать от имени администратора".
func checkCompatLayerRunAsAdmin(exePath string) bool {
	const layersPath = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\AppCompatFlags\Layers`
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		k, err := registry.OpenKey(root, layersPath, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		val, _, err := k.GetStringValue(exePath)
		k.Close()
		if err == nil && strings.Contains(strings.ToUpper(val), "RUNASADMIN") {
			return true
		}
	}
	return false
}
