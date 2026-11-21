package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/repository"
)

// defaultUserConfigs are the settings an account holds before it ever stores
// one. They are materialised as real rows the first time the whole set is read,
// so a client sees an explicit value instead of inventing one from a missing key.
//
// Both memory switches are off: the per-turn writer and the state board are
// opt-in. A stored row always wins, so an account that turned either back on
// keeps it.
var defaultUserConfigs = []struct {
	key   string
	value json.RawMessage
}{
	{key: eventLogConfigKey, value: json.RawMessage("false")},
	{key: stateUpdateConfigKey, value: json.RawMessage("false")},
}

const (
	// eventLogConfigKey switches the per-turn event row an agent writes.
	eventLogConfigKey = "memory.event_log"
	// stateUpdateConfigKey switches the per-turn overwrite of the state board.
	stateUpdateConfigKey = "memory.state_update"
)

// ListUserConfigs returns every config row of userID, with the defaults filled
// in for whatever the account has not set. The fill is written once, so later
// reads cost nothing extra and the client receives real ids and timestamps.
func ListUserConfigs(ctx context.Context, userID uuid.UUID) ([]repository.UserConfig, error) {
	configs, err := repository.ListUserConfigs(ctx, config.DB, userID)
	if err != nil {
		return nil, err
	}

	missing := missingUserConfigDefaults(configs, userID)
	if len(missing) == 0 {
		return configs, nil
	}
	if err := repository.CreateUserConfigsIfAbsent(ctx, config.DB, missing); err != nil {
		return nil, err
	}
	return repository.ListUserConfigs(ctx, config.DB, userID)
}

// missingUserConfigDefaults builds the default rows stored still lacks, so one
// insert covers all of them.
func missingUserConfigDefaults(stored []repository.UserConfig, userID uuid.UUID) []repository.UserConfig {
	present := make(map[string]bool, len(stored))
	for i := range stored {
		present[stored[i].Key] = true
	}

	missing := make([]repository.UserConfig, 0, len(defaultUserConfigs))
	for _, entry := range defaultUserConfigs {
		if present[entry.key] {
			continue
		}
		missing = append(missing, repository.UserConfig{
			ID:     uuid.New(),
			UserID: userID,
			Key:    entry.key,
			Value:  entry.value,
		})
	}
	return missing
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
