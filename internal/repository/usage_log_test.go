package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// usageListTestDB opens the shared test database and adds the tables this file
// needs. It skips when TEST_POSTGRES_DSN is not set, like the other database
// tests.
func usageListTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set, skipping the database test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	if err := db.AutoMigrate(&User{}, &APIKey{}, &UsageLog{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

// usageListFixture inserts a user, one key and three usage rows: two against the
// key and one against no key, plus a row of a second user that must never
// appear. It returns the owner and the key.
func usageListFixture(t *testing.T, db *gorm.DB) (*User, *APIKey) {
	t.Helper()

	ctx := context.Background()
	owner := &User{
		ID:             uuid.New(),
		Username:       "usagelist-" + uuid.NewString(),
		Email:          uuid.NewString() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	other := &User{
		ID:             uuid.New(),
		Username:       "usagelist-" + uuid.NewString(),
		Email:          uuid.NewString() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	for _, user := range []*User{owner, other} {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
	}

	key := &APIKey{
		ID:        uuid.New(),
		UserID:    owner.ID,
		Name:      "laptop",
		KeyPrefix: "rqark-test",
		KeyHash:   uuid.NewString(),
		IsActive:  true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}

	t.Cleanup(func() {
		db.Where("user_id IN ?", []uuid.UUID{owner.ID, other.ID}).Delete(&UsageLog{})
		db.Where("user_id IN ?", []uuid.UUID{owner.ID, other.ID}).Delete(&APIKey{})
		db.Where("id IN ?", []uuid.UUID{owner.ID, other.ID}).Delete(&User{})
	})

	now := time.Now().UTC()
	rows := []UsageLog{
		{
			ID: uuid.New(), CreatedAt: now.Add(-3 * time.Hour), UserID: owner.ID,
			APIKeyID: &key.ID, ProviderID: uuid.New(), Source: "TUI", Model: "deepseek-flash",
			PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120,
		},
		{
			ID: uuid.New(), CreatedAt: now.Add(-2 * time.Hour), UserID: owner.ID,
			APIKeyID: &key.ID, ProviderID: uuid.New(), Source: "TUI", Model: "gpt-4o",
			PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, IsCompaction: true,
		},
		{
			ID: uuid.New(), CreatedAt: now.Add(-time.Hour), UserID: owner.ID,
			ProviderID: uuid.New(), Source: "AGENT_CLOUD", Model: "deepseek-flash",
			PromptTokens: 50, CompletionTokens: 10, TotalTokens: 60,
		},
		{
			ID: uuid.New(), CreatedAt: now, UserID: other.ID,
			ProviderID: uuid.New(), Source: "TUI", Model: "deepseek-flash",
			PromptTokens: 999, CompletionTokens: 1, TotalTokens: 1000,
		},
	}
	for i := range rows {
		if err := CreateUsageLog(ctx, db, &rows[i]); err != nil {
			t.Fatalf("create usage log: %v", err)
		}
	}
	return owner, key
}

// TestListUsageLogsIsScopedToTheUser covers the boundary the endpoint relies on:
// no filter still returns only the caller's rows.
func TestListUsageLogsIsScopedToTheUser(t *testing.T) {
	db := usageListTestDB(t)
	owner, _ := usageListFixture(t, db)

	entries, err := ListUsageLogs(context.Background(), db, UsageLogFilter{UserID: &owner.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("rows = %d, want 3", len(entries))
	}
	for _, entry := range entries {
		if entry.UserID != owner.ID {
			t.Fatalf("row belongs to %s, want %s", entry.UserID, owner.ID)
		}
	}
}

// TestListUsageLogsWithNoUserTakesEveryUser covers the superuser listing: a nil
// UserID drops the owner predicate, so the second user's row appears too.
func TestListUsageLogsWithNoUserTakesEveryUser(t *testing.T) {
	db := usageListTestDB(t)
	owner, _ := usageListFixture(t, db)

	entries, err := ListUsageLogs(context.Background(), db, UsageLogFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("rows = %d, want 4", len(entries))
	}
	owners := map[uuid.UUID]int{}
	for _, entry := range entries {
		owners[entry.UserID]++
	}
	if owners[owner.ID] != 3 {
		t.Fatalf("owner rows = %d, want 3", owners[owner.ID])
	}
	if len(owners) != 2 {
		t.Fatalf("distinct owners = %d, want 2", len(owners))
	}
}

// TestListUsageLogsJoinsTheKeyName covers attribution: a key row reports its
// name, a key-less row reports none, and the name filters.
func TestListUsageLogsJoinsTheKeyName(t *testing.T) {
	db := usageListTestDB(t)
	owner, _ := usageListFixture(t, db)

	entries, err := ListUsageLogs(context.Background(), db, UsageLogFilter{UserID: &owner.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	named, unnamed := 0, 0
	for _, entry := range entries {
		if entry.APIKeyName == nil {
			unnamed++
			continue
		}
		if *entry.APIKeyName != "laptop" {
			t.Fatalf("key name = %q, want laptop", *entry.APIKeyName)
		}
		named++
	}
	if named != 2 || unnamed != 1 {
		t.Fatalf("named = %d, unnamed = %d, want 2 and 1", named, unnamed)
	}

	name := "laptop"
	filtered, err := ListUsageLogs(context.Background(), db, UsageLogFilter{UserID: &owner.ID, APIKeyName: &name})
	if err != nil {
		t.Fatalf("list by key name: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("rows by key name = %d, want 2", len(filtered))
	}
}

// TestListUsageLogsFilters covers the remaining predicates and the ordering.
func TestListUsageLogsFilters(t *testing.T) {
	db := usageListTestDB(t)
	owner, _ := usageListFixture(t, db)

	source := "AGENT_CLOUD"
	bySource, err := ListUsageLogs(context.Background(), db, UsageLogFilter{UserID: &owner.ID, Source: &source})
	if err != nil {
		t.Fatalf("list by source: %v", err)
	}
	if len(bySource) != 1 || bySource[0].Model != "deepseek-flash" {
		t.Fatalf("rows by source = %+v, want the AGENT_CLOUD row", bySource)
	}

	compaction := true
	byCompaction, err := ListUsageLogs(context.Background(), db, UsageLogFilter{UserID: &owner.ID, IsCompaction: &compaction})
	if err != nil {
		t.Fatalf("list by compaction: %v", err)
	}
	if len(byCompaction) != 1 || !byCompaction[0].IsCompaction {
		t.Fatalf("rows by compaction = %+v, want one compaction row", byCompaction)
	}

	from := time.Now().UTC().Add(-90 * time.Minute)
	byFrom, err := ListUsageLogs(context.Background(), db, UsageLogFilter{UserID: &owner.ID, From: &from})
	if err != nil {
		t.Fatalf("list by from: %v", err)
	}
	if len(byFrom) != 1 {
		t.Fatalf("rows from the last 90 minutes = %d, want 1", len(byFrom))
	}
	all, err := ListUsageLogs(context.Background(), db, UsageLogFilter{UserID: &owner.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].CreatedAt.Before(all[i].CreatedAt) {
			t.Fatalf("rows are not newest first: %v then %v", all[i-1].CreatedAt, all[i].CreatedAt)
		}
	}
}
