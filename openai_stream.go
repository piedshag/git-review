package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type streamDecoder struct {
	client       *openAIClient
	assembled    message
	toolCalls    map[int]*toolCall
	usage        tokenUsage
	dataLines    []string
	stats        streamStats
	started      time.Time
	receiving    bool
	processing   bool
	finishReason string
}

func (c *openAIClient) decodeStream(reader io.Reader) (message, tokenUsage, error) {
	decoder := &streamDecoder{
		client:    c,
		assembled: message{Role: "assistant"},
		toolCalls: make(map[int]*toolCall),
		started:   time.Now(),
	}
	bufferSize := min(64*1024, c.maxResponseBytes)
	stream := bufio.NewReaderSize(reader, bufferSize)
	done, err := decoder.read(stream)
	if err != nil {
		return message{}, decoder.usage, err
	}
	if !done && len(decoder.dataLines) > 0 {
		var eventDone bool
		eventDone, err = decoder.processEvent()
		if err != nil {
			return message{}, decoder.usage, err
		}
		done = eventDone
	}
	return decoder.result(done)
}

func (d *streamDecoder) read(reader *bufio.Reader) (bool, error) {
	var line []byte
	for {
		fragment, readErr := reader.ReadSlice('\n')
		d.stats.RawBytes += len(fragment)
		if d.stats.RawBytes > d.client.maxResponseBytes {
			return false, fmt.Errorf("streamed model response exceeded %s limit; increase --max-response-mib if this is expected", byteCount(d.client.maxResponseBytes))
		}
		line = append(line, fragment...)
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return false, fmt.Errorf("read streamed model response: %w", readErr)
		}
		if len(line) > 0 {
			text := strings.TrimSuffix(string(line), "\n")
			text = strings.TrimSuffix(text, "\r")
			done, err := d.consume(text)
			if err != nil || done {
				return done, err
			}
			line = line[:0]
		}
		if errors.Is(readErr, io.EOF) {
			return false, nil
		}
	}
}

func (d *streamDecoder) consume(line string) (bool, error) {
	switch {
	case line == "":
		return d.processEvent()
	case strings.HasPrefix(line, ":"):
		if !d.processing {
			d.client.reporter.Next("provider is processing the streamed request...")
			d.processing = true
		}
	case strings.HasPrefix(line, "data:"):
		d.dataLines = append(d.dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
	}
	return false, nil
}

func (d *streamDecoder) processEvent() (bool, error) {
	if len(d.dataLines) == 0 {
		return false, nil
	}
	data := strings.Join(d.dataLines, "\n")
	d.dataLines = d.dataLines[:0]
	if data == "[DONE]" {
		return true, nil
	}
	d.stats.Chunks++
	var chunk streamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return false, fmt.Errorf("decode streamed model response: %w", err)
	}
	if err := d.apply(chunk); err != nil {
		return false, err
	}
	if !d.receiving && len(chunk.Choices) > 0 {
		d.client.reporter.Next("Receiving streamed response")
		d.receiving = true
	}
	if d.receiving {
		d.client.reporter.Stream(d.stats, d.started)
	}
	return false, nil
}

func (d *streamDecoder) apply(chunk streamChunk) error {
	if chunk.Error != nil {
		return errors.New(chunk.Error.Message)
	}
	if chunk.Usage.reported() {
		d.usage = chunk.Usage
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != "" {
			d.finishReason = choice.FinishReason
		}
		if choice.FinishReason == "error" {
			return errors.New("model stream ended with an error")
		}
		mergeDelta(&d.assembled, d.toolCalls, choice.Delta, &d.stats)
	}
	return nil
}

func (d *streamDecoder) result(done bool) (message, tokenUsage, error) {
	d.assembled.ToolCalls = orderedToolCalls(d.toolCalls)
	if d.finishReason == "length" {
		return d.assembled, d.usage, errOutputLimit
	}
	if !done {
		return message{}, d.usage, errors.New("model stream ended before the [DONE] event")
	}
	if d.assembled.Content == "" && len(d.assembled.ToolCalls) == 0 {
		return message{}, tokenUsage{}, errors.New("model stream returned neither content nor tool calls")
	}
	return d.assembled, d.usage, nil
}

func mergeDelta(assembled *message, calls map[int]*toolCall, delta messageDelta, stats *streamStats) {
	if delta.Role != "" {
		assembled.Role = delta.Role
	}
	assembled.Content += delta.Content
	assembled.Reasoning += delta.Reasoning
	if delta.Content != "" {
		stats.ContentBytes += len(delta.Content)
		stats.LatestKind, stats.Latest = "content", delta.Content
	}
	if delta.Reasoning != "" {
		stats.ReasoningBytes += len(delta.Reasoning)
		stats.LatestKind, stats.Latest = "reasoning", delta.Reasoning
	}
	for _, detail := range delta.ReasoningDetails {
		assembled.ReasoningDetails = mergeReasoningDetail(assembled.ReasoningDetails, detail)
		text := detail.Text + detail.Summary
		stats.ReasoningBytes += len(text)
		if text != "" {
			stats.LatestKind, stats.Latest = "reasoning", text
		}
		stats.MetadataBytes += len(detail.Data) + len(detail.Signature)
	}
	for _, fragment := range delta.ToolCalls {
		call := calls[fragment.Index]
		if call == nil {
			call = &toolCall{}
			calls[fragment.Index] = call
		}
		call.ID += fragment.ID
		call.Type += fragment.Type
		call.Function.Name += fragment.Function.Name
		call.Function.Arguments += fragment.Function.Arguments
		toolText := fragment.Function.Name + fragment.Function.Arguments
		stats.ToolBytes += len(fragment.ID) + len(fragment.Type) + len(toolText)
		if toolText != "" {
			stats.LatestKind, stats.Latest = "tool", toolText
		}
	}
}

func orderedToolCalls(calls map[int]*toolCall) []toolCall {
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	result := make([]toolCall, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, *calls[index])
	}
	return result
}

func mergeReasoningDetail(details []reasoningDetail, fragment reasoningDetail) []reasoningDetail {
	for i := len(details) - 1; i >= 0; i-- {
		current := &details[i]
		if (len(current.Index) > 0 && len(fragment.Index) > 0 && !bytes.Equal(current.Index, fragment.Index)) ||
			(current.Type != "" && fragment.Type != "" && current.Type != fragment.Type) ||
			(len(current.ID) > 0 && len(fragment.ID) > 0 && !bytes.Equal(current.ID, fragment.ID)) {
			continue
		}
		current.Text += fragment.Text
		current.Summary += fragment.Summary
		current.Data += fragment.Data
		if len(fragment.Signature) > 0 {
			current.Signature = fragment.Signature
		}
		if len(current.ID) == 0 {
			current.ID = fragment.ID
		}
		if current.Type == "" {
			current.Type = fragment.Type
		}
		if len(current.Format) == 0 {
			current.Format = fragment.Format
		}
		if len(current.Index) == 0 {
			current.Index = fragment.Index
		}
		return details
	}
	return append(details, fragment)
}
