package master

import (
	"fmt"
	"sync/atomic"
	"time"

	"cracker/Common/console"
)

const masterRenderInterval = time.Second / 30

type masterUI struct {
	state     *masterState
	taskID    atomic.Value
	chunkID   atomic.Value
	processed atomic.Int64
	total     atomic.Int64
}

func newMasterUI(state *masterState) *masterUI {
	ui := &masterUI{state: state}
	ui.taskID.Store("-")
	ui.chunkID.Store("-")
	return ui
}

func (ui *masterUI) UpdateProgress(taskID, chunkID string, processed, total int64) {
	if ui == nil {
		return
	}
	if taskID == "" {
		taskID = "-"
	}
	if chunkID == "" {
		chunkID = "-"
	}
	ui.taskID.Store(taskID)
	ui.chunkID.Store(chunkID)
	ui.processed.Store(processed)
	ui.total.Store(total)
}

func (ui *masterUI) StatusLine() string {
	if ui == nil {
		return ""
	}
	taskID, _ := ui.taskID.Load().(string)
	chunkID, _ := ui.chunkID.Load().(string)
	processed := ui.processed.Load()
	total := ui.total.Load()
	percent := console.FormatPercent(processed, total)

	workerCount := 0
	totalRate := 0.0
	if ui.state != nil {
		ui.state.mu.Lock()
		workerCount = len(ui.state.workers)
		for _, worker := range ui.state.workers {
			totalRate += worker.AvgRate
		}
		ui.state.mu.Unlock()
	}
	return fmt.Sprintf("rate=%s | task=%s | chunk=%s | progress=%s | workers=%d", console.FormatHashRate(totalRate), taskID, chunkID, percent, workerCount)
}

func formatDuration(duration time.Duration) string {
	if duration <= 0 {
		return "-"
	}
	totalSeconds := int(duration.Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}
