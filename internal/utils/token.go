package utils

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ErrInvalidToken is returned for any access token that cannot be trusted: bad
// signature, algorithm mismatch, expired, malformed subject or missing claims.
var ErrInvalidToken = errors.New("utils: invalid access token")

// Token scopes. A token that carries no scope claim is a user token: the claim
// was added after login tokens were already in circulation, so its absence has
// to keep meaning what it always meant.
const (
	// ScopeUser is a token answered by the login and refresh flows. It reaches
	// every route the user may reach.
	ScopeUser = "user"
	// ScopeRun is the short-lived token a claimed task run carries. It reaches
	// one session and nothing else; see the auth middleware for the whitelist.
	ScopeRun = "run"
)

// tokenClaims is the wire form of an access token. The registered claims carry
// the identity and the lifetime; the rest is added by the run flow.
type tokenClaims struct {
	jwt.RegisteredClaims
	Scope     string `json:"scope,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// TokenClaims is a parsed access token: who it names, what it may do, and which
// run it belongs to when it is a run token.
type TokenClaims struct {
	UserID uuid.UUID
	// Scope is the scope the token carries, or ScopeUser when it carries none.
	Scope string
	// TaskID and SessionID are set on a run token only.
	TaskID    uuid.UUID
	SessionID uuid.UUID
}

// SignAccessToken returns an HS256 access token whose subject is userID and
// whose lifetime is ttl. It carries no scope, which reads as a user token.
func SignAccessToken(userID uuid.UUID, secret []byte, ttl time.Duration) (string, error) {
	return sign(tokenClaims{
		RegisteredClaims: lifetime(userID, ttl),
	}, secret)
}

// SignRunToken returns the token a claimed task run works with: it names the
// run's task and session, and reaches only that session. ttl is set by the
// caller, which knows how long the run may take; it is deliberately not the
// access token lifetime, which is far longer than one run.
func SignRunToken(userID, taskID, sessionID uuid.UUID, secret []byte, ttl time.Duration) (string, error) {
	return sign(tokenClaims{
		RegisteredClaims: lifetime(userID, ttl),
		Scope:            ScopeRun,
		TaskID:           taskID.String(),
		SessionID:        sessionID.String(),
	}, secret)
}

// lifetime is the registered half of a token, shared by both signers so the two
// cannot drift on the subject or the clock.
func lifetime(userID uuid.UUID, ttl time.Duration) jwt.RegisteredClaims {
	now := time.Now().UTC()
	return jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
}

func sign(claims tokenClaims, secret []byte) (string, error) {
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", err
	}
	return signed, nil
}

// ParseAccessToken verifies the signature, the algorithm and the expiry of raw
// and returns the subject user ID.
func ParseAccessToken(raw string, secret []byte) (uuid.UUID, error) {
	claims, err := ParseTokenClaims(raw, secret)
	if err != nil {
		return uuid.Nil, err
	}
	return claims.UserID, nil
}

// ParseTokenClaims verifies raw and returns everything it names. A token with no
// scope reads as a user token, which is what every token issued before the run
// scope existed is.
func ParseTokenClaims(raw string, secret []byte) (*TokenClaims, error) {
	claims := &tokenClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) {
		return secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidToken
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, ErrInvalidToken
	}

	result := &TokenClaims{UserID: userID, Scope: claims.Scope}
	if result.Scope == "" {
		result.Scope = ScopeUser
	}

	if claims.Scope == ScopeRun {
		// A run token without both ids names no session, so it could only ever
		// be rejected later. Refuse it here instead.
		taskID, err := uuid.Parse(claims.TaskID)
		if err != nil {
			return nil, ErrInvalidToken
		}
		sessionID, err := uuid.Parse(claims.SessionID)
		if err != nil {
			return nil, ErrInvalidToken
		}
		result.TaskID, result.SessionID = taskID, sessionID
	}

	return result, nil
}
