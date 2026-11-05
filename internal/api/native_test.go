package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeSessionAlwaysUsesOAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "reddit-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"native": map[string]string{"cookie": "reddit_session=SYNTHETIC", "user_agent": "NativeTest/1", "account_id": "fake-id"},
		"cookie": "browser=SYNTHETIC", "user_agent": "Browser/1", "username": "fake-user", "access_token": "synthetic-bearer", "expires_at": time.Now().Add(time.Hour),
	})
	if err != nil || os.WriteFile(filepath.Join(dir, "credentials.json"), data, 0o600) != nil {
		t.Fatal("cannot create synthetic session")
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v1/me" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer synthetic-bearer" || r.UserAgent() != "NativeTest/1" {
			t.Error("native request used wrong transport or headers")
		}
		fmt.Fprint(w, `{"id":"fake-id","name":"fake-user"}`)
	}))
	defer server.Close()
	oldAPI, oldWeb, oldSleep := APIBase, WebBase, noSleep
	APIBase, WebBase, noSleep = server.URL, server.URL+"/browser", true
	t.Cleanup(func() { APIBase, WebBase, noSleep = oldAPI, oldWeb, oldSleep })
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequireUser(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Whoami(); err != nil || requests != 1 {
		t.Fatalf("native whoami: requests=%d err=%v", requests, err)
	}
}

func TestNativeIdentityUsesCandidateOnceAndRejectsUnsafeResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		valid  bool
	}{
		{name: "valid", status: 200, body: `{"id":"fake-id","name":"fake-user"}`, valid: true},
		{name: "missing-id", status: 200, body: `{"name":"fake-user"}`},
		{name: "malformed", status: 200, body: "SYNTHETIC_SECRET"},
		{name: "oversized", status: 200, body: strings.Repeat("S", (1<<20)+1)},
		{name: "redirect", status: 302, body: "SYNTHETIC_SECRET"},
		{name: "unauthorized", status: 401, body: "SYNTHETIC_SECRET"},
		{name: "blocked", status: 403, body: "<html>SYNTHETIC_SECRET</html>"},
		{name: "rate-limited", status: 429, body: "SYNTHETIC_SECRET"},
		{name: "server-error", status: 500, body: "SYNTHETIC_SECRET"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls, forwarded int
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded++
			}))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v1/me" || r.Header.Get("Authorization") != "Bearer synthetic-candidate" || r.Header.Get("Cookie") != "" || r.UserAgent() != "NativeTest/1" {
					t.Error("identity did not use candidate directly")
				}
				w.Header().Set("Location", target.URL+"/SYNTHETIC_SECRET")
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			oldAPI, oldSleep := APIBase, noSleep
			APIBase, noSleep = server.URL, true
			t.Cleanup(func() { APIBase, noSleep = oldAPI, oldSleep })
			identity, err := ValidateNativeIdentity("synthetic-candidate", "NativeTest/1")
			if test.valid {
				if err != nil || identity.ID != "fake-id" || identity.Username != "fake-user" {
					t.Fatal("valid identity rejected")
				}
			} else if err == nil || strings.Contains(err.Error(), "SYNTHETIC") || strings.Contains(err.Error(), target.URL) || strings.Contains(err.Error(), "cookie session expired") {
				t.Fatal("unsafe or missing identity error")
			}
			if calls != 1 || forwarded != 0 {
				t.Fatal("identity validation retried or forwarded credentials")
			}
		})
	}
}

func TestNativeCookieOnlyStateIsAuthenticatedAndIncompleteStateFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "reddit-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "credentials.json")
	for _, native := range []string{
		`{"cookie":"reddit_session=SYNTHETIC","user_agent":"NativeTest/1","account_id":"fake-id"}`,
		`{}`, `{"cookie":"reddit_session=SYNTHETIC"}`,
	} {
		if err := os.WriteFile(path, []byte(`{"username":"fake-user","native":`+native+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := New()
		if strings.Contains(native, "account_id") {
			if err != nil || !c.native || c.RequireUser() != nil || c.cookie != "" || c.token == nil {
				t.Fatal("cookie-only native state selected anonymous or browser mode")
			}
		} else if err == nil {
			t.Fatal("incomplete native state silently accepted")
		}
	}
}

func TestNativeBusinessUnauthorizedIsNotReplayed(t *testing.T) {
	noSleep = true
	t.Cleanup(func() { noSleep = false })
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(401)
	}))
	defer server.Close()
	c := &Client{native: true, userAgent: "NativeTest/1", http: &http.Client{Timeout: time.Second}, token: func() (string, error) { return "synthetic", nil }}
	if _, err := c.request(http.MethodGet, server.URL+"/search", nil, "synthetic", false); err == nil || calls != 1 {
		t.Fatal("business API 401 was retried or accepted")
	}
}
