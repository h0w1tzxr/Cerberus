package master

import (
	"fmt"
	"strings"
	"time"

	"cracker/Common/console"

	"github.com/charmbracelet/lipgloss"
)

func (m masterTUIModel) renderHeader(width int) string {
	dispatch := tuiStyles.success.Render("active")
	if m.snapshot.DispatchPaused {
		dispatch = tuiStyles.warn.Render("paused")
	}
	if m.snapshot.ShutdownRequested {
		dispatch = tuiStyles.warn.Render("draining")
	}

	title := tuiStyles.title.Render("Cerberus Master")
	line := fmt.Sprintf("%s %s  %s | dispatch %s | workers %d/%d | tasks %d | rate %s",
		title,
		m.spinner.View(),
		truncateCell(m.snapshot.ListenAddr, 24),
		dispatch,
		m.snapshot.ActiveWorkers,
		m.snapshot.WorkerCount,
		m.snapshot.Summary.totalTasks,
		console.FormatHashRate(m.snapshot.TotalRate),
	)
	return tuiStyles.header.Width(width).Render(truncateCell(line, width-2))
}

func (m masterTUIModel) renderTabs(width int) string {
	parts := make([]string, 0, len(tuiTabNames))
	for i, name := range tuiTabNames {
		style := tuiStyles.tab
		if tuiTab(i) == m.tab {
			style = tuiStyles.activeTab
		}
		parts = append(parts, style.Render(name))
	}
	line := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	return tuiStyles.screen.Width(width).Render(truncateCell(line, width))
}

func (m masterTUIModel) renderBody(width, height int) string {
	switch m.tab {
	case tuiTabTasks:
		return m.renderTasks(width, height)
	case tuiTabWorkers:
		return m.renderWorkers(width, height)
	case tuiTabLogs:
		return m.renderLogs(width, height)
	case tuiTabHelp:
		return m.renderHelp(width, height)
	default:
		return m.renderDashboard(width, height)
	}
}

func (m masterTUIModel) renderDashboard(width, height int) string {
	lines := []string{
		tuiStyles.section.Render("Overview"),
		fmt.Sprintf("Progress %s  %s  scope %s  eta %s",
			m.progress.View(),
			console.FormatPercent(m.snapshot.Summary.completed, m.snapshot.Summary.total),
			formatScope(m.snapshot.Summary.total),
			m.renderETA(),
		),
		fmt.Sprintf("Tasks %d  active %d  chunks %d  found %d  failed %d",
			m.snapshot.Summary.totalTasks,
			m.snapshot.Summary.active,
			m.snapshot.ActiveChunks,
			countFoundTasks(m.snapshot.Tasks),
			countFailedTasks(m.snapshot.Tasks),
		),
	}

	if results := notableTaskResults(m.snapshot.Tasks); len(results) > 0 {
		lines = append(lines, "", tuiStyles.section.Render("Latest Results"))
		maxResults := 3
		if maxResults > len(results) {
			maxResults = len(results)
		}
		for i := 0; i < maxResults; i++ {
			task := results[i]
			line := fmt.Sprintf("%s  %s  %s", task.ID, task.Mode, resultSummary(task))
			lines = append(lines, resultStyle(task).Render(truncateCell(line, width)))
		}
		if len(results) > maxResults {
			lines = append(lines, tuiStyles.muted.Render(fmt.Sprintf("more results in Tasks: %d (press v for found/failed)", len(results)-maxResults)))
		}
	}

	lines = append(lines, "", tuiStyles.section.Render("Workers"), m.workerTableHeader(width))

	workerRows := height - len(lines) - 4
	if workerRows < 1 {
		workerRows = 1
	}
	if len(m.snapshot.Workers) == 0 {
		lines = append(lines, tuiStyles.muted.Render("no workers connected"))
	} else {
		for i, worker := range m.snapshot.Workers {
			if i >= workerRows {
				lines = append(lines, tuiStyles.muted.Render(fmt.Sprintf("hidden workers: %d", len(m.snapshot.Workers)-i)))
				break
			}
			lines = append(lines, m.workerRow(worker, false, width))
		}
	}

	lines = append(lines, "", tuiStyles.section.Render("Latest Event"))
	event := "-"
	if m.snapshot.Event.message != "" {
		event = eventStyle(m.snapshot.Event.level).Render(m.snapshot.Event.message)
	}
	lines = append(lines, truncateCell(event, width))
	return limitBlock(strings.Join(lines, "\n"), width, height)
}

func (m masterTUIModel) renderTasks(width, height int) string {
	tasks := m.filteredTasks()
	filter := strings.TrimSpace(m.filter.Value())
	title := fmt.Sprintf("Tasks %d  view=%s", len(tasks), m.taskViewName())
	if m.taskSort != tuiTaskSortDefault {
		title += "  sort=" + m.taskSortName()
	}
	if filter != "" {
		title += "  filter=" + filter
	}
	lines := []string{
		tuiStyles.section.Render(title),
		m.taskTableHeader(width),
	}

	detail := m.renderTaskDetail(width)
	detailHeight := lipgloss.Height(detail)
	if detailHeight < 1 {
		detailHeight = 1
	}

	rowHeight := height - len(lines) - detailHeight - 2
	if rowHeight < 0 {
		rowHeight = 0
	}
	if len(tasks) == 0 {
		lines = append(lines, tuiStyles.muted.Render("no tasks"))
	} else {
		start, end := visibleWindow(m.taskCursor, len(tasks), rowHeight)
		for i := start; i < end; i++ {
			lines = append(lines, m.taskRow(tasks[i], i == m.taskCursor, width))
		}
		summary := fmt.Sprintf("showing %d-%d of %d", start+1, end, len(tasks))
		if start == end {
			summary = fmt.Sprintf("showing 0 of %d", len(tasks))
		}
		lines = append(lines, tuiStyles.muted.Render(summary))
	}
	lines = append(lines, "")
	lines = append(lines, detail)
	return limitBlock(strings.Join(lines, "\n"), width, height)
}

func (m masterTUIModel) renderWorkers(width, height int) string {
	lines := []string{
		tuiStyles.section.Render(fmt.Sprintf("Workers %d", len(m.snapshot.Workers))),
		m.workerTableHeader(width),
	}
	detail := ""
	detailHeight := 0
	if m.workerDetailOpen && len(m.snapshot.Workers) > 0 {
		detail = m.renderWorkerDetail(width)
		detailHeight = lipgloss.Height(detail) + 1
	}
	rowHeight := height - len(lines) - 1 - detailHeight
	if rowHeight < 1 {
		rowHeight = 1
	}
	if len(m.snapshot.Workers) == 0 {
		lines = append(lines, tuiStyles.muted.Render("no workers connected"))
	} else {
		start, end := visibleWindow(m.workerCursor, len(m.snapshot.Workers), rowHeight)
		for i := start; i < end; i++ {
			lines = append(lines, m.workerRow(m.snapshot.Workers[i], i == m.workerCursor, width))
		}
	}
	if detail != "" {
		lines = append(lines, "")
		lines = append(lines, detail)
	}
	return limitBlock(strings.Join(lines, "\n"), width, height)
}

func (m masterTUIModel) renderWorkerDetail(width int) string {
	if len(m.snapshot.Workers) == 0 {
		return tuiStyles.muted.Render("No worker selected.")
	}
	w := m.snapshot.Workers[clampIndex(m.workerCursor, len(m.snapshot.Workers))]
	quarantineInfo := "no"
	if w.Quarantined {
		quarantineInfo = tuiStyles.danger.Render("yes: " + w.QuarantineReason)
	}
	lines := []string{
		tuiStyles.section.Render("Worker Detail"),
		fmt.Sprintf("%s  health=%s  cores=%d  quarantined=%s",
			w.ID,
			healthStyle(w.Health).Render(w.Health),
			w.CPUCores,
			quarantineInfo,
		),
		fmt.Sprintf("avg=%s  last-chunk=%s  last-task=%s  last-duration=%s",
			console.FormatHashRate(w.AvgRate),
			console.FormatHashRate(w.LastChunkRate),
			formatOptional(w.LastTaskID),
			formatDuration(w.LastTaskDuration),
		),
		fmt.Sprintf("total-processed=%d  chunks=%d  tasks=%d",
			w.TotalProcessed,
			w.CompletedChunks,
			w.CompletedTasks,
		),
	}
	return limitBlock(strings.Join(lines, "\n"), width, 4)
}

func (m masterTUIModel) renderLogs(width, height int) string {
	logs := m.filteredLogs()
	title := fmt.Sprintf("Logs %d", len(logs))
	if filter := strings.TrimSpace(m.filter.Value()); filter != "" {
		title += "  filter=" + filter
	}
	lines := []string{tuiStyles.section.Render(title)}
	rowHeight := height - len(lines)
	if rowHeight < 1 {
		rowHeight = 1
	}
	if len(logs) == 0 {
		lines = append(lines, tuiStyles.muted.Render("no logs yet"))
		return limitBlock(strings.Join(lines, "\n"), width, height)
	}
	start, end := visibleWindow(m.logCursor, len(logs), rowHeight)
	for i := start; i < end; i++ {
		entry := logs[i]
		prefix := entry.At.Format("15:04:05")
		line := fmt.Sprintf("%s %s", prefix, entry.Message)
		lines = append(lines, eventStyle(entry.Level).Render(truncateCell(line, width)))
	}
	return limitBlock(strings.Join(lines, "\n"), width, height)
}

func (m masterTUIModel) renderHelp(width, height int) string {
	lines := []string{
		tuiStyles.section.Render("Keys"),
		"left/right or h/l      switch tabs",
		"j/k or arrows          move selection",
		"g/G                    first/last row",
		"/                      filter tasks or logs",
		":                      command drawer",
		"tab                    autocomplete command",
		"up/down                command history",
		"v                      cycle task view",
		"o                      cycle task sort (A-Z, fastest, slowest, recently cracked)",
		"p/r                    pause/resume dispatch",
		"s                      save snapshot to logs",
		"d                      delete selected worker",
		"enter                  toggle worker detail (Workers tab)",
		"q                      graceful shutdown",
		"esc                    close drawer/filter",
		"",
		tuiStyles.section.Render("Commands"),
		"task add --hash <hash> --mode md5 --wordlist wordlists/test.txt --chunk 2",
		"task list --table --limit 20",
		"worker list",
		"dispatch pause",
		"dispatch resume",
		"",
		tuiStyles.muted.Render("Automation remains CLI-first. Run scripts and stdin-heavy commands in another terminal."),
	}
	return limitBlock(strings.Join(lines, "\n"), width, height)
}

func (m masterTUIModel) renderCommandArea(width int) string {
	if m.confirm != nil {
		return tuiStyles.confirm.Width(width).Render(truncateCell(m.confirm.message, width-2))
	}
	if m.commandMode {
		lines := []string{truncateCell(m.command.View(), width-2)}
		if strings.HasPrefix(m.status.message, "suggest: ") {
			lines = append(lines, tuiStyles.muted.Render(truncateCell(m.status.message, width-2)))
		}
		return tuiStyles.command.Width(width).Render(strings.Join(lines, "\n"))
	}
	if m.filterMode {
		return tuiStyles.command.Width(width).Render(truncateCell(m.filter.View(), width-2))
	}
	if m.commandOpen && len(m.commandLog) > 0 {
		lines := strings.Split(m.commandLog[len(m.commandLog)-1], "\n")
		if len(lines) > tuiCommandLines {
			lines = lines[len(lines)-tuiCommandLines:]
		}
		for i, line := range lines {
			lines[i] = truncateCell(line, width-2)
		}
		return tuiStyles.command.Width(width).Render(strings.Join(lines, "\n"))
	}
	if m.status.message != "" {
		return tuiStyles.command.Width(width).Render(eventStyle(m.status.level).Render(truncateCell("status: "+m.status.message, width-2)))
	}
	event := m.snapshot.Event.message
	if event == "" {
		event = "-"
	}
	return tuiStyles.command.Width(width).Render(tuiStyles.muted.Render("event: " + event))
}

func (m masterTUIModel) renderFooter(width int) string {
	help := "left/right switch | : command | / filter | p/r pause/resume | ? help | q quit"
	if m.commandMode {
		help = "tab autocomplete | up/down history | enter run | esc cancel"
	} else if m.commandOpen {
		help = "esc close output | left/right switch | : command | / filter | q quit"
	}
	if m.filterMode {
		help = "type to filter | enter/esc close"
	}
	return tuiStyles.footer.Width(width).Render(truncateCell(help, width-2))
}

type taskColumnWidths struct {
	id, status, mode, hash, progress, found int
}

func computeTaskColumnWidths(width int) taskColumnWidths {
	c := taskColumnWidths{id: 9, status: 10, mode: 6, progress: 15, found: 5}
	c.hash = width - c.id - c.status - c.mode - c.progress - c.found - 6
	if c.hash < 12 {
		c.hash = 12
	}
	return c
}

func (m masterTUIModel) taskTableHeader(width int) string {
	c := computeTaskColumnWidths(width)
	line := padCell("ID", c.id) + " " +
		padCell("STATUS", c.status) + " " +
		padCell("MODE", c.mode) + " " +
		padCell("HASH", c.hash) + " " +
		rightCell("PROGRESS", c.progress) + " " +
		padCell("FOUND", c.found)
	return tuiStyles.tableHeader.Render(truncateCell(line, width))
}

func (m masterTUIModel) taskRow(task tuiTaskSnapshot, selected bool, width int) string {
	c := computeTaskColumnWidths(width)
	found := "no"
	if task.Found {
		found = "yes"
	}
	progressText := fmt.Sprintf("%s %d/%d", console.FormatPercent(task.Estimated, task.TotalKeyspace), task.Estimated, task.TotalKeyspace)
	line := padCell(task.ID, c.id) + " " +
		statusStyle(task.Status).Render(padCell(string(task.Status), c.status)) + " " +
		padCell(task.Mode, c.mode) + " " +
		padCell(task.Hash, c.hash) + " " +
		rightCell(progressText, c.progress) + " " +
		padCell(found, c.found)
	line = taskRowStyle(task).Render(line)
	if selected {
		line = tuiStyles.accent.Render("> ") + line
	} else {
		line = "  " + line
	}
	return truncateCell(line, width)
}

func (m masterTUIModel) renderTaskDetail(width int) string {
	tasks := m.filteredTasks()
	if len(tasks) == 0 {
		return tuiStyles.muted.Render("Select a task to see details.")
	}
	task := tasks[clampIndex(m.taskCursor, len(tasks))]
	lines := []string{
		tuiStyles.section.Render("Task Detail"),
		fmt.Sprintf("%s  status=%s  priority=%d  attempts=%d/%d",
			task.ID,
			statusStyle(task.Status).Render(string(task.Status)),
			task.Priority,
			task.Attempts,
			task.MaxRetries,
		),
		fmt.Sprintf("wordlist=%s  updated=%s ago",
			formatOptional(task.WordlistPath),
			formatAge(m.snapshot.Now, task.UpdatedAt),
		),
	}
	if !task.StartedAt.IsZero() {
		timingParts := fmt.Sprintf("started=%s ago", formatAge(m.snapshot.Now, task.StartedAt))
		if task.CrackDuration > 0 {
			timingParts += fmt.Sprintf("  duration=%s  rate=%s",
				formatDuration(task.CrackDuration),
				console.FormatHashRate(task.CrackRate),
			)
		}
		lines = append(lines, tuiStyles.subtle.Render(timingParts))
	}
	if task.Found {
		pwText := fmt.Sprintf("  ★  PASSWORD FOUND:  %s  ", task.FoundPassword)
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(mochaBase)).
			Background(lipgloss.Color(mochaGreen)).
			Bold(true).
			Render(pwText))
	}
	if task.FailureReason != "" {
		lines = append(lines, tuiStyles.danger.Render("failure="+task.FailureReason))
	}
	return limitBlock(strings.Join(lines, "\n"), width, 7)
}

func (m masterTUIModel) workerTableHeader(width int) string {
	line := padCell("WORKER", 18) + " " +
		padCell("HEALTH", 11) + " " +
		rightCell("CORES", 5) + " " +
		rightCell("ACTIVE", 6) + " " +
		padCell("TASK", 10) + " " +
		rightCell("AVG", 10) + " " +
		rightCell("SEEN", 10)
	return tuiStyles.tableHeader.Render(truncateCell(line, width))
}

func (m masterTUIModel) workerRow(worker tuiWorkerSnapshot, selected bool, width int) string {
	taskID := worker.ActiveTaskID
	if taskID == "" {
		taskID = worker.LastTaskID
	}
	if taskID == "" {
		taskID = "-"
	}
	last := formatAge(m.snapshot.Now, worker.LastSeen)
	line := padCell(worker.ID, 18) + " " +
		healthStyle(worker.Health).Render(padCell(worker.Health, 11)) + " " +
		rightCell(fmt.Sprintf("%d", worker.CPUCores), 5) + " " +
		rightCell(fmt.Sprintf("%d", worker.ActiveChunks), 6) + " " +
		padCell(taskID, 10) + " " +
		rightCell(console.FormatHashRate(worker.AvgRate), 10) + " " +
		rightCell(last, 10)
	if selected {
		line = tuiStyles.accent.Render("> ") + line
	} else {
		line = "  " + line
	}
	return truncateCell(line, width)
}

func (m masterTUIModel) renderETA() string {
	if m.snapshot.TotalRate <= 0 || m.snapshot.Summary.remaining <= 0 {
		return "-"
	}
	eta := float64(m.snapshot.Summary.remaining) / m.snapshot.TotalRate
	return formatDuration(time.Duration(eta * float64(time.Second)))
}

func countFoundTasks(tasks []tuiTaskSnapshot) int {
	count := 0
	for _, task := range tasks {
		if task.Found {
			count++
		}
	}
	return count
}

func countFailedTasks(tasks []tuiTaskSnapshot) int {
	count := 0
	for _, task := range tasks {
		if task.Status == TaskStatusFailed || task.FailureReason != "" {
			count++
		}
	}
	return count
}

func notableTaskResults(tasks []tuiTaskSnapshot) []tuiTaskSnapshot {
	results := make([]tuiTaskSnapshot, 0)
	for i := len(tasks) - 1; i >= 0; i-- {
		if tasks[i].Found || tasks[i].Status == TaskStatusFailed || tasks[i].FailureReason != "" {
			results = append(results, tasks[i])
		}
	}
	return results
}

func resultSummary(task tuiTaskSnapshot) string {
	if task.Found {
		return "password=" + formatOptional(task.FoundPassword)
	}
	if task.FailureReason != "" {
		return "failure=" + task.FailureReason
	}
	return "status=" + string(task.Status)
}

func resultStyle(task tuiTaskSnapshot) lipgloss.Style {
	if task.Found {
		return tuiStyles.success
	}
	if task.Status == TaskStatusFailed || task.FailureReason != "" {
		return tuiStyles.danger
	}
	return tuiStyles.tableCell
}

func taskRowStyle(task tuiTaskSnapshot) lipgloss.Style {
	if task.Found {
		return tuiStyles.success
	}
	if task.Status == TaskStatusFailed || task.FailureReason != "" {
		return tuiStyles.danger
	}
	if task.Status == TaskStatusRunning {
		return tuiStyles.accent
	}
	if task.Status == TaskStatusQueued || task.Status == TaskStatusReviewed || task.Status == TaskStatusApproved {
		return tuiStyles.warn
	}
	return tuiStyles.tableCell
}
