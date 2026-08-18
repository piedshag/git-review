package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	reviewpkg "github.com/piedshag/git-review/internal/review"
)

const defaultMaxSteps = 30

const usage = `git-review reviews a Git branch with an OpenAI-compatible model.

Usage:
  git-review [flags] <branch> [flags]

The review is written to stdout. Progress is written to stderr.

Environment:
  OPENAI_API_KEY          API key (optional for local endpoints)
  OPENAI_BASE_URL         API base URL (default: https://api.openai.com/v1)
  OPENAI_MODEL            Model name (default: gpt-5)
  GIT_REVIEW_INSTRUCTIONS Additional review instructions
  GIT_REVIEW_EXTRA_BODY   Extra JSON object merged into each request body
`

type options struct {
	repoPath         string
	base             string
	branch           string
	model            string
	endpoint         string
	maxSteps         int
	timeout          time.Duration
	verbose          bool
	debugModelOutput bool
	inputPrice       float64
	outputPrice      float64
	stream           bool
	maxResponseMiB   int
	excludeReasoning bool
	reasoningEffort  string
	extraBody        map[string]json.RawMessage
	format           reviewpkg.OutputFormat
	instructions     string
	instructionsFile string
}

func parseOptions(arguments []string, output io.Writer) (options, error) {
	fs := flag.NewFlagSet("git-review", flag.ContinueOnError)
	fs.SetOutput(output)
	repoPath := fs.String("repo", ".", "path inside the Git repository")
	base := fs.String("base", "", "base branch or revision (auto-detected when omitted)")
	model := fs.String("model", env("OPENAI_MODEL", "gpt-5"), "model name")
	endpoint := fs.String("endpoint", env("OPENAI_BASE_URL", "https://api.openai.com/v1"), "OpenAI-compatible API base URL")
	maxSteps := fs.Int("max-steps", envInt("GIT_REVIEW_MAX_STEPS", defaultMaxSteps), "maximum model/tool turns")
	timeout := fs.Duration("timeout", 10*time.Minute, "overall review timeout")
	verbose := fs.Bool("v", false, "log review activity and token usage to stderr")
	debugModelOutput := fs.Bool("debug-model-output", false, "log complete parsed model responses to stderr")
	inputPrice := fs.Float64("input-price", 0, "input price in US dollars per million tokens")
	outputPrice := fs.Float64("output-price", 0, "output price in US dollars per million tokens")
	stream := fs.Bool("stream", true, "stream Chat Completions responses")
	maxResponseMiB := fs.Int("max-response-mib", 64, "maximum model response size per turn in MiB")
	excludeReasoning := fs.Bool("exclude-reasoning", false, "ask compatible endpoints not to return reasoning text")
	reasoningEffort := fs.String("reasoning-effort", "medium", "reasoning effort: none, minimal, low, medium, high, xhigh, max, or empty")
	formatName := fs.String("format", "markdown", "output format: markdown or json")
	instructions := fs.String("instructions", env("GIT_REVIEW_INSTRUCTIONS", ""), "additional review instructions")
	extraBody := fs.String("extra-body", env("GIT_REVIEW_EXTRA_BODY", ""), `extra JSON object merged into each request body, e.g. '{"chat_template_kwargs":{"enable_thinking":true}}'`)
	instructionsFile := fs.String("instructions-file", "", "read additional review instructions from a file, or - for stdin")
	fs.Usage = func() { fmt.Fprint(fs.Output(), usage); fs.PrintDefaults() }

	if err := parseInterspersed(fs, arguments); err != nil {
		return options{}, err
	}
	instructionsSet := false
	fs.Visit(func(option *flag.Flag) {
		instructionsSet = instructionsSet || option.Name == "instructions"
	})
	if strings.TrimSpace(*instructionsFile) != "" && !instructionsSet {
		// An explicit file takes precedence over the environment default.
		*instructions = ""
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return options{}, errors.New("exactly one branch is required")
	}
	format, err := reviewpkg.ParseOutputFormat(strings.ToLower(strings.TrimSpace(*formatName)))
	if err != nil {
		return options{}, err
	}
	if *maxSteps < 1 || *maxSteps > 100 {
		return options{}, errors.New("max-steps must be between 1 and 100")
	}
	if *timeout <= 0 {
		return options{}, errors.New("timeout must be positive")
	}
	if *inputPrice < 0 || *outputPrice < 0 {
		return options{}, errors.New("token prices cannot be negative")
	}
	if *maxResponseMiB < 1 || *maxResponseMiB > 1024 {
		return options{}, errors.New("max-response-mib must be between 1 and 1024")
	}
	if !validReasoningEffort(*reasoningEffort) {
		return options{}, errors.New("reasoning-effort must be none, minimal, low, medium, high, xhigh, max, or empty")
	}
	extra, err := parseExtraBody(*extraBody)
	if err != nil {
		return options{}, err
	}
	if instructionsSet && strings.TrimSpace(*instructions) != "" && strings.TrimSpace(*instructionsFile) != "" {
		return options{}, errors.New("instructions and instructions-file cannot be used together")
	}
	return options{
		repoPath:         *repoPath,
		base:             *base,
		branch:           fs.Arg(0),
		model:            *model,
		endpoint:         *endpoint,
		maxSteps:         *maxSteps,
		timeout:          *timeout,
		verbose:          *verbose,
		debugModelOutput: *debugModelOutput,
		inputPrice:       *inputPrice,
		outputPrice:      *outputPrice,
		stream:           *stream,
		maxResponseMiB:   *maxResponseMiB,
		excludeReasoning: *excludeReasoning,
		reasoningEffort:  *reasoningEffort,
		extraBody:        extra,
		format:           format,
		instructions:     *instructions,
		instructionsFile: *instructionsFile,
	}, nil
}

// parseExtraBody decodes the --extra-body JSON object. Providers put
// non-standard controls in the request body - vLLM and SGLang read
// chat_template_kwargs, for one - and this is the only way to reach them
// without a field per provider. Keys the client owns are rejected rather than
// silently overridden, so a typo cannot replace the messages or the tools.
func parseExtraBody(value string) (map[string]json.RawMessage, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	var extra map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &extra); err != nil {
		return nil, fmt.Errorf("extra-body must be a JSON object: %w", err)
	}
	if len(extra) == 0 {
		return nil, nil
	}
	for _, reserved := range []string{"model", "messages", "tools", "stream", "stream_options"} {
		if _, found := extra[reserved]; found {
			return nil, fmt.Errorf("extra-body cannot set %q", reserved)
		}
	}
	return extra, nil
}

func validReasoningEffort(value string) bool {
	switch value {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
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
