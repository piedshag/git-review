package pipeline

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piedshag/git-review/internal/review"
)

func TestWriteRunPreservesFindingProvenance(t *testing.T) {
	selected := review.Review{
		Status: review.ReviewComplete, Summary: "The branch changes authorization behavior.",
		Strengths:  "The implementation keeps the authorization flow explicit.",
		Weaknesses: "The delegated-request path still has a concrete defect.",
		Findings: []review.Finding{{
			ID: "final:1", Severity: "high", Summary: "Authorization can be bypassed",
			Explanation: "The delegated-request path skips the required authorization check before returning data.", File: "auth.go", Line: 42,
			Sources: []review.FindingSource{
				{FindingID: "security:1", Agent: "security", Model: "security-model"},
				{FindingID: "correctness:2", Agent: "correctness", Model: "correctness-model"},
			},
		}},
	}
	result := RunResult{
		SchemaVersion: 2, Output: "final",
		Nodes: []NodeResult{{ID: "final", Model: "judge-model", Inputs: []string{"security", "correctness"}, Review: &selected}},
	}

	var markdown bytes.Buffer
	if err := Write(&markdown, review.FormatMarkdown, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown.String(), "`security` (`security-model`), `correctness` (`correctness-model`)") {
		t.Fatalf("markdown omitted provenance: %s", markdown.String())
	}

	var output bytes.Buffer
	if err := Write(&output, review.FormatJSON, result); err != nil {
		t.Fatal(err)
	}
	var decoded RunResult
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != 2 || decoded.Nodes[0].Review.Findings[0].Sources[1].FindingID != "correctness:2" {
		t.Fatalf("JSON omitted provenance: %+v", decoded)
	}
}
