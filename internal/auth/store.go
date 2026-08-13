// Package auth handles Reddit OAuth.
//
// Two modes:
//   - Anonymous (default, zero-config): the official Android app's installed_client
//     grant, using the client_id extracted from the Reddit APK. Read-only, no account.
//   - User: password grant against a *script* app you register at
//     https://www.reddit.com/prefs/apps. Reddit blocks password auth for installed
//     apps, so user actions (vote/reply/whoami) require your own script app.
//
// Mimicking the first-party client is against Reddit's API ToS — personal use only.
package auth

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NewHTTPClient returns a client pinned to HTTP/1.1. Go's HTTP/2 TLS fingerprint
// trips Reddit's Cloudflare bot wall (serves an HTML interstitial instead of JSON);
// HTTP/1.1 passes. timeout==0 means no timeout.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			// empty map disables HTTP/2 upgrade
			TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
		},
	}
}

var tokenHTTP = func() *http.Client {
	c := NewHTTPClient(30 * time.Second)
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	return c
}()

// Pulled from base.apk (com.reddit.frontpage 2026.24.0 / build 2624050):
//
//	res/values/strings.xml  oauth_client_id = ohXpoqrZYub1kg
//	res/values/strings.xml  base_uri_default = https://oauth.reddit.com
//	classes*.dex            UA template: Reddit/Version <v>/Build <b>/Android <api>
const (
	AppClientID    = "ohXpoqrZYub1kg"
	UserAgent      = "Reddit/Version 2026.24.0/Build 2624050/Android 14"
	TokenURL       = "https://www.reddit.com/api/v1/access_token"
	installedGrant = "https://oauth.reddit.com/grants/installed_client"
)

// Session is the on-disk auth state for a logged-in user (script app).
// Password is stored (0600) so the token can refresh silently — the password
// grant returns no refresh_token.
type Session struct {
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	Username     string    `json:"username"`
	Password     string    `json:"password,omitempty"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at"`

	otp string // transient 2FA code; unexported so json never serializes it
}

func sessionPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "reddit-cli", "credentials.json"), nil
}

// Load reads the stored user session, or nil if not logged in (no error).
func Load() (*Session, error) {
	path, err := sessionPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *Session) save() error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func (s *Session) valid() bool {
	return s.AccessToken != "" && time.Now().Before(s.ExpiresAt.Add(-60*time.Second))
}

// Token returns a valid user bearer token, refreshing via the stored password if needed.
func (s *Session) Token() (string, error) {
	if s.valid() {
		return s.AccessToken, nil
	}
	if s.Password == "" {
		return "", fmt.Errorf("token expired and no stored password — run `reddit login`")
	}
	if err := s.refresh(); err != nil {
		return "", err
	}
	if err := s.save(); err != nil {
		return "", err
	}
	return s.AccessToken, nil
}

// Login performs a script-app password grant and persists the session.
func Login(clientID, clientSecret, username, password, otp string) (*Session, error) {
	if clientID == "" {
		return nil, fmt.Errorf("a script-app client id is required (register one at https://www.reddit.com/prefs/apps)")
	}
	s := &Session{ClientID: clientID, ClientSecret: clientSecret, Username: username, Password: password, otp: otp}
	if err := s.refresh(); err != nil {
		return nil, err
	}
	s.otp = ""
	if err := s.save(); err != nil {
		return nil, err
	}
	return s, nil
}

// LoginWithToken stores a bearer token lifted from a logged-in reddit.com
// session (DevTools → Network → Authorization header). No password is stored, so
// there's no auto-refresh — re-paste when it expires (~1h). username is optional
// (cosmetic). This is the no-script-app way to act as a user while mocking the app.
func LoginWithToken(username, token string) (*Session, error) {
	if token == "" {
		return nil, fmt.Errorf("empty access token")
	}
	s := &Session{
		Username:    username,
		AccessToken: strings.TrimPrefix(token, "Bearer "),
		ExpiresAt:   time.Now().Add(55 * time.Minute), // reddit web tokens ~1h
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) refresh() error {
	pass := s.Password
	if s.otp != "" {
		pass = s.Password + ":" + s.otp // Reddit 2FA: password:otp
	}
	form := url.Values{"grant_type": {"password"}, "username": {s.Username}, "password": {pass}}
	tok, exp, err := tokenRequest(s.ClientID, s.ClientSecret, form)
	if err != nil {
		return err
	}
	s.AccessToken, s.ExpiresAt = tok, exp
	return nil
}

// AppToken returns an anonymous (read-only) app token via the installed_client
// grant, cached at app_token.json. deviceID identifies this install; pass a
// stable random string.
func AppToken(deviceID string) (string, error) {
	type cache struct {
		AccessToken string    `json:"access_token"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "reddit-cli", "app_token.json")

	if b, err := os.ReadFile(path); err == nil {
		var c cache
		if json.Unmarshal(b, &c) == nil && c.AccessToken != "" && time.Now().Before(c.ExpiresAt.Add(-60*time.Second)) {
			return c.AccessToken, nil
		}
	}

	form := url.Values{"grant_type": {installedGrant}, "device_id": {deviceID}}
	tok, exp, err := tokenRequest(AppClientID, "", form)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		b, _ := json.MarshalIndent(cache{tok, exp}, "", "  ")
		os.WriteFile(path, b, 0o600)
	}
	return tok, nil
}

// tokenRequest hits the token endpoint with HTTP Basic (clientID:secret) and
// returns the bearer token and its absolute expiry.
func tokenRequest(clientID, clientSecret string, form url.Values) (string, time.Time, error) {
	req, err := http.NewRequest("POST", TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	basic := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
	req.Header.Set("Authorization", "Basic "+basic)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tokenHTTP.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("token endpoint returned %s", resp.Status)
	}

	var out struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
		Error       string  `json:"error"`
		ErrorDesc   string  `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", time.Time{}, fmt.Errorf("token endpoint returned %s", resp.Status)
	}
	if out.AccessToken == "" {
		if out.ErrorDesc != "" {
			return "", time.Time{}, fmt.Errorf("login failed: %s (%s)", out.ErrorDesc, out.Error)
		}
		if out.Error != "" {
			return "", time.Time{}, fmt.Errorf("login failed: %s", out.Error)
		}
		return "", time.Time{}, fmt.Errorf("login failed: %s", resp.Status)
	}
	return out.AccessToken, time.Now().Add(time.Duration(out.ExpiresIn) * time.Second), nil
}
