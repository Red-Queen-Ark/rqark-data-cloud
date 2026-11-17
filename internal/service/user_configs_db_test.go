package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// userConfigTestDB opens the shared test database with the user and config
// tables installed.
func userConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := testDatabase(t)
	if err := db.AutoMigrate(&repository.User{}, &repository.UserConfig{}); err != nil {
		t.Fatalf("automigrate user configs: %v", err)
	}
	return db
}

// testConfigOwner inserts a user the config tests can hang settings on, and
// removes its rows afterwards.
func testConfigOwner(t *testing.T, db *gorm.DB) *repository.User {
	t.Helper()

	id := uuid.New()
	user := &repository.User{
		ID:             id,
		Username:       "cfgtest-" + id.String(),
		Email:          id.String() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ?", id).Delete(&repository.UserConfig{})
		db.Delete(&repository.User{}, "id = ?", id)
	})
	return user
}

// configValue reads one key out of a listed set.
func configValue(t *testing.T, configs []repository.UserConfig, key string) (json.RawMessage, bool) {
	t.Helper()

	for i := range configs {
		if configs[i].Key == key {
			return configs[i].Value, true
		}
	}
	return nil, false
}

// TestListUserConfigsFillsMemoryDefaults covers a fresh account: both memory
// switches come back stored as false rather than absent, which is what makes the
// per-turn writer and the state board opt-in for a client that has never written
// a config row.
func TestListUserConfigsFillsMemoryDefaults(t *testing.T) {
	db := userConfigTestDB(t)
	user := testConfigOwner(t, db)

	configs, err := ListUserConfigs(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListUserConfigs: %v", err)
	}
	if len(configs) != len(defaultUserConfigs) {
		t.Fatalf("got %d configs, want %d", len(configs), len(defaultUserConfigs))
	}
	for _, key := range []string{eventLogConfigKey, stateUpdateConfigKey} {
		value, ok := configValue(t, configs, key)
		if !ok {
			t.Fatalf("%s missing from the listed configs", key)
		}
		if string(value) != "false" {
			t.Errorf("%s = %s, want false", key, value)
		}
	}
}

// TestListUserConfigsFillsDefaultsOnce covers the second read: the fill is
// written to the table, so a repeat read returns the same ids instead of
// inserting another pair of rows.
func TestListUserConfigsFillsDefaultsOnce(t *testing.T) {
	db := userConfigTestDB(t)
	user := testConfigOwner(t, db)

	first, err := ListUserConfigs(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("first ListUserConfigs: %v", err)
	}
	second, err := ListUserConfigs(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("second ListUserConfigs: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("first read returned %d configs, second %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Errorf("config %s changed id across reads", first[i].Key)
		}
	}

	var count int64
	if err := db.Model(&repository.UserConfig{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != int64(len(defaultUserConfigs)) {
		t.Errorf("stored %d rows, want %d", count, len(defaultUserConfigs))
	}
}

// TestListUserConfigsKeepsStoredValue covers an account that turned a switch
// back on: the stored row wins over the default.
func TestListUserConfigsKeepsStoredValue(t *testing.T) {
	db := userConfigTestDB(t)
	user := testConfigOwner(t, db)

	if _, err := SetUserConfig(context.Background(), user.ID, eventLogConfigKey, json.RawMessage("true")); err != nil {
		t.Fatalf("SetUserConfig: %v", err)
	}

	configs, err := ListUserConfigs(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListUserConfigs: %v", err)
	}
	value, ok := configValue(t, configs, eventLogConfigKey)
	if !ok {
		t.Fatalf("%s missing from the listed configs", eventLogConfigKey)
	}
	if string(value) != "true" {
		t.Errorf("%s = %s, want true", eventLogConfigKey, value)
	}
}

// TestListUserConfigsLeavesOtherKeysAlone covers the boundary of the fill: a key
// outside the default set is still absent when the account never wrote it.
func TestListUserConfigsLeavesOtherKeysAlone(t *testing.T) {
	db := userConfigTestDB(t)
	user := testConfigOwner(t, db)

	configs, err := ListUserConfigs(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListUserConfigs: %v", err)
	}
	for i := range configs {
		if configs[i].Key == "theme" || configs[i].Key == "language" {
			t.Fatalf("%s should not be filled by the memory defaults", configs[i].Key)
		}
	}
}
