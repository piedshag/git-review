package review

import (
	"github.com/piedshag/git-review/internal/agent"
	"github.com/piedshag/git-review/internal/openai"
	"github.com/piedshag/git-review/internal/toolset"
)

type message = agent.Message
type toolCall = agent.ToolCall
type functionCall = agent.FunctionCall
type tokenUsage = agent.TokenUsage
type Tool = toolset.Definition

var (
	errOutputLimit   = openai.ErrOutputLimit
	errResponseLimit = openai.ErrResponseLimit
)

func objectSchema(properties map[string]any, required []string) map[string]any {
	return toolset.ObjectSchema(properties, required)
}
