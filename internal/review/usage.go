package review

import (
	"fmt"
	"time"
)

type usageTracker struct {
	total         tokenUsage
	inputPrice    float64
	outputPrice   float64
	totalCost     float64
	usageSeen     bool
	usageComplete bool
	costSeen      bool
	costComplete  bool
	costEstimated bool
}

func newUsageTracker(inputPrice, outputPrice float64) *usageTracker {
	return &usageTracker{
		inputPrice:    inputPrice,
		outputPrice:   outputPrice,
		usageComplete: true,
		costComplete:  true,
	}
}

func (u *usageTracker) Add(value tokenUsage) string {
	if !value.Reported() {
		u.Missing()
		return "provider did not report token usage"
	}
	u.usageSeen = true
	u.total.Add(value)
	cost, available, estimated := u.cost(value)
	if available {
		u.totalCost += cost
		u.costSeen = true
		u.costEstimated = u.costEstimated || estimated
	} else {
		u.costComplete = false
	}
	return formatUsage(value, cost, available, estimated)
}

func (u *usageTracker) Missing() {
	u.usageComplete = false
	u.costComplete = false
}

func (u *usageTracker) Stats(duration time.Duration) ReviewStats {
	result := ReviewStats{
		DurationMS:     duration.Milliseconds(),
		InputTokens:    u.total.PromptTokens,
		OutputTokens:   u.total.CompletionTokens,
		TotalTokens:    u.total.TotalTokens,
		UsageAvailable: u.usageSeen,
		UsageComplete:  u.usageSeen && u.usageComplete,
		CostComplete:   u.usageSeen && u.costSeen && u.costComplete,
	}
	if result.CostComplete {
		cost := u.totalCost
		result.Cost = &cost
		result.CostEstimated = u.costEstimated
	}
	return result
}

func (u *usageTracker) Summary(prefix string, duration time.Duration) string {
	if !u.usageSeen {
		return fmt.Sprintf("%s: token usage unavailable, cost unavailable, time %s", prefix, humanDuration(duration))
	}
	qualifier := ""
	if !u.usageComplete {
		qualifier = "reported "
	}
	message := fmt.Sprintf("%s: %s%d input + %d output = %d tokens", prefix, qualifier, u.total.PromptTokens, u.total.CompletionTokens, u.total.TotalTokens)
	if u.costSeen && u.costComplete {
		label := "total cost"
		if u.costEstimated {
			label = "estimated total cost"
		}
		message += fmt.Sprintf(", %s $%.6f", label, u.totalCost)
	} else {
		message += ", total cost unavailable"
	}
	return message + ", time " + humanDuration(duration)
}

func (u *usageTracker) cost(value tokenUsage) (cost float64, available, estimated bool) {
	if value.Cost != nil {
		return *value.Cost, true, false
	}
	if (u.inputPrice == 0 && u.outputPrice == 0) || (value.PromptTokens == 0 && value.CompletionTokens == 0) {
		return 0, false, false
	}
	cost = float64(value.PromptTokens)*u.inputPrice/1_000_000 + float64(value.CompletionTokens)*u.outputPrice/1_000_000
	return cost, true, true
}

func formatUsage(value tokenUsage, cost float64, available, estimated bool) string {
	message := fmt.Sprintf("%d input + %d output = %d tokens", value.PromptTokens, value.CompletionTokens, value.TotalTokens)
	if !available {
		return message + ", cost unavailable"
	}
	label := "cost"
	if estimated {
		label = "estimated cost"
	}
	return message + fmt.Sprintf(", %s $%.6f", label, cost)
}
