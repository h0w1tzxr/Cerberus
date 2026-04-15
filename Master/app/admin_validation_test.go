package master

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	pb "cracker/cracker"

	"google.golang.org/grpc/codes"
)

func TestAddTaskValidatesHashForMode(t *testing.T) {
	admin := &adminServer{state: newMasterState()}

	_, err := admin.AddTask(context.Background(), &pb.TaskSpec{
		Hash:       "abc",
		Mode:       pb.HashMode_HASH_MODE_MD5,
		ChunkSize:  1,
		Keyspace:   1,
		MaxRetries: 1,
	})
	assertStatusCode(t, err, codes.InvalidArgument)

	_, err = admin.AddTask(context.Background(), &pb.TaskSpec{
		Hash:       strings.Repeat("g", 64),
		Mode:       pb.HashMode_HASH_MODE_SHA256,
		ChunkSize:  1,
		Keyspace:   1,
		MaxRetries: 1,
	})
	assertStatusCode(t, err, codes.InvalidArgument)
}

func TestAddTaskValidatesLimits(t *testing.T) {
	admin := &adminServer{state: newMasterState()}

	_, err := admin.AddTask(context.Background(), &pb.TaskSpec{
		Hash:       strings.Repeat("a", 32),
		Mode:       pb.HashMode_HASH_MODE_MD5,
		ChunkSize:  0,
		Keyspace:   1,
		MaxRetries: 1,
	})
	assertStatusCode(t, err, codes.InvalidArgument)

	_, err = admin.AddTask(context.Background(), &pb.TaskSpec{
		Hash:       strings.Repeat("a", 32),
		Mode:       pb.HashMode_HASH_MODE_MD5,
		ChunkSize:  1,
		Keyspace:   maxKeyspace + 1,
		MaxRetries: 1,
	})
	assertStatusCode(t, err, codes.InvalidArgument)

	_, err = admin.AddTask(context.Background(), &pb.TaskSpec{
		Hash:       strings.Repeat("a", 32),
		Mode:       pb.HashMode_HASH_MODE_MD5,
		ChunkSize:  1,
		Keyspace:   1,
		MaxRetries: maxTaskRetries + 1,
	})
	assertStatusCode(t, err, codes.InvalidArgument)
}

func TestAddTaskBatchValidatesHashesAndBatchLimit(t *testing.T) {
	admin := &adminServer{state: newMasterState()}

	_, err := admin.AddTaskBatch(context.Background(), &pb.TaskBatchSpec{
		Hashes:     []string{strings.Repeat("a", 32), "bad"},
		Mode:       pb.HashMode_HASH_MODE_MD5,
		ChunkSize:  1,
		Keyspace:   1,
		MaxRetries: 1,
	})
	assertStatusCode(t, err, codes.InvalidArgument)

	hashes := make([]string, maxBatchHashes+1)
	for i := range hashes {
		hashes[i] = strings.Repeat("a", 32)
	}
	_, err = admin.AddTaskBatch(context.Background(), &pb.TaskBatchSpec{
		Hashes:     hashes,
		Mode:       pb.HashMode_HASH_MODE_MD5,
		ChunkSize:  1,
		Keyspace:   1,
		MaxRetries: 1,
	})
	assertStatusCode(t, err, codes.InvalidArgument)
}

func TestSetPriorityValidatesRange(t *testing.T) {
	state := newMasterState()
	task := state.addTask(strings.Repeat("a", 32), HashModeMD5, "", "", "", "", 0, 0, 1, 1, 0, 1)
	admin := &adminServer{state: state}

	_, err := admin.ApplyTaskAction(context.Background(), &pb.TaskActionRequest{
		TaskId:   task.ID,
		Action:   pb.TaskAction_TASK_ACTION_SET_PRIORITY,
		Priority: int32(maxPriority + 1),
	})
	assertStatusCode(t, err, codes.InvalidArgument)
}

func TestAddTaskSendsDataDirRelativeWordlistPath(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("CERBERUS_DATA_DIR", dataDir)
	t.Setenv("CERBERUS_ALLOW_UNSAFE_PATHS", "")
	wordlistDir := filepath.Join(dataDir, "wordlists")
	if err := os.MkdirAll(wordlistDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	wordlistPath := filepath.Join(wordlistDir, "lab.txt")
	if err := os.WriteFile(wordlistPath, []byte("password\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	admin := &adminServer{state: newMasterState()}
	task, err := admin.AddTask(context.Background(), &pb.TaskSpec{
		Hash:         strings.Repeat("a", 32),
		Mode:         pb.HashMode_HASH_MODE_MD5,
		WordlistPath: wordlistPath,
		ChunkSize:    1,
		MaxRetries:   1,
	})
	if err != nil {
		t.Fatalf("AddTask with wordlist: %v", err)
	}
	if filepath.IsAbs(task.WordlistPath) {
		t.Fatalf("wordlist path leaked absolute path: %q", task.WordlistPath)
	}
	if task.WordlistPath != "wordlists/lab.txt" {
		t.Fatalf("expected relative wordlist path, got %q", task.WordlistPath)
	}
}

func TestListTasksSnapshotsWhileTasksMutate(t *testing.T) {
	state := newMasterState()
	task := state.addTask(strings.Repeat("a", 32), HashModeMD5, "", "", "", "", 0, 0, 1, 3, 0, 1)
	admin := &adminServer{state: state}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			state.mu.Lock()
			task.Found = !task.Found
			task.FoundPassword = strings.Repeat("x", 64)
			task.Completed = (task.Completed + 1) % task.TotalKeyspace
			state.mu.Unlock()
		}
	}()

	for i := 0; i < 1000; i++ {
		if _, err := admin.ListTasks(context.Background(), &pb.TaskListRequest{}); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("ListTasks: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}
