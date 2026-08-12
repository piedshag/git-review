package main

import (
	"fmt"
	"io"
	"sync"
	"time"
)

type spinner struct {
	mu      sync.Mutex
	writer  io.Writer
	status  string
	frame   int
	started bool
	stopped bool
	done    chan struct{}
}

func newSpinner(writer io.Writer) *spinner {
	return &spinner{writer: writer, done: make(chan struct{})}
}

func (s *spinner) Set(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if !s.started {
		s.started = true
		go s.animate()
	}
	s.status = fmt.Sprintf(format, args...)
	s.renderLocked()
}

func (s *spinner) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	if s.started {
		close(s.done)
	}
	fmt.Fprint(s.writer, "\r\x1b[2K")
	s.mu.Unlock()
}

func (s *spinner) animate() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			if !s.stopped && s.status != "" {
				s.frame++
				s.renderLocked()
			}
			s.mu.Unlock()
		case <-s.done:
			return
		}
	}
}

func (s *spinner) renderLocked() {
	frames := [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	fmt.Fprintf(s.writer, "\r\x1b[2K%s %s", frames[s.frame%len(frames)], s.status)
}
