package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"goMH/config"
	"goMH/core"
	moduleregistry "goMH/modules/registry"
	"io"
	"strings"
	"testing"
	"time"
)

type fakeQueueModule struct {
	id string
}

func (m *fakeQueueModule) ID() string { return m.id }

func (m *fakeQueueModule) MenuText() string { return "fake module" }

func (m *fakeQueueModule) Run(am core.AssetManager, wu core.WinUtils) error { return nil }

func (m *fakeQueueModule) ConfigureTask(ctx core.TaskContext, services core.ModuleServices) (any, error) {
	return struct{}{}, nil
}

func (m *fakeQueueModule) BuildTask(config any) (core.ModuleTaskPlan, error) {
	return core.ModuleTaskPlan{
		Mode: core.ModuleRunModeQueue,
		Task: core.ModuleTaskSpec{
			Title:     "Fake Queue Task",
			Signature: "fake|queue",
		},
		Result: core.ModuleActionResult{
			Note: "queued",
		},
	}, nil
}

func (m *fakeQueueModule) ExecuteTask(ctx core.TaskContext, services core.ModuleServices, config any) error {
	ctx.SetStatus("Подготовка")
	ctx.Info("Первый лог")
	ctx.SetProgress(35)
	ctx.SetStatus("Завершение")
	ctx.Success("Готово")
	return nil
}

func TestParseRequestBuildsOperationIDFromModuleAction(t *testing.T) {
	t.Parallel()

	req, err := ParseRequest(strings.NewReader(`{
	  "request_id": "req-1",
	  "module": "ServiceUtils",
	  "action": "Collect_Logs"
	}`))
	if err != nil {
		t.Fatalf("ParseRequest returned error: %v", err)
	}
	if req.OperationID != "serviceutils.collect_logs" {
		t.Fatalf("unexpected operation_id: %q", req.OperationID)
	}
	if string(req.Parameters) != "{}" {
		t.Fatalf("expected default parameters object, got %s", string(req.Parameters))
	}
}

func TestRunnerReturnsUnknownOperation(t *testing.T) {
	t.Parallel()

	runner := &Runner{
		Registry:       NewDefaultRegistry(),
		ModuleResolver: moduleregistry.New(),
		Config:         &config.Config{},
	}

	response := runner.Run(context.Background(), Request{
		ContractVersion: ContractVersion,
		RequestID:       "req-unknown",
		OperationID:     "missing.operation",
	})

	if response.ExitCode != ExitUnknownOperation {
		t.Fatalf("expected exit code %d, got %d", ExitUnknownOperation, response.ExitCode)
	}
	if response.Error == nil || response.Error.Code != "unknown_operation" {
		t.Fatalf("unexpected error payload: %+v", response.Error)
	}
}

func TestRunnerReturnsUnsupportedAutomationForInteractiveOperation(t *testing.T) {
	t.Parallel()

	runner := &Runner{
		Registry:       NewDefaultRegistry(),
		ModuleResolver: moduleregistry.New(),
		Config:         &config.Config{},
	}

	response := runner.Run(context.Background(), Request{
		ContractVersion: ContractVersion,
		RequestID:       "req-unsupported",
		OperationID:     "serviceutils.view_log",
	})

	if response.ExitCode != ExitUnsupportedAutomation {
		t.Fatalf("expected exit code %d, got %d", ExitUnsupportedAutomation, response.ExitCode)
	}
	if response.Error == nil || response.Error.Code != "unsupported_operation" {
		t.Fatalf("unexpected error payload: %+v", response.Error)
	}
}

func TestRunnerProducesSuccessResponseViaExistingRuntimeAndQueue(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(OperationDefinition{
		ID:          "test.fake_queue",
		Module:      "test",
		Action:      "fake_queue",
		Description: "fake queue operation",
		ModuleID:    "fake",
		Supported:   true,
		BuildConfig: func(req Request, cfg *config.Config) (any, []string, error) {
			return struct{}{}, nil, nil
		},
	})

	moduleRegistry := moduleregistry.New(func() core.QueueModule {
		return &fakeQueueModule{id: "fake"}
	})

	stderr := &bytes.Buffer{}
	runner := &Runner{
		Registry:       registry,
		ModuleResolver: moduleRegistry,
		Services:       core.ModuleServices{},
		Config:         &config.Config{},
		Stderr:         stderr,
	}

	response := runner.Run(context.Background(), Request{
		ContractVersion: ContractVersion,
		RequestID:       "req-success",
		OperationID:     "test.fake_queue",
		LogOptions: LogOptions{
			IncludeEntries: true,
		},
	})

	if response.Status != "success" {
		t.Fatalf("expected success status, got %q", response.Status)
	}
	if response.ExitCode != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, response.ExitCode)
	}
	if response.Result == nil || response.Result.Task == nil {
		t.Fatalf("expected task payload, got %+v", response.Result)
	}
	if response.Result.Task.State != "success" {
		t.Fatalf("expected successful task state, got %q", response.Result.Task.State)
	}
	if response.Logs == nil || len(response.Logs.Entries) == 0 {
		t.Fatalf("expected collected logs, got %+v", response.Logs)
	}
	if !strings.Contains(stderr.String(), `"type":"automation_status"`) {
		t.Fatalf("expected machine-readable status lines in stderr, got %q", stderr.String())
	}
}

func TestExecuteCLIUsesJSONOnlyOnStdout(t *testing.T) {
	t.Parallel()

	request := `{"request_id":"req-cli","operation_id":"test.echo"}`
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	exitCode := ExecuteCLI([]string{"run", "--stdin"}, strings.NewReader(request), stdout, stderr, CommandDependencies{
		ExecuteRequest: func(ctx context.Context, req Request, opts RequestOptions, stderr io.Writer, deps CommandDependencies) Response {
			return Response{
				ContractVersion: ContractVersion,
				Status:          "success",
				RequestID:       req.RequestID,
				OperationID:     req.OperationID,
				StartedAt:       time.Unix(0, 0).UTC(),
				CompletedAt:     time.Unix(0, 0).UTC(),
				ExitCode:        ExitSuccess,
				Summary:         "ok",
			}
		},
	})

	if exitCode != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("stdout must contain valid JSON response, got error: %v; raw: %q", err, stdout.String())
	}
	if response.RequestID != "req-cli" {
		t.Fatalf("unexpected response request_id: %q", response.RequestID)
	}
}

func TestExecuteCLIReturnsJSONErrorForInvalidRequest(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	exitCode := ExecuteCLI([]string{"run", "--stdin"}, strings.NewReader(`{"operation_id":"x"}`), stdout, stderr, CommandDependencies{})
	if exitCode != ExitInvalidRequest {
		t.Fatalf("expected exit code %d, got %d", ExitInvalidRequest, exitCode)
	}

	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("stdout must contain valid JSON error response: %v", err)
	}
	if response.Error == nil || response.Error.Code != "invalid_request" {
		t.Fatalf("unexpected error payload: %+v", response.Error)
	}
}
