package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"cracker/Common/console"
)

type workerTelemetry struct {
	chunkID    atomic.Value
	state      atomic.Value
	processed  atomic.Int64
	total      atomic.Int64
	startedAt  atomic.Int64
	lastUpdate atomic.Int64
}

func newWorkerTelemetry() *workerTelemetry {
	telemetry := &workerTelemetry{}
	telemetry.chunkID.Store("-")
	telemetry.state.Store("idle")
	return telemetry
}

func (w *workerTelemetry) SetState(state string) {
	if w == nil {
		return
	}
	if state == "" {
		state = "idle"
	}
	w.state.Store(state)
}

func (w *workerTelemetry) StartChunk(chunkID string, total int64) {
	if w == nil {
		return
	}
	if chunkID == "" {
		chunkID = "-"
	}
	w.chunkID.Store(chunkID)
	w.processed.Store(0)
	w.total.Store(total)
	now := time.Now().UnixNano()
	w.startedAt.Store(now)
	w.lastUpdate.Store(now)
	w.state.Store("cracking")
}

func (w *workerTelemetry) UpdateProgress(processed, total int64) {
	if w == nil {
		return
	}
	w.processed.Store(processed)
	if total > 0 {
		w.total.Store(total)
	}
	w.lastUpdate.Store(time.Now().UnixNano())
}

func (w *workerTelemetry) FinishChunk(processed, total int64) {
	if w == nil {
		return
	}
	if total > 0 {
		w.total.Store(total)
		if processed < total {
			processed = total
		}
	}
	w.processed.Store(processed)
	w.lastUpdate.Store(time.Now().UnixNano())
}

func (w *workerTelemetry) StatusLine() string {
	if w == nil {
		return ""
	}
	state, _ := w.state.Load().(string)
	if state == "" {
		state = "idle"
	}
	chunkID, _ := w.chunkID.Load().(string)
	if chunkID == "" {
		chunkID = "-"
	}
	processed := w.processed.Load()
	total := w.total.Load()
	percent := console.FormatPercent(processed, total)
	var rateStr string
	startedAt := w.startedAt.Load()
	if processed > 0 && startedAt > 0 {
		elapsed := time.Since(time.Unix(0, startedAt)).Seconds()
		if elapsed > 0 {
			rateStr = console.FormatHashRate(float64(processed) / elapsed)
		}
	}
	if rateStr == "" {
		rateStr = "-"
	}
	return fmt.Sprintf("status=%s | chunk=%s | rate=%s | progress=%s", state, chunkID, rateStr, percent)
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
