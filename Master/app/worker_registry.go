package master

import "time"

type workerInfo struct {
	ID                string
	CPUCores          int32
	LastSeen          time.Time
	AvgRate           float64
	TotalProcessed    int64
	TotalDuration     time.Duration
	CompletedChunks   int64
	LastChunkID       string
	LastTaskID        string
	LastChunkRate     float64
	LastChunkDuration time.Duration
}

type workerStatus struct {
	ID       string
	CPUCores int32
	LastSeen time.Time
	Inflight int
	Health   string
}

func workerHealth(now, lastSeen time.Time, timeout time.Duration) string {
	if lastSeen.IsZero() {
		return "unknown"
	}
	if now.Sub(lastSeen) <= timeout {
		return "healthy"
	}
	return "stale"
}
