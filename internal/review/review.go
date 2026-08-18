package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const submitReviewToolName = "submit_review"

type ReviewStatus string

const (
	ReviewComplete     ReviewStatus = "complete"
	ReviewInconclusive ReviewStatus = "inconclusive"
)

type Review struct {
	Status     ReviewStatus `json:"status"`
	Summary    string       `json:"summary,omitempty"`
	Strengths  string       `json:"strengths,omitempty"`
	Weaknesses string       `json:"weaknesses,omitempty"`
	Findings   []Finding    `json:"findings"`
	Message    string       `json:"message,omitempty"`
	Stats      ReviewStats  `json:"stats"`
}

type submittedReview struct {
	Summary    string
	Strengths  string
	Weaknesses string
	Findings   []Finding
}

type Finding struct {
	Severity    string `json:"severity"`
	Summary     string `json:"summary"`
	Explanation string `json:"explanation"`
	File        string `json:"file"`
	Line        int    `json:"line"`
}

type ReviewStats struct {
	DurationMS     int64    `json:"duration_ms"`
	InputTokens    int      `json:"input_tokens"`
	OutputTokens   int      `json:"output_tokens"`
	TotalTokens    int      `json:"total_tokens"`
	UsageAvailable bool     `json:"usage_available"`
	UsageComplete  bool     `json:"usage_complete"`
	Cost           *float64 `json:"cost,omitempty"`
	CostEstimated  bool     `json:"cost_estimated,omitempty"`
	CostComplete   bool     `json:"cost_complete"`
}

func submitReviewTool() Tool {
	// Keep the 4,000-character upper bounds in parseReviewSubmission rather than
	// the tool schema. Some grammar-backed OpenAI-compatible servers cannot
	// compile string maxLength values of 2,000 or greater.
	return Tool{Type: "function", Function: ToolFunction{
		Name:        submitReviewToolName,
		Description: "Submit the final code review with a change summary, strengths, weaknesses, and structured findings. Call exactly once after inspection, with an empty findings array when no defects were found.",
		Parameters: objectSchema(map[string]any{
			"summary":    map[string]any{"type": "string", "minLength": 20, "description": "Summarize the changes introduced by the branch and their purpose."},
			"strengths":  map[string]any{"type": "string", "minLength": 20, "description": "Explain what the implementation does well. State clearly when no notable strengths were identified."},
			"weaknesses": map[string]any{"type": "string", "minLength": 20, "description": "Explain weaknesses, tradeoffs, or remaining concerns. State clearly when none were identified; concrete defects must also appear in findings."},
			"findings": map[string]any{
				"type":     "array",
				"maxItems": 100,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"severity":    map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low"}, "description": "Impact severity."},
						"summary":     map[string]any{"type": "string", "minLength": 3, "maxLength": 80, "description": "A concise summary of at most 12 words."},
						"explanation": map[string]any{"type": "string", "minLength": 20, "description": "Detailed explanation of the defect, its impact, and a suggested fix."},
						"file":        map[string]any{"type": "string", "minLength": 1, "description": "Repository-relative target-branch file path."},
						"line":        map[string]any{"type": "integer", "minimum": 1, "description": "Relevant target-branch line number."},
					},
					"required":             []string{"severity", "summary", "explanation", "file", "line"},
					"additionalProperties": false,
				},
			},
		}, []string{"summary", "strengths", "weaknesses", "findings"}),
	}}
}

func parseReviewSubmission(arguments string) (submittedReview, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &fields); err != nil {
		return submittedReview{}, fmt.Errorf("invalid JSON: %w", err)
	}
	for _, name := range []string{"summary", "strengths", "weaknesses", "findings"} {
		if _, ok := fields[name]; !ok {
			return submittedReview{}, fmt.Errorf("%s is required", name)
		}
	}
	if len(fields) != 4 {
		return submittedReview{}, errors.New("only summary, strengths, weaknesses, and findings are allowed")
	}
	var submission submittedReview
	if err := json.Unmarshal(fields["summary"], &submission.Summary); err != nil {
		return submittedReview{}, errors.New("summary must be a string")
	}
	if err := json.Unmarshal(fields["strengths"], &submission.Strengths); err != nil {
		return submittedReview{}, errors.New("strengths must be a string")
	}
	if err := json.Unmarshal(fields["weaknesses"], &submission.Weaknesses); err != nil {
		return submittedReview{}, errors.New("weaknesses must be a string")
	}
	submission.Summary = strings.TrimSpace(submission.Summary)
	submission.Strengths = strings.TrimSpace(submission.Strengths)
	submission.Weaknesses = strings.TrimSpace(submission.Weaknesses)
	narrative := []struct {
		name  string
		value string
	}{
		{name: "summary", value: submission.Summary},
		{name: "strengths", value: submission.Strengths},
		{name: "weaknesses", value: submission.Weaknesses},
	}
	for _, field := range narrative {
		if count := utf8.RuneCountInString(field.value); count < 20 || count > 4000 {
			return submittedReview{}, fmt.Errorf("%s must contain 20 to 4000 characters", field.name)
		}
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(fields["findings"], &rawItems); err != nil {
		return submittedReview{}, fmt.Errorf("findings must be an array: %w", err)
	}
	if rawItems == nil {
		return submittedReview{}, errors.New("findings must be an array, not null")
	}
	if len(rawItems) > 100 {
		return submittedReview{}, errors.New("findings cannot contain more than 100 items")
	}
	submission.Findings = make([]Finding, len(rawItems))
	for i, rawItem := range rawItems {
		decoder := json.NewDecoder(strings.NewReader(string(rawItem)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&submission.Findings[i]); err != nil {
			return submittedReview{}, fmt.Errorf("finding %d: invalid fields: %w", i+1, err)
		}
		finding := &submission.Findings[i]
		finding.Severity = strings.ToLower(strings.TrimSpace(finding.Severity))
		finding.Summary = strings.Join(strings.Fields(finding.Summary), " ")
		finding.Explanation = strings.TrimSpace(finding.Explanation)
		finding.File = path.Clean(strings.TrimSpace(finding.File))
		if err := validateFinding(*finding); err != nil {
			return submittedReview{}, fmt.Errorf("finding %d: %w", i+1, err)
		}
	}
	return submission, nil
}

func validateFinding(finding Finding) error {
	switch finding.Severity {
	case "critical", "high", "medium", "low":
	default:
		return errors.New("severity must be critical, high, medium, or low")
	}
	if utf8.RuneCountInString(finding.Summary) < 3 || utf8.RuneCountInString(finding.Summary) > 80 {
		return errors.New("summary must contain 3 to 80 characters")
	}
	if len(strings.Fields(finding.Summary)) > 12 {
		return errors.New("summary must contain at most 12 words")
	}
	if utf8.RuneCountInString(finding.Explanation) < 20 || utf8.RuneCountInString(finding.Explanation) > 4000 {
		return errors.New("explanation must contain 20 to 4000 characters")
	}
	if finding.File == "" || finding.File == "." || path.IsAbs(finding.File) || strings.HasPrefix(finding.File, "../") {
		return errors.New("file must be a repository-relative path")
	}
	if finding.Line < 1 {
		return errors.New("line must be positive")
	}
	return nil
}

func sortedFindings(findings []Finding) []Finding {
	ordered := append([]Finding(nil), findings...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := severityRank(ordered[i].Severity), severityRank(ordered[j].Severity)
		if left != right {
			return left < right
		}
		if ordered[i].File != ordered[j].File {
			return ordered[i].File < ordered[j].File
		}
		return ordered[i].Line < ordered[j].Line
	})
	return ordered
}

func renderMarkdown(review Review) string {
	body := ""
	if review.Status == ReviewInconclusive {
		body = "# Review\n\n**Inconclusive:** " + review.Message
	} else {
		var output strings.Builder
		fmt.Fprintf(&output, "# Review\n\n## Change summary\n\n%s\n\n", review.Summary)
		fmt.Fprintf(&output, "## Strengths\n\n%s\n\n", review.Strengths)
		fmt.Fprintf(&output, "## Weaknesses\n\n%s\n\n", review.Weaknesses)
		output.WriteString("## Findings\n\n")
		if len(review.Findings) == 0 {
			output.WriteString("No findings.\n")
		}
		ordered := sortedFindings(review.Findings)
		for i, finding := range ordered {
			if i > 0 {
				output.WriteString("\n")
			}
			fmt.Fprintf(&output, "### [%s] %s\n\n", strings.ToUpper(finding.Severity), finding.Summary)
			fmt.Fprintf(&output, "`%s:%d`\n\n", strings.ReplaceAll(finding.File, "`", "\\`"), finding.Line)
			output.WriteString(finding.Explanation)
			output.WriteString("\n")
		}
		body = strings.TrimSpace(output.String())
	}
	return body + "\n\n---\n\n**Review stats:** " + formatReviewStats(review.Stats)
}

func formatReviewStats(stats ReviewStats) string {
	parts := make([]string, 0, 3)
	if stats.UsageAvailable {
		tokens := fmt.Sprintf("%d input + %d output = %d tokens", stats.InputTokens, stats.OutputTokens, stats.TotalTokens)
		if !stats.UsageComplete {
			tokens += " (reported; incomplete)"
		}
		parts = append(parts, tokens)
	} else {
		parts = append(parts, "token usage unavailable")
	}
	if stats.Cost != nil {
		label := "cost"
		if stats.CostEstimated {
			label = "estimated cost"
		}
		parts = append(parts, fmt.Sprintf("%s $%.6f", label, *stats.Cost))
	} else {
		parts = append(parts, "cost unavailable")
	}
	parts = append(parts, "time "+humanDuration(time.Duration(stats.DurationMS)*time.Millisecond))
	return strings.Join(parts, " · ")
}

func humanDuration(duration time.Duration) string {
	if duration < time.Second {
		return duration.Round(time.Millisecond).String()
	}
	if duration < time.Minute {
		return duration.Round(100 * time.Millisecond).String()
	}
	return duration.Round(time.Second).String()
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	default:
		return 3
	}
}
