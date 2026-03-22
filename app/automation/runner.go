package automation

import (
	"context"
	"goMH/app/modruntime"
	"goMH/config"
	"goMH/core"
	moduleregistry "goMH/modules/registry"
	"goMH/taskqueue"
	"time"
)

type Runner struct {
	Registry       *Registry
	ModuleResolver *moduleregistry.Registry
	Services       core.ModuleServices
	Config         *config.Config
	Stderr         ioWriter
	now            func() time.Time
}

type ioWriter interface {
	Write(p []byte) (int, error)
}

func NewRunner(cfg *config.Config, resolver *moduleregistry.Registry, services core.ModuleServices, stderr ioWriter) *Runner {
	return &Runner{
		Registry:       NewDefaultRegistry(),
		ModuleResolver: resolver,
		Services:       services,
		Config:         cfg,
		Stderr:         stderr,
		now:            time.Now,
	}
}

func (r *Runner) Run(ctx context.Context, req Request) Response {
	startedAt := r.timeNow().UTC()
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

	definition, err := r.Registry.Resolve(req)
	if err != nil {
		return r.fail(response, startedAt, err)
	}
	response.OperationID = definition.ID
	response.Module = definition.Module
	response.Action = definition.Action

	module, ok := r.ModuleResolver.Get(definition.ModuleID)
	if !ok {
		return r.fail(response, startedAt, newRunError(ExitInternalError, "module_not_found", "внутренний модуль операции не зарегистрирован", nil))
	}

	configValue, warnings, err := definition.BuildConfig(req, r.Config)
	if err != nil {
		return r.fail(response, startedAt, err, warnings...)
	}

	plan, err := module.BuildTask(configValue)
	if err != nil {
		return r.fail(response, startedAt, newRunError(ExitExecutionFailed, "build_task_failed", "не удалось построить план операции", err), warnings...)
	}

	response.Result = &ResultPayload{
		Mode:   planMode(plan),
		DryRun: req.DryRun,
		Plan:   planPayload(plan, configValue),
	}
	response.Warnings = append(response.Warnings, warnings...)

	if req.DryRun {
		response.Status = "success"
		response.ExitCode = ExitSuccess
		response.Summary = "Dry-run успешно завершён, план операции сформирован."
		return r.complete(response, startedAt, nil)
	}

	requestCtx, cancel := r.withTimeout(ctx, req.TimeoutSeconds)
	defer cancel()

	collector := newExecutionCollector(req.RequestID, req.CorrelationID, definition.ID, r.Stderr)
	service := modruntime.Service{
		Resolver: r.ModuleResolver,
		Services: r.Services,
	}

	result, execErr := r.execute(requestCtx, service, collector, definition, plan, configValue)
	if result != nil {
		response.Result.ModuleResult = result
	}

	task := collector.Task()
	if task != nil {
		response.Result.Task = task
	}

	includeEntries := req.LogOptions.IncludeEntries || req.LogOptions.TaskLogPath == ""
	logs, logWarnings := collector.Logs(includeEntries, req.LogOptions.TaskLogPath)
	if logs != nil {
		response.Logs = logs
	}
	response.Warnings = append(response.Warnings, logWarnings...)

	if execErr == nil {
		execErr = taskError(task, requestCtx)
	}
	if execErr != nil {
		return r.fail(response, startedAt, execErr, response.Warnings...)
	}

	response.Status = "success"
	response.ExitCode = ExitSuccess
	response.Summary = "Операция успешно выполнена."
	return r.complete(response, startedAt, nil)
}

func (r *Runner) execute(ctx context.Context, service modruntime.Service, collector *executionCollector, definition OperationDefinition, plan core.ModuleTaskPlan, configValue any) (*ModuleActionPayload, error) {
	switch plan.Mode {
	case core.ModuleRunModeImmediate:
		collector.StartImmediate(definition.ModuleID, plan.Task.Title)
		service.ImmediateContext = collector.ImmediateContext(ctx)
		result, err := service.EnqueuePrepared(definition.ModuleID, configValue)
		collector.FinishImmediate(err)
		return newModuleActionPayload(result), err
	case core.ModuleRunModeQueue:
		queue := taskqueue.New()
		detach := collector.AttachQueue(queue)
		defer detach()

		service.Submitter = modruntime.NewTaskQueueSubmitter(queue)
		result, err := service.EnqueuePrepared(definition.ModuleID, configValue)
		if err != nil {
			return nil, err
		}

		queue.RunPending(func(snapshot taskqueue.TaskSnapshot, _ context.Context) core.TaskContext {
			return taskqueue.NewTaskContext(queue, snapshot.ID, ctx)
		})
		return newModuleActionPayload(result), nil
	default:
		return nil, newRunError(ExitInternalError, "unsupported_plan_mode", "обнаружен неподдерживаемый режим выполнения модуля", nil)
	}
}

func (r *Runner) fail(response Response, startedAt time.Time, err error, warnings ...string) Response {
	exitCode, code, message := classifyError(err)
	response.Status = "error"
	response.ExitCode = exitCode
	response.Summary = message
	response.Warnings = append([]string(nil), warnings...)
	if code != "" {
		response.Error = &ErrorPayload{
			Code:    code,
			Message: message,
		}
	}
	return r.complete(response, startedAt, err)
}

func (r *Runner) complete(response Response, startedAt time.Time, err error) Response {
	completedAt := r.timeNow().UTC()
	response.CompletedAt = completedAt
	response.DurationMS = completedAt.Sub(startedAt).Milliseconds()
	if response.Error == nil && err != nil {
		response.Error = &ErrorPayload{
			Code:    "internal_error",
			Message: err.Error(),
		}
	}
	return response
}

func (r *Runner) withTimeout(ctx context.Context, timeoutSeconds int) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeoutSeconds <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
}

func (r *Runner) timeNow() time.Time {
	if r != nil && r.now != nil {
		return r.now()
	}
	return time.Now()
}

func planMode(plan core.ModuleTaskPlan) string {
	switch plan.Mode {
	case core.ModuleRunModeImmediate:
		return "immediate"
	case core.ModuleRunModeQueue:
		return "queue"
	default:
		return "unknown"
	}
}

func planPayload(plan core.ModuleTaskPlan, configValue any) *PlanPayload {
	payload := &PlanPayload{
		Mode:             planMode(plan),
		Title:            plan.Task.Title,
		Signature:        plan.Task.Signature,
		Exclusive:        plan.Task.Exclusive,
		SkipConfirmation: plan.SkipConfirmation,
		Result: ModuleActionPayload{
			Note:       plan.Result.Note,
			TaskID:     plan.Result.TaskID,
			SelectTask: plan.Result.SelectTask,
		},
	}

	if provider, ok := configValue.(core.TaskConfirmationProvider); ok {
		payload.Confirmation = provider.TaskConfirmation().Details
	}
	return payload
}

func newModuleActionPayload(result core.ModuleActionResult) *ModuleActionPayload {
	return &ModuleActionPayload{
		Note:       result.Note,
		TaskID:     result.TaskID,
		SelectTask: result.SelectTask,
	}
}
