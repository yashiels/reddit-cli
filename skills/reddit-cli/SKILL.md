---
name: reddit-cli
description: Use when an agent or user needs to interact with Reddit from the terminal — browse a subreddit or the front page, read a post's comments, search posts, look up a user or subreddit, list someone's submissions, or (when logged in) vote/reply. Wraps the `reddit` Go CLI, which authenticates as the official Reddit Android app. Trigger on "reddit", "subreddit", "r/<name>", "front page", "search reddit", "reddit comments", "upvote/downvote a post".
---

# reddit-cli

A terminal client for Reddit that authenticates as the official Reddit Android
app (OAuth `client_id` + `User-Agent` extracted from the APK). Reads work with
zero setup via the app's anonymous token; writes need a logged-in session.

> Personal use only — mimicking the first-party client is against Reddit's API ToS.

## Build / install

```sh
cd reddit-cli && make build      # produces ./reddit
make install                     # or: go install ./cmd/reddit  (puts `reddit` on PATH)
```

## Auth model — read this before writing

- **Reads are anonymous.** No login needed. `feed`, `comments`, `user`,
  `search`, `subreddit`, `posts` work immediately.
- **Writes need a user token.** `whoami`, `vote`, `reply` call `RequireUser()`
  and error if not logged in. Reddit blocks password auth for the app's own
  client, and the app's native login is gated by Play Integrity attestation, so
  there are exactly two ways to log in:
  1. `reddit login --access-token <eyJ…>` — a bearer lifted from a logged-in
     reddit.com session (DevTools → Network → `authorization` header). No script app.
  2. `reddit login --client-id <id> --client-secret <secret> --user <name>` —
     a *script* app from https://www.reddit.com/prefs/apps (the only headless
     password auth Reddit permits).

Credentials are stored at `$XDG_CONFIG_HOME/reddit-cli/credentials.json` (0600).

## Commands

| Command | Purpose | Auth |
|---|---|---|
| `reddit feed [sub] --sort hot\|new\|top\|rising --limit N` | front page / subreddit listing | anon |
| `reddit comments <post-id> --limit N` | a post + its top comments | anon |
| `reddit search <query> --sub <s> --sort relevance\|top\|new --limit N` | search posts | anon |
| `reddit user <name>` | a user's public profile | anon |
| `reddit posts <user> [--comments] --limit N` | a user's submissions/comments | anon |
| `reddit subreddit <name>` | subreddit info | anon |
| `reddit whoami` | the logged-in account | login |
| `reddit vote <fullname> <up\|down\|none>` | vote on `t3_…`/`t1_…` | login |
| `reddit reply <fullname> <text>` | reply to a post/comment | login |
| `reddit login …` | store credentials | — |

## Conventions

- Add `--json` to any command for raw JSON (scripting); human text otherwise.
- IDs are Reddit **fullnames**: `t3_<id>` = post, `t1_<id>` = comment. `feed`
  and `search` print the fullname so you can pass it to `vote`/`reply`.
- The client adds jitter (700–1800ms) + 429 backoff and pins HTTP/1.1 (Go's
  HTTP/2 fingerprint trips Reddit's Cloudflare bot wall). Don't remove this.

## Examples

```sh
reddit feed golang --sort top --limit 5
reddit search "binary size" --sub golang --json | jq '.data.children[].data.title'
reddit comments t3_1u90dkx
reddit subreddit programming
reddit vote t3_1u90dkx up      # requires login
```
