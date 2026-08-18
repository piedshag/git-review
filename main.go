package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/piedshag/git-review/internal/gitrepo"
	"github.com/piedshag/git-review/internal/openai"
	"github.com/piedshag/git-review/internal/report"
	reviewpkg "github.com/piedshag/git-review/internal/review"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "git-review:", err)
		os.Exit(1)
	}
}

func run() error {
	options, err := parseOptions(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	instructions, err := reviewpkg.LoadInstructions(options.instructions, options.instructionsFile, os.Stdin)
	if err != nil {
		return err
	}
	snapshot, err := gitrepo.Open(options.repoPath, options.base, options.branch)
	if err != nil {
		return err
	}
	reporter := report.New(os.Stderr, options.verbose, options.debugModelOutput, terminalOutput(os.Stderr))
	model, err := openai.New(openai.Config{
		Endpoint:         options.endpoint,
		APIKey:           os.Getenv("OPENAI_API_KEY"),
		Model:            options.model,
		Stream:           options.stream,
		MaxResponseBytes: options.maxResponseMiB * 1024 * 1024,
		ExcludeReasoning: options.excludeReasoning,
		ReasoningEffort:  options.reasoningEffort,
		ExtraBody:        options.extraBody,
		Reporter:         reporter,
	})
	if err != nil {
		return err
	}
	reviewer, err := reviewpkg.New(reviewpkg.Config{
		MaxSteps:     options.maxSteps,
		Instructions: instructions,
		InputPrice:   options.inputPrice,
		OutputPrice:  options.outputPrice,
		Reporter:     reporter,
	}, snapshot, model)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), options.timeout)
	defer cancel()
	review, err := reviewer.Review(ctx)
	if err != nil {
		return err
	}
	return reviewpkg.Write(os.Stdout, options.format, review)
}
