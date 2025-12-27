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

func startInteractiveConsole(server *grpc.Server, loop *console.RenderLoop, renderer *console.StickyRenderer, ui *masterUI, state *masterState) {
	if renderer == nil || !isTerminal(os.Stdin) {
		return
	}
	go func() {
		fd := int(os.Stdin.Fd())
		originalState, err := term.MakeRaw(fd)
		if err != nil {
			return
		}
		defer term.Restore(fd, originalState)

		reader := bufio.NewReader(os.Stdin)
		inCommand := false
		buffer := make([]rune, 0, 64)

		for {
			b, err := reader.ReadByte()
			if err != nil {
				if server != nil {
					server.GracefulStop()
				}
				return
			}

			if !inCommand {
				if handleHotkey(b, state, ui) {
					continue
				}
				if b == ':' || isPrintable(b) {
					inCommand = true
					buffer = buffer[:0]
					enterCommandMode(renderer, loop)
					if b != ':' && isPrintable(b) {
						buffer = append(buffer, rune(b))
						_, _ = renderer.Write([]byte{b})
						_ = renderer.Flush()
					}
				}
				continue
			}

			switch b {
			case '\r', '\n':
				line := strings.TrimSpace(string(buffer))
				buffer = buffer[:0]
				inCommand = false
				if line == "" {
					restoreStatus(renderer, loop, ui)
					continue
				}
				if isExitCommand(line) {
					requestShutdown(state, ui)
					restoreStatus(renderer, loop, ui)
					continue
				}
				args, parseErr := splitArgs(line)
				if parseErr != nil {
					fmt.Fprintf(renderer, "%s %v\n", console.TagError(), parseErr)
					restoreStatus(renderer, loop, ui)
					continue
				}
				if len(args) == 0 {
					restoreStatus(renderer, loop, ui)
					continue
				}
				if args[0] == "help" || args[0] == "?" {
					args = []string{"-h"}
				}
				if err := handleCLIWithWriter(args, renderer); err != nil {
					fmt.Fprintf(renderer, "%s %v\n", console.TagError(), err)
				}
				restoreStatus(renderer, loop, ui)
			case 3, 27:
				buffer = buffer[:0]
				inCommand = false
				restoreStatus(renderer, loop, ui)
			case 127, 8:
				if len(buffer) > 0 {
					buffer = buffer[:len(buffer)-1]
					_, _ = renderer.Write([]byte("\b \b"))
					_ = renderer.Flush()
				}
			default:
				if isPrintable(b) {
					buffer = append(buffer, rune(b))
					_, _ = renderer.Write([]byte{b})
					_ = renderer.Flush()
				}
			}
		}
	}()
}

func enterCommandMode(renderer *console.StickyRenderer, loop *console.RenderLoop) {
	if loop != nil {
		loop.Pause()
	}
	if renderer == nil {
		return
	}
	renderer.ClearStatus()
	_ = renderer.Flush()
	_, _ = renderer.Write([]byte(console.ColorInfo(consolePrompt)))
	_ = renderer.Flush()
}

func restoreStatus(renderer *console.StickyRenderer, loop *console.RenderLoop, ui *masterUI) {
	if renderer == nil {
		return
	}
	if ui != nil {
		renderer.SetStatus(ui.StatusLine())
		_ = renderer.Flush()
	}
	if loop != nil {
		loop.Resume()
	}
}

func handleHotkey(key byte, state *masterState, ui *masterUI) bool {
	switch key {
	case 'p':
		if state != nil {
			state.setDispatchPaused(true)
		}
		if ui != nil {
			ui.SetEvent(uiEventWarn, "Paused - press [r] to resume")
		}
		return true
	case 'r':
		if state != nil {
			state.setDispatchPaused(false)
		}
		if ui != nil {
			ui.SetEvent(uiEventInfo, "Resumed")
		}
		return true
	case 's':
		if ui != nil {
			logBlockInfo(ui.StatusLine())
			ui.SetEvent(uiEventInfo, "Snapshot saved")
		}
		return true
	case 'q':
		requestShutdown(state, ui)
		return true
	default:
		return false
	}
}

func requestShutdown(state *masterState, ui *masterUI) {
	if state == nil {
		return
	}
	state.mu.Lock()
	started := state.requestShutdownLocked()
	state.mu.Unlock()
	if started {
		logInfo("Shutdown requested. Waiting for active chunks to finish.")
		if ui != nil {
			ui.SetEvent(uiEventWarn, "Shutdown requested - waiting for active chunks")
		}
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

func isPrintable(value byte) bool {
	return value >= 32 && value <= 126
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
