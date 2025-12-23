package console

import (
	"bufio"
	"io"
	"strings"
	"sync"
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
}

func NewStickyRenderer(w io.Writer) *StickyRenderer {
	return &StickyRenderer{out: bufio.NewWriter(w)}
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
	r.lines = countLines(line)
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

func countLines(line string) int {
	if line == "" {
		return 0
	}
	return strings.Count(line, "\n") + 1
}
