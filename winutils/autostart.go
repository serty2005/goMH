package winutils

import (
	"encoding/json"
	"errors"
	"fmt"
	"goMH/core"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const autostartRegistryPath = `Software\Microsoft\Windows\CurrentVersion`

type autostartCommand struct {
	Name string
	Args []string
}

type scheduledTaskJSON struct {
	TaskName string                     `json:"TaskName"`
	TaskPath string                     `json:"TaskPath"`
	State    string                     `json:"State"`
	Actions  []scheduledTaskActionJSON  `json:"Actions"`
	Triggers []scheduledTaskTriggerJSON `json:"Triggers"`
}

type scheduledTaskActionJSON struct {
	Execute          string `json:"Execute"`
	Arguments        string `json:"Arguments"`
	WorkingDirectory string `json:"WorkingDirectory"`
}

type scheduledTaskTriggerJSON struct {
	TriggerType string `json:"TriggerType"`
}

type shortcutJSON struct {
	TargetPath       string `json:"TargetPath"`
	Arguments        string `json:"Arguments"`
	WorkingDirectory string `json:"WorkingDirectory"`
}

func ListAutostartEntries() ([]core.AutostartEntry, error) {
	return ListAutostartEntriesWithProgress(nil)
}

func ListAutostartEntriesWithProgress(progress func(core.AutostartScanProgress)) ([]core.AutostartEntry, error) {
	entries, err := listRegistryAutostartEntries()
	if err != nil {
		return nil, err
	}
	reportAutostartProgress(progress, "Реестр Run/RunOnce", len(entries))
	startupEntries, err := listStartupFolderEntries()
	if err != nil {
		return nil, err
	}
	entries = append(entries, startupEntries...)
	reportAutostartProgress(progress, "Папки автозапуска", len(entries))
	taskEntries, err := listScheduledAutostartEntries()
	if err != nil {
		return entries, err
	}
	entries = append(entries, taskEntries...)
	reportAutostartProgress(progress, "Планировщик задач", len(entries))
	return entries, nil
}

func ApplyAutostartChanges(changes []core.AutostartChange) error {
	var errs []error
	for _, command := range buildAutostartApplyCommands(changes) {
		if _, err := RunCommand(command.Name, command.Args...); err != nil {
			errs = append(errs, err)
		}
	}
	for _, change := range changes {
		switch change.Entry.Source {
		case core.AutostartSourceRegistryRun, core.AutostartSourceRegistryRunOnce:
			if change.Enabled {
				if err := SetRegistryAutostartValue(
					change.Entry.Scope,
					change.Entry.RegistryKey,
					change.Entry.RegistryValue,
					change.Entry.Command,
				); err != nil {
					errs = append(errs, err)
				}
			} else {
				if err := DeleteRegistryAutostartValue(change.Entry.Scope, change.Entry.RegistryKey, change.Entry.RegistryValue); err != nil {
					errs = append(errs, err)
				}
			}
		case core.AutostartSourceStartupFolder:
			if !change.Enabled {
				if err := os.Remove(change.Entry.FilePath); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}

func AddRegistryAutostartEntry(req core.AutostartCreateRequest) error {
	targetPath := strings.TrimSpace(req.Path)
	arguments := strings.TrimSpace(req.Arguments)
	workingDir := strings.TrimSpace(req.WorkingDirectory)
	if strings.EqualFold(filepath.Ext(targetPath), ".lnk") {
		shortcut, err := ResolveShortcut(targetPath)
		if err != nil {
			return err
		}
		targetPath = shortcut.TargetPath
		arguments = strings.TrimSpace(strings.Join(nonEmpty([]string{shortcut.Arguments, arguments}), " "))
		if workingDir == "" {
			workingDir = shortcut.WorkingDirectory
		}
	}
	if targetPath == "" {
		return errors.New("path is required")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath))
	}
	key := req.RegistryKey
	if key == "" {
		key = core.AutostartRegistryKeyRun
	}
	scope := req.Scope
	if scope == "" {
		scope = core.AutostartScopeUser
	}
	if err := SetRegistryAutostartValue(scope, key, name, buildAutostartCommand(targetPath, arguments)); err != nil {
		if scope == core.AutostartScopeMachine {
			return fmt.Errorf("запись в HKLM\\...\\Run требует прав администратора; запустите программу от имени администратора: %w", err)
		}
		return err
	}
	return nil
}

func SetRegistryAutostartValue(scope core.AutostartScope, key core.AutostartRegistryKey, valueName, command string) error {
	root, err := autostartRoot(scope)
	if err != nil {
		return err
	}
	path := registryAutostartPath(key)
	k, _, err := registry.CreateKey(root, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(valueName, command)
}

func DeleteRegistryAutostartValue(scope core.AutostartScope, key core.AutostartRegistryKey, valueName string) error {
	root, err := autostartRoot(scope)
	if err != nil {
		return err
	}
	k, err := registry.OpenKey(root, registryAutostartPath(key), registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func RegistryAutostartValueExists(scope core.AutostartScope, key core.AutostartRegistryKey, valueName string) bool {
	root, err := autostartRoot(scope)
	if err != nil {
		return false
	}
	k, err := registry.OpenKey(root, registryAutostartPath(key), registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(valueName)
	return err == nil
}

func ResolveShortcut(path string) (core.ShortcutInfo, error) {
	script := fmt.Sprintf(`
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut('%s')
[PSCustomObject]@{
  TargetPath = $shortcut.TargetPath
  Arguments = $shortcut.Arguments
  WorkingDirectory = $shortcut.WorkingDirectory
} | ConvertTo-Json -Compress
`, escapePowerShellSingleQuoted(path))
	out, err := RunCommand("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		return core.ShortcutInfo{}, err
	}
	var data shortcutJSON
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return core.ShortcutInfo{}, err
	}
	return core.ShortcutInfo{
		TargetPath:       data.TargetPath,
		Arguments:        data.Arguments,
		WorkingDirectory: data.WorkingDirectory,
	}, nil
}

func listRegistryAutostartEntries() ([]core.AutostartEntry, error) {
	var entries []core.AutostartEntry
	for _, scope := range []core.AutostartScope{core.AutostartScopeUser, core.AutostartScopeMachine} {
		root, err := autostartRoot(scope)
		if err != nil {
			return nil, err
		}
		for _, keyName := range []core.AutostartRegistryKey{core.AutostartRegistryKeyRun, core.AutostartRegistryKeyRunOnce} {
			path := registryAutostartPath(keyName)
			k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
			if err != nil {
				if errors.Is(err, registry.ErrNotExist) {
					continue
				}
				return nil, err
			}
			names, err := k.ReadValueNames(0)
			if err != nil {
				k.Close()
				return nil, err
			}
			for _, name := range names {
				command, _, err := k.GetStringValue(name)
				if err != nil {
					continue
				}
				source := core.AutostartSourceRegistryRun
				if keyName == core.AutostartRegistryKeyRunOnce {
					source = core.AutostartSourceRegistryRunOnce
				}
				targetPath, arguments := parseRegistryCommand(command)
				entries = append(entries, core.AutostartEntry{
					ID:            strings.Join([]string{"registry", string(scope), string(keyName), name}, "|"),
					Name:          name,
					Source:        source,
					Scope:         scope,
					Enabled:       true,
					Command:       command,
					TargetPath:    targetPath,
					Arguments:     arguments,
					RegistryKey:   keyName,
					RegistryValue: name,
					CanToggle:     true,
				})
			}
			k.Close()
		}
	}
	return entries, nil
}

func listStartupFolderEntries() ([]core.AutostartEntry, error) {
	userDir, commonDir, err := GetStartupFolders()
	if err != nil {
		return nil, err
	}
	type folder struct {
		path  string
		scope core.AutostartScope
	}
	var entries []core.AutostartEntry
	for _, folder := range []folder{
		{path: userDir, scope: core.AutostartScopeUser},
		{path: commonDir, scope: core.AutostartScopeMachine},
	} {
		files, err := os.ReadDir(folder.path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			filePath := filepath.Join(folder.path, file.Name())
			command := filePath
			target := filePath
			args := ""
			workingDir := filepath.Dir(filePath)
			if strings.EqualFold(filepath.Ext(filePath), ".lnk") {
				if shortcut, err := ResolveShortcut(filePath); err == nil {
					target = shortcut.TargetPath
					args = shortcut.Arguments
					workingDir = shortcut.WorkingDirectory
					command = buildAutostartCommand(target, args)
				}
			}
			entries = append(entries, core.AutostartEntry{
				ID:               "startup|" + filePath,
				Name:             file.Name(),
				Source:           core.AutostartSourceStartupFolder,
				Scope:            folder.scope,
				Enabled:          true,
				Command:          command,
				TargetPath:       target,
				Arguments:        args,
				WorkingDirectory: workingDir,
				FilePath:         filePath,
				CanToggle:        true,
			})
		}
	}
	return entries, nil
}

func listScheduledAutostartEntries() ([]core.AutostartEntry, error) {
	script := `
$tasks = Get-ScheduledTask | Where-Object {
  $_.Triggers | Where-Object {
    $_.CimClass.CimClassName -in @('MSFT_TaskLogonTrigger', 'MSFT_TaskBootTrigger')
  }
} | ForEach-Object {
  [PSCustomObject]@{
    TaskName = $_.TaskName
    TaskPath = $_.TaskPath
    State = [string]$_.State
    Actions = @($_.Actions | ForEach-Object {
      [PSCustomObject]@{
        Execute = $_.Execute
        Arguments = $_.Arguments
        WorkingDirectory = $_.WorkingDirectory
      }
    })
    Triggers = @($_.Triggers | ForEach-Object {
      [PSCustomObject]@{
        TriggerType = $_.CimClass.CimClassName -replace '^MSFT_Task','' -replace 'Trigger$','Trigger'
      }
    })
  }
}
@($tasks) | ConvertTo-Json -Depth 6 -Compress
`
	out, err := RunCommand("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		return nil, err
	}
	return parseScheduledAutostartTasks(out)
}

func parseScheduledAutostartTasks(input string) ([]core.AutostartEntry, error) {
	input = strings.TrimSpace(input)
	if input == "" || input == "null" {
		return nil, nil
	}
	var tasks []scheduledTaskJSON
	if strings.HasPrefix(input, "[") {
		if err := json.Unmarshal([]byte(input), &tasks); err != nil {
			return nil, err
		}
	} else {
		var task scheduledTaskJSON
		if err := json.Unmarshal([]byte(input), &task); err != nil {
			return nil, err
		}
		tasks = []scheduledTaskJSON{task}
	}
	entries := make([]core.AutostartEntry, 0, len(tasks))
	for _, task := range tasks {
		if !scheduledTaskHasAutostartTrigger(task) {
			continue
		}
		taskName := normalizeTaskName(task.TaskPath, task.TaskName)
		if isMicrosoftWindowsScheduledTask(taskName) {
			continue
		}
		command, target, args, workingDir := scheduledTaskCommand(task)
		entries = append(entries, core.AutostartEntry{
			ID:               "task|" + taskName,
			Name:             taskName,
			Source:           core.AutostartSourceScheduledTask,
			Scope:            core.AutostartScopeMachine,
			Enabled:          !strings.EqualFold(task.State, "Disabled"),
			Command:          command,
			TargetPath:       target,
			Arguments:        args,
			WorkingDirectory: workingDir,
			TaskName:         taskName,
			CanToggle:        true,
		})
	}
	return entries, nil
}

func buildAutostartApplyCommands(changes []core.AutostartChange) []autostartCommand {
	var commands []autostartCommand
	for _, change := range changes {
		if change.Entry.Source != core.AutostartSourceScheduledTask {
			continue
		}
		action := "/Disable"
		if change.Enabled {
			action = "/Enable"
		}
		taskName := change.Entry.TaskName
		if taskName == "" {
			taskName = change.Entry.Name
		}
		commands = append(commands, autostartCommand{
			Name: "schtasks",
			Args: []string{"/Change", "/TN", taskName, action},
		})
	}
	return commands
}

func parseRegistryCommand(cmd string) (targetPath, arguments string) {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		end := strings.Index(cmd[1:], `"`)
		if end >= 0 {
			targetPath = cmd[1 : end+1]
			arguments = strings.TrimSpace(cmd[end+2:])
			return
		}
	}
	idx := strings.IndexByte(cmd, ' ')
	if idx < 0 {
		targetPath = cmd
		return
	}
	targetPath = cmd[:idx]
	arguments = strings.TrimSpace(cmd[idx+1:])
	return
}

func buildAutostartCommand(executablePath, arguments string) string {
	path := strings.Trim(strings.TrimSpace(executablePath), `"`)
	if path == "" {
		return strings.TrimSpace(arguments)
	}
	command := path
	if strings.ContainsAny(command, " \t") {
		command = `"` + command + `"`
	}
	if arguments = strings.TrimSpace(arguments); arguments != "" {
		command += " " + arguments
	}
	return command
}

func autostartRoot(scope core.AutostartScope) (registry.Key, error) {
	switch scope {
	case "", core.AutostartScopeUser:
		return registry.CURRENT_USER, nil
	case core.AutostartScopeMachine:
		return registry.LOCAL_MACHINE, nil
	default:
		return 0, fmt.Errorf("unsupported autostart scope %q", scope)
	}
}

func registryAutostartPath(key core.AutostartRegistryKey) string {
	if key == "" {
		key = core.AutostartRegistryKeyRun
	}
	return autostartRegistryPath + `\` + string(key)
}

func normalizeTaskName(taskPath, taskName string) string {
	path := strings.TrimSpace(taskPath)
	name := strings.TrimSpace(taskName)
	if path == "" || path == `\` {
		return `\` + strings.TrimPrefix(name, `\`)
	}
	path = `\` + strings.Trim(path, `\`) + `\`
	return path + strings.Trim(name, `\`)
}

func scheduledTaskHasAutostartTrigger(task scheduledTaskJSON) bool {
	for _, trigger := range task.Triggers {
		switch strings.ToLower(trigger.TriggerType) {
		case "logontrigger", "boottrigger":
			return true
		}
	}
	return false
}

func isMicrosoftWindowsScheduledTask(taskName string) bool {
	name := strings.ToLower(strings.TrimSpace(taskName))
	name = strings.ReplaceAll(name, `/`, `\`)
	return strings.HasPrefix(name, `\microsoft\windows\`)
}

func scheduledTaskCommand(task scheduledTaskJSON) (command, target, args, workingDir string) {
	if len(task.Actions) == 0 {
		return "", "", "", ""
	}
	action := task.Actions[0]
	return buildAutostartCommand(action.Execute, action.Arguments), action.Execute, action.Arguments, action.WorkingDirectory
}

func escapePowerShellSingleQuoted(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func reportAutostartProgress(progress func(core.AutostartScanProgress), area string, found int) {
	if progress == nil {
		return
	}
	progress(core.AutostartScanProgress{
		Area:  area,
		Found: found,
	})
}
