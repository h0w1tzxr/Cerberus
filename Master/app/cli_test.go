package master

import (
	"bytes"
	"strings"
	"testing"

	pb "cracker/cracker"
)

func TestRenderTaskListDefaultPrintsSummaryOnly(t *testing.T) {
	var out bytes.Buffer
	renderTaskList(sampleTaskList(), taskListRenderOptions{}, &out)
	text := out.String()
	if !strings.Contains(text, "Total: 3") || !strings.Contains(text, "found:1") || !strings.Contains(text, "completed-no-password:1") {
		t.Fatalf("summary missing expected counts:\n%s", text)
	}
	if strings.Contains(text, "ID\tSTATUS") || strings.Contains(text, "task-1") {
		t.Fatalf("default task list printed table rows:\n%s", text)
	}
}

func TestRenderTaskListTablePrintsRows(t *testing.T) {
	var out bytes.Buffer
	renderTaskList(sampleTaskList(), taskListRenderOptions{table: true}, &out)
	text := out.String()
	if !strings.Contains(text, "ID") || !strings.Contains(text, "task-1") || !strings.Contains(text, "task-2") {
		t.Fatalf("table output missing expected rows:\n%s", text)
	}
}

func TestRenderTaskListLimitPrintsSubset(t *testing.T) {
	var out bytes.Buffer
	renderTaskList(sampleTaskList(), taskListRenderOptions{table: true, limit: 2, hasLimit: true}, &out)
	text := out.String()
	if !strings.Contains(text, "Showing 2 of 3 matching tasks.") {
		t.Fatalf("limited output missing count line:\n%s", text)
	}
	if !strings.Contains(text, "task-1") || !strings.Contains(text, "task-2") {
		t.Fatalf("limited output missing included rows:\n%s", text)
	}
	if strings.Contains(text, "task-3") {
		t.Fatalf("limited output included extra row:\n%s", text)
	}
}

func TestTaskListRejectsNegativeLimit(t *testing.T) {
	var out bytes.Buffer
	err := taskList(nil, []string{"--limit", "-1"}, &out)
	if err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("expected negative limit error, got %v", err)
	}
}

func sampleTaskList() []*pb.Task {
	return []*pb.Task{
		{
			Id:            "task-1",
			Status:        pb.TaskStatus_TASK_STATUS_COMPLETED,
			Mode:          pb.HashMode_HASH_MODE_MD5,
			Hash:          "21232f297a57a5a743894a0e4a801fc3",
			WordlistPath:  "wordlists/test.txt",
			Completed:     3,
			TotalKeyspace: 3,
			Found:         true,
			MaxRetries:    3,
		},
		{
			Id:            "task-2",
			Status:        pb.TaskStatus_TASK_STATUS_COMPLETED,
			Mode:          pb.HashMode_HASH_MODE_MD5,
			Hash:          "37ebae84c6f8c99033f35a539451a23e",
			WordlistPath:  "wordlists/test.txt",
			Completed:     3,
			TotalKeyspace: 3,
			MaxRetries:    3,
		},
		{
			Id:            "task-3",
			Status:        pb.TaskStatus_TASK_STATUS_RUNNING,
			Mode:          pb.HashMode_HASH_MODE_SHA256,
			Hash:          strings.Repeat("a", 64),
			Completed:     1,
			TotalKeyspace: 3,
			MaxRetries:    3,
			DispatchReady: true,
		},
	}
}
