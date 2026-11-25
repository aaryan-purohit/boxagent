# BoxAgent

A sandboxed coding agent in Go. You give it a task. A Gemini model works on it inside a disposable Docker container, and anything worth keeping is saved to a versioned store on your machine. The agent has no direct access to your files.

> **Status:** early and experimental. Commands and stored data may change between versions.

| Space | Lifetime | Purpose |
|---|---|---|
| **Sandbox** (`/workspace` in a container) | One run, then destroyed | Install, build, run, break things |
| **VFS** (SQLite and blobs on the host) | Persistent | Every version of every file the agent saves |

## Install

You need:

- Go 1.26.11 or later (an older Go from 1.21 on downloads it automatically)
- A running Docker daemon (Docker Desktop, OrbStack or Docker Engine)
- A [Gemini API key](https://aistudio.google.com/apikey)

Build from source:

```sh
git clone https://github.com/aaryan-purohit/boxagent.git
cd boxagent
make build                    # creates bin/boxagent
```

Or install straight to Go's bin directory, and type `boxagent` instead of `bin/boxagent` below:

```sh
go install github.com/aaryan-purohit/boxagent/cmd/boxagent@latest
```

Then provide your key. From a clone:

```sh
cp .env.example .env          # then put your key in .env
set -a; source .env; set +a
```

If you used `go install`, set `GEMINI_API_KEY` in your shell instead.

The first run downloads the sandbox image, which is several hundred MB.

**Platforms:** developed and tested on macOS. It compiles for Linux and Windows, but has not been tested there.

**Cost:** a run makes one Gemini request per turn (more if it retries), each carrying the whole conversation so far. Your key is billed, or your free quota used, at Google's rates. `--max-turns` caps the number of turns.

## Demo

Real output, with long lines cut short (`…`) and part of the final answer left out:

```console
$ bin/boxagent run "Write a Go function that reverses a string, with passing tests"
time=2026-11-24T23:16:55.904+05:30 level=INFO msg="session started" session=2284160a08af10ce model=gemini-3.5-flash-lite image=golang:1.23-bookworm seeded=0
> exec {"cmd":"go version"}
< {"exit_code":0,"stderr":"","stdout":"go version go1.23.12 linux/arm64\n","timed_out":false}
> exec {"cmd":"mkdir -p /workspace/stringutil && cd /workspace/stringutil && go mod init stringutil"}
< {"exit_code":0,"stderr":"go: creating new go.mod: module stringutil\n","stdout":"","timed_out":false}
> exec {"cmd":"cat << 'EOF' > /workspace/stringutil/reverse.go\npackage stringutil\n\n// Reverse returns …
< {"exit_code":0,"stderr":"","stdout":"=== RUN   TestReverse\n=== RUN   TestReverse/empty_string\n …
> exec {"cmd":"cat << 'EOF' > /workspace/stringutil/README.md\n# String Utilities in Go\n\nThis package …
< {"exit_code":0,"stderr":"","stdout":"","timed_out":false}
> exec {"cmd":"cd /workspace/stringutil && go test -cover"}
< {"exit_code":0,"stderr":"","stdout":"PASS\ncoverage: 100.0% of statements\nok  \tstringutil\t0.001s\n","timed_out":false}
> vfs_commit {"paths":["stringutil/go.mod","stringutil/reverse.go","stringutil/reverse_test.go","stringutil/README.md"]}
< [{"path":"stringutil/go.mod","version":1},{"path":"stringutil/reverse.go","version":1},{"path":"stringutil/reverse_test.go","version":1},{"path":"stringutil/README.md","version":1}]
I have written a rune-safe Go function that correctly reverses strings (including multi-byte UTF-8 characters, Unicode code points, and emojis), along with comprehensive tests achieving 100% test coverage.
…
time=2026-11-24T23:17:36.037+05:30 level=INFO msg="session ended" session=2284160a08af10ce turns=7
```

The container is gone, but the files are not:

```console
$ bin/boxagent vfs ls
PATH                        VERSION  SIZE
stringutil/README.md        1        335
stringutil/go.mod           1        30
stringutil/reverse.go       1        334
stringutil/reverse_test.go  1        735
```

## Asking for changes

Each run starts fresh. The agent doesn't remember earlier tasks, and its sandbox starts empty. To continue, copy saved files in with `--seed` and describe what you want in a new task. Each change becomes a new version:

```console
$ bin/boxagent run --seed stringutil "Add an IsPalindrome function to stringutil/reverse.go (already in /workspace) with tests, run them, and commit the changed files under the same paths."
…
$ bin/boxagent vfs history stringutil/reverse.go
VERSION  HASH                                                              SIZE  CREATED               SESSION
2        2b0276dcaf593405a543551742117353885dbee08c44e69eba5d97cc2997582d  691   2026-11-24T17:49:05Z  b384af904418320a
1        e2c0a748e7c1e09329f0c0e8ef14a7d0cf98a40d40a62396db2d13c7d6b874f1  334   2026-11-24T17:47:29Z  2284160a08af10ce
```

Tips:

- Put the file or directory right after `--seed`, then the task. Use `--seed .` to copy everything you have saved.
- Asking for a directory in your first task ("in a directory called strutil, ...") keeps projects apart, so you can seed just one.
- Say where the files are and ask for them to be committed under the same paths.

Model output varies: your run will differ, down to the file and directory names.

## Commands

| Command | Purpose |
|---|---|
| `boxagent run "<task>"` | Work on a task in a fresh sandbox |
| `boxagent vfs ls [prefix]` | List saved files |
| `boxagent vfs cat <path> [--version N]` | Print a saved file. On a terminal, control characters are removed; redirected output is exact |
| `boxagent vfs history <path>` | List a file's versions, newest first |
| `boxagent vfs export <directory> [prefix]` | Copy the latest versions into a new or empty directory |
| `boxagent vfs rm <prefix> --yes` | Permanently delete **every version** of a file or directory. Without `--yes`, only lists them |
| `boxagent sandbox prune` | Remove **all** BoxAgent containers, including those of running sessions |

Flags for `run`:

| Flag | Purpose |
|---|---|
| `--seed <prefix>` | Copy the latest version of a saved file or directory (`.` for everything) into `/workspace` first |
| `--max-turns N` | Turn limit; reaching it exits with code 1, keeping what was already saved |
| `--network` | Allow network access from the sandbox |

A prefix is a saved file or directory, and `.` means everything. Only `run` needs the API key. Steps are printed to stdout and logs to stderr.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `GEMINI_API_KEY` | none; needed by `run` | Google AI Studio API key |
| `BOXAGENT_MODEL` | `gemini-3.5-flash-lite` | Gemini model name |
| `BOXAGENT_DATA_DIR` | `~/.boxagent` | Where saved files are stored |
| `BOXAGENT_IMAGE` | `golang:1.23-bookworm` | Sandbox image |
| `BOXAGENT_MAX_TURNS` | `25` | Turn limit |
| `BOXAGENT_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

### Other languages

For tasks in another language, choose an image with its toolchain. It must provide `sh`, `sleep` and `timeout`, and work as a non-root user. For example, with Python:

```sh
export BOXAGENT_IMAGE=python:3.13-slim
bin/boxagent run "In a directory called strutil, write a Python function that reverses a string, with passing unittest tests"
bin/boxagent run --seed strutil "The project is in /workspace/strutil. Add a palindrome check with tests, run all the tests, and commit the changed files under the same paths."
```

The network is off by default, so packages must already be in the image.

## Security model

- The container runs as uid 1000 with all capabilities dropped, `no-new-privileges`, no host mounts and no Docker socket. It is limited to 1 CPU, 1 GiB of memory and 256 processes. A run that writes more than 4 GiB to the container is stopped.
- The network is off by default, so a run can't download modules or packages. `--network` uses Docker's bridge network, from which services on your machine or local network may be reachable.
- The agent reaches the host only through the `vfs_*` tools. Paths are validated, and content is stored by hash rather than under names the agent picks.
- Each command is killed after its timeout (60 s by default, 300 s at most).
- The container is destroyed when the run ends, including after Ctrl+C. If BoxAgent itself is killed with `kill -9`, use `sandbox prune`.

Limits to know about:

- A container shares the host's kernel (or your Docker VM's), so it is weaker isolation than a separate VM.
- Disk use is checked about once a second rather than enforced by a quota, so a fast command can overshoot the 4 GiB limit briefly. Volumes declared by a custom image are not counted.
- The task, the files the agent reads and the output of its commands are sent to the Gemini API.
- Review saved code before running it outside the sandbox. That includes exported hidden files, such as `.git/config`, which other tools may act on.
- Files are limited to 5 MB each, and the model sees at most 8 KB of any one output.

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## Troubleshooting

| Problem | What to do |
|---|---|
| `failed to connect to the docker API` | Start Docker. BoxAgent uses `DOCKER_HOST` if set, and otherwise `/var/run/docker.sock`. It ignores Docker contexts, so you may need the command below. |
| `GEMINI_API_KEY is not set` | Load your key in this terminal: `set -a; source .env; set +a` |
| `no task given` | `--seed` took your task as its path. Use `run --seed <path> "<task>"`. |
| `no files to seed` | Check the path with `boxagent vfs ls`. |
| `path conflicts with an existing file` | A name can't be both a file and a directory. Use another name, or remove the old one with `vfs rm`. |
| `sandbox exceeded its disk limit` | The run wrote more than 4 GiB. Ask for something smaller. Files already committed are kept. |
| `stopped at the turn limit` | Raise `--max-turns` or ask for something smaller. Files already committed are kept. |
| `sandbox cannot run commands` | The image lacks `sh`, `sleep` or `timeout`. |
| Error 429 | You hit a Gemini rate limit or quota. BoxAgent makes up to 5 attempts, waiting as long as Gemini asks, up to 90 seconds each time. If it still fails, wait and run again. |
| Error 404 from Gemini | Your key can't use the model. Set `BOXAGENT_MODEL` to one it can. |
| Leftover containers | `boxagent sandbox prune` |

To point BoxAgent at the daemon of your current Docker context:

```sh
export DOCKER_HOST=$(docker context inspect --format '{{.Endpoints.docker.Host}}')
```

## Uninstall

```sh
boxagent sandbox prune                      # leftover containers
rm "$(command -v boxagent)"                 # only if you used go install
docker image rm golang:1.23-bookworm        # and any other sandbox image
```

Your saved files are in `~/.boxagent`, or `BOXAGENT_DATA_DIR` if you set it. Deleting that directory permanently deletes every version of every file.

## Development

```sh
make test      # unit tests; no Docker or network needed
make test-int  # integration tests; need Docker, and use GEMINI_API_KEY if set
make lint
```

`make test-int` removes every BoxAgent container, so don't run it during a live session.

See [CONTRIBUTING.md](CONTRIBUTING.md) to get started and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the design.

## License

[MIT](LICENSE)
