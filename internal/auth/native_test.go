package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeImportAndCachedToken(t *testing.T) {
	isolatedStore(t)
	legacy := &Session{ClientID: "fake-client", ClientSecret: "synthetic-secret", Username: "old-fake-user", Password: "synthetic-password", Cookie: "browser=SYNTHETIC", UserAgent: "BrowserTest/1", AccessToken: "legacy-token"}
	if err := legacy.save(); err != nil {
		t.Fatal(err)
	}
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"access_token":"synthetic-bearer","expires_in":3600,"expiry_ts":%d}`, time.Now().Add(time.Hour).Unix())
	}))
	defer server.Close()
	previous := tokenHTTP
	tokenHTTP = &http.Client{Transport: nativeTestTransport{server.URL}, CheckRedirect: previous.CheckRedirect, Timeout: previous.Timeout}
	t.Cleanup(func() { tokenHTTP = previous })
	path := filepath.Join(t.TempDir(), "import.json")
	if err := os.WriteFile(path, []byte(`{"cookie":"reddit_session=SYNTHETIC","user_agent":"NativeTest/1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	validated := 0
	validate := func(token, ua string) (Identity, error) {
		validated++
		if token != "synthetic-bearer" || ua != "NativeTest/1" {
			t.Fatal("unexpected identity candidate")
		}
		return Identity{ID: "fake-account", Username: "fake-user"}, nil
	}
	s, err := LoginWithNativeFile(path, validate)
	if err != nil {
		t.Fatal(err)
	}
	if token, err := s.TokenWithValidator(validate); err != nil || token != "synthetic-bearer" || calls != 1 || validated != 1 {
		t.Fatalf("cached native token: calls=%d validated=%d error=%v", calls, validated, err)
	}
	if s.Native == nil || s.Native.AccountID != "fake-account" || s.Cookie != "" || s.Password != "" || s.ClientID != "" || s.ClientSecret != "" || s.UserAgent != "" {
		t.Fatal("native state missing or legacy state leaked")
	}
}

func nativeFixture() *NativeSession {
	return &NativeSession{Cookie: "reddit_session=SYNTHETIC", UserAgent: "NativeTest/1", AccountID: "fake-account"}
}

func nativeSuccess() string {
	return fmt.Sprintf(`{"access_token":"synthetic-bearer","expires_in":3600,"expiry_ts":%d}`, time.Now().Add(time.Hour).Unix())
}

func nativeServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	previous := tokenHTTP
	tokenHTTP = &http.Client{Transport: nativeTestTransport{server.URL}, CheckRedirect: previous.CheckRedirect, Timeout: previous.Timeout}
	t.Cleanup(func() { tokenHTTP = previous })
}

func TestNativeRequestContract(t *testing.T) {
	nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		id, password, ok := r.BasicAuth()
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != `{"scopes":["*"]}` || r.Method != http.MethodPost || r.URL.Path != "/auth/v2/oauth/access-token/session" {
			t.Error("wrong native method, path or body")
		}
		if !ok || id != AppClientID || password != "" || r.Header.Get("Cookie") != "reddit_session=SYNTHETIC" || r.UserAgent() != "NativeTest/1" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("wrong native authentication headers")
		}
		if r.Header.Get("X-Reddit-Loid") != "" || r.Header.Get("X-Attestation-Device-Token") != "" {
			t.Error("unexpected native dependency")
		}
		fmt.Fprint(w, nativeSuccess())
	})
	if tokenHTTP.Timeout != 30*time.Second || tokenHTTP.CheckRedirect == nil {
		t.Fatal("native transport is unbounded or follows redirects")
	}
	if tokenHTTP.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("native token transport follows redirects")
	}
	token, expiry, cookie, err := requestNative(nativeFixture())
	if err != nil || token != "synthetic-bearer" || cookie != "reddit_session=SYNTHETIC" || time.Until(expiry) > time.Hour {
		t.Fatalf("native response failed: %v", err)
	}
}

func TestNativeResponseFailuresAndRotation(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		cookie     string
		status     int
		wantCookie string
	}{
		{name: "unchanged", wantCookie: "reddit_session=SYNTHETIC"},
		{name: "unrelated", cookie: "edgebucket=fake; Path=/", wantCookie: "reddit_session=SYNTHETIC"},
		{name: "rotation", cookie: "reddit_session=ROTATED; Domain=.reddit.com; Path=/auth; Secure; HttpOnly", wantCookie: "reddit_session=ROTATED"},
		{name: "rotation-with-extension", cookie: "reddit_session=ROTATED; Path=/; Priority=High", wantCookie: "reddit_session=ROTATED"},
		{name: "deleted", cookie: "reddit_session=; Max-Age=0; Path=/"},
		{name: "expired-cookie", cookie: "reddit_session=ROTATED; Expires=Thu, 01 Jan 1970 00:00:00 GMT"},
		{name: "wrong-domain", cookie: "reddit_session=ROTATED; Domain=evil.example; Path=/"},
		{name: "wrong-path", cookie: "reddit_session=ROTATED; Path=/unrelated"},
		{name: "invalid-path", cookie: "reddit_session=ROTATED; Path=relative"},
		{name: "invalid-age", cookie: "reddit_session=ROTATED; Max-Age=not-an-age"},
		{name: "bad-json", body: "SYNTHETIC_SECRET"},
		{name: "oversized", body: strings.Repeat("S", nativeResponseLimit+1)},
		{name: "empty-token", body: `{"access_token":"","expires_in":3600,"expiry_ts":9999999999}`},
		{name: "expired", body: `{"access_token":"synthetic","expires_in":3600,"expiry_ts":1}`},
		{name: "millisecond-epoch", body: fmt.Sprintf(`{"access_token":"synthetic","expires_in":3600,"expiry_ts":%d}`, time.Now().Add(time.Hour).UnixMilli())},
		{name: "zero-lifetime", body: fmt.Sprintf(`{"access_token":"synthetic","expires_in":0,"expiry_ts":%d}`, time.Now().Add(time.Hour).Unix())},
		{name: "inconsistent-expiry", body: fmt.Sprintf(`{"access_token":"synthetic","expires_in":3600,"expiry_ts":%d}`, time.Now().Add(24*time.Hour).Unix())},
		{name: "unauthorized", status: 401},
		{name: "blocked", status: 403},
		{name: "rate-limited", status: 429},
		{name: "server-error", status: 500},
		{name: "redirect", status: 302},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if test.cookie != "" {
					w.Header().Add("Set-Cookie", test.cookie)
				}
				w.Header().Set("Location", "https://evil.example/SYNTHETIC_SECRET")
				if test.status != 0 {
					w.WriteHeader(test.status)
				}
				body := test.body
				if body == "" {
					body = nativeSuccess()
				}
				fmt.Fprint(w, body)
			})
			token, _, cookie, err := requestNative(nativeFixture())
			if test.wantCookie == "" {
				if err == nil || token != "" || strings.Contains(err.Error(), "SYNTHETIC") || strings.Contains(err.Error(), "evil.example") {
					t.Fatal("failure returned a token or unsafe error")
				}
			} else if err != nil || cookie != test.wantCookie {
				t.Fatalf("cookie handling failed: %v", err)
			}
			if calls != 1 {
				t.Fatalf("renewal attempted %d times", calls)
			}
		})
	}
}

func TestNativeImportStrictInput(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `[]`, `{"cookie":null,"user_agent":"NativeTest/1"}`,
		`{"cookie":"reddit_session=SYNTHETIC","user_agent":"NativeTest/1","access_token":"SYNTHETIC_SECRET"}`,
		`{"cookie":"reddit_session=SYNTHETIC","cookie":"reddit_session=OTHER","user_agent":"NativeTest/1"}`,
		`{"cookie":"reddit_session=SYNTHETIC; other=bad","user_agent":"NativeTest/1"}`,
		`{"cookie":"reddit_session=SYNTHETIC","user_agent":"NativeTest/1\r\nInjected"}`,
		`{"cookie":"reddit_session=SYNTHETIC","user_agent":"NativeTest/1"} {}`,
		strings.Repeat("SYNTHETIC_SECRET", nativeImportLimit),
	} {
		path := filepath.Join(t.TempDir(), "fixture.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readNativeFile(path); err == nil || strings.Contains(err.Error(), "SYNTHETIC") {
			t.Fatal("invalid import accepted or echoed")
		}
	}
}

func TestNativeImportRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	source, link := filepath.Join(dir, "fixture.json"), filepath.Join(dir, "link")
	if err := os.WriteFile(source, []byte(`{"cookie":"reddit_session=SYNTHETIC","user_agent":"NativeTest/1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readNativeFile(link); err == nil {
		t.Fatal("native import followed a symlink")
	}
}

func TestNativeIdentityFailurePreservesState(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprint(mismatch), func(t *testing.T) {
			path := isolatedStore(t)
			s := &Session{Native: nativeFixture(), Username: "fake-user", AccessToken: "old-token", ExpiresAt: time.Now().Add(-time.Hour)}
			if err := s.save(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Set-Cookie", "reddit_session=ROTATED; Path=/")
				fmt.Fprint(w, nativeSuccess())
			})
			validate := func(token, ua string) (Identity, error) {
				if mismatch {
					return Identity{ID: "other-fake-account", Username: "fake-user"}, nil
				}
				return Identity{}, fmt.Errorf("SYNTHETIC_SECRET")
			}
			if token, err := s.TokenWithValidator(validate); token != "" || err == nil || strings.Contains(err.Error(), "SYNTHETIC") {
				t.Fatal("identity failure exposed token or error detail")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) || s.AccessToken != "old-token" {
				t.Fatal("identity failure changed state")
			}
		})
	}
}

func TestNativeTimeoutDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	})
	tokenHTTP.Timeout = 20 * time.Millisecond
	if token, _, _, err := requestNative(nativeFixture()); token != "" || err == nil {
		t.Fatal("timeout exposed token")
	}
	if calls.Load() != 1 {
		t.Fatal("timeout retried")
	}
}

func TestNativeMissingTokenAndRotationPersist(t *testing.T) {
	path := isolatedStore(t)
	s := &Session{Native: nativeFixture(), Username: "fake-user"}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "edgebucket=fake; Path=/")
		w.Header().Add("Set-Cookie", "reddit_session=ROTATED; Path=/")
		fmt.Fprint(w, nativeSuccess())
	})
	if token, err := s.TokenWithValidator(func(token, ua string) (Identity, error) {
		return Identity{ID: "fake-account", Username: "fake-user"}, nil
	}); token == "" || err != nil {
		t.Fatalf("missing token did not renew: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored Session
	if json.Unmarshal(b, &stored) != nil || stored.AccessToken != "synthetic-bearer" || stored.Native.Cookie != "reddit_session=ROTATED" {
		t.Fatal("renewed token and cookie were not stored together")
	}
}

type nativeTestTransport struct{ target string }

func (t nativeTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != nativeTokenURL || !strings.HasPrefix(t.target, "http://127.0.0.1:") {
		return nil, fmt.Errorf("test transport refuses nonlocal request")
	}
	copy := r.Clone(r.Context())
	u, err := r.URL.Parse(t.target)
	if err != nil {
		return nil, err
	}
	copy.URL.Scheme, copy.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(copy)
}
