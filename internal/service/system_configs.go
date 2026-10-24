package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// ListSystemConfigs returns every config row of the deployment.
func ListSystemConfigs(ctx context.Context) ([]repository.SystemConfig, error) {
	return repository.ListSystemConfigs(ctx, config.DB)
}

// SetSystemConfig upserts one deployment-wide config value. The key grammar and
// the value rules are the ones user_configs uses; only the owner is absent. The
// stored value is the caller's JSON with every U+0000 removed, since jsonb
// cannot hold a NUL.
func SetSystemConfig(ctx context.Context, key string, value json.RawMessage) (*repository.SystemConfig, error) {
	if err := validateConfigKey(key); err != nil {
		return nil, err
	}
	sanitized, err := sanitizeConfigValue(value)
	if err != nil {
		return nil, err
	}

	existing, err := repository.GetSystemConfig(ctx, config.DB, key)
	switch {
	case err == nil:
		existing.Value = sanitized
		if err := repository.SaveSystemConfig(ctx, config.DB, existing); err != nil {
			return nil, err
		}
		return existing, nil
	case errors.Is(err, repository.ErrSystemConfigNotFound):
		created := &repository.SystemConfig{ID: uuid.New(), Key: key, Value: sanitized}
		if err := repository.CreateSystemConfig(ctx, config.DB, created); err != nil {
			return nil, err
		}
		return created, nil
	default:
		return nil, err
	}
}

// DeleteSystemConfig removes one config row of the deployment.
func DeleteSystemConfig(ctx context.Context, key string) error {
	if err := validateConfigKey(key); err != nil {
		return err
	}
	return repository.DeleteSystemConfig(ctx, config.DB, key)
}
