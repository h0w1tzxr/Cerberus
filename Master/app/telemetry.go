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
	dashboardProgressBarWidth = 12
	workerStallAfter          = 6 * time.Second
	masterMaxWorkersDisplay   = 12
	masterWorkerIDWidth       = 10
	masterHealthWidth         = 5
	masterCoresWidth          = 5
	masterActiveWidth         = 6
	masterTaskWidth           = 10
	masterRangeWidth          = 11
	masterRateWidth           = 9
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

	headerTop := fmt.Sprintf("%s master | rate=%s | task=%s | progress=%s", console.TagInfo(), console.FormatHashRate(totalRate), taskID, percent)
	chunkLabel := formatChunkHeader(chunkID)
	headerBottom := fmt.Sprintf("chunk=%s | workers=%d | active=%d | dispatch=%s", chunkLabel, workerCount, activeWorkers, dispatchLabel(dispatchPaused))

	lines := []string{headerTop, headerBottom, separatorLine(), workerHeaderLine()}
	if len(snapshots) == 0 {
		lines = append(lines, console.ColorMuted("no workers"))
	} else {
		sort.Slice(snapshots, func(i, j int) bool {
			return snapshots[i].lastSeen.After(snapshots[j].lastSeen)
		})
		display := snapshots
		hidden := 0
		if len(display) > masterMaxWorkersDisplay {
			hidden = len(display) - masterMaxWorkersDisplay
			display = display[:masterMaxWorkersDisplay]
		}
		for _, snapshot := range display {
			lines = append(lines, snapshotLine(snapshot, now))
		}
		if hidden > 0 {
			lines = append(lines, console.ColorMuted(fmt.Sprintf("hidden: %d worker(s)", hidden)))
		}
	}
	lines = append(lines, separatorLine(), ui.eventLine())
	return strings.Join(lines, "\n")
}

func dispatchLabel(paused bool) string {
	if paused {
		return console.ColorWarn("paused")
	}
	return console.ColorSuccess("active")
}

func separatorLine() string {
	return console.SeparatorLine()
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

func workerHeaderLine() string {
	progressWidth := dashboardProgressBarWidth + 10
	return console.ColorInfo(fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s %-*s %-*s %*s %*s",
		masterWorkerIDWidth, "WORKER",
		masterHealthWidth, "HLTH",
		masterCoresWidth, "CORES",
		masterActiveWidth, "ACTIVE",
		masterTaskWidth, "TASK",
		masterRangeWidth, "RANGE",
		progressWidth, "PROGRESS",
		masterRateWidth, "AVG",
		masterRateWidth, "LAST",
	))
}

func snapshotLine(snapshot workerSnapshot, now time.Time) string {
	healthLabel := healthLabel(snapshot.health)
	taskLabel := snapshot.activeTaskID
	if taskLabel == "" {
		taskLabel = snapshot.lastTaskID
	}
	if taskLabel == "" {
		taskLabel = "-"
	}
	taskLabel = padRight(shortLabel(taskLabel, masterTaskWidth), masterTaskWidth)

	rangeLabel := "-"
	if snapshot.activeChunks > 0 {
		rangeLabel = formatRange(snapshot.activeStart, snapshot.activeEnd)
	}
	rangeLabel = padRight(shortLabel(rangeLabel, masterRangeWidth), masterRangeWidth)

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

	progressWidth := dashboardProgressBarWidth + 10
	bar := padRight("-", progressWidth)
	if snapshot.activeChunks > 0 {
		bar = console.FormatProgressBar(barProcessed, barTotal, dashboardProgressBarWidth, state)
	}
	rateLabel := padLeft(console.FormatHashRate(snapshot.avgRate), masterRateWidth)
	lastLabel := "-"
	if snapshot.lastChunkRate > 0 {
		lastLabel = console.FormatHashRate(snapshot.lastChunkRate)
	}
	lastLabel = padLeft(lastLabel, masterRateWidth)

	idLabel := padRight(shortLabel(snapshot.id, masterWorkerIDWidth), masterWorkerIDWidth)
	coresLabel := padLeft(fmt.Sprintf("%d", snapshot.cpuCores), masterCoresWidth)
	activeLabel := padLeft(fmt.Sprintf("%d", snapshot.activeChunks), masterActiveWidth)
	return fmt.Sprintf("%s %s %s %s %s %s %s %s %s", idLabel, healthLabel, coresLabel, activeLabel, taskLabel, rangeLabel, bar, rateLabel, lastLabel)
}

func healthLabel(health string) string {
	label := padRight(shortHealthLabel(health), masterHealthWidth)
	switch health {
	case "healthy":
		return console.ColorSuccess(label)
	case "stale":
		return console.ColorWarn(label)
	default:
		return console.ColorError(label)
	}
}

func shortHealthLabel(health string) string {
	switch health {
	case "healthy":
		return "ok"
	case "stale":
		return "stale"
	case "unknown":
		return "unk"
	default:
		return health
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

func formatRange(start, end int64) string {
	if end <= start {
		return fmt.Sprintf("%d-%d", start, start)
	}
	return fmt.Sprintf("%d-%d", start, end-1)
}

func formatChunkHeader(chunkID string) string {
	value := strings.TrimSpace(chunkID)
	if value == "" || value == "-" {
		return "-"
	}
	if suffix := chunkIDSuffix(value); suffix != "" {
		return "#" + suffix
	}
	return shortLabel(value, masterTaskWidth)
}

func chunkIDSuffix(value string) string {
	end := len(value)
	start := end
	for start > 0 {
		ch := value[start-1]
		if ch < '0' || ch > '9' {
			break
		}
		start--
	}
	if start == end {
		return ""
	}
	return value[start:end]
}

func shortLabel(value string, max int) string {
	if max <= 0 || value == "" {
		return ""
	}
	if len(value) <= max {
		return value
	}
	return value[len(value)-max:]
}

func padRight(value string, width int) string {
	if width <= 0 {
		return value
	}
	if len(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-len(value))
}

func padLeft(value string, width int) string {
	if width <= 0 {
		return value
	}
	if len(value) >= width {
		return value
	}
	return strings.Repeat(" ", width-len(value)) + value
}
