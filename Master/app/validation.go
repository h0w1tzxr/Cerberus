package master

import (
	"fmt"

	"cracker/Common/security"
	pb "cracker/cracker"
)

const (
	minCPUCores = 1
	maxCPUCores = 1024

	maxChunkSize   int64 = 10_000_000
	maxKeyspace    int64 = 1_000_000_000_000
	minPriority          = -1000
	maxPriority          = 1000
	maxTaskRetries       = 10
	maxBatchHashes       = 10_000
)

func validateWorkerInfo(info *pb.WorkerInfo) (string, int32, error) {
	if info == nil {
		return "", 0, fmt.Errorf("worker info is required")
	}
	workerID, err := validateWorkerID(info.GetWorkerId())
	if err != nil {
		return "", 0, err
	}
	return workerID, clampCPUCores(info.GetCpuCores()), nil
}

func validateWorkerID(value string) (string, error) {
	return security.ValidateWorkerID(value)
}

func clampCPUCores(cpuCores int32) int32 {
	if cpuCores < minCPUCores {
		return minCPUCores
	}
	if cpuCores > maxCPUCores {
		return maxCPUCores
	}
	return cpuCores
}

func validateHashForMode(value string, mode HashMode) (string, error) {
	hash := normalizeHash(value)
	var expectedLen int
	switch mode {
	case HashModeMD5:
		expectedLen = 32
	case HashModeSHA256:
		expectedLen = 64
	default:
		return "", fmt.Errorf("hash mode must be md5 or sha256")
	}
	if len(hash) != expectedLen {
		return "", fmt.Errorf("%s hash must be exactly %d hex characters", mode, expectedLen)
	}
	for _, ch := range hash {
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch >= 'a' && ch <= 'f' {
			continue
		}
		return "", fmt.Errorf("%s hash must contain only hex characters", mode)
	}
	return hash, nil
}

func validateTaskLimits(chunkSize, keyspace int64, usesWordlist bool, priority, maxRetries int) error {
	if chunkSize < 1 || chunkSize > maxChunkSize {
		return fmt.Errorf("chunk size must be between 1 and %d", maxChunkSize)
	}
	if !usesWordlist && (keyspace < 1 || keyspace > maxKeyspace) {
		return fmt.Errorf("keyspace must be between 1 and %d", maxKeyspace)
	}
	if priority < minPriority || priority > maxPriority {
		return fmt.Errorf("priority must be between %d and %d", minPriority, maxPriority)
	}
	if maxRetries < 0 || maxRetries > maxTaskRetries {
		return fmt.Errorf("max retries must be between 0 and %d", maxTaskRetries)
	}
	return nil
}

func clampInt64(value, min, max int64) int64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
