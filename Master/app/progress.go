package master

import (
	"fmt"
	"log"
	"strings"
)

const masterProgressBarWidth = 24

func logTaskProgress(task *Task, completed int64) {
	if task == nil || task.TotalKeyspace <= 0 {
		return
	}
	if completed < 0 {
		completed = 0
	}
	if completed > task.TotalKeyspace {
		completed = task.TotalKeyspace
	}
	log.Printf("Task %s progress %s", task.ID, formatProgressBar(completed, task.TotalKeyspace, masterProgressBarWidth))
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
