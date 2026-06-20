// Package api is a thin client over Reddit's oauth.reddit.com API, authenticating
// as the official Android app. It adds "shudder": randomized jitter before every
// request plus 429-aware exponential backoff, so traffic doesn't look robotic and
// we stay under rate limits (mirrors twitter-cli's anti-ban behavior).
package api

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yashiels/reddit-cli/internal/auth"
)

// APIBase is a var so tests can point the client at a mock server.
var APIBase = "https://oauth.reddit.com"

// Tunables for shudder. Conservative on purpose — banned beats fast.
const (
	minJitter  = 700 * time.Millisecond
	maxJitter  = 1800 * time.Millisecond
	maxRetries = 4
)

// noSleep disables jitter/backoff sleeps in tests.
var noSleep bool

type Client struct {
	token  func() (string, error)
	authed bool // true if a user is logged in (vs anonymous app token)
	http   *http.Client
}

// New returns a client. If a user session exists it acts as that user; otherwise
// it falls back to an anonymous read-only app token (zero config).
func New() (*Client, error) {
	s, err := auth.Load()
	if err != nil {
		return nil, err
	}
	c := &Client{http: auth.NewHTTPClient(30 * time.Second)}
	if s != nil {
		c.authed, c.token = true, s.Token
	} else {
		dev := deviceID()
		c.token = func() (string, error) { return auth.AppToken(dev) }
	}
	return c, nil
}

// RequireUser errors if the client is anonymous (action needs a login).
func (c *Client) RequireUser() error {
	if !c.authed {
		return fmt.Errorf("this requires a logged-in account — run `reddit login` (needs a script app: https://www.reddit.com/prefs/apps)")
	}
	return nil
}

// deviceID returns a stable per-install random id, persisted next to the config.
func deviceID() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "reddit-cli-anon"
	}
	path := filepath.Join(dir, "reddit-cli", "device_id")
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return strings.TrimSpace(string(b))
	}
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		return "reddit-cli-anon"
	}
	// Reddit requires a UUID-format device_id (it rejects arbitrary strings).
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	h := hex.EncodeToString(b)
	id := fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		os.WriteFile(path, []byte(id), 0o600)
	}
	return id
}

// shudder sleeps a random interval so requests aren't perfectly periodic.
func shudder() {
	if noSleep {
		return
	}
	d := minJitter + time.Duration(rand.Int63n(int64(maxJitter-minJitter)))
	time.Sleep(d)
}

// do performs an authenticated request with jitter and 429/5xx backoff.
func (c *Client) do(method, path string, form url.Values) ([]byte, error) {
	token, err := c.token()
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		shudder()

		var body io.Reader // rebuilt each attempt: a Reader is single-use
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequest(method, APIBase+path, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", auth.UserAgent)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			backoff(attempt, 0)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			ra, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			lastErr = fmt.Errorf("reddit returned %s", resp.Status)
			backoff(attempt, ra)
			continue
		case resp.StatusCode == 401:
			return nil, fmt.Errorf("unauthorized — token rejected, try `reddit login` again")
		case resp.StatusCode >= 400:
			return nil, fmt.Errorf("reddit returned %s: %s", resp.Status, truncate(data))
		}
		return data, nil
	}
	return nil, fmt.Errorf("giving up after %d retries: %w", maxRetries, lastErr)
}

func backoff(attempt, retryAfter int) {
	if noSleep {
		return
	}
	if retryAfter > 0 {
		time.Sleep(time.Duration(retryAfter) * time.Second)
		return
	}
	// exponential with jitter: ~1s, 2s, 4s, 8s (+/- jitter)
	base := time.Duration(1<<attempt) * time.Second
	time.Sleep(base + time.Duration(rand.Int63n(int64(time.Second))))
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// Get fetches path (with optional query params) and decodes JSON into v.
func (c *Client) Get(path string, params url.Values, v any) error {
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	data, err := c.do("GET", path, nil)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(data, v)
}

// Post submits a form and decodes JSON into v (v may be nil to ignore the body).
func (c *Client) Post(path string, form url.Values, v any) error {
	data, err := c.do("POST", path, form)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(data, v)
}
