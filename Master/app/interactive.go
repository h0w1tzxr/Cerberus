package master

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"cracker/Common/console"

	"golang.org/x/term"
	"google.golang.org/grpc"
)

const consolePrompt = "cerberus> "

func startInteractiveConsole(server *grpc.Server, loop *console.RenderLoop, renderer *console.StickyRenderer, statusFn func() string) {
	if renderer == nil || !isTerminal(os.Stdin) {
		return
	}
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			if loop != nil {
				loop.Pause()
			}
			renderer.ClearStatus()
			_ = renderer.Flush()
			_, _ = renderer.Write([]byte(console.ColorInfo(consolePrompt)))
			_ = renderer.Flush()
			line, err := reader.ReadString('\n')
			if err != nil {
				if server != nil {
					server.GracefulStop()
				}
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				restoreStatus(renderer, loop, statusFn)
				continue
			}
			if isExitCommand(line) {
				if server != nil {
					server.GracefulStop()
				}
				return
			}
			args, parseErr := splitArgs(line)
			if parseErr != nil {
				fmt.Fprintf(renderer, "%s %v\n", console.TagError(), parseErr)
				restoreStatus(renderer, loop, statusFn)
				continue
			}
			if len(args) == 0 {
				restoreStatus(renderer, loop, statusFn)
				continue
			}
			if args[0] == "help" || args[0] == "?" {
				args = []string{"-h"}
			}
			if err := handleCLIWithWriter(args, renderer); err != nil {
				fmt.Fprintf(renderer, "%s %v\n", console.TagError(), err)
			}
			restoreStatus(renderer, loop, statusFn)
		}
	}()
}

func restoreStatus(renderer *console.StickyRenderer, loop *console.RenderLoop, statusFn func() string) {
	if renderer == nil {
		return
	}
	if statusFn != nil {
		renderer.SetStatus(statusFn())
		_ = renderer.Flush()
	}
	if loop != nil {
		loop.Resume()
	}
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func isExitCommand(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "exit", "quit", "q":
		return true
	default:
		return false
	}
}

func splitArgs(input string) ([]string, error) {
	var (
		args      []string
		builder   strings.Builder
		inQuotes  bool
		quoteChar rune
		escaped   bool
	)
	for _, r := range input {
		if escaped {
			builder.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if inQuotes {
			if r == quoteChar {
				inQuotes = false
				continue
			}
			builder.WriteRune(r)
			continue
		}
		if r == '"' || r == '\'' {
			inQuotes = true
			quoteChar = r
			continue
		}
		if unicode.IsSpace(r) {
			if builder.Len() > 0 {
				args = append(args, builder.String())
				builder.Reset()
			}
			continue
		}
		builder.WriteRune(r)
	}
	if escaped || inQuotes {
		return nil, errors.New("unterminated escape or quote")
	}
	if builder.Len() > 0 {
		args = append(args, builder.String())
	}
	return args, nil
}
