package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	reportpkg "github.com/piedshag/git-review/internal/report"
)

func TestReviewerExecutesToolsAndReturnsStructuredReview(t *testing.T) {
	var calls atomic.Int32
	var logs bytes.Buffer
	model := completionFunc(func(_ context.Context, messages []message, tools []Tool) (message, tokenUsage, error) {
		if len(tools) != 6 {
			t.Fatalf("got %d tools", len(tools))
		}
		if calls.Add(1) == 1 {
			return message{Role: "assistant", ToolCalls: []toolCall{{ID: "stat", Type: "function", Function: functionCall{Name: "stat", Arguments: `{}`}}}}, tokenUsage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, Cost: floatPointer(0.001)}, nil
		}
		toolResultFound := false
		for _, candidate := range messages {
			toolResultFound = toolResultFound || candidate.Role == "tool" && candidate.ToolCallID == "stat"
		}
		if !toolResultFound {
			t.Fatalf("tool result was not appended: %+v", messages)
		}
		return message{Role: "assistant", ToolCalls: []toolCall{{ID: "submit", Type: "function", Function: functionCall{Name: submitReviewToolName, Arguments: reviewArguments(`[]`)}}}}, tokenUsage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220, Cost: floatPointer(0.002)}, nil
	})
	reporter := reportpkg.New(&logs, true, false, false)
	reviewer, err := New(Config{MaxSteps: 3, Reporter: reporter}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewer.Review(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if review.Status != ReviewComplete || len(review.Findings) != 0 || calls.Load() != 2 {
		t.Fatalf("review=%+v calls=%d", review, calls.Load())
	}
	if review.Summary != testChangeSummary || review.Strengths != testStrengths || review.Weaknesses != testWeaknesses {
		t.Fatalf("review narrative was not preserved: %+v", review)
	}
	if review.Stats.TotalTokens != 330 || review.Stats.Cost == nil || *review.Stats.Cost != 0.003 || !review.Stats.UsageComplete || !review.Stats.CostComplete {
		t.Fatalf("unexpected review stats: %+v", review.Stats)
	}
	for _, expected := range []string{
		"thinking (step 1)",
		"tool: inspecting changed-file statistics",
		"usage step=1: 100 input + 10 output = 110 tokens, cost $0.001000",
		"review complete: 300 input + 30 output = 330 tokens, total cost $0.003000",
		"time ",
	} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("log does not contain %q:\n%s", expected, logs.String())
		}
	}
}

func TestReviewerRequestsStructuredSubmissionAfterFreeFormText(t *testing.T) {
	var calls atomic.Int32
	model := completionFunc(func(_ context.Context, messages []message, _ []Tool) (message, tokenUsage, error) {
		if calls.Add(1) == 1 {
			return message{Role: "assistant", Content: "Looks fine."}, tokenUsage{}, nil
		}
		correctionFound := false
		for _, candidate := range messages {
			correctionFound = correctionFound || candidate.Role == "user" && strings.Contains(candidate.Content, submitReviewToolName)
		}
		if !correctionFound {
			t.Fatalf("missing submission correction: %+v", messages)
		}
		return message{Role: "assistant", ToolCalls: []toolCall{{ID: "submit", Function: functionCall{Name: submitReviewToolName, Arguments: reviewArguments(`[]`)}}}}, tokenUsage{}, nil
	})
	reviewer, err := New(Config{MaxSteps: 3}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewer.Review(t.Context())
	if err != nil || review.Status != ReviewComplete || calls.Load() != 2 {
		t.Fatalf("review=%+v calls=%d err=%v", review, calls.Load(), err)
	}
}

func TestReviewerRejectsCleanSubmissionWithoutNarrative(t *testing.T) {
	var calls atomic.Int32
	model := completionFunc(func(_ context.Context, messages []message, _ []Tool) (message, tokenUsage, error) {
		if calls.Add(1) == 1 {
			return message{Role: "assistant", ToolCalls: []toolCall{{
				ID: "invalid", Function: functionCall{Name: submitReviewToolName, Arguments: `{"findings":[]}`},
			}}}, tokenUsage{}, nil
		}
		validationFound := false
		for _, candidate := range messages {
			validationFound = validationFound || candidate.Role == "tool" && strings.Contains(candidate.Content, "summary is required")
		}
		if !validationFound {
			t.Fatalf("missing narrative validation result: %+v", messages)
		}
		return message{Role: "assistant", ToolCalls: []toolCall{{
			ID: "valid", Function: functionCall{Name: submitReviewToolName, Arguments: reviewArguments(`[]`)},
		}}}, tokenUsage{}, nil
	})
	reviewer, err := New(Config{MaxSteps: 2}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewer.Review(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || review.Summary == "" || review.Strengths == "" || review.Weaknesses == "" {
		t.Fatalf("invalid final review: %+v calls=%d", review, calls.Load())
	}
}

func TestReviewerFeedsRemainingTurnLimitBackToModel(t *testing.T) {
	var calls atomic.Int32
	model := completionFunc(func(_ context.Context, messages []message, _ []Tool) (message, tokenUsage, error) {
		turn := calls.Add(1)
		last := messages[len(messages)-1]
		if turn == 1 {
			if last.Role != "user" || !strings.Contains(last.Content, "two model turns remaining") {
				t.Fatalf("missing two-turn warning: %+v", last)
			}
			return message{Role: "assistant", ToolCalls: []toolCall{{
				ID: "stat", Function: functionCall{Name: "stat", Arguments: `{}`},
			}}}, tokenUsage{}, nil
		}
		if last.Role != "user" || !strings.Contains(last.Content, "final allowed model turn") || !strings.Contains(last.Content, submitReviewToolName) {
			t.Fatalf("missing final-turn instruction: %+v", last)
		}
		return message{Role: "assistant", ToolCalls: []toolCall{{
			ID: "submit", Function: functionCall{Name: submitReviewToolName, Arguments: reviewArguments(`[]`)},
		}}}, tokenUsage{}, nil
	})
	reviewer, err := New(Config{MaxSteps: 2}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewer.Review(t.Context())
	if err != nil || review.Status != ReviewComplete || calls.Load() != 2 {
		t.Fatalf("review=%+v calls=%d err=%v", review, calls.Load(), err)
	}
}

func TestReviewerReturnsInconclusiveWhenTurnLimitIsExhausted(t *testing.T) {
	model := completionFunc(func(_ context.Context, messages []message, _ []Tool) (message, tokenUsage, error) {
		last := messages[len(messages)-1]
		if !strings.Contains(last.Content, "final allowed model turn") {
			t.Fatalf("missing final-turn instruction: %+v", last)
		}
		return message{Role: "assistant", Content: "I need more time to inspect the changes."}, tokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}, nil
	})
	reviewer, err := New(Config{MaxSteps: 1}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewer.Review(t.Context())
	if err != nil {
		t.Fatalf("turn exhaustion should not fail CI: %v", err)
	}
	if review.Status != ReviewInconclusive || !strings.Contains(review.Message, "1-turn limit") || review.Stats.TotalTokens != 15 {
		t.Fatalf("unexpected inconclusive review: %+v", review)
	}
}

func TestReviewerAddsCustomInstructionsWithoutReplacingContract(t *testing.T) {
	model := completionFunc(func(_ context.Context, messages []message, _ []Tool) (message, tokenUsage, error) {
		prompt := messages[0].Content
		if !strings.Contains(prompt, "Focus on database migrations") || !strings.Contains(prompt, "submit_review exactly once") {
			t.Fatalf("unexpected system instructions: %q", prompt)
		}
		return message{Role: "assistant", ToolCalls: []toolCall{{ID: "submit", Function: functionCall{Name: submitReviewToolName, Arguments: reviewArguments(`[]`)}}}}, tokenUsage{}, nil
	})
	reviewer, err := New(Config{MaxSteps: 1, Instructions: "Focus on database migrations"}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewer.Review(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestReviewerReturnsInconclusiveReviewForProviderOutputLimit(t *testing.T) {
	var calls atomic.Int32
	model := completionFunc(func(context.Context, []message, []Tool) (message, tokenUsage, error) {
		calls.Add(1)
		return message{}, tokenUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, Cost: floatPointer(0.001)}, errOutputLimit
	})
	reviewer, err := New(Config{MaxSteps: 3}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewer.Review(t.Context())
	if err != nil {
		t.Fatalf("truncated review should not fail CI: %v", err)
	}
	if review.Status != ReviewInconclusive || strings.Contains(renderMarkdown(review), "No findings") {
		t.Fatalf("unexpected review: %+v", review)
	}
	if calls.Load() != 2 || review.Stats.TotalTokens != 300 {
		t.Fatalf("expected one bounded recovery attempt, review=%+v calls=%d", review, calls.Load())
	}
}

func TestReviewerFeedsResponseLimitBackToModel(t *testing.T) {
	var calls atomic.Int32
	model := completionFunc(func(_ context.Context, messages []message, _ []Tool) (message, tokenUsage, error) {
		if calls.Add(1) == 1 {
			return message{}, tokenUsage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30}, errResponseLimit
		}
		last := messages[len(messages)-1]
		if last.Role != "user" || !strings.Contains(last.Content, "response-size limit") || !strings.Contains(last.Content, submitReviewToolName) {
			t.Fatalf("missing response-limit feedback: %+v", last)
		}
		return message{Role: "assistant", ToolCalls: []toolCall{{
			ID: "submit", Function: functionCall{Name: submitReviewToolName, Arguments: reviewArguments(`[]`)},
		}}}, tokenUsage{PromptTokens: 25, CompletionTokens: 5, TotalTokens: 30}, nil
	})
	reviewer, err := New(Config{MaxSteps: 4}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewer.Review(t.Context())
	if err != nil || review.Status != ReviewComplete || calls.Load() != 2 || review.Stats.TotalTokens != 60 {
		t.Fatalf("review=%+v calls=%d err=%v", review, calls.Load(), err)
	}
}

func TestReviewerUsesTimeoutReserveForFinalSubmission(t *testing.T) {
	var calls atomic.Int32
	model := completionFunc(func(ctx context.Context, messages []message, _ []Tool) (message, tokenUsage, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return message{}, tokenUsage{}, ctx.Err()
		}
		feedbackFound := false
		for _, candidate := range messages {
			feedbackFound = feedbackFound || candidate.Role == "user" && strings.Contains(candidate.Content, "deadline is approaching")
		}
		if !feedbackFound {
			t.Fatalf("missing deadline feedback: %+v", messages)
		}
		return message{Role: "assistant", ToolCalls: []toolCall{{
			ID: "submit", Function: functionCall{Name: submitReviewToolName, Arguments: reviewArguments(`[]`)},
		}}}, tokenUsage{}, nil
	})
	reviewer, err := New(Config{MaxSteps: 4}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	review, err := reviewer.Review(ctx)
	if err != nil || review.Status != ReviewComplete || calls.Load() != 2 {
		t.Fatalf("review=%+v calls=%d err=%v", review, calls.Load(), err)
	}
}

func TestReviewerReturnsInconclusiveReviewWhenDeadlineExpires(t *testing.T) {
	model := completionFunc(func(context.Context, []message, []Tool) (message, tokenUsage, error) {
		return message{}, tokenUsage{}, fmt.Errorf("read streamed model response: %w", context.DeadlineExceeded)
	})
	reviewer, err := New(Config{MaxSteps: 3}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewer.Review(t.Context())
	if err != nil {
		t.Fatalf("deadline should produce a CI-safe inconclusive review: %v", err)
	}
	if review.Status != ReviewInconclusive || !strings.Contains(review.Message, "timeout elapsed") {
		t.Fatalf("unexpected timeout review: %+v", review)
	}
}

func TestDescribeToolCallIncludesArgumentsAndRef(t *testing.T) {
	grep := describeToolCall(toolCall{Function: functionCall{Name: "grep", Arguments: `{"pattern":"TODO","glob":"**/*.go","ref":"base"}`}})
	read := describeToolCall(toolCall{Function: functionCall{Name: "read", Arguments: `{"path":"main.go","ref":"base","start":10,"end":20}`}})
	diff := describeToolCall(toolCall{Function: functionCall{Name: "diff", Arguments: `{"path":"main.go","context":5}`}})
	if grep != `Grepping base files matching "**/*.go" for "TODO"` {
		t.Fatalf("unexpected grep activity: %q", grep)
	}
	if read != `Reading "main.go" from base (lines 10-20)` {
		t.Fatalf("unexpected read activity: %q", read)
	}
	if diff != `Inspecting changes to "main.go" (5 context lines)` {
		t.Fatalf("unexpected diff activity: %q", diff)
	}
}

func TestUsageTrackerFallsBackToConfiguredPrices(t *testing.T) {
	tracker := newUsageTracker(0.50, 2.00)
	tracker.Add(tokenUsage{PromptTokens: 1_000_000, CompletionTokens: 500_000, TotalTokens: 1_500_000})
	result := tracker.Stats(time.Second)
	if result.Cost == nil || *result.Cost != 1.50 || !result.CostEstimated {
		t.Fatalf("unexpected cost: %+v", result)
	}
	if result.DurationMS != 1000 {
		t.Fatalf("unexpected duration: %+v", result)
	}
}

func TestReviewerPropagatesOrdinaryModelErrors(t *testing.T) {
	want := errors.New("network failed")
	model := completionFunc(func(context.Context, []message, []Tool) (message, tokenUsage, error) {
		return message{}, tokenUsage{}, want
	})
	reviewer, err := New(Config{MaxSteps: 1}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reviewer.Review(t.Context())
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func floatPointer(value float64) *float64 { return &value }
