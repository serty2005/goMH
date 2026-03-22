package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"goMH/app/platform"
	"goMH/assetmgr"
	"goMH/config"
	"goMH/core"
	"goMH/logging"
	moduleregistry "goMH/modules/registry"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type CommandDependencies struct {
	NewWinUtils    func() core.WinUtils
	ExecuteRequest func(ctx context.Context, req Request, opts RequestOptions, stderr io.Writer, deps CommandDependencies) Response
}

type RequestOptions struct {
	ConfigPath         string
	ConfigPathExplicit bool
}

func ExecuteCLI(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, deps CommandDependencies) int {
	if len(args) == 0 {
		return writeErrorResponse(stdout, stderr, newRunError(ExitInvalidRequest, "invalid_command", "нужно указать automation subcommand", nil))
	}

	switch args[0] {
	case "run":
		return executeRunCLI(args[1:], stdin, stdout, stderr, deps)
	case "list-operations":
		return executeListOperations(stdout)
	default:
		return writeErrorResponse(stdout, stderr, newRunError(ExitInvalidRequest, "invalid_command", fmt.Sprintf("неизвестный automation subcommand %q", args[0]), nil))
	}
}

func executeRunCLI(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, deps CommandDependencies) int {
	fs := flag.NewFlagSet("automation run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	requestPath := fs.String("request", "", "Путь к JSON request")
	stdinMode := fs.Bool("stdin", false, "Чтение request из stdin")
	configPath := fs.String("config", "", "Путь к config.json")

	if err := fs.Parse(args); err != nil {
		return writeErrorResponse(stdout, stderr, newRunError(ExitInvalidRequest, "invalid_command", "не удалось разобрать аргументы automation run", err))
	}

	if (*requestPath == "" && !*stdinMode) || (*requestPath != "" && *stdinMode) {
		return writeErrorResponse(stdout, stderr, newRunError(ExitInvalidRequest, "invalid_command", "нужно указать ровно один источник request: --request или --stdin", nil))
	}

	data, err := readRequestData(*requestPath, *stdinMode, stdin)
	if err != nil {
		return writeErrorResponse(stdout, stderr, newRunError(ExitInvalidRequest, "request_read_failed", "не удалось прочитать automation request", err))
	}

	req, err := ParseRequest(bytes.NewReader(data))
	if err != nil {
		return writeErrorResponse(stdout, stderr, newRunError(ExitInvalidRequest, "invalid_request", err.Error(), err))
	}

	executor := deps.ExecuteRequest
	if executor == nil {
		executor = executeRequestDefault
	}
	response := executor(context.Background(), req, RequestOptions{
		ConfigPath:         *configPath,
		ConfigPathExplicit: *configPath != "",
	}, stderr, deps)

	if err := WriteResponse(stdout, response); err != nil {
		fmt.Fprintf(stderr, "не удалось записать automation response: %v\n", err)
		return ExitInternalError
	}
	return response.ExitCode
}

func executeListOperations(stdout io.Writer) int {
	type item struct {
		OperationID string `json:"operation_id"`
		Module      string `json:"module"`
		Action      string `json:"action"`
		Supported   bool   `json:"supported"`
		Description string `json:"description,omitempty"`
		Reason      string `json:"reason,omitempty"`
	}

	registry := NewDefaultRegistry()
	items := make([]item, 0, len(registry.List()))
	for _, definition := range registry.List() {
		items = append(items, item{
			OperationID: definition.ID,
			Module:      definition.Module,
			Action:      definition.Action,
			Supported:   definition.Supported,
			Description: definition.Description,
			Reason:      definition.Reason,
		})
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]any{
		"contract_version": ContractVersion,
		"operations":       items,
	}); err != nil {
		return ExitInternalError
	}
	return ExitSuccess
}

func executeRequestDefault(ctx context.Context, req Request, opts RequestOptions, stderr io.Writer, deps CommandDependencies) Response {
	startedAt := time.Now().UTC()
	response := Response{
		ContractVersion: ContractVersion,
		Status:          "error",
		RequestID:       req.RequestID,
		CorrelationID:   req.CorrelationID,
		OperationID:     req.OperationID,
		Module:          req.Module,
		Action:          req.Action,
		StartedAt:       startedAt,
		CompletedAt:     startedAt,
		ExitCode:        ExitInternalError,
	}

	restoreWorkingDir, err := applyWorkingDir(req.WorkingDir)
	if err != nil {
		return finishCommandResponse(response, startedAt, newRunError(ExitInvalidRequest, "invalid_working_dir", "не удалось применить working_dir", err))
	}
	defer restoreWorkingDir()

	configPath, err := resolveConfigPath(opts.ConfigPath, opts.ConfigPathExplicit)
	if err != nil {
		return finishCommandResponse(response, startedAt, newRunError(ExitExecutionFailed, "config_resolve_failed", "не удалось определить путь к конфигурации", err))
	}

	cfg, err := config.LoadConfigQuiet(configPath)
	if err != nil {
		return finishCommandResponse(response, startedAt, newRunError(ExitExecutionFailed, "config_load_failed", "не удалось загрузить конфигурацию", err))
	}
	if req.RootOverride != "" {
		cfg.RootPath = req.RootOverride
	}

	logSetup, err := logging.Init(cfg.Logging)
	if err != nil {
		return finishCommandResponse(response, startedAt, newRunError(ExitExecutionFailed, "logging_init_failed", "не удалось инициализировать логирование", err))
	}
	defer logSetup.Close()

	addRequestLoggerAttrs(req.RequestID, req.CorrelationID, req.OperationID)

	winUtilsFactory := deps.NewWinUtils
	if winUtilsFactory == nil {
		winUtilsFactory = func() core.WinUtils {
			return platform.NewRealWinUtils()
		}
	}

	winUtils := winUtilsFactory()
	assetManager, err := assetmgr.New(cfg)
	if err != nil {
		return finishCommandResponse(response, startedAt, newRunError(ExitExecutionFailed, "asset_manager_init_failed", "не удалось инициализировать asset manager", err))
	}

	runner := NewRunner(cfg, moduleregistry.NewDefault(), core.ModuleServices{
		AssetManager: assetManager,
		WinUtils:     winUtils,
	}, stderr)
	return runner.Run(ctx, req)
}

func readRequestData(requestPath string, stdinMode bool, stdin io.Reader) ([]byte, error) {
	if stdinMode {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(requestPath)
}

func writeErrorResponse(stdout io.Writer, stderr io.Writer, err error) int {
	startedAt := time.Now().UTC()
	response := Response{
		ContractVersion: ContractVersion,
		Status:          "error",
		StartedAt:       startedAt,
		CompletedAt:     startedAt,
		DurationMS:      0,
	}
	exitCode, code, message := classifyError(err)
	response.ExitCode = exitCode
	response.Summary = message
	response.Error = &ErrorPayload{
		Code:    code,
		Message: message,
	}
	if writeErr := WriteResponse(stdout, response); writeErr != nil {
		fmt.Fprintf(stderr, "не удалось записать automation response: %v\n", writeErr)
		return ExitInternalError
	}
	return exitCode
}

func resolveConfigPath(explicitPath string, explicit bool) (string, error) {
	if explicit {
		return explicitPath, nil
	}

	const defaultConfigName = "config.json"
	const remoteConfigURL = "http://f.serty.top/distr/installer/config.json"

	if _, err := os.Stat(defaultConfigName); err == nil {
		return defaultConfigName, nil
	}

	resp, err := http.Get(remoteConfigURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("сервер вернул %s", resp.Status)
	}

	tempDir := filepath.Join(".", "temp")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return "", err
	}

	tempFile, err := os.CreateTemp(tempDir, "automation-config-*.json")
	if err != nil {
		return "", err
	}
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		return "", err
	}
	return tempFile.Name(), nil
}

func applyWorkingDir(dir string) (func(), error) {
	if dir == "" {
		return func() {}, nil
	}
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(dir); err != nil {
		return nil, err
	}
	return func() {
		_ = os.Chdir(currentDir)
	}, nil
}

func finishCommandResponse(response Response, startedAt time.Time, err error) Response {
	exitCode, code, message := classifyError(err)
	response.CompletedAt = time.Now().UTC()
	response.DurationMS = response.CompletedAt.Sub(startedAt).Milliseconds()
	response.ExitCode = exitCode
	response.Summary = message
	response.Error = &ErrorPayload{
		Code:    code,
		Message: message,
	}
	return response
}
