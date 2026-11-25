# Contributing

Thanks for helping. For anything beyond a small fix, please open an issue first so we can agree on the approach. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) lists what is deliberately not included.

## Set up

1. Install Go 1.26.11 or later and [golangci-lint](https://golangci-lint.run/docs/welcome/install/) v2.
2. Clone the repo and run `make build`, `make test` and `make lint`.
3. For integration tests, start Docker and run `make test-int`. Set `GEMINI_API_KEY` to include the live Gemini test, which makes billable API calls; without a key it is skipped. These tests download container images, and they **remove every BoxAgent container**, including those of live sessions.

| Command | Purpose |
|---|---|
| `make build` | Build `bin/boxagent` |
| `make test` | Unit tests with the race detector; must pass without Docker or network |
| `make test-int` | Integration tests |
| `make lint` | Lint, including the import rules |
| `make cover` | Coverage by function |

## Rules

A change is done when it has tests and `make test` and `make lint` pass. If it touches the sandbox, session, Gemini adapter or `run`, run `make test-int` too.

- **Keep it small.** Use the standard library or existing code first. Solve today's problem, without speculative options.
- **Respect the import directions** in the architecture doc. `depguard` enforces most of them. `internal/agent` uses only the standard library, and interfaces are defined where they are consumed.
- **Wire things only in `internal/cli`.** Only `internal/config` reads environment variables, apart from those the Docker library reads itself.
- **Never log the API key or full file contents.**
- **Treat everything from the model as untrusted:** paths, arguments, and text that reaches a terminal.
- **Justify new modules** with one line in the commit message. Agent frameworks, Viper, dotenv libraries, mock generators and assertion libraries are not accepted.
- **Tests** are table-driven, use hand-written fakes and `cmp.Diff`, and check errors with `errors.Is`. They don't sleep or use the network. They live in the package they test.

More detailed rules are in [.claude/rules](.claude/rules). They are written for Claude Code but apply to everyone.

## Adding a tool

1. Add `internal/tools/<name>.go` implementing `agent.Tool`, with the smallest interface it needs defined in that file.
2. Add table-driven tests with a fake: bad arguments, success and dependency errors.
3. Register it with one line in `internal/cli/run.go`. Don't change the agent loop.
4. If the model needs guidance, edit `internal/agent/prompt.go` and run `go test ./internal/agent -update`.
5. Add it to the tools table in the architecture doc.

## Using Claude Code

`CLAUDE.md` and `.claude/` configure Claude Code for this repo. The hooks format Go files after edits, and run `go vet` and `go test` before Claude finishes. If you don't use Claude Code, they have no effect.

## Pull requests

- Write commit subjects like `feat: ...`, `fix: ...`, `docs: ...`, `refactor: ...` or `chore: ...`.
- Keep a pull request to one change, and say how you tested it.
- By contributing, you agree that your work is licensed under the [MIT License](LICENSE), and to follow the [code of conduct](CODE_OF_CONDUCT.md).
