package review

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type limitAction uint8

const (
	limitUnhandled limitAction = iota
	limitRetry
	limitInconclusive
)

type limitNotice struct {
	activity string
	feedback string
}

type limitDecision struct {
	action   limitAction
	activity string
	feedback string
	message  string
}

// reviewLimits owns the policy for every bounded review resource. Transports
// only classify their errors; this policy decides whether to retry, stop, or
// let an ordinary error propagate.
type reviewLimits struct {
	maxTurns             int
	finalizationAt       time.Time
	hasTimeoutReserve    bool
	usingTimeoutReserve  bool
	timeoutRecoveryUsed  bool
	responseRecoveryUsed bool
	focusedRecoveryNext  bool
	submitTool           string
}

func newReviewLimits(ctx context.Context, started time.Time, maxTurns int, tools ...string) *reviewLimits {
	submitTool := submitReviewToolName
	if len(tools) > 0 && tools[0] != "" {
		submitTool = tools[0]
	}
	limits := &reviewLimits{maxTurns: maxTurns, submitTool: submitTool}
	deadline, ok := ctx.Deadline()
	if !ok {
		return limits
	}
	total := deadline.Sub(started)
	if total <= 0 {
		return limits
	}
	reserve := total / 5
	if reserve > time.Minute {
		reserve = time.Minute
	}
	limits.finalizationAt = deadline.Add(-reserve)
	limits.hasTimeoutReserve = true
	return limits
}

func (l *reviewLimits) BeginTurn(parent context.Context, step int) (context.Context, context.CancelFunc, *limitNotice) {
	notice := l.turnNotice(step)
	l.usingTimeoutReserve = l.hasTimeoutReserve && !l.timeoutRecoveryUsed && step < l.maxTurns
	if !l.usingTimeoutReserve {
		return parent, func() {}, notice
	}
	turnContext, cancel := context.WithDeadline(parent, l.finalizationAt)
	return turnContext, cancel, notice
}

func (l *reviewLimits) turnNotice(step int) *limitNotice {
	if l.focusedRecoveryNext {
		l.focusedRecoveryNext = false
		return nil
	}
	switch l.maxTurns - step + 1 {
	case 2:
		return &limitNotice{
			activity: "two model turns remain; requesting review completion",
			feedback: "You have two model turns remaining. Finish any essential inspection now and submit the review no later than your next turn.",
		}
	case 1:
		return &limitNotice{
			activity: "final model turn; requiring " + l.submitTool,
			feedback: "This is the final allowed model turn. Call " + l.submitTool + " exactly once and by itself now. Do not call inspection tools or return free-form text.",
		}
	default:
		return nil
	}
}

func (l *reviewLimits) HandleError(parent context.Context, step int, err error) limitDecision {
	if l.timeoutReserveExpired(parent, err) {
		l.timeoutRecoveryUsed = true
		l.focusedRecoveryNext = true
		return limitDecision{
			action:   limitRetry,
			activity: "review deadline approaching; reserving remaining time for submission",
			feedback: "The review deadline is approaching and the previous turn was stopped to preserve time. Use the remaining time to call " + l.submitTool + " exactly once and by itself now. Be concise and do not perform more inspection.",
		}
	}
	if feedback, activity, limited := responseLimitFeedback(err, l.submitTool); limited {
		if !l.responseRecoveryUsed && step < l.maxTurns {
			l.responseRecoveryUsed = true
			l.focusedRecoveryNext = true
			return limitDecision{action: limitRetry, activity: activity, feedback: feedback}
		}
		message := "The model could not complete a valid review within the configured response limits. No clean-review conclusion can be drawn."
		activity = "review inconclusive: response limit reached"
		if l.responseRecoveryUsed {
			message = "The model could not complete a valid review within the configured response limits after receiving a concise-submission retry. No clean-review conclusion can be drawn."
			activity = "review inconclusive: response limit reached after recovery"
		}
		return limitDecision{action: limitInconclusive, activity: activity, message: message}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return limitDecision{
			action:   limitInconclusive,
			activity: "review inconclusive: timeout reached",
			message:  "The configured review timeout elapsed before the review completed. No clean-review conclusion can be drawn. Increase --timeout if the model needs more time.",
		}
	}
	return limitDecision{action: limitUnhandled}
}

func (l *reviewLimits) timeoutReserveExpired(parent context.Context, err error) bool {
	return l.usingTimeoutReserve && errors.Is(err, context.DeadlineExceeded) &&
		parent.Err() == nil && !time.Now().Before(l.finalizationAt)
}

func (l *reviewLimits) Exhausted() limitDecision {
	return limitDecision{
		action:   limitInconclusive,
		activity: "review inconclusive: model turn limit reached",
		message:  fmt.Sprintf("The model did not submit a valid review within the %d-turn limit. No clean-review conclusion can be drawn. Increase --max-steps if this review requires more inspection.", l.maxTurns),
	}
}

func responseLimitFeedback(err error, tools ...string) (feedback, activity string, limited bool) {
	submitTool := submitReviewToolName
	if len(tools) > 0 && tools[0] != "" {
		submitTool = tools[0]
	}
	switch {
	case errors.Is(err, errOutputLimit):
		return "The provider truncated your previous response at its output limit. Call " + submitTool + " exactly once and by itself now. Be concise, do not perform more inspection, and include the required summary, strengths, weaknesses, and findings.",
			"provider output limit reached; requesting concise submission", true
	case errors.Is(err, errResponseLimit):
		return "Your previous streamed response approached the configured response-size limit. Call " + submitTool + " exactly once and by itself now. Be concise, do not perform more inspection, and do not include lengthy reasoning.",
			"response-size limit reached; requesting concise submission", true
	default:
		return "", "", false
	}
}
