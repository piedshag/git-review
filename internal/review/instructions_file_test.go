package review

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoadInstructionsFromStdin(t *testing.T) {
	instructions, err := LoadInstructions("", "-", strings.NewReader("  Check API compatibility.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if instructions != "Check API compatibility." {
		t.Fatalf("unexpected instructions: %q", instructions)
	}
	tooLarge := bytes.NewReader(bytes.Repeat([]byte("x"), maxInstructionsBytes+1))
	if _, err := LoadInstructions("", "-", tooLarge); err == nil {
		t.Fatal("oversized instructions were accepted")
	}
}
