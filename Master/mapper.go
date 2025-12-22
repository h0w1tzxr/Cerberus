package main

import pb "cracker/cracker"

func hashModeToProto(mode HashMode) pb.HashMode {
	switch mode {
	case HashModeSHA256:
		return pb.HashMode_HASH_MODE_SHA256
	case HashModeMD5:
		return pb.HashMode_HASH_MODE_MD5
	default:
		return pb.HashMode_HASH_MODE_UNSPECIFIED
	}
}

func taskStatusToProto(status TaskStatus) pb.TaskStatus {
	switch status {
	case TaskStatusQueued:
		return pb.TaskStatus_TASK_STATUS_QUEUED
	case TaskStatusReviewed:
		return pb.TaskStatus_TASK_STATUS_REVIEWED
	case TaskStatusApproved:
		return pb.TaskStatus_TASK_STATUS_APPROVED
	case TaskStatusRunning:
		return pb.TaskStatus_TASK_STATUS_RUNNING
	case TaskStatusCompleted:
		return pb.TaskStatus_TASK_STATUS_COMPLETED
	case TaskStatusFailed:
		return pb.TaskStatus_TASK_STATUS_FAILED
	case TaskStatusCanceled:
		return pb.TaskStatus_TASK_STATUS_CANCELED
	default:
		return pb.TaskStatus_TASK_STATUS_UNSPECIFIED
	}
}

func taskToProto(task *Task) *pb.Task {
	if task == nil {
		return nil
	}
	return &pb.Task{
		Id:            task.ID,
		Hash:          task.Hash,
		Mode:          hashModeToProto(task.Mode),
		WordlistPath:  task.WordlistPath,
		Status:        taskStatusToProto(task.Status),
		Priority:      int32(task.Priority),
		ChunkSize:     task.ChunkSize,
		TotalKeyspace: task.TotalKeyspace,
		Completed:     task.Completed,
		NextIndex:     task.NextIndex,
		Attempts:      int32(task.Attempts),
		MaxRetries:    int32(task.MaxRetries),
		Found:         task.Found,
		FoundPassword: task.FoundPassword,
		FailureReason: task.FailureReason,
		DispatchReady: task.DispatchReady,
		Paused:        task.Paused,
		ReviewedBy:    task.ReviewedBy,
		ApprovedBy:    task.ApprovedBy,
		CanceledBy:    task.CanceledBy,
		CreatedAtUnix: task.CreatedAt.Unix(),
		UpdatedAtUnix: task.UpdatedAt.Unix(),
	}
}
