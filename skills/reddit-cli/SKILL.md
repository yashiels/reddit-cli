---
name: reddit
description: Use when an agent or user needs Reddit from the terminal — browse a subreddit or the front page, read a post's comments, search posts, look up a user or subreddit, list someone's submissions (all anonymous, zero setup), or, when logged in, vote/reply (confirm-gated, only on explicit user request). Wraps the `reddit` Go CLI, which authenticates as the official Reddit Android app. Trigger on "reddit", "subreddit", "r/<name>", "front page", "search reddit", "reddit comments", "upvote/downvote a post".
---

# reddit

A terminal client for Reddit, in Go. It authenticates as the official Reddit
Android app (OAuth `client_id` + `User-Agent` lifted from the APK) and adds
request jitter + 429 backoff so traffic doesn't look robotic. Reads work with
zero setup via the app's anonymous token; vote/reply need a logged-in session.

> **Personal use only.** Mimicking the first-party client is against Reddit's
> API ToS — the CLI itself prints this. Do not build automation on top of it.

## Install

```sh
brew install yashiels/tap/reddit
```

The binary is `reddit`. From source: `cd reddit-cli && make build` (produces
`./reddit`), or `go install ./cmd/reddit`.

## Auth

Reads are **anonymous** — no login needed. The client falls back to the app's
`installed_client` grant (client id extracted from the APK) and caches that
token, so `feed`/`comments`/`search`/`user`/`subreddit`/`posts` work
immediately with no account.

Writes (`whoami`, `vote`, `reply`) call `RequireUser()` and error if you are
not logged in. Reddit **blocks the password grant for the app's own installed
client**, so `reddit login` cannot use the app's client id for user auth — you
supply your own credentials one of two ways:

| Login path | Command | Notes |
|---|---|---|
| Script app (password grant) | `reddit login --client-id <id> --client-secret <secret> --user <name>` | Register a **script** app at <https://www.reddit.com/prefs/apps>. `--client-id`/`--user` are prompted if omitted; password is prompted securely (or pass `--password`); add `--otp 123456` for 2FA. Password is stored so the 1-hour token auto-refreshes. |
| Browser bearer (no script app) | `reddit login --access-token <eyJ…>` | Paste a bearer from a logged-in reddit.com session (DevTools → Network → `authorization` header). No password stored, so **no auto-refresh** — expires in ~1h, re-paste when it does. |

**Token storage:** `$XDG_CONFIG_HOME/reddit-cli/credentials.json` (mode 0600;
on macOS `~/Library/Application Support/reddit-cli/credentials.json`). It holds
the access token and, on the script-app path, the password — treat the file
like a password. The anonymous app token and device id are cached alongside it
(`app_token.json`, `device_id`).

## Read commands (anonymous — safe unattended)

| Command | Purpose |
|---|---|
| `reddit feed [subreddit] --sort hot\|new\|top\|rising\|best --limit N` | front page (no arg) or a subreddit listing (default sort `hot`, limit 25) |
| `reddit comments <post-id> --limit N` | a post + its top-level comments (default limit 20; accepts `t3_…` or bare id) |
| `reddit search <query> --sub <s> --sort relevance\|hot\|top\|new\|comments --limit N` | search posts, optionally restricted to a subreddit |
| `reddit user <name>` | a user's public profile (karma, age) |
| `reddit posts <user> [--comments] --limit N` | a user's recent submissions, or their comments with `--comments` |
| `reddit subreddit <name>` | subreddit info (subscribers, online, description) |

## Write / action commands (require login — confirm-gated)

| Command | Purpose |
|---|---|
| `reddit whoami` | show the logged-in account |
| `reddit vote <fullname> <up\|down\|none>` | up/down/clear your vote on `t3_…` (post) or `t1_…` (comment) |
| `reddit reply <fullname> <text>` | reply to a post or comment |
| `reddit login …` | store credentials (see Auth) |

**Global:** add `--json` to any command for raw JSON to stdout (human text
otherwise). IDs are Reddit **fullnames** — `t3_<id>` = post, `t1_<id>` =
comment; `feed` and `search` print the fullname so you can pass it to
`vote`/`reply`.

## Headless / agent usage

- **Reads are fully headless and safe to run unattended.** No login, no
  interaction — the anonymous app token is fetched and cached automatically.
- **Login is non-interactive only if the caller supplies the secrets.** There
  are **no env vars** — credentials come from flags or an interactive prompt.
  Pass `--client-id --client-secret --user --password` (script app) or
  `--access-token` (browser bearer) to log in with no TTY. If `--password`/
  `--user`/`--client-id` are omitted, the CLI prompts, and the **password
  prompt uses `term.ReadPassword` on the real terminal — it cannot be piped**.
  So: either provide every flag, or hand login back to the user. Never
  fabricate or guess credentials; only log in when the user explicitly asks and
  provides the values (they are account-level secrets).
- **Do not hammer the API.** The client sleeps a random 700–1800ms *before
  every request* ("shudder"), backs off exponentially (~1s→8s, up to 4 retries)
  on 429/5xx, and honors `Retry-After`. Expect each call to take ~1s+ and run
  requests **serially** — do not parallelize, tight-loop, or strip the jitter.
  It also pins HTTP/1.1 (Go's HTTP/2 fingerprint trips Reddit's Cloudflare bot
  wall).
- **`vote` and `reply` are confirm-gated.** They mutate a real account under the
  user's name — run them **only on an explicit user request** for that exact
  action, never speculatively, in bulk, or as a side effect.

## Typical flow

```sh
# Read anonymously — no setup
reddit feed golang --sort top --limit 5
reddit search "binary size" --sub golang --json | jq '.data.children[].data.title'
reddit comments t3_1u90dkx
reddit subreddit programming

# Log in only when the user wants to act (script app + prompted password)
reddit login --client-id abc123 --client-secret s3cr3t --user my_handle
reddit whoami

# Act — only on explicit request
reddit vote t3_1u90dkx up
reddit reply t1_abcdef "Nice writeup, thanks!"
```
