package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

const (
	// maxAPIKeyNameLen is the column limit of a key name.
	maxAPIKeyNameLen = 255
	// APIKeyTouchInterval is how stale the stored last_used_at of a key may be
	// before a request refreshes it. Every request would otherwise pay for a
	// write; the price of the interval is a last_used_at accurate to a minute.
	APIKeyTouchInterval = 60 * time.Second
)

// ErrInvalidAPIKey is returned for every API key that cannot authenticate its
// caller, whatever the cause. The caller is told one thing only, so a rejected
// key never reveals whether it is unknown, revoked, expired or disabled.
var ErrInvalidAPIKey = errors.New("service: invalid api key")

// ErrAPIKeyRevoked is returned when a caller touches a key whose owner deleted
// it. A deleted key is hidden from the listing and nothing brings it back.
var ErrAPIKeyRevoked = errors.New("service: api key is deleted")

// ErrAPIKeyLapsed is returned when a caller tries to switch on a key whose
// expiry has passed without moving that expiry. Such a key is renewed by
// setting a new expires_at, which is what clears the revocation.
var ErrAPIKeyLapsed = errors.New("service: api key has lapsed")

// errAPIKeyExpired tells the authentication apart from the other rejections so
// it can write the revocation the expiry implies.
var errAPIKeyExpired = errors.New("service: api key has expired")

// CreateAPIKeyInput is the body accepted by POST /api/api-keys.
type CreateAPIKeyInput struct {
	Name      string
	ExpiresAt *time.Time
}

// UpdateAPIKeyInput is a partial update: a nil pointer for a plain column
// leaves the stored value alone, while expires_at is utils.Optional so an
// explicit null makes the key never expire again.
type UpdateAPIKeyInput struct {
	Name      *string
	IsActive  *bool
	ExpiresAt utils.Optional[time.Time]
}

// CreateAPIKey stores a new key for userID and returns it together with its
// plaintext, which is the only time the key is ever readable.
func CreateAPIKey(ctx context.Context, userID uuid.UUID, in CreateAPIKeyInput) (*repository.APIKey, string, error) {
	if err := validateAPIKeyName(in.Name); err != nil {
		return nil, "", err
	}
	if err := validateAPIKeyExpiry(in.ExpiresAt, time.Now().UTC()); err != nil {
		return nil, "", err
	}

	key, hash, err := utils.NewAPIKey([]byte(config.SecretKey))
	if err != nil {
		return nil, "", err
	}

	record := &repository.APIKey{
		ID:        uuid.New(),
		UserID:    userID,
		Name:      in.Name,
		KeyPrefix: utils.APIKeyDisplayPrefix(key),
		KeyHash:   hash,
		IsActive:  true,
		ExpiresAt: in.ExpiresAt,
	}
	if err := repository.CreateAPIKey(ctx, config.DB, record); err != nil {
		return nil, "", err
	}
	return record, key, nil
}

// UpdateAPIKey applies in to one of userID's keys. The key itself cannot be
// changed: it is not stored in a form that could be handed back.
//
// Setting expires_at is also how a lapsed key is renewed. The expiry is what
// made it lapse, so moving the expiry is what undoes it: the revocation is
// cleared and the key is usable again. A key its owner deleted is beyond that,
// and every update to one is refused.
func UpdateAPIKey(ctx context.Context, userID, id uuid.UUID, in UpdateAPIKeyInput) (*repository.APIKey, error) {
	key, err := repository.GetAPIKeyByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}
	if revokedMessage(key) == repository.RevokedReasonUserDeleted {
		return nil, ErrAPIKeyRevoked
	}

	if in.Name != nil {
		if err := validateAPIKeyName(*in.Name); err != nil {
			return nil, err
		}
		key.Name = *in.Name
	}
	if in.ExpiresAt.Set {
		if err := validateAPIKeyExpiry(in.ExpiresAt.Value, time.Now().UTC()); err != nil {
			return nil, err
		}
		if key.RevokedAt != nil {
			key.RevokedAt = nil
			key.RevokedMessage = nil
			key.IsActive = true
		}
		key.ExpiresAt = in.ExpiresAt.Value
	}
	if in.IsActive != nil {
		// Switching on a key that lapsed changes nothing: the expiry that
		// revoked it is still in the past, and the next use would lapse it
		// again. Only a new expires_at renews it.
		if *in.IsActive && key.RevokedAt != nil {
			return nil, ErrAPIKeyLapsed
		}
		key.IsActive = *in.IsActive
	}

	if err := repository.SaveAPIKey(ctx, config.DB, key); err != nil {
		return nil, err
	}
	return key, nil
}

// DeleteAPIKey deletes one of userID's keys. The row stays behind for audit with
// the time and the reason, but the key is hidden from the owner's listing and
// stops authenticating on the next request. Deleting a key that was already
// deleted is a no-op.
func DeleteAPIKey(ctx context.Context, userID, id uuid.UUID) error {
	key, err := repository.GetAPIKeyByID(ctx, config.DB, userID, id)
	if err != nil {
		return err
	}
	if revokedMessage(key) == repository.RevokedReasonUserDeleted {
		return nil
	}

	// A key that lapsed is already revoked. Deleting it now moves the reason to
	// user_deleted, which is what takes it off the listing.
	now := time.Now().UTC()
	reason := repository.RevokedReasonUserDeleted
	key.IsActive = false
	key.RevokedAt = &now
	key.RevokedMessage = &reason
	return repository.SaveAPIKey(ctx, config.DB, key)
}

// ListAPIKeys returns one page of userID's keys, newest first. Lapsed keys are
// revoked first, so the listing never claims a key is usable past its expiry.
func ListAPIKeys(ctx context.Context, userID uuid.UUID, limit, offset int) ([]repository.APIKey, int64, error) {
	if err := repository.RevokeLapsedAPIKeys(ctx, config.DB, userID, time.Now().UTC()); err != nil {
		return nil, 0, err
	}
	return repository.ListAPIKeys(ctx, config.DB, userID, limit, offset)
}

// AuthenticateAPIKey resolves the user behind a presented API key and records
// the use. It is the hook the auth middleware calls whenever a request carries
// a key instead of an access token.
func AuthenticateAPIKey(ctx context.Context, raw string) (*repository.User, error) {
	if !utils.IsAPIKey(raw) {
		return nil, ErrInvalidAPIKey
	}

	hash := utils.HashAPIKey([]byte(config.SecretKey), raw)
	key, err := repository.GetAPIKeyByHash(ctx, config.DB, hash)
	if err != nil {
		if errors.Is(err, repository.ErrAPIKeyNotFound) {
			return nil, ErrInvalidAPIKey
		}
		return nil, err
	}

	user, err := repository.GetUserByID(ctx, config.DB, key.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, ErrInvalidAPIKey
		}
		return nil, err
	}

	now := time.Now().UTC()
	if err := validateAPIKeyUse(key, user, now); err != nil {
		if errors.Is(err, errAPIKeyExpired) {
			// The key is lapsed: write the revocation its expiry implies, so
			// the listing stops showing it as active. Best effort, because the
			// request is rejected either way.
			_ = repository.RevokeLapsedAPIKey(ctx, config.DB, key.ID, now)
		}
		return nil, ErrInvalidAPIKey
	}

	if err := repository.TouchAPIKeyLastUsed(ctx, config.DB, key.ID, now, now.Add(-APIKeyTouchInterval)); err != nil {
		return nil, err
	}
	return user, nil
}

// validateAPIKeyUse reports whether key may authenticate user at now. The three
// guards are independent and all of them apply: the owner must be active, the
// key must be active, and the key must neither be revoked nor have expired.
func validateAPIKeyUse(key *repository.APIKey, user *repository.User, now time.Time) error {
	if !user.IsActive {
		return ErrInvalidAPIKey
	}
	if key.RevokedAt != nil {
		return ErrInvalidAPIKey
	}
	if !key.IsActive {
		return ErrInvalidAPIKey
	}
	if key.ExpiresAt != nil && !now.Before(*key.ExpiresAt) {
		return errAPIKeyExpired
	}
	return nil
}

// revokedMessage returns the recorded reason a key is revoked, or the empty
// string when it is not revoked.
func revokedMessage(key *repository.APIKey) string {
	if key.RevokedMessage == nil {
		return ""
	}
	return *key.RevokedMessage
}

// validateAPIKeyName checks that name is present and fits its column. The name
// is the only label a key has, so unlike a provider it may not be empty.
func validateAPIKeyName(name string) error {
	if name == "" {
		return &ValidationError{Message: "name is required."}
	}
	if len(name) > maxAPIKeyNameLen {
		return &ValidationError{Message: "name is too long."}
	}
	return nil
}

// validateAPIKeyExpiry checks that an expiry is in the future. A nil expiry
// means the key never expires.
func validateAPIKeyExpiry(expiresAt *time.Time, now time.Time) error {
	if expiresAt != nil && !expiresAt.After(now) {
		return &ValidationError{Message: "expires_at must be in the future."}
	}
	return nil
}
