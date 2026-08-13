package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSpinnerRendersStatusAndClearsLine(t *testing.T) {
	var output bytes.Buffer
	spinner := newSpinner(&output)
	spinner.Next("Thinking (step %d)...", 2)
	spinner.Next("Receiving streamed response")
	spinner.Update("Receiving streamed response: %d chunks, %s", 12, "4.2 KiB")
	spinner.Finish()

	if !strings.Contains(output.String(), "Thinking (step 2)...") {
		t.Fatalf("spinner did not render status: %q", output.String())
	}
	for _, expected := range []string{"✓ Thinking (step 2)...", "Receiving streamed response: 12 chunks, 4.2 KiB"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("spinner trail does not contain %q: %q", expected, output.String())
		}
	}
	if !strings.HasSuffix(output.String(), "\n") {
		t.Fatalf("finished spinner did not leave a complete line: %q", output.String())
	}
}

func TestVerboseLoggingTakesPrecedenceOverSpinner(t *testing.T) {
	var output bytes.Buffer
	reporter := newReporter(&output, true, false, true)
	if _, ok := reporter.(*textReporter); !ok {
		t.Fatalf("verbose mode should use text reporter, got %T", reporter)
	}
	reporter.Next("reviewing feature")
	if !strings.Contains(output.String(), "reviewing feature") {
		t.Fatalf("text reporter did not log activity: %q", output.String())
	}
}

func TestReporterKeepsDebugOutputExplicit(t *testing.T) {
	var verboseOutput bytes.Buffer
	verbose := newReporter(&verboseOutput, true, false, false)
	verbose.Debug("model response: secret reasoning")
	if verboseOutput.Len() != 0 {
		t.Fatalf("verbose mode leaked debug output: %q", verboseOutput.String())
	}

	var debugOutput bytes.Buffer
	debug := newReporter(&debugOutput, false, true, false)
	debug.Debug("model response: parsed response")
	if !strings.Contains(debugOutput.String(), "parsed response") {
		t.Fatalf("debug reporter omitted model output: %q", debugOutput.String())
	}
}

func TestReporterIsSilentForNonInteractiveDefault(t *testing.T) {
	reporter := newReporter(io.Discard, false, false, false)
	if _, ok := reporter.(discardReporter); !ok {
		t.Fatalf("non-interactive default should be silent, got %T", reporter)
	}
}

func TestSpinnerStreamStatusStaysCompact(t *testing.T) {
	var output bytes.Buffer
	reporter := newReporter(&output, false, false, true)
	reporter.Next("Receiving streamed response")
	reporter.Stream(streamStats{
		Chunks: 323, RawBytes: 133 * 1024, ReasoningBytes: 12 * 1024,
		LatestKind: "reasoning", Latest: strings.Repeat("gAAAA", 30),
	}, time.Now().Add(-14*time.Second))
	reporter.Finish()

	if strings.Contains(output.String(), "gAAAA") || strings.Contains(output.String(), "latest reasoning") {
		t.Fatalf("spinner exposed verbose stream preview: %q", output.String())
	}
	if !strings.Contains(output.String(), "Receiving: 323 chunks / 133.0 KiB") {
		t.Fatalf("spinner omitted compact stream progress: %q", output.String())
	}
}
