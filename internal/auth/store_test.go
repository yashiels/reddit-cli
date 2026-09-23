package auth

import (
	"encoding/base64"
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
