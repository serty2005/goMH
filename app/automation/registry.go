package automation

import (
	"encoding/json"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/modules/distro"
	"goMH/modules/serviceutils"
	"strings"
)

type OperationDefinition struct {
	ID          string
	Module      string
	Action      string
	Description string
	ModuleID    string
	Supported   bool
	Reason      string
	BuildConfig func(req Request, cfg *config.Config) (any, []string, error)
}

type Registry struct {
	definitions map[string]OperationDefinition
}

func NewRegistry(definitions ...OperationDefinition) *Registry {
	registry := &Registry{
		definitions: make(map[string]OperationDefinition, len(definitions)),
	}
	for _, definition := range definitions {
		registry.definitions[strings.ToLower(definition.ID)] = definition
	}
	return registry
}

func NewDefaultRegistry() *Registry {
	return NewRegistry(
		OperationDefinition{
			ID:          "serviceutils.clean_temp",
			Module:      "serviceutils",
			Action:      "clean_temp",
			Description: "Очистка временных файлов по правилам конфигурации",
			ModuleID:    "ServiceUtils",
			Supported:   true,
			BuildConfig: func(req Request, cfg *config.Config) (any, []string, error) {
				return &serviceutils.ServiceUtilsConfig{
					Action: serviceutils.ActionCleanTemp,
				}, nil, nil
			},
		},
		OperationDefinition{
			ID:          "serviceutils.collect_logs",
			Module:      "serviceutils",
			Action:      "collect_logs",
			Description: "Сбор логов в архив за заданный период",
			ModuleID:    "ServiceUtils",
			Supported:   true,
			BuildConfig: buildCollectLogsConfig,
		},
		OperationDefinition{
			ID:          "serviceutils.view_log",
			Module:      "serviceutils",
			Action:      "view_log",
			Description: "Просмотр лога в реальном времени",
			ModuleID:    "ServiceUtils",
			Supported:   false,
			Reason:      "операция требует интерактивного либо долгоживущего сценария и пока не поддерживается в automation mode",
		},
		OperationDefinition{
			ID:          "distro.install_component",
			Module:      "distro",
			Action:      "install_component",
			Description: "Установка компонента iiko/Syrve через существующий модуль distro",
			ModuleID:    "iiko",
			Supported:   true,
			BuildConfig: buildDistroInstallComponentConfig,
		},
	)
}

func (r *Registry) Resolve(req Request) (OperationDefinition, error) {
	if r == nil {
		return OperationDefinition{}, newRunError(ExitInternalError, "registry_missing", "automation registry не инициализирован", nil)
	}

	operationID := req.OperationID
	if operationID == "" && req.Module != "" && req.Action != "" {
		operationID = strings.ToLower(req.Module + "." + req.Action)
	}

	definition, ok := r.definitions[strings.ToLower(operationID)]
	if !ok {
		return OperationDefinition{}, newRunError(ExitUnknownOperation, "unknown_operation", fmt.Sprintf("операция %q не зарегистрирована", operationID), nil)
	}
	if !definition.Supported {
		return OperationDefinition{}, newRunError(ExitUnsupportedAutomation, "unsupported_operation", definition.Reason, nil)
	}
	return definition, nil
}

func (r *Registry) List() []OperationDefinition {
	if r == nil {
		return nil
	}
	result := make([]OperationDefinition, 0, len(r.definitions))
	for _, definition := range r.definitions {
		result = append(result, definition)
	}
	return result
}

type collectLogsParameters struct {
	LogDays int      `json:"log_days"`
	LogDirs []string `json:"log_dirs,omitempty"`
}

func buildCollectLogsConfig(req Request, cfg *config.Config) (any, []string, error) {
	var params collectLogsParameters
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		return nil, nil, newRunError(ExitInvalidRequest, "invalid_parameters", "не удалось разобрать parameters для serviceutils.collect_logs", err)
	}
	if params.LogDays <= 0 {
		return nil, nil, newRunError(ExitInvalidRequest, "invalid_parameters", "parameters.log_days должен быть больше нуля", nil)
	}

	logDirs := params.LogDirs
	if len(logDirs) == 0 {
		logDirs = serviceutils.DiscoverLogDirectories(cfg)
	}
	if len(logDirs) == 0 {
		return nil, nil, newRunError(ExitInvalidRequest, "invalid_parameters", "не удалось определить ни одной директории логов; укажите parameters.log_dirs явно", nil)
	}

	return &serviceutils.ServiceUtilsConfig{
		Action:  serviceutils.ActionCollectLogs,
		LogDays: params.LogDays,
		LogDirs: logDirs,
	}, nil, nil
}

type distroInstallComponentParameters struct {
	Brand                string `json:"brand"`
	ComponentID          string `json:"component_id"`
	Version              string `json:"version,omitempty"`
	Patch                string `json:"patch,omitempty"`
	UninstallOldVersion  bool   `json:"uninstall_old_version,omitempty"`
	OldVersion           string `json:"old_version,omitempty"`
	RunAutoUpdatePlugins bool   `json:"run_auto_update_plugins,omitempty"`
}

func buildDistroInstallComponentConfig(req Request, cfg *config.Config) (any, []string, error) {
	var params distroInstallComponentParameters
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		return nil, nil, newRunError(ExitInvalidRequest, "invalid_parameters", "не удалось разобрать parameters для distro.install_component", err)
	}

	brand := strings.ToLower(strings.TrimSpace(params.Brand))
	if brand == "" {
		return nil, nil, newRunError(ExitInvalidRequest, "invalid_parameters", "parameters.brand обязателен", nil)
	}

	componentID := strings.TrimSpace(params.ComponentID)
	if componentID == "" {
		return nil, nil, newRunError(ExitInvalidRequest, "invalid_parameters", "parameters.component_id обязателен", nil)
	}

	component, err := resolveDistroComponent(cfg, brand, componentID)
	if err != nil {
		return nil, nil, err
	}
	if strings.Contains(component.URLTemplate, "{{VERSION}}") && strings.TrimSpace(params.Version) == "" {
		return nil, nil, newRunError(ExitInvalidRequest, "invalid_parameters", "parameters.version обязателен для выбранного компонента", nil)
	}

	var patch *core.PatchInfo
	if patchName := strings.TrimSpace(params.Patch); patchName != "" {
		if brand != "iiko" {
			return nil, nil, newRunError(ExitInvalidRequest, "invalid_parameters", "parameters.patch поддерживается только для brand=iiko", nil)
		}
		if strings.TrimSpace(params.Version) == "" {
			return nil, nil, newRunError(ExitInvalidRequest, "invalid_parameters", "parameters.version обязателен при выборе patch", nil)
		}

		patches, patchErr := distro.FindPatches(cfg.DistroConfig.Iiko.PatchesBaseURL, params.Version)
		if patchErr != nil {
			return nil, nil, newRunError(ExitExecutionFailed, "patch_lookup_failed", "не удалось получить список патчей", patchErr)
		}

		resolvedPatch, patchErr := resolvePatch(patches, patchName)
		if patchErr != nil {
			return nil, nil, patchErr
		}
		patch = &resolvedPatch
	}

	return &distro.DistroInstallConfig{
		Action:               distro.ActionInstallComponent,
		Brand:                brand,
		Component:            component,
		Version:              strings.TrimSpace(params.Version),
		Patch:                patch,
		UninstallOldVersion:  params.UninstallOldVersion,
		OldVersionString:     strings.TrimSpace(params.OldVersion),
		RunAutoUpdatePlugins: params.RunAutoUpdatePlugins,
	}, nil, nil
}

func resolveDistroComponent(cfg *config.Config, brand string, componentID string) (config.DistroComponent, error) {
	var components []config.DistroComponent
	switch brand {
	case "iiko":
		components = cfg.DistroConfig.Iiko.Components
	case "syrve":
		components = cfg.DistroConfig.Syrve.Components
	default:
		return config.DistroComponent{}, newRunError(ExitInvalidRequest, "invalid_parameters", fmt.Sprintf("неподдерживаемый brand %q", brand), nil)
	}

	for _, component := range components {
		if strings.EqualFold(component.ID, componentID) {
			return component, nil
		}
	}

	return config.DistroComponent{}, newRunError(ExitInvalidRequest, "invalid_parameters", fmt.Sprintf("компонент %q не найден для brand=%s", componentID, brand), nil)
}

func resolvePatch(patches []core.PatchInfo, patchName string) (core.PatchInfo, error) {
	for _, patch := range patches {
		if strings.EqualFold(patch.ShortName, patchName) {
			return patch, nil
		}
	}

	return core.PatchInfo{}, newRunError(ExitInvalidRequest, "invalid_parameters", fmt.Sprintf("патч %q не найден", patchName), nil)
}
