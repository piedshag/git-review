package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type Config struct {
	Endpoint         string
	APIKey           string
	Model            string
	MaxSteps         int
	Verbose          bool
	DebugModelOutput bool
	InputPrice       float64
	OutputPrice      float64
	Stream           bool
	LogWriter        io.Writer
	Progress         bool
	ProgressWriter   io.Writer
}

type Client struct {
	endpoint    string
	apiKey      string
	model       string
	maxSteps    int
	http        *http.Client
	repo        *Snapshot
	logger      *log.Logger
	verbose     bool
	debug       bool
	inputPrice  float64
	outputPrice float64
	stream      bool
	progress    *spinner
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
	Model         string         `json:"model"`
	Messages      []message      `json:"messages"`
	Tools         []Tool         `json:"tools"`
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type response struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
	Error *apiError  `json:"error,omitempty"`
	Usage tokenUsage `json:"usage"`
}

type apiError struct {
	Message string `json:"message"`
	Code    any    `json:"code,omitempty"`
}

type streamChunk struct {
	Choices []struct {
		Delta        messageDelta `json:"delta"`
		FinishReason string       `json:"finish_reason"`
	} `json:"choices"`
	Error *apiError  `json:"error,omitempty"`
	Usage tokenUsage `json:"usage"`
}

type messageDelta struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	ToolCalls []toolCallDelta `json:"tool_calls"`
}

type toolCallDelta struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type tokenUsage struct {
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	Cost             *float64 `json:"cost,omitempty"`
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
	client := &Client{
		endpoint: endpoint, apiKey: config.APIKey, model: config.Model,
		maxSteps: config.MaxSteps, http: &http.Client{}, repo: repo,
		verbose: config.Verbose, debug: config.DebugModelOutput,
		inputPrice: config.InputPrice, outputPrice: config.OutputPrice,
		stream: config.Stream,
	}
	if config.Verbose || config.DebugModelOutput {
		writer := config.LogWriter
		if writer == nil {
			writer = io.Discard
		}
		client.logger = log.New(writer, "git-review: ", log.LstdFlags)
	}
	if config.Progress && !config.Verbose && !config.DebugModelOutput {
		writer := config.ProgressWriter
		if writer == nil {
			writer = io.Discard
		}
		client.progress = newSpinner(writer)
	}
	return client, nil
}

func (c *Client) Review(ctx context.Context) (string, error) {
	if c.progress != nil {
		defer c.progress.Stop()
	}
	messages := []message{
		{Role: "system", Content: `You are a meticulous code reviewer. Inspect the branch using the provided read-only Git tools. Start with stat, then read every materially changed file and use grep/glob for context. Review only changes introduced between base and target. Report only concrete, actionable defects; do not report style preferences. For each finding give severity, file, target-branch line number, impact, and a concise fix. If there are no findings, say so explicitly. Never invent file contents or claim to have run code.`},
		{Role: "user", Content: "Review " + c.repo.Description() + "."},
	}
	c.activity("reviewing %s", c.repo.Description())
	var total tokenUsage
	var totalCost float64
	costComplete := true
	usageComplete := true
	costEstimated := false
	usageSeen := false
	for step := 0; step < c.maxSteps; step++ {
		c.activity("thinking (step %d)...", step+1)
		assistant, turnUsage, err := c.complete(ctx, messages)
		if err != nil {
			return "", err
		}
		c.logModelResponse(step+1, assistant)
		if turnUsage.reported() {
			usageSeen = true
			total.add(turnUsage)
			cost, available, estimated := c.usageCost(turnUsage)
			if available {
				totalCost += cost
				costEstimated = costEstimated || estimated
			} else {
				costComplete = false
			}
			c.logUsage("step", step+1, turnUsage, cost, available, estimated)
		} else {
			c.activity("usage step=%d: provider did not report token usage", step+1)
			costComplete = false
			usageComplete = false
		}
		messages = append(messages, assistant)
		if len(assistant.ToolCalls) == 0 {
			if strings.TrimSpace(assistant.Content) == "" {
				return "", errors.New("model returned neither content nor tool calls")
			}
			if usageSeen {
				c.logTotal(total, totalCost, costComplete, costEstimated, usageComplete)
			}
			return assistant.Content, nil
		}
		for _, call := range assistant.ToolCalls {
			c.logToolCall(call)
			result := c.repo.Call(call.Function.Name, call.Function.Arguments)
			messages = append(messages, message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
	}
	return "", fmt.Errorf("model exceeded the %d-step tool limit", c.maxSteps)
}

func (c *Client) logModelResponse(step int, assistant message) {
	if c.logger == nil || !c.debug {
		return
	}
	encoded, err := json.Marshal(assistant)
	if err != nil {
		c.logger.Printf("model response step=%d: <could not encode: %v>", step, err)
		return
	}
	c.logger.Printf("model response step=%d: %s", step, encoded)
}

func (c *Client) activity(format string, args ...any) {
	if c.logger != nil && c.verbose {
		c.logger.Printf(format, args...)
	} else if c.progress != nil {
		c.progress.Set(format, args...)
	}
}

func (c *Client) logToolCall(call toolCall) {
	var args struct {
		Pattern string `json:"pattern"`
		Glob    string `json:"glob"`
		Path    string `json:"path"`
		Ref     string `json:"ref"`
		Start   int    `json:"start"`
		End     int    `json:"end"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		c.toolActivity("Using %q with invalid arguments", call.Function.Name)
		return
	}
	ref := args.Ref
	if ref == "" {
		ref = "target"
	}
	switch call.Function.Name {
	case "stat":
		c.toolActivity("Inspecting changed-file statistics")
	case "glob":
		c.toolActivity("Listing %q in %s", args.Pattern, ref)
	case "grep":
		if args.Glob == "" {
			c.toolActivity("Grepping %s for %q", ref, args.Pattern)
		} else {
			c.toolActivity("Grepping %s files matching %q for %q", ref, args.Glob, args.Pattern)
		}
	case "read":
		lineRange := "all lines"
		if args.Start > 0 || args.End > 0 {
			lineRange = fmt.Sprintf("lines %d-%d", args.Start, args.End)
		}
		c.toolActivity("Reading %q from %s (%s)", args.Path, ref, lineRange)
	default:
		c.toolActivity("Using %q", call.Function.Name)
	}
}

func (c *Client) toolActivity(format string, args ...any) {
	if c.logger != nil && c.verbose {
		c.logger.Printf("tool: "+strings.ToLower(format[:1])+format[1:], args...)
	} else if c.progress != nil {
		c.progress.Set(format, args...)
	}
}

func (u tokenUsage) reported() bool {
	return u.PromptTokens != 0 || u.CompletionTokens != 0 || u.TotalTokens != 0 || u.Cost != nil
}

func (u *tokenUsage) add(other tokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
}

func (c *Client) usageCost(value tokenUsage) (cost float64, available, estimated bool) {
	if value.Cost != nil {
		return *value.Cost, true, false
	}
	if (c.inputPrice == 0 && c.outputPrice == 0) || (value.PromptTokens == 0 && value.CompletionTokens == 0) {
		return 0, false, false
	}
	cost = float64(value.PromptTokens)*c.inputPrice/1_000_000 + float64(value.CompletionTokens)*c.outputPrice/1_000_000
	return cost, true, true
}

func (c *Client) logUsage(label string, step int, value tokenUsage, cost float64, costAvailable, costEstimated bool) {
	if c.logger == nil || !c.verbose {
		return
	}
	message := fmt.Sprintf("usage %s=%d: %d input + %d output = %d tokens", label, step, value.PromptTokens, value.CompletionTokens, value.TotalTokens)
	if costAvailable {
		costLabel := "cost"
		if costEstimated {
			costLabel = "estimated cost"
		}
		message += fmt.Sprintf(", %s $%.6f", costLabel, cost)
	} else {
		message += ", cost unavailable"
	}
	c.logger.Print(message)
}

func (c *Client) logTotal(value tokenUsage, cost float64, costAvailable, costEstimated, usageComplete bool) {
	if c.logger == nil || !c.verbose {
		return
	}
	usageLabel := ""
	if !usageComplete {
		usageLabel = "reported "
	}
	message := fmt.Sprintf("review complete: %s%d input + %d output = %d tokens", usageLabel, value.PromptTokens, value.CompletionTokens, value.TotalTokens)
	if costAvailable {
		costLabel := "total cost"
		if costEstimated {
			costLabel = "estimated total cost"
		}
		message += fmt.Sprintf(", %s $%.6f", costLabel, cost)
	} else {
		message += ", total cost unavailable"
	}
	c.logger.Print(message)
}

func (c *Client) complete(ctx context.Context, messages []message) (message, tokenUsage, error) {
	requestBody := request{Model: c.model, Messages: messages, Tools: c.repo.Tools(), Stream: c.stream}
	if c.stream {
		requestBody.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	payload, err := json.Marshal(requestBody)
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
	return decodeCompletion(resp.Body, resp.StatusCode)
}

func decodeCompletion(reader io.Reader, statusCode int) (message, tokenUsage, error) {
	body, err := io.ReadAll(io.LimitReader(reader, 4*1024*1024))
	if err != nil {
		return message{}, tokenUsage{}, err
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

func (c *Client) decodeStream(reader io.Reader) (message, tokenUsage, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	assembled := message{Role: "assistant"}
	toolCalls := make(map[int]*toolCall)
	var streamUsage tokenUsage
	var dataLines []string
	totalBytes := 0
	receivingLogged := false
	processingLogged := false

	processEvent := func() (bool, error) {
		if len(dataLines) == 0 {
			return false, nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		if data == "[DONE]" {
			return true, nil
		}
		totalBytes += len(data)
		if totalBytes > 4*1024*1024 {
			return false, errors.New("streamed model response exceeded 4 MiB")
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return false, fmt.Errorf("decode streamed model response: %w", err)
		}
		if chunk.Error != nil {
			return false, errors.New(chunk.Error.Message)
		}
		if chunk.Usage.reported() {
			streamUsage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason == "error" {
				return false, errors.New("model stream ended with an error")
			}
			delta := choice.Delta
			if delta.Role != "" {
				assembled.Role = delta.Role
			}
			assembled.Content += delta.Content
			for _, fragment := range delta.ToolCalls {
				call := toolCalls[fragment.Index]
				if call == nil {
					call = &toolCall{}
					toolCalls[fragment.Index] = call
				}
				call.ID += fragment.ID
				call.Type += fragment.Type
				call.Function.Name += fragment.Function.Name
				call.Function.Arguments += fragment.Function.Arguments
			}
		}
		if !receivingLogged && len(chunk.Choices) > 0 {
			c.activity("receiving streamed response...")
			receivingLogged = true
		}
		return false, nil
	}

	done := false
streamLoop:
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			var err error
			done, err = processEvent()
			if err != nil {
				return message{}, tokenUsage{}, err
			}
			if done {
				break streamLoop
			}
		case strings.HasPrefix(line, ":"):
			if !processingLogged {
				c.activity("provider is processing the streamed request...")
				processingLogged = true
			}
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return message{}, tokenUsage{}, fmt.Errorf("read streamed model response: %w", err)
	}
	if !done && len(dataLines) > 0 {
		if _, err := processEvent(); err != nil {
			return message{}, tokenUsage{}, err
		}
	}
	indexes := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		assembled.ToolCalls = append(assembled.ToolCalls, *toolCalls[index])
	}
	if assembled.Content == "" && len(assembled.ToolCalls) == 0 {
		return message{}, tokenUsage{}, errors.New("model stream returned neither content nor tool calls")
	}
	return assembled, streamUsage, nil
}
