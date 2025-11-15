package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// TokenLength is the length of generated tokens in bytes
	TokenLength = 32
	// TokenExpiry is how long tokens remain valid
	TokenExpiry = 24 * time.Hour
	// CookieName is the name of the auth cookie
	CookieName = "marina_auth_token"
)

// Auth handles authentication for the API
type Auth struct {
	password string
	tokens   map[string]time.Time // token -> expiry time
	mu       sync.RWMutex
}

// New creates a new Auth instance
func New(password string) *Auth {
	a := &Auth{
		password: password,
		tokens:   make(map[string]time.Time),
	}

	// Start cleanup goroutine
	go a.cleanupExpiredTokens()

	return a
}

// IsEnabled returns true if authentication is enabled (password is set)
func (a *Auth) IsEnabled() bool {
	return a.password != ""
}

// ValidatePassword checks if the provided password matches
func (a *Auth) ValidatePassword(password string) bool {
	// Use constant-time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare([]byte(a.password), []byte(password)) == 1
}

// GenerateToken creates a new authentication token
func (a *Auth) GenerateToken() (string, error) {
	bytes := make([]byte, TokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	token := base64.URLEncoding.EncodeToString(bytes)

	a.mu.Lock()
	a.tokens[token] = time.Now().Add(TokenExpiry)
	a.mu.Unlock()

	return token, nil
}

// ValidateToken checks if a token is valid and not expired
func (a *Auth) ValidateToken(token string) bool {
	a.mu.RLock()
	expiry, exists := a.tokens[token]
	a.mu.RUnlock()

	if !exists {
		return false
	}

	return time.Now().Before(expiry)
}

// InvalidateToken removes a token (for logout)
func (a *Auth) InvalidateToken(token string) {
	a.mu.Lock()
	delete(a.tokens, token)
	a.mu.Unlock()
}

// cleanupExpiredTokens periodically removes expired tokens
func (a *Auth) cleanupExpiredTokens() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		a.mu.Lock()
		for token, expiry := range a.tokens {
			if now.After(expiry) {
				delete(a.tokens, token)
			}
		}
		a.mu.Unlock()
	}
}

// Middleware returns an HTTP middleware that requires authentication
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If auth is not enabled, allow all requests
		if !a.IsEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		// Check for token in cookie
		cookie, err := r.Cookie(CookieName)
		if err == nil && a.ValidateToken(cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}

		// Check for token in Authorization header (for API clients and mesh)
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			// Support "Bearer <token>" format
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				if a.ValidateToken(parts[1]) {
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		// No valid authentication found
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json := `{"error": "Authentication required"}`
		w.Write([]byte(json))
	})
}

// GetTokenFromRequest extracts the auth token from a request (cookie or header)
func (a *Auth) GetTokenFromRequest(r *http.Request) string {
	// Try cookie first
	if cookie, err := r.Cookie(CookieName); err == nil {
		return cookie.Value
	}

	// Try Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && parts[0] == "Bearer" {
			return parts[1]
		}
	}

	return ""
}
