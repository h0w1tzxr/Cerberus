package master

import (
	"sort"
	"strings"
	"sync"
	"time"

	"cracker/Common/security"
)

const defaultTUILogLimit = 1000

type masterTUISnapshot struct {
	Now               time.Time
	ListenAddr        string
	DispatchPaused    bool
	ShutdownRequested bool
	ActiveChunks      int
	Summary           workloadSummary
	TotalRate         float64
	WorkerCount       int
	ActiveWorkers     int
	Workers           []tuiWorkerSnapshot
	Tasks             []tuiTaskSnapshot
	Event             uiEvent
	Logs              []tuiLogEntry
}

type tuiWorkerSnapshot struct {
	ID               string
	CPUCores         int32
	LastSeen         time.Time
	Health           string
	AvgRate          float64
	LastChunkRate    float64
	LastTaskID       string
	ActiveChunks     int
	ActiveTaskID     string
	TotalProcessed   int64
	CompletedChunks  int64
	CompletedTasks   int64
	LastTaskDuration time.Duration
	Quarantined      bool
	QuarantineReason string
}

type tuiTaskSnapshot struct {
	ID            string
	Hash          string
	Mode          string
	WordlistPath  string
	Status        TaskStatus
	Priority      int
	TotalKeyspace int64
	Estimated     int64
	Attempts      int
	MaxRetries    int
	Found         bool
	FoundPassword string
	FailureReason string
	UpdatedAt     time.Time
	CreatedAt     time.Time
	StartedAt     time.Time
	CompletedAt   time.Time
	CrackDuration time.Duration
	CrackRate     float64
}

type tuiLogEntry struct {
	At      time.Time
	Level   uiEventLevel
	Message string
}

type tuiLogStore struct {
	mu      sync.Mutex
	limit   int
	entries []tuiLogEntry
}

func newTUILogStore(limit int) *tuiLogStore {
	if limit <= 0 {
		limit = defaultTUILogLimit
	}
	return &tuiLogStore{limit: limit}
}

func (s *tuiLogStore) Write(p []byte) (int, error) {
	if s == nil {
		return len(p), nil
	}
	lines := strings.Split(strings.ReplaceAll(string(p), "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s.Append(parseLogLevel(line), line)
	}
	return len(p), nil
}

func (s *tuiLogStore) Append(level uiEventLevel, message string) {
	if s == nil || strings.TrimSpace(message) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, tuiLogEntry{
		At:      time.Now(),
		Level:   level,
		Message: strings.TrimSpace(message),
	})
	if overflow := len(s.entries) - s.limit; overflow > 0 {
		copy(s.entries, s.entries[overflow:])
		s.entries = s.entries[:s.limit]
	}
}

func (s *tuiLogStore) Entries() []tuiLogEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]tuiLogEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

func parseLogLevel(line string) uiEventLevel {
	switch {
	case strings.Contains(line, "[!]"):
		return uiEventError
	case strings.Contains(line, "[*]"):
		return uiEventWarn
	case strings.Contains(line, "[+]"):
		return uiEventSuccess
	default:
		return uiEventInfo
	}
}

func snapshotMasterTUI(state *masterState, ui *masterUI, logs *tuiLogStore, listenAddr string) masterTUISnapshot {
	now := time.Now()
	snapshot := masterTUISnapshot{
		Now:        now,
		ListenAddr: listenAddr,
		Logs:       logs.Entries(),
	}
	if ui != nil {
		ui.eventMu.Lock()
		snapshot.Event = ui.event
		ui.eventMu.Unlock()
	}
	if state == nil {
		return snapshot
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	snapshot.DispatchPaused = state.dispatchPaused
	snapshot.ShutdownRequested = state.shutdownRequested
	snapshot.ActiveChunks = len(state.activeChunks)
	snapshot.Summary = state.workloadSummaryLocked()

	workers := snapshotWorkersLocked(state, now)
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].id < workers[j].id
	})
	snapshot.WorkerCount = len(workers)
	for _, worker := range workers {
		snapshot.TotalRate += worker.avgRate
		if worker.health == "healthy" {
			snapshot.ActiveWorkers++
		}
		wsnap := tuiWorkerSnapshot{
			ID:            worker.id,
			CPUCores:      worker.cpuCores,
			LastSeen:      worker.lastSeen,
			Health:        worker.health,
			AvgRate:       worker.avgRate,
			LastChunkRate: worker.lastChunkRate,
			LastTaskID:    worker.lastTaskID,
			ActiveChunks:  worker.activeChunks,
			ActiveTaskID:  worker.activeTaskID,
		}
		if info := state.workers[worker.id]; info != nil {
			wsnap.TotalProcessed = info.TotalProcessed
			wsnap.CompletedChunks = info.CompletedChunks
			wsnap.CompletedTasks = info.CompletedTasks
			wsnap.LastTaskDuration = info.LastTaskDuration
			wsnap.Quarantined = info.Quarantined
			wsnap.QuarantineReason = info.QuarantineReason
		}
		snapshot.Workers = append(snapshot.Workers, wsnap)
	}

	tasks := make([]*Task, 0, len(state.tasks))
	for _, task := range state.tasks {
		if task != nil {
			tasks = append(tasks, task)
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})
	for _, task := range tasks {
		estimated := task.Completed
		if !task.isTerminal() {
			estimated += state.inflightProgressLocked(task.ID)
		}
		if estimated > task.TotalKeyspace {
			estimated = task.TotalKeyspace
		}
		foundPassword := ""
		if task.Found {
			foundPassword = security.SanitizeLogValue(task.FoundPassword, 300)
		}
		crackDuration := time.Duration(0)
		crackRate := 0.0
		if !task.StartedAt.IsZero() && !task.CompletedAt.IsZero() {
			crackDuration = task.CompletedAt.Sub(task.StartedAt)
			if crackDuration > 0 && task.Completed > 0 {
				crackRate = float64(task.Completed) / crackDuration.Seconds()
			}
		}
		snapshot.Tasks = append(snapshot.Tasks, tuiTaskSnapshot{
			ID:            task.ID,
			Hash:          task.Hash,
			Mode:          string(task.Mode),
			WordlistPath:  task.WordlistPath,
			Status:        task.Status,
			Priority:      task.Priority,
			TotalKeyspace: task.TotalKeyspace,
			Estimated:     estimated,
			Attempts:      task.Attempts,
			MaxRetries:    task.MaxRetries,
			Found:         task.Found,
			FoundPassword: foundPassword,
			FailureReason: security.SanitizeLogValue(task.FailureReason, 300),
			UpdatedAt:     task.UpdatedAt,
			CreatedAt:     task.CreatedAt,
			StartedAt:     task.StartedAt,
			CompletedAt:   task.CompletedAt,
			CrackDuration: crackDuration,
			CrackRate:     crackRate,
		})
	}

	return snapshot
}

func redactSensitiveOutput(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = redactSensitiveLine(line)
	}
	return strings.Join(lines, "\n")
}

var redactionKeys = []string{
	"cerberus_worker_token",
	"cerberus_admin_token",
	"worker_token",
	"admin_token",
	"authorization",
}

func redactSensitiveLine(line string) string {
	lower := strings.ToLower(line)
	for _, key := range redactionKeys {
		idx := strings.Index(lower, key)
		if idx < 0 {
			continue
		}
		eq := strings.Index(line[idx:], "=")
		if eq < 0 {
			continue
		}
		prefixEnd := idx + eq + 1
		return line[:prefixEnd] + "<redacted>"
	}
	return line
}
