package winutils

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func CreateOneShotScheduledTask(taskName, executablePath string, arguments []string, workingDir string, runAt time.Time) error {
	absExecutable, err := filepath.Abs(executablePath)
	if err != nil {
		return fmt.Errorf("абсолютный путь watchdog executable: %w", err)
	}
	absWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		return fmt.Errorf("абсолютный рабочий каталог watchdog: %w", err)
	}

	taskXML := buildOneShotTaskXML(absExecutable, arguments, absWorkingDir, runAt)

	tempDir := filepath.Join(os.TempDir(), "goMH-watchdog")
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return fmt.Errorf("создание временного каталога watchdog: %w", err)
	}
	tempFile, err := os.CreateTemp(tempDir, "task-*.xml")
	if err != nil {
		return fmt.Errorf("создание XML watchdog: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := tempFile.Write(encodeUTF16LE(taskXML)); err != nil {
		tempFile.Close()
		return fmt.Errorf("запись XML watchdog: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("закрытие XML watchdog: %w", err)
	}
	if _, err := RunCommand("schtasks", "/Create", "/TN", taskName, "/XML", tempPath, "/F"); err != nil {
		return fmt.Errorf("создание watchdog task %q: %w", taskName, err)
	}
	return nil
}

func buildOneShotTaskXML(executablePath string, arguments []string, workingDir string, runAt time.Time) string {
	escapeXML := func(value string) string {
		var builder strings.Builder
		_ = xml.EscapeText(&builder, []byte(value))
		return builder.String()
	}
	escapedArgs := make([]string, len(arguments))
	for index, argument := range arguments {
		escapedArgs[index] = windows.EscapeArg(argument)
	}

	taskXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>goMH temporary IPv4 safety watchdog</Description></RegistrationInfo>
  <Triggers><TimeTrigger><StartBoundary>%s</StartBoundary><Enabled>true</Enabled></TimeTrigger></Triggers>
  <Principals><Principal id="System"><UserId>S-1-5-18</UserId><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <AllowHardTerminate>true</AllowHardTerminate>
    <ExecutionTimeLimit>PT5M</ExecutionTimeLimit>
  </Settings>
  <Actions Context="System"><Exec><Command>%s</Command><Arguments>%s</Arguments><WorkingDirectory>%s</WorkingDirectory></Exec></Actions>
</Task>`,
		escapeXML(runAt.Local().Format("2006-01-02T15:04:05")),
		escapeXML(executablePath),
		escapeXML(strings.Join(escapedArgs, " ")),
		escapeXML(workingDir),
	)
	return taskXML
}

func ScheduledTaskExists(taskName string) (bool, error) {
	_, err := RunCommand("schtasks", "/Query", "/TN", taskName)
	if err == nil {
		return true, nil
	}
	// schtasks не предоставляет стабильный locale-independent код для отсутствующей задачи.
	output := strings.ToLower(err.Error())
	if strings.Contains(output, "cannot find") || strings.Contains(output, "не удается найти") || strings.Contains(output, "не найден") {
		return false, nil
	}
	return false, err
}

func OpenURL(rawURL string) error {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return errors.New("разрешены только HTTP/HTTPS URL")
	}
	return StartDetachedProcess("rundll32.exe", "url.dll,FileProtocolHandler", rawURL)
}
