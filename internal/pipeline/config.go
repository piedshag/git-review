package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/piedshag/git-review/internal/openai"
	"github.com/piedshag/git-review/internal/review"
	"github.com/piedshag/git-review/internal/reviewapp"
)

const maxAgents = 32

var agentIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

type RuntimeDefaults struct {
	Endpoint         string
	APIKey           string
	Model            string
	Stream           bool
	MaxResponseMiB   int
	ExcludeReasoning bool
	ReasoningEffort  string
	ExtraBody        map[string]json.RawMessage
	MaxSteps         int
	Timeout          time.Duration
	InputPrice       float64
	OutputPrice      float64
}

type Config struct {
	Output string
	Agents []Agent
}

type Agent struct {
	ID      string
	Inputs  []string
	Timeout time.Duration
	Job     reviewapp.Config
}

type fileConfig struct {
	Version  int           `toml:"version"`
	Output   string        `toml:"output"`
	Defaults optionSet     `toml:"defaults"`
	Agents   []agentConfig `toml:"agent"`
}

type optionSet struct {
	Model            string   `toml:"model"`
	Endpoint         string   `toml:"endpoint"`
	ReasoningEffort  string   `toml:"reasoning_effort"`
	ExtraBody        string   `toml:"extra_body"`
	Timeout          string   `toml:"timeout"`
	MaxSteps         *int     `toml:"max_steps"`
	Stream           *bool    `toml:"stream"`
	MaxResponseMiB   *int     `toml:"max_response_mib"`
	ExcludeReasoning *bool    `toml:"exclude_reasoning"`
	InputPrice       *float64 `toml:"input_price"`
	OutputPrice      *float64 `toml:"output_price"`
	Instructions     string   `toml:"instructions"`
	InstructionsFile string   `toml:"instructions_file"`
}

type agentConfig struct {
	ID     string   `toml:"id"`
	Inputs []string `toml:"inputs"`
	optionSet
}

func Load(filename string, runtime RuntimeDefaults) (Config, error) {
	var raw fileConfig
	metadata, err := toml.DecodeFile(filename, &raw)
	if err != nil {
		return Config{}, fmt.Errorf("read review run config: %w", err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("unknown review run config key %q", undecoded[0].String())
	}
	if raw.Version != 1 {
		return Config{}, fmt.Errorf("review run config version must be 1, got %d", raw.Version)
	}
	if len(raw.Agents) == 0 {
		return Config{}, errors.New("review run config requires at least one agent")
	}
	if len(raw.Agents) > maxAgents {
		return Config{}, fmt.Errorf("review run config cannot contain more than %d agents", maxAgents)
	}

	directory := filepath.Dir(filename)
	config := Config{Output: strings.TrimSpace(raw.Output), Agents: make([]Agent, len(raw.Agents))}
	for index, rawAgent := range raw.Agents {
		id := strings.TrimSpace(rawAgent.ID)
		resolved, err := resolveOptions(runtime, raw.Defaults, rawAgent.optionSet, directory)
		if err != nil {
			return Config{}, fmt.Errorf("agent %q: %w", id, err)
		}
		config.Agents[index] = Agent{ID: id, Inputs: append([]string(nil), rawAgent.Inputs...), Timeout: resolved.timeout, Job: resolved.job}
	}
	if err := validateGraph(&config); err != nil {
		return Config{}, err
	}
	return config, nil
}

type resolvedOptions struct {
	job     reviewapp.Config
	timeout time.Duration
}

func resolveOptions(runtime RuntimeDefaults, defaults, override optionSet, directory string) (resolvedOptions, error) {
	model := first(override.Model, defaults.Model, runtime.Model)
	endpoint := first(override.Endpoint, defaults.Endpoint, runtime.Endpoint)
	effort := firstAllowEmpty(override.ReasoningEffort, defaults.ReasoningEffort, runtime.ReasoningEffort)
	timeoutText := first(override.Timeout, defaults.Timeout)
	timeout := runtime.Timeout
	if timeoutText != "" {
		parsed, err := time.ParseDuration(timeoutText)
		if err != nil {
			return resolvedOptions{}, fmt.Errorf("invalid timeout: %w", err)
		}
		timeout = parsed
	}
	if timeout <= 0 {
		return resolvedOptions{}, errors.New("timeout must be positive")
	}
	maxSteps := inheritedInt(override.MaxSteps, defaults.MaxSteps, runtime.MaxSteps)
	maxResponseMiB := inheritedInt(override.MaxResponseMiB, defaults.MaxResponseMiB, runtime.MaxResponseMiB)
	inputPrice := inheritedFloat(override.InputPrice, defaults.InputPrice, runtime.InputPrice)
	outputPrice := inheritedFloat(override.OutputPrice, defaults.OutputPrice, runtime.OutputPrice)
	if strings.TrimSpace(model) == "" {
		return resolvedOptions{}, errors.New("model is required")
	}
	parsedEndpoint, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsedEndpoint.Scheme == "" || parsedEndpoint.Host == "" {
		return resolvedOptions{}, fmt.Errorf("invalid endpoint %q", endpoint)
	}
	if maxSteps < 1 || maxSteps > 100 {
		return resolvedOptions{}, errors.New("max_steps must be between 1 and 100")
	}
	if maxResponseMiB < 1 || maxResponseMiB > 1024 {
		return resolvedOptions{}, errors.New("max_response_mib must be between 1 and 1024")
	}
	if inputPrice < 0 || outputPrice < 0 {
		return resolvedOptions{}, errors.New("token prices cannot be negative")
	}
	if !validReasoningEffort(effort) {
		return resolvedOptions{}, errors.New("reasoning_effort must be none, minimal, low, medium, high, xhigh, max, or empty")
	}
	extraBody := runtime.ExtraBody
	if encodedExtra := first(override.ExtraBody, defaults.ExtraBody); encodedExtra != "" {
		parsedExtra, err := openai.ParseExtraBody(encodedExtra)
		if err != nil {
			return resolvedOptions{}, err
		}
		extraBody = parsedExtra
	}
	commonInstructions, err := loadInstructions(defaults, directory)
	if err != nil {
		return resolvedOptions{}, err
	}
	agentInstructions, err := loadInstructions(override, directory)
	if err != nil {
		return resolvedOptions{}, err
	}
	instructions := strings.TrimSpace(strings.Join(nonempty(commonInstructions, agentInstructions), "\n\n"))
	return resolvedOptions{
		timeout: timeout,
		job: reviewapp.Config{
			Endpoint:         endpoint,
			APIKey:           runtime.APIKey,
			Model:            model,
			Stream:           inheritedBool(override.Stream, defaults.Stream, runtime.Stream),
			MaxResponseBytes: maxResponseMiB * 1024 * 1024,
			ExcludeReasoning: inheritedBool(override.ExcludeReasoning, defaults.ExcludeReasoning, runtime.ExcludeReasoning),
			ReasoningEffort:  effort,
			ExtraBody:        extraBody,
			MaxSteps:         maxSteps,
			Instructions:     instructions,
			InputPrice:       inputPrice,
			OutputPrice:      outputPrice,
		},
	}, nil
}

func loadInstructions(options optionSet, directory string) (string, error) {
	if strings.TrimSpace(options.Instructions) != "" && strings.TrimSpace(options.InstructionsFile) != "" {
		return "", errors.New("instructions and instructions_file cannot be used together")
	}
	filename := strings.TrimSpace(options.InstructionsFile)
	if filename == "" {
		return strings.TrimSpace(options.Instructions), nil
	}
	if filename == "-" {
		return "", errors.New("instructions_file cannot be stdin in a concurrent review run")
	}
	if !filepath.IsAbs(filename) {
		filename = filepath.Join(directory, filename)
	}
	file, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf("read instructions: %w", err)
	}
	defer file.Close()
	return review.LoadInstructions("", "-", io.LimitReader(file, 1024*1024+1))
}

func validateGraph(config *Config) error {
	if len(config.Agents) == 0 {
		return errors.New("review run config requires at least one agent")
	}
	if len(config.Agents) > maxAgents {
		return fmt.Errorf("review run config cannot contain more than %d agents", maxAgents)
	}
	known := make(map[string]bool, len(config.Agents))
	for index, agent := range config.Agents {
		if !agentIDPattern.MatchString(agent.ID) {
			return fmt.Errorf("agent %d: id must start with a letter and contain only letters, digits, underscores, or hyphens", index+1)
		}
		if known[agent.ID] {
			return fmt.Errorf("duplicate agent id %q", agent.ID)
		}
		if agent.Timeout <= 0 {
			return fmt.Errorf("agent %q: timeout must be positive", agent.ID)
		}
		known[agent.ID] = true
	}
	dependents := make(map[string]int, len(config.Agents))
	for index := range config.Agents {
		agent := &config.Agents[index]
		seenInputs := make(map[string]bool, len(agent.Inputs))
		for inputIndex, input := range agent.Inputs {
			input = strings.TrimSpace(input)
			agent.Inputs[inputIndex] = input
			if !known[input] {
				return fmt.Errorf("agent %q: unknown input %q", agent.ID, input)
			}
			if input == agent.ID {
				return fmt.Errorf("agent %q cannot depend on itself", agent.ID)
			}
			if seenInputs[input] {
				return fmt.Errorf("agent %q: duplicate input %q", agent.ID, input)
			}
			seenInputs[input] = true
			dependents[input]++
		}
	}
	if err := detectCycle(config.Agents); err != nil {
		return err
	}
	if config.Output == "" {
		var sinks []string
		for _, agent := range config.Agents {
			if dependents[agent.ID] == 0 {
				sinks = append(sinks, agent.ID)
			}
		}
		if len(sinks) != 1 {
			return errors.New("output is required when the agent graph does not have exactly one sink")
		}
		config.Output = sinks[0]
	}
	if !known[config.Output] {
		return fmt.Errorf("unknown output agent %q", config.Output)
	}
	return nil
}

func detectCycle(agents []Agent) error {
	inputs := make(map[string][]string, len(agents))
	for _, agent := range agents {
		inputs[agent.ID] = agent.Inputs
	}
	state := make(map[string]uint8, len(agents))
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("agent graph contains a cycle involving %q", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, input := range inputs[id] {
			if err := visit(input); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, agent := range agents {
		if err := visit(agent.ID); err != nil {
			return err
		}
	}
	return nil
}

func inheritedInt(override, defaults *int, fallback int) int {
	if override != nil {
		return *override
	}
	if defaults != nil {
		return *defaults
	}
	return fallback
}

func inheritedFloat(override, defaults *float64, fallback float64) float64 {
	if override != nil {
		return *override
	}
	if defaults != nil {
		return *defaults
	}
	return fallback
}

func inheritedBool(override, defaults *bool, fallback bool) bool {
	if override != nil {
		return *override
	}
	if defaults != nil {
		return *defaults
	}
	return fallback
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstAllowEmpty(values ...string) string {
	// TOML cannot distinguish an omitted string from an explicitly empty one;
	// empty therefore inherits, matching the single-review CLI's defaults.
	return first(values...)
}

func nonempty(values ...string) []string {
	var result []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

func validReasoningEffort(value string) bool {
	switch value {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}
