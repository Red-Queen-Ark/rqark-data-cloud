// Command backfill-provider-models gives every provider stored before the
// Provider/Model split one provider_models row derived from its flat columns. It
// follows the createsuperuser bootstrap: connect, migrate the tables it touches,
// then do the one-off work.
//
// The run is recorded in system_configs, so a second invocation exits without
// writing anything. A partial failure is safe to retry: each insert ignores a
// model row that is already there, and the flag is only written once every
// provider has been handled.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

func main() {
	ctx := context.Background()
	config.InitDatabase()
	db := config.DB

	if err := db.AutoMigrate(
		&repository.Provider{},
		&repository.ProviderModel{},
		&repository.SystemConfig{},
	); err != nil {
		log.Fatalf("backfill-provider-models: automigrate: %v", err)
	}

	flag, err := repository.GetSystemConfigOrDefault(ctx, db, repository.ProviderModelBackfillKey, json.RawMessage(`false`))
	if err != nil {
		log.Fatalf("backfill-provider-models: read run flag: %v", err)
	}
	var done bool
	if err := json.Unmarshal(flag, &done); err != nil {
		log.Fatalf("backfill-provider-models: run flag is not a boolean: %v", err)
	}
	if done {
		log.Print("backfill-provider-models: already done, nothing to do")
		return
	}

	result, err := repository.BackfillProviderModels(ctx, db)
	if err != nil {
		log.Fatalf("backfill-provider-models: backfill: %v", err)
	}

	if err := recordRun(ctx, db); err != nil {
		log.Fatalf("backfill-provider-models: record run: %v", err)
	}

	log.Printf("backfill-provider-models: created %d, already present %d", result.Created, result.Existing)
}

// recordRun writes the flag that makes the next invocation a no-op, creating the
// row on the first run and overwriting it on a later one.
func recordRun(ctx context.Context, db *gorm.DB) error {
	value := json.RawMessage(`true`)
	existing, err := repository.GetSystemConfig(ctx, db, repository.ProviderModelBackfillKey)
	switch {
	case err == nil:
		existing.Value = value
		return repository.SaveSystemConfig(ctx, db, existing)
	case errors.Is(err, repository.ErrSystemConfigNotFound):
		return repository.CreateSystemConfig(ctx, db, &repository.SystemConfig{
			ID:    uuid.New(),
			Key:   repository.ProviderModelBackfillKey,
			Value: value,
		})
	default:
		return err
	}
}
