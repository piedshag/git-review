package report

import (
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/piedshag/git-review/internal/agent"
)

func New(writer io.Writer, verbose, debug, interactive bool) agent.Reporter {
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
	return agent.NopReporter{}
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

func (r *textReporter) Stream(stats agent.StreamStats, started time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.lastUpdate.IsZero() && time.Since(r.lastUpdate) < 10*time.Second {
		return
	}
	r.lastUpdate = time.Now()
	message := fmt.Sprintf("stream: received %s in %s", stats.Summary(), elapsed(started))
	if stats.Latest != "" {
		message += fmt.Sprintf(", latest %s=%q", stats.LatestKind, agent.Preview(stats.Latest, 120))
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

func (r *spinnerReporter) Stream(stats agent.StreamStats, started time.Time) {
	r.spinner.Update("Receiving: %d chunks / %s (%s)", stats.Chunks, agent.ByteCount(stats.RawBytes), elapsed(started))
}

func (r *spinnerReporter) Debug(string, ...any) {}
func (r *spinnerReporter) Finish()              { r.spinner.Finish() }
func (r *spinnerReporter) Close()               { r.spinner.Stop() }
