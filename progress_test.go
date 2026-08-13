package main

import (
	"bytes"
	"strings"
	"testing"
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
