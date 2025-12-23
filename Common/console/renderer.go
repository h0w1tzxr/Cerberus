package console

import (
	"bufio"
	"io"
	"sync"
)

const clearLine = "\r\033[2K"

type StickyRenderer struct {
	mu     sync.Mutex
	out    *bufio.Writer
	status string
	active bool
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
		r.mu.Unlock()
		return
	}
	r.status = line
	r.active = true
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
	_, _ = r.out.WriteString(clearLine)
	_, _ = r.out.WriteString(r.status)
}

func (r *StickyRenderer) clearStatusLocked() {
	_, _ = r.out.WriteString(clearLine)
}
