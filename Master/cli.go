package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	pb "cracker/cracker"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultAdminAddress = "localhost:50051"
	defaultRPCTimeout   = 5 * time.Second
)

func handleCLI(args []string) error {
	fs := flag.NewFlagSet("cerberus", flag.ContinueOnError)
	addr := fs.String("addr", defaultAdminAddress, "master gRPC address")
	operator := fs.String("operator", defaultOperator(), "operator id")
	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		return errors.New("missing command (task|worker|dispatch)")
	}

	switch remaining[0] {
	case "task":
		return handleTaskCLI(*addr, *operator, remaining[1:])
	case "worker":
		return handleWorkerCLI(*addr, remaining[1:])
	case "dispatch":
		return handleDispatchCLI(*addr, *operator, remaining[1:])
	default:
		return fmt.Errorf("unknown command %q", remaining[0])
	}
}

func handleTaskCLI(addr, operator string, args []string) error {
	if len(args) == 0 {
		return errors.New("missing task subcommand")
	}

	client, conn, err := dialAdmin(addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	switch args[0] {
	case "add":
		return taskAdd(client, operator, args[1:])
	case "add-batch":
		return taskAddBatch(client, operator, args[1:])
	case "review":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_REVIEW, args[1:])
	case "approve":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_APPROVE, args[1:])
	case "dispatch":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_DISPATCH, args[1:])
	case "cancel":
		return taskCancel(client, operator, args[1:])
	case "retry":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_RETRY, args[1:])
	case "pause":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_PAUSE, args[1:])
	case "resume":
		return taskActionMany(client, operator, pb.TaskAction_TASK_ACTION_RESUME, args[1:])
	case "set-priority":
		return taskSetPriority(client, operator, args[1:])
	case "list":
		return taskList(client, args[1:])
	case "show":
		return taskShow(client, args[1:])
	default:
		return fmt.Errorf("unknown task subcommand %q", args[0])
	}
}

func handleWorkerCLI(addr string, args []string) error {
	if len(args) == 0 {
		return errors.New("missing worker subcommand")
	}
	if args[0] != "list" {
		return fmt.Errorf("unknown worker subcommand %q", args[0])
	}

	client, conn, err := dialAdmin(addr)
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

	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "WORKER\tCORES\tLAST_SEEN\tINFLIGHT\tHEALTH")
	for _, worker := range resp.Workers {
		lastSeen := time.Unix(worker.LastSeenUnix, 0).Format(time.RFC3339)
		fmt.Fprintf(writer, "%s\t%d\t%s\t%d\t%s\n",
			worker.WorkerId, worker.CpuCores, lastSeen, worker.Inflight, worker.Health)
	}
	writer.Flush()
	return nil
}

func handleDispatchCLI(addr, operator string, args []string) error {
	if len(args) == 0 {
		return errors.New("missing dispatch subcommand")
	}

	var paused bool
	switch args[0] {
	case "pause":
		paused = true
	case "resume":
		paused = false
	default:
		return fmt.Errorf("unknown dispatch subcommand %q", args[0])
	}

	client, conn, err := dialAdmin(addr)
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
		fmt.Println("Dispatch paused.")
	} else {
		fmt.Println("Dispatch resumed.")
	}
	return nil
}

func taskAdd(client pb.CrackerAdminClient, operator string, args []string) error {
	fs := flag.NewFlagSet("task add", flag.ContinueOnError)
	hash := fs.String("hash", "", "target hash")
	mode := fs.String("mode", "", "hash mode (md5|sha256)")
	wordlist := fs.String("wordlist", "", "path to wordlist")
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

	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	task, err := client.AddTask(ctx, &pb.TaskSpec{
		Hash:         *hash,
		Mode:         modeProto,
		WordlistPath: strings.TrimSpace(*wordlist),
		ChunkSize:    *chunk,
		Keyspace:     *keyspace,
		Priority:     int32(*priority),
		MaxRetries:   int32(*maxRetries),
	})
	if err != nil {
		return err
	}
	fmt.Printf("Added task %s (%s)\n", task.Id, formatMode(task.Mode))
	return nil
}

func taskAddBatch(client pb.CrackerAdminClient, operator string, args []string) error {
	fs := flag.NewFlagSet("task add-batch", flag.ContinueOnError)
	filePath := fs.String("file", "", "path to hashes file ('-' for stdin)")
	useStdin := fs.Bool("stdin", false, "read hashes from stdin")
	mode := fs.String("mode", "", "hash mode (md5|sha256)")
	wordlist := fs.String("wordlist", "", "path to wordlist")
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

	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	resp, err := client.AddTaskBatch(ctx, &pb.TaskBatchSpec{
		Hashes:       hashes,
		Mode:         modeProto,
		WordlistPath: strings.TrimSpace(*wordlist),
		ChunkSize:    *chunk,
		Keyspace:     *keyspace,
		Priority:     int32(*priority),
		MaxRetries:   int32(*maxRetries),
	})
	if err != nil {
		return err
	}
	fmt.Printf("Added %d task(s).\n", len(resp.Tasks))
	return nil
}

func taskActionMany(client pb.CrackerAdminClient, operator string, action pb.TaskAction, args []string) error {
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
		fmt.Printf("Updated task %s\n", taskID)
	}
	return nil
}

func taskCancel(client pb.CrackerAdminClient, operator string, args []string) error {
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
		fmt.Printf("Canceled task %s\n", taskID)
	}
	return nil
}

func taskSetPriority(client pb.CrackerAdminClient, operator string, args []string) error {
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
		fmt.Printf("Set priority for task %s\n", taskID)
	}
	return nil
}

func taskList(client pb.CrackerAdminClient, args []string) error {
	fs := flag.NewFlagSet("task list", flag.ContinueOnError)
	statuses := fs.String("status", "", "comma-separated statuses (queued,reviewed,approved,running,completed,failed,canceled)")
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

	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tSTATUS\tMODE\tHASH\tWORDLIST\tPROGRESS\tPRIORITY\tDISPATCH\tPAUSED")
	for _, task := range resp.Tasks {
		progress := formatProgress(task.Completed, task.TotalKeyspace)
		wordlist := task.WordlistPath
		if wordlist == "" {
			wordlist = "-"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%t\t%t\n",
			task.Id, formatTaskStatus(task.Status), formatMode(task.Mode), task.Hash, wordlist, progress,
			task.Priority, task.DispatchReady, task.Paused)
	}
	writer.Flush()
	return nil
}

func taskShow(client pb.CrackerAdminClient, args []string) error {
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

	fmt.Printf("Task %s\n", task.Id)
	fmt.Printf("  Status: %s\n", formatTaskStatus(task.Status))
	fmt.Printf("  Hash: %s\n", task.Hash)
	fmt.Printf("  Mode: %s\n", formatMode(task.Mode))
	fmt.Printf("  Wordlist: %s\n", formatOptional(task.WordlistPath))
	fmt.Printf("  Chunk size: %d\n", task.ChunkSize)
	fmt.Printf("  Total keyspace: %d\n", task.TotalKeyspace)
	fmt.Printf("  Completed: %d\n", task.Completed)
	fmt.Printf("  Progress: %s\n", formatProgress(task.Completed, task.TotalKeyspace))
	fmt.Printf("  Next index: %d\n", task.NextIndex)
	fmt.Printf("  Attempts: %d/%d\n", task.Attempts, task.MaxRetries)
	fmt.Printf("  Priority: %d\n", task.Priority)
	fmt.Printf("  Dispatch ready: %t\n", task.DispatchReady)
	fmt.Printf("  Paused: %t\n", task.Paused)
	if task.FoundPassword != "" {
		fmt.Printf("  Found password: %s\n", task.FoundPassword)
	}
	if task.FailureReason != "" {
		fmt.Printf("  Failure reason: %s\n", task.FailureReason)
	}
	fmt.Printf("  Created: %s\n", time.Unix(task.CreatedAtUnix, 0).Format(time.RFC3339))
	fmt.Printf("  Updated: %s\n", time.Unix(task.UpdatedAtUnix, 0).Format(time.RFC3339))
	return nil
}

func dialAdmin(addr string) (pb.CrackerAdminClient, *grpc.ClientConn, error) {
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
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
