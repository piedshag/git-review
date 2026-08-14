package toolset

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestObjectSchemaOmitsEmptyProperties(t *testing.T) {
	schema := ObjectSchema(nil, nil)
	if got := len(schema); got != 1 || schema["type"] != "object" {
		t.Fatalf("ObjectSchema(nil, nil) = %#v", schema)
	}
	if _, exists := schema["properties"]; exists {
		t.Fatal("empty object schema includes properties")
	}
}

func TestRegistryDispatchesAndCombines(t *testing.T) {
	newSet := func(name string) *Registry {
		set, err := New(Tool{
			Definition: Definition{Name: name, Description: "test tool", InputSchema: ObjectSchema(nil, nil)},
			Handler: func(_ context.Context, raw json.RawMessage) (Result, error) {
				return Result{Text: name + ":" + string(raw)}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return set
	}
	combined, err := Combine(newSet("one"), newSet("two"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(combined.Definitions()); got != 2 {
		t.Fatalf("definitions = %d, want 2", got)
	}
	result, err := combined.Call(context.Background(), "two", json.RawMessage(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != `two:{"ok":true}` {
		t.Fatalf("result = %q", result.Text)
	}
	if _, err := combined.Call(context.Background(), "missing", nil); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown tool error = %v", err)
	}
}

func TestRegistryRejectsDuplicateNames(t *testing.T) {
	definition := Definition{Name: "same", Description: "test", InputSchema: ObjectSchema(nil, nil)}
	handler := func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }
	_, err := New(Tool{Definition: definition, Handler: handler}, Tool{Definition: definition, Handler: handler})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("New duplicate error = %v", err)
	}
}
