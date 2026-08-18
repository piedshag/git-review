package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/piedshag/git-review/internal/report"
)

func TestOpenAIClientSendsCompatibleChatCompletionRequest(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected request URL=%s auth=%q", r.URL, r.Header.Get("Authorization"))
		}
		var body request
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ReasoningEffort != "medium" || body.Reasoning == nil || !body.Reasoning.Exclude {
			t.Fatalf("reasoning options missing: %+v", body)
		}
		encoded, _ := json.Marshal(body)
		if strings.Contains(string(encoded), "max_completion_tokens") {
			t.Fatalf("request unexpectedly includes an output limit: %s", encoded)
		}
		return jsonResponse(`{"choices":[{"message":{"role":"assistant","content":"done"}}]}`), nil
	})
	client := makeOpenAIClient(t, Config{Endpoint: "http://model.test/v1", APIKey: "test-key", Model: "test-model", ExcludeReasoning: true, ReasoningEffort: "medium"}, transport)
	result, _, err := client.Complete(t.Context(), []message{{Role: "user", Content: "review"}}, nil)
	if err != nil || result.Content != "done" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if client.http.Timeout != 0 {
		t.Fatalf("HTTP timeout should defer to context, got %s", client.http.Timeout)
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
	reporter := report.New(&logs, true, false, false)
	client := makeOpenAIClient(t, Config{Endpoint: "http://model.test/v1", Model: "test-model", Stream: true, Reporter: reporter}, transport)
	result, usage, err := client.Complete(t.Context(), []message{{Role: "user", Content: "review"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Function.Name != "read" || result.ToolCalls[0].Function.Arguments != `{"path":"main.go"}` {
		t.Fatalf("unexpected tool call: %+v", result.ToolCalls)
	}
	if result.Reasoning != "Inspecting the changed file. Choosing a line range." || len(result.ReasoningDetails) != 1 || result.ReasoningDetails[0].Text != "Inspecting the file" {
		t.Fatalf("unexpected reasoning: %+v", result)
	}
	if usage.TotalTokens != 55 || usage.Cost == nil || *usage.Cost != 0.0001 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	for _, expected := range []string{"provider is processing", "Receiving streamed response", "reasoning"} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("stream log does not contain %q: %s", expected, logs.String())
		}
	}
}

func TestStreamingCompletionReportsProviderOutputLimit(t *testing.T) {
	client := makeOpenAIClient(t, Config{Endpoint: "http://model.test/v1", Model: "test-model"}, nil)
	stream := "data: {\"choices\":[{\"delta\":{\"reasoning\":\"still thinking\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"
	_, _, err := client.decodeStream(strings.NewReader(stream))
	if !errors.Is(err, errOutputLimit) {
		t.Fatalf("expected output limit error, got %v", err)
	}
}

func TestStreamingResponseLimitIncludesCommentsAndFraming(t *testing.T) {
	client, err := New(Config{Endpoint: "http://model.test/v1", Model: "test-model", MaxResponseBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	stream := strings.Repeat(": provider keepalive metadata\r\n", 5)
	_, _, err = client.decodeStream(strings.NewReader(stream))
	if !errors.Is(err, errResponseLimit) || !strings.Contains(err.Error(), "100 B") {
		t.Fatalf("expected comments and framing to consume response budget, got %v", err)
	}
}

func TestStreamingCompletionRejectsEOFBeforeDone(t *testing.T) {
	client := makeOpenAIClient(t, Config{Endpoint: "http://model.test/v1", Model: "test-model"}, nil)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"stop\"}]}\n\n"
	_, _, err := client.decodeStream(strings.NewReader(stream))
	if err == nil || !strings.Contains(err.Error(), "before the [DONE] event") {
		t.Fatalf("expected incomplete stream error, got %v", err)
	}
}

func TestStreamingCompletionChecksPendingFinishReasonAtEOF(t *testing.T) {
	client := makeOpenAIClient(t, Config{Endpoint: "http://model.test/v1", Model: "test-model"}, nil)
	stream := "data: {\"choices\":[{\"delta\":{\"reasoning\":\"partial\"},\"finish_reason\":\"length\"}]}"
	_, _, err := client.decodeStream(strings.NewReader(stream))
	if !errors.Is(err, errOutputLimit) {
		t.Fatalf("expected pending output-limit error, got %v", err)
	}
}

func TestStreamingCompletionAcceptsDoneAtEOF(t *testing.T) {
	client := makeOpenAIClient(t, Config{Endpoint: "http://model.test/v1", Model: "test-model"}, nil)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"complete\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]"
	result, _, err := client.decodeStream(strings.NewReader(stream))
	if err != nil || result.Content != "complete" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestEncryptedReasoningDataIsMetadataNotPreviewText(t *testing.T) {
	stats := streamStats{}
	assembled := message{}
	calls := make(map[int]*toolCall)
	mergeDelta(&assembled, calls, messageDelta{ReasoningDetails: []reasoningDetail{{
		Text: "visible reasoning", Data: "gAAAAABopaque-encrypted-payload", Signature: json.RawMessage(`"signature"`),
	}}}, &stats)

	if stats.ReasoningBytes != len("visible reasoning") {
		t.Fatalf("opaque data inflated reasoning count: %+v", stats)
	}
	wantMetadata := len("gAAAAABopaque-encrypted-payload") + len(`"signature"`)
	if stats.MetadataBytes != wantMetadata {
		t.Fatalf("metadata bytes=%d, want %d", stats.MetadataBytes, wantMetadata)
	}
	if stats.Latest != "visible reasoning" || strings.Contains(stats.Latest, "gAAAA") {
		t.Fatalf("opaque metadata leaked into preview: %+v", stats)
	}
	if assembled.ReasoningDetails[0].Data == "" {
		t.Fatal("reasoning metadata was not preserved for the next model turn")
	}
}

func TestCompletionResponseLimits(t *testing.T) {
	client, err := New(Config{Endpoint: "http://model.test/v1", Model: "test-model", MaxResponseBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	events := strings.Repeat("data: {\"choices\":[]}\n\n", 8) + "data: [DONE]\n\n"
	if _, _, err := client.decodeStream(strings.NewReader(events)); !errors.Is(err, errResponseLimit) || !strings.Contains(err.Error(), "100 B") {
		t.Fatalf("expected stream limit error, got %v", err)
	}
	body := `{"choices":[{"message":{"role":"assistant","content":"a response larger than the configured limit"}}]}`
	if _, _, err := decodeCompletion(strings.NewReader(body), http.StatusOK, 20); !errors.Is(err, errResponseLimit) || !strings.Contains(err.Error(), "20 B") {
		t.Fatalf("expected response limit error, got %v", err)
	}
}

func TestNewOpenAIClientRejectsNonURL(t *testing.T) {
	_, err := New(Config{Endpoint: "localhost:1234", Model: "test"})
	if err == nil || !strings.Contains(err.Error(), "invalid endpoint") {
		t.Fatalf("expected invalid endpoint error, got %v", err)
	}
}

func makeOpenAIClient(t *testing.T, config Config, transport http.RoundTripper) *Client {
	t.Helper()
	client, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if transport != nil {
		client.http = &http.Client{Transport: transport}
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestExtraBodyReachesTheWireAndOverridesOptionalFields(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if got := string(body["chat_template_kwargs"]); got != `{"enable_thinking":true}` {
			t.Fatalf("chat_template_kwargs=%s", got)
		}
		if got := string(body["reasoning_effort"]); got != `"high"` {
			t.Fatalf("extra body did not override reasoning_effort: %s", got)
		}
		if got := string(body["model"]); got != `"test-model"` {
			t.Fatalf("client-owned field was lost: %s", got)
		}
		return jsonResponse(`{"choices":[{"message":{"role":"assistant","content":"done"}}]}`), nil
	})
	extra := map[string]json.RawMessage{
		"chat_template_kwargs": json.RawMessage(`{"enable_thinking":true}`),
		"reasoning_effort":     json.RawMessage(`"high"`),
	}
	client := makeOpenAIClient(t, Config{Endpoint: "http://model.test/v1", Model: "test-model", ReasoningEffort: "medium", ExtraBody: extra}, transport)
	if _, _, err := client.Complete(t.Context(), []message{{Role: "user", Content: "review"}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestNoExtraBodyLeavesThePayloadUntouched(t *testing.T) {
	payload := []byte(`{"model":"test-model"}`)
	merged, err := mergeExtraBody(payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(merged, payload) {
		t.Fatalf("payload was rewritten: %s", merged)
	}
}
