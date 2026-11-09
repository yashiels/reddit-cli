package cmd

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

	"github.com/yashiels/reddit-cli/internal/api"
)

func TestNativeSessionFlagExcludesCredentialFlags(t *testing.T) {
	if loginCmd.Flags().Lookup("native-session-file") == nil {
		t.Fatal("native session import flag is missing")
	}
	for _, other := range []string{"cookie-file", "access-token", "client-id", "client-secret", "user", "password", "otp"} {
		t.Run(other, func(t *testing.T) {
			native := loginCmd.Flags().Lookup("native-session-file")
			flag := loginCmd.Flags().Lookup(other)
			native.Changed, flag.Changed = true, true
			t.Cleanup(func() { native.Changed, flag.Changed = false, false })
			if err := loginCmd.ValidateFlagGroups(); err == nil || !strings.Contains(err.Error(), "native-session-file") {
				t.Fatal("native import accepted conflicting credentials")
			}
		})
	}
}

func TestNativeCommentsCommandRawRouting(t *testing.T) {
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
	session, err := json.Marshal(map[string]any{
		"native":   map[string]string{"cookie": "reddit_session=SYNTHETIC", "user_agent": "NativeTest/1", "account_id": "fake-id"},
		"username": "fake-user", "access_token": "synthetic-bearer", "expires_at": time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), session, 0o600); err != nil {
		t.Fatal(err)
	}
	raw := ` {"unknown":"preserved", "data":{"postInfoById":{"id":"t3_abc","title":"Example post","subreddit":{"name":"example"},"commentCount":3,"commentForest":{"trees":[],"pageInfo":{"cursor":"kept"}}}}}` + "\n"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			t.Errorf("native comments route = %s %s, want POST /", r.Method, r.URL.Path)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer synthetic-bearer" || r.UserAgent() != "NativeTest/1" {
			t.Error("incorrect native headers")
		}
		fmt.Fprint(w, raw)
	}))
	defer server.Close()
	oldAPI, oldGQL, oldJSON := api.APIBase, api.GraphQLBase, jsonOut
	api.APIBase, api.GraphQLBase, jsonOut = server.URL, server.URL, true
	t.Cleanup(func() { api.APIBase, api.GraphQLBase, jsonOut = oldAPI, oldGQL, oldJSON })
	output := captureOutput(t, func() error { return commentsCmd.RunE(commentsCmd, []string{"t3_abc"}) })
	if output != raw || calls != 1 {
		t.Fatalf("raw output = %q, calls = %d", output, calls)
	}
}

func TestNativeCommentsHumanFiltering(t *testing.T) {
	var post api.NativeCommentsPost
	data := `{"id":"t3_abc","title":"Example post","subreddit":{"name":"example"},"commentCount":71,"commentForest":{"trees":[
 {"depth":0,"node":null,"more":{"cursor":"hidden"}},
 {"depth":1,"node":{"content":{"markdown":"nested-hidden"}}},
 {"depth":0,"node":{"__typename":"Comment","authorInfo":{"name":"reader"},"score":12,"content":{"markdown":"First comment"},"permalink":"/r/example/comments/abc/title/one/"}},
 {"depth":0,"node":{"__typename":"DeletedComment","createdAt":"2026-01-01"}},
 {"depth":0,"node":{"__typename":"Comment","score":0,"content":{"markdown":"Missing author"}}},
 {"depth":0,"node":{"__typename":"Comment","authorInfo":{"name":"reader"}}},
 {"depth":0,"node":{"content":{"markdown":"over-limit-hidden"}}}
 ]}}`
	if err := json.Unmarshal([]byte(data), &post); err != nil {
		t.Fatal(err)
	}
	output := captureOutput(t, func() error { printNativeComments(&post, 4); return nil })
	expected := "Example post\n(r/example · 71 comments)\n" + strings.Repeat("-", 40) + "\n" +
		"u/reader [12]: First comment\n    https://www.reddit.com/r/example/comments/abc/title/one/\n" +
		"[deleted]: [deleted]\n[unknown] [0]: Missing author\nu/reader: [unavailable]\n"
	if output != expected {
		t.Fatalf("human output = %q, want %q", output, expected)
	}
}
