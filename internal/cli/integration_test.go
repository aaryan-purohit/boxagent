//go:build integration

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/aaryan-purohit/boxagent/internal/agent"
	"github.com/aaryan-purohit/boxagent/internal/config"
	"github.com/aaryan-purohit/boxagent/internal/llm/gemini"
	"github.com/aaryan-purohit/boxagent/internal/sandbox/docker"
)

// lookup is a tool whose answers the model cannot guess. Each answer names the
// next key, which forces calls over several turns.
type lookup struct{ calls int }

func (*lookup) Name() string        { return "lookup" }
func (*lookup) Description() string { return "Look up the value stored under a key." }

func (*lookup) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"key": map[string]any{"type": "string"}},
		"required":   []string{"key"},
	}
}

func (l *lookup) Call(_ context.Context, args json.RawMessage) (any, error) {
	l.calls++
	var in struct{ Key string }
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	switch in.Key {
	case "start":
		return map[string]string{"next_key": "zq7"}, nil
	case "zq7":
		return map[string]string{"password": "plum-4812"}, nil
	}
	return nil, errors.New("no such key")
}

// TestIntegrationGeminiRoundTrip lives here because only cli may combine the
// adapter with config, which owns the key and the default model name.
func TestIntegrationGeminiRoundTrip(t *testing.T) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	if cfg.RequireAPIKey() != nil {
		t.Skip("GEMINI_API_KEY is not set")
	}
	model, err := gemini.New(t.Context(), gemini.Options{APIKey: cfg.APIKey, Model: cfg.Model, Log: slog.New(slog.NewTextHandler(t.Output(), nil))})
	if err != nil {
		t.Fatalf("gemini.New() error: %v", err)
	}
	tool := &lookup{}
	reg, err := agent.NewRegistry(tool)
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}
	a, err := agent.New(agent.Options{Model: model, Tools: reg, MaxTurns: 8})
	if err != nil {
		t.Fatalf("agent.New() error: %v", err)
	}

	res, err := a.Run(t.Context(), `Use the lookup tool with key "start". Each result gives the next key; keep going until you get a password, then reply with it.`)

	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if tool.calls < 2 || !strings.Contains(res.Text, "plum-4812") {
		t.Errorf("Run() = %+v after %d tool calls, want the password after at least 2", res, tool.calls)
	}
	t.Logf("model %s answered in %d turns", cfg.Model, res.Turns)
}

// scriptedModel replays messages, then finishes.
type scriptedModel struct {
	script  []agent.Message
	results []agent.ToolResult
	block   bool // after the script, wait for cancellation
}

func (m *scriptedModel) Generate(ctx context.Context, req agent.Request) (agent.Message, error) {
	if last := req.Messages[len(req.Messages)-1]; last.Role == agent.RoleTool {
		m.results = append(m.results, last.ToolResults...)
	}
	if len(m.script) > 0 {
		msg := m.script[0]
		m.script = m.script[1:]
		return msg, nil
	}
	if m.block {
		<-ctx.Done()
		return agent.Message{}, ctx.Err()
	}
	return agent.Message{Text: "done"}, nil
}

func toolCall(name string, args any) agent.Message {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err) // impossible: args are literals
	}
	return agent.Message{ToolCalls: []agent.ToolCall{{ID: "1", Name: name, Args: raw}}}
}

func execCall(cmd string) agent.Message { return toolCall("exec", map[string]any{"cmd": cmd}) }

func runScripted(ctx context.Context, t *testing.T, env func(string) string, m *scriptedModel, args ...string) error {
	t.Helper()
	cmd := newRunCmd(env, func(context.Context, config.Config, *slog.Logger) (agent.Model, error) { return m, nil })
	cmd.SetOut(t.Output())
	cmd.SetErr(t.Output())
	cmd.SetArgs(append([]string{"task"}, args...))
	return cmd.ExecuteContext(ctx)
}

func envIn(dir string) func(string) string {
	return func(key string) string {
		switch key {
		case "BOXAGENT_DATA_DIR":
			return dir
		case "GEMINI_API_KEY":
			return "unused"
		}
		return ""
	}
}

// checkNoNewSandboxes fails the test if it ends with more sandboxes than it
// began with.
func checkNoNewSandboxes(t *testing.T) {
	t.Helper()
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	list := func() map[string]bool {
		found, err := cli.ContainerList(context.Background(), client.ContainerListOptions{
			All: true, Filters: make(client.Filters).Add("label", "boxagent.session"),
		})
		if err != nil {
			t.Fatalf("list containers: %v", err)
		}
		ids := map[string]bool{}
		for _, c := range found.Items {
			ids[c.ID] = true
		}
		return ids
	}
	before := list()
	t.Cleanup(func() {
		for id := range list() {
			if !before[id] {
				t.Errorf("container left behind: %s", id)
			}
		}
		_ = cli.Close() // best effort
	})
}

func TestIntegrationRunCommitsAndSeeds(t *testing.T) {
	checkNoNewSandboxes(t)
	env := envIn(t.TempDir())
	commit := toolCall("vfs_commit", map[string]any{"paths": []string{"src/f.txt"}})
	first := &scriptedModel{script: []agent.Message{
		execCall("mkdir src && echo one > src/f.txt"), commit,
		execCall("echo two > src/f.txt"), commit,
	}}

	if err := runScripted(t.Context(), t, env, first); err != nil {
		t.Fatalf("first run error: %v", err)
	}

	if got, err := runCLI(t, env, "vfs", "cat", "src/f.txt"); err != nil || got != "two\n" {
		t.Errorf("vfs cat = %q, %v, want two", got, err)
	}
	if got, err := runCLI(t, env, "vfs", "cat", "src/f.txt", "--version", "1"); err != nil || got != "one\n" {
		t.Errorf("vfs cat version 1 = %q, %v, want one", got, err)
	}
	second := &scriptedModel{script: []agent.Message{execCall("cat src/f.txt")}}
	if err := runScripted(t.Context(), t, env, second, "--seed", "src"); err != nil {
		t.Fatalf("seeded run error: %v", err)
	}
	if got := string(second.results[0].Content); !strings.Contains(got, `"stdout":"two\n"`) {
		t.Errorf("seeded file = %s, want two", got)
	}
}

func TestIntegrationRunReportsTurnLimit(t *testing.T) {
	checkNoNewSandboxes(t)
	m := &scriptedModel{script: []agent.Message{execCall("true"), execCall("true")}}

	err := runScripted(t.Context(), t, envIn(t.TempDir()), m, "--max-turns", "1")

	if !errors.Is(err, errTurnLimit) {
		t.Errorf("run error = %v, want errTurnLimit", err)
	}
}

func TestIntegrationRunCancelledLeavesNothing(t *testing.T) {
	tests := []struct {
		name  string
		model *scriptedModel
	}{
		{"during an exec", &scriptedModel{script: []agent.Message{execCall("sleep 60")}}},
		{"during a model call", &scriptedModel{block: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkNoNewSandboxes(t)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			if err := runScripted(ctx, t, envIn(t.TempDir()), tc.model); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("run error = %v, want context.DeadlineExceeded", err)
			}
		})
	}
}

func TestIntegrationRunStopsAtDiskLimit(t *testing.T) {
	checkNoNewSandboxes(t)
	m := &scriptedModel{script: []agent.Message{execCall("head -c 4500000000 /dev/zero > big; sleep 50")}}

	err := runScripted(t.Context(), t, envIn(t.TempDir()), m)

	if !errors.Is(err, docker.ErrDiskLimit) {
		t.Errorf("run error = %v, want ErrDiskLimit", err)
	}
}
