package master

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"cracker/Common/security"
	pb "cracker/cracker"

	"google.golang.org/grpc"
)

const (
	defaultAdminAddress = "localhost:50051"
	defaultRPCTimeout   = 5 * time.Second
)

func handleCLI(args []string) error {
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	return handleCLIWithWriter(args, writer)
}

func handleCLIWithWriter(args []string, out io.Writer) error {
	if target, ok := helpContext(args); ok {
		renderHelp(out, target)
		return nil
	}

	args = normalizeArgs(args)
	cfg, operator, remaining, err := parseGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return errors.New("missing command (task|worker|token|dispatch)")
	}

	switch remaining[0] {
	case "task":
		return handleTaskCLI(cfg, operator, remaining[1:], out)
	case "worker":
		return handleWorkerCLI(cfg, remaining[1:], out)
	case "token":
		return handleTokenCLI(remaining[1:], out)
	case "dispatch":
		return handleDispatchCLI(cfg, operator, remaining[1:], out)
	default:
		return fmt.Errorf("unknown command %q", remaining[0])
	}
}

func handleTokenCLI(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("missing token subcommand")
	}
	if args[0] != "worker" {
		return fmt.Errorf("unknown token subcommand %q", args[0])
	}
	if len(args) < 2 {
		return errors.New("missing worker token action")
	}
	path, err := security.DefaultWorkerTokenStorePath()
	if err != nil {
		return err
	}
	switch args[1] {
	case "issue":
		fs := flag.NewFlagSet("token worker issue", flag.ContinueOnError)
		workerID := fs.String("worker-id", "", "worker id")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		id, err := validateWorkerID(*workerID)
		if err != nil {
			return err
		}
		token, err := security.IssueWorkerToken(path, id, true)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Issued worker token for %s\n", id)
		fmt.Fprintf(out, "%s=%s\n", security.EnvWorkerID, id)
		fmt.Fprintf(out, "%s=%s\n", security.EnvWorkerToken, token)
		return nil
	case "list":
		records, err := security.ListWorkerTokens(path)
		if err != nil {
			return err
		}
		writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "WORKER\tCREATED\tSTATUS")
		for _, record := range records {
			status := "active"
			if record.RevokedAtUnix != 0 {
				status = "revoked"
			}
			created := time.Unix(record.CreatedAtUnix, 0).Format(time.RFC3339)
			fmt.Fprintf(writer, "%s\t%s\t%s\n", record.WorkerID, created, status)
		}
		writer.Flush()
		return nil
	case "revoke":
		fs := flag.NewFlagSet("token worker revoke", flag.ContinueOnError)
		workerID := fs.String("worker-id", "", "worker id")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		id, err := validateWorkerID(*workerID)
		if err != nil {
			return err
		}
		revoked, err := security.RevokeWorkerToken(path, id)
		if err != nil {
			return err
		}
		if !revoked {
			return fmt.Errorf("no active token found for %s", id)
		}
		fmt.Fprintf(out, "Revoked worker token for %s\n", id)
		return nil
	default:
		return fmt.Errorf("unknown worker token action %q", args[1])
	}
}

func parseGlobalFlags(args []string) (clientConfig, string, []string, error) {
	fs := flag.NewFlagSet("cerberus", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", defaultAdminAddress, "master gRPC address")
	operator := fs.String("operator", defaultOperator(), "operator id")
	token := fs.String("token", "", "admin auth token")
	tlsCA := fs.String("tls-ca", "", "path to TLS CA certificate")
	tlsServerName := fs.String("tls-server-name", "", "TLS server name override")
	if err := fs.Parse(args); err != nil {
		return clientConfig{}, "", nil, err
	}

	cfg := clientConfig{
		addr:          strings.TrimSpace(*addr),
		token:         strings.TrimSpace(*token),
		tlsCA:         strings.TrimSpace(*tlsCA),
		tlsServerName: strings.TrimSpace(*tlsServerName),
	}
	return cfg, *operator, fs.Args(), nil
}

func handleTaskCLI(cfg clientConfig, operator string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("missing task subcommand")
	}
	args[0] = normalizeTaskSubcommand(args[0])

	client, conn, err := dialAdmin(cfg)
	if err != nil {
		return err
	}
	defer conn.Close()

	switch args[0] {
	case "add":
		return taskAdd(client, args[1:], out)
	case "add-batch":
		return taskAddBatch(client, args[1:], out)
	case "review":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_REVIEW, args[1:], out)
	case "approve":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_APPROVE, args[1:], out)
	case "dispatch":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_DISPATCH, args[1:], out)
	case "cancel":
		return taskCancel(client, operator, args[1:], out)
	case "retry":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_RETRY, args[1:], out)
	case "pause":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_PAUSE, args[1:], out)
	case "resume":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_RESUME, args[1:], out)
	case "set-priority":
		return taskSetPriority(client, operator, args[1:], out)
	case "list":
		return taskList(client, args[1:], out)
	case "show":
		return taskShow(client, args[1:], out)
	default:
		return fmt.Errorf("unknown task subcommand %q", args[0])
	}
}

func handleWorkerCLI(cfg clientConfig, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("missing worker subcommand")
	}
	if args[0] == "-l" {
		args[0] = "list"
	}
	if args[0] != "list" {
		return fmt.Errorf("unknown worker subcommand %q", args[0])
	}

	client, conn, err := dialAdmin(cfg)
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	resp, err := client.ListWorkers(ctx, &pb.WorkerListRequest{})
	if err != nil {
		return err
	}

	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "WORKER\tCORES\tLAST_SEEN\tINFLIGHT\tHEALTH")
	for _, worker := range resp.Workers {
		lastSeen := time.Unix(worker.LastSeenUnix, 0).Format(time.RFC3339)
		fmt.Fprintf(writer, "%s\t%d\t%s\t%d\t%s\n",
			worker.WorkerId, worker.CpuCores, lastSeen, worker.Inflight, worker.Health)
	}
	writer.Flush()
	return nil
}

func handleDispatchCLI(cfg clientConfig, operator string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("missing dispatch subcommand")
	}

	var paused bool
	switch normalizeDispatchSubcommand(args[0]) {
	case "pause":
		paused = true
	case "resume":
		paused = false
	default:
		return fmt.Errorf("unknown dispatch subcommand %q", args[0])
	}

	client, conn, err := dialAdmin(cfg)
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	resp, err := client.SetDispatchPaused(ctx, &pb.DispatchControlRequest{
		Paused:   paused,
		Operator: operator,
	})
	if err != nil {
		return err
	}

	if resp.Paused {
		fmt.Fprintln(out, "Dispatch paused.")
	} else {
		fmt.Fprintln(out, "Dispatch resumed.")
	}
	return nil
}

func taskAdd(client pb.CrackerAdminClient, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("task add", flag.ContinueOnError)
	hash := fs.String("hash", "", "target hash")
	mode := fs.String("mode", "", "hash mode (md5|sha256)")
	wordlist := fs.String("wordlist", "", "path to wordlist")
	output := fs.String("output", "", "output file (one line per hash)")
	outputShort := fs.String("o", "", "output file (one line per hash)")
	chunk := fs.Int64("chunk", DefaultChunkSize, "chunk size")
	keyspace := fs.Int64("keyspace", DefaultKeyspace, "total keyspace size (ignored for wordlist)")
	priority := fs.Int("priority", 0, "priority (higher is sooner)")
	maxRetries := fs.Int("max-retries", DefaultMaxRetries, "max retries")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *hash == "" {
		return errors.New("hash is required")
	}
	modeProto, err := parseModeProto(*mode)
	if err != nil {
		return err
	}
	outputPath := strings.TrimSpace(*output)
	if outputPath == "" {
		outputPath = strings.TrimSpace(*outputShort)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	task, err := client.AddTask(ctx, &pb.TaskSpec{
		Hash:         *hash,
		Mode:         modeProto,
		WordlistPath: strings.TrimSpace(*wordlist),
		OutputPath:   outputPath,
		ChunkSize:    *chunk,
		Keyspace:     *keyspace,
		Priority:     int32(*priority),
		MaxRetries:   int32(*maxRetries),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Added task %s (%s) and queued for dispatch.\n", task.Id, formatMode(task.Mode))
	return nil
}

func taskAddBatch(client pb.CrackerAdminClient, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("task add-batch", flag.ContinueOnError)
	filePath := fs.String("file", "", "path to hashes file ('-' for stdin)")
	useStdin := fs.Bool("stdin", false, "read hashes from stdin")
	mode := fs.String("mode", "", "hash mode (md5|sha256)")
	wordlist := fs.String("wordlist", "", "path to wordlist")
	output := fs.String("output", "", "output file (one line per hash)")
	outputShort := fs.String("o", "", "output file (one line per hash)")
	chunk := fs.Int64("chunk", DefaultChunkSize, "chunk size")
	keyspace := fs.Int64("keyspace", DefaultKeyspace, "total keyspace size (ignored for wordlist)")
	priority := fs.Int("priority", 0, "priority (higher is sooner)")
	maxRetries := fs.Int("max-retries", DefaultMaxRetries, "max retries")
	if err := fs.Parse(args); err != nil {
		return err
	}

	hashes, err := readHashes(*filePath, *useStdin)
	if err != nil {
		return err
	}
	modeProto, err := parseModeProto(*mode)
	if err != nil {
		return err
	}
	outputPath := strings.TrimSpace(*output)
	if outputPath == "" {
		outputPath = strings.TrimSpace(*outputShort)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	resp, err := client.AddTaskBatch(ctx, &pb.TaskBatchSpec{
		Hashes:       hashes,
		Mode:         modeProto,
		WordlistPath: strings.TrimSpace(*wordlist),
		OutputPath:   outputPath,
		ChunkSize:    *chunk,
		Keyspace:     *keyspace,
		Priority:     int32(*priority),
		MaxRetries:   int32(*maxRetries),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Added %d task(s) and queued for dispatch.\n", len(resp.Tasks))
	return nil
}

func taskActionMany(client pb.CrackerAdminClient, operator string, action pb.TaskAction, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("task id is required")
	}
	for _, taskID := range args {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
		_, err := client.ApplyTaskAction(ctx, &pb.TaskActionRequest{
			TaskId:   taskID,
			Action:   action,
			Operator: operator,
		})
		cancel()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Updated task %s\n", taskID)
	}
	return nil
}

func taskCancel(client pb.CrackerAdminClient, operator string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("task cancel", flag.ContinueOnError)
	reason := fs.String("reason", "", "cancel reason")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ids := fs.Args()
	if len(ids) == 0 {
		return errors.New("task id is required")
	}
	for _, taskID := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
		_, err := client.ApplyTaskAction(ctx, &pb.TaskActionRequest{
			TaskId:   taskID,
			Action:   pb.TaskAction_TASK_ACTION_CANCEL,
			Operator: operator,
			Reason:   strings.TrimSpace(*reason),
		})
		cancel()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Canceled task %s\n", taskID)
	}
	return nil
}

func taskSetPriority(client pb.CrackerAdminClient, operator string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("task set-priority", flag.ContinueOnError)
	priority := fs.Int("priority", 0, "priority (higher is sooner)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ids := fs.Args()
	if len(ids) == 0 {
		return errors.New("task id is required")
	}
	for _, taskID := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
		_, err := client.ApplyTaskAction(ctx, &pb.TaskActionRequest{
			TaskId:   taskID,
			Action:   pb.TaskAction_TASK_ACTION_SET_PRIORITY,
			Operator: operator,
			Priority: int32(*priority),
		})
		cancel()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Set priority for task %s\n", taskID)
	}
	return nil
}

func taskList(client pb.CrackerAdminClient, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("task list", flag.ContinueOnError)
	statuses := fs.String("status", "", "comma-separated statuses (queued,reviewed,approved,running,completed,failed,canceled)")
	table := fs.Bool("table", false, "print the detailed task table")
	var limit optionalLimitFlag
	fs.Var(&limit, "limit", "print at most N task rows; implies --table")
	if err := fs.Parse(args); err != nil {
		return err
	}

	statusFilter, err := parseStatusFilter(*statuses)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	resp, err := client.ListTasks(ctx, &pb.TaskListRequest{StatusFilter: statusFilter})
	if err != nil {
		return err
	}
	renderTaskList(resp.Tasks, taskListRenderOptions{
		table:    *table || limit.set,
		limit:    limit.value,
		hasLimit: limit.set,
	}, out)
	return nil
}

func taskShow(client pb.CrackerAdminClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("task id is required")
	}
	taskID := strings.TrimSpace(args[0])
	if taskID == "" {
		return errors.New("task id is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	task, err := client.GetTask(ctx, &pb.TaskActionRequest{TaskId: taskID})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Task %s\n", task.Id)
	fmt.Fprintf(out, "  Status: %s\n", formatTaskStatus(task.Status))
	fmt.Fprintf(out, "  Hash: %s\n", task.Hash)
	fmt.Fprintf(out, "  Mode: %s\n", formatMode(task.Mode))
	fmt.Fprintf(out, "  Wordlist: %s\n", formatOptional(task.WordlistPath))
	fmt.Fprintf(out, "  Chunk size: %d\n", task.ChunkSize)
	fmt.Fprintf(out, "  Total keyspace: %d\n", task.TotalKeyspace)
	fmt.Fprintf(out, "  Completed: %d\n", task.Completed)
	fmt.Fprintf(out, "  Progress: %s\n", formatProgress(task.Completed, task.TotalKeyspace))
	fmt.Fprintf(out, "  Next index: %d\n", task.NextIndex)
	fmt.Fprintf(out, "  Attempts: %d/%d\n", task.Attempts, task.MaxRetries)
	fmt.Fprintf(out, "  Priority: %d\n", task.Priority)
	fmt.Fprintf(out, "  Dispatch ready: %t\n", task.DispatchReady)
	fmt.Fprintf(out, "  Paused: %t\n", task.Paused)
	if task.ReviewedBy != "" {
		fmt.Fprintf(out, "  Reviewed by: %s\n", task.ReviewedBy)
	}
	if task.ApprovedBy != "" {
		fmt.Fprintf(out, "  Approved by: %s\n", task.ApprovedBy)
	}
	if task.CanceledBy != "" {
		fmt.Fprintf(out, "  Canceled by: %s\n", task.CanceledBy)
	}
	if task.FoundPassword != "" {
		if security.RevealPasswords() {
			fmt.Fprintf(out, "  Found password: %s\n", security.SanitizeLogValue(task.FoundPassword, 300))
		} else {
			fmt.Fprintln(out, "  Found password: <hidden; set CERBERUS_REVEAL_PASSWORDS=1 to display>")
		}
	}
	if task.FailureReason != "" {
		fmt.Fprintf(out, "  Failure reason: %s\n", task.FailureReason)
	}
	fmt.Fprintf(out, "  Created: %s\n", time.Unix(task.CreatedAtUnix, 0).Format(time.RFC3339))
	fmt.Fprintf(out, "  Updated: %s\n", time.Unix(task.UpdatedAtUnix, 0).Format(time.RFC3339))
	return nil
}

func dialAdmin(cfg clientConfig) (pb.CrackerAdminClient, *grpc.ClientConn, error) {
	options, err := clientDialOptions(cfg, true)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(cfg.addr, options...)
	if err != nil {
		return nil, nil, err
	}
	conn.Connect()
	return pb.NewCrackerAdminClient(conn), conn, nil
}

func defaultOperator() string {
	if value := strings.TrimSpace(os.Getenv("USER")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("USERNAME")); value != "" {
		return value
	}
	return "operator"
}

func parseModeProto(value string) (pb.HashMode, error) {
	mode, err := parseHashMode(value)
	if err != nil {
		return pb.HashMode_HASH_MODE_UNSPECIFIED, err
	}
	return hashModeToProto(mode), nil
}

func parseStatusFilter(value string) ([]pb.TaskStatus, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	statuses := make([]pb.TaskStatus, 0, len(parts))
	for _, part := range parts {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "queued":
			statuses = append(statuses, pb.TaskStatus_TASK_STATUS_QUEUED)
		case "reviewed":
			statuses = append(statuses, pb.TaskStatus_TASK_STATUS_REVIEWED)
		case "approved":
			statuses = append(statuses, pb.TaskStatus_TASK_STATUS_APPROVED)
		case "running":
			statuses = append(statuses, pb.TaskStatus_TASK_STATUS_RUNNING)
		case "completed":
			statuses = append(statuses, pb.TaskStatus_TASK_STATUS_COMPLETED)
		case "failed":
			statuses = append(statuses, pb.TaskStatus_TASK_STATUS_FAILED)
		case "canceled", "cancelled":
			statuses = append(statuses, pb.TaskStatus_TASK_STATUS_CANCELED)
		case "":
			continue
		default:
			return nil, fmt.Errorf("unknown status %q", part)
		}
	}
	return statuses, nil
}

func formatMode(mode pb.HashMode) string {
	switch mode {
	case pb.HashMode_HASH_MODE_MD5:
		return "md5"
	case pb.HashMode_HASH_MODE_SHA256:
		return "sha256"
	default:
		return "unspecified"
	}
}

func formatTaskStatus(status pb.TaskStatus) string {
	switch status {
	case pb.TaskStatus_TASK_STATUS_QUEUED:
		return "queued"
	case pb.TaskStatus_TASK_STATUS_REVIEWED:
		return "reviewed"
	case pb.TaskStatus_TASK_STATUS_APPROVED:
		return "approved"
	case pb.TaskStatus_TASK_STATUS_RUNNING:
		return "running"
	case pb.TaskStatus_TASK_STATUS_COMPLETED:
		return "completed"
	case pb.TaskStatus_TASK_STATUS_FAILED:
		return "failed"
	case pb.TaskStatus_TASK_STATUS_CANCELED:
		return "canceled"
	default:
		return "unknown"
	}
}

func readHashes(filePath string, useStdin bool) ([]string, error) {
	if useStdin || filePath == "-" {
		return readLinesFromReader(os.Stdin)
	}
	if filePath == "" {
		return nil, errors.New("file path or --stdin is required")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLinesFromReader(file)
}

func readLinesFromReader(reader io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	lines := make([]string, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, errors.New("no hashes provided")
	}
	return lines, nil
}

func formatOptional(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func formatProgress(completed, total int64) string {
	if total == 0 {
		return "0%"
	}
	percent := float64(completed) / float64(total) * 100
	return fmt.Sprintf("%.2f%%", percent)
}

func formatProgressWithCounts(completed, total int64) string {
	return fmt.Sprintf("%s (%d/%d)", formatProgress(completed, total), completed, total)
}

type taskListSummary struct {
	total               int
	queued              int
	reviewed            int
	approved            int
	running             int
	completed           int
	failed              int
	canceled            int
	found               int
	completedNoPassword int
	dispatchReady       int
	paused              int
}

func summarizeTasks(tasks []*pb.Task) taskListSummary {
	summary := taskListSummary{total: len(tasks)}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		switch task.Status {
		case pb.TaskStatus_TASK_STATUS_QUEUED:
			summary.queued++
		case pb.TaskStatus_TASK_STATUS_REVIEWED:
			summary.reviewed++
		case pb.TaskStatus_TASK_STATUS_APPROVED:
			summary.approved++
		case pb.TaskStatus_TASK_STATUS_RUNNING:
			summary.running++
		case pb.TaskStatus_TASK_STATUS_COMPLETED:
			summary.completed++
			if !task.Found {
				summary.completedNoPassword++
			}
		case pb.TaskStatus_TASK_STATUS_FAILED:
			summary.failed++
		case pb.TaskStatus_TASK_STATUS_CANCELED:
			summary.canceled++
		}
		if task.Found {
			summary.found++
		}
		if task.DispatchReady {
			summary.dispatchReady++
		}
		if task.Paused {
			summary.paused++
		}
	}
	return summary
}

type taskListRenderOptions struct {
	table    bool
	limit    int
	hasLimit bool
}

func renderTaskList(tasks []*pb.Task, options taskListRenderOptions, out io.Writer) {
	if len(tasks) == 0 {
		fmt.Fprintln(out, "No tasks found.")
		return
	}
	summary := summarizeTasks(tasks)
	fmt.Fprintf(out, "Total: %d | queued:%d reviewed:%d approved:%d running:%d completed:%d failed:%d canceled:%d | found:%d completed-no-password:%d | dispatch-ready:%d paused:%d\n",
		summary.total, summary.queued, summary.reviewed, summary.approved, summary.running,
		summary.completed, summary.failed, summary.canceled, summary.found, summary.completedNoPassword,
		summary.dispatchReady, summary.paused)
	if !options.table {
		return
	}

	rows := tasks
	if options.hasLimit && options.limit < len(rows) {
		rows = rows[:options.limit]
	}
	if options.hasLimit {
		fmt.Fprintf(out, "Showing %d of %d matching tasks.\n", len(rows), len(tasks))
	}

	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tSTATUS\tMODE\tHASH\tWORDLIST\tPROGRESS\tATTEMPTS\tFOUND\tPRIORITY\tDISPATCH\tPAUSED")
	for _, task := range rows {
		progress := formatProgressWithCounts(task.Completed, task.TotalKeyspace)
		wordlist := task.WordlistPath
		if wordlist == "" {
			wordlist = "-"
		}
		attempts := fmt.Sprintf("%d/%d", task.Attempts, task.MaxRetries)
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%t\t%d\t%t\t%t\n",
			task.Id, formatTaskStatus(task.Status), formatMode(task.Mode), task.Hash, wordlist, progress,
			attempts, task.Found, task.Priority, task.DispatchReady, task.Paused)
	}
	writer.Flush()
}

type optionalLimitFlag struct {
	value int
	set   bool
}

func (f *optionalLimitFlag) String() string {
	if f == nil || !f.set {
		return ""
	}
	return strconv.Itoa(f.value)
}

func (f *optionalLimitFlag) Set(value string) error {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("limit must be an integer")
	}
	if parsed < 0 {
		return fmt.Errorf("limit must be non-negative")
	}
	f.value = parsed
	f.set = true
	return nil
}

func normalizeArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	normalized := make([]string, len(args))
	copy(normalized, args)
	for i := 0; i < len(normalized); i++ {
		arg := normalized[i]
		if arg == "--" {
			break
		}
		if isGlobalFlag(arg) {
			if strings.Contains(arg, "=") {
				continue
			}
			if i+1 < len(normalized) {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if cmd, ok := commandAliases[arg]; ok {
				normalized[i] = cmd
				break
			}
			continue
		}
		break
	}
	return normalized
}

func isGlobalFlag(arg string) bool {
	switch arg {
	case "--addr", "-addr", "--operator", "-operator", "--token", "-token", "--tls-ca", "-tls-ca", "--tls-server-name", "-tls-server-name":
		return true
	}
	return strings.HasPrefix(arg, "--addr=") ||
		strings.HasPrefix(arg, "-addr=") ||
		strings.HasPrefix(arg, "--operator=") ||
		strings.HasPrefix(arg, "-operator=") ||
		strings.HasPrefix(arg, "--token=") ||
		strings.HasPrefix(arg, "-token=") ||
		strings.HasPrefix(arg, "--tls-ca=") ||
		strings.HasPrefix(arg, "-tls-ca=") ||
		strings.HasPrefix(arg, "--tls-server-name=") ||
		strings.HasPrefix(arg, "-tls-server-name=")
}

func normalizeTaskSubcommand(value string) string {
	if mapped, ok := taskSubcommandAliases[value]; ok {
		return mapped
	}
	return value
}

func normalizeDispatchSubcommand(value string) string {
	if mapped, ok := dispatchSubcommandAliases[value]; ok {
		return mapped
	}
	return value
}

var commandAliases = map[string]string{
	"-t": "task",
	"-w": "worker",
	"-d": "dispatch",
}

var taskSubcommandAliases = map[string]string{
	"-a": "add",
	"-b": "add-batch",
	"-l": "list",
	"-s": "show",
	"-d": "dispatch",
	"-c": "cancel",
	"-p": "pause",
	"-u": "resume",
	"-r": "retry",
}

var dispatchSubcommandAliases = map[string]string{
	"-p": "pause",
	"-r": "resume",
}

var workerSubcommandAliases = map[string]string{
	"-l": "list",
}
