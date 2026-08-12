package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Endpoint  string
	APIKey    string
	Model     string
	MaxSteps  int
	Verbose   bool
	LogWriter io.Writer
}

type Client struct {
	endpoint string
	apiKey   string
	model    string
	maxSteps int
	http     *http.Client
	repo     *Snapshot
	logger   *log.Logger
}

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type request struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Tools    []Tool    `json:"tools"`
}

type response struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewClient(config Config, repo *Snapshot) (*Client, error) {
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
	client := &Client{endpoint: endpoint, apiKey: config.APIKey, model: config.Model, maxSteps: config.MaxSteps, http: &http.Client{Timeout: 2 * time.Minute}, repo: repo}
	if config.Verbose {
		writer := config.LogWriter
		if writer == nil {
			writer = io.Discard
		}
		client.logger = log.New(writer, "git-review: ", log.LstdFlags)
	}
	return client, nil
}

func (c *Client) Review(ctx context.Context) (string, error) {
	messages := []message{
		{Role: "system", Content: `You are a meticulous code reviewer. Inspect the branch using the provided read-only Git tools. Start with stat, then read every materially changed file and use grep/glob for context. Review only changes introduced between base and target. Report only concrete, actionable defects; do not report style preferences. For each finding give severity, file, target-branch line number, impact, and a concise fix. If there are no findings, say so explicitly. Never invent file contents or claim to have run code.`},
		{Role: "user", Content: "Review " + c.repo.Description() + "."},
	}
	for step := 0; step < c.maxSteps; step++ {
		assistant, err := c.complete(ctx, messages)
		if err != nil {
			return "", err
		}
		c.logModelResponse(step+1, assistant)
		messages = append(messages, assistant)
		if len(assistant.ToolCalls) == 0 {
			if strings.TrimSpace(assistant.Content) == "" {
				return "", errors.New("model returned neither content nor tool calls")
			}
			return assistant.Content, nil
		}
		for _, call := range assistant.ToolCalls {
			result := c.repo.Call(call.Function.Name, call.Function.Arguments)
			messages = append(messages, message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
	}
	return "", fmt.Errorf("model exceeded the %d-step tool limit", c.maxSteps)
}

func (c *Client) logModelResponse(step int, assistant message) {
	if c.logger == nil {
		return
	}
	encoded, err := json.Marshal(assistant)
	if err != nil {
		c.logger.Printf("model response step=%d: <could not encode: %v>", step, err)
		return
	}
	c.logger.Printf("model response step=%d: %s", step, encoded)
}

func (c *Client) complete(ctx context.Context, messages []message) (message, error) {
	payload, err := json.Marshal(request{Model: c.model, Messages: messages, Tools: c.repo.Tools()})
	if err != nil {
		return message{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return message{}, fmt.Errorf("call model: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return message{}, err
	}
	var decoded response
	if err := json.Unmarshal(body, &decoded); err != nil {
		return message{}, fmt.Errorf("decode model response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decoded.Error != nil && decoded.Error.Message != "" {
			return message{}, fmt.Errorf("model API returned HTTP %d: %s", resp.StatusCode, decoded.Error.Message)
		}
		return message{}, fmt.Errorf("model API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if decoded.Error != nil {
		return message{}, errors.New(decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return message{}, errors.New("model API returned no choices")
	}
	return decoded.Choices[0].Message, nil
}
