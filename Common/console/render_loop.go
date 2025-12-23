package console

import "time"

type RenderLoop struct {
	renderer *StickyRenderer
	interval time.Duration
	statusFn func() string
	stopCh   chan struct{}
}

func NewRenderLoop(renderer *StickyRenderer, interval time.Duration, statusFn func() string) *RenderLoop {
	return &RenderLoop{
		renderer: renderer,
		interval: interval,
		statusFn: statusFn,
	}
}

func (l *RenderLoop) Start() {
	if l == nil || l.stopCh != nil {
		return
	}
	l.stopCh = make(chan struct{})
	go func() {
		ticker := time.NewTicker(l.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if l.statusFn != nil && l.renderer != nil {
					l.renderer.SetStatus(l.statusFn())
				}
				if l.renderer != nil {
					_ = l.renderer.Flush()
				}
			case <-l.stopCh:
				return
			}
		}
	}()
}

func (l *RenderLoop) Stop() {
	if l == nil || l.stopCh == nil {
		return
	}
	close(l.stopCh)
	l.stopCh = nil
	if l.renderer != nil {
		l.renderer.ClearStatus()
		_ = l.renderer.Flush()
	}
}
