package service

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// testDatabase opens the PostgreSQL named by TEST_POSTGRES_DSN and installs it
// as the connection the service layer writes to. A test that needs a database
// is skipped when the variable is unset, so `go test ./...` still passes
// without one.
func testDatabase(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set, skipping the database test")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	if err := db.AutoMigrate(&repository.User{}, &repository.APIKey{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	previousDB, previousSecret := config.DB, config.SecretKey
	config.DB = db
	config.SecretKey = "test-secret"
	t.Cleanup(func() {
		config.DB, config.SecretKey = previousDB, previousSecret
	})
	return db
}

// testKeyOwner inserts a user the tests can hang keys on and removes the user
// and its keys afterwards.
func testKeyOwner(t *testing.T, db *gorm.DB) *repository.User {
	t.Helper()

	id := uuid.New()
	user := &repository.User{
		ID:             id,
		Username:       "keytest-" + id.String(),
		Email:          id.String() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Cleanup(func() {
		db.Where("user_id = ?", id).Delete(&repository.APIKey{})
		db.Delete(&repository.User{}, "id = ?", id)
	})
	return user
}

// newTestKey creates a key through the service and returns the stored row with
// the plaintext that was handed out once.
func newTestKey(t *testing.T, owner *repository.User, expiresAt *time.Time) (*repository.APIKey, string) {
	t.Helper()

	key, plaintext, err := CreateAPIKey(context.Background(), owner.ID, CreateAPIKeyInput{
		Name:      "laptop",
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	return key, plaintext
}

// reload reads a key back from the database.
func reload(t *testing.T, db *gorm.DB, owner *repository.User, id uuid.UUID) *repository.APIKey {
	t.Helper()

	key, err := repository.GetAPIKeyByID(context.Background(), db, owner.ID, id)
	if err != nil {
		t.Fatalf("GetAPIKeyByID: %v", err)
	}
	return key
}

func TestCreateAPIKeyStoresOnlyTheHash(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	future := time.Now().UTC().Add(24 * time.Hour)

	key, plaintext := newTestKey(t, owner, &future)

	if !utils.IsAPIKey(plaintext) {
		t.Errorf("plaintext %q does not carry the key prefix", plaintext)
	}
	if key.KeyHash == plaintext {
		t.Error("the key was stored in clear")
	}
	if key.KeyHash != utils.HashAPIKey([]byte(config.SecretKey), plaintext) {
		t.Error("the stored hash does not match the key that was handed out")
	}

	var stored []repository.APIKey
	if err := db.Where("user_id = ?", owner.ID).Find(&stored).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored %d keys, want 1", len(stored))
	}
	if stored[0].KeyPrefix != utils.APIKeyDisplayPrefix(plaintext) {
		t.Errorf("key_prefix = %q, want %q", stored[0].KeyPrefix, utils.APIKeyDisplayPrefix(plaintext))
	}
}

func TestCreateAPIKeyRejectsBadInput(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	past := time.Now().UTC().Add(-time.Minute)

	cases := []struct {
		name string
		in   CreateAPIKeyInput
	}{
		{name: "empty name", in: CreateAPIKeyInput{Name: ""}},
		{name: "name over the column limit", in: CreateAPIKeyInput{Name: string(make([]byte, maxAPIKeyNameLen+1))}},
		{name: "expiry in the past", in: CreateAPIKeyInput{Name: "laptop", ExpiresAt: &past}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := CreateAPIKey(context.Background(), owner.ID, tc.in); err == nil {
				t.Fatal("CreateAPIKey accepted invalid input")
			}

			var count int64
			if err := db.Model(&repository.APIKey{}).Where("user_id = ?", owner.ID).Count(&count).Error; err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != 0 {
				t.Errorf("stored %d keys for rejected input, want 0", count)
			}
		})
	}
}

func TestAuthenticateAPIKeyAcceptsLiveKey(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	key, plaintext := newTestKey(t, owner, nil)

	user, _, err := AuthenticateAPIKey(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}
	if user.ID != owner.ID {
		t.Errorf("authenticated user = %s, want %s", user.ID, owner.ID)
	}

	if reload(t, db, owner, key.ID).LastUsedAt == nil {
		t.Error("last_used_at was not recorded")
	}
}

func TestAuthenticateAPIKeyRejectsUnknownKey(t *testing.T) {
	db := testDatabase(t)
	testKeyOwner(t, db)

	_, _, err := AuthenticateAPIKey(context.Background(), "rqark-9Zk2Xf1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1p")
	if !errors.Is(err, ErrInvalidAPIKey) {
		t.Errorf("AuthenticateAPIKey = %v, want %v", err, ErrInvalidAPIKey)
	}
}

func TestAuthenticateAPIKeyRejectsDisabledKey(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	key, plaintext := newTestKey(t, owner, nil)

	disabled := false
	if _, err := UpdateAPIKey(context.Background(), owner.ID, key.ID, UpdateAPIKeyInput{IsActive: &disabled}); err != nil {
		t.Fatalf("UpdateAPIKey: %v", err)
	}

	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); !errors.Is(err, ErrInvalidAPIKey) {
		t.Errorf("AuthenticateAPIKey = %v, want %v", err, ErrInvalidAPIKey)
	}

	// Disabling is a switch, not a revocation: the key can be turned back on.
	stored := reload(t, db, owner, key.ID)
	if stored.RevokedAt != nil {
		t.Errorf("revoked_at = %v after a mere disable, want nil", stored.RevokedAt)
	}

	enabled := true
	if _, err := UpdateAPIKey(context.Background(), owner.ID, key.ID, UpdateAPIKeyInput{IsActive: &enabled}); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); err != nil {
		t.Errorf("AuthenticateAPIKey after re-enable: %v", err)
	}
}

func TestDeleteAPIKeyRevokesForGood(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	key, plaintext := newTestKey(t, owner, nil)

	if err := DeleteAPIKey(context.Background(), owner.ID, key.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}

	stored := reload(t, db, owner, key.ID)
	if stored.RevokedAt == nil {
		t.Fatal("revoked_at was not written")
	}
	if stored.RevokedMessage == nil || *stored.RevokedMessage != repository.RevokedReasonUserDeleted {
		t.Errorf("revoked_message = %v, want %q", stored.RevokedMessage, repository.RevokedReasonUserDeleted)
	}
	if stored.IsActive {
		t.Error("a revoked key is still active")
	}

	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); !errors.Is(err, ErrInvalidAPIKey) {
		t.Errorf("AuthenticateAPIKey = %v, want %v", err, ErrInvalidAPIKey)
	}

	// Revoking twice is a no-op, and it keeps the first reason and time.
	first := *stored.RevokedAt
	if err := DeleteAPIKey(context.Background(), owner.ID, key.ID); err != nil {
		t.Fatalf("second DeleteAPIKey: %v", err)
	}
	if again := reload(t, db, owner, key.ID); !again.RevokedAt.Equal(first) {
		t.Errorf("revoked_at moved from %v to %v", first, again.RevokedAt)
	}

	// A deleted key stays deleted: no update brings it back, whichever field
	// the body carries.
	enabled := true
	future := time.Now().UTC().Add(24 * time.Hour)
	cases := []struct {
		name string
		in   UpdateAPIKeyInput
	}{
		{name: "activate", in: UpdateAPIKeyInput{IsActive: &enabled}},
		{name: "renew", in: UpdateAPIKeyInput{ExpiresAt: utils.Optional[time.Time]{Set: true, Value: &future}}},
		{name: "rename", in: UpdateAPIKeyInput{Name: ptr("brought back")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := UpdateAPIKey(context.Background(), owner.ID, key.ID, tc.in); !errors.Is(err, ErrAPIKeyRevoked) {
				t.Errorf("UpdateAPIKey on a deleted key = %v, want %v", err, ErrAPIKeyRevoked)
			}
		})
	}
	if again := reload(t, db, owner, key.ID); again.RevokedAt == nil {
		t.Error("a refused update cleared the revocation")
	} else if again.IsActive {
		t.Error("a refused update switched the key back on")
	}
}

// TestDeleteAPIKeyTakesTheKeyOffTheListing covers the two halves of the owner's
// view: a key they deleted is gone from it, and a key that merely lapsed stays.
func TestDeleteAPIKeyTakesTheKeyOffTheListing(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)

	kept, _ := newTestKey(t, owner, nil)
	deleted, _ := newTestKey(t, owner, nil)

	if _, total, err := ListAPIKeys(context.Background(), owner.ID, 10, 0); err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	} else if total != 2 {
		t.Fatalf("total = %d, want 2 before the delete", total)
	}

	if err := DeleteAPIKey(context.Background(), owner.ID, deleted.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}

	keys, total, err := ListAPIKeys(context.Background(), owner.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 after the delete", total)
	}
	for _, key := range keys {
		if key.ID == deleted.ID {
			t.Error("a deleted key is still listed")
		}
	}
	if len(keys) != 1 || keys[0].ID != kept.ID {
		t.Errorf("listing = %v, want only the key that was kept", keys)
	}
}

// TestListAPIKeysKeepsALapsedKey covers the other half of the view: a key that
// expired is still there to be seen and renewed.
func TestListAPIKeysKeepsALapsedKey(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	key, _ := newTestKey(t, owner, nil)

	lapsed := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&repository.APIKey{}).Where("id = ?", key.ID).Update("expires_at", lapsed).Error; err != nil {
		t.Fatalf("lapse key: %v", err)
	}

	keys, total, err := ListAPIKeys(context.Background(), owner.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if total != 1 || len(keys) != 1 || keys[0].ID != key.ID {
		t.Fatalf("listing = %v (total %d), want the lapsed key to stay", keys, total)
	}
	if keys[0].RevokedMessage == nil || *keys[0].RevokedMessage != repository.RevokedReasonExpired {
		t.Errorf("revoked_message = %v, want %q", keys[0].RevokedMessage, repository.RevokedReasonExpired)
	}
	if keys[0].RevokedAt == nil {
		t.Error("the listing did not record the lapse")
	}
}

// TestUpdateAPIKeyRenewsALapsedKey covers the renewal: the expiry is what made
// the key lapse, so moving the expiry is what brings it back.
func TestUpdateAPIKeyRenewsALapsedKey(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)

	for _, tc := range []struct {
		name      string
		expiresAt utils.Optional[time.Time]
	}{
		{name: "a new expiry", expiresAt: utils.Optional[time.Time]{Set: true, Value: ptr(time.Now().UTC().Add(24 * time.Hour))}},
		{name: "no expiry at all", expiresAt: utils.Optional[time.Time]{Set: true, Value: nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, plaintext := newTestKey(t, owner, nil)

			lapsed := time.Now().UTC().Add(-time.Minute)
			if err := db.Model(&repository.APIKey{}).Where("id = ?", key.ID).Update("expires_at", lapsed).Error; err != nil {
				t.Fatalf("lapse key: %v", err)
			}
			if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); !errors.Is(err, ErrInvalidAPIKey) {
				t.Fatalf("AuthenticateAPIKey on the lapsed key = %v, want %v", err, ErrInvalidAPIKey)
			}
			if reload(t, db, owner, key.ID).RevokedAt == nil {
				t.Fatal("the lapse was not written")
			}

			renewed, err := UpdateAPIKey(context.Background(), owner.ID, key.ID, UpdateAPIKeyInput{ExpiresAt: tc.expiresAt})
			if err != nil {
				t.Fatalf("UpdateAPIKey: %v", err)
			}
			if renewed.RevokedAt != nil || renewed.RevokedMessage != nil {
				t.Errorf("revocation survived the renewal: at %v, message %v", renewed.RevokedAt, renewed.RevokedMessage)
			}
			if !renewed.IsActive {
				t.Error("a renewed key is not active")
			}

			stored := reload(t, db, owner, key.ID)
			if stored.RevokedAt != nil || stored.RevokedMessage != nil || !stored.IsActive {
				t.Errorf("stored key after the renewal = revoked_at %v, message %v, is_active %v", stored.RevokedAt, stored.RevokedMessage, stored.IsActive)
			}
			if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); err != nil {
				t.Errorf("AuthenticateAPIKey after the renewal: %v", err)
			}
		})
	}
}

// TestUpdateAPIKeyRefusesToSwitchALapsedKeyOn covers the other way round: an
// expiry that is still in the past cannot be papered over with is_active.
func TestUpdateAPIKeyRefusesToSwitchALapsedKeyOn(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	key, plaintext := newTestKey(t, owner, nil)

	lapsed := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&repository.APIKey{}).Where("id = ?", key.ID).Update("expires_at", lapsed).Error; err != nil {
		t.Fatalf("lapse key: %v", err)
	}
	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("AuthenticateAPIKey on the lapsed key = %v, want %v", err, ErrInvalidAPIKey)
	}

	enabled := true
	if _, err := UpdateAPIKey(context.Background(), owner.ID, key.ID, UpdateAPIKeyInput{IsActive: &enabled}); !errors.Is(err, ErrAPIKeyLapsed) {
		t.Errorf("switching on a lapsed key = %v, want %v", err, ErrAPIKeyLapsed)
	}

	// The request carries both: the renewal applies, and the explicit switch
	// off is applied after it.
	future := time.Now().UTC().Add(24 * time.Hour)
	disabled := false
	renewed, err := UpdateAPIKey(context.Background(), owner.ID, key.ID, UpdateAPIKeyInput{
		IsActive:  &disabled,
		ExpiresAt: utils.Optional[time.Time]{Set: true, Value: &future},
	})
	if err != nil {
		t.Fatalf("renew and switch off: %v", err)
	}
	if renewed.RevokedAt != nil {
		t.Error("the renewal did not clear the revocation")
	}
	if renewed.IsActive {
		t.Error("the explicit switch off was ignored")
	}
	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); !errors.Is(err, ErrInvalidAPIKey) {
		t.Errorf("AuthenticateAPIKey with the key switched off = %v, want %v", err, ErrInvalidAPIKey)
	}
}

// TestDeleteAPIKeyHidesALapsedKey covers deleting a key that had already lapsed:
// the deletion moves it off the listing, so the owner cannot see it any more.
func TestDeleteAPIKeyHidesALapsedKey(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	key, _ := newTestKey(t, owner, nil)

	lapsed := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&repository.APIKey{}).Where("id = ?", key.ID).Update("expires_at", lapsed).Error; err != nil {
		t.Fatalf("lapse key: %v", err)
	}

	if err := DeleteAPIKey(context.Background(), owner.ID, key.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}

	stored := reload(t, db, owner, key.ID)
	if stored.RevokedMessage == nil || *stored.RevokedMessage != repository.RevokedReasonUserDeleted {
		t.Errorf("revoked_message = %v, want %q", stored.RevokedMessage, repository.RevokedReasonUserDeleted)
	}

	if _, total, err := ListAPIKeys(context.Background(), owner.ID, 10, 0); err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	} else if total != 0 {
		t.Errorf("total = %d, want 0 after deleting the lapsed key", total)
	}
}

func TestAuthenticateAPIKeyRejectsDisabledOwner(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	_, plaintext := newTestKey(t, owner, nil)

	if err := db.Model(&repository.User{}).Where("id = ?", owner.ID).Update("is_active", false).Error; err != nil {
		t.Fatalf("disable user: %v", err)
	}

	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); !errors.Is(err, ErrInvalidAPIKey) {
		t.Errorf("AuthenticateAPIKey = %v, want %v", err, ErrInvalidAPIKey)
	}
}

func TestAuthenticateAPIKeyRevokesLapsedKey(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	key, plaintext := newTestKey(t, owner, nil)

	// Move the expiry into the past behind the service's back: the service
	// refuses to store an expiry that has already passed.
	lapsed := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&repository.APIKey{}).Where("id = ?", key.ID).Update("expires_at", lapsed).Error; err != nil {
		t.Fatalf("lapse key: %v", err)
	}
	if reload(t, db, owner, key.ID).RevokedAt != nil {
		t.Fatal("the key was revoked without being used")
	}

	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); !errors.Is(err, ErrInvalidAPIKey) {
		t.Errorf("AuthenticateAPIKey = %v, want %v", err, ErrInvalidAPIKey)
	}

	stored := reload(t, db, owner, key.ID)
	if stored.RevokedAt == nil {
		t.Fatal("the expiry did not write a revocation")
	}
	if stored.RevokedMessage == nil || *stored.RevokedMessage != repository.RevokedReasonExpired {
		t.Errorf("revoked_message = %v, want %q", stored.RevokedMessage, repository.RevokedReasonExpired)
	}
}

func TestAuthenticateAPIKeyRecordsUseAtMostOncePerInterval(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	key, plaintext := newTestKey(t, owner, nil)

	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}

	// A use inside the interval leaves the stored value alone.
	inside := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	if err := db.Model(&repository.APIKey{}).Where("id = ?", key.ID).Update("last_used_at", inside).Error; err != nil {
		t.Fatalf("set last_used_at: %v", err)
	}
	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}
	if got := reload(t, db, owner, key.ID).LastUsedAt; !got.Equal(inside) {
		t.Errorf("last_used_at = %v, want it to stay %v inside the interval", got, inside)
	}

	// A use past the interval refreshes it.
	stale := time.Now().UTC().Add(-2 * APIKeyTouchInterval)
	if err := db.Model(&repository.APIKey{}).Where("id = ?", key.ID).Update("last_used_at", stale).Error; err != nil {
		t.Fatalf("set last_used_at: %v", err)
	}
	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}
	if got := reload(t, db, owner, key.ID).LastUsedAt; !got.After(stale) {
		t.Errorf("last_used_at = %v, want it refreshed past %v", got, stale)
	}
}

func TestUpdateAPIKeyClearsAndSetsExpiry(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	future := time.Now().UTC().Add(24 * time.Hour)
	key, _ := newTestKey(t, owner, &future)

	// An explicit null makes the key never expire again.
	if _, err := UpdateAPIKey(context.Background(), owner.ID, key.ID, UpdateAPIKeyInput{
		ExpiresAt: utils.Optional[time.Time]{Set: true, Value: nil},
	}); err != nil {
		t.Fatalf("clear expiry: %v", err)
	}
	if stored := reload(t, db, owner, key.ID); stored.ExpiresAt != nil {
		t.Errorf("expires_at = %v, want nil", stored.ExpiresAt)
	}

	// An expiry that has already passed is refused.
	past := time.Now().UTC().Add(-time.Minute)
	if _, err := UpdateAPIKey(context.Background(), owner.ID, key.ID, UpdateAPIKeyInput{
		ExpiresAt: utils.Optional[time.Time]{Set: true, Value: &past},
	}); err == nil {
		t.Error("UpdateAPIKey accepted an expiry in the past")
	}

	// An absent expiry leaves the stored value alone.
	later := time.Now().UTC().Add(48 * time.Hour)
	if _, err := UpdateAPIKey(context.Background(), owner.ID, key.ID, UpdateAPIKeyInput{
		ExpiresAt: utils.Optional[time.Time]{Set: true, Value: &later},
	}); err != nil {
		t.Fatalf("set expiry: %v", err)
	}
	if _, err := UpdateAPIKey(context.Background(), owner.ID, key.ID, UpdateAPIKeyInput{}); err != nil {
		t.Fatalf("update nothing: %v", err)
	}
	if stored := reload(t, db, owner, key.ID); stored.ExpiresAt == nil || !stored.ExpiresAt.Equal(later) {
		t.Errorf("expires_at = %v, want %v", stored.ExpiresAt, later)
	}
}

func TestListAPIKeysRevokesLapsedKeysAndPaginates(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	future := time.Now().UTC().Add(24 * time.Hour)

	_, _ = newTestKey(t, owner, nil)
	_, _ = newTestKey(t, owner, &future)
	lapsed, _ := newTestKey(t, owner, nil)

	past := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&repository.APIKey{}).Where("id = ?", lapsed.ID).Update("expires_at", past).Error; err != nil {
		t.Fatalf("lapse key: %v", err)
	}

	keys, total, err := ListAPIKeys(context.Background(), owner.ID, 2, 0)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(keys) != 2 {
		t.Errorf("page holds %d keys, want 2", len(keys))
	}

	// The listing revokes a lapsed key even when the key is on another page.
	stored := reload(t, db, owner, lapsed.ID)
	if stored.RevokedAt == nil {
		t.Error("the lapsed key was not revoked by the listing")
	} else if stored.RevokedMessage == nil || *stored.RevokedMessage != repository.RevokedReasonExpired {
		t.Errorf("revoked_message = %v, want %q", stored.RevokedMessage, repository.RevokedReasonExpired)
	}
	if stored.IsActive {
		t.Error("the lapsed key is still active")
	}

	// Another user's keys are not part of the page.
	other := testKeyOwner(t, db)
	if _, otherTotal, err := ListAPIKeys(context.Background(), other.ID, 10, 0); err != nil {
		t.Fatalf("ListAPIKeys of the other owner: %v", err)
	} else if otherTotal != 0 {
		t.Errorf("the other owner sees %d keys, want 0", otherTotal)
	}
}

func TestAPIKeyOfAnotherOwnerIsNotFound(t *testing.T) {
	db := testDatabase(t)
	owner := testKeyOwner(t, db)
	other := testKeyOwner(t, db)
	key, plaintext := newTestKey(t, owner, nil)

	if _, err := UpdateAPIKey(context.Background(), other.ID, key.ID, UpdateAPIKeyInput{Name: ptr("stolen")}); !errors.Is(err, repository.ErrAPIKeyNotFound) {
		t.Errorf("UpdateAPIKey of another owner = %v, want %v", err, repository.ErrAPIKeyNotFound)
	}
	if err := DeleteAPIKey(context.Background(), other.ID, key.ID); !errors.Is(err, repository.ErrAPIKeyNotFound) {
		t.Errorf("DeleteAPIKey of another owner = %v, want %v", err, repository.ErrAPIKeyNotFound)
	}

	// The key of the real owner is untouched.
	if _, _, err := AuthenticateAPIKey(context.Background(), plaintext); err != nil {
		t.Errorf("AuthenticateAPIKey after the cross-owner attempts: %v", err)
	}
}

// ptr returns a pointer to value, for the partial updates in this file.
func ptr[T any](value T) *T { return &value }
