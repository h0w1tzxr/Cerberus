package master

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	pb "cracker/cracker"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	Port        = ":50051"
	TaskTimeout = 10 * time.Second
)

type server struct {
	pb.UnimplementedCrackerServiceServer
	state *masterState
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
			chunkSize := s.state.suggestChunkSizeLocked(task, workerID, in.GetCpuCores())
			start, end, ok := s.state.allocateRangeLocked(task, chunkSize)
			if !ok {
				s.state.mu.Unlock()
				continue
			}
			if task.Status == TaskStatusApproved {
				task.Status = TaskStatusRunning
			}
			task.UpdatedAt = now

			chunk := s.state.assignChunkLocked(task, start, end, workerID, now)
			if task.NextIndex < task.TotalKeyspace || len(task.PendingRanges) > 0 {
				s.state.enqueueTaskLocked(task)
			}
			s.state.mu.Unlock()
			log.Printf("Assigned chunk %s for task %s to %s: %d-%d", chunk.TaskId, task.ID, workerID, start, end)
			return chunk, nil
		}

		if !s.state.allTasksTerminalLocked() {
			s.state.mu.Unlock()
			return &pb.TaskChunk{DispatchPaused: true}, nil
		}

		if len(s.state.activeChunks) == 0 && s.state.allTasksTerminalLocked() {
			s.state.mu.Unlock()
			return &pb.TaskChunk{NoMoreWork: true}, nil
		}

		s.state.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s *server) ReportProgress(ctx context.Context, in *pb.ProgressUpdate) (*pb.Ack, error) {
	if in == nil || in.GetChunkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "chunk id is required")
	}
	s.state.mu.Lock()
	s.state.updateWorkerLocked(in.GetWorkerId(), 0, time.Now())
	task, estimated := s.state.updateChunkProgressLocked(in.GetChunkId(), in.GetProcessed())
	if task != nil {
		logTaskProgress(task, estimated)
	}
	s.state.mu.Unlock()
	return &pb.Ack{Received: true}, nil
}

func (s *server) ReportResult(ctx context.Context, in *pb.CrackResult) (*pb.Ack, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	s.state.updateWorkerLocked(in.WorkerId, 0, time.Now())
	lease, ok := s.state.activeChunks[in.TaskId]
	if ok {
		delete(s.state.activeChunks, in.TaskId)
		delete(s.state.chunkProgress, in.TaskId)
		task := s.state.tasks[lease.taskID]
		if task != nil && !task.isTerminal() {
			now := time.Now()
			if in.ErrorMessage != "" {
				task.Status = TaskStatusFailed
				task.FailureReason = in.ErrorMessage
				task.Attempts++
				task.DispatchReady = false
				task.UpdatedAt = now
				s.state.clearTaskLeasesLocked(task.ID)
				log.Printf("Task %s failed by %s: %s", task.ID, in.WorkerId, in.ErrorMessage)
				return &pb.Ack{Received: true}, nil
			}

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
				log.Printf("Password found by %s for task %s: %s", in.WorkerId, task.ID, in.FoundPassword)
			} else {
				log.Printf("Chunk %s completed by %s with no match (%d-%d)", in.TaskId, in.WorkerId, lease.start, lease.end)
				if task.Completed >= task.TotalKeyspace && !s.state.hasActiveChunksLocked(task.ID) && len(task.PendingRanges) == 0 {
					task.Status = TaskStatusCompleted
					task.DispatchReady = false
					log.Printf("Task %s exhausted with no password found.", task.ID)
				}
			}
			s.state.updateWorkerRateLocked(in.WorkerId, lease.end-lease.start, now.Sub(lease.assignedAt))
			logTaskProgress(task, task.Completed)
		}
	}

	return &pb.Ack{Received: true}, nil
}

func runServer() error {
	lis, err := net.Listen("tcp", Port)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	state := newMasterState()
	s := grpc.NewServer()
	srv := &server{
		state: state,
	}
	admin := &adminServer{
		state: state,
	}

	pb.RegisterCrackerServiceServer(s, srv)
	pb.RegisterCrackerAdminServer(s, admin)
	log.Printf("Master Hash Cracker running on port %s", Port)
	log.Printf("Ready for Workers...")

	if err := s.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}
	return nil
}
