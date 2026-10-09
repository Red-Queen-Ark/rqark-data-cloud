package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrUserConfigNotFound is returned when no config row matches the lookup. It
// also covers a key that belongs to another user, so keys cannot be enumerated
// across accounts.
var ErrUserConfigNotFound = errors.New("repository: user config not found")

// UserConfig is the user_configs table: one setting per row, keyed by
// (user_id, key). The pair is unique, so a key holds at most one value per user.
// value is jsonb so the type of a stored setting survives the round trip: a
// string stays a string, a number stays a number, an object stays an object.
type UserConfig struct {
	ID        uuid.UUID       `gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID       `gorm:"type:uuid;not null;index;uniqueIndex:idx_user_configs_user_id_key,priority:1"`
	Key       string          `gorm:"type:varchar(128);not null;uniqueIndex:idx_user_configs_user_id_key,priority:2"`
	Value     json.RawMessage `gorm:"serializer:json;type:jsonb;not null"`
	CreatedAt time.Time       `gorm:"not null"`
	UpdatedAt time.Time       `gorm:"not null"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (UserConfig) TableName() string { return "user_configs" }

// ListUserConfigs returns every config row of userID ordered by key. Configs are
// not paginated: the app treats them as one local config file and wants the
// whole set at once.
func ListUserConfigs(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]UserConfig, error) {
	configs := []UserConfig{}
	err := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("key").Order("id").
		Find(&configs).Error
	if err != nil {
		return nil, err
	}
	return configs, nil
}

// GetUserConfig loads one config row by (userID, key). A key owned by another
// user is reported as ErrUserConfigNotFound.
func GetUserConfig(ctx context.Context, db *gorm.DB, userID uuid.UUID, key string) (*UserConfig, error) {
	var config UserConfig
	if err := db.WithContext(ctx).First(&config, "user_id = ? AND key = ?", userID, key).Error; err != nil {
		return nil, userConfigLookupError(err)
	}
	return &config, nil
}

// CreateUserConfig inserts a config row.
func CreateUserConfig(ctx context.Context, db *gorm.DB, config *UserConfig) error {
	return db.WithContext(ctx).Create(config).Error
}

// SaveUserConfig writes every column of config.
func SaveUserConfig(ctx context.Context, db *gorm.DB, config *UserConfig) error {
	return db.WithContext(ctx).Save(config).Error
}

// DeleteUserConfig removes one of userID's config rows. A key that does not
// exist is reported as ErrUserConfigNotFound.
func DeleteUserConfig(ctx context.Context, db *gorm.DB, userID uuid.UUID, key string) error {
	result := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&UserConfig{}, "key = ?", key)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrUserConfigNotFound
	}
	return nil
}

// userConfigLookupError maps GORM's not-found error onto the repository's own.
func userConfigLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrUserConfigNotFound
	}
	return err
}
