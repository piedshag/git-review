package main

import (
	"strings"
	"testing"
)

func TestSubmitReviewToolSchemaRequiresStructuredFindings(t *testing.T) {
	tool := submitReviewTool()
	if tool.Function.Name != submitReviewToolName {
		t.Fatalf("unexpected tool name %q", tool.Function.Name)
	}
	properties := tool.Function.Parameters["properties"].(map[string]any)
	findings := properties["findings"].(map[string]any)
	item := findings["items"].(map[string]any)
	required := item["required"].([]string)
	for _, name := range []string{"severity", "summary", "explanation", "file", "line"} {
		if !contains(required, name) {
			t.Errorf("finding schema does not require %q", name)
		}
	}
}

func TestParseAndRenderReview(t *testing.T) {
	findings, err := parseReviewSubmission(`{
		"findings": [
			{"severity":"low","summary":"  Handle empty response  ","explanation":"An empty response currently causes an ambiguous result; return a clear error instead.","file":"client.go","line":42},
			{"severity":"critical","summary":"Prevent credential exposure","explanation":"The request logger can expose bearer credentials to CI logs; redact the authorization value before logging.","file":"main.go","line":10}
		]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	review := renderMarkdown(Review{Status: ReviewComplete, Findings: findings})
	critical := "## [CRITICAL] Prevent credential exposure\n\n`main.go:10`"
	low := "## [LOW] Handle empty response\n\n`client.go:42`"
	if !strings.Contains(review, critical) || !strings.Contains(review, low) {
		t.Fatalf("review is missing structured findings:\n%s", review)
	}
	if strings.Index(review, critical) > strings.Index(review, low) {
		t.Fatalf("review is not ordered by severity:\n%s", review)
	}
}

func TestRenderReviewWithNoFindings(t *testing.T) {
	findings, err := parseReviewSubmission(`{"findings":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	expected := "# Review\n\nNo findings.\n\n---\n\n**Review stats:** token usage unavailable · cost unavailable · time 0s"
	if review := renderMarkdown(Review{Status: ReviewComplete, Findings: findings}); review != expected {
		t.Fatalf("unexpected empty review: %q", review)
	}
}

func TestReviewSubmissionValidation(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		errorText string
	}{
		{name: "missing findings", arguments: `{}`, errorText: "findings is required"},
		{name: "null findings", arguments: `{"findings":null}`, errorText: "not null"},
		{name: "bad severity", arguments: `{"findings":[{"severity":"urgent","summary":"Bad severity","explanation":"This explanation is sufficiently detailed for validation.","file":"main.go","line":1}]}`, errorText: "severity"},
		{name: "long summary", arguments: `{"findings":[{"severity":"high","summary":"one two three four five six seven eight nine ten eleven twelve thirteen","explanation":"This explanation is sufficiently detailed for validation.","file":"main.go","line":1}]}`, errorText: "12 words"},
		{name: "short explanation", arguments: `{"findings":[{"severity":"high","summary":"Short explanation","explanation":"Too short","file":"main.go","line":1}]}`, errorText: "explanation"},
		{name: "unsafe file", arguments: `{"findings":[{"severity":"high","summary":"Unsafe file path","explanation":"This explanation is sufficiently detailed for validation.","file":"../main.go","line":1}]}`, errorText: "repository-relative"},
		{name: "invalid line", arguments: `{"findings":[{"severity":"high","summary":"Invalid line number","explanation":"This explanation is sufficiently detailed for validation.","file":"main.go","line":0}]}`, errorText: "line"},
		{name: "unknown field", arguments: `{"findings":[{"severity":"high","summary":"Unknown extra field","explanation":"This explanation is sufficiently detailed for validation.","file":"main.go","line":1,"confidence":1}]}`, errorText: "unknown field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseReviewSubmission(test.arguments)
			if err == nil || !strings.Contains(err.Error(), test.errorText) {
				t.Fatalf("expected error containing %q, got %v", test.errorText, err)
			}
		})
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
