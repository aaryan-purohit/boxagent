# BoxAgent

A sandboxed coding agent in Go. A Gemini model works on a user's task inside a disposable Docker container (scratch space, reached only through the `exec` tool). Anything worth keeping goes into a host-side, versioned VFS (SQLite + content-addressed blobs), reached only through the `vfs_*` tools. The agent never touches the host filesystem.

Full design, layout and tool table: `docs/ARCHITECTURE.md`. Read it before making a design change. Area-specific rules load automatically from `.claude/rules/` when you touch matching files.

## Commands

- Build: `make build`
- Unit tests: `make test` (`go test -race ./...`). Must pass with no Docker daemon and no network.
- One package or test: `go test -race ./internal/vfs -run TestParsePath`
- Integration tests: `make test-int` (needs Docker; Gemini round trip is skipped when `GEMINI_API_KEY` is unset)
- Lint: `make lint` (golangci-lint; `depguard` enforces the import rules below)
- Coverage: `make cover`

A change isn't done until it has tests and `make test` and `make lint` pass.

## How to write code here: the lazy staff engineer

Do the least work that is correct, readable and easy to change. Never skip tests, error handling or thinking.

- Use the standard library or existing code before writing new code. Delete before you add.
- Solve today's problem. No speculative hooks, options or abstractions.
- Make the change easy (a separate refactor), then make the easy change.
- Small cleanups in code you're touching are fine. No drive-by refactors.

## Architecture rules

- `internal/agent` imports the stdlib only. It defines `Model`, `Tool` and its own neutral message types.
- Define interfaces in the package that consumes them. Concrete packages return structs.
- `internal/cli` is the only composition root. All wiring is in `cli/run.go`.
- Only `internal/config` reads env vars, via `config.Load(getenv)`. Everything else receives `Config` values.
- Only `internal/llm/gemini` imports `genai`. Docker is used through the Go client, never the `docker` CLI.
- Adding a tool means one file in `internal/tools` plus one registration line. Never add a `switch` in the loop.
- If a change needs an import direction not listed in `docs/ARCHITECTURE.md`, stop and rethink the design.

## Scope and dependencies

- MVP only. Ask before adding stretch features (MCP server, web UI, microVM runtimes, rollback, multi-agent).
- Stdlib first. Every new module in `go.mod` needs a one-line reason in the commit message.
- Banned: agent frameworks (LangChain-style, ADK), Viper, dotenv libraries, mock generators, assertion libraries.

## Gotchas

- Never log the API key or full file contents. Log paths, sizes and hashes.
