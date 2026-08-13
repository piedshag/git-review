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
	since   time.Time
	frame   int
	started bool
	stopped bool
	done    chan struct{}
}

func newSpinner(writer io.Writer) *spinner {
	return &spinner{writer: writer, done: make(chan struct{})}
}

func (s *spinner) Next(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if s.status != "" {
		s.completeLocked()
	}
	if !s.started {
		s.started = true
		go s.animate()
	}
	s.status = fmt.Sprintf(format, args...)
	s.since = time.Now()
	s.frame = 0
	s.renderLocked()
}

func (s *spinner) Update(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.status == "" {
		return
	}
	s.status = fmt.Sprintf(format, args...)
}

func (s *spinner) Finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if s.status != "" {
		s.completeLocked()
	}
	s.stopLocked()
}

func (s *spinner) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	fmt.Fprint(s.writer, "\r\x1b[2K")
	s.stopLocked()
	s.mu.Unlock()
}

func (s *spinner) stopLocked() {
	s.stopped = true
	if s.started {
		close(s.done)
	}
}

func (s *spinner) completeLocked() {
	fmt.Fprintf(s.writer, "\r\x1b[2K✓ %s (%s)\n", s.status, elapsed(s.since))
	s.status = ""
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
	fmt.Fprintf(s.writer, "\r\x1b[2K%s %s (%s)", frames[s.frame%len(frames)], s.status, elapsed(s.since))
}

func elapsed(start time.Time) time.Duration {
	return time.Since(start).Truncate(time.Second)
}
