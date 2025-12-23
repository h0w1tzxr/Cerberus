package master

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cracker/Common/console"
)

const masterRenderInterval = time.Second / 30

const (
	dashboardProgressBarWidth = 18
	workerStallAfter          = 6 * time.Second
)

type uiEventLevel int

const (
	uiEventInfo uiEventLevel = iota
	uiEventWarn
	uiEventError
	uiEventSuccess
)

type uiEvent struct {
	level   uiEventLevel
	message string
	at      time.Time
}

type masterUI struct {
	state     *masterState
	taskID    atomic.Value
	chunkID   atomic.Value
	processed atomic.Int64
	total     atomic.Int64
	eventMu   sync.Mutex
	event     uiEvent
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

func (ui *masterUI) SetEvent(level uiEventLevel, message string) {
	if ui == nil || message == "" {
		return
	}
	ui.eventMu.Lock()
	ui.event = uiEvent{level: level, message: message, at: time.Now()}
	ui.eventMu.Unlock()
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

	now := time.Now()
	workerCount := 0
	activeWorkers := 0
	totalRate := 0.0
	dispatchPaused := false
	snapshots := []workerSnapshot{}

	if ui.state != nil {
		ui.state.mu.Lock()
		dispatchPaused = ui.state.dispatchPaused
		workerCount = len(ui.state.workers)
		snapshots = snapshotWorkersLocked(ui.state, now)
		for _, snapshot := range snapshots {
			totalRate += snapshot.avgRate
			if snapshot.activeChunks > 0 {
				activeWorkers++
			}
		}
		ui.state.mu.Unlock()
	}

	header := fmt.Sprintf("%s master | rate=%s | task=%s | chunk=%s | progress=%s | workers=%d | active=%d | dispatch=%s", console.TagInfo(), console.FormatHashRate(totalRate), taskID, chunkID, percent, workerCount, activeWorkers, dispatchLabel(dispatchPaused))

	lines := []string{header, separatorLine()}
	lines = append(lines, console.ColorInfo("WORKERS"))
	if len(snapshots) == 0 {
		lines = append(lines, "-")
	} else {
		sort.Slice(snapshots, func(i, j int) bool {
			return snapshots[i].lastSeen.After(snapshots[j].lastSeen)
		})
		for _, snapshot := range snapshots {
			lines = append(lines, snapshotLines(snapshot, now)...)
		}
	}
	lines = append(lines, separatorLine())
	lines = append(lines, ui.eventLine())
	return strings.Join(lines, "\n")
}

func dispatchLabel(paused bool) string {
	if paused {
		return console.ColorWarn("paused")
	}
	return console.ColorSuccess("active")
}

func separatorLine() string {
	return strings.Repeat("-", 90)
}

type workerSnapshot struct {
	id              string
	cpuCores        int32
	lastSeen        time.Time
	health          string
	avgRate         float64
	lastChunkRate   float64
	lastTaskID      string
	activeChunks    int
	activeTaskID    string
	activeStart     int64
	activeEnd       int64
	activeProcessed int64
	activeTotal     int64
	lastProgressAt  time.Time
}

func snapshotWorkersLocked(state *masterState, now time.Time) []workerSnapshot {
	if state == nil {
		return nil
	}
	snapshots := make([]workerSnapshot, 0, len(state.workers))
	snapshotByID := make(map[string]int, len(state.workers))
	for id, info := range state.workers {
		snapshot := workerSnapshot{
			id:            id,
			cpuCores:      info.CPUCores,
			lastSeen:      info.LastSeen,
			health:        workerHealth(now, info.LastSeen, WorkerStaleAfter),
			avgRate:       info.AvgRate,
			lastChunkRate: info.LastChunkRate,
			lastTaskID:    info.LastTaskID,
		}
		snapshots = append(snapshots, snapshot)
		snapshotByID[id] = len(snapshots) - 1
	}

	for chunkID, lease := range state.activeChunks {
		index, ok := snapshotByID[lease.workerID]
		if !ok {
			continue
		}
		snapshot := &snapshots[index]
		snapshot.activeChunks++
		progressAt := lease.lastProgressAt
		if progressAt.IsZero() {
			progressAt = lease.assignedAt
		}
		if snapshot.activeTaskID == "" || progressAt.After(snapshot.lastProgressAt) {
			snapshot.activeTaskID = lease.taskID
			snapshot.activeStart = lease.start
			snapshot.activeEnd = lease.end
			snapshot.activeProcessed = state.chunkProgress[chunkID]
			snapshot.activeTotal = lease.end - lease.start
			snapshot.lastProgressAt = progressAt
		}
	}
	return snapshots
}

func snapshotLines(snapshot workerSnapshot, now time.Time) []string {
	healthLabel := colorHealth(snapshot.health)
	taskLabel := snapshot.activeTaskID
	if taskLabel == "" {
		taskLabel = snapshot.lastTaskID
	}
	if taskLabel == "" {
		taskLabel = "-"
	}
	assignment := "idle"
	if snapshot.activeChunks > 0 {
		assignment = fmt.Sprintf("task=%s range=%d-%d", taskLabel, snapshot.activeStart, snapshot.activeEnd)
	} else {
		assignment = fmt.Sprintf("task=%s idle", taskLabel)
	}
	line1 := fmt.Sprintf("%s %s cores=%d active=%d %s", snapshot.id, healthLabel, snapshot.cpuCores, snapshot.activeChunks, assignment)

	barProcessed := snapshot.activeProcessed
	barTotal := snapshot.activeTotal
	state := console.ProgressStateIdle
	if barTotal > 0 {
		if barProcessed >= barTotal {
			state = console.ProgressStateCompleted
		} else if !snapshot.lastProgressAt.IsZero() && now.Sub(snapshot.lastProgressAt) > workerStallAfter {
			state = console.ProgressStateStalled
		} else {
			state = console.ProgressStateProcessing
		}
	} else {
		barTotal = 1
		barProcessed = 0
	}

	bar := console.FormatProgressBar(barProcessed, barTotal, dashboardProgressBarWidth, state)
	rateLabel := console.FormatHashRate(snapshot.avgRate)
	lastLabel := "-"
	if snapshot.lastChunkRate > 0 {
		lastLabel = console.FormatHashRate(snapshot.lastChunkRate)
	}
	line2 := fmt.Sprintf("  %s rate=%s last=%s", bar, rateLabel, lastLabel)
	return []string{line1, line2}
}

func colorHealth(health string) string {
	switch health {
	case "healthy":
		return console.ColorSuccess(health)
	case "stale":
		return console.ColorWarn(health)
	default:
		return console.ColorError(health)
	}
}

func (ui *masterUI) eventLine() string {
	ui.eventMu.Lock()
	event := ui.event
	ui.eventMu.Unlock()
	if event.message == "" {
		return "event: -"
	}
	return fmt.Sprintf("event: %s", colorEvent(event.level, event.message))
}

func colorEvent(level uiEventLevel, message string) string {
	switch level {
	case uiEventSuccess:
		return console.ColorSuccessBright(message)
	case uiEventError:
		return console.ColorError(message)
	case uiEventWarn:
		return console.ColorWarn(message)
	default:
		return console.ColorInfo(message)
	}
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
