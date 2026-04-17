package master

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const envCerberusTUI = "CERBERUS_TUI"

func shouldUseFullscreenTUI(interactive bool, cfg serverConfig) bool {
	if !interactive || cfg.plainMode || !tuiEnvEnabled() {
		return false
	}
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

func tuiEnvEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(envCerberusTUI)))
	switch value {
	case "0", "false", "no", "off", "plain":
		return false
	default:
		return true
	}
}

func runMasterTUI(state *masterState, ui *masterUI, logs *tuiLogStore, cfg serverConfig, serverErr <-chan error) error {
	model := newMasterTUIModel(state, ui, logs, cfg, serverErr)
	program := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	)
	_, err := program.Run()
	return err
}
