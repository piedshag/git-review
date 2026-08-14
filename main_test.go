package main

import (
	"flag"
	"io"
	"reflect"
	"testing"

	reviewpkg "github.com/piedshag/git-review/internal/review"
)

func TestParseInterspersedFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "flags first", args: []string{"-v", "--base", "main", "feature"}},
		{name: "branch first", args: []string{"feature", "-v", "--base", "main"}},
		{name: "flags around branch", args: []string{"--base=main", "feature", "-v"}},
		{name: "explicit boolean", args: []string{"feature", "-v=false", "--base", "main"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			verbose := fs.Bool("v", false, "verbose")
			base := fs.String("base", "", "base")
			if err := parseInterspersed(fs, test.args); err != nil {
				t.Fatal(err)
			}
			expectedVerbose := test.name != "explicit boolean"
			if *verbose != expectedVerbose || *base != "main" || !reflect.DeepEqual(fs.Args(), []string{"feature"}) {
				t.Fatalf("verbose=%t base=%q args=%v", *verbose, *base, fs.Args())
			}
		})
	}
}

func TestParseInterspersedHonorsDoubleDash(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	verbose := fs.Bool("v", false, "verbose")
	if err := parseInterspersed(fs, []string{"-v", "--", "-branch"}); err != nil {
		t.Fatal(err)
	}
	if !*verbose || !reflect.DeepEqual(fs.Args(), []string{"-branch"}) {
		t.Fatalf("verbose=%t args=%v", *verbose, fs.Args())
	}
}

func TestValidReasoningEffort(t *testing.T) {
	for _, value := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if !validReasoningEffort(value) {
			t.Errorf("valid effort %q was rejected", value)
		}
	}
	if validReasoningEffort("extreme") {
		t.Fatal("invalid effort was accepted")
	}
}

func TestParseOptionsSupportsFormatAndInstructionsAfterBranch(t *testing.T) {
	t.Setenv("GIT_REVIEW_INSTRUCTIONS", "")
	options, err := parseOptions([]string{"feature", "--format", "json", "--instructions", "Focus on migrations"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if options.branch != "feature" || options.format != reviewpkg.FormatJSON || options.instructions != "Focus on migrations" {
		t.Fatalf("unexpected options: %+v", options)
	}
}

func TestParseOptionsDefaultsToThirtySteps(t *testing.T) {
	t.Setenv("GIT_REVIEW_MAX_STEPS", "")
	options, err := parseOptions([]string{"feature"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if options.maxSteps != 30 {
		t.Fatalf("max steps=%d, want 30", options.maxSteps)
	}
}

func TestParseMCPOptionsSupportsInterspersedFlags(t *testing.T) {
	options, err := parseMCPOptions([]string{"feature", "--base", "main", "--repo", "/repo"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if options.branch != "feature" || options.base != "main" || options.repoPath != "/repo" {
		t.Fatalf("unexpected options: %+v", options)
	}
}
