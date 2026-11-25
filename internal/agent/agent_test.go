package agent

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var update = flag.Bool("update", false, "rewrite golden files")

func newAgent(t *testing.T, opts Options, tools ...Tool) *Agent {
	t.Helper()
	reg, err := NewRegistry(tools...)
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}
	opts.Tools = reg
	if opts.MaxTurns == 0 {
		opts.MaxTurns = 10
	}
	a, err := New(opts)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return a
}

func TestRunMultiTurn(t *testing.T) {
	t.Parallel()
	echo := &fakeTool{name: "echo", fn: func(_ context.Context, args json.RawMessage) (any, error) {
		return map[string]any{"got": args}, nil
	}}
	first := Message{
		Text:      "Working.",
		Signature: []byte("text-sig"),
		ToolCalls: []ToolCall{
			{ID: "1", Name: "echo", Args: json.RawMessage(`{"n":1}`), Signature: []byte("call-sig")},
			call("2", "echo", `{"n":2}`),
		},
	}
	model := &fakeModel{script: []Message{first, text("Done.")}}
	var events []Event
	a := newAgent(t, Options{Model: model, OnEvent: func(e Event) { events = append(events, e) }}, echo)

	got, err := a.Run(t.Context(), "do it")

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if diff := cmp.Diff(Result{Text: "Done.", Turns: 2}, got); diff != "" {
		t.Errorf("Run() mismatch (-want +got):\n%s", diff)
	}
	res1 := ToolResult{CallID: "1", Name: "echo", Content: json.RawMessage(`{"got":{"n":1}}`)}
	res2 := ToolResult{CallID: "2", Name: "echo", Content: json.RawMessage(`{"got":{"n":2}}`)}
	assistant := first
	assistant.Role = RoleAssistant
	wantHistory := []Message{
		{Role: RoleUser, Text: "do it"},
		assistant, // signatures included
		{Role: RoleTool, ToolResults: []ToolResult{res1, res2}},
	}
	if diff := cmp.Diff(wantHistory, model.requests[1].Messages); diff != "" {
		t.Errorf("second request mismatch (-want +got):\n%s", diff)
	}
	wantEvents := []Event{
		TextEvent{"Working."},
		CallEvent{first.ToolCalls[0]}, ResultEvent{res1},
		CallEvent{first.ToolCalls[1]}, ResultEvent{res2},
		TextEvent{"Done."},
	}
	if diff := cmp.Diff(wantEvents, events); diff != "" {
		t.Errorf("events mismatch (-want +got):\n%s", diff)
	}
}

func TestRunDeclaresToolsInOrder(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []Message{text("ok")}}
	a := newAgent(t, Options{Model: model}, &fakeTool{name: "b"}, &fakeTool{name: "a"})

	if _, err := a.Run(t.Context(), "task"); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	schema := map[string]any{"type": "object"}
	want := []ToolSpec{
		{Name: "b", Description: "b description", Schema: schema},
		{Name: "a", Description: "a description", Schema: schema},
	}
	if diff := cmp.Diff(want, model.requests[0].Tools); diff != "" {
		t.Errorf("tools mismatch (-want +got):\n%s", diff)
	}
}

// TestRunSendsToolOutcomesToModel covers outcomes that must not end the run.
func TestRunSendsToolOutcomesToModel(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("a", 8<<10) + strings.Repeat("b", 100)
	cut := strings.Repeat("a", 4<<10) + "\n[truncated 100 bytes]\n" + strings.Repeat("a", 4<<10-100) + strings.Repeat("b", 100)
	failing := func(msg string) func(context.Context, json.RawMessage) (any, error) {
		return func(context.Context, json.RawMessage) (any, error) { return nil, errors.New(msg) }
	}
	decoding := func(_ context.Context, args json.RawMessage) (any, error) {
		var in struct{ N int }
		return in, json.Unmarshal(args, &in)
	}
	tests := []struct {
		name string
		call ToolCall
		fn   func(context.Context, json.RawMessage) (any, error)
		want any
	}{
		{"unknown tool", call("1", "nope", `{}`), nil, map[string]any{"error": `unknown tool "nope"`}},
		{"tool error", call("1", "tool", `{}`), failing("boom"), map[string]any{"error": "boom"}},
		{"tool error is truncated", call("1", "tool", `{}`), failing(big), map[string]any{"error": cut}},
		{
			"tool's own deadline", call("1", "tool", `{}`),
			func(context.Context, json.RawMessage) (any, error) { return nil, context.DeadlineExceeded },
			map[string]any{"error": "context deadline exceeded"},
		},
		{
			"malformed args", call("1", "tool", `{`), decoding,
			map[string]any{"error": "unexpected end of JSON input"},
		},
		{
			"unencodable result", call("1", "tool", `{}`), returning(func() {}),
			map[string]any{"error": "encode tool result: json: unsupported type: func()"},
		},
		{
			"output fields truncated separately", call("1", "tool", `{}`),
			returning(map[string]any{"stdout": big, "stderr": big, "exit_code": 0}),
			map[string]any{"stdout": cut, "stderr": cut, "exit_code": 0.0},
		},
		{"list result", call("1", "tool", `{}`), returning([]string{big, "x"}), []any{cut, "x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			model := &fakeModel{script: []Message{calls(tc.call), text("recovered")}}
			a := newAgent(t, Options{Model: model}, &fakeTool{name: "tool", fn: tc.fn})

			res, err := a.Run(t.Context(), "task")

			if err != nil || res.Text != "recovered" {
				t.Fatalf("Run() = %+v, %v, want a recovered run", res, err)
			}
			sent := model.requests[1].Messages[2].ToolResults[0]
			var got any
			if err := json.Unmarshal(sent.Content, &got); err != nil {
				t.Fatalf("result is not JSON: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("result mismatch (-want +got):\n%s", diff)
			}
			if sent.CallID != tc.call.ID || sent.Name != tc.call.Name {
				t.Errorf("result is for %q %q, want %q %q", sent.CallID, sent.Name, tc.call.ID, tc.call.Name)
			}
		})
	}
}

func TestRunStopsAtMaxTurns(t *testing.T) {
	t.Parallel()
	tool := &fakeTool{name: "tool", fn: returning("ok")}
	again := calls(call("1", "tool", `{}`))
	model := &fakeModel{script: []Message{again, again, again}}
	a := newAgent(t, Options{Model: model, MaxTurns: 2}, tool)

	got, err := a.Run(t.Context(), "task")

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if diff := cmp.Diff(Result{Turns: 2, HitLimit: true}, got); diff != "" {
		t.Errorf("Run() mismatch (-want +got):\n%s", diff)
	}
	if len(model.requests) != 2 || len(tool.args) != 2 {
		t.Errorf("got %d model and %d tool calls, want 2 of each", len(model.requests), len(tool.args))
	}
}

func TestRunFinishesOnLastTurn(t *testing.T) {
	t.Parallel()
	a := newAgent(t, Options{Model: &fakeModel{script: []Message{text("ok")}}, MaxTurns: 1})

	got, err := a.Run(t.Context(), "task")

	if err != nil || got.HitLimit || got.Text != "ok" {
		t.Errorf("Run() = %+v, %v, want a final answer", got, err)
	}
}

func TestRunModelFailure(t *testing.T) {
	t.Parallel()
	a := newAgent(t, Options{Model: &fakeModel{}})

	if _, err := a.Run(t.Context(), "task"); !errors.Is(err, errScriptEnded) {
		t.Errorf("Run() error = %v, want the model's error", err)
	}
}

func TestRunEmptyResponse(t *testing.T) {
	t.Parallel()
	for _, msg := range []Message{{}, text(" \n")} {
		a := newAgent(t, Options{Model: &fakeModel{script: []Message{msg}}})

		if _, err := a.Run(t.Context(), "task"); !errors.Is(err, ErrEmptyResponse) {
			t.Errorf("Run() with response %+v error = %v, want ErrEmptyResponse", msg, err)
		}
	}
}

func TestRunCancelledBeforeStart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	model := &fakeModel{script: []Message{text("ok")}}

	_, err := newAgent(t, Options{Model: model}).Run(ctx, "task")

	if !errors.Is(err, context.Canceled) || len(model.requests) != 0 {
		t.Errorf("Run() error = %v after %d model calls, want context.Canceled after none", err, len(model.requests))
	}
}

func TestRunCancelledDuringTool(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	stopper := &fakeTool{name: "stopper", fn: func(ctx context.Context, _ json.RawMessage) (any, error) {
		cancel()
		return nil, ctx.Err()
	}}
	later := &fakeTool{name: "later", fn: returning("ok")}
	model := &fakeModel{script: []Message{calls(call("1", "stopper", `{}`), call("2", "later", `{}`))}}

	_, err := newAgent(t, Options{Model: model}, stopper, later).Run(ctx, "task")

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() error = %v, want context.Canceled", err)
	}
	if len(later.args) != 0 || len(model.requests) != 1 {
		t.Errorf("run continued after cancellation: %d later calls, %d model calls", len(later.args), len(model.requests))
	}
}

func TestNewRejectsMaxTurnsBelowOne(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, -1} {
		if _, err := New(Options{MaxTurns: n}); !errors.Is(err, ErrInvalidMaxTurns) {
			t.Errorf("New(MaxTurns: %d) error = %v, want ErrInvalidMaxTurns", n, err)
		}
	}
}

func TestNewRegistryRejectsDuplicates(t *testing.T) {
	t.Parallel()
	if _, err := NewRegistry(&fakeTool{name: "a"}, &fakeTool{name: "a"}); !errors.Is(err, ErrDuplicateTool) {
		t.Errorf("NewRegistry() error = %v, want ErrDuplicateTool", err)
	}
}

func TestSystemPromptGolden(t *testing.T) {
	t.Parallel()
	const golden = "testdata/system_prompt.golden"
	model := &fakeModel{script: []Message{text("ok")}}
	if _, err := newAgent(t, Options{Model: model}).Run(t.Context(), "task"); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	got := model.requests[0].System

	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("system prompt mismatch, rerun with -update if intended (-want +got):\n%s", diff)
	}
}
