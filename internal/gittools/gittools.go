package gittools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/piedshag/git-review/internal/gitrepo"
	"github.com/piedshag/git-review/internal/toolset"
)

const maxToolOutput = 64 * 1024

func New(snapshot *gitrepo.Snapshot) (*toolset.Registry, error) {
	if snapshot == nil {
		return nil, errors.New("repository snapshot is required")
	}
	refProperty := map[string]any{
		"type": "string", "enum": []string{"target", "base"},
		"description": "Git snapshot to inspect; defaults to target",
	}
	return toolset.New(
		toolset.Tool{
			Definition: toolset.Definition{
				Name: "stat", Description: "List files changed between the merge-base and target, with status and line counts.",
				InputSchema: toolset.ObjectSchema(nil, nil),
			},
			Handler: func(ctx context.Context, raw json.RawMessage) (toolset.Result, error) {
				var args struct{}
				if err := decodeArguments(raw, &args); err != nil {
					return toolset.Result{}, err
				}
				output, err := snapshot.Stat(ctx)
				return textResult(output, err)
			},
		},
		toolset.Tool{
			Definition: toolset.Definition{
				Name: "diff", Description: "Show unified base-to-target diff hunks with target line numbers. Optionally restrict to one changed path.",
				InputSchema: toolset.ObjectSchema(map[string]any{
					"path":    map[string]any{"type": "string", "description": "Optional repository-relative changed file path"},
					"context": map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "description": "Unchanged lines around each hunk; defaults to 3"},
				}, nil),
			},
			Handler: func(ctx context.Context, raw json.RawMessage) (toolset.Result, error) {
				var args struct {
					Path    string `json:"path"`
					Context *int   `json:"context"`
				}
				if err := decodeArguments(raw, &args); err != nil {
					return toolset.Result{}, err
				}
				output, err := snapshot.Diff(ctx, args.Path, args.Context)
				return textResult(output, err)
			},
		},
		toolset.Tool{
			Definition: toolset.Definition{
				Name: "glob", Description: "List repository paths matching a glob. Supports *, ?, and **.",
				InputSchema: toolset.ObjectSchema(map[string]any{
					"pattern": map[string]any{"type": "string"}, "ref": refProperty,
				}, []string{"pattern"}),
			},
			Handler: func(ctx context.Context, raw json.RawMessage) (toolset.Result, error) {
				var args struct {
					Pattern string `json:"pattern"`
					Ref     string `json:"ref"`
				}
				if err := decodeArguments(raw, &args); err != nil {
					return toolset.Result{}, err
				}
				output, err := snapshot.Glob(ctx, args.Pattern, args.Ref)
				return textResult(output, err)
			},
		},
		toolset.Tool{
			Definition: toolset.Definition{
				Name: "grep", Description: "Search text files in a Git snapshot with a Go regular expression.",
				InputSchema: toolset.ObjectSchema(map[string]any{
					"pattern": map[string]any{"type": "string"},
					"glob":    map[string]any{"type": "string", "description": "Optional path glob"},
					"ref":     refProperty,
				}, []string{"pattern"}),
			},
			Handler: func(ctx context.Context, raw json.RawMessage) (toolset.Result, error) {
				var args struct {
					Pattern string `json:"pattern"`
					Glob    string `json:"glob"`
					Ref     string `json:"ref"`
				}
				if err := decodeArguments(raw, &args); err != nil {
					return toolset.Result{}, err
				}
				output, err := snapshot.Grep(ctx, args.Pattern, args.Glob, args.Ref)
				return textResult(output, err)
			},
		},
		toolset.Tool{
			Definition: toolset.Definition{
				Name: "read", Description: "Read numbered lines from a file stored in a Git snapshot. Use ref=base to inspect deleted or previous content.",
				InputSchema: toolset.ObjectSchema(map[string]any{
					"path": map[string]any{"type": "string"}, "ref": refProperty,
					"start": map[string]any{"type": "integer", "minimum": 1},
					"end":   map[string]any{"type": "integer", "minimum": 1},
				}, []string{"path"}),
			},
			Handler: func(ctx context.Context, raw json.RawMessage) (toolset.Result, error) {
				var args struct {
					Path  string `json:"path"`
					Ref   string `json:"ref"`
					Start int    `json:"start"`
					End   int    `json:"end"`
				}
				if err := decodeArguments(raw, &args); err != nil {
					return toolset.Result{}, err
				}
				output, err := snapshot.Read(ctx, args.Path, args.Ref, args.Start, args.End)
				return textResult(output, err)
			},
		},
	)
}

func decodeArguments(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("invalid arguments: multiple JSON values")
		}
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func textResult(output string, err error) (toolset.Result, error) {
	if err != nil {
		return toolset.Result{}, err
	}
	if len(output) > maxToolOutput {
		output = output[:maxToolOutput] + "\n[output truncated]"
	}
	return toolset.Result{Text: output}, nil
}
