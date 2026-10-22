package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yashiels/reddit-cli/internal/api"
	"github.com/yashiels/reddit-cli/internal/auth"

	"github.com/spf13/cobra"
)

func TestValidateArg(t *testing.T) {
	tests := []struct {
		name    string
		argName string
		val     string
		wantErr bool
	}{
		{"empty string", "post-id", "", true},
		{"whitespace only", "post-id", "   ", true},
		{"valid simple", "post-id", "abc123", false},
		{"slash in value", "post-id", "foo/bar", true},
		{"question mark", "post-id", "test?x=1", true},
		{"hash", "post-id", "test#foo", true},
		{"space inside", "subreddit", "foo bar", true},
		{"tab", "post-id", "abc\tdef", true},
		{"newline in value", "post-id", "abc\ndef", true},
		{"carriage return in value", "post-id", "abc\rdef", true},
		{"backslash in value", "post-id", "abc\\def", true},
		{"percent in value", "post-id", "abc%20def", true},
		{"dotdot", "post-id", "..", true},
		{"valid alphanumeric", "post-id", "t3_abc123", false},
		{"valid with underscores", "subreddit", "r_golang", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateArg(tt.argName, tt.val)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateArg(%q, %q) error = %v, wantErr = %v", tt.argName, tt.val, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSort(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		sortVal string
		wantErr bool
	}{
		{"valid", []string{"hot", "new", "top"}, "hot", false},
		{"valid alt", []string{"hot", "new", "top"}, "new", false},
		{"invalid", []string{"hot", "new", "top"}, "blah", true},
		{"empty sort", []string{"hot", "new", "top"}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().String("sort", tt.sortVal, "")
			err := validateSort(cmd, tt.allowed)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSort(%v, %q) error = %v, wantErr = %v", tt.allowed, tt.sortVal, err, tt.wantErr)
			}
		})
	}
}

func TestValidateLimit(t *testing.T) {
	tests := []struct {
		name    string
		limit   int
		wantErr bool
	}{
		{"zero", 0, true},
		{"one", 1, false},
		{"hundred", 100, false},
		{"101", 101, true},
		{"negative", -1, true},
		{"mid range", 50, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().Int("limit", tt.limit, "")
			err := validateLimit(cmd)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateLimit(%d) error = %v, wantErr = %v", tt.limit, err, tt.wantErr)
			}
		})
	}
}

// Command-level tests call RunE directly to avoid Cobra's single-execute limitation.

func TestCommentsCmdEmptyArg(t *testing.T) {
	err := commentsCmd.RunE(commentsCmd, []string{""})
	if err == nil {
		t.Error("expected error for empty comment post-id, got nil")
	}
}

func TestFeedCmdEmptyArg(t *testing.T) {
	err := feedCmd.RunE(feedCmd, []string{""})
	if err == nil {
		t.Error("expected error for empty feed subreddit, got nil")
	}
}

func TestSearchCmdEmptyQuery(t *testing.T) {
	err := searchCmd.RunE(searchCmd, []string{"  "})
	if err == nil {
		t.Error("expected error for whitespace-only search query, got nil")
	}
}

func TestUserCmdEmptyArg(t *testing.T) {
	err := userCmd.RunE(userCmd, []string{""})
	if err == nil {
		t.Error("expected error for empty username, got nil")
	}
}

func TestSubredditCmdEmptyArg(t *testing.T) {
	err := subredditCmd.RunE(subredditCmd, []string{""})
	if err == nil {
		t.Error("expected error for empty subreddit name, got nil")
	}
}

func TestPostsCmdEmptyArg(t *testing.T) {
	err := postsCmd.RunE(postsCmd, []string{""})
	if err == nil {
		t.Error("expected error for empty posts username, got nil")
	}
}

func TestVoteCmdEmptyFullname(t *testing.T) {
	err := voteCmd.RunE(voteCmd, []string{"", "up"})
	if err == nil {
		t.Error("expected error for empty vote fullname, got nil")
	}
}

func TestReplyCmdEmptyText(t *testing.T) {
	err := replyCmd.RunE(replyCmd, []string{"t3_test", ""})
	if err == nil {
		t.Error("expected error for empty reply text, got nil")
	}
}

func TestReplyCmdEmptyFullname(t *testing.T) {
	err := replyCmd.RunE(replyCmd, []string{"", "some text"})
	if err == nil {
		t.Error("expected error for empty reply fullname, got nil")
	}
}

func TestFeedCmdInvalidSort(t *testing.T) {
	origSort, _ := feedCmd.Flags().GetString("sort")
	t.Cleanup(func() { feedCmd.Flags().Set("sort", origSort) })
	f := feedCmd
	f.Flags().Set("sort", "invalid_sort")
	err := f.RunE(f, []string{})
	if err == nil {
		t.Error("expected error for invalid sort, got nil")
	}
}

func TestCommentsCmdInvalidSort(t *testing.T) {
	origSort, _ := commentsCmd.Flags().GetString("sort")
	t.Cleanup(func() { commentsCmd.Flags().Set("sort", origSort) })
	f := commentsCmd
	f.Flags().Set("sort", "invalid_sort")
	err := f.RunE(f, []string{"abc123"})
	if err == nil {
		t.Error("expected error for invalid sort on comments, got nil")
	}
}

func TestPostsCmdInvalidSort(t *testing.T) {
	origSort, _ := postsCmd.Flags().GetString("sort")
	t.Cleanup(func() { postsCmd.Flags().Set("sort", origSort) })
	f := postsCmd
	f.Flags().Set("sort", "invalid_sort")
	err := f.RunE(f, []string{"testuser"})
	if err == nil {
		t.Error("expected error for invalid sort on posts, got nil")
	}
}

func TestSearchCmdInvalidSort(t *testing.T) {
	origSort, _ := searchCmd.Flags().GetString("sort")
	t.Cleanup(func() { searchCmd.Flags().Set("sort", origSort) })
	f := searchCmd
	f.Flags().Set("sort", "invalid_sort")
	err := f.RunE(f, []string{"testquery"})
	if err == nil {
		t.Error("expected error for invalid sort on search, got nil")
	}
}

func TestPermalinkURL(t *testing.T) {
	tests := []struct {
		name      string
		permalink string
		want      string
	}{
		{"typical post", "/r/golang/comments/abc123/some_slug/", "https://www.reddit.com/r/golang/comments/abc123/some_slug/"},
		{"comment permalink", "/r/golang/comments/abc123/some_slug/def456/", "https://www.reddit.com/r/golang/comments/abc123/some_slug/def456/"},
		{"empty stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := permalinkURL(tt.permalink); got != tt.want {
				t.Errorf("permalinkURL(%q) = %q, want %q", tt.permalink, got, tt.want)
			}
		})
	}
}

func TestValidateArgReturnsTrimmed(t *testing.T) {
	val, err := validateArg("test", "  hello  ")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if val != "hello" {
		t.Errorf("expected trimmed 'hello', got %q", val)
	}
}

func TestCookieLoginAndReadCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	originalWeb, originalAPI := api.WebBase, api.APIBase
	rejectIdentity := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "session=FAKE" || r.UserAgent() != "TestBrowser/1" || r.Header.Get("Authorization") != "" {
			t.Error("incorrect cookie transport headers")
		}
		switch r.URL.Path {
		case "/api/me.json":
			if rejectIdentity {
				w.Write([]byte(`{"data":null}`))
				return
			}
			w.Write([]byte(`{"data":{"name":"browser-user","total_karma":42,"link_karma":20,"comment_karma":22}}`))
		case "/comments/abc.json":
			w.Write([]byte(`[{"data":{"children":[{"kind":"t3","data":{"title":"Example post","name":"t3_abc"}}]}},{"data":{"children":[{"kind":"t1","data":{"author":"reader","body":"Example comment"}}]}}]`))
		case "/user/browser-user/about.json":
			w.Write([]byte(`{"data":{"name":"browser-user","total_karma":42}}`))
		case "/r/golang/about.json":
			w.Write([]byte(`{"data":{"display_name":"golang","title":"Go language","subscribers":100}}`))
		case "/search.json", "/r/golang/hot.json", "/user/browser-user/submitted.json":
			w.Write([]byte(`{"data":{"children":[{"kind":"t3","data":{"name":"t3_abc","title":"Example post","subreddit":"golang","author":"reader"}}]}}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	api.WebBase, api.APIBase = server.URL, server.URL+"/unexpected-oauth"
	t.Cleanup(func() { api.WebBase, api.APIBase = originalWeb, originalAPI })
	cookiePath := filepath.Join(home, "browser.txt")
	if err := os.WriteFile(cookiePath, []byte("session=FAKE\nTestBrowser/1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flag := loginCmd.Flags().Lookup("cookie-file")
	originalValue, originalChanged := flag.Value.String(), flag.Changed
	if err := loginCmd.Flags().Set("cookie-file", cookiePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { flag.Value.Set(originalValue); flag.Changed = originalChanged })
	if err := loginCmd.RunE(loginCmd, nil); err == nil {
		t.Fatal("login accepted missing browser identity")
	}
	if stored, err := auth.Load(); err != nil || stored != nil {
		t.Fatalf("failed validation persisted credentials: %v", err)
	}
	rejectIdentity = false
	output := captureOutput(t, func() error { return loginCmd.RunE(loginCmd, nil) })
	if !strings.Contains(output, "browser-user") || strings.Contains(output, "FAKE") {
		t.Fatalf("login output = %q", output)
	}
	for _, tc := range []struct {
		command *cobra.Command
		args    []string
		want    string
	}{
		{whoamiCmd, nil, "u/browser-user"},
		{searchCmd, []string{"go"}, "Example post"},
		{commentsCmd, []string{"abc"}, "Example comment"},
		{feedCmd, []string{"golang"}, "Example post"},
		{userCmd, []string{"browser-user"}, "browser-user"},
		{postsCmd, []string{"browser-user"}, "Example post"},
		{subredditCmd, []string{"golang"}, "golang"},
	} {
		t.Run(tc.command.Name(), func(t *testing.T) {
			output := captureOutput(t, func() error { return tc.command.RunE(tc.command, tc.args) })
			if !strings.Contains(output, tc.want) {
				t.Fatalf("output %q lacks %q", output, tc.want)
			}
		})
	}
	c, err := api.New()
	if err != nil {
		t.Fatal(err)
	}
	if c.RequireUser() == nil {
		t.Fatal("cookie-only login granted OAuth write access")
	}
}

func captureOutput(t *testing.T, run func() error) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = original; reader.Close(); writer.Close() }()
	runErr := run()
	writer.Close()
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	return string(output)
}
