---
paths:
  - "internal/config/**"
  - ".env.example"
---

# Config rules

- `config.Load(getenv func(string) string)` reads env once and returns a validated `Config`. Taking `getenv` as a parameter keeps it testable without touching the real environment.
- Env vars are uppercase with a `BOXAGENT_` prefix (`BOXAGENT_MODEL`). The only exception is `GEMINI_API_KEY`.
- Precedence: flag > env > default. Flags override env for per-run settings only.
- The default model name lives only here. Never hard-code the model name or the key anywhere else.
- `Load` allows a missing `GEMINI_API_KEY`, since `vfs`, `sandbox prune` and `--version` don't need it. Commands that do (`run`) call `cfg.RequireAPIKey()` before any other work; its error names the variable.
- `.env.example` lists every variable (see the table in `docs/ARCHITECTURE.md`). `.env` is gitignored. No dotenv library: users `source .env` or use direnv.
