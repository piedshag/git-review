package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/piedshag/git-review/internal/toolset"
)

const version = "0.1.0"

func New(tools toolset.Set) (*mcp.Server, error) {
	if tools == nil {
		return nil, errors.New("tool set is required")
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name: "git-review", Title: "Git Review", Version: version,
		Description: "Read-only Git snapshot inspection and structured review submission.",
	}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}})

	for _, definition := range tools.Definitions() {
		definition := definition
		server.AddTool(&mcp.Tool{
			Name:         definition.Name,
			Description:  definition.Description,
			InputSchema:  definition.InputSchema,
			OutputSchema: definition.OutputSchema,
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPointer(false),
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := tools.Call(ctx, definition.Name, request.Params.Arguments)
			if err != nil {
				response := &mcp.CallToolResult{}
				response.SetError(err)
				return response, nil
			}
			response := &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: result.Text}},
			}
			if result.Structured != nil {
				encoded, err := json.Marshal(result.Structured)
				if err != nil {
					response.SetError(err)
					return response, nil
				}
				response.StructuredContent = json.RawMessage(encoded)
			}
			return response, nil
		})
	}
	return server, nil
}

func RunStdio(ctx context.Context, tools toolset.Set) error {
	server, err := New(tools)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

func boolPointer(value bool) *bool { return &value }
