package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"rqark-data-cloud/internal/repository"
)

// usageTestDB opens the shared test database and adds the two tables this file
// needs on top of the ones the auth tests create.
func usageTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := testDatabase(t)
	if err := db.AutoMigrate(&repository.Session{}, &repository.UsageLog{}); err != nil {
		t.Fatalf("automigrate usage tables: %v", err)
	}
	return db
}

// TestUsageSurvivesSessionDeletion is the reason the table exists: a session is
// deleted through the ordinary path and the usage rows are still there, still
// summed, and still attributed to the user.
func TestUsageSurvivesSessionDeletion(t *testing.T) {
	db := usageTestDB(t)
	user := testKeyOwner(t, db)
	providerID := uuid.New()
	sessionID := uuid.New()

	session := &repository.Session{ID: sessionID, UserID: user.ID, Title: "demo"}
	if err := repository.CreateSession(context.Background(), db, session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := repository.CreateUsageLog(context.Background(), db, &repository.UsageLog{
		ID:           uuid.New(),
		CreatedAt:    time.Now().UTC(),
		UserID:       user.ID,
		SessionID:    &sessionID,
		ProviderID:   providerID,
		Source:       "TUI",
		Model:        "deepseek-flash",
		PromptTokens: 100,
		TotalTokens:  120,
	}); err != nil {
		t.Fatalf("create usage: %v", err)
	}

	from, to := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	before, err := repository.SumUsageByUser(context.Background(), db, user.ID, from, to)
	if err != nil {
		t.Fatalf("sum before: %v", err)
	}

	if err := repository.DeleteSession(context.Background(), db, user.ID, sessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	after, err := repository.SumUsageByUser(context.Background(), db, user.ID, from, to)
	if err != nil {
		t.Fatalf("sum after: %v", err)
	}

	if after != before {
		t.Errorf("usage changed when the session was deleted: %+v then %+v", before, after)
	}
	if after.Requests != 1 || after.TotalTokens != 120 {
		t.Errorf("usage = %+v, want one request and 120 tokens", after)
	}
}

// TestUsageKeepsAReferenceToADeletedSession covers the column semantics: the
// row keeps the id it was given, and the session table no longer has it.
func TestUsageKeepsAReferenceToADeletedSession(t *testing.T) {
	db := usageTestDB(t)
	user := testKeyOwner(t, db)
	sessionID := uuid.New()

	if err := repository.CreateSession(context.Background(), db, &repository.Session{ID: sessionID, UserID: user.ID}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	row := &repository.UsageLog{
		ID:         uuid.New(),
		CreatedAt:  time.Now().UTC(),
		UserID:     user.ID,
		SessionID:  &sessionID,
		ProviderID: uuid.New(),
	}
	if err := repository.CreateUsageLog(context.Background(), db, row); err != nil {
		t.Fatalf("create usage: %v", err)
	}
	if err := repository.DeleteSession(context.Background(), db, user.ID, sessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	var stored repository.UsageLog
	if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatalf("reload usage: %v", err)
	}
	if stored.SessionID == nil || *stored.SessionID != sessionID {
		t.Errorf("SessionID = %v, want the id the row was written with", stored.SessionID)
	}

	var count int64
	if err := db.Model(&repository.Session{}).Where("id = ?", sessionID).Count(&count).Error; err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Errorf("session rows = %d, want 0", count)
	}
}

// TestUsageKeepsAbsentReferencesNull covers a call that named nothing: the
// columns are NULL, not a zero uuid.
func TestUsageKeepsAbsentReferencesNull(t *testing.T) {
	db := usageTestDB(t)
	user := testKeyOwner(t, db)

	row := &repository.UsageLog{
		ID:         uuid.New(),
		CreatedAt:  time.Now().UTC(),
		UserID:     user.ID,
		ProviderID: uuid.New(),
	}
	if err := repository.CreateUsageLog(context.Background(), db, row); err != nil {
		t.Fatalf("create usage: %v", err)
	}

	var stored repository.UsageLog
	if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatalf("reload usage: %v", err)
	}
	if stored.SessionID != nil || stored.AgentID != nil || stored.APIKeyID != nil {
		t.Errorf("references = %v/%v/%v, want all nil", stored.SessionID, stored.AgentID, stored.APIKeyID)
	}
}
