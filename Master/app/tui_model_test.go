package master

import (
	"bytes"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTUIViewFitsMinimumTerminal(t *testing.T) {
	state, ui, logs := testTUIState(t)
	model := newMasterTUIModel(state, ui, logs, serverConfig{listenAddr: defaultListenAddress}, nil)
	model.width = tuiMinWidth
	model.height = tuiMinHeight
	model.snapshot = snapshotMasterTUI(state, ui, logs, defaultListenAddress)

	view := stripANSI(model.View())
	lines := strings.Split(view, "\n")
	if len(lines) > tuiMinHeight {
		t.Fatalf("view height = %d, want <= %d\n%s", len(lines), tuiMinHeight, view)
	}
	for i, line := range lines {
		if len([]rune(line)) > tuiMinWidth {
			t.Fatalf("line %d width = %d, want <= %d: %q", i+1, len([]rune(line)), tuiMinWidth, line)
		}
	}
}

func TestTUILogStoreCapsAndPreservesTokens(t *testing.T) {
	logs := newTUILogStore(2)
	_, _ = logs.Write([]byte("one\n"))
	_, _ = logs.Write([]byte("CERBERUS_WORKER_TOKEN=secret-token\n"))
	_, _ = logs.Write([]byte("[!] three\n"))

	entries := logs.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	// tokens are now visible in the Logs tab (operator-only view)
	if !strings.Contains(entries[0].Message, "secret-token") {
		t.Fatalf("token was unexpectedly redacted in logs: %q", entries[0].Message)
	}
	if entries[1].Level != uiEventError {
		t.Fatalf("level = %v, want error", entries[1].Level)
	}
}

func TestTUIWorkerIssueTokenVisibleOnlyInCommandDrawer(t *testing.T) {
	logs := newTUILogStore(defaultTUILogLimit)
	model := newMasterTUIModel(nil, nil, logs, serverConfig{listenAddr: defaultListenAddress}, nil)
	output := strings.Join([]string{
		"Issued worker token for worker-1",
		"CERBERUS_WORKER_ID=worker-1",
		"CERBERUS_WORKER_TOKEN=secret-token",
	}, "\n")

	next, _ := model.Update(tuiCommandResultMsg{
		line:            "token worker issue --worker-id worker-1",
		output:          output,
		revealSensitive: true,
	})
	updated := next.(masterTUIModel)
	if len(updated.commandLog) == 0 {
		t.Fatal("command drawer did not receive token output")
	}
	drawer := updated.commandLog[len(updated.commandLog)-1]
	if !strings.Contains(drawer, "CERBERUS_WORKER_TOKEN=secret-token") {
		t.Fatalf("worker token was not visible in drawer:\n%s", drawer)
	}
	if !updated.commandOpen {
		t.Fatal("worker token output drawer was not kept open")
	}

	entries := logs.Entries()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	// tokens are visible in the Logs tab: operator-only view, no redaction
	if !strings.Contains(entries[0].Message, "secret-token") {
		t.Fatalf("worker token missing from logs: %q", entries[0].Message)
	}
}

func TestTUICommandOutputRedactsByDefault(t *testing.T) {
	model := newMasterTUIModel(nil, nil, nil, serverConfig{listenAddr: defaultListenAddress}, nil)
	next, _ := model.Update(tuiCommandResultMsg{
		line:   "worker list",
		output: "CERBERUS_WORKER_TOKEN=secret-token",
	})
	updated := next.(masterTUIModel)
	if updated.commandOpen {
		t.Fatal("single-line command output stayed open in drawer")
	}
	if strings.Contains(updated.status.message, "secret-token") {
		t.Fatalf("sensitive output was not redacted by default: %q", updated.status.message)
	}
	if !strings.Contains(updated.status.message, "CERBERUS_WORKER_TOKEN=<redacted>") {
		t.Fatalf("redacted token marker missing: %q", updated.status.message)
	}
}

func TestAllowsSensitiveDrawerOutputOnlyForWorkerIssue(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{args: []string{"token", "worker", "issue", "--worker-id", "worker-1"}, want: true},
		{args: []string{"--addr", "localhost:55051", "token", "worker", "issue", "--worker-id", "worker-1"}, want: true},
		{args: []string{"token", "worker", "list"}, want: false},
		{args: []string{"token", "worker", "revoke", "--worker-id", "worker-1"}, want: false},
		{args: []string{"task", "list"}, want: false},
	}
	for _, tc := range cases {
		if got := allowsSensitiveDrawerOutput(tc.args); got != tc.want {
			t.Fatalf("allowsSensitiveDrawerOutput(%v) = %t, want %t", tc.args, got, tc.want)
		}
	}
}

func TestTUIDangerousCommandClassification(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{args: []string{"task", "cancel", "task-1"}, want: true},
		{args: []string{"task", "-c", "task-1"}, want: true},
		{args: []string{"task", "add-batch", "--file", "hashes.txt"}, want: true},
		{args: []string{"token", "worker", "revoke", "--worker-id", "worker-1"}, want: true},
		{args: []string{"dispatch", "pause"}, want: false},
		{args: []string{"worker", "list"}, want: false},
	}
	for _, tc := range cases {
		got, _ := dangerousTUICommand(tc.args)
		if got != tc.want {
			t.Fatalf("dangerousTUICommand(%v) = %t, want %t", tc.args, got, tc.want)
		}
	}
}

func TestTUIArgsWithDefaultAddress(t *testing.T) {
	args := argsWithTUIDefaults([]string{"task", "list"}, "127.0.0.1:55051")
	if strings.Join(args[:2], " ") != "--addr 127.0.0.1:55051" {
		t.Fatalf("missing default addr prefix: %v", args)
	}

	args = argsWithTUIDefaults([]string{"--addr", "localhost:1", "task", "list"}, "127.0.0.1:55051")
	if strings.Join(args[:2], " ") != "--addr localhost:1" {
		t.Fatalf("overrode explicit addr: %v", args)
	}

	args = argsWithTUIDefaults([]string{"task", "list"}, "0.0.0.0:55051")
	if strings.Join(args[:2], " ") != "--addr localhost:55051" {
		t.Fatalf("non-loopback listen addr was not normalized: %v", args)
	}
}

func TestTUIRejectsStdinAddBatchForms(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{args: []string{"task", "add-batch", "--stdin"}, want: true},
		{args: []string{"task", "add-batch", "--stdin=true"}, want: true},
		{args: []string{"task", "add-batch", "-stdin"}, want: true},
		{args: []string{"task", "add-batch", "-stdin=true"}, want: true},
		{args: []string{"task", "add-batch", "--file", "-"}, want: true},
		{args: []string{"task", "add-batch", "--file=-"}, want: true},
		{args: []string{"task", "add-batch", "-file", "-"}, want: true},
		{args: []string{"task", "add-batch", "-file=-"}, want: true},
		{args: []string{"task", "add-batch", "--file", "hashes.txt"}, want: false},
		{args: []string{"task", "list", "--file=-"}, want: false},
		{args: []string{"worker", "list", "-"}, want: false},
	}
	for _, tc := range cases {
		if got := isUnsupportedTUICommand(tc.args); got != tc.want {
			t.Fatalf("isUnsupportedTUICommand(%v) = %t, want %t", tc.args, got, tc.want)
		}
	}
}

func TestTUIImmediateValidationFeedbackClosesCommandMode(t *testing.T) {
	cases := []string{
		"ls",
		"task add-batch --stdin=true",
		`task cancel "unterminated`,
	}
	for _, line := range cases {
		model := newMasterTUIModel(nil, nil, newTUILogStore(defaultTUILogLimit), serverConfig{listenAddr: defaultListenAddress}, nil)
		model.commandMode = true
		model.command.Focus()

		if cmd := model.prepareCommand(line); cmd != nil {
			t.Fatalf("prepareCommand(%q) returned command for immediate validation failure", line)
		}
		if model.commandMode {
			t.Fatalf("prepareCommand(%q) left command mode active", line)
		}
		if model.command.Focused() {
			t.Fatalf("prepareCommand(%q) left command input focused", line)
		}
		if model.status.message == "" {
			t.Fatalf("prepareCommand(%q) did not set feedback status", line)
		}
		if model.commandOpen {
			t.Fatalf("prepareCommand(%q) opened command drawer for validation feedback", line)
		}
	}
}

func TestTUIDashboardShowsFoundPassword(t *testing.T) {
	now := time.Now()
	model := newMasterTUIModel(nil, nil, nil, serverConfig{listenAddr: defaultListenAddress}, nil)
	model.snapshot = masterTUISnapshot{
		Now:        now,
		ListenAddr: defaultListenAddress,
		Summary: workloadSummary{
			totalTasks: 1,
			total:      3,
			completed:  3,
		},
		Tasks: []tuiTaskSnapshot{
			{
				ID:            "task-1",
				Hash:          "37eba84c6f8c9903f35a539451a23e",
				Mode:          "md5",
				Status:        TaskStatusCompleted,
				TotalKeyspace: 3,
				Estimated:     3,
				Found:         true,
				FoundPassword: "cerberus123",
				UpdatedAt:     now,
				CreatedAt:     now,
			},
		},
	}

	view := stripANSI(model.renderDashboard(100, 18))
	for _, want := range []string{"Latest Results", "task-1", "password=cerberus123"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, view)
		}
	}
}

func TestTUITabSwitchCollapsesCommandOutput(t *testing.T) {
	model := newMasterTUIModel(nil, nil, nil, serverConfig{listenAddr: defaultListenAddress}, nil)
	next, _ := model.Update(tuiCommandResultMsg{
		line:            "token worker issue --worker-id worker-1",
		output:          "CERBERUS_WORKER_TOKEN=secret-token",
		revealSensitive: true,
	})
	updated := next.(masterTUIModel)
	if !updated.commandOpen {
		t.Fatal("expected command output to be open")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated = next.(masterTUIModel)
	if updated.commandOpen {
		t.Fatal("tab switch did not collapse command output")
	}
	if updated.tab != tuiTabTasks {
		t.Fatalf("tab = %v, want tasks", updated.tab)
	}
}

func TestTUITabKeyNoLongerSwitchesTabs(t *testing.T) {
	model := newMasterTUIModel(nil, nil, nil, serverConfig{listenAddr: defaultListenAddress}, nil)
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(masterTUIModel)
	if updated.tab != tuiTabDashboard {
		t.Fatalf("tab key switched top-level tab to %v", updated.tab)
	}
}

func TestTUICommandAutocompleteAndHistory(t *testing.T) {
	model := newMasterTUIModel(nil, nil, nil, serverConfig{listenAddr: defaultListenAddress}, nil)
	model.openCommand()
	model.command.SetValue("token worker i")
	model.autocompleteCommand()
	if got := model.command.Value(); got != "token worker issue --worker-id " {
		t.Fatalf("autocomplete = %q", got)
	}

	model.addCommandHistory("worker list")
	model.addCommandHistory("task list")
	model.openCommand()
	model.recallCommandHistory(-1)
	if got := model.command.Value(); got != "task list" {
		t.Fatalf("first history recall = %q", got)
	}
	model.recallCommandHistory(-1)
	if got := model.command.Value(); got != "worker list" {
		t.Fatalf("second history recall = %q", got)
	}
	model.recallCommandHistory(1)
	if got := model.command.Value(); got != "task list" {
		t.Fatalf("history forward = %q", got)
	}
}

func TestTUITaskDetailShowsFoundPassword(t *testing.T) {
	now := time.Now()
	model := newMasterTUIModel(nil, nil, nil, serverConfig{listenAddr: defaultListenAddress}, nil)
	model.snapshot = masterTUISnapshot{
		Now: now,
		Tasks: []tuiTaskSnapshot{
			{
				ID:            "task-1",
				Mode:          "md5",
				Status:        TaskStatusCompleted,
				Found:         true,
				FoundPassword: "cerberus123",
				MaxRetries:    DefaultMaxRetries,
				UpdatedAt:     now,
			},
		},
	}

	view := stripANSI(model.renderTaskDetail(100))
	if !strings.Contains(view, "PASSWORD FOUND") || !strings.Contains(view, "cerberus123") {
		t.Fatalf("task detail did not reveal found password:\n%s", view)
	}
}

func TestTUITasksTabKeepsDetailVisibleWithLongList(t *testing.T) {
	now := time.Now()
	model := newMasterTUIModel(nil, nil, nil, serverConfig{listenAddr: defaultListenAddress}, nil)
	model.tab = tuiTabTasks
	model.taskCursor = 11
	model.snapshot = masterTUISnapshot{
		Now:        now,
		ListenAddr: defaultListenAddress,
		Tasks:      make([]tuiTaskSnapshot, 12),
	}
	for i := range model.snapshot.Tasks {
		model.snapshot.Tasks[i] = tuiTaskSnapshot{
			ID:            "task-" + strconv.Itoa(i+1),
			Hash:          "21232f297a57a5a743894a0e4a801fc3",
			Mode:          "md5",
			Status:        TaskStatusQueued,
			WordlistPath:  "wordlists/test.txt",
			TotalKeyspace: 2,
			MaxRetries:    DefaultMaxRetries,
			UpdatedAt:     now,
			CreatedAt:     now,
		}
	}
	model.snapshot.Tasks[11].WordlistPath = "wordlists/selected.txt"
	model.snapshot.Tasks[11].FailureReason = "worker EOF"

	view := stripANSI(model.renderTasks(80, 10))
	for _, want := range []string{
		"Task Detail",
		"wordlist=wordlists/selected.txt",
		"failure=worker EOF",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("tasks view missing %q:\n%s", want, view)
		}
	}
	lines := strings.Split(view, "\n")
	if len(lines) > 10 {
		t.Fatalf("tasks view height = %d, want <= 10\n%s", len(lines), view)
	}
}

func TestTUITaskViewModesAndSearch(t *testing.T) {
	now := time.Now()
	model := newMasterTUIModel(nil, nil, nil, serverConfig{listenAddr: defaultListenAddress}, nil)
	model.snapshot = masterTUISnapshot{
		Now: now,
		Tasks: []tuiTaskSnapshot{
			{ID: "task-found", Hash: strings.Repeat("a", 32), Mode: "md5", Status: TaskStatusCompleted, Found: true, FoundPassword: "cerberus123", UpdatedAt: now},
			{ID: "task-failed", Hash: strings.Repeat("b", 32), Mode: "md5", Status: TaskStatusFailed, FailureReason: "worker EOF", UpdatedAt: now},
			{ID: "task-active", Hash: strings.Repeat("c", 32), Mode: "md5", Status: TaskStatusRunning, UpdatedAt: now},
		},
	}

	model.taskView = tuiTaskViewFound
	tasks := model.filteredTasks()
	if len(tasks) != 1 || tasks[0].ID != "task-found" {
		t.Fatalf("found view = %+v", tasks)
	}

	model.taskView = tuiTaskViewFailed
	tasks = model.filteredTasks()
	if len(tasks) != 1 || tasks[0].ID != "task-failed" {
		t.Fatalf("failed view = %+v", tasks)
	}

	model.taskView = tuiTaskViewActive
	tasks = model.filteredTasks()
	if len(tasks) != 1 || tasks[0].ID != "task-active" {
		t.Fatalf("active view = %+v", tasks)
	}

	model.taskView = tuiTaskViewAll
	model.filter.SetValue("cerberus123")
	tasks = model.filteredTasks()
	if len(tasks) != 1 || tasks[0].ID != "task-found" {
		t.Fatalf("password search = %+v", tasks)
	}

	model.filter.SetValue("worker eof")
	tasks = model.filteredTasks()
	if len(tasks) != 1 || tasks[0].ID != "task-failed" {
		t.Fatalf("failure search = %+v", tasks)
	}
}

func TestTUIConfirmQuitRequestsShutdown(t *testing.T) {
	state, ui, logs := testTUIState(t)
	model := newMasterTUIModel(state, ui, logs, serverConfig{listenAddr: defaultListenAddress}, nil)
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	updated := next.(masterTUIModel)
	if updated.confirm == nil {
		t.Fatal("expected quit confirmation")
	}
	updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.shutdownRequested || !state.dispatchPaused {
		t.Fatal("confirming quit did not request graceful shutdown")
	}
}

func TestTUIDeleteSelectedWorker(t *testing.T) {
	state, ui, logs := testTUIState(t)
	model := newMasterTUIModel(state, ui, logs, serverConfig{listenAddr: defaultListenAddress}, nil)
	model.tab = tuiTabWorkers
	model.snapshot = snapshotMasterTUI(state, ui, logs, defaultListenAddress)

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	updated := next.(masterTUIModel)
	if updated.confirm == nil {
		t.Fatal("expected delete confirmation")
	}

	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	updated = next.(masterTUIModel)
	if cmd == nil {
		t.Fatal("expected delete command")
	}
	next, _ = updated.Update(cmd())
	updated = next.(masterTUIModel)

	state.mu.Lock()
	_, exists := state.workers["worker-laptop-1"]
	state.mu.Unlock()
	if exists {
		t.Fatal("worker still exists after TUI delete")
	}
	if updated.status.message == "" || !strings.Contains(updated.status.message, "Evicted worker worker-laptop-1") {
		t.Fatalf("evict status missing: %q", updated.status.message)
	}

	state.mu.Lock()
	isEvicted := state.isEvictedLocked("worker-laptop-1")
	state.mu.Unlock()
	if !isEvicted {
		t.Fatal("worker-laptop-1 missing from evicted set after TUI delete")
	}
}

func TestRestoreTerminalLogger(t *testing.T) {
	original := log.Writer()
	t.Cleanup(func() {
		log.SetOutput(original)
	})

	var buf bytes.Buffer
	log.SetOutput(&buf)
	restoreTerminalLogger()
	if log.Writer() != os.Stderr {
		t.Fatal("restoreTerminalLogger did not restore stderr output")
	}
}

func BenchmarkTUIViewTwentyThousandTasks(b *testing.B) {
	model := newMasterTUIModel(nil, nil, newTUILogStore(defaultTUILogLimit), serverConfig{listenAddr: defaultListenAddress}, nil)
	model.width = 120
	model.height = 30
	model.tab = tuiTabTasks
	model.snapshot = masterTUISnapshot{
		Now:        time.Now(),
		ListenAddr: defaultListenAddress,
		Summary: workloadSummary{
			totalTasks: 20_000,
			total:      40_000,
			completed:  10_000,
			remaining:  30_000,
			active:     10_000,
		},
		Tasks: make([]tuiTaskSnapshot, 20_000),
	}
	for i := range model.snapshot.Tasks {
		model.snapshot.Tasks[i] = tuiTaskSnapshot{
			ID:            "task-" + strconv.Itoa(i+1),
			Hash:          "21232f297a57a5a743894a0e4a801fc3",
			Mode:          "md5",
			Status:        TaskStatusRunning,
			TotalKeyspace: 2,
			Estimated:     1,
			MaxRetries:    DefaultMaxRetries,
			UpdatedAt:     model.snapshot.Now,
			CreatedAt:     model.snapshot.Now,
		}
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = model.View()
	}
}

func testTUIState(t *testing.T) (*masterState, *masterUI, *tuiLogStore) {
	t.Helper()
	state := newMasterState()
	now := time.Now()
	state.mu.Lock()
	task := state.addTask("21232f297a57a5a743894a0e4a801fc3", HashModeMD5, "wordlists/test.txt", "", "", "", 0, 0, 2, 3, 0, DefaultMaxRetries)
	state.updateWorkerLocked("worker-laptop-1", 32, now)
	state.assignChunkLocked(task, 0, 2, "worker-laptop-1", now)
	state.chunkProgress["chunk-worker-laptop-1-1"] = 1
	state.mu.Unlock()

	ui := newMasterUI(state)
	ui.SetEvent(uiEventInfo, "ready")
	logs := newTUILogStore(defaultTUILogLimit)
	logs.Append(uiEventInfo, "booted")
	return state, ui, logs
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripANSI(value string) string {
	return ansiPattern.ReplaceAllString(value, "")
}
