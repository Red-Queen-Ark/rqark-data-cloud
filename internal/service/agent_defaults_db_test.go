package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// agentDefaultTestDB opens the shared test database and installs the tables the
// agent default tests need on top of it.
func agentDefaultTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := testDatabase(t)
	if err := db.AutoMigrate(&repository.Agent{}, &repository.AgentDefault{}); err != nil {
		t.Fatalf("automigrate agent defaults: %v", err)
	}
	return db
}

// testAgentDefaultUser inserts a user the agent default tests can hang agents on
// and removes the user, its agents and its defaults afterwards.
func testAgentDefaultUser(t *testing.T, db *gorm.DB) *repository.User {
	t.Helper()

	id := uuid.New()
	user := &repository.User{
		ID:             id,
		Username:       "agentdefaulttest-" + id.String(),
		Email:          id.String() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Cleanup(func() {
		db.Where("user_id = ?", id).Delete(&repository.AgentDefault{})
		db.Where("user_id = ?", id).Delete(&repository.Agent{})
		db.Delete(&repository.User{}, "id = ?", id)
	})
	return user
}

// newTestAgent stores one agent for owner.
func newTestAgent(t *testing.T, db *gorm.DB, owner *repository.User) *repository.Agent {
	t.Helper()

	id := uuid.New()
	agent := &repository.Agent{
		ID:     id,
		UserID: owner.ID,
		Name:   "agent-" + id.String(),
	}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

// TestAgentDefaultUpsertsPerSource covers the point of the table: a user holds
// one default agent per source, and setting the same source again repoints the
// row rather than adding another.
func TestAgentDefaultUpsertsPerSource(t *testing.T) {
	db := agentDefaultTestDB(t)
	user := testAgentDefaultUser(t, db)
	first := newTestAgent(t, db, user)
	second := newTestAgent(t, db, user)

	created, err := SetAgentDefault(context.Background(), user.ID, "TUI", first.ID)
	if err != nil {
		t.Fatalf("SetAgentDefault: %v", err)
	}
	if created.AgentID != first.ID {
		t.Errorf("agent_id = %s, want %s", created.AgentID, first.ID)
	}

	updated, err := SetAgentDefault(context.Background(), user.ID, "TUI", second.ID)
	if err != nil {
		t.Fatalf("SetAgentDefault on an existing source: %v", err)
	}
	if updated.ID != created.ID {
		t.Errorf("second write made row %s, want the existing %s", updated.ID, created.ID)
	}
	if updated.AgentID != second.ID {
		t.Errorf("agent_id = %s, want the repointed %s", updated.AgentID, second.ID)
	}

	if _, err := SetAgentDefault(context.Background(), user.ID, "AGENT_CLOUD", first.ID); err != nil {
		t.Fatalf("SetAgentDefault for a second source: %v", err)
	}

	defaults, err := ListAgentDefaults(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListAgentDefaults: %v", err)
	}
	if len(defaults) != 2 {
		t.Fatalf("defaults = %d rows, want 2", len(defaults))
	}
	if defaults[0].Source != "AGENT_CLOUD" || defaults[1].Source != "TUI" {
		t.Errorf("sources = %s, %s, want AGENT_CLOUD before TUI", defaults[0].Source, defaults[1].Source)
	}
}

// TestAgentDefaultRefusesAnotherUsersAgent covers the boundary: a default has to
// point at an agent the same user owns.
func TestAgentDefaultRefusesAnotherUsersAgent(t *testing.T) {
	db := agentDefaultTestDB(t)
	owner := testAgentDefaultUser(t, db)
	viewer := testAgentDefaultUser(t, db)
	foreign := newTestAgent(t, db, owner)

	if _, err := SetAgentDefault(context.Background(), viewer.ID, "TUI", foreign.ID); !errors.Is(err, repository.ErrAgentNotFound) {
		t.Errorf("SetAgentDefault with another user's agent = %v, want %v", err, repository.ErrAgentNotFound)
	}
}

// TestAgentDefaultHidesADefaultTheUserDoesNotHave covers the 404: deleting a
// source that holds no default is refused rather than acknowledged.
func TestAgentDefaultHidesADefaultTheUserDoesNotHave(t *testing.T) {
	db := agentDefaultTestDB(t)
	user := testAgentDefaultUser(t, db)

	if err := DeleteAgentDefault(context.Background(), user.ID, "TUI"); !errors.Is(err, repository.ErrAgentDefaultNotFound) {
		t.Errorf("DeleteAgentDefault with no default = %v, want %v", err, repository.ErrAgentDefaultNotFound)
	}
}

// TestValidateAgentDefaultSource covers the path segment grammar: a source has
// to be present, fit its column and stay URL-safe.
func TestValidateAgentDefaultSource(t *testing.T) {
	if err := validateAgentDefaultSource("TUI"); err != nil {
		t.Errorf("TUI = %v, want nil", err)
	}
	if err := validateAgentDefaultSource(""); err == nil {
		t.Error("an empty source = nil, want an error")
	}
	if err := validateAgentDefaultSource(string(make([]byte, maxAgentDefaultSourceLen+1))); err == nil {
		t.Error("an over-long source = nil, want an error")
	}
	if err := validateAgentDefaultSource("my app"); err == nil {
		t.Error("a source with a space = nil, want an error")
	}
}
