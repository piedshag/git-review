package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/piedshag/git-review/internal/gitrepo"
	"github.com/piedshag/git-review/internal/openai"
	"github.com/piedshag/git-review/internal/pipeline"
	"github.com/piedshag/git-review/internal/report"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "git-review-run:", err)
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
	extraBody, err := openai.ParseExtraBody(os.Getenv("GIT_REVIEW_EXTRA_BODY"))
	if err != nil {
		return err
	}
	config, err := pipeline.Load(options.configPath, pipeline.RuntimeDefaults{
		Endpoint:        env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		APIKey:          os.Getenv("OPENAI_API_KEY"),
		Model:           env("OPENAI_MODEL", "gpt-5"),
		Stream:          true,
		MaxResponseMiB:  64,
		ReasoningEffort: "medium",
		ExtraBody:       extraBody,
		MaxSteps:        30,
		Timeout:         10 * time.Minute,
	})
	if err != nil {
		return err
	}
	snapshot, err := gitrepo.Open(options.repoPath, options.base, options.branch)
	if err != nil {
		return err
	}
	coordinator := report.NewCoordinator(os.Stderr, options.verbose, options.debugModelOutput, terminalOutput(os.Stderr))
	result, runErr := (pipeline.Runner{Snapshot: snapshot, Config: config, Coordinator: coordinator}).Run(context.Background())
	writeErr := pipeline.Write(os.Stdout, options.format, result)
	return errors.Join(runErr, writeErr)
}
