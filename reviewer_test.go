package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewerExecutesToolsAndReturnsStructuredReview(t *testing.T) {
	var calls atomic.Int32
	var logs bytes.Buffer
	model := completionFunc(func(_ context.Context, messages []message, tools []Tool) (message, tokenUsage, error) {
		if len(tools) != 5 {
			t.Fatalf("got %d tools", len(tools))
		}
		if calls.Add(1) == 1 {
			return message{Role: "assistant", ToolCalls: []toolCall{{ID: "stat", Type: "function", Function: functionCall{Name: "stat", Arguments: `{}`}}}}, tokenUsage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, Cost: floatPointer(0.001)}, nil
		}
		last := messages[len(messages)-1]
		if last.Role != "tool" || last.ToolCallID != "stat" {
			t.Fatalf("tool result was not appended: %+v", last)
		}
		return message{Role: "assistant", ToolCalls: []toolCall{{ID: "submit", Type: "function", Function: functionCall{Name: submitReviewToolName, Arguments: `{"findings":[]}`}}}}, tokenUsage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220, Cost: floatPointer(0.002)}, nil
	})
	reporter := newReporter(&logs, true, false, false)
	reviewer, err := newReviewer(ReviewerConfig{MaxSteps: 3, Reporter: reporter}, makeSnapshot(t), model)
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
		last := messages[len(messages)-1]
		if last.Role != "user" || !strings.Contains(last.Content, submitReviewToolName) {
			t.Fatalf("missing submission correction: %+v", last)
		}
		return message{Role: "assistant", ToolCalls: []toolCall{{ID: "submit", Function: functionCall{Name: submitReviewToolName, Arguments: `{"findings":[]}`}}}}, tokenUsage{}, nil
	})
	reviewer, err := newReviewer(ReviewerConfig{MaxSteps: 3}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewer.Review(t.Context())
	if err != nil || review.Status != ReviewComplete || calls.Load() != 2 {
		t.Fatalf("review=%+v calls=%d err=%v", review, calls.Load(), err)
	}
}

func TestReviewerAddsCustomInstructionsWithoutReplacingContract(t *testing.T) {
	model := completionFunc(func(_ context.Context, messages []message, _ []Tool) (message, tokenUsage, error) {
		prompt := messages[0].Content
		if !strings.Contains(prompt, "Focus on database migrations") || !strings.Contains(prompt, "submit_review exactly once") {
			t.Fatalf("unexpected system instructions: %q", prompt)
		}
		return message{Role: "assistant", ToolCalls: []toolCall{{ID: "submit", Function: functionCall{Name: submitReviewToolName, Arguments: `{"findings":[]}`}}}}, tokenUsage{}, nil
	})
	reviewer, err := newReviewer(ReviewerConfig{MaxSteps: 1, Instructions: "Focus on database migrations"}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewer.Review(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestReviewerReturnsInconclusiveReviewForProviderOutputLimit(t *testing.T) {
	model := completionFunc(func(context.Context, []message, []Tool) (message, tokenUsage, error) {
		return message{}, tokenUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, Cost: floatPointer(0.001)}, errOutputLimit
	})
	reviewer, err := newReviewer(ReviewerConfig{MaxSteps: 3}, makeSnapshot(t), model)
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
}

func TestReviewerReturnsInconclusiveReviewWhenDeadlineExpires(t *testing.T) {
	model := completionFunc(func(context.Context, []message, []Tool) (message, tokenUsage, error) {
		return message{}, tokenUsage{}, fmt.Errorf("read streamed model response: %w", context.DeadlineExceeded)
	})
	reviewer, err := newReviewer(ReviewerConfig{MaxSteps: 3}, makeSnapshot(t), model)
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
	if grep != `Grepping base files matching "**/*.go" for "TODO"` {
		t.Fatalf("unexpected grep activity: %q", grep)
	}
	if read != `Reading "main.go" from base (lines 10-20)` {
		t.Fatalf("unexpected read activity: %q", read)
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
	reviewer, err := newReviewer(ReviewerConfig{MaxSteps: 1}, makeSnapshot(t), model)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reviewer.Review(t.Context())
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func floatPointer(value float64) *float64 { return &value }
