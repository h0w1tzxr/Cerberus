package master

import (
	"fmt"
	"io"
	"strings"
)

type helpTarget struct {
	command    string
	subcommand string
}

func helpContext(args []string) (helpTarget, bool) {
	helpIndex := -1
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "-h" || arg == "--help" {
			helpIndex = i
			break
		}
	}
	if helpIndex == -1 {
		return helpTarget{}, false
	}
	return parseHelpTarget(args[:helpIndex]), true
}

func parseHelpTarget(args []string) helpTarget {
	var target helpTarget
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if isGlobalFlag(arg) {
			if strings.Contains(arg, "=") {
				continue
			}
			if i+1 < len(args) {
				i++
			}
			continue
		}
		if mapped, ok := commandAliases[arg]; ok {
			target.command = mapped
			if i+1 < len(args) {
				target.subcommand = findSubcommand(args[i+1:], mapped)
			}
			return target
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		target.command = arg
		if i+1 < len(args) {
			target.subcommand = findSubcommand(args[i+1:], arg)
		}
		return target
	}
	return target
}

func findSubcommand(args []string, command string) string {
	var aliases map[string]string
	switch command {
	case "task":
		aliases = taskSubcommandAliases
	case "dispatch":
		aliases = dispatchSubcommandAliases
	case "worker":
		aliases = workerSubcommandAliases
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if mapped, ok := aliases[arg]; ok {
			return mapped
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
}

func renderHelp(out io.Writer, target helpTarget) {
	switch target.command {
	case "task":
		if target.subcommand == "" {
			renderTaskHelp(out)
			return
		}
		renderTaskSubHelp(out, target.subcommand)
	case "worker":
		renderWorkerHelp(out)
	case "dispatch":
		renderDispatchHelp(out)
	default:
		renderGlobalHelp(out)
	}
}

func renderGlobalHelp(out io.Writer) {
	fmt.Fprintln(out, "Cerberus CLI")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  cerberus [--addr ADDR] [--operator ID] <command> [args]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  task       Manage tasks (add, list, show, actions)")
	fmt.Fprintln(out, "  worker     Worker monitoring")
	fmt.Fprintln(out, "  dispatch   Pause/resume global dispatch")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Global flags:")
	fmt.Fprintf(out, "  --addr, -addr        master gRPC address (default %s)\n", defaultAdminAddress)
	fmt.Fprintf(out, "  --operator, -operator operator id (default %s)\n", defaultOperator())
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Shortcuts:")
	fmt.Fprintln(out, "  -t task")
	fmt.Fprintln(out, "  -w worker")
	fmt.Fprintln(out, "  -d dispatch")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Help:")
	fmt.Fprintln(out, "  cerberus -h")
	fmt.Fprintln(out, "  cerberus task -h")
	fmt.Fprintln(out, "  cerberus task add -h")
}

func renderTaskHelp(out io.Writer) {
	fmt.Fprintln(out, "Task commands")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  cerberus task <subcommand> [flags] [args]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Subcommands:")
	fmt.Fprintln(out, "  add            Add a task")
	fmt.Fprintln(out, "  add-batch      Add tasks from file/stdin")
	fmt.Fprintln(out, "  review         Review task(s)")
	fmt.Fprintln(out, "  approve        Approve task(s)")
	fmt.Fprintln(out, "  dispatch       Dispatch task(s)")
	fmt.Fprintln(out, "  cancel         Cancel task(s)")
	fmt.Fprintln(out, "  retry          Retry failed task(s)")
	fmt.Fprintln(out, "  pause          Pause task(s)")
	fmt.Fprintln(out, "  resume         Resume task(s)")
	fmt.Fprintln(out, "  set-priority   Set priority for task(s)")
	fmt.Fprintln(out, "  list           List tasks")
	fmt.Fprintln(out, "  show           Show task details")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Shortcuts:")
	fmt.Fprintln(out, "  -a add")
	fmt.Fprintln(out, "  -b add-batch")
	fmt.Fprintln(out, "  -l list")
	fmt.Fprintln(out, "  -s show")
	fmt.Fprintln(out, "  -d dispatch")
	fmt.Fprintln(out, "  -c cancel")
	fmt.Fprintln(out, "  -p pause")
	fmt.Fprintln(out, "  -u resume")
	fmt.Fprintln(out, "  -r retry")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Help:")
	fmt.Fprintln(out, "  cerberus task add -h")
	fmt.Fprintln(out, "  cerberus task list -h")
	fmt.Fprintln(out, "  cerberus task show -h")
}

func renderTaskSubHelp(out io.Writer, subcommand string) {
	switch subcommand {
	case "add":
		renderTaskAddHelp(out)
	case "add-batch":
		renderTaskAddBatchHelp(out)
	case "list":
		renderTaskListHelp(out)
	case "show":
		renderTaskShowHelp(out)
	case "cancel":
		renderTaskCancelHelp(out)
	case "set-priority":
		renderTaskSetPriorityHelp(out)
	case "review", "approve", "dispatch", "retry", "pause", "resume":
		renderTaskActionHelp(out, subcommand)
	default:
		renderTaskHelp(out)
	}
}

func renderTaskAddHelp(out io.Writer) {
	fmt.Fprintln(out, "Task add")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  cerberus task add --hash <hash> --mode md5|sha256 [--wordlist path] [--keyspace N] [--chunk N] [--priority N] [--max-retries N] [-o output.txt]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Flags:")
	fmt.Fprintln(out, "  --hash         target hash (required)")
	fmt.Fprintln(out, "  --mode         hash mode: md5 or sha256 (required)")
	fmt.Fprintln(out, "  --wordlist     path to wordlist (optional)")
	fmt.Fprintln(out, "  -o, --output   output file (one line per hash)")
	fmt.Fprintf(out, "  --keyspace     total keyspace size (default %d)\n", DefaultKeyspace)
	fmt.Fprintf(out, "  --chunk        chunk size (default %d)\n", DefaultChunkSize)
	fmt.Fprintln(out, "  --priority     priority (higher is sooner)")
	fmt.Fprintf(out, "  --max-retries  max retries (default %d)\n", DefaultMaxRetries)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Notes:")
	fmt.Fprintln(out, "  If --wordlist is set, --keyspace is ignored.")
}

func renderTaskAddBatchHelp(out io.Writer) {
	fmt.Fprintln(out, "Task add-batch")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  cerberus task add-batch --file hashes.txt --mode md5|sha256 [--wordlist path] [--keyspace N] [--chunk N] [--priority N] [--max-retries N] [-o output.txt]")
	fmt.Fprintln(out, "  cerberus task add-batch --stdin --mode md5|sha256 [--wordlist path] [--keyspace N] [--chunk N] [--priority N] [--max-retries N] [-o output.txt]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Flags:")
	fmt.Fprintln(out, "  --file         path to hashes file ('-' for stdin)")
	fmt.Fprintln(out, "  --stdin        read hashes from stdin")
	fmt.Fprintln(out, "  --mode         hash mode: md5 or sha256 (required)")
	fmt.Fprintln(out, "  --wordlist     path to wordlist (optional)")
	fmt.Fprintln(out, "  -o, --output   output file (one line per hash)")
	fmt.Fprintf(out, "  --keyspace     total keyspace size (default %d)\n", DefaultKeyspace)
	fmt.Fprintf(out, "  --chunk        chunk size (default %d)\n", DefaultChunkSize)
	fmt.Fprintln(out, "  --priority     priority (higher is sooner)")
	fmt.Fprintf(out, "  --max-retries  max retries (default %d)\n", DefaultMaxRetries)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Notes:")
	fmt.Fprintln(out, "  If --wordlist is set, --keyspace is ignored.")
}

func renderTaskListHelp(out io.Writer) {
	fmt.Fprintln(out, "Task list")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  cerberus task list [--status queued,reviewed,approved,running,completed,failed,canceled]")
}

func renderTaskShowHelp(out io.Writer) {
	fmt.Fprintln(out, "Task show")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  cerberus task show <task-id>")
}

func renderTaskCancelHelp(out io.Writer) {
	fmt.Fprintln(out, "Task cancel")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  cerberus task cancel [--reason text] <task-id> [task-id...]")
}

func renderTaskSetPriorityHelp(out io.Writer) {
	fmt.Fprintln(out, "Task set-priority")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  cerberus task set-priority --priority N <task-id> [task-id...]")
}

func renderTaskActionHelp(out io.Writer, action string) {
	fmt.Fprintf(out, "Task %s\n\n", action)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintf(out, "  cerberus task %s <task-id> [task-id...]\n", action)
}

func renderWorkerHelp(out io.Writer) {
	fmt.Fprintln(out, "Worker commands")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  cerberus worker list")
	fmt.Fprintln(out, "  cerberus worker -l")
}

func renderDispatchHelp(out io.Writer) {
	fmt.Fprintln(out, "Dispatch commands")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  cerberus dispatch pause")
	fmt.Fprintln(out, "  cerberus dispatch resume")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Shortcuts:")
	fmt.Fprintln(out, "  cerberus dispatch -p")
	fmt.Fprintln(out, "  cerberus dispatch -r")
}
