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

const (
	submitReviewToolName   = "submit_review"
	submitJudgmentToolName = "submit_judgment"
)

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

// NamedReview identifies an upstream review supplied to an adjudicating
// reviewer. The review remains a claim until the adjudicator verifies it
// against the repository snapshot.
type NamedReview struct {
	ID     string `json:"id"`
	Model  string `json:"model,omitempty"`
	Review Review `json:"review"`
}

type submittedReview struct {
	Summary    string
	Strengths  string
	Weaknesses string
	Findings   []Finding
}

type Finding struct {
	ID          string          `json:"id,omitempty"`
	Severity    string          `json:"severity"`
	Summary     string          `json:"summary"`
	Explanation string          `json:"explanation"`
	File        string          `json:"file"`
	Line        int             `json:"line"`
	Sources     []FindingSource `json:"sources,omitempty"`
}

type FindingSource struct {
	FindingID string `json:"finding_id"`
	Agent     string `json:"agent"`
	Model     string `json:"model"`
}

type submittedFinding struct {
	Severity         string          `json:"severity"`
	Summary          string          `json:"summary"`
	Explanation      string          `json:"explanation"`
	File             string          `json:"file"`
	Line             int             `json:"line"`
	SourceFindingIDs json.RawMessage `json:"source_finding_ids"`
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
	return submissionTool(false)
}

func submitJudgmentTool() Tool {
	return submissionTool(true)
}

func submissionTool(judging bool) Tool {
	// Keep the 4,000-character upper bounds in parseReviewSubmission rather than
	// the tool schema. Some grammar-backed OpenAI-compatible servers cannot
	// compile string maxLength values of 2,000 or greater.
	name := submitReviewToolName
	description := "Submit the final code review with a change summary, strengths, weaknesses, and structured findings. Call exactly once after inspection, with an empty findings array when no defects were found."
	findingProperties := map[string]any{
		"severity":    map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low"}, "description": "Impact severity."},
		"summary":     map[string]any{"type": "string", "minLength": 3, "maxLength": 80, "description": "A concise summary of at most 12 words."},
		"explanation": map[string]any{"type": "string", "minLength": 20, "description": "Detailed explanation of the defect, its impact, and a suggested fix."},
		"file":        map[string]any{"type": "string", "minLength": 1, "description": "Repository-relative target-branch file path."},
		"line":        map[string]any{"type": "integer", "minimum": 1, "description": "Relevant target-branch line number."},
	}
	requiredFindingFields := []string{"severity", "summary", "explanation", "file", "line"}
	if judging {
		name = submitJudgmentToolName
		description = "Submit the final adjudicated review. Verify upstream claims, consolidate equivalent findings, and cite every upstream finding that contributed to each result. Call exactly once, with an empty findings array when no defects survive verification."
		findingProperties["source_finding_ids"] = map[string]any{
			"type": "array", "maxItems": 100,
			"items":       map[string]any{"type": "string", "minLength": 1},
			"description": "IDs of every equivalent upstream finding consolidated into this finding. Use an empty array only for a defect discovered independently during adjudication.",
		}
		requiredFindingFields = append(requiredFindingFields, "source_finding_ids")
	}
	return Tool{Type: "function", Function: ToolFunction{
		Name:        name,
		Description: description,
		Parameters: objectSchema(map[string]any{
			"summary":    map[string]any{"type": "string", "minLength": 20, "description": "Summarize the changes introduced by the branch and their purpose."},
			"strengths":  map[string]any{"type": "string", "minLength": 20, "description": "Explain what the implementation does well. State clearly when no notable strengths were identified."},
			"weaknesses": map[string]any{"type": "string", "minLength": 20, "description": "Explain weaknesses, tradeoffs, or remaining concerns. State clearly when none were identified; concrete defects must also appear in findings."},
			"findings": map[string]any{
				"type":     "array",
				"maxItems": 100,
				"items": map[string]any{
					"type":                 "object",
					"properties":           findingProperties,
					"required":             requiredFindingFields,
					"additionalProperties": false,
				},
			},
		}, []string{"summary", "strengths", "weaknesses", "findings"}),
	}}
}

func parseReviewSubmission(arguments string) (submittedReview, error) {
	return parseSubmission(arguments, nil, false)
}

func parseJudgmentSubmission(arguments string, inputs []NamedReview) (submittedReview, error) {
	return parseSubmission(arguments, inputs, true)
}

func parseSubmission(arguments string, inputs []NamedReview, judging bool) (submittedReview, error) {
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
	sourceIndex, err := indexFindingSources(inputs)
	if err != nil {
		return submittedReview{}, err
	}
	for i, rawItem := range rawItems {
		var submitted submittedFinding
		decoder := json.NewDecoder(strings.NewReader(string(rawItem)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&submitted); err != nil {
			return submittedReview{}, fmt.Errorf("finding %d: invalid fields: %w", i+1, err)
		}
		if !judging && submitted.SourceFindingIDs != nil {
			return submittedReview{}, fmt.Errorf("finding %d: source_finding_ids is only allowed in judgments", i+1)
		}
		var sourceIDs []string
		if judging {
			if submitted.SourceFindingIDs == nil {
				return submittedReview{}, fmt.Errorf("finding %d: source_finding_ids is required", i+1)
			}
			if err := json.Unmarshal(submitted.SourceFindingIDs, &sourceIDs); err != nil || sourceIDs == nil {
				return submittedReview{}, fmt.Errorf("finding %d: source_finding_ids must be an array", i+1)
			}
			if len(sourceIDs) > 100 {
				return submittedReview{}, fmt.Errorf("finding %d: source_finding_ids cannot contain more than 100 items", i+1)
			}
		}
		finding := &submission.Findings[i]
		finding.Severity = submitted.Severity
		finding.Summary = submitted.Summary
		finding.Explanation = submitted.Explanation
		finding.File = submitted.File
		finding.Line = submitted.Line
		finding.Severity = strings.ToLower(strings.TrimSpace(finding.Severity))
		finding.Summary = strings.Join(strings.Fields(finding.Summary), " ")
		finding.Explanation = strings.TrimSpace(finding.Explanation)
		finding.File = path.Clean(strings.TrimSpace(finding.File))
		if err := validateFinding(*finding); err != nil {
			return submittedReview{}, fmt.Errorf("finding %d: %w", i+1, err)
		}
		if judging {
			finding.Sources, err = resolveFindingSources(sourceIDs, sourceIndex)
			if err != nil {
				return submittedReview{}, fmt.Errorf("finding %d: %w", i+1, err)
			}
		}
	}
	return submission, nil
}

func indexFindingSources(inputs []NamedReview) (map[string][]FindingSource, error) {
	index := make(map[string][]FindingSource)
	for _, input := range inputs {
		for position, finding := range input.Review.Findings {
			if strings.TrimSpace(finding.ID) == "" {
				return nil, fmt.Errorf("upstream review %q finding %d has no id", input.ID, position+1)
			}
			if _, exists := index[finding.ID]; exists {
				return nil, fmt.Errorf("duplicate upstream finding id %q", finding.ID)
			}
			sources := append([]FindingSource(nil), finding.Sources...)
			if len(sources) == 0 {
				sources = []FindingSource{{FindingID: finding.ID, Agent: input.ID, Model: input.Model}}
			}
			index[finding.ID] = sources
		}
	}
	return index, nil
}

func resolveFindingSources(ids []string, index map[string][]FindingSource) ([]FindingSource, error) {
	seenIDs := make(map[string]bool, len(ids))
	seenSources := make(map[FindingSource]bool)
	var sources []FindingSource
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return nil, errors.New("source_finding_ids cannot contain an empty id")
		}
		if seenIDs[id] {
			return nil, fmt.Errorf("duplicate source finding id %q", id)
		}
		seenIDs[id] = true
		resolved, ok := index[id]
		if !ok {
			return nil, fmt.Errorf("unknown source finding id %q", id)
		}
		for _, source := range resolved {
			if !seenSources[source] {
				seenSources[source] = true
				sources = append(sources, source)
			}
		}
	}
	return sources, nil
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
			if sources := formatFindingSources(finding.Sources); sources != "" {
				fmt.Fprintf(&output, "**Reported by:** %s\n\n", sources)
			}
			output.WriteString(finding.Explanation)
			output.WriteString("\n")
		}
		body = strings.TrimSpace(output.String())
	}
	return body + "\n\n---\n\n**Review stats:** " + formatReviewStats(review.Stats)
}

func formatFindingSources(sources []FindingSource) string {
	seen := make(map[string]bool, len(sources))
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		agent := strings.TrimSpace(source.Agent)
		model := strings.TrimSpace(source.Model)
		key := agent + "\x00" + model
		if agent == "" || seen[key] {
			continue
		}
		seen[key] = true
		label := "`" + strings.ReplaceAll(agent, "`", "\\`") + "`"
		if model != "" {
			label += " (`" + strings.ReplaceAll(model, "`", "\\`") + "`)"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
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
