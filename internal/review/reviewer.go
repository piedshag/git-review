package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/piedshag/git-review/internal/agent"
	"github.com/piedshag/git-review/internal/toolset"
)

type Config struct {
	MaxSteps     int
	Instructions string
	InputPrice   float64
	OutputPrice  float64
	Reporter     agent.Reporter
}

type Reviewer struct {
	model        agent.CompletionClient
	target       string
	tools        toolset.Set
	definitions  []toolset.Definition
	maxSteps     int
	instructions string
	inputPrice   float64
	outputPrice  float64
	reporter     agent.Reporter
}

func New(config Config, target string, tools toolset.Set, model agent.CompletionClient) (*Reviewer, error) {
	if strings.TrimSpace(target) == "" {
		return nil, errors.New("review target description is required")
	}
	if tools == nil {
		return nil, errors.New("tool set is required")
	}
	if model == nil {
		return nil, errors.New("model client is required")
	}
	if config.MaxSteps < 1 || config.MaxSteps > 100 {
		return nil, errors.New("max steps must be between 1 and 100")
	}
	reporter := config.Reporter
	if reporter == nil {
		reporter = agent.NopReporter{}
	}
	return &Reviewer{
		model:        model,
		target:       target,
		tools:        tools,
		definitions:  tools.Definitions(),
		maxSteps:     config.MaxSteps,
		instructions: reviewInstructions(config.Instructions),
		inputPrice:   config.InputPrice,
		outputPrice:  config.OutputPrice,
		reporter:     reporter,
	}, nil
}

func (r *Reviewer) Review(ctx context.Context) (Review, error) {
	started := time.Now()
	limits := newReviewLimits(ctx, started, r.maxSteps)
	defer r.reporter.Close()
	messages := []message{
		{Role: "system", Content: r.instructions},
		{Role: "user", Content: "Review " + r.target + "."},
	}
	usage := newUsageTracker(r.inputPrice, r.outputPrice)
	r.reporter.Next("reviewing %s", r.target)

	for step := 1; step <= r.maxSteps; step++ {
		turnContext, cancelTurn, notice := limits.BeginTurn(ctx, step)
		if notice != nil {
			r.reporter.Next("%s", notice.activity)
			messages = append(messages, message{Role: "user", Content: notice.feedback})
		}
		r.reporter.Next("thinking (step %d)...", step)
		assistant, turnUsage, err := r.model.Complete(turnContext, messages, r.definitions)
		cancelTurn()
		if err != nil {
			decision := limits.HandleError(ctx, step, err)
			switch decision.action {
			case limitRetry:
				r.recordUsage(usage, step, turnUsage)
				r.reporter.Next("%s", decision.activity)
				messages = append(messages, message{Role: "user", Content: decision.feedback})
				continue
			case limitInconclusive:
				return r.finishInconclusive(usage, step, turnUsage, started,
					decision.message, decision.activity), nil
			default:
				return Review{}, err
			}
		}
		r.debugResponse(step, assistant)
		r.recordUsage(usage, step, turnUsage)
		messages = append(messages, assistant)

		if len(assistant.ToolCalls) == 0 {
			r.reporter.Next("model returned free-form output; requesting submit_review")
			messages = append(messages, message{Role: "user", Content: "Complete the review by calling submit_review. Free-form final text is not accepted."})
			continue
		}
		var submission *Submission
		messages, submission = r.runTools(ctx, messages, assistant.ToolCalls)
		if submission != nil {
			duration := time.Since(started)
			r.reporter.Next("%s", usage.Summary("review complete", duration))
			r.reporter.Finish()
			return Review{
				Status: ReviewComplete, Summary: submission.Summary, Strengths: submission.Strengths,
				Weaknesses: submission.Weaknesses, Findings: submission.Findings, Stats: usage.Stats(duration),
			}, nil
		}
	}
	decision := limits.Exhausted()
	duration := time.Since(started)
	r.reporter.Next("%s", usage.Summary(decision.activity, duration))
	r.reporter.Finish()
	return inconclusiveReview(decision.message, usage.Stats(duration)), nil
}

func (r *Reviewer) finishInconclusive(usage *usageTracker, step int, turnUsage tokenUsage, started time.Time, message, activity string) Review {
	r.recordUsage(usage, step, turnUsage)
	duration := time.Since(started)
	r.reporter.Next("%s", usage.Summary(activity, duration))
	r.reporter.Finish()
	return inconclusiveReview(message, usage.Stats(duration))
}

func (r *Reviewer) runTools(ctx context.Context, messages []message, calls []toolCall) ([]message, *Submission) {
	for _, call := range calls {
		r.reportToolCall(call)
		if call.Function.Name == submitReviewToolName && len(calls) != 1 {
			messages = append(messages, message{Role: "tool", ToolCallID: call.ID, Content: "error: submit_review must be called exactly once and by itself"})
			continue
		}
		result, err := r.tools.Call(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
		if err != nil {
			if call.Function.Name == submitReviewToolName {
				r.reporter.Next("review submission rejected: %v", err)
			}
			messages = append(messages, message{Role: "tool", ToolCallID: call.ID, Content: "error: " + err.Error()})
			continue
		}
		if result.Final {
			submission, ok := result.Structured.(Submission)
			if !ok {
				messages = append(messages, message{Role: "tool", ToolCallID: call.ID, Content: "error: final tool returned an unsupported result"})
				continue
			}
			return messages, &submission
		}
		messages = append(messages, message{Role: "tool", ToolCallID: call.ID, Content: result.Text})
	}
	return messages, nil
}

func (r *Reviewer) recordUsage(usage *usageTracker, step int, value tokenUsage) {
	if !value.Reported() {
		usage.Missing()
		r.reporter.Next("usage step=%d: provider did not report token usage", step)
		return
	}
	r.reporter.Next("usage step=%d: %s", step, usage.Add(value))
}

func (r *Reviewer) debugResponse(step int, assistant message) {
	encoded, err := json.Marshal(assistant)
	if err != nil {
		r.reporter.Debug("model response step=%d: <could not encode: %v>", step, err)
		return
	}
	r.reporter.Debug("model response step=%d: %s", step, encoded)
}

func (r *Reviewer) reportToolCall(call toolCall) {
	r.reporter.Next("tool: %s", strings.ToLower(describeToolCall(call)))
}

func describeToolCall(call toolCall) string {
	var args struct {
		Pattern string `json:"pattern"`
		Glob    string `json:"glob"`
		Path    string `json:"path"`
		Ref     string `json:"ref"`
		Start   int    `json:"start"`
		End     int    `json:"end"`
		Context *int   `json:"context"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return fmt.Sprintf("Using %q with invalid arguments", call.Function.Name)
	}
	ref := args.Ref
	if ref == "" {
		ref = "target"
	}
	switch call.Function.Name {
	case "stat":
		return "Inspecting changed-file statistics"
	case "diff":
		context := 3
		if args.Context != nil {
			context = *args.Context
		}
		if args.Path == "" {
			return fmt.Sprintf("Inspecting all changes (%d context lines)", context)
		}
		return fmt.Sprintf("Inspecting changes to %q (%d context lines)", args.Path, context)
	case "glob":
		return fmt.Sprintf("Listing %q in %s", args.Pattern, ref)
	case "grep":
		if args.Glob == "" {
			return fmt.Sprintf("Grepping %s for %q", ref, args.Pattern)
		}
		return fmt.Sprintf("Grepping %s files matching %q for %q", ref, args.Glob, args.Pattern)
	case "read":
		lineRange := "all lines"
		if args.Start > 0 || args.End > 0 {
			lineRange = fmt.Sprintf("lines %d-%d", args.Start, args.End)
		}
		return fmt.Sprintf("Reading %q from %s (%s)", args.Path, ref, lineRange)
	case submitReviewToolName:
		var submission struct {
			Findings []json.RawMessage `json:"findings"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &submission); err == nil && submission.Findings != nil {
			return fmt.Sprintf("Submitting review with %d findings", len(submission.Findings))
		}
		return "Submitting review"
	default:
		return fmt.Sprintf("Using %q", call.Function.Name)
	}
}

func inconclusiveReview(message string, stats ReviewStats) Review {
	return Review{
		Status:  ReviewInconclusive,
		Message: message,
		Stats:   stats,
	}
}
