package auth

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func jwtWithPayload(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(payload)) + ".sig"
}

func TestTokenExpiryUsesJWTExpClaim(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	got := tokenExpiry(jwtWithPayload(`{"exp":1790086400.5,"sub":"t2_x"}`), now)
	if want := time.Unix(1_790_086_400, 0); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTokenExpiryFallsBackForOpaqueToken(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	cases := []string{"opaque-token", jwtWithPayload(`{"sub":"t2_x"}`), "a.!!!.c"}
	for _, tok := range cases {
		if got := tokenExpiry(tok, now); !got.Equal(now.Add(fallbackWebTokenLifetime)) {
			t.Fatalf("%q: got %v, want fallback", tok, got)
		}
	}
}

func isolatedStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	path, err := sessionPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCookieStoreRoundTripAndBackCompatibility(t *testing.T) {
	path := isolatedStore(t)
	legacy := &Session{ClientID: "client", ClientSecret: "secret", Username: "oauth-user", Password: "password", AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour).Truncate(time.Second).UTC()}
	if err := legacy.save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil || !reflect.DeepEqual(loaded, legacy) || loaded.Cookie != "" || loaded.UserAgent != "" {
		t.Fatalf("legacy round-trip: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoginWithCookie("session=FAKE", "TestBrowser/1"); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Cookie != "session=FAKE" || loaded.UserAgent != "TestBrowser/1" {
		t.Fatal("cookie fields did not round-trip")
	}
	loaded.Cookie, loaded.UserAgent = "", ""
	if !reflect.DeepEqual(loaded, legacy) {
		t.Fatal("cookie login changed OAuth credentials")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials permissions: %v, %v", info, err)
	}
}

func TestCookieOnlyStore(t *testing.T) {
	isolatedStore(t)
	if _, err := LoginWithCookie("session=FAKE", "TestBrowser/1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil || loaded == nil || loaded.Cookie != "session=FAKE" || loaded.UserAgent != "TestBrowser/1" || loaded.AccessToken != "" {
		t.Fatalf("cookie-only round-trip: %v", err)
	}
}

func TestReadCookieFile(t *testing.T) {
	for _, content := range []string{"session=FAKE\nTestBrowser/1\n", "session=FAKE\r\nTestBrowser/1\r\n", "", "session=FAKE", "\nTestBrowser/1", "session=FAKE\n", "session=FAKE\nBrowser\nextra", "session=FAKE\rInjected\nBrowser"} {
		t.Run(fmt.Sprint(len(content), content), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cookie.txt")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			cookie, ua, err := ReadCookieFile(path)
			valid := content == "session=FAKE\nTestBrowser/1\n" || content == "session=FAKE\r\nTestBrowser/1\r\n"
			if valid {
				if err != nil || cookie != "session=FAKE" || ua != "TestBrowser/1" {
					t.Fatalf("invalid parsed fields: %v", err)
				}
			} else if err == nil || strings.Contains(err.Error(), "FAKE") {
				t.Fatalf("unsafe validation error: %v", err)
			}
		})
	}
}
