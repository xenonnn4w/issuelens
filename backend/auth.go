package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwt"
)

type sessionVerifier struct {
	issuer  string
	client  *http.Client
	mu      sync.Mutex
	keys    map[string]*clerk.JSONWebKey
	expires time.Time
}

func newSessionVerifier(issuer string) *sessionVerifier {
	return &sessionVerifier{issuer: issuer, client: &http.Client{Timeout: 5 * time.Second}}
}

func (v *sessionVerifier) verify(ctx context.Context, authorization, origin string) (string, error) {
	if !strings.HasPrefix(authorization, "Bearer ") {
		return "", errors.New("missing bearer token")
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	decoded, err := jwt.Decode(ctx, &jwt.DecodeParams{Token: token})
	if err != nil || decoded.KeyID == "" {
		return "", errors.New("invalid token")
	}
	key, err := v.key(ctx, decoded.KeyID)
	if err != nil {
		return "", err
	}
	claims, err := jwt.Verify(ctx, &jwt.VerifyParams{
		Token:                  token,
		JWK:                    key,
		ProxyURL:               &v.issuer,
		AuthorizedPartyHandler: func(party string) bool { return party == "" || party == origin },
	})
	if err != nil || claims.Subject == "" {
		return "", errors.New("invalid session")
	}
	return claims.Subject, nil
}

func (v *sessionVerifier) key(ctx context.Context, id string) (*clerk.JSONWebKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if key := v.keys[id]; key != nil && time.Now().Before(v.expires) {
		return key, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.issuer+"/.well-known/jwks.json", nil)
	if err != nil {
		return nil, err
	}
	response, err := v.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("JWKS unavailable")
	}
	var set clerk.JSONWebKeySet
	if err := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	v.keys = make(map[string]*clerk.JSONWebKey, len(set.Keys))
	for _, key := range set.Keys {
		if key.Use == "sig" && key.Algorithm == "RS256" {
			v.keys[key.KeyID] = key
		}
	}
	v.expires = time.Now().Add(time.Hour)
	if key := v.keys[id]; key != nil {
		return key, nil
	}
	return nil, errors.New("signing key not found")
}
