package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaryan-purohit/boxagent/internal/agent"
)

func TestPrintEvent(t *testing.T) {
	t.Parallel()
	call := func(args string) agent.Event {
		return agent.CallEvent{Call: agent.ToolCall{Name: "exec", Args: json.RawMessage(args)}}
	}
	result := func(content string) agent.Event {
		return agent.ResultEvent{Result: agent.ToolResult{Content: json.RawMessage(content)}}
	}
	tests := []struct {
		name  string
		event agent.Event
		want  string
	}{
		{"text", agent.TextEvent{Text: "Hi\n\tthere\n\n"}, "Hi\n\tthere\n"},
		{"text without terminal escapes", agent.TextEvent{Text: "a\x1b[2J\r\u009b\u202eb"}, "a[2Jb\n"},
		{"call without HTML escapes", call(`{"cmd":"a 2>&1"}`), "> exec {\"cmd\":\"a 2>&1\"}\n"},
		{"call with malformed args", call(`{"cmd`), "> exec {\"cmd\n"},
		{"long call is shortened", call(`"` + strings.Repeat("é", 300) + `"`), "> exec \"" + strings.Repeat("é", 199) + "...\n"},
		{"result keeps large numbers", result(`{"n":9007199254740993}`), "< {\"n\":9007199254740993}\n"},
		{"long result is shortened", result(`"` + strings.Repeat("a", 600) + `"`), "< \"" + strings.Repeat("a", 499) + "...\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer

			printEvent(&out, tc.event)

			if got := out.String(); got != tc.want {
				t.Errorf("printEvent() = %q, want %q", got, tc.want)
			}
		})
	}
}
