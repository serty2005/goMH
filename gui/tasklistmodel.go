package gui

import (
	"fmt"
	"time"

	"github.com/lxn/walk"
)

type TaskListModel struct {
	walk.TableModelBase
	items []TaskSnapshot
}

func NewTaskListModel() *TaskListModel {
	return &TaskListModel{}
}

func (m *TaskListModel) RowCount() int {
	return len(m.items)
}

func (m *TaskListModel) Value(row, col int) interface{} {
	item := m.items[row]
	switch col {
	case 0:
		return string(item.State)
	case 1:
		return item.ModuleID
	case 2:
		return item.Title
	case 3:
		return item.StageText
	case 4:
		if item.State == TaskRunning || item.State == TaskSuccess || item.State == TaskFailed {
			return fmt.Sprintf("%d%%", item.Progress)
		}
		return "-"
	case 5:
		if item.FinishedAt.IsZero() {
			if item.StartedAt.IsZero() {
				return fmt.Sprintf("queued %s", item.EnqueuedAt.Format("15:04:05"))
			}
			return fmt.Sprintf("started %s", item.StartedAt.Format("15:04:05"))
		}
		d := item.FinishedAt.Sub(item.StartedAt)
		if d < 0 {
			d = 0
		}
		return humanDuration(d)
	}
	return ""
}

func (m *TaskListModel) Replace(items []TaskSnapshot) {
	m.items = append(m.items[:0], items...)
	m.PublishRowsReset()
}

func humanDuration(d time.Duration) string {
	sec := int(d.Seconds())
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	min := sec / 60
	sec = sec % 60
	return fmt.Sprintf("%dm %02ds", min, sec)
}
