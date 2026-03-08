package modruntime

import (
	"context"
	"errors"
	"fmt"
	"goMH/core"
	"reflect"
)

type ModuleResolver interface {
	Get(id string) (core.QueueModule, bool)
}

type QueueTaskSpec struct {
	ModuleID  string
	Title     string
	Signature string
	Exclusive bool
	Run       func(taskCtx core.TaskContext) error
}

type Submission struct {
	TaskID string
}

type Submitter interface {
	Submit(spec QueueTaskSpec) (Submission, error)
}

type Service struct {
	Resolver         ModuleResolver
	Submitter        Submitter
	Services         core.ModuleServices
	ConfigureContext core.TaskContext
	ImmediateContext core.TaskContext
}

func (s Service) EnqueueModule(moduleID string) (core.ModuleActionResult, error) {
	module, err := s.resolve(moduleID)
	if err != nil {
		return core.ModuleActionResult{}, err
	}

	cfg, err := module.ConfigureTask(s.configureContext(), s.Services)
	if err != nil || isNilConfig(cfg) {
		return core.ModuleActionResult{}, err
	}

	return s.EnqueuePrepared(moduleID, cfg)
}

func (s Service) EnqueuePrepared(moduleID string, config any) (core.ModuleActionResult, error) {
	module, err := s.resolve(moduleID)
	if err != nil {
		return core.ModuleActionResult{}, err
	}

	plan, err := module.BuildTask(config)
	if err != nil {
		return core.ModuleActionResult{}, err
	}

	switch plan.Mode {
	case core.ModuleRunModeImmediate:
		return s.executeImmediate(module, config, plan)
	case core.ModuleRunModeQueue:
		return s.enqueue(module, config, plan)
	default:
		return core.ModuleActionResult{}, fmt.Errorf("неподдерживаемый режим выполнения модуля %s", module.ID())
	}
}

func (s Service) resolve(moduleID string) (core.QueueModule, error) {
	if s.Resolver == nil {
		return nil, errors.New("реестр модулей не задан")
	}

	module, ok := s.Resolver.Get(moduleID)
	if !ok {
		return nil, fmt.Errorf("модуль %s не зарегистрирован", moduleID)
	}
	return module, nil
}

func (s Service) enqueue(module core.QueueModule, config any, plan core.ModuleTaskPlan) (core.ModuleActionResult, error) {
	if s.Submitter == nil {
		return core.ModuleActionResult{}, errors.New("обработчик очереди не задан")
	}

	spec := QueueTaskSpec{
		ModuleID:  module.ID(),
		Title:     plan.Task.Title,
		Signature: plan.Task.Signature,
		Exclusive: plan.Task.Exclusive,
		Run: func(taskCtx core.TaskContext) error {
			return ExecuteWithTaskRuntime(taskCtx, s.Services, func(taskServices core.ModuleServices) error {
				return module.ExecuteTask(taskCtx, taskServices, config)
			})
		},
	}

	submission, err := s.Submitter.Submit(spec)
	if err != nil {
		return core.ModuleActionResult{}, err
	}

	result := plan.Result
	if result.Note == "" {
		result.Note = "Задача добавлена в очередь."
	}
	result.TaskID = submission.TaskID
	return result, nil
}

func (s Service) executeImmediate(module core.QueueModule, config any, plan core.ModuleTaskPlan) (core.ModuleActionResult, error) {
	ctx := s.immediateContext()

	if action, ok := module.(core.ImmediateModuleAction); ok {
		result, err := action.ExecuteImmediate(ctx, s.Services, config)
		if result.Note == "" {
			result.Note = plan.Result.Note
		}
		return result, err
	}

	err := ExecuteWithTaskRuntime(ctx, s.Services, func(taskServices core.ModuleServices) error {
		return module.ExecuteTask(ctx, taskServices, config)
	})
	result := plan.Result
	return result, err
}

func (s Service) configureContext() core.TaskContext {
	if s.ConfigureContext != nil {
		return s.ConfigureContext
	}
	return core.NewSilentTaskContext(context.Background())
}

func (s Service) immediateContext() core.TaskContext {
	if s.ImmediateContext != nil {
		return s.ImmediateContext
	}
	return core.NewSilentTaskContext(context.Background())
}

func isNilConfig(config any) bool {
	if config == nil {
		return true
	}
	value := reflect.ValueOf(config)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
