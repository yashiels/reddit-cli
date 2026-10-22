// Package api is a thin client over Reddit's oauth.reddit.com API, authenticating
// as the official Android app. It adds "shudder": randomized jitter before every
// request plus 429-aware exponential backoff, so traffic doesn't look robotic and
// we stay under rate limits (mirrors twitter-cli's anti-ban behavior).
package api

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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

var WebBase = "https://www.reddit.com"

// Tunables for shudder. Conservative on purpose — banned beats fast.
const (
	minJitter  = 700 * time.Millisecond
	maxJitter  = 1800 * time.Millisecond
	maxRetries = 4
)

// noSleep disables jitter/backoff sleeps in tests.
var noSleep bool

type Client struct {
	cookie    string
	userAgent string
	token     func() (string, error)
	authed    bool
	http      *http.Client
}

// New returns a client. If a user session exists it acts as that user; otherwise
// it falls back to an anonymous read-only app token (zero config).
func New() (*Client, error) {
	s, err := auth.Load()
	if err != nil {
		return nil, err
	}
	c := &Client{http: auth.NewHTTPClient(30 * time.Second)}
	c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	if s != nil {
		c.cookie, c.userAgent = s.Cookie, s.UserAgent
	}
	if s != nil && (s.AccessToken != "" || s.Password != "") {
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
	if method == http.MethodGet && c.cookie != "" {
		cookiePath, err := browserPath(path)
		if err != nil {
			return nil, err
		}
		data, err := c.request(method, WebBase+cookiePath, form, "", true)
		if err == nil && strings.Split(path, "?")[0] == "/api/v1/me" {
			data, _, err = cookieIdentity(data)
		}
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, errCookieExpired) {
			return nil, err
		}
		data, err = c.oauthRequest(method, path, form)
		if err != nil || !json.Valid(data) {
			return nil, errCookieExpired
		}
		return data, nil
	}
	return c.oauthRequest(method, path, form)
}

var errCookieExpired = errors.New("cookie session expired: re-run reddit login --cookie-file")

func browserPath(path string) (string, error) {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "", fmt.Errorf("invalid Reddit read path")
	}
	if u.Path == "/api/v1/me" {
		u.Path = "/api/me"
	}
	if !strings.HasSuffix(u.Path, ".json") {
		u.Path = strings.TrimRight(u.Path, "/") + ".json"
	}
	q := u.Query()
	q.Set("raw_json", "1")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Client) oauthRequest(method, path string, form url.Values) ([]byte, error) {
	token, err := c.token()
	if err != nil {
		return nil, err
	}
	return c.request(method, APIBase+path, form, token, false)
}

func (c *Client) request(method, endpoint string, form url.Values, token string, cookie bool) ([]byte, error) {
	// POST requests are not idempotent — never retry them.
	maxAttempts := maxRetries
	if method == "POST" {
		maxAttempts = 0
	}

	var lastErr error
	for attempt := 0; attempt <= maxAttempts; attempt++ {
		shudder()

		var body io.Reader // rebuilt each attempt: a Reader is single-use
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequest(method, endpoint, body)
		if err != nil {
			return nil, err
		}
		if cookie {
			req.Header.Set("Cookie", c.cookie)
			req.Header.Set("User-Agent", c.userAgent)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("User-Agent", auth.UserAgent)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxAttempts {
				backoff(attempt, 0)
			}
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if cookie && (resp.StatusCode == 401 || resp.StatusCode == 403 || (resp.StatusCode != 429 && resp.StatusCode < 500 && !json.Valid(data))) {
			return nil, errCookieExpired
		}

		switch {
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			if cookie && attempt == maxAttempts && !json.Valid(data) {
				return nil, errCookieExpired
			}
			ra, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			lastErr = fmt.Errorf("reddit returned %s", resp.Status)
			if attempt < maxAttempts {
				backoff(attempt, ra)
			}
			continue
		case resp.StatusCode == 401:
			return nil, fmt.Errorf("unauthorized — token rejected, try `reddit login` again")
		case resp.StatusCode >= 300:
			if cookie {
				return nil, fmt.Errorf("reddit returned %s", resp.Status)
			}
			return nil, fmt.Errorf("reddit returned %s: %s", resp.Status, truncate(data))
		}
		if readErr != nil {
			return nil, fmt.Errorf("cannot read Reddit response")
		}
		return data, nil
	}
	return nil, fmt.Errorf("giving up after %d retries: %w", maxAttempts, lastErr)
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

// GetRaw fetches path (with optional query params) and returns the raw response
// body bytes. Callers that want to emit unmodified upstream JSON use this.
func (c *Client) GetRaw(path string, params url.Values) ([]byte, error) {
	u, err := url.ParseRequestURI(path)
	if err != nil {
		return nil, fmt.Errorf("invalid Reddit read path")
	}
	q := u.Query()
	for key, values := range params {
		q[key] = values
	}
	u.RawQuery = q.Encode()
	path = u.String()
	return c.do("GET", path, nil)
}

// Get fetches path (with optional query params) and decodes JSON into v.
func (c *Client) Get(path string, params url.Values, v any) error {
	data, err := c.GetRaw(path, params)
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

func ValidateCookie(cookie, userAgent string) (string, error) {
	c := &Client{cookie: cookie, userAgent: userAgent, http: auth.NewHTTPClient(30 * time.Second)}
	c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	data, err := c.request(http.MethodGet, WebBase+"/api/me.json?raw_json=1", nil, "", true)
	if err != nil {
		return "", err
	}
	_, name, err := cookieIdentity(data)
	return name, err
}

func cookieIdentity(data []byte) ([]byte, string, error) {
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &out) != nil {
		return nil, "", errCookieExpired
	}
	var me struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(out.Data, &me) != nil || me.Name == "" {
		return nil, "", errCookieExpired
	}
	return out.Data, me.Name, nil
}

func (c *Client) Whoami() ([]byte, error) {
	if c.cookie == "" {
		if err := c.RequireUser(); err != nil {
			return nil, err
		}
	}
	return c.GetRaw("/api/v1/me", nil)
}
