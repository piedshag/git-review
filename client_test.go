package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestReviewExecutesToolCallAndReturnsReport(t *testing.T) {
	snapshot := makeSnapshot(t)
	var calls atomic.Int32
	var logs bytes.Buffer
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected API path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing authorization header")
		}
		var body request
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		responseBody := ""
		if calls.Add(1) == 1 {
			if len(body.Tools) != 4 {
				t.Errorf("got %d tools", len(body.Tools))
			}
			responseBody = `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"stat","arguments":"{}"}}]}}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"cost":0.001}}`
		} else {
			last := body.Messages[len(body.Messages)-1]
			if last.Role != "tool" || last.ToolCallID != "call-1" {
				t.Errorf("tool result was not appended correctly: %+v", last)
			}
			responseBody = `{"choices":[{"message":{"role":"assistant","content":"No findings."}}],"usage":{"prompt_tokens":200,"completion_tokens":20,"total_tokens":220,"cost":0.002}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responseBody))}, nil
	})

	client, err := NewClient(Config{Endpoint: "http://model.test/v1", APIKey: "test-key", Model: "test-model", MaxSteps: 3, Verbose: true, LogWriter: &logs}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	client.http = &http.Client{Transport: transport}
	report, err := client.Review(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report != "No findings." || calls.Load() != 2 {
		t.Fatalf("report=%q calls=%d", report, calls.Load())
	}
	logOutput := logs.String()
	for _, expected := range []string{
		"thinking (step 1)",
		"tool: inspecting changed-file statistics",
		"usage step=1: 100 input + 10 output = 110 tokens, cost $0.001000",
		"thinking (step 2)",
		"review complete: 300 input + 30 output = 330 tokens, total cost $0.003000",
	} {
		if !strings.Contains(logOutput, expected) {
			t.Errorf("verbose log does not contain %q:\n%s", expected, logOutput)
		}
	}
	if strings.Contains(logOutput, `"role":"assistant"`) {
		t.Errorf("verbose log unexpectedly contains raw model output:\n%s", logOutput)
	}
}

func TestReviewDoesNotLogByDefault(t *testing.T) {
	client, err := NewClient(Config{Endpoint: "http://model.test/v1", Model: "test-model", MaxSteps: 1}, makeSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	if client.logger != nil {
		t.Fatal("logger is enabled without Verbose")
	}
}

func TestToolActivityIncludesArgumentsAndRef(t *testing.T) {
	var logs bytes.Buffer
	client, err := NewClient(Config{Endpoint: "http://model.test/v1", Model: "test-model", Verbose: true, LogWriter: &logs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.logToolCall(toolCall{Function: functionCall{Name: "grep", Arguments: `{"pattern":"TODO","glob":"**/*.go","ref":"base"}`}})
	client.logToolCall(toolCall{Function: functionCall{Name: "read", Arguments: `{"path":"main.go","ref":"base","start":10,"end":20}`}})
	for _, expected := range []string{
		`tool: grepping base files matching "**/*.go" for "TODO"`,
		`tool: reading "main.go" from base (lines 10-20)`,
	} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("activity log does not contain %q:\n%s", expected, logs.String())
		}
	}
}

func TestUsageCostFallsBackToConfiguredPrices(t *testing.T) {
	client := &Client{inputPrice: 0.50, outputPrice: 2.00}
	cost, available, estimated := client.usageCost(tokenUsage{PromptTokens: 1_000_000, CompletionTokens: 500_000})
	if !available || !estimated || cost != 1.50 {
		t.Fatalf("cost=%f available=%t estimated=%t", cost, available, estimated)
	}
}

func TestDebugModelOutputIsSeparateFromVerboseActivity(t *testing.T) {
	var logs bytes.Buffer
	client, err := NewClient(Config{Endpoint: "http://model.test/v1", Model: "test-model", DebugModelOutput: true, LogWriter: &logs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.logModelResponse(1, message{Role: "assistant", Content: "details"})
	if !strings.Contains(logs.String(), `"content":"details"`) {
		t.Fatalf("debug log did not contain model output: %s", logs.String())
	}
}

func TestStreamingCompletionAssemblesToolCallsAndUsage(t *testing.T) {
	var logs bytes.Buffer
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body request
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Stream || body.StreamOptions == nil || !body.StreamOptions.IncludeUsage {
			t.Fatalf("streaming usage was not requested: %+v", body)
		}
		stream := strings.Join([]string{
			`: OPENROUTER PROCESSING`,
			``,
			`data: {"choices":[{"delta":{"role":"assistant","reasoning":"Inspecting the changed file. ","reasoning_details":[{"type":"reasoning.text","text":"Inspecting ","id":"reason-1","index":0}],"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"re","arguments":"{\"pa"}}]}}]}`,
			``,
			`data: {"choices":[{"delta":{"reasoning":"Choosing a line range.","reasoning_details":[{"text":"the file"}],"tool_calls":[{"index":0,"function":{"name":"ad","arguments":"th\":\"main.go\"}"}}]},"finish_reason":"tool_calls"}]}`,
			``,
			`data: {"choices":[],"usage":{"prompt_tokens":50,"completion_tokens":5,"total_tokens":55,"cost":0.0001}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
	})
	client, err := NewClient(Config{Endpoint: "http://model.test/v1", Model: "test-model", Stream: true, Verbose: true, LogWriter: &logs}, makeSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	client.http = &http.Client{Transport: transport}
	result, resultUsage, err := client.complete(t.Context(), []message{{Role: "user", Content: "review"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Function.Name != "read" || result.ToolCalls[0].Function.Arguments != `{"path":"main.go"}` {
		t.Fatalf("unexpected assembled tool call: %+v", result.ToolCalls)
	}
	if result.Reasoning != "Inspecting the changed file. Choosing a line range." {
		t.Fatalf("stream reasoning was not assembled: %q", result.Reasoning)
	}
	if len(result.ReasoningDetails) != 1 || result.ReasoningDetails[0].Text != "Inspecting the file" {
		t.Fatalf("stream reasoning details were not assembled: %+v", result.ReasoningDetails)
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedResult), `"reasoning":"Inspecting the changed file. Choosing a line range."`) || !strings.Contains(string(encodedResult), `"index":0`) {
		t.Fatalf("reasoning will not be preserved in the next request: %s", encodedResult)
	}
	if resultUsage.TotalTokens != 55 || resultUsage.Cost == nil || *resultUsage.Cost != 0.0001 {
		t.Fatalf("unexpected stream usage: %+v", resultUsage)
	}
	if !strings.Contains(logs.String(), "provider is processing") || !strings.Contains(logs.String(), "Receiving streamed response") || !strings.Contains(logs.String(), "reasoning ") {
		t.Fatalf("missing stream progress logs: %s", logs.String())
	}
	if client.http.Timeout != 0 {
		t.Fatalf("HTTP timeout should defer to review context, got %s", client.http.Timeout)
	}
}

func TestExcludeReasoningIsSentWhenConfigured(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body request
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Reasoning == nil || !body.Reasoning.Exclude {
			t.Fatalf("reasoning exclusion was not requested: %+v", body.Reasoning)
		}
		responseBody := `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responseBody))}, nil
	})
	client, err := NewClient(Config{Endpoint: "http://model.test/v1", Model: "test-model", ExcludeReasoning: true}, makeSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	client.http = &http.Client{Transport: transport}
	if _, _, err := client.complete(t.Context(), []message{{Role: "user", Content: "review"}}); err != nil {
		t.Fatal(err)
	}
}

func TestStreamingCompletionHonorsConfiguredResponseLimit(t *testing.T) {
	client, err := NewClient(Config{Endpoint: "http://model.test/v1", Model: "test-model", MaxResponseBytes: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	events := strings.Repeat("data: {\"choices\":[]}\n\n", 8) + "data: [DONE]\n\n"
	_, _, err = client.decodeStream(strings.NewReader(events))
	if err == nil || !strings.Contains(err.Error(), "exceeded 100 B limit") {
		t.Fatalf("expected configured stream limit error, got %v", err)
	}
}

func TestNonStreamingCompletionHonorsConfiguredResponseLimit(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","content":"a response larger than the configured limit"}}]}`
	_, _, err := decodeCompletion(strings.NewReader(body), http.StatusOK, 20)
	if err == nil || !strings.Contains(err.Error(), "exceeded 20 B limit") {
		t.Fatalf("expected configured response limit error, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func makeSnapshot(t *testing.T) *Snapshot {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, _ := repo.Worktree()
	if err := os.WriteFile(filepath.Join(dir, "file.go"), []byte("package example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("."); err != nil {
		t.Fatal(err)
	}
	hash, err := worktree.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main", "feature"} {
		if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), hash)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := Open(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestNewClientRejectsNonURL(t *testing.T) {
	_, err := NewClient(Config{Endpoint: "localhost:1234", Model: "test"}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid endpoint") {
		t.Fatalf("expected invalid endpoint error, got %v", err)
	}
}
