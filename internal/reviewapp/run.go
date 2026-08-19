// Package reviewapp wires model clients to the reusable review engine.
package reviewapp

import (
	"context"
	"encoding/json"

	"github.com/piedshag/git-review/internal/agent"
	"github.com/piedshag/git-review/internal/gitrepo"
	"github.com/piedshag/git-review/internal/openai"
	"github.com/piedshag/git-review/internal/review"
)

type Config struct {
	Endpoint         string
	APIKey           string
	Model            string
	Stream           bool
	MaxResponseBytes int
	ExcludeReasoning bool
	ReasoningEffort  string
	ExtraBody        map[string]json.RawMessage
	MaxSteps         int
	Instructions     string
	Inputs           []review.NamedReview
	InputPrice       float64
	OutputPrice      float64
	Reporter         agent.Reporter
}

func Run(ctx context.Context, snapshot *gitrepo.Snapshot, config Config) (review.Review, error) {
	model, err := openai.New(openai.Config{
		Endpoint:         config.Endpoint,
		APIKey:           config.APIKey,
		Model:            config.Model,
		Stream:           config.Stream,
		MaxResponseBytes: config.MaxResponseBytes,
		ExcludeReasoning: config.ExcludeReasoning,
		ReasoningEffort:  config.ReasoningEffort,
		ExtraBody:        config.ExtraBody,
		Reporter:         config.Reporter,
	})
	if err != nil {
		return review.Review{}, err
	}
	reviewer, err := review.New(review.Config{
		MaxSteps:     config.MaxSteps,
		Instructions: config.Instructions,
		Inputs:       config.Inputs,
		InputPrice:   config.InputPrice,
		OutputPrice:  config.OutputPrice,
		Reporter:     config.Reporter,
	}, snapshot, model)
	if err != nil {
		return review.Review{}, err
	}
	return reviewer.Review(ctx)
}
