package master

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"cracker/Common/console"
	pb "cracker/cracker"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	Port        = ":50051"
	TaskTimeout = 30 * time.Second
)

type server struct {
	pb.UnimplementedCrackerServiceServer
	state *masterState
	ui    *masterUI
}

func (s *server) RegisterWorker(ctx context.Context, in *pb.WorkerInfo) (*pb.Ack, error) {
	if in == nil || in.GetWorkerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "worker id is required")
	}
	s.state.mu.Lock()
	s.state.updateWorkerLocked(in.GetWorkerId(), in.GetCpuCores(), time.Now())
	s.state.mu.Unlock()
	return &pb.Ack{Received: true}, nil
}

func (s *server) GetTask(ctx context.Context, in *pb.WorkerInfo) (*pb.TaskChunk, error) {
	workerID := in.GetWorkerId()

	for {
		s.state.mu.Lock()
		now := time.Now()
		s.state.updateWorkerLocked(workerID, in.GetCpuCores(), now)

		if s.state.dispatchPaused {
			s.state.mu.Unlock()
			return &pb.TaskChunk{DispatchPaused: true}, nil
		}

		s.state.reclaimExpiredLeasesLocked(now, TaskTimeout)

		task := s.state.nextDispatchableTaskLocked(now)
		if task != nil {
			taskStarted := false
			chunkSize := s.state.suggestChunkSizeLocked(task, workerID, in.GetCpuCores())
			start, end, ok := s.state.allocateRangeLocked(task, chunkSize)
			if !ok {
				s.state.mu.Unlock()
				continue
			}
			if task.Status == TaskStatusApproved {
				task.Status = TaskStatusRunning
				taskStarted = true
				if task.StartedAt.IsZero() {
					task.StartedAt = now
				}
			}
			task.UpdatedAt = now

			chunk := s.state.assignChunkLocked(task, start, end, workerID, now)
			if task.NextIndex < task.TotalKeyspace || len(task.PendingRanges) > 0 {
				s.state.enqueueTaskLocked(task)
			}
			s.state.mu.Unlock()
			if taskStarted {
				logInfo("Task %s started (mode=%s keyspace=%d)", task.ID, task.Mode, task.TotalKeyspace)
			}
			return chunk, nil
		}

		s.state.mu.Unlock()
		return &pb.TaskChunk{NoMoreWork: true}, nil
	}
}

func (s *server) ReportProgress(ctx context.Context, in *pb.ProgressUpdate) (*pb.Ack, error) {
	if in == nil || in.GetChunkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "chunk id is required")
	}
	s.state.mu.Lock()
	now := time.Now()
	s.state.updateWorkerLocked(in.GetWorkerId(), 0, now)
	task, estimated := s.state.updateChunkProgressLocked(in.GetChunkId(), in.GetProcessed(), now)
	var taskID string
	var totalKeyspace int64
	if task != nil {
		taskID = task.ID
		totalKeyspace = task.TotalKeyspace
	}
	s.state.mu.Unlock()
	if s.ui != nil && taskID != "" {
		s.ui.UpdateProgress(taskID, in.GetChunkId(), estimated, totalKeyspace)
	}
	return &pb.Ack{Received: true}, nil
}

func (s *server) ReportResult(ctx context.Context, in *pb.CrackResult) (*pb.Ack, error) {
	if in == nil {
		return &pb.Ack{Received: true}, nil
	}
	now := time.Now()
	var (
		taskEnded         bool
		terminalMsg       string
		terminalLevel     uiEventLevel
		leaderboard       []leaderboardEntry
		terminalTask      string
		durationLabel     string
		taskDurationValue time.Duration
		outputWrite       *outputWrite
	)

	s.state.mu.Lock()
	s.state.updateWorkerLocked(in.WorkerId, 0, now)
	lease, ok := s.state.activeChunks[in.TaskId]
	if !ok {
		s.state.mu.Unlock()
		return &pb.Ack{Received: true}, nil
	}

	delete(s.state.activeChunks, in.TaskId)
	delete(s.state.chunkProgress, in.TaskId)

	task := s.state.tasks[lease.taskID]
	processed := in.GetProcessed()
	total := in.GetTotal()
	duration := time.Duration(in.GetDurationMs()) * time.Millisecond
	avgRate := in.GetAvgRate()
	if processed <= 0 {
		processed = lease.end - lease.start
	}
	if total <= 0 {
		total = lease.end - lease.start
	}
	if duration <= 0 {
		duration = now.Sub(lease.assignedAt)
	}
	if avgRate <= 0 && processed > 0 && duration > 0 {
		avgRate = float64(processed) / duration.Seconds()
	}
	overhead := now.Sub(lease.assignedAt) - duration
	if overhead < 0 {
		overhead = 0
	}

	info := s.state.workers[in.WorkerId]
	if info != nil {
		info.TotalProcessed += processed
		info.TotalDuration += duration
		info.TotalOverhead += overhead
		info.CompletedChunks++
		info.LastChunkID = in.TaskId
		info.LastChunkRate = avgRate
		info.LastChunkDuration = duration
		if task != nil {
			info.LastTaskID = task.ID
		}
	}

	if task != nil && !task.isTerminal() {
		if in.ErrorMessage != "" {
			task.Status = TaskStatusFailed
			task.FailureReason = in.ErrorMessage
			task.Attempts++
			task.DispatchReady = false
			task.UpdatedAt = now
			if task.CompletedAt.IsZero() {
				task.CompletedAt = now
			}
			s.state.clearTaskLeasesLocked(task.ID)
			taskEnded = true
			terminalLevel = uiEventError
			taskDurationValue = taskDuration(task, now)
			durationLabel = formatDuration(taskDurationValue)
			terminalMsg = fmt.Sprintf("Task %s failed by %s: %s (duration=%s)", task.ID, in.WorkerId, in.ErrorMessage, durationLabel)
		} else {
			task.Completed += lease.end - lease.start
			if task.Completed > task.TotalKeyspace {
				task.Completed = task.TotalKeyspace
			}
			task.UpdatedAt = now

			if in.Success {
				task.Found = true
				task.FoundPassword = in.FoundPassword
				task.Status = TaskStatusCompleted
				task.Completed = task.TotalKeyspace
				task.DispatchReady = false
				task.PendingRanges = nil
				s.state.clearTaskLeasesLocked(task.ID)
				taskEnded = true
				terminalLevel = uiEventSuccess
				if task.CompletedAt.IsZero() {
					task.CompletedAt = now
				}
				taskDurationValue = taskDuration(task, now)
				durationLabel = formatDuration(taskDurationValue)
				terminalMsg = fmt.Sprintf("Password found by %s for task %s: %s (duration=%s)", in.WorkerId, task.ID, in.FoundPassword, durationLabel)
			} else {
				if task.Completed >= task.TotalKeyspace && !s.state.hasActiveChunksLocked(task.ID) && len(task.PendingRanges) == 0 {
					task.Status = TaskStatusCompleted
					task.DispatchReady = false
					taskEnded = true
					terminalLevel = uiEventWarn
					if task.CompletedAt.IsZero() {
						task.CompletedAt = now
					}
					taskDurationValue = taskDuration(task, now)
					durationLabel = formatDuration(taskDurationValue)
					terminalMsg = fmt.Sprintf("Task %s exhausted with no password found. (duration=%s)", task.ID, durationLabel)
				}
			}
		}
		if s.ui != nil {
			s.ui.UpdateProgress(task.ID, in.TaskId, task.Completed, task.TotalKeyspace)
		}
		if taskEnded {
			terminalTask = task.ID
			if info != nil {
				info.CompletedTasks++
				info.TotalTaskDuration += taskDurationValue
				info.LastTaskDuration = taskDurationValue
			}
			outputWrite = s.state.recordTaskOutputLocked(task)
			if !s.state.leaderboardLogged && s.state.allTasksTerminalLocked() && len(s.state.activeChunks) == 0 {
				leaderboard = snapshotLeaderboardLocked(s.state)
				s.state.leaderboardLogged = true
			}
		}
	}

	if processed > 0 && duration > 0 {
		s.state.updateWorkerRateLocked(in.WorkerId, processed, duration)
	}
	s.state.mu.Unlock()

	if outputWrite != nil {
		if err := outputWrite.write(); err != nil {
			logWarn("Output write failed: %v", err)
		}
	}

	if taskEnded {
		switch terminalLevel {
		case uiEventSuccess:
			logSuccess("%s", terminalMsg)
		case uiEventError:
			logError("%s", terminalMsg)
		case uiEventWarn:
			logWarn("%s", terminalMsg)
		default:
			logInfo("%s", terminalMsg)
		}
		if s.ui != nil {
			s.ui.SetEvent(terminalLevel, terminalMsg)
		}
		if len(leaderboard) > 0 {
			logInfo("%s", formatLeaderboard(terminalTask, leaderboard))
		}
	}

	return &pb.Ack{Received: true}, nil
}

func runServer(interactive bool) error {
	lis, err := net.Listen("tcp", Port)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	state := newMasterState()
	renderer := console.NewStickyRenderer(os.Stdout)
	log.SetOutput(renderer)
	ui := newMasterUI(state)
	renderLoop := console.NewRenderLoop(renderer, masterRenderInterval, ui.StatusLine)
	renderLoop.Start()
	defer renderLoop.Stop()

	monitor := newWorkerMonitor(state)
	monitor.Start()
	defer monitor.Stop()

	s := grpc.NewServer()
	srv := &server{
		state: state,
		ui:    ui,
	}
	admin := &adminServer{
		state: state,
	}

	pb.RegisterCrackerServiceServer(s, srv)
	pb.RegisterCrackerAdminServer(s, admin)
	logInfo("Master Hash Cracker running on port %s", Port)
	logInfo("Ready for Workers...")

	if interactive {
		startInteractiveConsole(s, renderLoop, renderer, ui.StatusLine)
	}

	if err := s.Serve(lis); err != nil {
		if err == grpc.ErrServerStopped {
			return nil
		}
		return fmt.Errorf("failed to serve: %w", err)
	}
	return nil
}
