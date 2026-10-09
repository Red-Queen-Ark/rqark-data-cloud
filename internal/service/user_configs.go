package service

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

const (
	// maxUserConfigKeyLen is the width of the user_configs.key column.
	maxUserConfigKeyLen = 128
	// maxUserConfigValueLen caps one stored config value. It is measured on the
	// encoded JSON text and kept well below the request body limit so a config
	// sync cannot be turned into a bulk upload.
	maxUserConfigValueLen = 64 * 1024
)

// userConfigKeyPattern is the key grammar: URL-safe, so a key can be a path
// segment, and namespaced by dots or dashes when a setting belongs to one
// client.
var userConfigKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ListUserConfigs returns every config row of userID.
func ListUserConfigs(ctx context.Context, userID uuid.UUID) ([]repository.UserConfig, error) {
	return repository.ListUserConfigs(ctx, config.DB, userID)
}

// SetUserConfig upserts one config value under userID. The stored value is the
// caller's JSON with every U+0000 removed, since jsonb cannot hold a NUL.
func SetUserConfig(ctx context.Context, userID uuid.UUID, key string, value json.RawMessage) (*repository.UserConfig, error) {
	if err := validateUserConfigKey(key); err != nil {
		return nil, err
	}
	sanitized, err := sanitizeUserConfigValue(value)
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
	if err := validateUserConfigKey(key); err != nil {
		return err
	}
	return repository.DeleteUserConfig(ctx, config.DB, userID, key)
}

// validateUserConfigKey checks that key is present, fits its column and is made
// of characters that are safe in a path segment.
func validateUserConfigKey(key string) error {
	if key == "" {
		return &ValidationError{Message: "key is required."}
	}
	if len(key) > maxUserConfigKeyLen {
		return &ValidationError{Message: "key is too long."}
	}
	if !userConfigKeyPattern.MatchString(key) {
		return &ValidationError{Message: "key may only contain letters, digits, '_', '.' and '-'."}
	}
	return nil
}

// sanitizeUserConfigValue decodes value, drops every U+0000 and re-encodes it,
// then enforces the size cap. A missing value is rejected so a key is never
// created without one.
func sanitizeUserConfigValue(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 {
		return nil, &ValidationError{Message: "value is required."}
	}

	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, &ValidationError{Message: "value must be valid JSON."}
	}
	if decoded == nil {
		return nil, &ValidationError{Message: "value must not be null."}
	}
	encoded, err := json.Marshal(utils.StripNullChars(decoded))
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxUserConfigValueLen {
		return nil, &ValidationError{Message: "value is too large."}
	}
	return json.RawMessage(encoded), nil
}
