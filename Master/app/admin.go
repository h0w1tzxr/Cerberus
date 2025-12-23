package master

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	pb "cracker/cracker"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type adminServer struct {
	pb.UnimplementedCrackerAdminServer
	state *masterState
}

func (a *adminServer) AddTask(ctx context.Context, spec *pb.TaskSpec) (*pb.Task, error) {
	if spec == nil {
		return nil, status.Error(codes.InvalidArgument, "task spec is required")
	}
	hash := normalizeHash(spec.Hash)
	if hash == "" {
		return nil, status.Error(codes.InvalidArgument, "hash is required")
	}
	mode, err := hashModeFromProto(spec.Mode)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	wordlistPath := strings.TrimSpace(spec.WordlistPath)
	outputPath := strings.TrimSpace(spec.OutputPath)
	totalKeyspace := spec.Keyspace
	if wordlistPath != "" {
		index, err := a.state.wordlists.Get(wordlistPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		totalKeyspace = index.LineCount
	}
	if totalKeyspace <= 0 {
		return nil, status.Error(codes.InvalidArgument, "keyspace must be greater than zero")
	}

	chunkSize := spec.ChunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	priority := int(spec.Priority)
	maxRetries := int(spec.MaxRetries)

	a.state.mu.Lock()
	batchID := ""
	batchTotal := 0
	if outputPath != "" {
		batchID = a.state.newBatchIDLocked()
		batchTotal = 1
		a.state.ensureBatchOutputLocked(batchID, outputPath, batchTotal)
	}
	task := a.state.addTask(hash, mode, wordlistPath, outputPath, batchID, 0, batchTotal, chunkSize, totalKeyspace, priority, maxRetries)
	a.state.mu.Unlock()

	return taskToProto(task), nil
}

func (a *adminServer) AddTaskBatch(ctx context.Context, spec *pb.TaskBatchSpec) (*pb.TaskListResponse, error) {
	if spec == nil {
		return nil, status.Error(codes.InvalidArgument, "task batch spec is required")
	}
	if len(spec.Hashes) == 0 {
		return nil, status.Error(codes.InvalidArgument, "hashes are required")
	}
	mode, err := hashModeFromProto(spec.Mode)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	wordlistPath := strings.TrimSpace(spec.WordlistPath)
	outputPath := strings.TrimSpace(spec.OutputPath)
	totalKeyspace := spec.Keyspace
	if wordlistPath != "" {
		index, err := a.state.wordlists.Get(wordlistPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		totalKeyspace = index.LineCount
	}
	if totalKeyspace <= 0 {
		return nil, status.Error(codes.InvalidArgument, "keyspace must be greater than zero")
	}

	chunkSize := spec.ChunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	priority := int(spec.Priority)
	maxRetries := int(spec.MaxRetries)

	a.state.mu.Lock()
	validHashes := make([]string, 0, len(spec.Hashes))
	for _, hash := range spec.Hashes {
		hash = normalizeHash(hash)
		if hash == "" {
			continue
		}
		validHashes = append(validHashes, hash)
	}
	tasks := make([]*pb.Task, 0, len(validHashes))
	batchID := ""
	batchTotal := 0
	if outputPath != "" && len(validHashes) > 0 {
		batchID = a.state.newBatchIDLocked()
		batchTotal = len(validHashes)
		a.state.ensureBatchOutputLocked(batchID, outputPath, batchTotal)
	}
	for i, hash := range validHashes {
		task := a.state.addTask(hash, mode, wordlistPath, outputPath, batchID, i, batchTotal, chunkSize, totalKeyspace, priority, maxRetries)
		tasks = append(tasks, taskToProto(task))
	}
	a.state.mu.Unlock()

	if len(tasks) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no valid hashes provided")
	}
	return &pb.TaskListResponse{Tasks: tasks}, nil
}

func (a *adminServer) ApplyTaskAction(ctx context.Context, req *pb.TaskActionRequest) (*pb.Task, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "task action request is required")
	}
	taskID := strings.TrimSpace(req.TaskId)
	if taskID == "" {
		return nil, status.Error(codes.InvalidArgument, "task id is required")
	}
	operator := strings.TrimSpace(req.Operator)

	a.state.mu.Lock()
	var (
		logMsg      string
		leaderboard []leaderboardEntry
		outputWrite *outputWrite
	)
	defer func() {
		a.state.mu.Unlock()
		if outputWrite != nil {
			if err := outputWrite.write(); err != nil {
				logWarn("Output write failed: %v", err)
			}
		}
		if logMsg != "" {
			logWarn("%s", logMsg)
			if len(leaderboard) > 0 {
				logBlockInfo(formatLeaderboard(leaderboard))
			}
		}
	}()

	task := a.state.tasks[taskID]
	if task == nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	now := time.Now()
	switch req.Action {
	case pb.TaskAction_TASK_ACTION_REVIEW:
		if task.Status != TaskStatusQueued {
			return nil, status.Errorf(codes.FailedPrecondition, "task %s is not queued", task.ID)
		}
		task.Status = TaskStatusReviewed
		task.ReviewedBy = operator
		task.UpdatedAt = now
	case pb.TaskAction_TASK_ACTION_APPROVE:
		if task.Status != TaskStatusReviewed {
			return nil, status.Errorf(codes.FailedPrecondition, "task %s is not reviewed", task.ID)
		}
		task.Status = TaskStatusApproved
		task.ApprovedBy = operator
		task.UpdatedAt = now
	case pb.TaskAction_TASK_ACTION_DISPATCH:
		if task.Status != TaskStatusApproved && task.Status != TaskStatusRunning {
			return nil, status.Errorf(codes.FailedPrecondition, "task %s is not approved", task.ID)
		}
		task.DispatchReady = true
		task.UpdatedAt = now
		a.state.enqueueTaskLocked(task)
	case pb.TaskAction_TASK_ACTION_CANCEL:
		if task.isTerminal() {
			return nil, status.Errorf(codes.FailedPrecondition, "task %s is already terminal", task.ID)
		}
		task.Status = TaskStatusCanceled
		task.DispatchReady = false
		task.Paused = false
		task.CanceledBy = operator
		task.FailureReason = strings.TrimSpace(req.Reason)
		task.PendingRanges = nil
		task.UpdatedAt = now
		if task.CompletedAt.IsZero() {
			task.CompletedAt = now
		}
		a.state.clearTaskLeasesLocked(task.ID)
		outputWrite = a.state.recordTaskOutputLocked(task)
		logMsg = fmt.Sprintf("Task %s canceled by %s", task.ID, operator)
		if !a.state.leaderboardLogged && a.state.allTasksTerminalLocked() && len(a.state.activeChunks) == 0 {
			leaderboard = snapshotLeaderboardLocked(a.state)
			a.state.leaderboardLogged = true
		}
	case pb.TaskAction_TASK_ACTION_RETRY:
		if task.Status != TaskStatusFailed {
			return nil, status.Errorf(codes.FailedPrecondition, "task %s is not failed", task.ID)
		}
		task.Status = TaskStatusApproved
		task.DispatchReady = false
		task.Paused = false
		if operator != "" {
			task.ApprovedBy = operator
		}
		task.FailureReason = ""
		task.Found = false
		task.FoundPassword = ""
		task.Completed = 0
		task.NextIndex = 0
		task.PendingRanges = nil
		task.UpdatedAt = now
		task.StartedAt = time.Time{}
		task.CompletedAt = time.Time{}
		a.state.leaderboardLogged = false
	case pb.TaskAction_TASK_ACTION_PAUSE:
		if task.isTerminal() {
			return nil, status.Errorf(codes.FailedPrecondition, "task %s is already terminal", task.ID)
		}
		task.Paused = true
		task.UpdatedAt = now
	case pb.TaskAction_TASK_ACTION_RESUME:
		if task.isTerminal() {
			return nil, status.Errorf(codes.FailedPrecondition, "task %s is already terminal", task.ID)
		}
		task.Paused = false
		task.UpdatedAt = now
		if task.isDispatchable() {
			a.state.enqueueTaskLocked(task)
		}
	case pb.TaskAction_TASK_ACTION_SET_PRIORITY:
		task.Priority = int(req.Priority)
		task.UpdatedAt = now
		if task.isDispatchable() {
			a.state.enqueueTaskLocked(task)
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown task action")
	}

	return taskToProto(task), nil
}

func (a *adminServer) GetTask(ctx context.Context, req *pb.TaskActionRequest) (*pb.Task, error) {
	if req == nil || strings.TrimSpace(req.TaskId) == "" {
		return nil, status.Error(codes.InvalidArgument, "task id is required")
	}
	a.state.mu.Lock()
	task := a.state.tasks[req.TaskId]
	a.state.mu.Unlock()
	if task == nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}
	return taskToProto(task), nil
}

func (a *adminServer) ListTasks(ctx context.Context, req *pb.TaskListRequest) (*pb.TaskListResponse, error) {
	filter := make(map[TaskStatus]bool)
	if req != nil {
		for _, statusValue := range req.StatusFilter {
			taskStatus, err := taskStatusFromProto(statusValue)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			filter[taskStatus] = true
		}
	}

	a.state.mu.Lock()
	tasks := a.state.listTasksLocked(filter)
	a.state.mu.Unlock()

	resp := &pb.TaskListResponse{Tasks: make([]*pb.Task, 0, len(tasks))}
	for _, task := range tasks {
		resp.Tasks = append(resp.Tasks, taskToProto(task))
	}
	return resp, nil
}

func (a *adminServer) SetDispatchPaused(ctx context.Context, req *pb.DispatchControlRequest) (*pb.DispatchStatus, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "dispatch control request is required")
	}
	a.state.mu.Lock()
	a.state.dispatchPaused = req.Paused
	a.state.mu.Unlock()
	return &pb.DispatchStatus{Paused: req.Paused}, nil
}

func (a *adminServer) ListWorkers(ctx context.Context, req *pb.WorkerListRequest) (*pb.WorkerListResponse, error) {
	a.state.mu.Lock()
	statuses := a.state.workerStatusesLocked(time.Now())
	a.state.mu.Unlock()

	resp := &pb.WorkerListResponse{Workers: make([]*pb.WorkerStatus, 0, len(statuses))}
	for _, status := range statuses {
		resp.Workers = append(resp.Workers, &pb.WorkerStatus{
			WorkerId:     status.ID,
			CpuCores:     status.CPUCores,
			LastSeenUnix: status.LastSeen.Unix(),
			Inflight:     int32(status.Inflight),
			Health:       status.Health,
		})
	}
	return resp, nil
}

func hashModeFromProto(mode pb.HashMode) (HashMode, error) {
	switch mode {
	case pb.HashMode_HASH_MODE_MD5:
		return HashModeMD5, nil
	case pb.HashMode_HASH_MODE_SHA256:
		return HashModeSHA256, nil
	default:
		return "", errors.New("hash mode must be md5 or sha256")
	}
}

func taskStatusFromProto(status pb.TaskStatus) (TaskStatus, error) {
	switch status {
	case pb.TaskStatus_TASK_STATUS_QUEUED:
		return TaskStatusQueued, nil
	case pb.TaskStatus_TASK_STATUS_REVIEWED:
		return TaskStatusReviewed, nil
	case pb.TaskStatus_TASK_STATUS_APPROVED:
		return TaskStatusApproved, nil
	case pb.TaskStatus_TASK_STATUS_RUNNING:
		return TaskStatusRunning, nil
	case pb.TaskStatus_TASK_STATUS_COMPLETED:
		return TaskStatusCompleted, nil
	case pb.TaskStatus_TASK_STATUS_FAILED:
		return TaskStatusFailed, nil
	case pb.TaskStatus_TASK_STATUS_CANCELED:
		return TaskStatusCanceled, nil
	case pb.TaskStatus_TASK_STATUS_UNSPECIFIED:
		return "", errors.New("task status must be explicit")
	default:
		return "", fmt.Errorf("unsupported task status %v", status)
	}
}
