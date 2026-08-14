package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Definition describes a transport-neutral tool. Protocol adapters are
// responsible for converting it to their wire format.
type Definition struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
}

// Result is returned by a tool handler. Final marks a terminal result, such as
// a submitted review, which the native review loop should return to its caller.
type Result struct {
	Text       string
	Structured any
	Final      bool
}

type Handler func(context.Context, json.RawMessage) (Result, error)

type Tool struct {
	Definition Definition
	Handler    Handler
}

type Set interface {
	Definitions() []Definition
	Call(context.Context, string, json.RawMessage) (Result, error)
}

type Registry struct {
	definitions []Definition
	handlers    map[string]Handler
}

func New(tools ...Tool) (*Registry, error) {
	registry := &Registry{handlers: make(map[string]Handler, len(tools))}
	for _, tool := range tools {
		definition := tool.Definition
		if definition.Name == "" {
			return nil, errors.New("tool name is required")
		}
		if definition.Description == "" {
			return nil, fmt.Errorf("tool %q description is required", definition.Name)
		}
		if definition.InputSchema == nil {
			return nil, fmt.Errorf("tool %q input schema is required", definition.Name)
		}
		if tool.Handler == nil {
			return nil, fmt.Errorf("tool %q handler is required", definition.Name)
		}
		if _, exists := registry.handlers[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate tool %q", definition.Name)
		}
		registry.definitions = append(registry.definitions, definition)
		registry.handlers[definition.Name] = tool.Handler
	}
	return registry, nil
}

func Combine(sets ...Set) (*Registry, error) {
	var tools []Tool
	for _, set := range sets {
		if set == nil {
			return nil, errors.New("tool set is required")
		}
		for _, definition := range set.Definitions() {
			definition := definition
			tools = append(tools, Tool{
				Definition: definition,
				Handler: func(ctx context.Context, arguments json.RawMessage) (Result, error) {
					return set.Call(ctx, definition.Name, arguments)
				},
			})
		}
	}
	return New(tools...)
}

func (r *Registry) Definitions() []Definition {
	return append([]Definition(nil), r.definitions...)
}

func (r *Registry) Call(ctx context.Context, name string, arguments json.RawMessage) (Result, error) {
	handler, ok := r.handlers[name]
	if !ok {
		return Result{}, fmt.Errorf("unknown tool %q", name)
	}
	return handler(ctx, arguments)
}

// ObjectSchema builds the compact object schemas used by all transports. Empty
// objects deliberately omit "properties": some OpenAI-compatible grammar
// compilers reject an explicitly empty properties object.
func ObjectSchema(properties map[string]any, required []string) map[string]any {
	if len(properties) == 0 {
		return map[string]any{"type": "object"}
	}
	result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}
