package master

import (
	"time"

	"cracker/Common/console"
)

type workerMonitor struct {
	state  *masterState
	stopCh chan struct{}
}

type workerSummary struct {
	id        string
	chunks    int64
	processed int64
	duration  time.Duration
	avgRate   float64
}

func newWorkerMonitor(state *masterState) *workerMonitor {
	return &workerMonitor{state: state, stopCh: make(chan struct{})}
}

func (m *workerMonitor) Start() {
	if m == nil || m.state == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		staleLogged := make(map[string]bool)
		for {
			select {
			case <-ticker.C:
				now := time.Now()
				summaries := make([]workerSummary, 0)
				m.state.mu.Lock()
				for id, info := range m.state.workers {
					health := workerHealth(now, info.LastSeen, WorkerStaleAfter)
					if health == "healthy" {
						staleLogged[id] = false
						continue
					}
					if staleLogged[id] {
						continue
					}
					avgRate := 0.0
					if info.TotalDuration > 0 && info.TotalProcessed > 0 {
						avgRate = float64(info.TotalProcessed) / info.TotalDuration.Seconds()
					}
					summaries = append(summaries, workerSummary{
						id:        id,
						chunks:    info.CompletedChunks,
						processed: info.TotalProcessed,
						duration:  info.TotalDuration,
						avgRate:   avgRate,
					})
					staleLogged[id] = true
				}
				m.state.mu.Unlock()
				for _, summary := range summaries {
					logWarn("Worker %s disconnected. chunks=%d processed=%d duration=%s avg=%s", summary.id, summary.chunks, summary.processed, formatDuration(summary.duration), console.FormatHashRate(summary.avgRate))
				}
			case <-m.stopCh:
				return
			}
		}
	}()
}

func (m *workerMonitor) Stop() {
	if m == nil || m.stopCh == nil {
		return
	}
	close(m.stopCh)
	m.stopCh = nil
}
