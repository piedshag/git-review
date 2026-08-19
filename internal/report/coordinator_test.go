package report

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestCoordinatorPrefixesConcurrentAgentLogs(t *testing.T) {
	var output bytes.Buffer
	coordinator := NewCoordinator(&output, true, false, false)
	security := coordinator.Agent("security")
	bugs := coordinator.Agent("bugs")

	var wait sync.WaitGroup
	for _, item := range []struct {
		name     string
		reporter interface{ Next(string, ...any) }
	}{
		{name: "security", reporter: security},
		{name: "bugs", reporter: bugs},
	} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			item.reporter.Next("checking changes")
		}()
	}
	wait.Wait()

	for _, expected := range []string{"[security] checking changes", "[bugs] checking changes"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("coordinated log omitted %q: %q", expected, output.String())
		}
	}
}

func TestCoordinatorLimitsDefaultInteractiveOutputToLifecycle(t *testing.T) {
	var output bytes.Buffer
	coordinator := NewCoordinator(&output, false, false, true)
	coordinator.Agent("security").Next("thinking")
	coordinator.Lifecycle("security", "started")

	if strings.Contains(output.String(), "thinking") || !strings.Contains(output.String(), "[security] started") {
		t.Fatalf("unexpected interactive output: %q", output.String())
	}
}

func TestScopedReporterCloseDoesNotDisableCoordinator(t *testing.T) {
	var output bytes.Buffer
	coordinator := NewCoordinator(&output, true, false, false)
	first := coordinator.Agent("first")
	first.Close()
	coordinator.Agent("second").Next("still running")

	if !strings.Contains(output.String(), "[second] still running") {
		t.Fatalf("shared coordinator was closed: %q", output.String())
	}
}
