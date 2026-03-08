package modruntime

import (
	"errors"
	"goMH/taskqueue"
)

type TaskQueueSubmitter struct {
	Queue *taskqueue.Queue
}

func NewTaskQueueSubmitter(queue *taskqueue.Queue) TaskQueueSubmitter {
	return TaskQueueSubmitter{Queue: queue}
}

func (s TaskQueueSubmitter) Submit(spec QueueTaskSpec) (Submission, error) {
	if s.Queue == nil {
		return Submission{}, errors.New("очередь задач не задана")
	}

	snapshot, err := s.Queue.Enqueue(taskqueue.TaskSpec{
		ModuleID:  spec.ModuleID,
		Title:     spec.Title,
		Signature: spec.Signature,
		Exclusive: spec.Exclusive,
		Run:       spec.Run,
	})
	if err != nil {
		return Submission{}, err
	}
	return Submission{TaskID: snapshot.ID}, nil
}
