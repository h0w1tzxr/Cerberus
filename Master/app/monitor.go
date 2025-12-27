package master

import "time"

type workerMonitor struct {
	state  *masterState
	stopCh chan struct{}
}

type workerSummary struct {
	id string
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
					summaries = append(summaries, workerSummary{
						id: id,
					})
					staleLogged[id] = true
				}
				m.state.mu.Unlock()
				for _, summary := range summaries {
					logError("Worker Disconnected: %s", summary.id)
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
