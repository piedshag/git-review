package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var errOutputLimit = errors.New("model response was truncated because it reached a provider output limit")

type message struct {
	Role             string            `json:"role"`
	Content          string            `json:"content,omitempty"`
	Reasoning        string            `json:"reasoning,omitempty"`
	ReasoningDetails []reasoningDetail `json:"reasoning_details,omitempty"`
	ToolCalls        []toolCall        `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
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

type reasoningDetail struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Data      string          `json:"data,omitempty"`
	Signature json.RawMessage `json:"signature,omitempty"`
	ID        json.RawMessage `json:"id,omitempty"`
	Format    json.RawMessage `json:"format,omitempty"`
	Index     json.RawMessage `json:"index,omitempty"`
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

func (u tokenUsage) reported() bool {
	return u.PromptTokens != 0 || u.CompletionTokens != 0 || u.TotalTokens != 0 || u.Cost != nil
}

func (u *tokenUsage) add(other tokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
}

type streamStats struct {
	Chunks         int
	RawBytes       int
	ContentBytes   int
	ReasoningBytes int
	MetadataBytes  int
	ToolBytes      int
	LatestKind     string
	Latest         string
}

func (s streamStats) summary() string {
	parts := []string{fmt.Sprintf("%d chunks / %s raw", s.Chunks, byteCount(s.RawBytes))}
	if s.ReasoningBytes > 0 {
		parts = append(parts, "reasoning "+byteCount(s.ReasoningBytes))
	}
	if s.ContentBytes > 0 {
		parts = append(parts, "content "+byteCount(s.ContentBytes))
	}
	if s.ToolBytes > 0 {
		parts = append(parts, "tools "+byteCount(s.ToolBytes))
	}
	if s.MetadataBytes > 0 {
		parts = append(parts, "metadata "+byteCount(s.MetadataBytes))
	}
	return strings.Join(parts, ", ")
}

func preview(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func byteCount(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size >= 1024*1024 {
		return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
	}
	return fmt.Sprintf("%.1f KiB", float64(size)/1024)
}
