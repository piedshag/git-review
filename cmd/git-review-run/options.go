package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/piedshag/git-review/internal/cli"
	"github.com/piedshag/git-review/internal/review"
)

const usage = `git-review-run executes a configured graph of review agents.

Usage:
  git-review-run [flags] <branch> [flags]

The runner reads .git-review.toml by default. Independent agents run
concurrently; agents with inputs wait and adjudicate those reviews.
`

type options struct {
	configPath       string
	repoPath         string
	base             string
	branch           string
	format           review.OutputFormat
	verbose          bool
	debugModelOutput bool
}

func parseOptions(arguments []string, output io.Writer) (options, error) {
	fs := flag.NewFlagSet("git-review-run", flag.ContinueOnError)
	fs.SetOutput(output)
	configPath := fs.String("config", ".git-review.toml", "review run configuration file")
	repoPath := fs.String("repo", ".", "path inside the Git repository")
	base := fs.String("base", "", "base branch or revision (auto-detected when omitted)")
	formatName := fs.String("format", "markdown", "output format: markdown or json")
	verbose := fs.Bool("v", false, "log agent activity and token usage to stderr")
	debugModelOutput := fs.Bool("debug-model-output", false, "log complete parsed model responses to stderr")
	fs.Usage = func() { fmt.Fprint(fs.Output(), usage); fs.PrintDefaults() }

	if err := cli.ParseInterspersed(fs, arguments); err != nil {
		return options{}, err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return options{}, errors.New("exactly one branch is required")
	}
	format, err := review.ParseOutputFormat(strings.ToLower(strings.TrimSpace(*formatName)))
	if err != nil {
		return options{}, err
	}
	return options{
		configPath:       *configPath,
		repoPath:         *repoPath,
		base:             *base,
		branch:           fs.Arg(0),
		format:           format,
		verbose:          *verbose,
		debugModelOutput: *debugModelOutput,
	}, nil
}

func terminalOutput(file *os.File) bool {
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
