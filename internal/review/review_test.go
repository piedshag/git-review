package review

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
	topLevelRequired := tool.Function.Parameters["required"].([]string)
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
}

func TestSubmitJudgmentToolRequiresFindingSources(t *testing.T) {
	tool := submitJudgmentTool()
	if tool.Function.Name != submitJudgmentToolName {
		t.Fatalf("unexpected tool name %q", tool.Function.Name)
	}
	properties := tool.Function.Parameters["properties"].(map[string]any)
	findings := properties["findings"].(map[string]any)
	item := findings["items"].(map[string]any)
	required := item["required"].([]string)
	if !contains(required, "source_finding_ids") {
		t.Fatal("judgment finding schema does not require source_finding_ids")
	}
	itemProperties := item["properties"].(map[string]any)
	if _, ok := itemProperties["source_finding_ids"]; !ok {
		t.Fatal("judgment finding schema omits source_finding_ids")
	}
}

func TestSubmitReviewToolSchemaAvoidsLargeStringMaxLengths(t *testing.T) {
	var inspect func(string, map[string]any)
	inspect = func(path string, schema map[string]any) {
		if maximum, ok := schema["maxLength"].(int); ok && maximum >= 2000 {
			t.Errorf("%s has grammar-incompatible maxLength %d", path, maximum)
		}
		if properties, ok := schema["properties"].(map[string]any); ok {
			for name, property := range properties {
				inspect(path+"."+name, property.(map[string]any))
			}
		}
		if items, ok := schema["items"].(map[string]any); ok {
			inspect(path+"[]", items)
		}
	}
	for _, tool := range []Tool{submitReviewTool(), submitJudgmentTool()} {
		inspect(tool.Function.Name, tool.Function.Parameters)
	}
}

func TestParseJudgmentResolvesMultipleFindingSources(t *testing.T) {
	inputs := []NamedReview{
		{ID: "security", Model: "security-model", Review: Review{Findings: []Finding{{ID: "security:1", Sources: []FindingSource{{FindingID: "security:1", Agent: "security", Model: "security-model"}}}}}},
		{ID: "correctness", Model: "correctness-model", Review: Review{Findings: []Finding{{ID: "correctness:2", Sources: []FindingSource{{FindingID: "correctness:2", Agent: "correctness", Model: "correctness-model"}}}}}},
	}
	submission, err := parseJudgmentSubmission(reviewArguments(`[
		{"severity":"high","summary":"Authorization can be bypassed","explanation":"The updated handler skips the required authorization check for delegated requests.","file":"auth.go","line":42,"source_finding_ids":["security:1","correctness:2"]}
	]`), inputs)
	if err != nil {
		t.Fatal(err)
	}
	sources := submission.Findings[0].Sources
	if len(sources) != 2 || sources[0].Model != "security-model" || sources[1].Model != "correctness-model" {
		t.Fatalf("unexpected resolved sources: %+v", sources)
	}
}

func TestParseJudgmentPropagatesLeafSourcesThroughJudge(t *testing.T) {
	inputs := []NamedReview{{
		ID: "first-judge", Model: "judge-model",
		Review: Review{Findings: []Finding{{
			ID: "first-judge:1",
			Sources: []FindingSource{
				{FindingID: "security:1", Agent: "security", Model: "security-model"},
				{FindingID: "correctness:1", Agent: "correctness", Model: "correctness-model"},
			},
		}}},
	}}
	submission, err := parseJudgmentSubmission(reviewArguments(`[
		{"severity":"high","summary":"Authorization can be bypassed","explanation":"The updated handler skips the required authorization check for delegated requests.","file":"auth.go","line":42,"source_finding_ids":["first-judge:1"]}
	]`), inputs)
	if err != nil {
		t.Fatal(err)
	}
	if sources := submission.Findings[0].Sources; len(sources) != 2 || sources[0].Agent != "security" || sources[1].Agent != "correctness" {
		t.Fatalf("leaf provenance was not propagated: %+v", sources)
	}
}

func TestJudgmentSourceValidation(t *testing.T) {
	inputs := []NamedReview{{ID: "security", Model: "model", Review: Review{Findings: []Finding{{ID: "security:1"}}}}}
	tests := []struct {
		name    string
		sources string
		message string
	}{
		{name: "missing", sources: "", message: "source_finding_ids is required"},
		{name: "null", sources: `,"source_finding_ids":null`, message: "must be an array"},
		{name: "unknown", sources: `,"source_finding_ids":["missing:1"]`, message: "unknown source"},
		{name: "duplicate", sources: `,"source_finding_ids":["security:1","security:1"]`, message: "duplicate source"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finding := `{"severity":"high","summary":"Authorization can be bypassed","explanation":"The updated handler skips the required authorization check for delegated requests.","file":"auth.go","line":42` + test.sources + `}`
			_, err := parseJudgmentSubmission(reviewArguments("["+finding+"]"), inputs)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v, want substring %q", err, test.message)
			}
		})
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
		{name: "judgment sources", arguments: reviewArguments(`[{"severity":"high","summary":"Unexpected finding sources","explanation":"This explanation is sufficiently detailed for validation.","file":"main.go","line":1,"source_finding_ids":[]}]`), errorText: "only allowed in judgments"},
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
