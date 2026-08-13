package review

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteReviewFormats(t *testing.T) {
	review := Review{
		Status: ReviewComplete, Summary: testChangeSummary, Strengths: testStrengths, Weaknesses: testWeaknesses,
		Stats: ReviewStats{DurationMS: 2500, InputTokens: 100, OutputTokens: 20, TotalTokens: 120, UsageAvailable: true, UsageComplete: true},
		Findings: []Finding{{
			Severity: "high", Summary: "Prevent data loss", Explanation: "The replacement truncates existing data before validation completes.", File: "store.go", Line: 42,
		}},
	}
	var markdown bytes.Buffer
	if err := Write(&markdown, FormatMarkdown, review); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown.String(), "### [HIGH] Prevent data loss") || !strings.Contains(markdown.String(), "## Change summary") {
		t.Fatalf("unexpected markdown: %s", markdown.String())
	}
	if !strings.Contains(markdown.String(), "120 tokens") || !strings.Contains(markdown.String(), "time 2.5s") {
		t.Fatalf("markdown omitted review stats: %s", markdown.String())
	}

	var output bytes.Buffer
	if err := Write(&output, FormatJSON, review); err != nil {
		t.Fatal(err)
	}
	var decoded Review
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != ReviewComplete || decoded.Summary != testChangeSummary || len(decoded.Findings) != 1 || decoded.Findings[0].Line != 42 || decoded.Stats.DurationMS != 2500 {
		t.Fatalf("unexpected JSON review: %+v", decoded)
	}
}

func TestParseOutputFormat(t *testing.T) {
	for _, value := range []string{"markdown", "json"} {
		if _, err := ParseOutputFormat(value); err != nil {
			t.Errorf("valid format %q rejected: %v", value, err)
		}
	}
	if _, err := ParseOutputFormat("yaml"); err == nil {
		t.Fatal("invalid format accepted")
	}
}
