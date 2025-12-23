package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"cracker/Common/console"
)

const (
	workerProgressBarWidth = 16
	workerStallAfter       = 6 * time.Second
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

	header := fmt.Sprintf("%s worker=%s | master=%s | cores=%d | state=%s", console.TagInfo(), workerID, masterAddr, cores, colorState(globalState))
	lines := []string{header, separatorLine(), console.ColorInfo("THREADS")}
	if len(slots) == 0 {
		lines = append(lines, "-")
	} else {
		for _, slot := range slots {
			lines = append(lines, slotLine(slot, now))
		}
	}
	lines = append(lines, separatorLine())
	lines = append(lines, eventLine(event))
	return strings.Join(lines, "\n")
}

func slotLine(slot slotState, now time.Time) string {
	runState := slot.state
	if runState == "" {
		runState = "idle"
	}
	chunkLabel := shortID(slot.chunkID)
	if runState == "idle" {
		chunkLabel = "-"
	}
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

	bar := console.FormatProgressBar(barProcessed, barTotal, workerProgressBarWidth, state)
	rateLabel := "-"
	if !slot.startedAt.IsZero() && slot.processed > 0 {
		elapsed := now.Sub(slot.startedAt).Seconds()
		if elapsed > 0 {
			rateLabel = console.FormatHashRate(float64(slot.processed) / elapsed)
		}
	}
	return fmt.Sprintf("T%02d %s chunk=%s %s rate=%s", slot.id, colorState(runState), chunkLabel, bar, rateLabel)
}

func colorState(state string) string {
	switch state {
	case "cracking":
		return console.ColorProcessing(state)
	case "dispatch-paused", "stalled":
		return console.ColorWarn(state)
	case "master-down", "error":
		return console.ColorError(state)
	case "fetching", "reporting", "connecting":
		return console.ColorInfo(state)
	case "idle":
		return console.ColorMuted(state)
	default:
		return console.ColorInfo(state)
	}
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
	return strings.Repeat("-", 90)
}

func shortID(value string) string {
	if value == "" {
		return "-"
	}
	const max = 12
	if len(value) <= max {
		return value
	}
	return value[len(value)-max:]
}
