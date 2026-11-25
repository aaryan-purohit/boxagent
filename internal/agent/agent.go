// Package agent runs the loop between a model and its tools.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrEmptyResponse means the model returned neither text nor tool calls.
	ErrEmptyResponse = errors.New("model returned an empty response")
	// ErrInvalidMaxTurns means the turn limit is below 1.
	ErrInvalidMaxTurns = errors.New("max turns must be at least 1")
)

// Event is a step of a run: a TextEvent, CallEvent or ResultEvent.
type Event interface{ isEvent() }

// TextEvent reports text from the model.
type TextEvent struct{ Text string }

// CallEvent reports a tool call that is about to run.
type CallEvent struct{ Call ToolCall }

// ResultEvent reports the result of a tool call, as the model will see it.
type ResultEvent struct{ Result ToolResult }

func (TextEvent) isEvent()   {}
func (CallEvent) isEvent()   {}
func (ResultEvent) isEvent() {}

// Options configures an Agent.
type Options struct {
	Model    Model
	Tools    *Registry
	MaxTurns int
	OnEvent  func(Event) // optional; called synchronously, in order
}

// Result is the outcome of a run.
type Result struct {
	Text     string // the final answer; empty if HitLimit
	Turns    int    // model responses received
	HitLimit bool   // the run stopped at MaxTurns without a final answer
}

// Agent works on tasks by looping between a model and its tools.
type Agent struct{ opts Options }

// New returns an Agent.
func New(opts Options) (*Agent, error) {
	if opts.MaxTurns < 1 {
		return nil, fmt.Errorf("%w, got %d", ErrInvalidMaxTurns, opts.MaxTurns)
	}
	if opts.OnEvent == nil {
		opts.OnEvent = func(Event) {}
	}
	return &Agent{opts: opts}, nil
}

// Run works on task until the model gives a final answer or the turn limit is
// reached. Tool failures go back to the model; only cancellation and model
// failures return an error. Tool calls in the last allowed turn still run, so
// work the model saves there is kept.
func (a *Agent) Run(ctx context.Context, task string) (Result, error) {
	history := []Message{{Role: RoleUser, Text: task}}
	for turn := 1; turn <= a.opts.MaxTurns; turn++ {
		msg, err := a.next(ctx, history)
		if err != nil {
			return Result{Turns: turn - 1}, fmt.Errorf("agent turn %d: %w", turn, err)
		}
		if len(msg.ToolCalls) == 0 {
			return Result{Text: msg.Text, Turns: turn}, nil
		}
		results, err := a.runTools(ctx, msg.ToolCalls)
		if err != nil {
			return Result{Turns: turn}, fmt.Errorf("agent turn %d: %w", turn, err)
		}
		history = append(history, msg, Message{Role: RoleTool, ToolResults: results})
	}
	return Result{Turns: a.opts.MaxTurns, HitLimit: true}, nil
}

// next asks the model for its next message.
func (a *Agent) next(ctx context.Context, history []Message) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	msg, err := a.opts.Model.Generate(ctx, Request{
		System:   systemPrompt,
		Messages: history,
		Tools:    a.opts.Tools.specs,
	})
	if err != nil {
		return Message{}, err
	}
	hasText := strings.TrimSpace(msg.Text) != ""
	if !hasText && len(msg.ToolCalls) == 0 {
		return Message{}, ErrEmptyResponse
	}
	if hasText {
		a.opts.OnEvent(TextEvent{msg.Text})
	}
	msg.Role = RoleAssistant
	return msg, nil
}

// runTools runs calls one at a time, in order, stopping if ctx is cancelled.
func (a *Agent) runTools(ctx context.Context, calls []ToolCall) ([]ToolResult, error) {
	results := make([]ToolResult, 0, len(calls))
	for _, c := range calls {
		a.opts.OnEvent(CallEvent{c})
		res := a.opts.Tools.call(ctx, c)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a.opts.OnEvent(ResultEvent{res})
		results = append(results, res)
	}
	return results, nil
}
