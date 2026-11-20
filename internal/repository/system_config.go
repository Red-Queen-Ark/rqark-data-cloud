package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrSystemConfigNotFound is returned when no config row carries the key.
var ErrSystemConfigNotFound = errors.New("repository: system config not found")

// SystemConfig is the system_configs table: one deployment-wide setting per
// row, keyed by the key alone. It is the user_configs table without an owner —
// everything it holds applies to the whole deployment, so key is unique on its
// own instead of per user. value is jsonb so the type of a stored setting
// survives the round trip: a string stays a string, a number stays a number,
// an object stays an object.
type SystemConfig struct {
	ID        uuid.UUID       `gorm:"type:uuid;primaryKey"`
	Key       string          `gorm:"type:varchar(128);not null;uniqueIndex:idx_system_configs_key"`
	Value     json.RawMessage `gorm:"serializer:json;type:jsonb;not null"`
	CreatedAt time.Time       `gorm:"not null"`
	UpdatedAt time.Time       `gorm:"not null"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (SystemConfig) TableName() string { return "system_configs" }

// ListSystemConfigs returns every system config row ordered by key. Like the
// user-owned collections it is deliberately not paginated: a client treats the
// whole set as one config file and reads it in one go. The set is bounded by
// the key grammar and the per-value size cap, not by a page size.
func ListSystemConfigs(ctx context.Context, db *gorm.DB) ([]SystemConfig, error) {
	configs := []SystemConfig{}
	err := db.WithContext(ctx).
		Order("key").Order("id").
		Find(&configs).Error
	if err != nil {
		return nil, err
	}
	return configs, nil
}

// GetSystemConfig loads one config row by key.
func GetSystemConfig(ctx context.Context, db *gorm.DB, key string) (*SystemConfig, error) {
	var config SystemConfig
	if err := db.WithContext(ctx).First(&config, "key = ?", key).Error; err != nil {
		return nil, systemConfigLookupError(err)
	}
	return &config, nil
}

// CreateSystemConfig inserts a config row.
func CreateSystemConfig(ctx context.Context, db *gorm.DB, config *SystemConfig) error {
	return db.WithContext(ctx).Create(config).Error
}

// SaveSystemConfig writes every column of config.
func SaveSystemConfig(ctx context.Context, db *gorm.DB, config *SystemConfig) error {
	return db.WithContext(ctx).Save(config).Error
}

// DeleteSystemConfig removes one config row. A key that does not exist is
// reported as ErrSystemConfigNotFound.
func DeleteSystemConfig(ctx context.Context, db *gorm.DB, key string) error {
	result := db.WithContext(ctx).Delete(&SystemConfig{}, "key = ?", key)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSystemConfigNotFound
	}
	return nil
}

// systemConfigLookupError maps GORM's not-found error onto the repository's own.
func systemConfigLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrSystemConfigNotFound
	}
	return err
}
