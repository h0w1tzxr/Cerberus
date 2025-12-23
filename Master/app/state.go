package master

import (
	"container/heap"
	"fmt"
	"sort"
	"sync"
	"time"

	"cracker/Common/wordlist"
	pb "cracker/cracker"
)

const (
	DefaultChunkSize    int64 = 1000
	DefaultKeyspace     int64 = 100000
	DefaultMaxRetries         = 3
	WorkerStaleAfter          = 30 * time.Second
	TargetChunkDuration       = 8 * time.Second
	MinChunkSize        int64 = 1
	MaxChunkFactor            = 8
)

type taskLease struct {
	start          int64
	end            int64
	workerID       string
	taskID         string
	assignedAt     time.Time
	expiresAt      time.Time
	lastProgressAt time.Time
}

type masterState struct {
	mu             sync.Mutex
	tasks          map[string]*Task
	queue          taskQueue
	activeChunks   map[string]taskLease
	chunkProgress  map[string]int64
	workers        map[string]*workerInfo
	dispatchPaused bool
	nextTaskSeq    int64
	nextQueueSeq   int64
	nextChunkSeq   int64
	nextBatchSeq   int64
	leaderboardLogged bool
	wordlists      *wordlist.Cache
	batchOutputs   map[string]*batchOutput
}

func newMasterState() *masterState {
	state := &masterState{
		tasks:         make(map[string]*Task),
		queue:         make(taskQueue, 0),
		activeChunks:  make(map[string]taskLease),
		chunkProgress: make(map[string]int64),
		workers:       make(map[string]*workerInfo),
		wordlists:     wordlist.NewCache(wordlist.DefaultIndexStride, wordlist.DefaultMaxLineBytes),
		batchOutputs:  make(map[string]*batchOutput),
	}
	heap.Init(&state.queue)
	return state
}

func (s *masterState) addTask(hash string, mode HashMode, wordlistPath, outputPath, batchID string, batchIndex, batchTotal int, chunkSize, totalKeyspace int64, priority, maxRetries int) *Task {
	s.nextTaskSeq++
	now := time.Now()
	taskID := fmt.Sprintf("task-%d", s.nextTaskSeq)
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}
	task := &Task{
		ID:            taskID,
		Hash:          hash,
		Mode:          mode,
		WordlistPath:  wordlistPath,
		OutputPath:    outputPath,
		Status:        TaskStatusApproved,
		Priority:      priority,
		ChunkSize:     chunkSize,
		TotalKeyspace: totalKeyspace,
		MaxRetries:    maxRetries,
		BatchID:       batchID,
		BatchIndex:    batchIndex,
		BatchTotal:    batchTotal,
		ReviewedBy:    "auto",
		ApprovedBy:    "auto",
		DispatchReady: true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.tasks[taskID] = task
	s.leaderboardLogged = false
	if task.isDispatchable() {
		s.enqueueTaskLocked(task)
	}
	return task
}

func (s *masterState) enqueueTaskLocked(task *Task) {
	s.nextQueueSeq++
	heap.Push(&s.queue, &taskQueueItem{
		taskID:    task.ID,
		priority:  task.Priority,
		createdAt: task.CreatedAt,
		seq:       s.nextQueueSeq,
	})
}

func (s *masterState) nextDispatchableTaskLocked(now time.Time) *Task {
	for s.queue.Len() > 0 {
		item := heap.Pop(&s.queue).(*taskQueueItem)
		task := s.tasks[item.taskID]
		if task == nil {
			continue
		}
		if task.isTerminal() {
			continue
		}
		if task.Priority != item.priority {
			continue
		}
		if !task.isDispatchable() {
			continue
		}
		if task.NextIndex >= task.TotalKeyspace && len(task.PendingRanges) == 0 {
			if task.Completed >= task.TotalKeyspace && !s.hasActiveChunksLocked(task.ID) {
				task.Status = TaskStatusCompleted
				task.UpdatedAt = now
				if task.CompletedAt.IsZero() {
					task.CompletedAt = now
				}
			}
			continue
		}
		return task
	}
	return nil
}

func (s *masterState) assignChunkLocked(task *Task, start, end int64, workerID string, now time.Time) *pb.TaskChunk {
	s.nextChunkSeq++
	chunkID := fmt.Sprintf("chunk-%s-%d", workerID, s.nextChunkSeq)
	s.activeChunks[chunkID] = taskLease{
		start:          start,
		end:            end,
		workerID:       workerID,
		taskID:         task.ID,
		assignedAt:     now,
		expiresAt:      now.Add(TaskTimeout),
		lastProgressAt: now,
	}
	s.chunkProgress[chunkID] = 0

	return &pb.TaskChunk{
		TaskId:        chunkID,
		StartIndex:    start,
		EndIndex:      end,
		TargetHash:    task.Hash,
		Mode:          hashModeToProto(task.Mode),
		WordlistPath:  task.WordlistPath,
		TotalKeyspace: task.TotalKeyspace,
	}
}

func (s *masterState) reclaimExpiredLeasesLocked(now time.Time, timeout time.Duration) {
	for chunkID, lease := range s.activeChunks {
		expiresAt := lease.expiresAt
		if expiresAt.IsZero() {
			expiresAt = lease.assignedAt.Add(timeout)
		}
		if now.Before(expiresAt) {
			continue
		}
		delete(s.activeChunks, chunkID)
		delete(s.chunkProgress, chunkID)
		task := s.tasks[lease.taskID]
		if task == nil || task.isTerminal() {
			continue
		}
		task.PendingRanges = append(task.PendingRanges, taskRange{start: lease.start, end: lease.end})
		task.UpdatedAt = now
		if task.isDispatchable() {
			s.enqueueTaskLocked(task)
		}
	}
}

func (s *masterState) allocateRangeLocked(task *Task, chunkSize int64) (int64, int64, bool) {
	if len(task.PendingRanges) > 0 {
		pending := task.PendingRanges[0]
		task.PendingRanges = task.PendingRanges[1:]
		return pending.start, pending.end, true
	}
	if task.NextIndex >= task.TotalKeyspace {
		return 0, 0, false
	}
	start := task.NextIndex
	if chunkSize <= 0 {
		chunkSize = task.ChunkSize
	}
	end := start + chunkSize
	if end > task.TotalKeyspace {
		end = task.TotalKeyspace
	}
	task.NextIndex = end
	return start, end, true
}

func (s *masterState) hasActiveChunksLocked(taskID string) bool {
	for _, lease := range s.activeChunks {
		if lease.taskID == taskID {
			return true
		}
	}
	return false
}

func (s *masterState) activeChunkCountLocked(taskID string) int {
	count := 0
	for _, lease := range s.activeChunks {
		if lease.taskID == taskID {
			count++
		}
	}
	return count
}

func (s *masterState) activeChunkCountByWorkerLocked(workerID string) int {
	count := 0
	for _, lease := range s.activeChunks {
		if lease.workerID == workerID {
			count++
		}
	}
	return count
}

func (s *masterState) listTasksLocked(filter map[TaskStatus]bool) []*Task {
	tasks := make([]*Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		if task == nil {
			continue
		}
		if len(filter) > 0 && !filter[task.Status] {
			continue
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})
	return tasks
}

func (s *masterState) updateWorkerLocked(workerID string, cpuCores int32, now time.Time) {
	info := s.workers[workerID]
	if info == nil {
		info = &workerInfo{ID: workerID}
		s.workers[workerID] = info
	}
	if cpuCores > 0 && (info.CPUCores == 0 || cpuCores > info.CPUCores) {
		info.CPUCores = cpuCores
	}
	info.LastSeen = now
}

func (s *masterState) updateWorkerRateLocked(workerID string, processed int64, duration time.Duration) {
	if processed <= 0 || duration <= 0 {
		return
	}
	info := s.workers[workerID]
	if info == nil {
		return
	}
	rate := float64(processed) / duration.Seconds()
	if info.AvgRate == 0 {
		info.AvgRate = rate
		return
	}
	const alpha = 0.3
	info.AvgRate = alpha*rate + (1-alpha)*info.AvgRate
}

func (s *masterState) suggestChunkSizeLocked(task *Task, workerID string, cpuCores int32) int64 {
	if task == nil {
		return DefaultChunkSize
	}
	base := task.ChunkSize
	if base <= 0 {
		base = DefaultChunkSize
	}
	info := s.workers[workerID]
	if info != nil && info.AvgRate > 0 {
		desired := int64(info.AvgRate * TargetChunkDuration.Seconds())
		return clampChunkSize(desired, base, cpuCores)
	}
	return clampChunkSize(base, base, cpuCores)
}

func clampChunkSize(desired, base int64, cpuCores int32) int64 {
	if desired <= 0 {
		desired = base
	}
	cores := int64(cpuCores)
	if cores < 1 {
		cores = 1
	}
	minChunk := base / 2
	if minChunk < MinChunkSize {
		minChunk = MinChunkSize
	}
	maxChunk := base * cores * MaxChunkFactor
	if maxChunk < minChunk {
		maxChunk = minChunk
	}
	if desired < minChunk {
		return minChunk
	}
	if desired > maxChunk {
		return maxChunk
	}
	return desired
}

func (s *masterState) updateChunkProgressLocked(chunkID string, processed int64, now time.Time) (*Task, int64) {
	lease, ok := s.activeChunks[chunkID]
	if !ok {
		return nil, 0
	}
	size := lease.end - lease.start
	if processed < 0 {
		processed = 0
	}
	if processed > size {
		processed = size
	}
	s.chunkProgress[chunkID] = processed
	lease.expiresAt = now.Add(TaskTimeout)
	lease.lastProgressAt = now
	s.activeChunks[chunkID] = lease
	s.updateWorkerRateLocked(lease.workerID, processed, now.Sub(lease.assignedAt))
	task := s.tasks[lease.taskID]
	if task == nil {
		return nil, 0
	}
	estimated := task.Completed + s.inflightProgressLocked(task.ID)
	if estimated > task.TotalKeyspace {
		estimated = task.TotalKeyspace
	}
	return task, estimated
}

func (s *masterState) inflightProgressLocked(taskID string) int64 {
	var total int64
	for chunkID, lease := range s.activeChunks {
		if lease.taskID != taskID {
			continue
		}
		total += s.chunkProgress[chunkID]
	}
	return total
}

func (s *masterState) workerStatusesLocked(now time.Time) []workerStatus {
	statuses := make([]workerStatus, 0, len(s.workers))
	for _, worker := range s.workers {
		inflight := s.activeChunkCountByWorkerLocked(worker.ID)
		statuses = append(statuses, workerStatus{
			ID:       worker.ID,
			CPUCores: worker.CPUCores,
			LastSeen: worker.LastSeen,
			Inflight: inflight,
			Health:   workerHealth(now, worker.LastSeen, WorkerStaleAfter),
		})
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].LastSeen.After(statuses[j].LastSeen)
	})
	return statuses
}

func (s *masterState) clearTaskLeasesLocked(taskID string) {
	for chunkID, lease := range s.activeChunks {
		if lease.taskID == taskID {
			delete(s.activeChunks, chunkID)
			delete(s.chunkProgress, chunkID)
		}
	}
}

func (s *masterState) allTasksTerminalLocked() bool {
	if len(s.tasks) == 0 {
		return true
	}
	for _, task := range s.tasks {
		if task == nil {
			continue
		}
		if !task.isTerminal() {
			return false
		}
	}
	return true
}
