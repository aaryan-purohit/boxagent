package agent

import (
	"context"
	"encoding/json"
)

// Model produces the next assistant message of a conversation.
type Model interface {
	Generate(ctx context.Context, req Request) (Message, error)
}

// Request is everything a Model needs to produce the next message.
type Request struct {
	System   string
	Messages []Message
	Tools    []ToolSpec
}

// Role says who produced a Message.
type Role string

// The roles in a conversation.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one provider-neutral entry in a conversation.
type Message struct {
	Role        Role
	Text        string
	Signature   []byte       // opaque provider data for Text, returned to the model unchanged
	ToolCalls   []ToolCall   // set only on assistant messages
	ToolResults []ToolResult // set only on tool messages
}

// ToolCall is the model's request to run a tool.
type ToolCall struct {
	ID        string
	Name      string
	Args      json.RawMessage
	Signature []byte // opaque provider data, returned to the model unchanged
}

// ToolResult is the JSON outcome of one ToolCall.
type ToolResult struct {
	CallID  string
	Name    string
	Content json.RawMessage
}
