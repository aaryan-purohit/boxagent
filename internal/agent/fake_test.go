package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
)

var errScriptEnded = errors.New("fake model: script ended")

// fakeModel replays a script and records the requests it receives.
type fakeModel struct {
	script   []Message
	requests []Request
}

func (m *fakeModel) Generate(_ context.Context, req Request) (Message, error) {
	req.Messages = slices.Clone(req.Messages)
	m.requests = append(m.requests, req)
	if len(m.requests) > len(m.script) {
		return Message{}, errScriptEnded
	}
	return m.script[len(m.requests)-1], nil
}

// fakeTool calls fn and records the arguments it receives.
type fakeTool struct {
	name string
	fn   func(ctx context.Context, args json.RawMessage) (any, error)
	args []string
}

func (t *fakeTool) Name() string           { return t.name }
func (t *fakeTool) Description() string    { return t.name + " description" }
func (t *fakeTool) Schema() map[string]any { return map[string]any{"type": "object"} }

func (t *fakeTool) Call(ctx context.Context, args json.RawMessage) (any, error) {
	t.args = append(t.args, string(args))
	return t.fn(ctx, args)
}

func returning(v any) func(context.Context, json.RawMessage) (any, error) {
	return func(context.Context, json.RawMessage) (any, error) { return v, nil }
}

func call(id, name, args string) ToolCall {
	return ToolCall{ID: id, Name: name, Args: json.RawMessage(args)}
}

func calls(cs ...ToolCall) Message { return Message{ToolCalls: cs} }

func text(s string) Message { return Message{Text: s} }
