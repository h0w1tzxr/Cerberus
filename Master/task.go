package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type TaskStatus string

const (
	TaskStatusQueued    TaskStatus = "queued"
	TaskStatusReviewed  TaskStatus = "reviewed"
	TaskStatusApproved  TaskStatus = "approved"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCanceled  TaskStatus = "canceled"
)

type HashMode string

const (
	HashModeMD5    HashMode = "md5"
	HashModeSHA256 HashMode = "sha256"
)

type Task struct {
	ID            string
	Hash          string
	Mode          HashMode
	WordlistPath  string
	Status        TaskStatus
	Priority      int
	ChunkSize     int64
	TotalKeyspace int64
	NextIndex     int64
	Completed     int64
	PendingRanges []taskRange
	Attempts      int
	MaxRetries    int
	Found         bool
	FoundPassword string
	FailureReason string
	DispatchReady bool
	Paused        bool
	ReviewedBy    string
	ApprovedBy    string
	CanceledBy    string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type taskRange struct {
	start int64
	end   int64
}

func (t *Task) isTerminal() bool {
	return t.Status == TaskStatusCompleted || t.Status == TaskStatusFailed || t.Status == TaskStatusCanceled
}

func (t *Task) isDispatchable() bool {
	if t.isTerminal() {
		return false
	}
	if t.Paused {
		return false
	}
	if t.Status != TaskStatusApproved && t.Status != TaskStatusRunning {
		return false
	}
	return t.DispatchReady
}

func parseHashMode(value string) (HashMode, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case string(HashModeMD5):
		return HashModeMD5, nil
	case string(HashModeSHA256):
		return HashModeSHA256, nil
	case "":
		return "", errors.New("hash mode is required")
	default:
		return "", fmt.Errorf("invalid mode %q (expected md5 or sha256)", value)
	}
}
