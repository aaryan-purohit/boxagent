---
paths:
  - "internal/cli/**"
  - "cmd/**"
---

# Cobra / CLI rules

- Build commands with constructor functions (`newRunCmd(deps) *cobra.Command`). No package-level `var` commands, no `init()` registration.
- Use `RunE`, never `Run`. Return errors; don't print and `os.Exit` inside commands.
- Set `SilenceUsage: true` on the root.
- Validate positional args with `cobra.ExactArgs`/`cobra.MaximumNArgs`.
- Write output to `cmd.OutOrStdout()`/`cmd.ErrOrStderr()` so tests can capture it.
- Get the context from `cmd.Context()`. The root sets it up with `signal.NotifyContext` for SIGINT, SIGTERM, SIGHUP and SIGPIPE, so that cleanup always runs.
- Keep command bodies thin: parse input, call one function, format output.
- `cmd/boxagent/main.go` stays about 10 lines: build the root command, Execute, set the exit code.
- `cli/run.go` is the composition root. Reading it should show how the whole system fits together.
