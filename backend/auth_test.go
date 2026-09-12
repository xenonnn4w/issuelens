package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v3"
	jwtlib "github.com/go-jose/go-jose/v3/jwt"
)

func TestSessionVerifier(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	public := jose.JSONWebKey{Key: &private.PublicKey, KeyID: "test-key", Algorithm: string(jose.RS256), Use: "sig"}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{public}})
	}))
	defer provider.Close()
	verifier := newSessionVerifier(provider.URL)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: private}, (&jose.SignerOptions{}).WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	sign := func(issuer, party string, expiry time.Time) string {
		t.Helper()
		claims := jwtlib.Claims{Issuer: issuer, Subject: "user-a", Expiry: jwtlib.NewNumericDate(expiry), IssuedAt: jwtlib.NewNumericDate(time.Now().Add(-time.Minute))}
		token, err := jwtlib.Signed(signer).Claims(claims).Claims(map[string]any{"azp": party}).CompactSerialize()
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	valid := sign(provider.URL, "https://issuelens.me", time.Now().Add(time.Hour))
	if id, err := verifier.verify(context.Background(), "Bearer "+valid, "https://issuelens.me"); err != nil || id != "user-a" {
		t.Fatalf("valid session: id=%q err=%v", id, err)
	}
	for _, token := range []string{
		sign(provider.URL, "https://other.example", time.Now().Add(time.Hour)),
		sign("https://other.example", "https://issuelens.me", time.Now().Add(time.Hour)),
		sign(provider.URL, "https://issuelens.me", time.Now().Add(-time.Hour)),
	} {
		if _, err := verifier.verify(context.Background(), "Bearer "+token, "https://issuelens.me"); err == nil {
			t.Fatal("invalid session accepted")
		}
	}
	if _, err := verifier.verify(context.Background(), "", "https://issuelens.me"); err == nil {
		t.Fatal("missing token accepted")
	}
}
