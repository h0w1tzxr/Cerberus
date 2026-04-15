package master

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cracker/Common/security"
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
	hash, err = validateHashForMode(hash, mode)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	wordlistPath := strings.TrimSpace(spec.WordlistPath)
	masterWordlistPath := ""
	if wordlistPath != "" {
		var err error
		masterWordlistPath, err = security.ResolveWordlistPath(wordlistPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		wordlistPath, err = security.DataPathReference(masterWordlistPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	outputPath := strings.TrimSpace(spec.OutputPath)
	if outputPath != "" {
		var err error
		outputPath, err = security.ResolveOutputPath(outputPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	totalKeyspace := spec.Keyspace
	if masterWordlistPath != "" {
		index, err := a.state.wordlists.Get(masterWordlistPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		totalKeyspace = index.LineCount
	}
	chunkSize := spec.ChunkSize
	priority := int(spec.Priority)
	maxRetries := int(spec.MaxRetries)
	if err := validateTaskLimits(chunkSize, totalKeyspace, masterWordlistPath != "", priority, maxRetries); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	a.state.mu.Lock()
	batchID := ""
	batchTotal := 0
	if outputPath != "" {
		batchID = a.state.newBatchIDLocked()
		batchTotal = 1
		a.state.ensureBatchOutputLocked(batchID, outputPath, batchTotal)
	}
	task := a.state.addTask(hash, mode, wordlistPath, masterWordlistPath, outputPath, batchID, 0, batchTotal, chunkSize, totalKeyspace, priority, maxRetries)
	protoTask := taskToProto(task)
	a.state.mu.Unlock()

	return protoTask, nil
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
	masterWordlistPath := ""
	if wordlistPath != "" {
		var err error
		masterWordlistPath, err = security.ResolveWordlistPath(wordlistPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		wordlistPath, err = security.DataPathReference(masterWordlistPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	outputPath := strings.TrimSpace(spec.OutputPath)
	if outputPath != "" {
		var err error
		outputPath, err = security.ResolveOutputPath(outputPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	totalKeyspace := spec.Keyspace
	if masterWordlistPath != "" {
		index, err := a.state.wordlists.Get(masterWordlistPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		totalKeyspace = index.LineCount
	}
	chunkSize := spec.ChunkSize
	priority := int(spec.Priority)
	maxRetries := int(spec.MaxRetries)
	if err := validateTaskLimits(chunkSize, totalKeyspace, masterWordlistPath != "", priority, maxRetries); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	validHashes := make([]string, 0, len(spec.Hashes))
	for i, hash := range spec.Hashes {
		hash = normalizeHash(hash)
		if hash == "" {
			continue
		}
		hash, err = validateHashForMode(hash, mode)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "hash %d: %s", i+1, err.Error())
		}
		validHashes = append(validHashes, hash)
	}
	if len(validHashes) > maxBatchHashes {
		return nil, status.Errorf(codes.InvalidArgument, "batch hashes must be at most %d", maxBatchHashes)
	}
	if len(validHashes) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no valid hashes provided")
	}

	a.state.mu.Lock()
	tasks := make([]*pb.Task, 0, len(validHashes))
	batchID := ""
	batchTotal := 0
	if outputPath != "" && len(validHashes) > 0 {
		batchID = a.state.newBatchIDLocked()
		batchTotal = len(validHashes)
		a.state.ensureBatchOutputLocked(batchID, outputPath, batchTotal)
	}
	for i, hash := range validHashes {
		task := a.state.addTask(hash, mode, wordlistPath, masterWordlistPath, outputPath, batchID, i, batchTotal, chunkSize, totalKeyspace, priority, maxRetries)
		tasks = append(tasks, taskToProto(task))
	}
	a.state.mu.Unlock()

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
	operator := security.SanitizeLogValue(req.Operator, 128)

	a.state.mu.Lock()
	var (
		logMsg        string
		leaderboard   []leaderboardEntry
		sessionReport string
		outputWrite   *outputWrite
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
			if sessionReport != "" {
				logBlockInfo(sessionReport)
			}
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
		task.FailureReason = security.SanitizeLogValue(req.Reason, 300)
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
			sessionReport = formatSessionReportLocked(a.state)
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
		if int(req.Priority) < minPriority || int(req.Priority) > maxPriority {
			return nil, status.Errorf(codes.InvalidArgument, "priority must be between %d and %d", minPriority, maxPriority)
		}
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
	if task == nil {
		a.state.mu.Unlock()
		return nil, status.Error(codes.NotFound, "task not found")
	}
	protoTask := taskToProto(task)
	a.state.mu.Unlock()
	return protoTask, nil
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
	resp := &pb.TaskListResponse{Tasks: make([]*pb.Task, 0, len(tasks))}
	for _, task := range tasks {
		resp.Tasks = append(resp.Tasks, taskToProto(task))
	}
	a.state.mu.Unlock()
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
