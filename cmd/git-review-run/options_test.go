package main

import (
	"io"
	"testing"

	"github.com/piedshag/git-review/internal/review"
)

func TestParseOptionsAllowsFlagsAroundBranch(t *testing.T) {
	options, err := parseOptions([]string{"--base", "main", "feature", "--format", "json", "-v"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if options.branch != "feature" || options.base != "main" || options.format != review.FormatJSON || !options.verbose {
		t.Fatalf("unexpected options: %+v", options)
	}
}
