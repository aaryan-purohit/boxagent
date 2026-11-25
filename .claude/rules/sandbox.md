---
paths:
  - "internal/sandbox/**"
  - "internal/session/**"
---

# Sandbox and session rules

## Security (non-negotiable)

- No host bind mounts, no Docker socket mount, no `--privileged`. Drop all capabilities. Run as a non-root user where the image allows it.
- Resource limits: 1 CPU, 1 GiB memory, pids limit 256. A watchdog kills the container past 4 GiB written; `Destroy` stops it.
- Network mode `none` unless `--network` is set.
- `CopyIn`/`CopyOut` use tar streams. Paths are relative to `/workspace` and validated with the same rules as VFS paths (`vfs.ParsePath` rules; `sandbox/docker` must not import `vfs`, so the caller passes validated paths).

## Lifecycle

- One container per `run` session. `session` creates it and **always destroys it** with `defer`, including after Ctrl+C. Use a fresh short-timeout context for cleanup, because the run context may already be cancelled.
- Keep the container alive with `sleep infinity`. Run commands through exec create/attach and demux the streams with `stdcopy`.
- Enforce exec timeouts with a context deadline. On timeout, kill the process and return `timed_out: true`.
- Label containers `boxagent.session=<id>` so `boxagent sandbox prune` can find orphans.
- Use the Docker Go client only. Never shell out to the `docker` CLI.

## Session

- Order: create sandbox → seed → run agent → destroy, with cleanup guaranteed.
- `--seed <prefix>` copies the latest version of every VFS file under that prefix into `/workspace`. An empty prefix or one with no files fails before the sandbox is created. This is the only automatic VFS → sandbox transfer.
- `session.NewID` generates the session ID used for container labels and the `session_id` column.
