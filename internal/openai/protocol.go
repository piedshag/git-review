package openai

import (
	"errors"

	"github.com/piedshag/git-review/internal/agent"
)

var (
	ErrOutputLimit   = errors.New("model response was truncated because it reached a provider output limit")
	ErrResponseLimit = errors.New("model response exceeded the configured response-size limit")
)

type message = agent.Message
type toolCall = agent.ToolCall
type functionCall = agent.FunctionCall
type reasoningDetail = agent.ReasoningDetail
type tokenUsage = agent.TokenUsage
type streamStats = agent.StreamStats
type Tool = agent.Tool

var (
	errOutputLimit   = ErrOutputLimit
	errResponseLimit = ErrResponseLimit
)

type request struct {
	Model           string            `json:"model"`
	Messages        []message         `json:"messages"`
	Tools           []Tool            `json:"tools"`
	Stream          bool              `json:"stream,omitempty"`
	StreamOptions   *streamOptions    `json:"stream_options,omitempty"`
	Reasoning       *reasoningOptions `json:"reasoning,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type reasoningOptions struct {
	Exclude bool `json:"exclude"`
}

type response struct {
	Choices []struct {
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
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
	Role             string            `json:"role"`
	Content          string            `json:"content"`
	Reasoning        string            `json:"reasoning"`
	ReasoningDetails []reasoningDetail `json:"reasoning_details"`
	ToolCalls        []toolCallDelta   `json:"tool_calls"`
}

type toolCallDelta struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

func byteCount(size int) string { return agent.ByteCount(size) }
