# AGENTS.md

Agent instructions for the `reddit-cli` repo.

- **To use the tool**, load the skill at [`skills/reddit-cli/SKILL.md`](skills/reddit-cli/SKILL.md) — it has the command reference, auth model, and gotchas.
- **Build**: `make build` → `./reddit`. **Test**: `make test`. **Vet**: `make vet`.
- **Layout**: `cmd/reddit` (entry), `internal/auth` (OAuth + token store), `internal/api` (HTTP client with jitter/backoff), `internal/cmd` (Cobra commands).
- **Reads need no auth** (anonymous app token); **writes need login** (see the skill). Don't add a network call without going through `internal/api.Client` — it carries the app User-Agent, bearer, jitter, 429 backoff, and the HTTP/1.1 pin that dodges Reddit's bot wall.
- New commands: add a `*cobra.Command` in `internal/cmd/commands.go`, register it in `init()`, gate writes with `c.RequireUser()`.
