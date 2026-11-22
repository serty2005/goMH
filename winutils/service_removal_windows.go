package winutils

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// RemoveServiceWithProcesses останавливает и удаляет службу, завершая оставшиеся
// процессы по точным именам образов. Отсутствие службы или процессов допустимо.
func RemoveServiceWithProcesses(ctx context.Context, serviceName string, processNames ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(serviceName)
	if err != nil {
		return err
	}
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("не удалось открыть диспетчер служб: %w", err)
	}
	defer windows.CloseServiceHandle(scm)

	if err := requestServiceRemoval(scm, name); err != nil {
		return fmt.Errorf("не удалось удалить службу %s: %w", serviceName, err)
	}

	killErrors := make(map[string]error)
	for _, processName := range processNames {
		if _, err := RunCommand("taskkill.exe", "/F", "/IM", processName); err != nil {
			killErrors[processName] = err
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		service, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS)
		switch {
		case err == nil:
			windows.CloseServiceHandle(service)
		case errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST):
			// Только этот код означает, что имя службы уже можно использовать.
		case errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE):
		default:
			return fmt.Errorf("не удалось проверить удаление службы %s: %w", serviceName, err)
		}
		serviceRemoved := errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST)

		running, err := remainingProcesses(processNames)
		if err != nil {
			return fmt.Errorf("не удалось проверить завершение процессов: %w", err)
		}
		if serviceRemoved && len(running) == 0 {
			return nil
		}

		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			if len(running) != 0 {
				causes := []error{waitCtx.Err()}
				for _, processName := range running {
					if err := killErrors[processName]; err != nil {
						causes = append(causes, err)
					}
				}
				return fmt.Errorf("не завершились процессы %s: %w", strings.Join(running, ", "), errors.Join(causes...))
			}
			return fmt.Errorf("служба %s ещё не удалена; закройте оснастку «Службы» и повторите установку: %w", serviceName, waitCtx.Err())
		}
	}
}

func requestServiceRemoval(scm windows.Handle, name *uint16) error {
	service, err := windows.OpenService(scm, name, windows.SERVICE_STOP|windows.DELETE)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) || errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(service)

	// Даём службе остановиться штатно; если она не принимает STOP, ниже
	// всё равно завершатся её процессы, а результат проверим через SCM.
	var status windows.SERVICE_STATUS
	_ = windows.ControlService(service, windows.SERVICE_CONTROL_STOP, &status)
	err = windows.DeleteService(service)
	if errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return nil
	}
	return err
}

func remainingProcesses(processNames []string) ([]string, error) {
	if len(processNames) == 0 {
		return nil, nil
	}
	out, err := RunCommand("tasklist.exe", "/NH", "/FO", "CSV")
	if err != nil {
		return nil, err
	}
	records, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		return nil, err
	}

	images := make(map[string]bool, len(records))
	for _, record := range records {
		images[strings.ToLower(record[0])] = true
	}
	var running []string
	for _, processName := range processNames {
		if images[strings.ToLower(processName)] {
			running = append(running, processName)
		}
	}
	return running, nil
}
