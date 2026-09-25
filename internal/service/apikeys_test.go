package service

import (
	"errors"
	"testing"
	"time"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

func TestValidateAPIKeyName(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "plain", value: "laptop"},
		{name: "empty", value: "", wantErr: true},
		{name: "at the column limit", value: string(make([]byte, maxAPIKeyNameLen))},
		{name: "over the column limit", value: string(make([]byte, maxAPIKeyNameLen+1)), wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAPIKeyName(tc.value)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateAPIKeyName(%d bytes) = %v, wantErr %v", len(tc.value), err, tc.wantErr)
			}
		})
	}
}

func TestValidateAPIKeyExpiry(t *testing.T) {
	now := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	cases := []struct {
		name      string
		expiresAt *time.Time
		wantErr   bool
	}{
		{name: "never expires", expiresAt: nil},
		{name: "in the future", expiresAt: &future},
		{name: "exactly now", expiresAt: &now, wantErr: true},
		{name: "in the past", expiresAt: &past, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAPIKeyExpiry(tc.expiresAt, now)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateAPIKeyExpiry = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateAPIKeyUse(t *testing.T) {
	now := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	reason := repository.RevokedReasonUserDeleted

	live := func() (*repository.APIKey, *repository.User) {
		return &repository.APIKey{IsActive: true}, &repository.User{IsActive: true}
	}

	cases := []struct {
		name        string
		mutate      func(key *repository.APIKey, user *repository.User)
		wantErr     error
		wantExpired bool
	}{
		{name: "live key"},
		{
			name:   "never expires",
			mutate: func(key *repository.APIKey, _ *repository.User) { key.ExpiresAt = nil },
		},
		{
			name:   "expires in the future",
			mutate: func(key *repository.APIKey, _ *repository.User) { key.ExpiresAt = &future },
		},
		{
			name:    "inactive owner",
			mutate:  func(_ *repository.APIKey, user *repository.User) { user.IsActive = false },
			wantErr: ErrInvalidAPIKey,
		},
		{
			name:    "inactive key",
			mutate:  func(key *repository.APIKey, _ *repository.User) { key.IsActive = false },
			wantErr: ErrInvalidAPIKey,
		},
		{
			name:    "revoked key",
			mutate:  func(key *repository.APIKey, _ *repository.User) { key.RevokedAt = &past; key.RevokedMessage = &reason },
			wantErr: ErrInvalidAPIKey,
		},
		{
			name:        "expired key",
			mutate:      func(key *repository.APIKey, _ *repository.User) { key.ExpiresAt = &past },
			wantErr:     errAPIKeyExpired,
			wantExpired: true,
		},
		{
			name:        "expiring right now",
			mutate:      func(key *repository.APIKey, _ *repository.User) { key.ExpiresAt = &now },
			wantErr:     errAPIKeyExpired,
			wantExpired: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, user := live()
			if tc.mutate != nil {
				tc.mutate(key, user)
			}

			err := validateAPIKeyUse(key, user, now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("validateAPIKeyUse = %v, want %v", err, tc.wantErr)
			}
			if got := errors.Is(err, errAPIKeyExpired); got != tc.wantExpired {
				t.Errorf("lapsed = %v, want %v", got, tc.wantExpired)
			}
		})
	}
}
