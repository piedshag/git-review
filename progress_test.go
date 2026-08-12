package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSpinnerRendersStatusAndClearsLine(t *testing.T) {
	var output bytes.Buffer
	spinner := newSpinner(&output)
	spinner.Set("Thinking (step %d)...", 2)
	spinner.Stop()

	if !strings.Contains(output.String(), "Thinking (step 2)...") {
		t.Fatalf("spinner did not render status: %q", output.String())
	}
	if !strings.HasSuffix(output.String(), "\r\x1b[2K") {
		t.Fatalf("spinner did not clear its line: %q", output.String())
	}
}

func TestVerboseLoggingTakesPrecedenceOverSpinner(t *testing.T) {
	var output bytes.Buffer
	client, err := NewClient(Config{
		Endpoint:       "http://model.test/v1",
		Model:          "test-model",
		Verbose:        true,
		LogWriter:      &output,
		Progress:       true,
		ProgressWriter: &output,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.logger == nil || client.progress != nil {
		t.Fatalf("verbose logger and spinner should be mutually exclusive: logger=%v progress=%v", client.logger, client.progress)
	}
}
