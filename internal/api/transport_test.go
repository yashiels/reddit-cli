package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yashiels/reddit-cli/internal/auth"
)

// testClient builds a Client pointed at srv with a fixed token and no sleeps.
func testClient(srv *httptest.Server) *Client {
	noSleep = true
	APIBase = srv.URL
	return &Client{
		authed: true,
		token:  func() (string, error) { return "TESTTOKEN", nil },
		http:   auth.NewHTTPClient(5 * time.Second),
	}
}

// A POST (vote/reply) must carry the bearer token, the app User-Agent, and the
// correct form body — this is what the write commands depend on.
func TestPostSendsAuthUAAndForm(t *testing.T) {
	var gotAuth, gotUA, gotID, gotDir string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		r.ParseForm()
		gotID, gotDir = r.PostForm.Get("id"), r.PostForm.Get("dir")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := testClient(srv)
	if err := c.Post("/api/vote", url.Values{"id": {"t3_abc"}, "dir": {"1"}}, nil); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer TESTTOKEN" {
		t.Errorf("auth header = %q, want Bearer TESTTOKEN", gotAuth)
	}
	if gotUA != auth.UserAgent {
		t.Errorf("user-agent = %q, want %q", gotUA, auth.UserAgent)
	}
	if gotID != "t3_abc" || gotDir != "1" {
		t.Errorf("form = id:%q dir:%q, want t3_abc/1", gotID, gotDir)
	}
}

// A 429 must be retried (the anti-ban "shudder"), not surfaced as an error.
func TestRetriesOn429ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := testClient(srv)
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.Get("/api/v1/me", nil, &out); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("server got %d calls, want 2 (1 throttled + 1 retry)", n)
	}
	if !out.OK {
		t.Error("response not decoded after retry")
	}
}
