package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const usage = `git-review reviews a Git branch with an OpenAI-compatible model.

Usage:
  git-review [flags] <branch> [flags]

Environment:
  OPENAI_API_KEY   API key (optional for local endpoints)
  OPENAI_BASE_URL  API base URL (default: https://api.openai.com/v1)
  OPENAI_MODEL     Model name (default: gpt-5)
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "git-review:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("git-review", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoPath := fs.String("repo", ".", "path inside the Git repository")
	base := fs.String("base", "", "base branch or revision (auto-detected when omitted)")
	model := fs.String("model", env("OPENAI_MODEL", "gpt-5"), "model name")
	endpoint := fs.String("endpoint", env("OPENAI_BASE_URL", "https://api.openai.com/v1"), "OpenAI-compatible API base URL")
	maxSteps := fs.Int("max-steps", envInt("GIT_REVIEW_MAX_STEPS", 20), "maximum model/tool turns")
	timeout := fs.Duration("timeout", 10*time.Minute, "overall review timeout")
	verbose := fs.Bool("v", false, "log review activity and token usage to stderr")
	debugModelOutput := fs.Bool("debug-model-output", false, "log complete parsed model responses to stderr")
	inputPrice := fs.Float64("input-price", 0, "input price in US dollars per million tokens")
	outputPrice := fs.Float64("output-price", 0, "output price in US dollars per million tokens")
	stream := fs.Bool("stream", true, "stream Chat Completions responses")
	maxResponseMiB := fs.Int("max-response-mib", 64, "maximum model response size per turn in MiB")
	excludeReasoning := fs.Bool("exclude-reasoning", false, "ask compatible endpoints not to return reasoning text")
	fs.Usage = func() { fmt.Fprint(fs.Output(), usage); fs.PrintDefaults() }
	if err := parseInterspersed(fs, os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("exactly one branch is required")
	}
	if *maxSteps < 1 || *maxSteps > 100 {
		return errors.New("max-steps must be between 1 and 100")
	}
	if *inputPrice < 0 || *outputPrice < 0 {
		return errors.New("token prices cannot be negative")
	}
	if *maxResponseMiB < 1 || *maxResponseMiB > 1024 {
		return errors.New("max-response-mib must be between 1 and 1024")
	}

	snapshot, err := Open(*repoPath, *base, fs.Arg(0))
	if err != nil {
		return err
	}
	client, err := NewClient(Config{
		Endpoint:         *endpoint,
		APIKey:           os.Getenv("OPENAI_API_KEY"),
		Model:            *model,
		MaxSteps:         *maxSteps,
		Verbose:          *verbose,
		DebugModelOutput: *debugModelOutput,
		InputPrice:       *inputPrice,
		OutputPrice:      *outputPrice,
		Stream:           *stream,
		MaxResponseBytes: *maxResponseMiB * 1024 * 1024,
		ExcludeReasoning: *excludeReasoning,
		LogWriter:        os.Stderr,
		Progress:         terminalOutput(os.Stderr),
		ProgressWriter:   os.Stderr,
	}, snapshot)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	review, err := client.Review(ctx)
	if err != nil {
		return err
	}
	fmt.Println(review)
	return nil
}

type boolFlag interface {
	IsBoolFlag() bool
}

// parseInterspersed lets flags appear before or after the branch. The standard
// flag package stops parsing at the first positional argument.
func parseInterspersed(fs *flag.FlagSet, arguments []string) error {
	flags := make([]string, 0, len(arguments))
	positionals := make([]string, 0, 1)
	for i := 0; i < len(arguments); i++ {
		argument := arguments[i]
		if argument == "--" {
			positionals = append(positionals, arguments[i+1:]...)
			break
		}
		if argument == "-" || !strings.HasPrefix(argument, "-") {
			positionals = append(positionals, argument)
			continue
		}

		flags = append(flags, argument)
		name := strings.TrimLeft(argument, "-")
		if before, _, found := strings.Cut(name, "="); found {
			name = before
			continue
		}
		definition := fs.Lookup(name)
		if definition == nil {
			continue
		}
		if boolean, ok := definition.Value.(boolFlag); ok && boolean.IsBoolFlag() {
			continue
		}
		if i+1 < len(arguments) {
			i++
			flags = append(flags, arguments[i])
		}
	}
	normalized := append(flags, "--")
	normalized = append(normalized, positionals...)
	return fs.Parse(normalized)
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

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}
