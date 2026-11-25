---
name: add-tool
description: Add a new agent tool to BoxAgent (a new file in internal/tools implementing agent.Tool, registered in cli/run.go, with unit tests against a fake). Use when asked to add or create a tool the model can call.
---

# Add a tool

1. Create `internal/tools/<name>.go` with a type that implements `agent.Tool` (`Name`, `Description`, `Schema`, `Call`).
2. Define the smallest interface the tool needs (1–3 methods) **in that file**. Don't import `sandbox/docker`, `vfs` or any other concrete package.
3. In `Call`, decode `args` into a struct and validate it. Return argument and dependency errors so the loop sends them to the model as `{"error": ...}` results.
4. Add `internal/tools/<name>_test.go`, table-driven, with a hand-written fake. Cover missing fields, wrong types, out-of-range values, the success path and dependency-error mapping.
5. Register it with one line in `internal/cli/run.go`. Don't touch the agent loop.
6. If the model needs to know when to use it, update the system prompt in `internal/agent/prompt.go` and its golden file (`go test ./internal/agent -update`).
7. Add a row to the tools table in `docs/ARCHITECTURE.md`.
8. Run `make test` and `make lint`.
