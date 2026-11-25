package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genai"

	"github.com/aaryan-purohit/boxagent/internal/agent"
)

const okBody = `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`

type reply struct {
	status int
	body   string
}

// fakeTransport replays replies, repeating the last, and records requests.
type fakeTransport struct {
	replies []reply
	bodies  []string
	keys    []string
	urls    []string
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	f.bodies = append(f.bodies, string(body))
	f.keys = append(f.keys, r.Header.Get("x-goog-api-key"))
	f.urls = append(f.urls, r.URL.String())
	rep := f.replies[min(len(f.bodies), len(f.replies))-1]
	return &http.Response{
		StatusCode: rep.status,
		Status:     fmt.Sprint(rep.status),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(rep.body)),
		Request:    r,
	}, nil
}

func newTestModel(t *testing.T, f *fakeTransport) (*Model, *[]time.Duration) {
	t.Helper()
	m, err := newModel(t.Context(), Options{APIKey: "sekrit", Model: "test-model", Log: slog.New(slog.DiscardHandler)}, &http.Client{Transport: f})
	if err != nil {
		t.Fatalf("newModel() error: %v", err)
	}
	var slept []time.Duration
	m.sleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return ctx.Err()
	}
	return m, &slept
}

var userRequest = agent.Request{System: "sys", Messages: []agent.Message{{Role: agent.RoleUser, Text: "task"}}}

func TestGenerateSendsRequest(t *testing.T) {
	t.Parallel()
	f := &fakeTransport{replies: []reply{{200, okBody}}}
	m, _ := newTestModel(t, f)
	req := agent.Request{
		System: "sys",
		Messages: []agent.Message{
			{Role: agent.RoleUser, Text: "task"},
			{
				Role: agent.RoleAssistant, Text: "ok", Signature: []byte("ts"),
				ToolCalls: []agent.ToolCall{{ID: "1", Name: "exec", Args: json.RawMessage(`{"cmd":"ls"}`), Signature: []byte("cs")}},
			},
			{Role: agent.RoleTool, ToolResults: []agent.ToolResult{
				{CallID: "1", Name: "exec", Content: json.RawMessage(`{"exit_code":0}`)},
				{CallID: "2", Name: "list", Content: json.RawMessage(`[1]`)},
			}},
		},
		Tools: []agent.ToolSpec{{Name: "exec", Description: "run", Schema: map[string]any{"type": "object"}}},
	}

	got, err := m.Generate(t.Context(), req)

	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if diff := cmp.Diff(agent.Message{Role: agent.RoleAssistant, Text: "hi"}, got); diff != "" {
		t.Errorf("Generate() mismatch (-want +got):\n%s", diff)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(f.bodies[0]), &sent); err != nil {
		t.Fatalf("request is not JSON: %v", err)
	}
	delete(sent, "generationConfig") // an empty object the SDK adds
	var want map[string]any
	if err := json.Unmarshal([]byte(`{
		"systemInstruction": {"role": "user", "parts": [{"text": "sys"}]},
		"contents": [
			{"role": "user", "parts": [{"text": "task"}]},
			{"role": "model", "parts": [
				{"text": "ok", "thoughtSignature": "dHM="},
				{"functionCall": {"id": "1", "name": "exec", "args": {"cmd": "ls"}}, "thoughtSignature": "Y3M="}
			]},
			{"role": "user", "parts": [
				{"functionResponse": {"id": "1", "name": "exec", "response": {"exit_code": 0}}},
				{"functionResponse": {"id": "2", "name": "list", "response": {"output": [1]}}}
			]}
		],
		"tools": [{"functionDeclarations": [
			{"name": "exec", "description": "run", "parametersJsonSchema": {"type": "object"}}
		]}]
	}`), &want); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, sent); diff != "" {
		t.Errorf("request mismatch (-want +got):\n%s", diff)
	}
	if f.keys[0] != "sekrit" || !strings.Contains(f.urls[0], "test-model") || strings.Contains(f.urls[0], "sekrit") {
		t.Errorf("sent key %q to %q, want the key in the header only", f.keys[0], f.urls[0])
	}
}

func TestGenerateRetries(t *testing.T) {
	t.Parallel()
	failure := func(code int) reply {
		return reply{code, fmt.Sprintf(`{"error":{"code":%d,"message":"no","status":"BAD"}}`, code)}
	}
	ok := reply{200, okBody}
	tests := []struct {
		name         string
		replies      []reply
		wantAttempts int
		wantCode     int // 0 means success
	}{
		{"succeeds first time", []reply{ok}, 1, 0},
		{"retries a rate limit", []reply{failure(429), ok}, 2, 0},
		{"retries server errors", []reply{failure(500), failure(503), ok}, 3, 0},
		{"retries a non-JSON server error", []reply{{502, "bad gateway"}, ok}, 2, 0},
		{"succeeds on the last attempt", []reply{failure(500), failure(500), failure(500), failure(500), ok}, 5, 0},
		{"gives up after five attempts", []reply{failure(429)}, 5, 429},
		{"retries a malformed call", []reply{{200, `{"candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}]}`}, ok}, 2, 0},
		{"retries an empty response", []reply{{200, `{}`}, ok}, 2, 0},
		{"does not retry a bad request", []reply{failure(400)}, 1, 400},
		{"does not retry a bad key", []reply{failure(403)}, 1, 403},
		{"does not retry an unknown model", []reply{failure(404)}, 1, 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeTransport{replies: tc.replies}
			m, slept := newTestModel(t, f)

			_, err := m.Generate(t.Context(), userRequest)

			var apiErr genai.APIError
			errors.As(err, &apiErr)
			if apiErr.Code != tc.wantCode || (err == nil) != (tc.wantCode == 0) {
				t.Errorf("Generate() error = %v, want code %d", err, tc.wantCode)
			}
			if len(f.bodies) != tc.wantAttempts || len(*slept) != tc.wantAttempts-1 {
				t.Errorf("got %d attempts and %d sleeps, want %d attempts", len(f.bodies), len(*slept), tc.wantAttempts)
			}
			if err != nil && strings.Contains(err.Error(), "sekrit") {
				t.Errorf("error leaks the API key: %v", err)
			}
		})
	}
}

func TestGenerateStopsWhenCancelledDuringBackoff(t *testing.T) {
	t.Parallel()
	f := &fakeTransport{replies: []reply{{503, ""}}}
	m, _ := newTestModel(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	m.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleep(ctx, d)
	}

	_, err := m.Generate(ctx, userRequest)

	if !errors.Is(err, context.Canceled) || len(f.bodies) != 1 {
		t.Errorf("Generate() error = %v after %d attempts, want context.Canceled after 1", err, len(f.bodies))
	}
}

func TestGenerateRejectsUnusableResponses(t *testing.T) {
	t.Parallel()
	finished := func(reason string) string {
		return fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":%q}]}`, reason)
	}
	tests := []struct {
		name, body string
		want       error
	}{
		{"blocked prompt", `{"promptFeedback":{"blockReason":"SAFETY"}}`, ErrBlocked},
		{"no candidates", `{}`, ErrNoContent},
		{"blank text", `{"candidates":[{"content":{"parts":[{"text":" "}]}}]}`, ErrNoContent},
		{"no content", `{"candidates":[{"finishReason":"STOP"}]}`, ErrNoContent},
		{"token limit", finished("MAX_TOKENS"), ErrStopped},
		{"safety stop", finished("SAFETY"), ErrStopped},
		{"malformed call", finished("MALFORMED_FUNCTION_CALL"), ErrMalformed},
		{"missing signature", finished("MISSING_THOUGHT_SIGNATURE"), ErrStopped},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, _ := newTestModel(t, &fakeTransport{replies: []reply{{200, tc.body}}})

			if _, err := m.Generate(t.Context(), userRequest); !errors.Is(err, tc.want) {
				t.Errorf("Generate() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestToMessage(t *testing.T) {
	t.Parallel()
	fc := func(id, name string, args map[string]any) *genai.FunctionCall {
		return &genai.FunctionCall{ID: id, Name: name, Args: args}
	}
	tests := []struct {
		name    string
		content *genai.Content
		want    agent.Message
	}{
		{
			"joins text, skips thoughts and keeps the signature",
			&genai.Content{Parts: []*genai.Part{
				{Text: "hmm", Thought: true}, {Text: "a"}, {Text: "b"}, {ThoughtSignature: []byte("s")},
			}},
			agent.Message{Role: agent.RoleAssistant, Text: "ab", Signature: []byte("s")},
		},
		{
			"keeps call IDs and signatures, and fills in missing IDs",
			&genai.Content{Parts: []*genai.Part{
				{FunctionCall: fc("x", "a", map[string]any{"n": 1.0}), ThoughtSignature: []byte("s")},
				{FunctionCall: fc("", "b", nil)},
			}},
			agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{
				{ID: "x", Name: "a", Args: json.RawMessage(`{"n":1}`), Signature: []byte("s")},
				{ID: "call-3-1", Name: "b", Args: json.RawMessage(`{}`)},
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{Content: tc.content}}}

			got, err := toMessage(resp, "call-3")

			if err != nil {
				t.Fatalf("toMessage() error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("toMessage() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBackoff(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		attempt int
		jitter  float64
		want    time.Duration
	}{
		{"first, least jitter", 1, 0, time.Second},
		{"first, half jitter", 1, 0.5, 1500 * time.Millisecond},
		{"doubles", 2, 0, 2 * time.Second},
		{"doubles again", 3, 0, 4 * time.Second},
		{"fourth", 4, 0, 8 * time.Second},
		{"capped", 5, 0, 15 * time.Second},
		{"capped, most jitter", 5, 1, 30 * time.Second},
		{"does not overflow", 1000, 1, 30 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := backoff(tc.attempt, tc.jitter); got != tc.want {
				t.Errorf("backoff(%d, %v) = %v, want %v", tc.attempt, tc.jitter, got, tc.want)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	t.Parallel()
	limited := func(delay string) error {
		return fmt.Errorf("wrapped: %w", genai.APIError{Code: 429, Details: []map[string]any{
			{"@type": "type.googleapis.com/google.rpc.Help", "retryDelay": "1s"},
			{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": delay},
		}})
	}
	network := &url.Error{Op: "Post", URL: "x", Err: &net.OpError{Op: "read", Err: errors.New("connection reset")}}
	tests := []struct {
		name    string
		err     error
		attempt int
		want    time.Duration
		wantOK  bool
	}{
		{"rate limit without a delay", genai.APIError{Code: 429}, 1, time.Second, true},
		{"rate limit with a longer delay", limited("37.5s"), 1, 37500 * time.Millisecond, true},
		{"rate limit with a shorter delay", limited("0.5s"), 2, 2 * time.Second, true},
		{"rate limit with a bad delay", limited("soon"), 1, time.Second, true},
		{"longest accepted delay", limited("90s"), 1, 90 * time.Second, true},
		{"quota that won't recover", limited("91s"), 1, 0, false},
		{"network failure", network, 1, time.Second, true},
		{"truncated body", io.ErrUnexpectedEOF, 1, time.Second, true},
		{"malformed response", ErrMalformed, 1, time.Second, true},
		{"empty response", ErrNoContent, 1, time.Second, true},
		{"last attempt", genai.APIError{Code: 429}, 5, 0, false},
		{"bad request", genai.APIError{Code: 400}, 1, 0, false},
		{"blocked prompt", ErrBlocked, 1, 0, false},
		{"token limit", ErrStopped, 1, 0, false},
		{"unknown error", errors.New("x"), 1, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := retryDelay(tc.err, tc.attempt, 0); got != tc.want || ok != tc.wantOK {
				t.Errorf("retryDelay() = %v, %v, want %v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestGenerateLogsRetriesWithoutKey(t *testing.T) {
	t.Parallel()
	m, _ := newTestModel(t, &fakeTransport{replies: []reply{{503, "busy"}, {200, okBody}}})
	var logs bytes.Buffer
	m.log = slog.New(slog.NewTextHandler(&logs, nil))

	if _, err := m.Generate(t.Context(), userRequest); err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if out := logs.String(); !strings.Contains(out, "retrying") || strings.Contains(out, "sekrit") {
		t.Errorf("log = %q, want a retry notice without the key", out)
	}
}

func TestGenerateIgnoresBaseURLFromEnvironment(t *testing.T) {
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "https://evil.invalid/")
	f := &fakeTransport{replies: []reply{{200, okBody}}}
	m, _ := newTestModel(t, f)

	if _, err := m.Generate(t.Context(), userRequest); err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if !strings.HasPrefix(f.urls[0], baseURL) {
		t.Errorf("sent to %q, want %q", f.urls[0], baseURL)
	}
}

func TestNewRejectsEmptyKey(t *testing.T) {
	t.Parallel()
	if _, err := New(t.Context(), Options{Model: "m"}); !errors.Is(err, errNoAPIKey) {
		t.Errorf("New() error = %v, want errNoAPIKey", err)
	}
}
