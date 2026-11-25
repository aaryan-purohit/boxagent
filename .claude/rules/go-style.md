---
paths:
  - "**/*.go"
---

# Go code rules

## Design

- One reason to exist per package, one job per type. If you can't describe a package in one sentence without "and", split it.
- Small interfaces of 1–3 methods. A consumer that only reads asks for a reader, not the whole store. Don't create an interface until there is a second implementation or a test that needs a fake.
- Composition over inheritance: embed or wrap. No "base" structs carrying shared behaviour.
- Explicit dependencies through constructors (`NewX(deps...)`). No globals, no `init()` side effects, no package-level singletons, no service locators.
- Make illegal states unrepresentable: validate at construction (e.g. `vfs.ParsePath`). Downstream code never re-validates.
- Functional core, imperative shell: put logic (validation, truncation, version assignment, loop decisions) in pure functions. Keep Docker, SQLite and HTTP at thin edges.
- Fail loudly at boundaries (bad config fails at startup with a clear message). Tool failures during a run go back to the model as structured errors.
- No "manager", "helper" or "util" types, and no layers that only forward calls.

## Code

- `context.Context` is the first parameter of anything that does I/O or can block. Never store a context in a struct.
- Wrap errors with context: `fmt.Errorf("vfs write %q: %w", p, err)`. Export sentinel errors (`vfs.ErrNotFound`, `vfs.ErrInvalidPath`) and check them with `errors.Is`. Never compare error strings.
- Handle every error, or comment why it's ignored: `_ = f.Close() // read-only, error irrelevant`.
- No `panic` outside truly impossible states. No `log.Fatal` outside `main`.
- Functions: aim for under 40 lines, at most 3 levels of nesting, at most 4 parameters (group more into an options struct). Return early.
- Names: short in small scopes, descriptive at package level. No stutter (`vfs.Store`, not `vfs.VFSStore`). No `util`, `common`, `helpers` or `misc` packages, and no package named after a stdlib package.
- Exported identifiers get a doc comment that starts with the identifier's name.
- Constants for magic numbers (`maxOutputBytes = 8 << 10`), defined next to the code that uses them.
- Concurrency only when needed. Every goroutine has an obvious owner and stop condition. `go test -race` must pass.
- Logging through `log/slog` with structured key/value pairs.
