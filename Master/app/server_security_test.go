package master

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	pb "cracker/cracker"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkerReportMustOwnChunk(t *testing.T) {
	srv, chunk := testServerWithLease(t, "worker-a")

	_, err := srv.ReportProgress(context.Background(), &pb.ProgressUpdate{
		ChunkId:   chunk.TaskId,
		WorkerId:  "worker-b",
		Processed: 1,
	})
	assertStatusCode(t, err, codes.PermissionDenied)

	_, err = srv.ReportResult(context.Background(), &pb.CrackResult{
		TaskId:   chunk.TaskId,
		WorkerId: "worker-b",
		Success:  true,
	})
	assertStatusCode(t, err, codes.PermissionDenied)
}

func TestUnknownChunkResultIsRejected(t *testing.T) {
	srv := &server{state: newMasterState()}
	_, err := srv.ReportResult(context.Background(), &pb.CrackResult{
		TaskId:   "chunk-missing",
		WorkerId: "worker-a",
	})
	assertStatusCode(t, err, codes.NotFound)
}

func TestMasterVerifiesWorkerSuccessBeforeCompletingTask(t *testing.T) {
	srv, chunk := testServerWithLease(t, "worker-a")

	_, err := srv.ReportResult(context.Background(), &pb.CrackResult{
		TaskId:        chunk.TaskId,
		WorkerId:      "worker-a",
		Success:       true,
		FoundPassword: "wrong",
		Processed:     10,
	})
	if err != nil {
		t.Fatalf("ReportResult invalid success returned error: %v", err)
	}

	srv.state.mu.Lock()
	task := srv.state.tasks["task-1"]
	quarantined := srv.state.isWorkerQuarantinedLocked("worker-a")
	pending := len(task.PendingRanges)
	completed := task.Status == TaskStatusCompleted
	srv.state.mu.Unlock()

	if !quarantined {
		t.Fatal("worker was not quarantined after invalid result")
	}
	if pending == 0 {
		t.Fatal("chunk was not requeued after invalid result")
	}
	if completed {
		t.Fatal("task completed on unverified worker result")
	}
}

func TestValidWorkerSuccessCompletesTask(t *testing.T) {
	password := "0000000001"
	hash := md5.Sum([]byte(password))
	srv, chunk := testServerWithHashLease(t, "worker-a", hex.EncodeToString(hash[:]))

	_, err := srv.ReportResult(context.Background(), &pb.CrackResult{
		TaskId:        chunk.TaskId,
		WorkerId:      "worker-a",
		Success:       true,
		FoundPassword: password,
		Processed:     10,
	})
	if err != nil {
		t.Fatalf("ReportResult valid success: %v", err)
	}

	srv.state.mu.Lock()
	task := srv.state.tasks["task-1"]
	srv.state.mu.Unlock()
	if task.Status != TaskStatusCompleted || !task.Found || task.FoundPassword != password {
		t.Fatalf("task did not complete with verified password: %+v", task)
	}
}

func TestWorkerErrorRequeuesChunkBeforeFailingTask(t *testing.T) {
	srv, chunk := testServerWithLease(t, "worker-a")

	_, err := srv.ReportResult(context.Background(), &pb.CrackResult{
		TaskId:       chunk.TaskId,
		WorkerId:     "worker-a",
		ErrorMessage: "temporary failure",
	})
	if err != nil {
		t.Fatalf("ReportResult worker error: %v", err)
	}

	srv.state.mu.Lock()
	task := srv.state.tasks["task-1"]
	pending := len(task.PendingRanges)
	status := task.Status
	srv.state.mu.Unlock()
	if status == TaskStatusFailed {
		t.Fatal("task failed on first worker error")
	}
	if pending == 0 {
		t.Fatal("worker error did not requeue chunk")
	}
}

func TestWorkerInfoValidation(t *testing.T) {
	if _, _, err := validateWorkerInfo(nil); err == nil {
		t.Fatal("validateWorkerInfo accepted nil worker")
	}
	if _, _, err := validateWorkerInfo(&pb.WorkerInfo{WorkerId: "bad/id", CpuCores: 4}); err == nil {
		t.Fatal("validateWorkerInfo accepted invalid worker id")
	}
	workerID, cores, err := validateWorkerInfo(&pb.WorkerInfo{WorkerId: "worker-1", CpuCores: 2048})
	if err != nil {
		t.Fatalf("validateWorkerInfo valid worker: %v", err)
	}
	if workerID != "worker-1" {
		t.Fatalf("expected worker-1, got %q", workerID)
	}
	if cores != maxCPUCores {
		t.Fatalf("expected clamped cores %d, got %d", maxCPUCores, cores)
	}
}

func testServerWithLease(t *testing.T, workerID string) (*server, *pb.TaskChunk) {
	t.Helper()
	return testServerWithHashLease(t, workerID, strings.Repeat("a", 32))
}

func testServerWithHashLease(t *testing.T, workerID, hash string) (*server, *pb.TaskChunk) {
	t.Helper()
	state := newMasterState()
	task := state.addTask(hash, HashModeMD5, "", "", "", "", 0, 0, 10, 100, 0, 3)

	state.mu.Lock()
	chunk := state.assignChunkLocked(task, 0, 10, workerID, time.Now())
	state.mu.Unlock()

	return &server{state: state}, chunk
}

func assertStatusCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected status %s, got nil", want)
	}
	got := status.Code(err)
	if got != want {
		t.Fatalf("expected status %s, got %s: %v", want, got, err)
	}
}
