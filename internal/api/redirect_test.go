package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHasRedirectProtection(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Skipf("skipping: New() failed: %v", err)
	}
	if c.http.CheckRedirect == nil {
		t.Error("New() did not install CheckRedirect — redirects will be followed")
	}
}

func TestClientRejectsRedirect(t *testing.T) {
	noSleep = true
	t.Cleanup(func() { noSleep = false })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example.com", http.StatusFound)
	}))
	defer srv.Close()

	orig := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = orig })

	c := &Client{
		token: func() (string, error) { return "test", nil },
		http:  &http.Client{},
	}
	c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	err := c.Get("/test", nil, nil)
	if err == nil {
		t.Fatal("expected error for redirect response, got nil")
	}
}

func TestClientRejects3xxStatus(t *testing.T) {
	noSleep = true
	t.Cleanup(func() { noSleep = false })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultipleChoices)
		w.Write([]byte("multiple choices"))
	}))
	defer srv.Close()

	orig := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = orig })

	c := &Client{
		token: func() (string, error) { return "test", nil },
		http:  &http.Client{},
	}
	c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	err := c.Get("/test", nil, nil)
	if err == nil {
		t.Fatal("expected error for 300 response, got nil")
	}
}
