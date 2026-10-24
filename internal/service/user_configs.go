package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// ListUserConfigs returns every config row of userID.
func ListUserConfigs(ctx context.Context, userID uuid.UUID) ([]repository.UserConfig, error) {
	return repository.ListUserConfigs(ctx, config.DB, userID)
}

// SetUserConfig upserts one config value under userID. The stored value is the
// caller's JSON with every U+0000 removed, since jsonb cannot hold a NUL.
func SetUserConfig(ctx context.Context, userID uuid.UUID, key string, value json.RawMessage) (*repository.UserConfig, error) {
	if err := validateConfigKey(key); err != nil {
		return nil, err
	}
	sanitized, err := sanitizeConfigValue(value)
	if err != nil {
		return nil, err
	}

	existing, err := repository.GetUserConfig(ctx, config.DB, userID, key)
	switch {
	case err == nil:
		existing.Value = sanitized
		if err := repository.SaveUserConfig(ctx, config.DB, existing); err != nil {
			return nil, err
		}
		return existing, nil
	case errors.Is(err, repository.ErrUserConfigNotFound):
		created := &repository.UserConfig{ID: uuid.New(), UserID: userID, Key: key, Value: sanitized}
		if err := repository.CreateUserConfig(ctx, config.DB, created); err != nil {
			return nil, err
		}
		return created, nil
	default:
		return nil, err
	}
}

// DeleteUserConfig removes one config row of userID.
func DeleteUserConfig(ctx context.Context, userID uuid.UUID, key string) error {
	if err := validateConfigKey(key); err != nil {
		return err
	}
	return repository.DeleteUserConfig(ctx, config.DB, userID, key)
}
