package openai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseExtraBody validates provider-specific request fields. Keys owned by the
// client are rejected so configuration cannot replace the model, messages, or
// tools assembled by the review engine.
func ParseExtraBody(value string) (map[string]json.RawMessage, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	var extra map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &extra); err != nil {
		return nil, fmt.Errorf("extra-body must be a JSON object: %w", err)
	}
	if len(extra) == 0 {
		return nil, nil
	}
	for _, reserved := range []string{"model", "messages", "tools", "stream", "stream_options"} {
		if _, found := extra[reserved]; found {
			return nil, fmt.Errorf("extra-body cannot set %q", reserved)
		}
	}
	return extra, nil
}
