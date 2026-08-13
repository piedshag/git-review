package main

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
	Status   ReviewStatus `json:"status"`
	Findings []Finding    `json:"findings"`
	Message  string       `json:"message,omitempty"`
	Stats    ReviewStats  `json:"stats"`
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
	return Tool{Type: "function", Function: ToolFunction{
		Name:        submitReviewToolName,
		Description: "Submit the final code review. Call exactly once after inspection, with an empty findings array when no defects were found.",
		Parameters: objectSchema(map[string]any{
			"findings": map[string]any{
				"type":     "array",
				"maxItems": 100,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"severity":    map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low"}, "description": "Impact severity."},
						"summary":     map[string]any{"type": "string", "minLength": 3, "maxLength": 80, "description": "A concise summary of at most 12 words."},
						"explanation": map[string]any{"type": "string", "minLength": 20, "maxLength": 4000, "description": "Detailed explanation of the defect, its impact, and a suggested fix."},
						"file":        map[string]any{"type": "string", "minLength": 1, "description": "Repository-relative target-branch file path."},
						"line":        map[string]any{"type": "integer", "minimum": 1, "description": "Relevant target-branch line number."},
					},
					"required":             []string{"severity", "summary", "explanation", "file", "line"},
					"additionalProperties": false,
				},
			},
		}, []string{"findings"}),
	}}
}

func parseReviewSubmission(arguments string) ([]Finding, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &fields); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	rawFindings, ok := fields["findings"]
	if !ok {
		return nil, errors.New("findings is required")
	}
	if len(fields) != 1 {
		return nil, errors.New("only findings is allowed")
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(rawFindings, &rawItems); err != nil {
		return nil, fmt.Errorf("findings must be an array: %w", err)
	}
	if rawItems == nil {
		return nil, errors.New("findings must be an array, not null")
	}
	if len(rawItems) > 100 {
		return nil, errors.New("findings cannot contain more than 100 items")
	}
	findings := make([]Finding, len(rawItems))
	for i, rawItem := range rawItems {
		decoder := json.NewDecoder(strings.NewReader(string(rawItem)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&findings[i]); err != nil {
			return nil, fmt.Errorf("finding %d: invalid fields: %w", i+1, err)
		}
		finding := &findings[i]
		finding.Severity = strings.ToLower(strings.TrimSpace(finding.Severity))
		finding.Summary = strings.Join(strings.Fields(finding.Summary), " ")
		finding.Explanation = strings.TrimSpace(finding.Explanation)
		finding.File = path.Clean(strings.TrimSpace(finding.File))
		if err := validateFinding(*finding); err != nil {
			return nil, fmt.Errorf("finding %d: %w", i+1, err)
		}
	}
	return findings, nil
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
	} else if len(review.Findings) == 0 {
		body = "# Review\n\nNo findings."
	} else {
		ordered := sortedFindings(review.Findings)
		var output strings.Builder
		output.WriteString("# Review\n\n")
		for i, finding := range ordered {
			if i > 0 {
				output.WriteString("\n")
			}
			fmt.Fprintf(&output, "## [%s] %s\n\n", strings.ToUpper(finding.Severity), finding.Summary)
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
