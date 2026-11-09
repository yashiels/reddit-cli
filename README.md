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

## Auth

**Anonymous (default, zero config).** Reads work immediately — no account, no
setup — via the app's `installed_client` grant:

```sh
./reddit feed golang --sort top --limit 5
./reddit comments t3_1u90dkx
```

### Native sessions with automatic renewal

Import an owner-authorized native Reddit session from a private JSON file:

```json
{"cookie":"reddit_session=YOUR_NATIVE_SESSION_VALUE","user_agent":"Reddit/Version 2026.44.0/Build 2644080/Android 14"}
```

```sh
chmod 600 /path/to/native-import.json
./reddit login --native-session-file /path/to/native-import.json
./reddit whoami
./reddit search "golang" --limit 3
```

The file must contain only `cookie` and `user_agent`. Supply exactly one native
`reddit_session` cookie pair, not a bearer token or browser Cookie header. The
import file must be a regular file, not a symlink. Import renews the bearer and
validates the account before saving. Delete the input file after import and treat
the stored session like a password.

Commands renew an expired or missing bearer lazily and verify the same account
before using it. Android, a browser, a background service and a password are not
needed for renewal. Reads use OAuth with the native User-Agent. Credentials and
any rotated session cookie are saved together with mode 0600 in the OS config
directory at `reddit-cli/credentials.json`. Concurrent processes coordinate with
a bounded file lock; if storage is busy, retry the command.

Server revocation or session-cookie expiry requires a fresh authorized import.
Network, identity and storage failures return an error without falling back to
anonymous access. A crash after a server rotates a single-use cookie but before
local persistence can also require reimport. Cookie rotation is supported, but
the verified native responses so far retained the original session cookie.

Native import replaces any previous login mode. Logging in with browser cookies,
a pasted bearer or a script app clears native renewal state. The native import
flag cannot be combined with other credential flags.

### Script-app login

Reddit blocks password auth for the official *installed* app. For password login,
use your **own script app** (free, 2 min):

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

### Browser cookies for reads

If OAuth reads return `403 Blocked`, save the logged-in browser's raw Cookie
header value on line 1 of a local file and its User-Agent on line 2, then run:

```sh
./reddit login --cookie-file /path/to/browser-session.txt
```

Login validates the browser identity before saving `cookie` and `user_agent`
in the same 0600 credentials file, preserving existing legacy OAuth credentials.
Switching from native mode clears its bearer and renewal state.
All reads use reddit.com JSON endpoints with those cookies; `whoami` shows the
browser account. Re-run this login when the cookie session expires. Reads fall
back to OAuth if cookies are rejected. Vote/reply still require OAuth login.
Treat the input file like a password and delete it after importing.

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

With a native session, `reddit comments` uses the persisted PostComments GraphQL
read. `--json` preserves the raw GraphQL envelope: comments are under
`data.postInfoById.commentForest.trees`, including pagination placeholders and
unknown upstream fields. Other auth modes keep the legacy two-listing JSON array.
Native text output shows at most `--limit` top-level comment nodes returned by
Reddit, skipping placeholders and replies without fetching further pages. It can
return fewer comments than requested. The native post header shows title,
subreddit and comment count; post author, score and selftext are unavailable in
this query.
