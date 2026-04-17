package master

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"path/filepath"
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

func TestWorkerHeartbeatUpdatesLastSeen(t *testing.T) {
	srv := &server{state: newMasterState()}
	srv.state.mu.Lock()
	srv.state.updateWorkerLocked("worker-a", 4, time.Now().Add(-time.Second))
	srv.state.mu.Unlock()

	_, err := srv.Heartbeat(context.Background(), &pb.WorkerInfo{WorkerId: "worker-a", CpuCores: 4})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	srv.state.mu.Lock()
	info := srv.state.workers["worker-a"]
	srv.state.mu.Unlock()
	if info == nil {
		t.Fatal("heartbeat did not update worker")
	}
	if info.CPUCores != 4 {
		t.Fatalf("heartbeat cores = %d, want 4", info.CPUCores)
	}
	if info.LastSeen.IsZero() {
		t.Fatal("heartbeat did not update last seen")
	}
}

func TestHeartbeatRejectsUnregisteredWorker(t *testing.T) {
	srv := &server{state: newMasterState()}
	_, err := srv.Heartbeat(context.Background(), &pb.WorkerInfo{WorkerId: "worker-ghost", CpuCores: 4})
	if err == nil {
		t.Fatal("expected error for unregistered worker, got nil")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}
}

func TestOperatorEvictRejectsReregistration(t *testing.T) {
	srv := &server{state: newMasterState()}
	ctx := context.Background()
	info := &pb.WorkerInfo{WorkerId: "worker-evict", CpuCores: 4}

	if _, err := srv.RegisterWorker(ctx, info); err != nil {
		t.Fatalf("initial RegisterWorker: %v", err)
	}

	srv.state.mu.Lock()
	evicted := srv.state.operatorEvictWorkerLocked("worker-evict", time.Now())
	srv.state.mu.Unlock()
	if !evicted {
		t.Fatal("operatorEvictWorkerLocked returned false")
	}

	_, err := srv.RegisterWorker(ctx, info)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for evicted worker, got %v", err)
	}

	srv.state.mu.Lock()
	admitted := srv.state.admitWorkerLocked("worker-evict")
	srv.state.mu.Unlock()
	if !admitted {
		t.Fatal("admitWorkerLocked returned false for evicted worker")
	}

	if _, err := srv.RegisterWorker(ctx, info); err != nil {
		t.Fatalf("RegisterWorker after admit: %v", err)
	}
}

func TestEvictedWorkersFilePersistsAcrossStates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evicted_workers.txt")

	first := newMasterState()
	if err := first.ConfigureEvictionStore(path); err != nil {
		t.Fatalf("ConfigureEvictionStore: %v", err)
	}
	first.mu.Lock()
	first.operatorEvictWorkerLocked("worker-a", time.Now())
	first.operatorEvictWorkerLocked("worker-b", time.Now())
	first.mu.Unlock()

	second := newMasterState()
	if err := second.ConfigureEvictionStore(path); err != nil {
		t.Fatalf("second ConfigureEvictionStore: %v", err)
	}
	second.mu.Lock()
	ids := second.evictedWorkersLocked()
	second.mu.Unlock()
	if len(ids) != 2 || ids[0] != "worker-a" || ids[1] != "worker-b" {
		t.Fatalf("loaded evicted ids = %v, want [worker-a worker-b]", ids)
	}

	second.mu.Lock()
	second.admitWorkerLocked("worker-a")
	second.mu.Unlock()

	third := newMasterState()
	if err := third.ConfigureEvictionStore(path); err != nil {
		t.Fatalf("third ConfigureEvictionStore: %v", err)
	}
	third.mu.Lock()
	ids = third.evictedWorkersLocked()
	third.mu.Unlock()
	if len(ids) != 1 || ids[0] != "worker-b" {
		t.Fatalf("after admit loaded ids = %v, want [worker-b]", ids)
	}
}

func TestWorkerReregistersAfterEviction(t *testing.T) {
	srv := &server{state: newMasterState()}
	ctx := context.Background()
	info := &pb.WorkerInfo{WorkerId: "worker-rejoin", CpuCores: 4}

	if _, err := srv.RegisterWorker(ctx, info); err != nil {
		t.Fatalf("initial RegisterWorker: %v", err)
	}
	if _, err := srv.Heartbeat(ctx, info); err != nil {
		t.Fatalf("first Heartbeat: %v", err)
	}

	srv.state.mu.Lock()
	srv.state.deleteWorkerLocked("worker-rejoin", time.Now())
	srv.state.mu.Unlock()

	if _, err := srv.Heartbeat(ctx, info); status.Code(err) != codes.NotFound {
		t.Fatalf("Heartbeat after eviction = %v, want NotFound", err)
	}

	if _, err := srv.RegisterWorker(ctx, info); err != nil {
		t.Fatalf("re-RegisterWorker: %v", err)
	}
	if _, err := srv.Heartbeat(ctx, info); err != nil {
		t.Fatalf("Heartbeat after re-register: %v", err)
	}

	srv.state.mu.Lock()
	_, present := srv.state.workers["worker-rejoin"]
	srv.state.mu.Unlock()
	if !present {
		t.Fatal("worker missing after re-registration")
	}
}

func TestWorkerHealthOfflineAfterMissedHeartbeats(t *testing.T) {
	now := time.Now()
	if got := workerHealth(now, now.Add(-WorkerStaleAfter+time.Second), WorkerStaleAfter); got != "healthy" {
		t.Fatalf("fresh worker health = %q, want healthy", got)
	}
	if got := workerHealth(now, now.Add(-WorkerStaleAfter-time.Second), WorkerStaleAfter); got != "offline" {
		t.Fatalf("expired worker health = %q, want offline", got)
	}
}

func TestDeleteWorkerRequeuesActiveLeases(t *testing.T) {
	state := newMasterState()
	task := state.addTask(strings.Repeat("a", 32), HashModeMD5, "", "", "", "", 0, 0, 10, 100, 0, 3)
	now := time.Now()

	state.mu.Lock()
	chunk := state.assignChunkLocked(task, 0, 10, "worker-a", now)
	state.updateWorkerLocked("worker-a", 4, now)
	deleted := state.deleteWorkerLocked("worker-a", now.Add(time.Second))
	_, workerExists := state.workers["worker-a"]
	_, leaseExists := state.activeChunks[chunk.TaskId]
	pending := len(task.PendingRanges)
	state.mu.Unlock()

	if !deleted {
		t.Fatal("deleteWorkerLocked returned false")
	}
	if workerExists {
		t.Fatal("worker still exists after delete")
	}
	if leaseExists {
		t.Fatal("worker lease still exists after delete")
	}
	if pending != 1 {
		t.Fatalf("pending ranges = %d, want 1", pending)
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
