package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/piedshag/git-review/internal/agent"
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
	Reporter         agent.Reporter
}

type Client struct {
	endpoint         string
	apiKey           string
	model            string
	http             *http.Client
	stream           bool
	maxResponseBytes int
	excludeReasoning bool
	reasoningEffort  string
	extraBody        map[string]json.RawMessage
	reporter         agent.Reporter
}

func New(config Config) (*Client, error) {
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("model is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(config.Endpoint))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid endpoint %q", config.Endpoint)
	}
	endpoint := strings.TrimRight(parsed.String(), "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = 64 * 1024 * 1024
	}
	if maxResponseBytes < 1 {
		return nil, errors.New("maximum response size must be positive")
	}
	reporter := config.Reporter
	if reporter == nil {
		reporter = agent.NopReporter{}
	}
	return &Client{
		endpoint:         endpoint,
		apiKey:           config.APIKey,
		model:            config.Model,
		http:             &http.Client{},
		stream:           config.Stream,
		maxResponseBytes: maxResponseBytes,
		excludeReasoning: config.ExcludeReasoning,
		reasoningEffort:  config.ReasoningEffort,
		extraBody:        config.ExtraBody,
		reporter:         reporter,
	}, nil
}

func (c *Client) Complete(ctx context.Context, messages []agent.Message, tools []agent.Tool) (agent.Message, agent.TokenUsage, error) {
	body := request{
		Model:           c.model,
		Messages:        messages,
		Tools:           tools,
		Stream:          c.stream,
		ReasoningEffort: c.reasoningEffort,
	}
	if c.excludeReasoning {
		body.Reasoning = &reasoningOptions{Exclude: true}
	}
	if c.stream {
		body.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return message{}, tokenUsage{}, err
	}
	payload, err = mergeExtraBody(payload, c.extraBody)
	if err != nil {
		return message{}, tokenUsage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return message{}, tokenUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return message{}, tokenUsage{}, fmt.Errorf("call model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return message{}, tokenUsage{}, decodeAPIError(resp)
	}
	if c.stream {
		return c.decodeStream(resp.Body)
	}
	return decodeCompletion(resp.Body, resp.StatusCode, c.maxResponseBytes)
}

func decodeCompletion(reader io.Reader, statusCode, limit int) (message, tokenUsage, error) {
	body, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return message{}, tokenUsage{}, err
	}
	if len(body) > limit {
		return message{}, tokenUsage{}, fmt.Errorf("%w (%s)", errResponseLimit, byteCount(limit))
	}
	var decoded response
	if err := json.Unmarshal(body, &decoded); err != nil {
		return message{}, tokenUsage{}, fmt.Errorf("decode model response (HTTP %d): %w", statusCode, err)
	}
	if decoded.Error != nil {
		return message{}, tokenUsage{}, errors.New(decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return message{}, tokenUsage{}, errors.New("model API returned no choices")
	}
	if decoded.Choices[0].FinishReason == "length" {
		return decoded.Choices[0].Message, decoded.Usage, errOutputLimit
	}
	return decoded.Choices[0].Message, decoded.Usage, nil
}

func decodeAPIError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return err
	}
	var decoded response
	if err := json.Unmarshal(body, &decoded); err != nil {
		return fmt.Errorf("model API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return fmt.Errorf("model API returned HTTP %d: %s", resp.StatusCode, decoded.Error.Message)
	}
	return fmt.Errorf("model API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// mergeExtraBody adds the caller's extra keys to an encoded request body.
// Merging encoded JSON keeps the request struct as the description of what the
// client itself sends: provider-specific controls pass through without a field
// each. Extra keys win over the struct's optional fields (reasoning_effort, for
// one) so an endpoint that wants a different spelling can be given it; the keys
// the client owns are refused at flag-parse time, not here.
func mergeExtraBody(payload []byte, extra map[string]json.RawMessage) ([]byte, error) {
	if len(extra) == 0 {
		return payload, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(payload, &merged); err != nil {
		return nil, fmt.Errorf("decode request body: %w", err)
	}
	for key, value := range extra {
		merged[key] = value
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}
	return encoded, nil
}
