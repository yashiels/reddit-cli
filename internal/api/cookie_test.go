package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yashiels/reddit-cli/internal/auth"
)

func cookieTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	originalWeb, originalAPI, originalSleep := WebBase, APIBase, noSleep
	WebBase, APIBase, noSleep = server.URL+"/web", server.URL+"/oauth", true
	t.Cleanup(func() { server.Close(); WebBase, APIBase, noSleep = originalWeb, originalAPI, originalSleep })
	client := &Client{cookie: "session=FAKE", userAgent: "TestBrowser/1", http: auth.NewHTTPClient(time.Second), authed: true, token: func() (string, error) { return "TESTTOKEN", nil }}
	client.http.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

func TestCookieReadPathsAndHeaders(t *testing.T) {
	cases := []struct{ path, want, response string }{
		{"/search?q=go", "/search.json", `{"data":{"children":[]}}`},
		{"/comments/abc", "/comments/abc.json", `[{"data":{"children":[]}}]`},
		{"/r/golang/hot", "/r/golang/hot.json", `{"data":{"children":[]}}`},
		{"/hot", "/hot.json", `{"data":{"children":[]}}`},
		{"/user/test/about", "/user/test/about.json", `{"data":{"name":"test"}}`},
		{"/user/test/submitted", "/user/test/submitted.json", `{"data":{"children":[]}}`},
		{"/user/test/comments", "/user/test/comments.json", `{"data":{"children":[]}}`},
		{"/r/golang/about", "/r/golang/about.json", `{"data":{"display_name":"golang"}}`},
		{"/r/golang/hot/?after=t3_a", "/r/golang/hot.json", `{"data":{"children":[]}}`},
		{"/search.json?q=go", "/search.json", `{"data":{"children":[]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			client := cookieTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/web"+tc.want {
					t.Errorf("path = %s", r.URL.Path)
				}
				if r.Header.Get("Cookie") != "session=FAKE" || r.UserAgent() != "TestBrowser/1" || r.Header.Get("Authorization") != "" {
					t.Error("incorrect browser headers")
				}
				if r.URL.Query().Get("raw_json") != "1" || r.URL.Query().Get("limit") != "5" {
					t.Errorf("query = %v", r.URL.Query())
				}
				original, _ := url.Parse(tc.path)
				for key := range original.Query() {
					if r.URL.Query().Get(key) != original.Query().Get(key) {
						t.Errorf("lost query %s", key)
					}
				}
				w.Write([]byte(tc.response))
			})
			client.token = func() (string, error) { t.Fatal("cookie read requested OAuth token"); return "", nil }
			got, err := client.GetRaw(tc.path, url.Values{"limit": {"5"}})
			if err != nil || string(got) != tc.response {
				t.Fatalf("got %s, %v", got, err)
			}
		})
	}
}

func TestCookieFallback(t *testing.T) {
	for _, status := range []int{401, 403, 200} {
		for _, outcome := range []string{"success", "blocked", "non-json", "token-error"} {
			t.Run(http.StatusText(status)+"/"+outcome, func(t *testing.T) {
				webCalls, oauthCalls := 0, 0
				client := cookieTestClient(t, func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/web/") {
						webCalls++
						w.WriteHeader(status)
						w.Write([]byte("<html>blocked</html>"))
						return
					}
					oauthCalls++
					if r.URL.Path != "/oauth/search" || r.URL.Query().Get("q") != "go" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer TESTTOKEN" || r.UserAgent() != auth.UserAgent {
						t.Error("incorrect OAuth fallback")
					}
					switch outcome {
					case "blocked":
						w.WriteHeader(403)
					case "non-json":
						w.Write([]byte("blocked"))
					default:
						w.Write([]byte(`{"ok":true}`))
					}
				})
				if outcome == "token-error" {
					client.token = func() (string, error) { return "", errors.New("expired token") }
				}
				data, err := client.GetRaw("/search?q=go", nil)
				if outcome == "success" {
					if err != nil || string(data) != `{"ok":true}` {
						t.Fatalf("got %s, %v", data, err)
					}
				} else if err == nil || err.Error() != "cookie session expired: re-run reddit login --cookie-file" {
					t.Fatalf("error = %v", err)
				}
				wantOAuth := 1
				if outcome == "token-error" {
					wantOAuth = 0
				}
				if webCalls != 1 || oauthCalls != wantOAuth {
					t.Fatalf("requests: web=%d OAuth=%d", webCalls, oauthCalls)
				}
			})
		}
	}
}

func TestCookieWritesUseOAuth(t *testing.T) {
	client := cookieTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/api/vote" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer TESTTOKEN" || r.UserAgent() != auth.UserAgent {
			t.Error("write did not use OAuth exclusively")
		}
		w.Write([]byte(`{}`))
	})
	if err := client.Post("/api/vote", url.Values{"id": {"t3_abc"}, "dir": {"1"}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCookieBackoffAndWhoami(t *testing.T) {
	calls := 0
	client := cookieTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(429)
			return
		}
		if r.URL.Path != "/web/api/me.json" {
			t.Errorf("identity path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"data":{"name":"browser-user","total_karma":42}}`))
	})
	client.authed = false
	client.token = func() (string, error) { t.Fatal("cookie identity requested OAuth"); return "", nil }
	data, err := client.Whoami()
	if err != nil || string(data) != `{"name":"browser-user","total_karma":42}` || calls != 2 {
		t.Fatalf("identity=%s, err=%v, calls=%d", data, err, calls)
	}
	if client.RequireUser() == nil {
		t.Fatal("cookie-only session allowed writes")
	}
}

func TestValidateCookie(t *testing.T) {
	for _, body := range []string{`{"data":{"name":"browser-user"}}`, `{"data":{}}`, `{"data":null}`, `{}`, `<html>blocked</html>`} {
		t.Run(body, func(t *testing.T) {
			cookieTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/web/api/me.json" || r.Header.Get("Cookie") != "session=FAKE" || r.UserAgent() != "TestBrowser/1" || r.Header.Get("Authorization") != "" {
					t.Error("invalid validation request")
				}
				w.Write([]byte(body))
			})
			name, err := ValidateCookie("session=FAKE", "TestBrowser/1")
			if strings.Contains(body, "browser-user") {
				if err != nil || name != "browser-user" {
					t.Fatalf("name=%s err=%v", name, err)
				}
			} else if err == nil {
				t.Fatal("accepted missing identity")
			}
		})
	}
}

func TestCookieWhoamiFallback(t *testing.T) {
	client := cookieTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/web/") {
			w.Write([]byte(`{"data":null}`))
			return
		}
		w.Write([]byte(`{"name":"oauth-user"}`))
	})
	data, err := client.Whoami()
	var me struct {
		Name string `json:"name"`
	}
	if err != nil || json.Unmarshal(data, &me) != nil || me.Name != "oauth-user" {
		t.Fatalf("identity=%s, err=%v", data, err)
	}
}
