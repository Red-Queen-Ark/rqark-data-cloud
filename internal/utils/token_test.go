package utils

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var tokenTestSecret = []byte("token-test-secret")

// TestUserTokenCarriesNoScope covers the compatibility rule: a token that names
// no scope is a user token, which is what every login token already in
// circulation is.
func TestUserTokenCarriesNoScope(t *testing.T) {
	userID := uuid.New()

	raw, err := SignAccessToken(userID, tokenTestSecret, time.Minute)
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}

	claims, err := ParseTokenClaims(raw, tokenTestSecret)
	if err != nil {
		t.Fatalf("ParseTokenClaims: %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("user = %s, want %s", claims.UserID, userID)
	}
	if claims.Scope != ScopeUser {
		t.Errorf("scope = %q, want %q", claims.Scope, ScopeUser)
	}
	if claims.TaskID != uuid.Nil || claims.SessionID != uuid.Nil {
		t.Errorf("a user token carries task %s and session %s, want neither", claims.TaskID, claims.SessionID)
	}
}

// TestRunTokenNamesItsRun covers the three values a run token has to carry: the
// user it acts for, the task it belongs to, and the one session it may touch.
func TestRunTokenNamesItsRun(t *testing.T) {
	userID, taskID, sessionID := uuid.New(), uuid.New(), uuid.New()

	raw, err := SignRunToken(userID, taskID, sessionID, tokenTestSecret, time.Minute)
	if err != nil {
		t.Fatalf("SignRunToken: %v", err)
	}

	claims, err := ParseTokenClaims(raw, tokenTestSecret)
	if err != nil {
		t.Fatalf("ParseTokenClaims: %v", err)
	}
	if claims.Scope != ScopeRun {
		t.Errorf("scope = %q, want %q", claims.Scope, ScopeRun)
	}
	if claims.UserID != userID || claims.TaskID != taskID || claims.SessionID != sessionID {
		t.Errorf("claims = %s/%s/%s, want %s/%s/%s", claims.UserID, claims.TaskID, claims.SessionID, userID, taskID, sessionID)
	}
}

// TestRunTokenRefusesAnUnnamedRun covers the strictness on the run side: a token
// that says it is for a run but names no session could never be allowed onto a
// route, so it is refused at parse time.
func TestRunTokenRefusesAnUnnamedRun(t *testing.T) {
	userID := uuid.New()

	raw, err := sign(tokenClaims{
		RegisteredClaims: lifetime(userID, time.Minute),
		Scope:            ScopeRun,
		TaskID:           uuid.New().String(),
	}, tokenTestSecret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := ParseTokenClaims(raw, tokenTestSecret); err == nil {
		t.Fatal("a run token with no session was accepted")
	}
}

// TestExpiredRunTokenIsRefused covers the lifetime the caller sets: a run token
// past its expiry is no more usable than any other expired token.
func TestExpiredRunTokenIsRefused(t *testing.T) {
	raw, err := SignRunToken(uuid.New(), uuid.New(), uuid.New(), tokenTestSecret, -time.Minute)
	if err != nil {
		t.Fatalf("SignRunToken: %v", err)
	}

	if _, err := ParseTokenClaims(raw, tokenTestSecret); err == nil {
		t.Fatal("an expired run token was accepted")
	}
}

// TestTokenSignedWithAnotherSecretIsRefused covers the signature check, which is
// what keeps a run token from being minted by anyone but the data cloud.
func TestTokenSignedWithAnotherSecretIsRefused(t *testing.T) {
	raw, err := SignRunToken(uuid.New(), uuid.New(), uuid.New(), []byte("another-secret"), time.Minute)
	if err != nil {
		t.Fatalf("SignRunToken: %v", err)
	}

	if _, err := ParseTokenClaims(raw, tokenTestSecret); err == nil {
		t.Fatal("a token signed with another secret was accepted")
	}
}
