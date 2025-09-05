package auth

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

// Config defines validation settings for OAuth2-style Bearer tokens (JWT).
type Config struct {
	Enabled     bool
	Issuer      string
	Audience    string
	HS256Secret string // optional symmetric secret
	JWKSURL     string // optional JWKS URL (RS256 / ES256 etc.)
	JWKSRefresh time.Duration
}

// Validator performs token validation.
type Validator struct {
	cfg       Config
	mu        sync.RWMutex
	jwks      *JWKS
	lastFetch time.Time
}

// JWKS minimal representation for key lookup.
type JWKS struct {
	Keys []struct {
		Kty string   `json:"kty"`
		Kid string   `json:"kid"`
		Alg string   `json:"alg"`
		Use string   `json:"use"`
		N   string   `json:"n"`
		E   string   `json:"e"`
		Crv string   `json:"crv"`
		X   string   `json:"x"`
		Y   string   `json:"y"`
		X5c []string `json:"x5c"`
	} `json:"keys"`
}

// New creates a validator.
func New(cfg Config) *Validator { return &Validator{cfg: cfg} }

// Middleware wraps an http.Handler enforcing auth (except health path allowed). If disabled, passes through.
func (v *Validator) Middleware(next http.Handler) http.Handler {
	if v == nil || !v.cfg.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/health") || strings.HasPrefix(r.URL.Path, "/api/v1/health") {
			next.ServeHTTP(w, r)
			return
		}
		authz := r.Header.Get("Authorization")
		if !strings.HasPrefix(strings.ToLower(authz), "bearer ") {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		token := strings.TrimSpace(authz[7:])
		if _, err := v.Validate(token); err != nil {
			http.Error(w, fmt.Sprintf("invalid token: %v", err), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Validate parses & validates a JWT.
func (v *Validator) Validate(tok string) (*jwt.RegisteredClaims, error) {
	if !v.cfg.Enabled {
		return &jwt.RegisteredClaims{}, nil
	}
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"HS256", "RS256", "ES256"}))
	claims := &jwt.RegisteredClaims{}
	keyFunc := func(t *jwt.Token) (interface{}, error) {
		if v.cfg.HS256Secret != "" && t.Method.Alg() == "HS256" {
			sum := sha256.Sum256([]byte(v.cfg.HS256Secret)) // derive stable key length
			return sum[:], nil
		}
		if v.cfg.JWKSURL != "" {
			return v.lookupKey(t.Header["kid"].(string))
		}
		return nil, errors.New("no key material configured")
	}
	token, err := parser.ParseWithClaims(tok, claims, keyFunc)
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token invalid")
	}
	if v.cfg.Issuer != "" && claims.Issuer != v.cfg.Issuer {
		return nil, errors.New("issuer mismatch")
	}
	if v.cfg.Audience != "" {
		okAud := false
		for _, a := range claims.Audience {
			if a == v.cfg.Audience {
				okAud = true
				break
			}
		}
		if !okAud {
			return nil, errors.New("audience mismatch")
		}
	}
	if claims.ExpiresAt != nil && time.Until(claims.ExpiresAt.Time) <= 0 {
		return nil, errors.New("token expired")
	}
	return claims, nil
}

func (v *Validator) lookupKey(kid string) (interface{}, error) {
	if v.cfg.JWKSURL == "" {
		return nil, errors.New("jwks not configured")
	}
	// refresh if stale
	if time.Since(v.lastFetch) > v.cfg.JWKSRefresh {
		v.fetchJWKS()
	}
	v.mu.RLock()
	jw := v.jwks
	v.mu.RUnlock()
	if jw == nil {
		return nil, errors.New("jwks empty")
	}
	for _, k := range jw.Keys {
		if k.Kid == kid {
			return nil, errors.New("unsupported key type (PEM parse not implemented yet)")
		}
	}
	return nil, errors.New("kid not found")
}

func (v *Validator) fetchJWKS() {
	resp, err := http.Get(v.cfg.JWKSURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var jw JWKS
	if json.Unmarshal(b, &jw) == nil {
		v.mu.Lock()
		v.jwks = &jw
		v.lastFetch = time.Now()
		v.mu.Unlock()
	}
}
