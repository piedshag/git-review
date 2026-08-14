package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/piedshag/git-review/internal/toolset"
)

func TestServerListsAndCallsSharedTools(t *testing.T) {
	registry, err := toolset.New(toolset.Tool{
		Definition: toolset.Definition{
			Name: "echo", Description: "Echo a value.",
			InputSchema:  toolset.ObjectSchema(map[string]any{"value": map[string]any{"type": "string"}}, []string{"value"}),
			OutputSchema: toolset.ObjectSchema(map[string]any{"value": map[string]any{"type": "string"}}, []string{"value"}),
		},
		Handler: func(_ context.Context, raw json.RawMessage) (toolset.Result, error) {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				return toolset.Result{}, err
			}
			return toolset.Result{Text: "echoed", Structured: value}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(registry)
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	listed, err := clientSession.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "echo" || !listed.Tools[0].Annotations.ReadOnlyHint {
		t.Fatalf("listed tools = %+v", listed.Tools)
	}
	result, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "echo", Arguments: map[string]any{"value": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != "echoed" {
		t.Fatalf("call result = %+v", result)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["value"] != "hello" {
		t.Fatalf("structured content = %#v", result.StructuredContent)
	}
}
