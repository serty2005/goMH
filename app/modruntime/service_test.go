package modruntime

import (
	"testing"

	"goMH/core"
)

type testResolver struct {
	module core.QueueModule
}

func (r testResolver) Get(id string) (core.QueueModule, bool) {
	if r.module == nil || r.module.ID() != id {
		return nil, false
	}
	return r.module, true
}

type testSubmitter struct {
	calls int
}

func (s *testSubmitter) Submit(spec QueueTaskSpec) (Submission, error) {
	s.calls++
	return Submission{TaskID: "task-1"}, nil
}

type testQueueModule struct {
	id   string
	plan core.ModuleTaskPlan
	cfg  any
}

func (m testQueueModule) ID() string { return m.id }

func (m testQueueModule) MenuText() string { return "test module" }

func (m testQueueModule) Run(am core.AssetManager, wu core.WinUtils) error { return nil }

func (m testQueueModule) ConfigureTask(ctx core.TaskContext, services core.ModuleServices) (any, error) {
	return m.cfg, nil
}

func (m testQueueModule) BuildTask(config any) (core.ModuleTaskPlan, error) {
	return m.plan, nil
}

func (m testQueueModule) ExecuteTask(ctx core.TaskContext, services core.ModuleServices, config any) error {
	return nil
}

func TestEnqueuePreparedDoesNotSubmitWithoutConfirmation(t *testing.T) {
	module := testQueueModule{
		id: "test",
		plan: core.ModuleTaskPlan{
			Mode: core.ModuleRunModeQueue,
			Task: core.ModuleTaskSpec{
				Title:     "test task",
				Signature: "test|queue",
			},
		},
	}
	submitter := &testSubmitter{}
	service := Service{
		Resolver:  testResolver{module: module},
		Submitter: submitter,
		ConfirmPrepared: func(module core.QueueModule, config any, plan core.ModuleTaskPlan) (bool, error) {
			return false, nil
		},
	}

	result, err := service.EnqueuePrepared("test", struct{}{})
	if err != nil {
		t.Fatalf("unexpected error while cancelling confirmation: %v", err)
	}
	if submitter.calls != 0 {
		t.Fatalf("submit should not be called on cancellation, got %d", submitter.calls)
	}
	if result.TaskID != "" {
		t.Fatalf("expected empty TaskID on cancellation, got %q", result.TaskID)
	}
	if result.Note != "Постановка задачи отменена." {
		t.Fatalf("unexpected cancellation note: %q", result.Note)
	}
}

func TestEnqueuePreparedSubmitsAfterConfirmation(t *testing.T) {
	module := testQueueModule{
		id: "test",
		plan: core.ModuleTaskPlan{
			Mode: core.ModuleRunModeQueue,
			Task: core.ModuleTaskSpec{
				Title:     "test task",
				Signature: "test|queue",
			},
		},
	}
	submitter := &testSubmitter{}
	service := Service{
		Resolver:  testResolver{module: module},
		Submitter: submitter,
		ConfirmPrepared: func(module core.QueueModule, config any, plan core.ModuleTaskPlan) (bool, error) {
			return true, nil
		},
	}

	result, err := service.EnqueuePrepared("test", struct{}{})
	if err != nil {
		t.Fatalf("unexpected enqueue error: %v", err)
	}
	if submitter.calls != 1 {
		t.Fatalf("expected one submit call, got %d", submitter.calls)
	}
	if result.TaskID != "task-1" {
		t.Fatalf("expected task-1, got %q", result.TaskID)
	}
}

func TestEnqueuePreparedSkipsConfirmationWhenRequestedByPlan(t *testing.T) {
	module := testQueueModule{
		id: "test",
		plan: core.ModuleTaskPlan{
			Mode:             core.ModuleRunModeImmediate,
			SkipConfirmation: true,
			Result: core.ModuleActionResult{
				Note: "started",
			},
		},
	}

	confirmCalls := 0
	service := Service{
		Resolver: testResolver{module: module},
		ConfirmPrepared: func(module core.QueueModule, config any, plan core.ModuleTaskPlan) (bool, error) {
			confirmCalls++
			return false, nil
		},
	}

	result, err := service.EnqueuePrepared("test", struct{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if confirmCalls != 0 {
		t.Fatalf("confirmation should be skipped, got %d calls", confirmCalls)
	}
	if result.Note != "started" {
		t.Fatalf("unexpected result note: %q", result.Note)
	}
}
