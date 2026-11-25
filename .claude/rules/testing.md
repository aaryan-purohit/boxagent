---
paths:
  - "**/*_test.go"
  - "testdata/**"
---

# Testing rules

## What to test

- Behaviour through exported APIs. Don't assert on private fields or the order of internal calls unless the order is the behaviour.
- Every pure function gets exhaustive table-driven tests: `vfs.ParsePath` (every rejection case), truncation, config parsing and validation, version assignment, backoff calculation.
- `agent`: scripted fake `Model` and fake tools. Cover a normal multi-turn run, an unknown tool name, a tool error (the loop continues), invalid tool args, hitting max turns, context cancellation, and truncation applied to tool output.
- `tools`: each tool with a fake dependency. Cover argument validation (missing fields, wrong types, out-of-range timeout), the success path, and how dependency errors map into the result.
- `vfs`: real SQLite and real files in `t.TempDir()`. Don't fake them. Cover first write, new version on changed content, no-op on identical content, blob dedupe across paths, reading a specific version, `ErrNotFound`, history ordering, list by prefix, and the size limit.
- `config`: inject a fake `getenv`. Cover defaults, overrides, a missing API key and invalid numbers.
- `cli`: run commands in-process with `cmd.SetArgs`/`SetOut`/`SetErr` against fakes. Cover argument validation and output format.
- `sandbox/docker` and the Gemini adapter: unit-test the pure parts (output capture, archives, message conversion, backoff) and the Gemini request and retry path with a fake HTTP transport. Test everything that needs Docker or the real API behind `//go:build integration`. Cover exec, timeout, no network, cleanup on destroy, and one real function-calling round trip (skip when `GEMINI_API_KEY` is unset). The round trip lives in `internal/cli`, the only package that may combine the adapter with `config`.

## How to write tests

- Table-driven with `t.Run(tc.name, ...)`. Case names read like sentences: `"rejects parent traversal"`.
- Arrange / act / assert, visibly separated. One behaviour per case.
- Hand-written fakes in `fake_test.go`, or in a `<pkg>test` sub-package when shared. No mock generators, no assertion libraries.
- Compare with `cmp.Diff(want, got)` and report `t.Errorf("Run() mismatch (-want +got):\n%s", diff)`.
- Check errors with `errors.Is`/`errors.As`, never by string.
- Use `t.Helper()`, `t.TempDir()` and `t.Cleanup`. Use `t.Setenv` only where `getenv` injection isn't possible.
- `t.Parallel()` wherever there's no shared state.
- Deterministic: no `time.Sleep`, no real clock (inject `clock`), no unseeded randomness, no network.
- Golden files in `testdata/` for long expected outputs (prompts, CLI output), regenerated with an `-update` flag.
- Coverage is a signal, not a target. Don't write tests just to raise the number.
