package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/piedshag/git-review/internal/toolset"
)

type Message struct {
	Role             string            `json:"role"`
	Content          string            `json:"content,omitempty"`
	Reasoning        string            `json:"reasoning,omitempty"`
	ReasoningDetails []ReasoningDetail `json:"reasoning_details,omitempty"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ReasoningDetail struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Data      string          `json:"data,omitempty"`
	Signature json.RawMessage `json:"signature,omitempty"`
	ID        json.RawMessage `json:"id,omitempty"`
	Format    json.RawMessage `json:"format,omitempty"`
	Index     json.RawMessage `json:"index,omitempty"`
}

type TokenUsage struct {
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	Cost             *float64 `json:"cost,omitempty"`
}

func (u TokenUsage) Reported() bool {
	return u.PromptTokens != 0 || u.CompletionTokens != 0 || u.TotalTokens != 0 || u.Cost != nil
}

func (u *TokenUsage) Add(other TokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
}

type StreamStats struct {
	Chunks         int
	RawBytes       int
	ContentBytes   int
	ReasoningBytes int
	MetadataBytes  int
	ToolBytes      int
	LatestKind     string
	Latest         string
}

func (s StreamStats) Summary() string {
	parts := []string{fmt.Sprintf("%d chunks / %s raw", s.Chunks, ByteCount(s.RawBytes))}
	if s.ReasoningBytes > 0 {
		parts = append(parts, "reasoning "+ByteCount(s.ReasoningBytes))
	}
	if s.ContentBytes > 0 {
		parts = append(parts, "content "+ByteCount(s.ContentBytes))
	}
	if s.ToolBytes > 0 {
		parts = append(parts, "tools "+ByteCount(s.ToolBytes))
	}
	if s.MetadataBytes > 0 {
		parts = append(parts, "metadata "+ByteCount(s.MetadataBytes))
	}
	return strings.Join(parts, ", ")
}

func Preview(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func ByteCount(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size >= 1024*1024 {
		return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
	}
	return fmt.Sprintf("%.1f KiB", float64(size)/1024)
}

type CompletionClient interface {
	Complete(context.Context, []Message, []toolset.Definition) (Message, TokenUsage, error)
}

type Reporter interface {
	Next(format string, args ...any)
	Stream(StreamStats, time.Time)
	Debug(format string, args ...any)
	Finish()
	Close()
}

type NopReporter struct{}

func (NopReporter) Next(string, ...any)           {}
func (NopReporter) Stream(StreamStats, time.Time) {}
func (NopReporter) Debug(string, ...any)          {}
func (NopReporter) Finish()                       {}
func (NopReporter) Close()                        {}
