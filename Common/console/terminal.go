package console

import (
	"os"

	"golang.org/x/term"
)

func TerminalSize() (int, int) {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return 0, 0
	}
	width, height, err := term.GetSize(fd)
	if err != nil {
		return 0, 0
	}
	return width, height
}
