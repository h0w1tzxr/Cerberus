package master

import (
	"bytes"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"cracker/Common/console"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	ansi "github.com/charmbracelet/x/ansi"
)

const (
	tuiRefreshInterval     = 250 * time.Millisecond
	tuiMinWidth            = 80
	tuiMinHeight           = 24
	tuiCommandLines        = 4
	tuiFooterHeight        = 1
	tuiCommandHistoryLimit = 100
)

type tuiTab int

const (
	tuiTabDashboard tuiTab = iota
	tuiTabTasks
	tuiTabWorkers
	tuiTabLogs
	tuiTabHelp
)

var tuiTabNames = []string{"Dashboard", "Tasks", "Workers", "Logs", "Help"}

type tuiTaskView int

const (
	tuiTaskViewAll tuiTaskView = iota
	tuiTaskViewFound
	tuiTaskViewFailed
	tuiTaskViewActive
)

var tuiTaskViewNames = []string{"all", "found", "failed", "active"}

type tuiTaskSort int

const (
	tuiTaskSortDefault tuiTaskSort = iota
	tuiTaskSortAZ
	tuiTaskSortZA
	tuiTaskSortFastest
	tuiTaskSortSlowest
	tuiTaskSortRecentlyCracked
)

var tuiTaskSortNames = []string{"default", "name A→Z", "name Z→A", "fastest", "slowest", "newest-cracked"}

type tuiRefreshMsg struct{}

type tuiServerStoppedMsg struct {
	err error
}

type tuiCommandResultMsg struct {
	line            string
	output          string
	err             error
	revealSensitive bool
}

type tuiWorkerDeletedMsg struct {
	workerID string
	deleted  bool
}

type tuiCommandStatus struct {
	message string
	level   uiEventLevel
}

type tuiConfirmation struct {
	message string
	action  func() tea.Cmd
}

type masterTUIModel struct {
	state     *masterState
	ui        *masterUI
	logs      *tuiLogStore
	cfg       serverConfig
	serverErr <-chan error

	width  int
	height int
	tab    tuiTab

	snapshot masterTUISnapshot
	progress progress.Model
	spinner  spinner.Model

	taskCursor       int
	workerCursor     int
	logCursor        int
	taskView         tuiTaskView
	taskSort         tuiTaskSort
	workerDetailOpen bool

	commandMode bool
	filterMode  bool
	command     textinput.Model
	filter      textinput.Model
	commandLog  []string
	commandOpen bool
	history     []string
	historyPos  int
	status      tuiCommandStatus
	confirm     *tuiConfirmation
}

func newMasterTUIModel(state *masterState, ui *masterUI, logs *tuiLogStore, cfg serverConfig, serverErr <-chan error) masterTUIModel {
	command := textinput.New()
	command.Prompt = "cerberus> "
	command.Placeholder = "task list --table"
	command.CharLimit = 512
	command.Width = 72
	command.PromptStyle = tuiStyles.accent
	command.TextStyle = tuiStyles.commandInput

	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "filter tasks"
	filter.CharLimit = 128
	filter.Width = 72
	filter.PromptStyle = tuiStyles.accent
	filter.TextStyle = tuiStyles.commandInput

	spin := spinner.New(spinner.WithSpinner(spinner.Line), spinner.WithStyle(tuiStyles.accent))
	bar := progress.New(
		progress.WithWidth(28),
		progress.WithSolidFill(mochaGreen),
		progress.WithFillCharacters('#', '-'),
		progress.WithSpringOptions(20, 0.85),
	)

	return masterTUIModel{
		state:     state,
		ui:        ui,
		logs:      logs,
		cfg:       cfg,
		serverErr: serverErr,
		progress:  bar,
		spinner:   spin,
		command:   command,
		filter:    filter,
		snapshot:  snapshotMasterTUI(state, ui, logs, cfg.listenAddr),
	}
}

func (m masterTUIModel) Init() tea.Cmd {
	return tea.Batch(
		m.refreshCmd(),
		m.spinner.Tick,
		waitForTUIServerStop(m.serverErr),
	)
}

func (m masterTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeInputs()
		m.resizeProgress()
	case tuiRefreshMsg:
		m.snapshot = snapshotMasterTUI(m.state, m.ui, m.logs, m.cfg.listenAddr)
		m.clampCursors()
		cmds = append(cmds, m.progress.SetPercent(m.overallPercent()), m.refreshCmd())
	case tuiServerStoppedMsg:
		if msg.err != nil {
			m.appendCommandOutput("server", fmt.Sprintf("server stopped: %v", msg.err))
		}
		return m, tea.Quit
	case tuiCommandResultMsg:
		m.commandMode = false
		m.command.Blur()
		output := strings.TrimSpace(msg.output)
		if !msg.revealSensitive {
			output = strings.TrimSpace(redactSensitiveOutput(output))
		}
		if msg.err != nil {
			if output != "" {
				output += "\n"
			}
			output += fmt.Sprintf("[!] %v", msg.err)
			if m.ui != nil {
				m.ui.SetEvent(uiEventError, msg.err.Error())
			}
		} else if output == "" {
			output = "[+] command completed"
			if m.ui != nil {
				m.ui.SetEvent(uiEventSuccess, "Command completed")
			}
		}
		sticky := msg.revealSensitive || (msg.err == nil && hasMultiLineOutput(output))
		if sticky {
			m.appendCommandOutputWithPolicy(msg.line, output, msg.revealSensitive)
		} else {
			m.commandOpen = false
			m.setCommandStatus(commandStatusMessage(output, msg.err), commandStatusLevel(output, msg.err))
		}
		if m.logs != nil {
			m.logs.Append(parseLogLevel(output), output)
		}
	case tuiWorkerDeletedMsg:
		m.snapshot = snapshotMasterTUI(m.state, m.ui, m.logs, m.cfg.listenAddr)
		m.clampCursors()
		if msg.deleted {
			if m.ui != nil {
				m.ui.SetEvent(uiEventWarn, fmt.Sprintf("Deleted worker %s", msg.workerID))
			}
			m.setCommandStatus("Deleted worker "+msg.workerID, uiEventWarn)
		} else {
			m.setCommandStatus("Worker not found: "+msg.workerID, uiEventWarn)
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	case progress.FrameMsg:
		next, cmd := m.progress.Update(msg)
		if nextProgress, ok := next.(progress.Model); ok {
			m.progress = nextProgress
		}
		cmds = append(cmds, cmd)
	case tea.KeyMsg:
		cmd := m.handleKey(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m masterTUIModel) View() string {
	width := m.safeWidth()
	height := m.safeHeight()
	if width < tuiMinWidth || height < tuiMinHeight {
		return tuiStyles.screen.Width(width).Height(height).Render(
			tuiStyles.warn.Render(fmt.Sprintf("Cerberus TUI needs at least %dx%d. Current: %dx%d.", tuiMinWidth, tuiMinHeight, width, height)),
		)
	}

	header := m.renderHeader(width)
	tabs := m.renderTabs(width)
	command := m.renderCommandArea(width)
	footer := m.renderFooter(width)
	bodyHeight := height - lipgloss.Height(header) - lipgloss.Height(tabs) - lipgloss.Height(command) - tuiFooterHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	body := m.renderBody(width, bodyHeight)

	view := lipgloss.JoinVertical(lipgloss.Left, header, tabs, body, command, footer)
	return tuiStyles.screen.Width(width).Height(height).Render(limitBlock(view, width, height))
}

func (m *masterTUIModel) resizeInputs() {
	width := m.safeWidth() - 4
	if width < 24 {
		width = 24
	}
	m.command.Width = width
	m.filter.Width = width
}

func (m *masterTUIModel) resizeProgress() {
	width := m.safeWidth() - 32
	if width > 42 {
		width = 42
	}
	if width < 18 {
		width = 18
	}
	m.progress.Width = width
}

func (m masterTUIModel) refreshCmd() tea.Cmd {
	return tea.Tick(tuiRefreshInterval, func(time.Time) tea.Msg {
		return tuiRefreshMsg{}
	})
}

func waitForTUIServerStop(ch <-chan error) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		return tuiServerStoppedMsg{err: <-ch}
	}
}

func (m *masterTUIModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.confirm != nil {
		return m.handleConfirmKey(msg)
	}
	if m.commandMode {
		return m.handleCommandKey(msg)
	}
	if m.filterMode {
		return m.handleFilterKey(msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.confirm = &tuiConfirmation{
			message: "Gracefully stop Master? y/n",
			action: func() tea.Cmd {
				requestShutdown(m.state, m.ui)
				return nil
			},
		}
	case "right", "l":
		m.nextTab()
	case "left", "h":
		m.prevTab()
	case "down", "j":
		m.moveCursor(1)
	case "up", "k":
		m.moveCursor(-1)
	case "g":
		m.jumpCursor(false)
	case "G":
		m.jumpCursor(true)
	case ":":
		m.openCommand()
		return m.command.Focus()
	case "/":
		m.openFilter()
		return m.filter.Focus()
	case "?":
		m.tab = tuiTabHelp
		m.commandOpen = false
	case "enter":
		if m.tab == tuiTabWorkers {
			m.workerDetailOpen = !m.workerDetailOpen
		}
	case "esc":
		if m.workerDetailOpen {
			m.workerDetailOpen = false
		} else {
			m.commandOpen = false
		}
	case "v":
		if m.tab == tuiTabTasks {
			m.nextTaskView()
		}
	case "o":
		if m.tab == tuiTabTasks {
			m.nextTaskSort()
		}
	case "p":
		if m.state != nil {
			m.state.setDispatchPaused(true)
		}
		if m.ui != nil {
			m.ui.SetEvent(uiEventWarn, "Dispatch paused")
		}
	case "r":
		if m.state != nil {
			m.state.setDispatchPaused(false)
		}
		if m.ui != nil {
			m.ui.SetEvent(uiEventInfo, "Dispatch resumed")
		}
	case "s":
		m.appendSnapshotLog()
	case "d":
		if m.tab == tuiTabWorkers {
			m.confirmDeleteWorker()
		}
	}
	return nil
}

func (m *masterTUIModel) handleConfirmKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y", "enter":
		confirm := m.confirm
		m.confirm = nil
		if confirm != nil && confirm.action != nil {
			return confirm.action()
		}
	case "n", "N", "esc":
		m.confirm = nil
	}
	return nil
}

func (m *masterTUIModel) handleCommandKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		line := strings.TrimSpace(m.command.Value())
		m.command.Reset()
		if line == "" {
			m.commandMode = false
			m.command.Blur()
			return nil
		}
		m.addCommandHistory(line)
		return m.prepareCommand(line)
	case "tab":
		m.autocompleteCommand()
		return nil
	case "up":
		m.recallCommandHistory(-1)
		return nil
	case "down":
		m.recallCommandHistory(1)
		return nil
	case "esc":
		m.commandMode = false
		m.command.Reset()
		m.command.Blur()
		return nil
	default:
		var cmd tea.Cmd
		m.command, cmd = m.command.Update(msg)
		return cmd
	}
}

func (m *masterTUIModel) handleFilterKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter", "esc":
		m.filterMode = false
		m.filter.Blur()
		m.taskCursor = 0
		m.logCursor = 0
		return nil
	default:
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.taskCursor = 0
		m.logCursor = 0
		return cmd
	}
}

func (m *masterTUIModel) prepareCommand(line string) tea.Cmd {
	if isExitCommand(line) {
		m.closeCommandMode()
		m.confirm = &tuiConfirmation{
			message: "Gracefully stop Master? y/n",
			action: func() tea.Cmd {
				requestShutdown(m.state, m.ui)
				return nil
			},
		}
		return nil
	}

	args, err := splitArgs(line)
	if err != nil {
		m.closeCommandMode()
		m.setCommandStatus(fmt.Sprintf("[!] %v", err), uiEventError)
		return nil
	}
	if len(args) == 0 {
		m.closeCommandMode()
		return nil
	}
	if args[0] == "help" || args[0] == "?" {
		args = []string{"-h"}
	}
	if isShellLikeCommand(args) {
		m.closeCommandMode()
		m.setCommandStatus(shellCommandHint, uiEventWarn)
		return nil
	}
	if isUnsupportedTUICommand(args) {
		m.closeCommandMode()
		m.setCommandStatus("stdin-driven commands are not supported inside the TUI. Run them from another terminal.", uiEventWarn)
		return nil
	}
	if dangerous, message := dangerousTUICommand(args); dangerous {
		commandArgs := append([]string(nil), args...)
		m.closeCommandMode()
		m.confirm = &tuiConfirmation{
			message: message + " y/n",
			action: func() tea.Cmd {
				return runTUICommand(line, commandArgs, m.cfg)
			},
		}
		return nil
	}
	return runTUICommand(line, args, m.cfg)
}

func (m *masterTUIModel) closeCommandMode() {
	m.commandMode = false
	m.command.Blur()
	m.command.Reset()
}

func runTUICommand(line string, args []string, cfg serverConfig) tea.Cmd {
	commandArgs := append([]string(nil), args...)
	return func() tea.Msg {
		var out bytes.Buffer
		err := handleCLIWithWriter(argsWithTUIDefaults(commandArgs, cfg.listenAddr), &out)
		return tuiCommandResultMsg{
			line:            line,
			output:          out.String(),
			err:             err,
			revealSensitive: allowsSensitiveDrawerOutput(commandArgs),
		}
	}
}

func allowsSensitiveDrawerOutput(args []string) bool {
	normalized := normalizeArgs(args)
	_, _, remaining, err := parseGlobalFlags(normalized)
	if err != nil || len(remaining) < 3 {
		return false
	}
	return remaining[0] == "token" && remaining[1] == "worker" && remaining[2] == "issue"
}

func argsWithTUIDefaults(args []string, listenAddr string) []string {
	if len(args) == 0 || hasGlobalAddr(args) {
		return args
	}
	addr := adminAddrForListen(listenAddr)
	if addr == "" {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, "--addr", addr)
	out = append(out, args...)
	return out
}

func hasGlobalAddr(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return false
		}
		if arg == "--addr" || arg == "-addr" || strings.HasPrefix(arg, "--addr=") || strings.HasPrefix(arg, "-addr=") {
			return true
		}
		if isGlobalFlag(arg) && !strings.Contains(arg, "=") {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return false
	}
	return false
}

func adminAddrForListen(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil || port == "" {
		return defaultAdminAddress
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

func dangerousTUICommand(args []string) (bool, string) {
	normalized := normalizeArgs(args)
	_, _, remaining, err := parseGlobalFlags(normalized)
	if err != nil || len(remaining) == 0 {
		return false, ""
	}
	switch remaining[0] {
	case "task":
		if len(remaining) < 2 {
			return false, ""
		}
		switch normalizeTaskSubcommand(remaining[1]) {
		case "cancel":
			return true, "Cancel task(s)?"
		case "add-batch":
			return true, "Submit batch task(s)?"
		}
	case "token":
		if len(remaining) >= 3 && remaining[1] == "worker" && remaining[2] == "revoke" {
			return true, "Revoke worker token?"
		}
	}
	return false, ""
}

func isUnsupportedTUICommand(args []string) bool {
	normalized := normalizeArgs(args)
	_, _, remaining, err := parseGlobalFlags(normalized)
	if err != nil || len(remaining) < 2 {
		return false
	}
	if remaining[0] != "task" || normalizeTaskSubcommand(remaining[1]) != "add-batch" {
		return false
	}
	for i := 2; i < len(remaining); i++ {
		arg := remaining[i]
		switch {
		case arg == "--stdin" || arg == "-stdin":
			return true
		case strings.HasPrefix(arg, "--stdin=") || strings.HasPrefix(arg, "-stdin="):
			return true
		case arg == "--file" || arg == "-file":
			if i+1 < len(remaining) && remaining[i+1] == "-" {
				return true
			}
			i++
		case strings.HasPrefix(arg, "--file="):
			if strings.TrimPrefix(arg, "--file=") == "-" {
				return true
			}
		case strings.HasPrefix(arg, "-file="):
			if strings.TrimPrefix(arg, "-file=") == "-" {
				return true
			}
		case arg == "-":
			return true
		}
	}
	return false
}

func (m *masterTUIModel) openCommand() {
	m.commandMode = true
	m.filterMode = false
	m.commandOpen = false
	m.historyPos = len(m.history)
	if strings.HasPrefix(m.status.message, "suggest: ") {
		m.status = tuiCommandStatus{}
	}
	m.command.Reset()
	m.command.Placeholder = "task list --table"
}

func (m *masterTUIModel) openFilter() {
	m.filterMode = true
	m.commandMode = false
	m.commandOpen = false
	if m.tab != tuiTabTasks && m.tab != tuiTabLogs {
		m.tab = tuiTabTasks
	}
}

func (m *masterTUIModel) nextTab() {
	m.commandOpen = false
	m.workerDetailOpen = false
	m.tab++
	if int(m.tab) >= len(tuiTabNames) {
		m.tab = 0
	}
	m.clampCursors()
}

func (m *masterTUIModel) prevTab() {
	m.commandOpen = false
	m.workerDetailOpen = false
	if m.tab == 0 {
		m.tab = tuiTab(len(tuiTabNames) - 1)
		m.clampCursors()
		return
	}
	m.tab--
	m.clampCursors()
}

func (m *masterTUIModel) nextTaskView() {
	m.taskView++
	if int(m.taskView) >= len(tuiTaskViewNames) {
		m.taskView = 0
	}
}

func (m *masterTUIModel) nextTaskSort() {
	m.taskSort++
	if int(m.taskSort) >= len(tuiTaskSortNames) {
		m.taskSort = 0
	}
	m.taskCursor = 0
}

func (m masterTUIModel) taskSortName() string {
	index := int(m.taskSort)
	if index < 0 || index >= len(tuiTaskSortNames) {
		return tuiTaskSortNames[0]
	}
	return tuiTaskSortNames[index]
}

func (m *masterTUIModel) moveCursor(delta int) {
	switch m.tab {
	case tuiTabTasks:
		m.taskCursor += delta
	case tuiTabWorkers:
		m.workerCursor += delta
	case tuiTabLogs:
		m.logCursor += delta
	}
	m.clampCursors()
}

func (m *masterTUIModel) jumpCursor(end bool) {
	switch m.tab {
	case tuiTabTasks:
		if end {
			m.taskCursor = len(m.filteredTasks()) - 1
		} else {
			m.taskCursor = 0
		}
	case tuiTabWorkers:
		if end {
			m.workerCursor = len(m.snapshot.Workers) - 1
		} else {
			m.workerCursor = 0
		}
	case tuiTabLogs:
		if end {
			m.logCursor = len(m.snapshot.Logs) - 1
		} else {
			m.logCursor = 0
		}
	}
	m.clampCursors()
}

func (m *masterTUIModel) clampCursors() {
	m.taskCursor = clampIndex(m.taskCursor, len(m.filteredTasks()))
	m.workerCursor = clampIndex(m.workerCursor, len(m.snapshot.Workers))
	m.logCursor = clampIndex(m.logCursor, len(m.filteredLogs()))
}

func (m masterTUIModel) taskViewName() string {
	index := int(m.taskView)
	if index < 0 || index >= len(tuiTaskViewNames) {
		return tuiTaskViewNames[0]
	}
	return tuiTaskViewNames[index]
}

func clampIndex(index, length int) int {
	if length <= 0 {
		return 0
	}
	if index < 0 {
		return 0
	}
	if index >= length {
		return length - 1
	}
	return index
}

func (m *masterTUIModel) appendSnapshotLog() {
	text := fmt.Sprintf("snapshot tasks=%d workers=%d active=%d progress=%s rate=%s",
		m.snapshot.Summary.totalTasks,
		m.snapshot.WorkerCount,
		m.snapshot.ActiveWorkers,
		console.FormatPercent(m.snapshot.Summary.completed, m.snapshot.Summary.total),
		console.FormatHashRate(m.snapshot.TotalRate),
	)
	m.appendCommandOutput("snapshot", text)
	m.commandOpen = false
	m.setCommandStatus("Snapshot captured", uiEventInfo)
	if m.logs != nil {
		m.logs.Append(uiEventInfo, text)
	}
	if m.ui != nil {
		m.ui.SetEvent(uiEventInfo, "Snapshot captured")
	}
}

func (m *masterTUIModel) confirmDeleteWorker() {
	workerID := m.selectedWorkerID()
	if workerID == "" {
		m.setCommandStatus("No worker selected", uiEventWarn)
		return
	}
	m.commandOpen = false
	m.confirm = &tuiConfirmation{
		message: fmt.Sprintf("Delete worker %s and requeue its active work? y/n", workerID),
		action: func() tea.Cmd {
			state := m.state
			return func() tea.Msg {
				deleted := false
				if state != nil {
					state.mu.Lock()
					deleted = state.deleteWorkerLocked(workerID, time.Now())
					state.mu.Unlock()
				}
				return tuiWorkerDeletedMsg{workerID: workerID, deleted: deleted}
			}
		},
	}
}

func (m masterTUIModel) selectedWorkerID() string {
	if len(m.snapshot.Workers) == 0 {
		return ""
	}
	index := clampIndex(m.workerCursor, len(m.snapshot.Workers))
	return m.snapshot.Workers[index].ID
}

func (m *masterTUIModel) appendCommandOutput(line, output string) {
	m.appendCommandOutputWithPolicy(line, output, false)
}

func (m *masterTUIModel) appendCommandOutputWithPolicy(line, output string, revealSensitive bool) {
	line = strings.TrimSpace(line)
	output = strings.TrimSpace(output)
	if !revealSensitive {
		output = strings.TrimSpace(redactSensitiveOutput(output))
	}
	if output == "" {
		return
	}
	entry := output
	if line != "" {
		entry = "> " + line + "\n" + output
	}
	m.commandLog = append(m.commandLog, entry)
	if len(m.commandLog) > tuiCommandLines {
		m.commandLog = m.commandLog[len(m.commandLog)-tuiCommandLines:]
	}
	m.commandOpen = true
	m.setCommandStatus(commandStatusMessage(output, nil), parseLogLevel(output))
}

func (m *masterTUIModel) setCommandStatus(message string, level uiEventLevel) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	m.status = tuiCommandStatus{message: singleLineStatus(message), level: level}
}

func (m *masterTUIModel) addCommandHistory(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(m.history) == 0 || m.history[len(m.history)-1] != line {
		m.history = append(m.history, line)
	}
	if len(m.history) > tuiCommandHistoryLimit {
		m.history = m.history[len(m.history)-tuiCommandHistoryLimit:]
	}
	m.historyPos = len(m.history)
}

func (m *masterTUIModel) recallCommandHistory(delta int) {
	if len(m.history) == 0 {
		return
	}
	m.historyPos += delta
	if m.historyPos < 0 {
		m.historyPos = 0
	}
	if m.historyPos >= len(m.history) {
		m.historyPos = len(m.history)
		m.command.SetValue("")
		return
	}
	m.command.SetValue(m.history[m.historyPos])
	m.command.CursorEnd()
}

func (m *masterTUIModel) autocompleteCommand() {
	value := m.command.Value()
	candidates := commandCompletions(value)
	if len(candidates) == 0 {
		return
	}
	if len(candidates) == 1 {
		m.command.SetValue(candidates[0])
		m.command.CursorEnd()
		return
	}
	prefix := commonPrefix(candidates)
	if len(prefix) > len(value) {
		m.command.SetValue(prefix)
		m.command.CursorEnd()
		return
	}
	m.setCommandStatus("suggest: "+strings.Join(candidates, "  "), uiEventInfo)
}

func (m masterTUIModel) overallPercent() float64 {
	total := m.snapshot.Summary.total
	if total <= 0 {
		return 0
	}
	value := float64(m.snapshot.Summary.completed) / float64(total)
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func (m masterTUIModel) safeWidth() int {
	if m.width <= 0 {
		return tuiMinWidth
	}
	return m.width
}

func (m masterTUIModel) safeHeight() int {
	if m.height <= 0 {
		return tuiMinHeight
	}
	return m.height
}

func (m masterTUIModel) filteredTasks() []tuiTaskSnapshot {
	filter := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	tasks := make([]tuiTaskSnapshot, 0, len(m.snapshot.Tasks))
	for _, task := range m.snapshot.Tasks {
		if !taskMatchesView(task, m.taskView) {
			continue
		}
		if filter == "" || taskMatchesFilter(task, filter) {
			tasks = append(tasks, task)
		}
	}
	switch m.taskSort {
	case tuiTaskSortAZ:
		sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	case tuiTaskSortZA:
		sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].ID > tasks[j].ID })
	case tuiTaskSortFastest:
		sort.SliceStable(tasks, func(i, j int) bool {
			return lessDurationZeroLast(tasks[i].CrackDuration, tasks[j].CrackDuration, true)
		})
	case tuiTaskSortSlowest:
		sort.SliceStable(tasks, func(i, j int) bool {
			return lessDurationZeroLast(tasks[i].CrackDuration, tasks[j].CrackDuration, false)
		})
	case tuiTaskSortRecentlyCracked:
		sort.SliceStable(tasks, func(i, j int) bool {
			return lessTimeZeroLast(tasks[i].CompletedAt, tasks[j].CompletedAt, false)
		})
	}
	return tasks
}

func lessDurationZeroLast(a, b time.Duration, ascending bool) bool {
	if a == 0 && b == 0 {
		return false
	}
	if a == 0 {
		return false
	}
	if b == 0 {
		return true
	}
	if ascending {
		return a < b
	}
	return a > b
}

func lessTimeZeroLast(a, b time.Time, ascending bool) bool {
	if a.IsZero() && b.IsZero() {
		return false
	}
	if a.IsZero() {
		return false
	}
	if b.IsZero() {
		return true
	}
	if ascending {
		return a.Before(b)
	}
	return a.After(b)
}

func taskMatchesView(task tuiTaskSnapshot, view tuiTaskView) bool {
	switch view {
	case tuiTaskViewFound:
		return task.Found
	case tuiTaskViewFailed:
		return task.Status == TaskStatusFailed || task.FailureReason != ""
	case tuiTaskViewActive:
		return task.Status == TaskStatusQueued ||
			task.Status == TaskStatusReviewed ||
			task.Status == TaskStatusApproved ||
			task.Status == TaskStatusRunning
	default:
		return true
	}
}

func taskMatchesFilter(task tuiTaskSnapshot, filter string) bool {
	if filter == "" {
		return true
	}
	values := []string{
		task.ID,
		task.Hash,
		string(task.Status),
		task.Mode,
		task.WordlistPath,
		task.FoundPassword,
		task.FailureReason,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), filter) {
			return true
		}
	}
	return false
}

func (m masterTUIModel) filteredLogs() []tuiLogEntry {
	filter := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if filter == "" {
		return m.snapshot.Logs
	}
	logs := make([]tuiLogEntry, 0, len(m.snapshot.Logs))
	for _, entry := range m.snapshot.Logs {
		if strings.Contains(strings.ToLower(entry.Message), filter) ||
			strings.Contains(strings.ToLower(entry.At.Format("15:04:05")), filter) {
			logs = append(logs, entry)
		}
	}
	return logs
}

func visibleWindow(cursor, length, height int) (int, int) {
	if height <= 0 || length <= 0 {
		return 0, 0
	}
	if height >= length {
		return 0, length
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	if start+height > length {
		start = length - height
	}
	return start, start + height
}

func padCell(value string, width int) string {
	value = truncateCell(value, width)
	visible := ansi.StringWidth(value)
	if visible >= width {
		return value
	}
	return value + strings.Repeat(" ", width-visible)
}

func rightCell(value string, width int) string {
	value = truncateCell(value, width)
	visible := ansi.StringWidth(value)
	if visible >= width {
		return value
	}
	return strings.Repeat(" ", width-visible) + value
}

func truncateCell(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = strings.ReplaceAll(value, "\n", " ")
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "~")
}

func limitBlock(value string, width, height int) string {
	lines := strings.Split(value, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		if ansi.StringWidth(line) > width {
			lines[i] = truncateCell(line, width)
		}
	}
	return strings.Join(lines, "\n")
}

func statusStyle(status TaskStatus) lipgloss.Style {
	switch status {
	case TaskStatusCompleted:
		return tuiStyles.success
	case TaskStatusFailed, TaskStatusCanceled:
		return tuiStyles.danger
	case TaskStatusRunning:
		return tuiStyles.accent
	case TaskStatusQueued, TaskStatusReviewed:
		return tuiStyles.warn
	default:
		return tuiStyles.tableCell
	}
}

func healthStyle(health string) lipgloss.Style {
	switch health {
	case "healthy":
		return tuiStyles.success
	case "offline":
		return tuiStyles.warn
	default:
		return tuiStyles.danger
	}
}

func eventStyle(level uiEventLevel) lipgloss.Style {
	switch level {
	case uiEventSuccess:
		return tuiStyles.success
	case uiEventWarn:
		return tuiStyles.warn
	case uiEventError:
		return tuiStyles.danger
	default:
		return tuiStyles.accent
	}
}

func commandStatusLevel(output string, err error) uiEventLevel {
	if err != nil {
		return uiEventError
	}
	return parseLogLevel(output)
}

func commandStatusMessage(output string, err error) string {
	if err != nil {
		return "[!] " + err.Error()
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return "[+] command completed"
	}
	return output
}

func hasMultiLineOutput(output string) bool {
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		count++
		if count > 1 {
			return true
		}
	}
	return false
}

func singleLineStatus(message string) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(message), "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func commandCompletions(value string) []string {
	value = strings.TrimLeft(value, " ")
	if value == "" {
		return []string{"task ", "worker ", "token ", "dispatch "}
	}
	candidates := []string{
		"task add --hash ",
		"task add --hash <hash> --mode md5 --wordlist wordlists/test.txt --chunk 2",
		"task add-batch --file ",
		"task list",
		"task list --table",
		"task show ",
		"task cancel ",
		"task retry ",
		"worker list",
		"token worker issue --worker-id ",
		"token worker list",
		"token worker revoke --worker-id ",
		"dispatch pause",
		"dispatch resume",
		"help",
	}
	matches := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, value) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func commonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func formatAge(now, at time.Time) string {
	if at.IsZero() {
		return "-"
	}
	d := now.Sub(at)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + "s"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d.Minutes())) + "m"
	}
	return strconv.Itoa(int(d.Hours())) + "h"
}
