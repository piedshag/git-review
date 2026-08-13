package review

import (
	"github.com/piedshag/git-review/internal/agent"
	"github.com/piedshag/git-review/internal/openai"
)

type message = agent.Message
type toolCall = agent.ToolCall
type functionCall = agent.FunctionCall
type tokenUsage = agent.TokenUsage
type Tool = agent.Tool
type ToolFunction = agent.ToolFunction

var (
	errOutputLimit   = openai.ErrOutputLimit
	errResponseLimit = openai.ErrResponseLimit
)

func objectSchema(properties map[string]any, required []string) map[string]any {
	return agent.ObjectSchema(properties, required)
}
