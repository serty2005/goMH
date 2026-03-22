package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const ContractVersion = "gomh.automation/v1"

const (
	ExitSuccess               = 0
	ExitInvalidRequest        = 2
	ExitUnknownOperation      = 3
	ExitUnsupportedAutomation = 4
	ExitExecutionFailed       = 5
	ExitTimeout               = 6
	ExitInternalError         = 7
)

type Request struct {
	ContractVersion string          `json:"contract_version,omitempty"`
	RequestID       string          `json:"request_id"`
	CorrelationID   string          `json:"correlation_id,omitempty"`
	OperationID     string          `json:"operation_id,omitempty"`
	Module          string          `json:"module,omitempty"`
	Action          string          `json:"action,omitempty"`
	Parameters      json.RawMessage `json:"parameters,omitempty"`
	TimeoutSeconds  int             `json:"timeout_seconds,omitempty"`
	DryRun          bool            `json:"dry_run,omitempty"`
	WorkingDir      string          `json:"working_dir,omitempty"`
	RootOverride    string          `json:"root_override,omitempty"`
	LogOptions      LogOptions      `json:"log_options,omitempty"`
	Metadata        map[string]any  `json:"metadata,omitempty"`
}

type LogOptions struct {
	IncludeEntries bool   `json:"include_entries,omitempty"`
	TaskLogPath    string `json:"task_log_path,omitempty"`
}

type Response struct {
	ContractVersion string         `json:"contract_version"`
	Status          string         `json:"status"`
	RequestID       string         `json:"request_id,omitempty"`
	CorrelationID   string         `json:"correlation_id,omitempty"`
	OperationID     string         `json:"operation_id,omitempty"`
	Module          string         `json:"module,omitempty"`
	Action          string         `json:"action,omitempty"`
	StartedAt       time.Time      `json:"started_at"`
	CompletedAt     time.Time      `json:"completed_at"`
	DurationMS      int64          `json:"duration_ms"`
	ExitCode        int            `json:"exit_code"`
	Summary         string         `json:"summary,omitempty"`
	Result          *ResultPayload `json:"result,omitempty"`
	Warnings        []string       `json:"warnings,omitempty"`
	Error           *ErrorPayload  `json:"error,omitempty"`
	Logs            *LogsPayload   `json:"logs,omitempty"`
}

type ResultPayload struct {
	Mode         string               `json:"mode,omitempty"`
	DryRun       bool                 `json:"dry_run,omitempty"`
	Plan         *PlanPayload         `json:"plan,omitempty"`
	ModuleResult *ModuleActionPayload `json:"module_result,omitempty"`
	Task         *TaskPayload         `json:"task,omitempty"`
	Metadata     map[string]any       `json:"metadata,omitempty"`
}

type PlanPayload struct {
	Mode             string              `json:"mode"`
	Title            string              `json:"title,omitempty"`
	Signature        string              `json:"signature,omitempty"`
	Exclusive        bool                `json:"exclusive,omitempty"`
	SkipConfirmation bool                `json:"skip_confirmation,omitempty"`
	Confirmation     []string            `json:"confirmation,omitempty"`
	Result           ModuleActionPayload `json:"result"`
}

type ModuleActionPayload struct {
	Note       string `json:"note,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	SelectTask bool   `json:"select_task,omitempty"`
}

type TaskPayload struct {
	ID         string    `json:"id,omitempty"`
	ModuleID   string    `json:"module_id,omitempty"`
	Title      string    `json:"title,omitempty"`
	Signature  string    `json:"signature,omitempty"`
	State      string    `json:"state,omitempty"`
	Progress   int       `json:"progress,omitempty"`
	Status     string    `json:"status,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	Cancelable bool      `json:"cancelable,omitempty"`
	EnqueuedAt time.Time `json:"enqueued_at,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

type LogsPayload struct {
	Path    string     `json:"path,omitempty"`
	Entries []LogEntry `json:"entries,omitempty"`
}

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level,omitempty"`
	Message   string    `json:"message"`
	Source    string    `json:"source,omitempty"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ParseRequest(r io.Reader) (Request, error) {
	var req Request
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return Request{}, fmt.Errorf("не удалось разобрать automation request: %w", err)
	}

	if strings.TrimSpace(req.ContractVersion) == "" {
		req.ContractVersion = ContractVersion
	}
	if req.ContractVersion != ContractVersion {
		return Request{}, fmt.Errorf("неподдерживаемая версия контракта %q", req.ContractVersion)
	}
	if strings.TrimSpace(req.RequestID) == "" {
		return Request{}, errors.New("request_id обязателен")
	}
	if strings.TrimSpace(req.OperationID) == "" {
		if strings.TrimSpace(req.Module) == "" || strings.TrimSpace(req.Action) == "" {
			return Request{}, errors.New("нужно указать operation_id либо пару module/action")
		}
		req.OperationID = strings.ToLower(strings.TrimSpace(req.Module) + "." + strings.TrimSpace(req.Action))
	}

	req.OperationID = strings.ToLower(strings.TrimSpace(req.OperationID))
	req.Module = strings.ToLower(strings.TrimSpace(req.Module))
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	if req.TimeoutSeconds < 0 {
		return Request{}, errors.New("timeout_seconds не может быть отрицательным")
	}
	if len(req.Parameters) == 0 {
		req.Parameters = json.RawMessage("{}")
	}

	return req, nil
}

func WriteResponse(w io.Writer, response Response) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(response)
}
