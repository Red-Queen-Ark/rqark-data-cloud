package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrAPIKeyNotFound is returned when no API key matches the lookup. It also
// covers a lookup that names another user's key, so key ids cannot be
// enumerated.
var ErrAPIKeyNotFound = errors.New("repository: api key not found")

// The reasons recorded in revoked_message. A key stops working either because
// its owner deleted it or because its expiry passed; the reason is kept so the
// listing can say which, and it decides what the key's owner sees and whether
// the key can come back:
//
//   - user_deleted is final. The key is hidden from its owner's listing and no
//     update brings it back.
//   - expired keeps the key in the listing, and setting a new expires_at renews
//     it: the revocation is cleared and the key authenticates again.
const (
	RevokedReasonUserDeleted = "user_deleted"
	RevokedReasonExpired     = "expired"
)

// APIKey is the api_keys table. Only the hash of a key is stored: the key
// itself is handed to its owner once, when it is created.
//
// Three independent guards decide whether a key still authenticates: is_active
// is the on/off switch its owner controls, expires_at bounds its lifetime, and
// revoked_at marks it as finished for good. A key with a non-nil revoked_at is
// never usable again, and revoked_message says why it got there.
type APIKey struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index"`
	Name      string    `gorm:"type:varchar(255);not null;default:''"`
	KeyPrefix string    `gorm:"type:varchar(16);not null"`
	KeyHash   string    `gorm:"type:varchar(64);not null;uniqueIndex"`

	IsActive   bool       `gorm:"not null;default:true"`
	ExpiresAt  *time.Time `gorm:"type:timestamptz"`
	LastUsedAt *time.Time `gorm:"type:timestamptz"`

	RevokedAt      *time.Time `gorm:"type:timestamptz"`
	RevokedMessage *string    `gorm:"type:varchar(255)"`

	// User only exists to tie user_id to users.id with a foreign key. It is
	// never loaded or written through this struct.
	User User `gorm:"foreignKey:UserID;references:ID"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (APIKey) TableName() string { return "api_keys" }

// GetAPIKeyByHash loads the key behind a presented hash. It is the lookup the
// API key authentication runs on every request that carries a key.
func GetAPIKeyByHash(ctx context.Context, db *gorm.DB, hash string) (*APIKey, error) {
	var key APIKey
	if err := db.WithContext(ctx).First(&key, "key_hash = ?", hash).Error; err != nil {
		return nil, apiKeyLookupError(err)
	}
	return &key, nil
}

// GetAPIKeyByID loads one of userID's keys by primary key. Another user's key
// is reported as ErrAPIKeyNotFound.
func GetAPIKeyByID(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) (*APIKey, error) {
	var key APIKey
	if err := db.WithContext(ctx).First(&key, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, apiKeyLookupError(err)
	}
	return &key, nil
}

// CreateAPIKey inserts a key.
func CreateAPIKey(ctx context.Context, db *gorm.DB, key *APIKey) error {
	return db.WithContext(ctx).Create(key).Error
}

// SaveAPIKey writes every column of key.
func SaveAPIKey(ctx context.Context, db *gorm.DB, key *APIKey) error {
	return db.WithContext(ctx).Save(key).Error
}

// ListAPIKeys returns one page of userID's keys, newest first, together with
// the total number of keys. A key its owner deleted is left out: deleting one is
// how a key is taken off the listing. A key that only lapsed stays, so its owner
// can see that it did and renew it.
func ListAPIKeys(ctx context.Context, db *gorm.DB, userID uuid.UUID, limit, offset int) ([]APIKey, int64, error) {
	base := func() *gorm.DB {
		return db.WithContext(ctx).
			Model(&APIKey{}).
			Where("user_id = ?", userID).
			Where("revoked_message IS DISTINCT FROM ?", RevokedReasonUserDeleted)
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var keys []APIKey
	err := base().Order("created_at DESC").Order("id").Limit(limit).Offset(offset).Find(&keys).Error
	if err != nil {
		return nil, 0, err
	}
	return keys, total, nil
}

// TouchAPIKeyLastUsed records that key was used at usedAt, but only when the
// stored last_used_at is missing or older than cutoff. A key used on every
// request would otherwise write its row on every request; the cutoff keeps the
// column accurate to within one interval instead.
func TouchAPIKeyLastUsed(ctx context.Context, db *gorm.DB, id uuid.UUID, usedAt, cutoff time.Time) error {
	return db.WithContext(ctx).Model(&APIKey{}).
		Where("id = ? AND (last_used_at IS NULL OR last_used_at < ?)", id, cutoff).
		Update("last_used_at", usedAt).Error
}

// RevokeLapsedAPIKey writes the revocation of one key whose expiry has passed
// and that has not been revoked yet. A key that has never been presented again
// after its expiry keeps a null revoked_at until something asks for it.
func RevokeLapsedAPIKey(ctx context.Context, db *gorm.DB, id uuid.UUID, now time.Time) error {
	return revokeLapsed(ctx, db, now).Where("id = ?", id).Error
}

// RevokeLapsedAPIKeys writes the revocation of every lapsed key of userID, so a
// listing never shows a key as active past its expiry.
func RevokeLapsedAPIKeys(ctx context.Context, db *gorm.DB, userID uuid.UUID, now time.Time) error {
	return revokeLapsed(ctx, db, now).Where("user_id = ?", userID).Error
}

// revokeLapsed is the update shared by the two revocation helpers: every key
// that carries an expiry in the past and no revocation yet.
func revokeLapsed(ctx context.Context, db *gorm.DB, now time.Time) *gorm.DB {
	reason := RevokedReasonExpired
	return db.WithContext(ctx).Model(&APIKey{}).
		Where("revoked_at IS NULL AND expires_at IS NOT NULL AND expires_at <= ?", now).
		Updates(map[string]any{
			"is_active":       false,
			"revoked_at":      now,
			"revoked_message": reason,
		})
}

// apiKeyLookupError maps GORM's not-found error onto the repository's own.
func apiKeyLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrAPIKeyNotFound
	}
	return err
}
