package main

import (
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

// Reporter is the single destination for human-readable progress. Review output
// is written separately to stdout so it remains safe to pipe to another tool.
type Reporter interface {
	Next(format string, args ...any)
	Stream(streamStats, time.Time)
	Debug(format string, args ...any)
	Finish()
	Close()
}

func newReporter(writer io.Writer, verbose, debug, interactive bool) Reporter {
	if writer == nil {
		writer = io.Discard
	}
	if verbose || debug {
		return &textReporter{
			logger: log.New(writer, "git-review: ", log.LstdFlags),
			debug:  debug,
		}
	}
	if interactive {
		return &spinnerReporter{spinner: newSpinner(writer)}
	}
	return discardReporter{}
}

type textReporter struct {
	mu         sync.Mutex
	logger     *log.Logger
	debug      bool
	lastUpdate time.Time
}

func (r *textReporter) Next(format string, args ...any) {
	r.logger.Printf(format, args...)
}

func (r *textReporter) Stream(stats streamStats, started time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.lastUpdate.IsZero() && time.Since(r.lastUpdate) < 10*time.Second {
		return
	}
	r.lastUpdate = time.Now()
	message := fmt.Sprintf("stream: received %s in %s", stats.summary(), elapsed(started))
	if stats.Latest != "" {
		message += fmt.Sprintf(", latest %s=%q", stats.LatestKind, preview(stats.Latest, 120))
	}
	r.logger.Print(message)
}

func (r *textReporter) Debug(format string, args ...any) {
	if r.debug {
		r.logger.Printf(format, args...)
	}
}

func (r *textReporter) Finish() {}
func (r *textReporter) Close()  {}

type spinnerReporter struct {
	spinner *spinner
}

func (r *spinnerReporter) Next(format string, args ...any) {
	r.spinner.Next(format, args...)
}

func (r *spinnerReporter) Stream(stats streamStats, started time.Time) {
	r.spinner.Update("Receiving: %d chunks / %s (%s)", stats.Chunks, byteCount(stats.RawBytes), elapsed(started))
}

func (r *spinnerReporter) Debug(string, ...any) {}
func (r *spinnerReporter) Finish()              { r.spinner.Finish() }
func (r *spinnerReporter) Close()               { r.spinner.Stop() }

type discardReporter struct{}

func (discardReporter) Next(string, ...any)           {}
func (discardReporter) Stream(streamStats, time.Time) {}
func (discardReporter) Debug(string, ...any)          {}
func (discardReporter) Finish()                       {}
func (discardReporter) Close()                        {}
