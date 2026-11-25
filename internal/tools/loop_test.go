package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aaryan-purohit/boxagent/internal/agent"
)

// scriptedModel calls every tool once, then finishes.
type scriptedModel struct {
	calls   []agent.ToolCall
	specs   []agent.ToolSpec
	results []agent.ToolResult
}

func (m *scriptedModel) Generate(_ context.Context, req agent.Request) (agent.Message, error) {
	m.specs = req.Tools
	last := req.Messages[len(req.Messages)-1]
	if last.Role == agent.RoleUser {
		return agent.Message{ToolCalls: m.calls}, nil
	}
	m.results = last.ToolResults
	return agent.Message{Text: "done"}, nil
}

func TestAllToolsRoundTripThroughLoop(t *testing.T) {
	t.Parallel()
	sandbox := &fakeSandbox{files: map[string]string{"a": "1"}, result: ExecResult{Stdout: "ok"}}
	files := &fakeFiles{content: "hi", versions: []FileVersion{{Path: "a", Version: 1}}}
	reg, err := agent.NewRegistry(
		NewExec(sandbox), NewVFSWrite(files), NewVFSRead(files),
		NewVFSList(files), NewVFSHistory(files), NewVFSCommit(sandbox, files),
	)
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}
	args := map[string]string{
		"exec":        `{"cmd":"ls"}`,
		"vfs_write":   `{"path":"a","content":"x"}`,
		"vfs_read":    `{"path":"a"}`,
		"vfs_list":    `{}`,
		"vfs_history": `{"path":"a"}`,
		"vfs_commit":  `{"paths":["a"]}`,
	}
	model := &scriptedModel{}
	for name, a := range args {
		model.calls = append(model.calls, agent.ToolCall{ID: name, Name: name, Args: json.RawMessage(a)})
	}
	a, err := agent.New(agent.Options{Model: model, Tools: reg, MaxTurns: 2})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := a.Run(t.Context(), "task"); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(model.specs) != 6 || len(model.results) != 6 {
		t.Fatalf("got %d tools and %d results, want 6 of each", len(model.specs), len(model.results))
	}
	for _, r := range model.results {
		var failed struct{ Error string }
		if json.Unmarshal(r.Content, &failed) == nil && failed.Error != "" {
			t.Errorf("%s failed: %s", r.Name, failed.Error)
		}
	}
	for _, s := range model.specs {
		if _, err := json.Marshal(s.Schema); err != nil || s.Description == "" || s.Schema["type"] != "object" {
			t.Errorf("%s has an unusable declaration: %+v, %v", s.Name, s, err)
		}
	}
}
