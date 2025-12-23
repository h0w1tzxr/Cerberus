package master

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	masterProgressBarWidth    = 24
	masterProgressLogInterval = 2 * time.Second
)

var progressLogTracker = &taskProgressTracker{
	lastLogged: make(map[string]time.Time),
}

type taskProgressTracker struct {
	mu         sync.Mutex
	lastLogged map[string]time.Time
}

func logTaskProgress(task *Task, completed int64) {
	if task == nil || task.TotalKeyspace <= 0 {
		return
	}
	if !progressLogTracker.shouldLog(task.ID, time.Now()) {
		return
	}
	if completed < 0 {
		completed = 0
	}
	if completed > task.TotalKeyspace {
		completed = task.TotalKeyspace
	}
	logInfo("Task %s progress %s", task.ID, formatProgressBar(completed, task.TotalKeyspace, masterProgressBarWidth))
}

func (t *taskProgressTracker) shouldLog(taskID string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	last, ok := t.lastLogged[taskID]
	if ok && now.Sub(last) < masterProgressLogInterval {
		return false
	}
	t.lastLogged[taskID] = now
	return true
}

func formatProgressBar(completed, total int64, width int) string {
	if total <= 0 || width <= 0 {
		return "[no-progress]"
	}
	percent := float64(completed) / float64(total)
	filled := int(percent * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	return fmt.Sprintf("[%s] %6.2f%% (%d/%d)", bar, percent*100, completed, total)
}
