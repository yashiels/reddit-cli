package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCommentsTransport(t *testing.T) {
	raw := ` {"unknown":true,"data":{"postInfoById":{"id":"t3_abc","commentForest":{"trees":[],"pageInfo":{"cursor":"kept"}}}}}`
	var native bool
	var sort string
	var limit int
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer TESTTOKEN" {
			t.Error("incorrect comments auth")
		}
		if native {
			if r.Method != http.MethodPost || r.URL.Path != "/" || r.Header.Get("Content-Type") != "application/json" || r.UserAgent() != "NativeTest/1" {
				t.Error("incorrect GraphQL route or headers")
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			expected := map[string]any{
				"operationName": "PostComments",
				"variables":     map[string]any{"id": "t3_abc", "sortType": strings.ToUpper(sort), "count": float64(limit), "maxDepth": float64(1), "includePremiumAvatarTreatment": false, "includeConsistencyToken": false},
				"extensions":    map[string]any{"persistedQuery": map[string]any{"version": float64(1), "sha256Hash": "314f29c2494fd78c7844ef2c6b09acb4bef5b37ab50b4ddc01691d6d5c8433f8"}},
			}
			if !reflect.DeepEqual(payload, expected) {
				t.Errorf("payload = %s", data)
			}
			fmt.Fprint(w, raw)
		} else {
			if r.Method != http.MethodGet || r.URL.Path != "/comments/abc" || r.URL.Query().Get("sort") != "top" || r.URL.Query().Get("limit") != "3" || r.URL.Query().Get("raw_json") != "1" {
				t.Error("legacy route changed")
			}
			fmt.Fprint(w, `[{"data":{"children":[]}},{"data":{"children":[]}}]`)
		}
	}))
	defer server.Close()
	oldAPI, oldGQL, oldSleep := APIBase, GraphQLBase, noSleep
	t.Cleanup(func() { APIBase, GraphQLBase, noSleep = oldAPI, oldGQL, oldSleep })
	c := testClient(server)
	GraphQLBase = server.URL
	c.userAgent = "NativeTest/1"
	for _, sort = range []string{"confidence", "top", "new", "controversial", "old", "qa"} {
		native, c.native = true, true
		limit = 3
		switch sort {
		case "confidence":
			limit = 1
		case "qa":
			limit = 100
		}
		data, post, err := c.CommentsRaw("t3_abc", sort, limit)
		if err != nil || string(data) != raw || post == nil || post.ID != "t3_abc" {
			t.Fatalf("native comments: %s %v", data, err)
		}
	}
	native, c.native = false, false
	if _, post, err := c.CommentsRaw("abc", "top", 3); err != nil || post != nil {
		t.Fatalf("legacy comments: %v", err)
	}
	if calls != 7 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestNativeCommentsRejectInvalidResponsesWithoutReplay(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		valid  bool
	}{
		{"empty", 200, `{"data":{"postInfoById":{"id":"t3_abc","commentForest":{"trees":[]}}}}`, true},
		{"html", 200, `<html>SYNTHETIC_SECRET</html>`, false},
		{"malformed", 200, `{`, false},
		{"null", 200, `null`, false},
		{"null-post", 200, `{"data":{"postInfoById":null}}`, false},
		{"wrong-id", 200, `{"data":{"postInfoById":{"id":"t3_other","commentForest":{"trees":[]}}}}`, false},
		{"null-forest", 200, `{"data":{"postInfoById":{"id":"t3_abc","commentForest":null}}}`, false},
		{"missing-trees", 200, `{"data":{"postInfoById":{"id":"t3_abc","commentForest":{}}}}`, false},
		{"null-trees", 200, `{"data":{"postInfoById":{"id":"t3_abc","commentForest":{"trees":null}}}}`, false},
		{"wrong-trees", 200, `{"data":{"postInfoById":{"id":"t3_abc","commentForest":{"trees":{}}}}}`, false},
		{"partial-errors", 200, `{"errors":[{"message":"SYNTHETIC_SECRET"}],"data":{"postInfoById":{"id":"t3_abc","commentForest":{"trees":[]}}}}`, false},
		{"unauthorized", 401, `SYNTHETIC_SECRET`, false},
		{"redirect", 307, `SYNTHETIC_SECRET`, false},
		{"rate-limit", 429, `SYNTHETIC_SECRET`, false},
		{"server-error", 500, `SYNTHETIC_SECRET`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls, forwarded, tokens := 0, 0, 0
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded++ }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", target.URL+"/SYNTHETIC_SECRET")
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			oldAPI, oldGQL, oldSleep := APIBase, GraphQLBase, noSleep
			t.Cleanup(func() { APIBase, GraphQLBase, noSleep = oldAPI, oldGQL, oldSleep })
			c := testClient(server)
			c.native = true
			c.token = func() (string, error) { tokens++; return "synthetic", nil }
			c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
			GraphQLBase = server.URL
			_, _, err := c.CommentsRaw("abc", "top", 3)
			if test.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || strings.Contains(err.Error(), "SYNTHETIC") || strings.Contains(err.Error(), target.URL) {
				t.Fatalf("unsafe or missing error: %v", err)
			}
			if calls != 1 || forwarded != 0 || tokens != 1 {
				t.Fatalf("calls=%d forwarded=%d tokens=%d", calls, forwarded, tokens)
			}
		})
	}
}
