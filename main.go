package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/piedshag/git-review/internal/gitrepo"
	"github.com/piedshag/git-review/internal/gittools"
	"github.com/piedshag/git-review/internal/mcpserver"
	"github.com/piedshag/git-review/internal/openai"
	"github.com/piedshag/git-review/internal/report"
	reviewpkg "github.com/piedshag/git-review/internal/review"
	"github.com/piedshag/git-review/internal/toolset"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "git-review:", err)
		os.Exit(1)
	}
}

func run() error {
	arguments := os.Args[1:]
	if len(arguments) > 0 && arguments[0] == "mcp" {
		return runMCP(arguments[1:])
	}
	return runReview(arguments)
}

func runReview(arguments []string) error {
	options, err := parseOptions(arguments, os.Stderr)
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
	snapshot, tools, err := reviewTools(options.repoPath, options.base, options.branch)
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
	}, snapshot.Description(), tools, model)
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

func runMCP(arguments []string) error {
	options, err := parseMCPOptions(arguments, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	_, tools, err := reviewTools(options.repoPath, options.base, options.branch)
	if err != nil {
		return err
	}
	return mcpserver.RunStdio(context.Background(), tools)
}

func reviewTools(repoPath, base, branch string) (*gitrepo.Snapshot, *toolset.Registry, error) {
	snapshot, err := gitrepo.Open(repoPath, base, branch)
	if err != nil {
		return nil, nil, err
	}
	gitSet, err := gittools.New(snapshot)
	if err != nil {
		return nil, nil, err
	}
	submissionSet, err := reviewpkg.SubmissionTools()
	if err != nil {
		return nil, nil, err
	}
	tools, err := toolset.Combine(gitSet, submissionSet)
	if err != nil {
		return nil, nil, err
	}
	return snapshot, tools, nil
}
