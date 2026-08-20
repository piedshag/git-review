package review

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReviewLimitsReserveFinalizationTime(t *testing.T) {
	started := time.Now()
	ctx, cancel := context.WithDeadline(t.Context(), started.Add(10*time.Minute))
	defer cancel()
	limits := newReviewLimits(ctx, started, 20)
	if !limits.hasTimeoutReserve {
		t.Fatal("deadline reserve was not created")
	}
	if want := started.Add(9 * time.Minute); !limits.finalizationAt.Equal(want) {
		t.Fatalf("finalization deadline=%s, want %s", limits.finalizationAt, want)
	}
}

func TestReviewLimitsAllowOneResponseRecovery(t *testing.T) {
	limits := newReviewLimits(t.Context(), time.Now(), 4)
	first := limits.HandleError(t.Context(), 1, errOutputLimit)
	if first.action != limitRetry || first.feedback == "" {
		t.Fatalf("first response limit decision: %+v", first)
	}
	second := limits.HandleError(t.Context(), 2, errResponseLimit)
	if second.action != limitInconclusive || second.message == "" {
		t.Fatalf("second response limit decision: %+v", second)
	}
}

func TestReviewLimitsDoNotSwallowOrdinaryErrors(t *testing.T) {
	limits := newReviewLimits(t.Context(), time.Now(), 4)
	decision := limits.HandleError(t.Context(), 1, errors.New("connection reset"))
	if decision.action != limitUnhandled {
		t.Fatalf("ordinary error was handled as a limit: %+v", decision)
	}
}

func TestReviewLimitsRequireJudgmentToolForJudge(t *testing.T) {
	limits := newReviewLimits(t.Context(), time.Now(), 1, submitJudgmentToolName)
	notice := limits.turnNotice(1)
	if notice == nil || !strings.Contains(notice.feedback, submitJudgmentToolName) || strings.Contains(notice.feedback, submitReviewToolName) {
		t.Fatalf("unexpected final judgment notice: %+v", notice)
	}
	decision := limits.HandleError(t.Context(), 0, errOutputLimit)
	if !strings.Contains(decision.feedback, submitJudgmentToolName) {
		t.Fatalf("unexpected judgment recovery feedback: %+v", decision)
	}
}
