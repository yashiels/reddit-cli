# reddit-cli

Reddit from the terminal, in Go. Authenticates as the official Reddit Android
app using the OAuth client_id extracted from the APK, with request **jitter +
429 backoff** ("shudder") so traffic doesn't look robotic — modelled on
[twitter-cli](https://github.com/yashiels/twitter-cli).

> ⚠️ Mimicking the first-party client is against Reddit's API ToS. Personal use only.

## Build

```sh
make build      # -> ./reddit
make test
```

## Auth, in two modes

**Anonymous (default, zero config).** Reads work immediately — no account, no
setup — via the app's `installed_client` grant:

```sh
./reddit feed golang --sort top --limit 5
./reddit comments t3_1u90dkx
```

**User (for vote/reply/whoami).** Reddit blocks password auth for the official
*installed* app, so user actions need your **own script app** (free, 2 min):

1. Go to <https://www.reddit.com/prefs/apps> → *create another app* → type **script**.
2. Note the client id (under the app name) and the secret.
3. Log in:

```sh
./reddit login --client-id <id> --client-secret <secret> --user <name>
# password is prompted securely; add --otp 123456 if 2FA is on
```

The token + password are stored at
`~/Library/Application Support/reddit-cli/credentials.json` (mode 0600). The
password is kept so the 1-hour token can refresh silently — treat that file
like a password.

## Commands

| Command | What |
|---|---|
| `reddit feed [sub] --sort hot\|new\|top\|rising --limit N` | front page or subreddit listing |
| `reddit comments <post-id> --limit N` | a post and its top comments |
| `reddit whoami` | the logged-in account *(login)* |
| `reddit vote <fullname> <up\|down\|none>` | vote on `t3_…`/`t1_…` *(login)* |
| `reddit reply <fullname> <text>` | reply to a post/comment *(login)* |
| any command `--json` | raw JSON to stdout |

## How the auth was derived

Pulled from `base.apk` (com.reddit.frontpage 2026.24.0 / build 2624050):

- `res/values/strings.xml` → `oauth_client_id = ohXpoqrZYub1kg`, `base_uri_default = https://oauth.reddit.com`
- `classes*.dex` → User-Agent template `Reddit/Version <v>/Build <b>/Android <api>`

## Gotcha: HTTP/1.1 is forced

Go's default HTTP/2 TLS fingerprint trips Reddit's Cloudflare bot wall (it
serves an HTML interstitial instead of JSON). The client pins HTTP/1.1, which
passes. See `internal/auth.NewHTTPClient`.
