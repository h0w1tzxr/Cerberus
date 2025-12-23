package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"cracker/Common/console"
)

const (
	workerProgressBarWidth = 12
	workerStallAfter       = 6 * time.Second
	workerMaxSlotsDisplay  = 12
	workerMaxIdleDisplay   = 4
	workerStateWidth       = 7
	workerChunkWidth       = 7
	workerRateWidth        = 9
)

type workerEventLevel int

const (
	workerEventInfo workerEventLevel = iota
	workerEventWarn
	workerEventError
	workerEventSuccess
)

type workerEvent struct {
	level   workerEventLevel
	message string
	at      time.Time
}

type slotState struct {
	id         int
	state      string
	chunkID    string
	processed  int64
	total      int64
	startedAt  time.Time
	lastUpdate time.Time
}

type workerTelemetry struct {
	mu          sync.RWMutex
	workerID    string
	masterAddr  string
	cores       int32
	slots       []slotState
	globalState string
	event       workerEvent
}

func newWorkerTelemetry(workerID, masterAddr string, cores int32, slots int) *workerTelemetry {
	if slots <= 0 {
		slots = 1
	}
	telemetry := &workerTelemetry{
		workerID:    workerID,
		masterAddr:  masterAddr,
		cores:       cores,
		globalState: "starting",
	}
	telemetry.slots = make([]slotState, slots)
	for i := range telemetry.slots {
		telemetry.slots[i] = slotState{id: i, state: "idle"}
	}
	return telemetry
}

func (w *workerTelemetry) SetGlobalState(state string) {
	if w == nil {
		return
	}
	if state == "" {
		state = "idle"
	}
	w.mu.Lock()
	w.globalState = state
	w.mu.Unlock()
}

func (w *workerTelemetry) SetSlotState(slotID int, state string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if slotID >= 0 && slotID < len(w.slots) {
		if state == "" {
			state = "idle"
		}
		w.slots[slotID].state = state
	}
	w.mu.Unlock()
}

func (w *workerTelemetry) StartChunk(slotID int, chunkID string, total int64) {
	if w == nil {
		return
	}
	if chunkID == "" {
		chunkID = "-"
	}
	now := time.Now()
	w.mu.Lock()
	if slotID >= 0 && slotID < len(w.slots) {
		slot := &w.slots[slotID]
		slot.chunkID = chunkID
		slot.processed = 0
		slot.total = total
		slot.startedAt = now
		slot.lastUpdate = now
		slot.state = "cracking"
	}
	w.mu.Unlock()
}

func (w *workerTelemetry) UpdateProgress(slotID int, processed, total int64) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if slotID >= 0 && slotID < len(w.slots) {
		slot := &w.slots[slotID]
		slot.processed = processed
		if total > 0 {
			slot.total = total
		}
		slot.lastUpdate = time.Now()
	}
	w.mu.Unlock()
}

func (w *workerTelemetry) FinishChunk(slotID int, processed, total int64) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if slotID >= 0 && slotID < len(w.slots) {
		slot := &w.slots[slotID]
		if total > 0 {
			slot.total = total
			if processed < total {
				processed = total
			}
		}
		slot.processed = processed
		slot.lastUpdate = time.Now()
	}
	w.mu.Unlock()
}

func (w *workerTelemetry) RecordEvent(level workerEventLevel, message string) {
	if w == nil || message == "" {
		return
	}
	w.mu.Lock()
	w.event = workerEvent{level: level, message: message, at: time.Now()}
	w.mu.Unlock()
}

func (w *workerTelemetry) StatusLine() string {
	if w == nil {
		return ""
	}
	now := time.Now()
	w.mu.RLock()
	workerID := w.workerID
	masterAddr := w.masterAddr
	cores := w.cores
	globalState := w.globalState
	slots := make([]slotState, len(w.slots))
	copy(slots, w.slots)
	event := w.event
	w.mu.RUnlock()

	activeSlots, idleSlots := splitSlots(slots)
	displaySlots := activeSlots
	hiddenActive := 0
	hiddenIdle := 0
	if len(displaySlots) > workerMaxSlotsDisplay {
		hiddenActive = len(displaySlots) - workerMaxSlotsDisplay
		displaySlots = displaySlots[:workerMaxSlotsDisplay]
	}
	if len(activeSlots) == 0 {
		displaySlots = idleSlots
		if len(displaySlots) > workerMaxIdleDisplay {
			hiddenIdle = len(displaySlots) - workerMaxIdleDisplay
			displaySlots = displaySlots[:workerMaxIdleDisplay]
		}
	} else {
		hiddenIdle = len(idleSlots)
	}

	header := fmt.Sprintf("%s worker %s | master %s", console.TagInfo(), workerID, masterAddr)
	meta := fmt.Sprintf("state %s | cores %d | slots %d | active %d | idle %d", stateLabelCompact(globalState), cores, len(slots), len(activeSlots), len(idleSlots))
	lines := []string{header, meta, separatorLine(), threadHeaderLine()}
	if len(displaySlots) == 0 {
		lines = append(lines, console.ColorMuted("no active threads"))
	} else {
		for _, slot := range displaySlots {
			lines = append(lines, slotLine(slot, now))
		}
	}
	if hiddenActive > 0 || hiddenIdle > 0 {
		lines = append(lines, hiddenSummaryLine(hiddenActive, hiddenIdle))
	}
	lines = append(lines, separatorLine(), eventLine(event))
	return strings.Join(lines, "\n")
}

func splitSlots(slots []slotState) ([]slotState, []slotState) {
	active := make([]slotState, 0, len(slots))
	idle := make([]slotState, 0, len(slots))
	for _, slot := range slots {
		state := normalizeSlotState(slot.state)
		if state == "idle" {
			idle = append(idle, slot)
		} else {
			active = append(active, slot)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		a := slotActivityTime(active[i])
		b := slotActivityTime(active[j])
		if a.Equal(b) {
			return active[i].id < active[j].id
		}
		return a.After(b)
	})
	sort.Slice(idle, func(i, j int) bool {
		return idle[i].id < idle[j].id
	})
	return active, idle
}

func slotActivityTime(slot slotState) time.Time {
	if !slot.lastUpdate.IsZero() {
		return slot.lastUpdate
	}
	if !slot.startedAt.IsZero() {
		return slot.startedAt
	}
	return time.Time{}
}

func threadHeaderLine() string {
	progressWidth := workerProgressBarWidth + 10
	return console.ColorInfo(fmt.Sprintf("%-3s %-*s %-*s %-*s %s",
		"ID",
		workerStateWidth, "STATE",
		workerChunkWidth, "CHUNK",
		progressWidth, "PROGRESS",
		"RATE",
	))
}

func hiddenSummaryLine(activeHidden, idleHidden int) string {
	parts := make([]string, 0, 2)
	if activeHidden > 0 {
		parts = append(parts, fmt.Sprintf("%d active", activeHidden))
	}
	if idleHidden > 0 {
		parts = append(parts, fmt.Sprintf("%d idle", idleHidden))
	}
	if len(parts) == 0 {
		return ""
	}
	return console.ColorMuted(fmt.Sprintf("hidden: %s", strings.Join(parts, ", ")))
}

func slotLine(slot slotState, now time.Time) string {
	runState := normalizeSlotState(slot.state)
	chunkLabel := formatChunkLabel(runState, slot.chunkID)

	barProcessed := slot.processed
	barTotal := slot.total
	state := console.ProgressStateIdle
	if barTotal > 0 {
		if barProcessed >= barTotal {
			state = console.ProgressStateCompleted
		} else if !slot.lastUpdate.IsZero() && now.Sub(slot.lastUpdate) > workerStallAfter {
			state = console.ProgressStateStalled
		} else {
			state = console.ProgressStateProcessing
		}
	} else {
		barTotal = 1
		barProcessed = 0
	}
	if runState == "idle" {
		barTotal = 1
		barProcessed = 0
		state = console.ProgressStateIdle
	}

	progressWidth := workerProgressBarWidth + 10
	bar := padRight("-", progressWidth)
	if runState == "cracking" || runState == "reporting" {
		bar = console.FormatProgressBar(barProcessed, barTotal, workerProgressBarWidth, state)
	}
	rateLabel := "-"
	if runState == "cracking" || runState == "reporting" {
		if !slot.startedAt.IsZero() && slot.processed > 0 {
			elapsed := now.Sub(slot.startedAt).Seconds()
			if elapsed > 0 {
				rateLabel = console.FormatHashRate(float64(slot.processed) / elapsed)
			}
		}
	}
	rateLabel = padLeft(rateLabel, workerRateWidth)
	return fmt.Sprintf("T%02d %s %s %s %s", slot.id, stateLabel(runState), chunkLabel, bar, rateLabel)
}

func stateLabel(state string) string {
	label := padRight(shortStateLabel(state), workerStateWidth)
	return colorStateLabel(state, label)
}

func stateLabelCompact(state string) string {
	return colorStateLabel(state, shortStateLabel(state))
}

func colorStateLabel(state, label string) string {
	switch state {
	case "cracking":
		return console.ColorProcessing(label)
	case "dispatch-paused", "stalled":
		return console.ColorWarn(label)
	case "master-down", "error":
		return console.ColorError(label)
	case "fetching", "reporting", "connecting":
		return console.ColorInfo(label)
	case "idle":
		return console.ColorMuted(label)
	default:
		return console.ColorInfo(label)
	}
}

func shortStateLabel(state string) string {
	switch state {
	case "cracking":
		return "crack"
	case "reporting":
		return "report"
	case "fetching":
		return "fetch"
	case "dispatch-paused":
		return "paused"
	case "master-down":
		return "down"
	case "connecting":
		return "conn"
	case "connected":
		return "conn"
	case "idle":
		return "idle"
	default:
		return state
	}
}

func normalizeSlotState(state string) string {
	state = strings.TrimSpace(state)
	if state == "" {
		return "idle"
	}
	return state
}

func formatChunkLabel(state, chunkID string) string {
	if state == "idle" || state == "fetching" || state == "connecting" || state == "master-down" || state == "dispatch-paused" {
		return padRight("-", workerChunkWidth)
	}
	label := shortChunkID(chunkID, workerChunkWidth)
	return padRight(label, workerChunkWidth)
}

func eventLine(event workerEvent) string {
	if event.message == "" {
		return "event: -"
	}
	return fmt.Sprintf("event: %s", colorEvent(event.level, event.message))
}

func colorEvent(level workerEventLevel, message string) string {
	switch level {
	case workerEventSuccess:
		return console.ColorSuccessBright(message)
	case workerEventError:
		return console.ColorError(message)
	case workerEventWarn:
		return console.ColorWarn(message)
	default:
		return console.ColorInfo(message)
	}
}

func separatorLine() string {
	return console.SeparatorLine()
}

func shortChunkID(value string, max int) string {
	if value == "" {
		return "-"
	}
	suffix := chunkIDSuffix(value)
	if suffix != "" {
		label := "#" + suffix
		return shortLabel(label, max)
	}
	return shortLabel(value, max)
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
