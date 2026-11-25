# BoxAgent architecture

How BoxAgent is built and why. For setup and use, see the [README](../README.md). For coding and testing rules, see [CONTRIBUTING](../CONTRIBUTING.md).

## What this project is

BoxAgent is a sandboxed coding agent written in Go. The user gives it a task. A Gemini model works on that task inside its own disposable Docker container. Anything the agent wants to keep goes into a **virtual file system (VFS)**. The VFS lives on the host, versions every file and survives after the container is gone.

| Space | Lifetime | Agent access | Purpose |
|---|---|---|---|
| **Sandbox** (container) | One session, then destroyed | Through `exec`; `vfs_commit` copies files out | Scratch space: install, build, run, break things |
| **VFS** (host, SQLite + blobs) | Persistent | Through the `vfs_*` tools only | Durable, versioned artifacts |

The agent never touches the host filesystem directly. Files move between the VFS and the sandbox only through explicit steps.

## Tech stack

- **Language:** Go 1.26.11+.
- **CLI:** `github.com/spf13/cobra`. No Viper.
- **LLM:** Google Gemini through `google.golang.org/genai` with function calling.
- **Sandbox:** Docker Engine through the official Go client (`github.com/moby/moby/client` and its `api` module), with `github.com/containerd/errdefs` for its not-found errors.
- **VFS metadata:** SQLite through `modernc.org/sqlite` (pure Go, no cgo).
- **Logging:** `log/slog`.
- **Testing:** standard `testing` plus `github.com/google/go-cmp/cmp`.

## Configuration

| Env var | Required | Default | Purpose |
|---|---|---|---|
| `GEMINI_API_KEY` | for `run` | none | Google AI Studio API key |
| `BOXAGENT_MODEL` | no | `gemini-3.5-flash-lite` | Gemini model name |
| `BOXAGENT_DATA_DIR` | no | `~/.boxagent` | Holds `boxagent.db` and `blobs/` |
| `BOXAGENT_IMAGE` | no | `golang:1.23-bookworm` | Sandbox base image |
| `BOXAGENT_MAX_TURNS` | no | `25` | Agent turn limit |
| `BOXAGENT_LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, `error` |

The Docker client also reads Docker's own variables (`DOCKER_HOST` and friends). It ignores Docker contexts.

Precedence: flag > env > default. Cobra flags may override env for per-run settings only.

## Repository layout

```
boxagent/
├── cmd/
│   └── boxagent/
│       └── main.go              # Runs the CLI, sets the exit code
├── internal/
│   ├── cli/                     # Cobra commands; the composition root
│   │   ├── root.go              # Root cmd, global flags, logger setup
│   │   ├── run.go               # `boxagent run` — wires config → deps → session
│   │   ├── files.go             # Adapters: tools' interfaces → vfs and sandbox/docker
│   │   ├── print.go             # Streams run steps to stdout
│   │   ├── vfs.go               # `boxagent vfs ls|cat|history`
│   │   ├── export.go            # `boxagent vfs export|rm`
│   │   └── sandbox.go           # `boxagent sandbox prune`
│   ├── config/                  # Env → validated Config. Nothing else.
│   ├── agent/                   # Core loop. Imports no other internal package.
│   │   ├── agent.go             # Agent type, Run(ctx, task) (Result, error)
│   │   ├── model.go             # Model interface + provider-neutral Message/ToolCall/ToolResult types
│   │   ├── tool.go              # Tool interface + Registry
│   │   ├── truncate.go          # Pure output-truncation helpers
│   │   └── prompt.go            # System prompt
│   ├── tools/                   # Each tool implements agent.Tool
│   │   ├── args.go              # Strict argument decoding and schema building
│   │   ├── exec.go
│   │   ├── vfs.go               # vfs_write, vfs_read, vfs_list, vfs_history
│   │   └── commit.go            # vfs_commit (sandbox → VFS)
│   ├── llm/
│   │   └── gemini/              # Adapter: implements agent.Model on top of genai
│   ├── sandbox/
│   │   └── docker/              # Concrete Docker sandbox: lifecycle, exec, copy in/out
│   ├── vfs/                     # Versioned, content-addressed store
│   │   ├── path.go              # Path type + ParsePath (pure, heavily tested)
│   │   ├── store.go             # Store: Write/Read/List/History
│   │   ├── blobs.go             # Blob storage on disk (atomic writes)
│   │   └── schema.sql           # go:embed
│   └── session/                 # One run: create sandbox, seed, run agent, always destroy
├── docs/                        # ARCHITECTURE.md
├── .env.example
├── .golangci.yml
├── Makefile
├── go.mod
└── README.md
```

### Why it is laid out this way

- **`agent` is the core and depends on nothing internal.** It defines the interfaces it needs (`Model`, `Tool`) and its own neutral message types. Swapping Gemini for another provider means adding `internal/llm/<provider>`; `agent` doesn't change.
- **Adapters live in sub-packages named after the technology** (`llm/gemini`, `sandbox/docker`). The parent directory holds no code until there is a real shared concept.
- **`tools` is the seam between the agent and the world.** Each tool declares the narrow interface it needs (for example, `exec` needs only `Exec(ctx, cmd, timeout)`).
- **`cli` is the only place where concrete types meet.** `cli/run.go` wires a run; the small `vfs` and `sandbox` commands wire their own dependency.
- **`session` owns lifecycle.** Creating and always destroying the sandbox is one responsibility.
- **No package is named after a stdlib package** (that's why there's no `sync`).
- **Everything is under `internal/`.**

### Allowed import directions

```
cli ──► config, session, agent, tools, llm/gemini, sandbox/docker, vfs
session ──► agent  (plus small interfaces it defines for sandbox lifecycle)
tools ──► agent    (plus small interfaces it defines for exec / file storage)
llm/gemini ──► agent
agent ──► (stdlib only)
vfs, sandbox/docker, config ──► (stdlib + their driver only)
```

`depguard` in `.golangci.yml` enforces most of these directions.

## Components

### 1. CLI

```
boxagent run "<task>" [--seed <vfs-prefix>] [--max-turns N] [--network]
boxagent vfs ls [prefix]
boxagent vfs cat <path> [--version N]
boxagent vfs history <path>
boxagent vfs export <directory> [prefix]
boxagent vfs rm <prefix> --yes
boxagent sandbox prune            # remove leftover containers labelled boxagent.session
boxagent --version
```

`run` streams each step to stdout: model text, tool calls (name and short args) and truncated tool results.

### 2. Agent loop

1. Send the system prompt, the user task and the tool declarations from the registry to the `Model`.
2. If the response has tool calls, look each one up in the `Registry`, run it, append the results, and loop.
3. If the response has text only and no tool calls, it is the final answer; stop.
4. Stop hard at the max-turns limit and report that the limit was hit. `run` then exits with code 1. Tool calls in the last turn still run.

### 3. Tools

| Tool | Args | Returns | Notes |
|---|---|---|---|
| `exec` | `cmd: string`, `timeout_sec?: int` (default 60, max 300) | `stdout`, `stderr`, `exit_code`, `timed_out` | Runs `sh -c cmd` in the sandbox, working dir `/workspace`. A non-zero exit or timeout is a result, not an error |
| `vfs_write` | `path: string`, `content: string` | `path`, `version`, `hash` | Does not touch `/workspace`. Writing the same content as the latest version is a no-op |
| `vfs_read` | `path: string`, `version?: int` | `content`, `version` | Latest version by default |
| `vfs_list` | `prefix?: string` | `[{path, latest_version, size}]` | |
| `vfs_history` | `path: string` | `[{version, hash, size, created_at}]` | Newest first |
| `vfs_commit` | `paths: string[]` | `[{path, version}]` | Copies files **from sandbox `/workspace`** into the VFS. At most 100 paths; all are read before any is saved |

### 4. Sandbox

The concrete type exposes `New(ctx, client, opts) (*Sandbox, error)`, `Exec`, `CopyIn`, `CopyOut` and `Destroy`.

- One container per run, labelled `boxagent.session=<id>`, kept alive with `sleep infinity`. It runs as uid 1000 with all capabilities dropped, `no-new-privileges`, no host mounts and no Docker socket.
- Limits: 1 CPU, 1 GiB of memory without swap, 256 processes. The network is off unless `--network` is set. A background check stops the run if the container writes more than 4 GiB.
- `New` runs a check command, because Docker's init process hides a missing `sleep`, `sh` or `timeout`. If anything fails, it removes the container.
- `Exec` keeps the first and last 512 KB of each output stream, so a command can't exhaust host memory.
- `CopyIn` and `CopyOut` use tar streams and expect paths the caller has validated. `CopyOut` accepts only regular files up to 5 MB.
- The session destroys the container with a fresh context, so cleanup runs after cancellation. The CLI catches SIGINT, SIGTERM, SIGHUP and SIGPIPE.

### 5. VFS

Content-addressed blobs plus a version table.

```sql
CREATE TABLE IF NOT EXISTS versions (
    path       TEXT    NOT NULL,
    version    INTEGER NOT NULL,
    hash       TEXT    NOT NULL,
    size       INTEGER NOT NULL,
    session_id TEXT    NOT NULL,
    created_at TEXT    NOT NULL,   -- RFC3339 UTC
    PRIMARY KEY (path, version)
);
```

`List` takes a `vfs.Prefix`, validated like a path except that empty means everything.

- `vfs.ParsePath` is the only place paths are validated. It rejects absolute paths, any `..` segment, empty paths, NUL bytes, backslashes and paths over 512 bytes, then cleans what remains. `Store` methods take only validated types.
- Blobs live at `<data-dir>/blobs/<first-2-hex>/<sha256>`, written to a temp file and renamed. Reads check the hash.
- Versions start at 1 per path and are assigned inside a transaction. Writing the latest content again is a no-op.
- The blob is written before the row, so a crash leaves at most an unused blob. Files over 5 MB are rejected.
- A new path can't be a directory of an existing file, or inside one.
- `Delete` removes rows, then the blobs nothing uses. Blobs change only while holding the database write lock, so a concurrent `Write` can't lose one.

### 6. Session

- Creates the sandbox, seeds it, runs the agent and destroys the sandbox, in that order, with cleanup guaranteed.
- `--seed <prefix>` (`.` for everything): a prefix that is empty or matches no files fails before the sandbox is created. Otherwise it copies the latest version of every VFS file under that prefix into `/workspace` before the agent starts. This is the only automatic VFS → sandbox transfer.
- Provides `NewID` for the session ID (used for container labels and the `session_id` column).

## Design decisions

- **API key check:** `config.Load` allows a missing key, because only `run` needs one. `run` calls `RequireAPIKey` before any other work.
- **Path and session adapters:** tools can't import `vfs`, so their interfaces take plain strings. Adapters in `cli/files.go` validate paths with `vfs.ParsePath` and stamp writes with the session ID.
- **Session wiring:** the sandbox, tools and store writes all need the session ID, so `cli/run.go` gets it from `session.NewID` and passes `session.Run` the functions that create the sandbox and the agent.
- **Command timeouts:** Docker has no API to kill an exec, so commands run under `timeout -s KILL` inside the container. A slightly later host deadline closes the stream as a fallback. Images must therefore provide `timeout`.
- **Truncation:** the loop caps every string in a tool result at 8 KB, so it needs no knowledge of individual tools. This also caps `vfs_read` content.
- **Thought signatures:** Gemini needs these returned with function calls. `agent` carries them as opaque bytes and never interprets them.
- **Disk limit:** Docker's size option works with few storage drivers, so `sandbox/docker` polls the container's reported size instead. Past the limit it kills the container and cancels the run with `ErrDiskLimit` as the cause.
- **Model retries:** rate limits, server errors, network failures and empty or malformed responses get up to 5 attempts, with backoff and jitter or the delay Gemini asks for. A requested delay over 90 seconds is treated as an exhausted quota and fails at once.
- **Library environment variables:** the Docker library reads its own, such as `DOCKER_HOST`. The Gemini address is set in code, so the environment can't redirect the API key.
- **Tests share their package's name** (`package vfs`, not `vfs_test`), because `depguard` would reject a test that imports its own package. They still go through exported APIs where possible.

## Not included

There is no MCP server, web UI, microVM runtime, rollback or multi-agent support. Please open an issue before working on one of these.
