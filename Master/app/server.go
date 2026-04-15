package master

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"cracker/Common/console"
	"cracker/Common/security"
	pb "cracker/cracker"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

const (
	defaultMasterPort      = "50051"
	TaskTimeout            = 30 * time.Second
	maxRPCRecvMessageBytes = 1 << 20
)

type server struct {
	pb.UnimplementedCrackerServiceServer
	state *masterState
	ui    *masterUI
}

func (s *server) RegisterWorker(ctx context.Context, in *pb.WorkerInfo) (*pb.Ack, error) {
	workerID, cpuCores, err := validateWorkerInfo(in)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if boundWorker := workerIDFromContext(ctx); boundWorker != "" && boundWorker != workerID {
		return nil, status.Error(codes.PermissionDenied, "worker token is not valid for worker id")
	}
	s.state.mu.Lock()
	s.state.updateWorkerLocked(workerID, cpuCores, time.Now())
	s.state.mu.Unlock()
	return &pb.Ack{Received: true}, nil
}

func (s *server) GetTask(ctx context.Context, in *pb.WorkerInfo) (*pb.TaskChunk, error) {
	workerID, cpuCores, err := validateWorkerInfo(in)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if boundWorker := workerIDFromContext(ctx); boundWorker != "" && boundWorker != workerID {
		return nil, status.Error(codes.PermissionDenied, "worker token is not valid for worker id")
	}

	for {
		s.state.mu.Lock()
		now := time.Now()
		s.state.updateWorkerLocked(workerID, cpuCores, now)
		if s.state.isWorkerQuarantinedLocked(workerID) {
			s.state.mu.Unlock()
			return nil, status.Error(codes.PermissionDenied, "worker is quarantined")
		}

		if s.state.dispatchPaused {
			s.state.mu.Unlock()
			return &pb.TaskChunk{DispatchPaused: true}, nil
		}

		s.state.reclaimExpiredLeasesLocked(now, TaskTimeout)

		task := s.state.nextDispatchableTaskLocked(now)
		if task != nil {
			taskStarted := false
			chunkSize := s.state.suggestChunkSizeLocked(task, workerID, cpuCores)
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
				logDebug("Task %s started (mode=%s keyspace=%d)", task.ID, task.Mode, task.TotalKeyspace)
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
	workerID, err := validateWorkerID(in.GetWorkerId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if boundWorker := workerIDFromContext(ctx); boundWorker != "" && boundWorker != workerID {
		return nil, status.Error(codes.PermissionDenied, "worker token is not valid for worker id")
	}
	s.state.mu.Lock()
	now := time.Now()
	lease, ok := s.state.activeChunks[in.GetChunkId()]
	if !ok {
		s.state.mu.Unlock()
		return nil, status.Error(codes.NotFound, "chunk not found")
	}
	if lease.workerID != workerID {
		s.state.mu.Unlock()
		return nil, status.Error(codes.PermissionDenied, "worker does not own chunk")
	}
	s.state.updateWorkerLocked(workerID, 0, now)
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
		return nil, status.Error(codes.InvalidArgument, "crack result is required")
	}
	if strings.TrimSpace(in.GetTaskId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chunk id is required")
	}
	workerID, err := validateWorkerID(in.GetWorkerId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if boundWorker := workerIDFromContext(ctx); boundWorker != "" && boundWorker != workerID {
		return nil, status.Error(codes.PermissionDenied, "worker token is not valid for worker id")
	}
	now := time.Now()
	var (
		taskEnded         bool
		terminalMsg       string
		terminalLevel     uiEventLevel
		leaderboard       []leaderboardEntry
		sessionReport     string
		taskDurationValue time.Duration
		outputWrite       *outputWrite
	)

	s.state.mu.Lock()
	lease, ok := s.state.activeChunks[in.TaskId]
	if !ok {
		s.state.mu.Unlock()
		return nil, status.Error(codes.NotFound, "chunk not found")
	}
	if lease.workerID != workerID {
		s.state.mu.Unlock()
		return nil, status.Error(codes.PermissionDenied, "worker does not own chunk")
	}
	s.state.updateWorkerLocked(workerID, 0, now)

	delete(s.state.activeChunks, in.TaskId)
	delete(s.state.chunkProgress, in.TaskId)

	task := s.state.tasks[lease.taskID]
	leaseSize := lease.end - lease.start
	processed := clampInt64(in.GetProcessed(), 0, leaseSize)
	if processed <= 0 {
		processed = leaseSize
	}
	elapsed := now.Sub(lease.assignedAt)
	duration := time.Duration(in.GetDurationMs()) * time.Millisecond
	if duration <= 0 || duration > elapsed+time.Second {
		duration = elapsed
	}
	avgRate := 0.0
	if processed > 0 && duration > 0 {
		avgRate = float64(processed) / duration.Seconds()
	}
	overhead := elapsed - duration
	if overhead < 0 {
		overhead = 0
	}

	info := s.state.workers[workerID]
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
		errorMessage := security.SanitizeLogValue(in.ErrorMessage, 300)
		if errorMessage != "" {
			task.Attempts++
			task.UpdatedAt = now
			if task.Attempts >= task.MaxRetries {
				task.Status = TaskStatusFailed
				task.FailureReason = errorMessage
				task.DispatchReady = false
				if task.CompletedAt.IsZero() {
					task.CompletedAt = now
				}
				s.state.clearTaskLeasesLocked(task.ID)
				taskEnded = true
				terminalLevel = uiEventError
				taskDurationValue = taskDuration(task, now)
				terminalMsg = fmt.Sprintf("Task failed after worker errors: %s (%s)", task.ID, errorMessage)
			} else {
				task.PendingRanges = append(task.PendingRanges, taskRange{start: lease.start, end: lease.end})
				if task.isDispatchable() {
					s.state.enqueueTaskLocked(task)
				}
				terminalLevel = uiEventWarn
				terminalMsg = fmt.Sprintf("Chunk %s requeued after worker error from %s", in.TaskId, workerID)
			}
		} else if in.Success {
			foundPassword := sanitizeResultPassword(in.FoundPassword)
			if !verifyCandidateHash(task.Mode, foundPassword, task.Hash) {
				task.PendingRanges = append(task.PendingRanges, taskRange{start: lease.start, end: lease.end})
				if task.isDispatchable() {
					s.state.enqueueTaskLocked(task)
				}
				s.state.quarantineWorkerLocked(workerID, "invalid crack result", now)
				terminalLevel = uiEventWarn
				terminalMsg = fmt.Sprintf("Worker %s quarantined after invalid result for task %s", workerID, task.ID)
			} else {
				task.Completed += leaseSize
				if task.Completed > task.TotalKeyspace {
					task.Completed = task.TotalKeyspace
				}
				task.Found = true
				task.FoundPassword = foundPassword
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
				if security.RevealPasswords() {
					terminalMsg = fmt.Sprintf("Password Found: %s (task %s)", security.SanitizeLogValue(foundPassword, 300), task.ID)
				} else {
					terminalMsg = fmt.Sprintf("Password found for task %s", task.ID)
				}
			}
			task.UpdatedAt = now
		} else {
			task.Completed += leaseSize
			if task.Completed > task.TotalKeyspace {
				task.Completed = task.TotalKeyspace
			}
			task.UpdatedAt = now
			if task.Completed >= task.TotalKeyspace && !s.state.hasActiveChunksLocked(task.ID) && len(task.PendingRanges) == 0 {
				task.Status = TaskStatusCompleted
				task.DispatchReady = false
				taskEnded = true
				terminalLevel = uiEventInfo
				if task.CompletedAt.IsZero() {
					task.CompletedAt = now
				}
				taskDurationValue = taskDuration(task, now)
				terminalMsg = fmt.Sprintf("Task Complete: %s (no password)", task.ID)
			}
		}
		if s.ui != nil {
			s.ui.UpdateProgress(task.ID, in.TaskId, task.Completed, task.TotalKeyspace)
		}
		if taskEnded {
			if info != nil {
				info.CompletedTasks++
				info.TotalTaskDuration += taskDurationValue
				info.LastTaskDuration = taskDurationValue
			}
			outputWrite = s.state.recordTaskOutputLocked(task)
			if !s.state.leaderboardLogged && s.state.allTasksTerminalLocked() && len(s.state.activeChunks) == 0 {
				leaderboard = snapshotLeaderboardLocked(s.state)
				sessionReport = formatSessionReportLocked(s.state)
				s.state.leaderboardLogged = true
			}
		}
	}

	if processed > 0 && duration > 0 && !s.state.isWorkerQuarantinedLocked(workerID) {
		s.state.updateWorkerRateLocked(workerID, processed, duration)
	}
	s.state.mu.Unlock()

	if outputWrite != nil {
		if err := outputWrite.write(); err != nil {
			logWarn("Output write failed: %v", err)
		}
	}

	if terminalMsg != "" {
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
		if sessionReport != "" {
			logBlockInfo(sessionReport)
		}
		if len(leaderboard) > 0 {
			logBlockInfo(formatLeaderboard(leaderboard))
		}
	}

	return &pb.Ack{Received: true}, nil
}

func runServer(interactive bool, cfg serverConfig) error {
	if err := validateServerConfig(cfg); err != nil {
		return err
	}
	securityState, err := loadServerSecurity(cfg)
	if err != nil {
		return err
	}

	lis, err := net.Listen("tcp", cfg.listenAddr)
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

	if securityState.generatedCert {
		logWarn("Generated TLS cert at %s (set %s/%s to override)", securityState.certPath, security.EnvTLSCert, security.EnvTLSKey)
	}
	if securityState.generatedAdmin {
		logWarn("Generated admin token at %s (set %s to override)", securityState.adminTokenPath, security.EnvAdminToken)
	}
	if securityState.generatedWorker {
		logWarn("Generated local worker token for %s (issue per-worker tokens before remote deployment)", securityState.defaultWorkerID)
	}

	monitor := newWorkerMonitor(state)
	monitor.Start()
	defer monitor.Stop()

	authPolicy := newAuthPolicy(securityState.adminToken, securityState.workerTokenPath, cfg.allowRemoteAdmin)
	s := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(securityState.tlsConfig)),
		grpc.MaxRecvMsgSize(maxRPCRecvMessageBytes),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
			Time:              2 * time.Minute,
			Timeout:           20 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             30 * time.Second,
			PermitWithoutStream: false,
		}),
		grpc.UnaryInterceptor(unaryAuthInterceptor(authPolicy)),
		grpc.StreamInterceptor(streamAuthInterceptor(authPolicy)),
	)
	srv := &server{
		state: state,
		ui:    ui,
	}
	admin := &adminServer{
		state: state,
	}

	pb.RegisterCrackerServiceServer(s, srv)
	pb.RegisterCrackerAdminServer(s, admin)
	logInfo("Master Hash Cracker listening on %s", cfg.listenAddr)
	logInfo("Ready for Workers...")

	startShutdownWatcher(s, state, ui)

	if interactive {
		startInteractiveConsole(s, renderLoop, renderer, ui, state)
	}

	if err := s.Serve(lis); err != nil {
		if err == grpc.ErrServerStopped {
			return nil
		}
		return fmt.Errorf("failed to serve: %w", err)
	}
	return nil
}

func startShutdownWatcher(server *grpc.Server, state *masterState, ui *masterUI) {
	if server == nil || state == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			state.mu.Lock()
			requested := state.shutdownRequested
			active := len(state.activeChunks)
			state.mu.Unlock()
			if !requested {
				continue
			}
			if active == 0 {
				logInfo("Shutdown complete.")
				if ui != nil {
					ui.SetEvent(uiEventInfo, "Shutdown complete")
				}
				server.GracefulStop()
				return
			}
		}
	}()
}
