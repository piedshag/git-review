package report

import (
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/piedshag/git-review/internal/agent"
)

// Coordinator owns the shared log stream for a concurrent review run. Agent
// reporters are scoped views; closing one never closes or finishes the shared
// stream.
type Coordinator struct {
	logger      *log.Logger
	verbose     bool
	debug       bool
	interactive bool
	mu          sync.Mutex
}

func NewCoordinator(writer io.Writer, verbose, debug, interactive bool) *Coordinator {
	if writer == nil {
		writer = io.Discard
	}
	return &Coordinator{
		logger:      log.New(writer, "git-review-run: ", log.LstdFlags),
		verbose:     verbose,
		debug:       debug,
		interactive: interactive,
	}
}

func (c *Coordinator) Agent(id string) agent.Reporter {
	if !c.verbose && !c.debug {
		return agent.NopReporter{}
	}
	return &scopedReporter{coordinator: c, id: id}
}

// Lifecycle reports coarse graph state. Interactive terminals see lifecycle
// changes without receiving every model/tool event.
func (c *Coordinator) Lifecycle(id, format string, args ...any) {
	if !c.interactive && !c.verbose && !c.debug {
		return
	}
	c.write(id, fmt.Sprintf(format, args...))
}

func (c *Coordinator) write(id, message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger.Printf("[%s] %s", id, message)
}

type scopedReporter struct {
	coordinator *Coordinator
	id          string
	mu          sync.Mutex
	lastUpdate  time.Time
}

func (r *scopedReporter) Next(format string, args ...any) {
	r.coordinator.write(r.id, fmt.Sprintf(format, args...))
}

func (r *scopedReporter) Stream(stats agent.StreamStats, started time.Time) {
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
	r.coordinator.write(r.id, message)
}

func (r *scopedReporter) Debug(format string, args ...any) {
	if r.coordinator.debug {
		r.coordinator.write(r.id, fmt.Sprintf(format, args...))
	}
}

func (*scopedReporter) Finish() {}
func (*scopedReporter) Close()  {}
