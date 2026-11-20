package repository

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// providerModelTestDB opens the shared test database and returns a connection
// pinned to a schema of its own, so the backfill test sees only the providers it
// created and the index test never shares a name with another test's leftovers.
// It skips when TEST_POSTGRES_DSN is not set, like the other database tests.
func providerModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set, skipping the database test")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}

	schema := "providermodel_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
	})

	db, err := gorm.Open(postgres.Open(dsn+" options='-c search_path="+schema+"'"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open %s on schema %s: %v", dsn, schema, err)
	}
	if err := db.AutoMigrate(&User{}, &Provider{}, &ProviderModel{}, &SystemConfig{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

// providerModelTestUser inserts a user the tests can hang providers on and
// removes the user, its providers and their models afterwards.
func providerModelTestUser(t *testing.T, db *gorm.DB) *User {
	t.Helper()

	user := &User{
		ID:             uuid.New(),
		Username:       "providermodel-" + uuid.NewString(),
		Email:          uuid.NewString() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Cleanup(func() {
		db.Where("provider_id IN (SELECT id FROM providers WHERE user_id = ?)", user.ID).
			Delete(&ProviderModel{})
		db.Where("user_id = ?", user.ID).Delete(&Provider{})
		db.Delete(&User{}, "id = ?", user.ID)
	})
	return user
}

// TestBackfillProviderModelsGivesEveryProviderOneDefaultModel covers the
// one-off migration: a provider's flat columns become one model row tagged llm
// and marked default, and the second run writes nothing.
func TestBackfillProviderModelsGivesEveryProviderOneDefaultModel(t *testing.T) {
	db := providerModelTestDB(t)
	user := providerModelTestUser(t, db)

	temperature := 0.3
	providers := []*Provider{
		{
			ID: uuid.New(), UserID: user.ID, Name: "first-" + uuid.NewString(),
			ModelName: "deepseek-chat", APIKey: "sk-test", APIBase: "https://example.invalid/v1",
			Temperature: &temperature, Combine: true, Priority: 10,
		},
		{
			ID: uuid.New(), UserID: user.ID, Name: "second-" + uuid.NewString(),
			ModelName: "gpt-4o", APIKey: "sk-test", APIBase: "https://example.invalid/v1",
			Priority: 20,
		},
	}
	for _, provider := range providers {
		if err := db.Create(provider).Error; err != nil {
			t.Fatalf("create provider: %v", err)
		}
	}

	first, err := BackfillProviderModels(context.Background(), db)
	if err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if first.Created != len(providers) || first.Existing != 0 {
		t.Fatalf("first backfill = %+v, want %d created and 0 existing", first, len(providers))
	}

	second, err := BackfillProviderModels(context.Background(), db)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if second.Created != 0 || second.Existing != len(providers) {
		t.Fatalf("second backfill = %+v, want 0 created and %d existing", second, len(providers))
	}

	firstProvider := providers[0]
	models, err := ListProviderModels(context.Background(), db, firstProvider.ID)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	model := models[0]
	if model.ModelID != firstProvider.ModelName || model.DisplayName != firstProvider.ModelName {
		t.Errorf("model = %q/%q, want %q twice", model.ModelID, model.DisplayName, firstProvider.ModelName)
	}
	if !model.IsDefault || !model.Enabled {
		t.Errorf("model default/enabled = %v/%v, want true/true", model.IsDefault, model.Enabled)
	}
	if len(model.Capabilities) != 1 || model.Capabilities[0] != ProviderModelCapabilityLLM {
		t.Errorf("capabilities = %v, want [%s]", model.Capabilities, ProviderModelCapabilityLLM)
	}
	if model.Params == nil || model.Params.Temperature == nil || *model.Params.Temperature != temperature {
		t.Errorf("params = %+v, want temperature %v", model.Params, temperature)
	}

	other, err := ListProviderModels(context.Background(), db, providers[1].ID)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(other) != 1 || other[0].Params == nil || other[0].Params.Temperature != nil {
		t.Errorf("second provider model = %+v, want one model without a temperature", other)
	}
}

// TestEnsureProviderNameIndexSkipsDuplicates covers the guard: the index is not
// created while duplicates exist, the pairs are reported, and the second call
// succeeds once they are gone.
func TestEnsureProviderNameIndexSkipsDuplicates(t *testing.T) {
	db := providerModelTestDB(t)
	user := providerModelTestUser(t, db)

	name := "shared-" + uuid.NewString()
	for i := 0; i < 2; i++ {
		provider := &Provider{
			ID: uuid.New(), UserID: user.ID, Name: name, ModelName: "m" + uuid.NewString(),
			APIKey: "sk-test", APIBase: "https://example.invalid/v1",
		}
		if err := db.Create(provider).Error; err != nil {
			t.Fatalf("create provider: %v", err)
		}
	}

	duplicates, err := EnsureProviderNameIndex(context.Background(), db)
	if err != nil {
		t.Fatalf("ensure index with duplicates: %v", err)
	}
	if len(duplicates) != 1 || duplicates[0].UserID != user.ID || duplicates[0].Name != name || duplicates[0].Count != 2 {
		t.Fatalf("duplicates = %+v, want one pair (%s, %q, 2)", duplicates, user.ID, name)
	}

	// Remove one of the two, leaving a single provider under the name.
	var doomed Provider
	if err := db.Where("user_id = ? AND name = ?", user.ID, name).Order("id").First(&doomed).Error; err != nil {
		t.Fatalf("read duplicate: %v", err)
	}
	if err := db.Delete(&Provider{}, "id = ?", doomed.ID).Error; err != nil {
		t.Fatalf("remove duplicate: %v", err)
	}

	duplicates, err = EnsureProviderNameIndex(context.Background(), db)
	if err != nil {
		t.Fatalf("ensure index: %v", err)
	}
	if len(duplicates) != 0 {
		t.Fatalf("duplicates = %+v, want none", duplicates)
	}

	err = db.Create(&Provider{
		ID: uuid.New(), UserID: user.ID, Name: name, ModelName: "dupe-" + uuid.NewString(),
		APIKey: "sk-test", APIBase: "https://example.invalid/v1",
	}).Error
	if err == nil {
		t.Error("a second provider under the same name was accepted, want the unique index to reject it")
	}
}

// TestGetSystemConfigOrDefaultFallsBackOnAMissingKey covers the helper the
// backfill uses to tell a first run from a repeat.
func TestGetSystemConfigOrDefaultFallsBackOnAMissingKey(t *testing.T) {
	db := providerModelTestDB(t)
	ctx := context.Background()
	key := "test-flag-" + uuid.NewString()

	value, err := GetSystemConfigOrDefault(ctx, db, key, json.RawMessage(`false`))
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if string(value) != "false" {
		t.Errorf("value = %s, want false", value)
	}

	if err := CreateSystemConfig(ctx, db, &SystemConfig{ID: uuid.New(), Key: key, Value: json.RawMessage(`true`)}); err != nil {
		t.Fatalf("create config: %v", err)
	}
	t.Cleanup(func() { db.Delete(&SystemConfig{}, "key = ?", key) })

	value, err = GetSystemConfigOrDefault(ctx, db, key, json.RawMessage(`false`))
	if err != nil {
		t.Fatalf("get present: %v", err)
	}
	if string(value) != "true" {
		t.Errorf("value = %s, want true", value)
	}
}
