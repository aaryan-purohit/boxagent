---
paths:
  - "internal/agent/**"
  - "internal/tools/**"
  - "internal/llm/**"
---

# Agent loop, tools and model adapter

## Loop (`internal/agent`)

- Plain hand-written loop, no framework.
- Unknown tools, bad arguments and tool failures go back to the model as results (`{"error": "..."}`) and never end the run. Only context cancellation and model/transport failures end the run with an error.
- Truncate tool output before it goes back to the model: stdout and stderr capped at 8 KB each, keeping head and tail with a `[truncated N bytes]` marker. This is a pure function in `truncate.go`.
- Conversation history lives in memory for the session only.

```go
type Tool interface {
    Name() string
    Description() string
    Schema() map[string]any // JSON Schema for the arguments
    Call(ctx context.Context, args json.RawMessage) (any, error)
}
```

## Tools (`internal/tools`)

- Each tool declares the narrow interface it needs (e.g. `exec` needs only `Exec(ctx, cmd, timeout)`). Tools don't import `sandbox/docker` or the full VFS store.
- The args and returns table is in `docs/ARCHITECTURE.md`. Use the `add-tool` skill for the steps to add a tool.
- Binary file content goes back to the model as a note (`binary file, N bytes`), never as raw bytes.

## System prompt (`prompt.go`)

It must tell the model:
- `/workspace` is scratch and is destroyed after the session. Only the VFS persists.
- Write or commit anything worth keeping (final source, tests, a short README) to the VFS before finishing.
- Work in small, verifiable steps: write a file, run it, read the error, fix it.

## Gemini adapter (`internal/llm/gemini`)

- Implements `agent.Model` on top of `genai`. It is the only package that imports the SDK.
- Retries 429/5xx, network failures and empty or malformed responses with exponential backoff and jitter, at most 5 attempts. Waits as long as the server asks, up to 90 s; a longer request fails at once. Backoff calculation is a pure, unit-tested function. The loop never sees transport details.
- Never hard-code the model name. It comes from `Config`.
