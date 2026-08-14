package review

import (
	"strings"
	"testing"
)

func TestSubmitReviewToolSchemaRequiresStructuredFindings(t *testing.T) {
	tool := SubmissionTool()
	if tool.Definition.Name != submitReviewToolName {
		t.Fatalf("unexpected tool name %q", tool.Definition.Name)
	}
	properties := tool.Definition.InputSchema["properties"].(map[string]any)
	topLevelRequired := tool.Definition.InputSchema["required"].([]string)
	for _, name := range []string{"summary", "strengths", "weaknesses", "findings"} {
		if !contains(topLevelRequired, name) {
			t.Errorf("submission schema does not require %q", name)
		}
	}
	findings := properties["findings"].(map[string]any)
	item := findings["items"].(map[string]any)
	required := item["required"].([]string)
	for _, name := range []string{"severity", "summary", "explanation", "file", "line"} {
		if !contains(required, name) {
			t.Errorf("finding schema does not require %q", name)
		}
	}
	for _, property := range []map[string]any{
		properties["summary"].(map[string]any),
		properties["strengths"].(map[string]any),
		properties["weaknesses"].(map[string]any),
		item["properties"].(map[string]any)["explanation"].(map[string]any),
	} {
		if _, ok := property["maxLength"]; ok {
			t.Errorf("long review text uses a maxLength grammar repetition: %#v", property)
		}
	}
}

func TestParseAndRenderReview(t *testing.T) {
	submission, err := parseReviewSubmission(reviewArguments(`[
			{"severity":"low","summary":"  Handle empty response  ","explanation":"An empty response currently causes an ambiguous result; return a clear error instead.","file":"client.go","line":42},
			{"severity":"critical","summary":"Prevent credential exposure","explanation":"The request logger can expose bearer credentials to CI logs; redact the authorization value before logging.","file":"main.go","line":10}
		]`))
	if err != nil {
		t.Fatal(err)
	}
	review := renderMarkdown(Review{
		Status: ReviewComplete, Summary: submission.Summary, Strengths: submission.Strengths,
		Weaknesses: submission.Weaknesses, Findings: submission.Findings,
	})
	critical := "### [CRITICAL] Prevent credential exposure\n\n`main.go:10`"
	low := "### [LOW] Handle empty response\n\n`client.go:42`"
	if !strings.Contains(review, critical) || !strings.Contains(review, low) {
		t.Fatalf("review is missing structured findings:\n%s", review)
	}
	for _, expected := range []string{"## Change summary\n\n" + testChangeSummary, "## Strengths\n\n" + testStrengths, "## Weaknesses\n\n" + testWeaknesses} {
		if !strings.Contains(review, expected) {
			t.Errorf("review is missing narrative %q:\n%s", expected, review)
		}
	}
	if strings.Index(review, critical) > strings.Index(review, low) {
		t.Fatalf("review is not ordered by severity:\n%s", review)
	}
}

func TestRenderReviewWithNoFindings(t *testing.T) {
	submission, err := parseReviewSubmission(reviewArguments(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	review := renderMarkdown(Review{
		Status: ReviewComplete, Summary: submission.Summary, Strengths: submission.Strengths,
		Weaknesses: submission.Weaknesses, Findings: submission.Findings,
	})
	for _, expected := range []string{testChangeSummary, testStrengths, testWeaknesses, "## Findings\n\nNo findings."} {
		if !strings.Contains(review, expected) {
			t.Errorf("clean review does not contain %q: %s", expected, review)
		}
	}
}

func TestReviewSubmissionValidation(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		errorText string
	}{
		{name: "findings only", arguments: `{"findings":[]}`, errorText: "summary is required"},
		{name: "missing summary", arguments: `{"strengths":"A sufficiently detailed implementation strength.","weaknesses":"No material implementation weaknesses identified.","findings":[]}`, errorText: "summary is required"},
		{name: "short summary", arguments: `{"summary":"Too short","strengths":"A sufficiently detailed implementation strength.","weaknesses":"No material implementation weaknesses identified.","findings":[]}`, errorText: "summary must contain"},
		{name: "short strengths", arguments: `{"summary":"A sufficiently detailed summary of the branch changes.","strengths":"None","weaknesses":"No material implementation weaknesses identified.","findings":[]}`, errorText: "strengths must contain"},
		{name: "short weaknesses", arguments: `{"summary":"A sufficiently detailed summary of the branch changes.","strengths":"The implementation has a clear and appropriately narrow design.","weaknesses":"None","findings":[]}`, errorText: "weaknesses must contain"},
		{name: "null findings", arguments: strings.Replace(reviewArguments(`[]`), `"findings":[]`, `"findings":null`, 1), errorText: "not null"},
		{name: "bad severity", arguments: reviewArguments(`[{"severity":"urgent","summary":"Bad severity","explanation":"This explanation is sufficiently detailed for validation.","file":"main.go","line":1}]`), errorText: "severity"},
		{name: "long summary", arguments: reviewArguments(`[{"severity":"high","summary":"one two three four five six seven eight nine ten eleven twelve thirteen","explanation":"This explanation is sufficiently detailed for validation.","file":"main.go","line":1}]`), errorText: "12 words"},
		{name: "short explanation", arguments: reviewArguments(`[{"severity":"high","summary":"Short explanation","explanation":"Too short","file":"main.go","line":1}]`), errorText: "explanation"},
		{name: "unsafe file", arguments: reviewArguments(`[{"severity":"high","summary":"Unsafe file path","explanation":"This explanation is sufficiently detailed for validation.","file":"../main.go","line":1}]`), errorText: "repository-relative"},
		{name: "invalid line", arguments: reviewArguments(`[{"severity":"high","summary":"Invalid line number","explanation":"This explanation is sufficiently detailed for validation.","file":"main.go","line":0}]`), errorText: "line"},
		{name: "unknown field", arguments: reviewArguments(`[{"severity":"high","summary":"Unknown extra field","explanation":"This explanation is sufficiently detailed for validation.","file":"main.go","line":1,"confidence":1}]`), errorText: "unknown field"},
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

func TestReviewSubmissionStillEnforcesNarrativeLimit(t *testing.T) {
	arguments := reviewArguments(`[]`)
	arguments = strings.Replace(arguments, testChangeSummary, strings.Repeat("x", 4001), 1)
	_, err := parseReviewSubmission(arguments)
	if err == nil || !strings.Contains(err.Error(), "summary must contain 20 to 4000 characters") {
		t.Fatalf("expected summary length error, got %v", err)
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
