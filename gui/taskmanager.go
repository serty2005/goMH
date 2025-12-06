package gui

import (
	"fmt"
	"sync"

	"github.com/lxn/walk"
)

// Task представляет собой функцию, которую нужно выполнить.
type Task func() error

// TaskManager управляет очередью задач.
type TaskManager struct {
	mw            *walk.MainWindow
	queue         chan Task
	wg            sync.WaitGroup
	totalProgress *walk.ProgressBar
	isRunning     bool
	mu            sync.Mutex
}

func NewTaskManager(mw *walk.MainWindow, totalProgress *walk.ProgressBar) *TaskManager {
	return &TaskManager{
		mw:            mw,
		queue:         make(chan Task, 100), // Буферизованная очередь
		totalProgress: totalProgress,
	}
}

// Start запускает воркер обработки очереди.
func (tm *TaskManager) Start() {
	go tm.worker()
}

// AddTask добавляет задачу в очередь.
func (tm *TaskManager) AddTask(t Task) {
	tm.queue <- t
	tm.updateTotalProgress()
}

func (tm *TaskManager) worker() {
	for task := range tm.queue {
		tm.setRunning(true)

		// Выполнение задачи
		if err := task(); err != nil {
			// Ошибки уже должны быть обработаны внутри задачи через Context.Error,
			// но здесь можно добавить глобальную логику.
			fmt.Printf("Task error: %v\n", err)
		}

		tm.mw.Synchronize(func() {
			val := tm.totalProgress.Value()
			tm.totalProgress.SetValue(val + 1)
		})

		tm.setRunning(false)
	}
}

func (tm *TaskManager) setRunning(running bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.isRunning = running
	// Тут можно блокировать/разблокировать интерфейс, если нужно
}

func (tm *TaskManager) updateTotalProgress() {
	tm.mw.Synchronize(func() {
		// Простая логика: увеличиваем максимум прогресс-бара
		max := tm.totalProgress.MaxValue()
		if max == 0 { // Если это первая задача
			tm.totalProgress.SetRange(0, 1)
			tm.totalProgress.SetValue(0)
		} else {
			tm.totalProgress.SetRange(0, max+1)
		}
	})
}
