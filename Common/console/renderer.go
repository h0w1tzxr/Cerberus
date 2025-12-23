package console

import (
	"bufio"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

const (
	clearLine = "\r\033[2K"
	cursorUp  = "\033[1A"
)

type StickyRenderer struct {
	mu     sync.Mutex
	out    *bufio.Writer
	status string
	active bool
	lines  int
	fd     int
}

func NewStickyRenderer(w io.Writer) *StickyRenderer {
	renderer := &StickyRenderer{out: bufio.NewWriter(w), fd: -1}
	if file, ok := w.(*os.File); ok {
		fd := int(file.Fd())
		if term.IsTerminal(fd) {
			renderer.fd = fd
		}
	}
	return renderer
}

func (r *StickyRenderer) Write(p []byte) (int, error) {
	if r == nil {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active {
		r.clearStatusLocked()
	}
	n, err := r.out.Write(p)
	if err != nil {
		return n, err
	}
	if r.active {
		r.renderStatusLocked()
	}
	return n, nil
}

func (r *StickyRenderer) SetStatus(line string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	width := r.terminalWidth()
	if line != "" {
		line = r.fitToWidth(line, width)
	}
	if line == r.status {
		r.mu.Unlock()
		return
	}
	if line == "" {
		if r.active {
			r.clearStatusLocked()
		}
		r.status = ""
		r.active = false
		r.lines = 0
		r.mu.Unlock()
		return
	}
	if r.active {
		r.clearStatusLocked()
	}
	r.status = line
	r.active = true
	r.lines = countDisplayLines(line, width)
	r.renderStatusLocked()
	r.mu.Unlock()
}

func (r *StickyRenderer) ClearStatus() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.active {
		r.clearStatusLocked()
	}
	r.status = ""
	r.active = false
	r.lines = 0
	r.mu.Unlock()
}

func (r *StickyRenderer) Flush() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.out.Flush()
}

func (r *StickyRenderer) renderStatusLocked() {
	if !r.active {
		return
	}
	lines := strings.Split(r.status, "\n")
	for i, line := range lines {
		_, _ = r.out.WriteString(clearLine)
		_, _ = r.out.WriteString(line)
		if i < len(lines)-1 {
			_, _ = r.out.WriteString("\n")
		}
	}
}

func (r *StickyRenderer) clearStatusLocked() {
	if r.lines <= 1 {
		_, _ = r.out.WriteString(clearLine)
		return
	}
	for i := 0; i < r.lines; i++ {
		_, _ = r.out.WriteString(clearLine)
		if i < r.lines-1 {
			_, _ = r.out.WriteString(cursorUp)
		}
	}
}

func countDisplayLines(line string, width int) int {
	if line == "" {
		return 0
	}
	lines := strings.Split(line, "\n")
	if width <= 0 {
		return len(lines)
	}
	total := 0
	for _, value := range lines {
		visible := visibleWidthANSI(value)
		if visible == 0 {
			total++
			continue
		}
		wrapped := (visible + width - 1) / width
		if wrapped < 1 {
			wrapped = 1
		}
		total += wrapped
	}
	return total
}

func (r *StickyRenderer) fitToWidth(status string, width int) string {
	if width <= 0 {
		return status
	}
	maxWidth := width
	if maxWidth > 1 {
		maxWidth = width - 1
	}
	lines := strings.Split(status, "\n")
	for i, line := range lines {
		lines[i] = trimLineANSI(line, maxWidth)
	}
	return strings.Join(lines, "\n")
}

func (r *StickyRenderer) terminalWidth() int {
	if r == nil || r.fd < 0 {
		return 0
	}
	width, _, err := term.GetSize(r.fd)
	if err != nil || width <= 0 {
		return 0
	}
	return width
}

func trimLineANSI(line string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(line))
	visible := 0
	trimmed := false
	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) {
				ch := line[j]
				if ch >= '@' && ch <= '~' {
					j++
					break
				}
				j++
			}
			b.WriteString(line[i:j])
			i = j
			continue
		}
		if visible >= maxWidth {
			trimmed = true
			break
		}
		b.WriteByte(line[i])
		i++
		visible++
	}
	if trimmed {
		b.WriteString(colorReset)
	}
	return b.String()
}

func visibleWidthANSI(line string) int {
	width := 0
	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) {
				ch := line[j]
				if ch >= '@' && ch <= '~' {
					j++
					break
				}
				j++
			}
			i = j
			continue
		}
		width++
		i++
	}
	return width
}
