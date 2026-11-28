package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"rqark-data-cloud/internal/repository"
)

// agentStateTestDB opens the shared test database and installs the state table.
// The table may be left behind by an earlier run with a narrower CHECK, so the
// content limit is migrated the way the server does it at startup.
func agentStateTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := testDatabase(t)
	if err := db.AutoMigrate(&repository.User{}, &repository.Agent{}, &repository.AgentState{}); err != nil {
		t.Fatalf("automigrate agent state: %v", err)
	}
	if err := repository.MigrateAgentStateContentLimit(context.Background(), db); err != nil {
		t.Fatalf("migrate agent state content limit: %v", err)
	}
	return db
}

// testAgentStateOwner inserts a user and one agent it owns, and removes both
// afterwards.
func testAgentStateOwner(t *testing.T, db *gorm.DB) (*repository.User, *repository.Agent) {
	t.Helper()

	id := uuid.New()
	user := &repository.User{
		ID:             id,
		Username:       "statetest-" + id.String(),
		Email:          id.String() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	agent := &repository.Agent{
		ID:     uuid.New(),
		UserID: id,
		Name:   "agent-" + id.String(),
	}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}

	t.Cleanup(func() {
		db.Where("user_id = ?", id).Delete(&repository.AgentState{})
		db.Delete(&repository.Agent{}, "id = ?", agent.ID)
		db.Delete(&repository.User{}, "id = ?", id)
	})
	return user, agent
}

// TestAgentStateUpsertKeepsOneRow covers the board's core promise: a pair owns
// one row, however many times it is written, and every write bumps version.
func TestAgentStateUpsertKeepsOneRow(t *testing.T) {
	db := agentStateTestDB(t)
	user, agent := testAgentStateOwner(t, db)

	first, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "第一版", Source: "TUI"})
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if first.Version != 1 {
		t.Fatalf("first version = %d, want 1", first.Version)
	}

	second, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "第二版", Source: "AGENT_CLOUD"})
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if second.Version != 2 {
		t.Fatalf("second version = %d, want 2", second.Version)
	}
	if second.Content != "第二版" || second.Source != "AGENT_CLOUD" {
		t.Fatalf("second write stored %+v", second)
	}

	var count int64
	if err := db.Model(&repository.AgentState{}).Where("user_id = ? AND agent_id = ?", user.ID, agent.ID).Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("pair holds %d rows, want 1", count)
	}

	stored, err := GetAgentState(context.Background(), user.ID, agent.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Version != 2 || stored.Content != "第二版" {
		t.Fatalf("read back %+v", stored)
	}
}

// TestAgentStateVersionConflict covers the optimistic lock: a write carrying a
// version somebody else already moved past changes nothing.
func TestAgentStateVersionConflict(t *testing.T) {
	db := agentStateTestDB(t)
	user, agent := testAgentStateOwner(t, db)

	for i := 1; i <= 3; i++ {
		if _, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "board"}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	stale := 2
	_, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "stale overwrite", Version: &stale})
	if !errors.Is(err, repository.ErrStateConflict) {
		t.Fatalf("expected ErrStateConflict, got %v", err)
	}

	stored, err := GetAgentState(context.Background(), user.ID, agent.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Content != "board" || stored.Version != 3 {
		t.Fatalf("a refused write changed the row: %+v", stored)
	}

	current := 3
	ok, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "newer", Version: &current})
	if err != nil {
		t.Fatalf("write at the current version: %v", err)
	}
	if ok.Version != 4 {
		t.Fatalf("version = %d, want 4", ok.Version)
	}
}

// TestAgentStateLimits covers the checks the column limits back up: content is
// bounded and blank content is refused, and source is bounded too.
func TestAgentStateLimits(t *testing.T) {
	db := agentStateTestDB(t)
	user, agent := testAgentStateOwner(t, db)

	base := SetAgentStateInput{AgentID: agent.ID}

	tooLong := base
	tooLong.Content = strings.Repeat("字", repository.MaxStateContentLen+1)
	if _, err := SetAgentState(context.Background(), user.ID, tooLong); err == nil {
		t.Fatal("expected over-long state to be rejected")
	}

	blank := base
	blank.Content = "   "
	if _, err := SetAgentState(context.Background(), user.ID, blank); err == nil {
		t.Fatal("expected blank state to be rejected")
	}

	longSource := base
	longSource.Content = "board"
	longSource.Source = strings.Repeat("s", maxMemorySourceLen+1)
	if _, err := SetAgentState(context.Background(), user.ID, longSource); err == nil {
		t.Fatal("expected an over-long source to be rejected")
	}
}

// TestAgentStateBelongsToItsOwner covers the pair boundary: another user's agent
// is reported as missing, never read and never written.
func TestAgentStateBelongsToItsOwner(t *testing.T) {
	db := agentStateTestDB(t)
	owner, agent := testAgentStateOwner(t, db)
	stranger, _ := testAgentStateOwner(t, db)

	if _, err := SetAgentState(context.Background(), owner.ID, SetAgentStateInput{AgentID: agent.ID, Content: "board"}); err != nil {
		t.Fatalf("owner write: %v", err)
	}

	if _, err := GetAgentState(context.Background(), stranger.ID, agent.ID); !errors.Is(err, repository.ErrAgentNotFound) {
		t.Fatalf("stranger read: expected ErrAgentNotFound, got %v", err)
	}
	if _, err := SetAgentState(context.Background(), stranger.ID, SetAgentStateInput{AgentID: agent.ID, Content: "hijack"}); !errors.Is(err, repository.ErrAgentNotFound) {
		t.Fatalf("stranger write: expected ErrAgentNotFound, got %v", err)
	}
	if err := DeleteAgentState(context.Background(), stranger.ID, agent.ID); !errors.Is(err, repository.ErrAgentNotFound) {
		t.Fatalf("stranger delete: expected ErrAgentNotFound, got %v", err)
	}

	stored, err := GetAgentState(context.Background(), owner.ID, agent.ID)
	if err != nil {
		t.Fatalf("owner read: %v", err)
	}
	if stored.Content != "board" {
		t.Fatalf("owner state = %q, want board", stored.Content)
	}
}

// TestAgentStateDeleteAndNotFound covers the two 404s: a pair without a row
// answers not found, and deleting twice reports the second as not found.
func TestAgentStateDeleteAndNotFound(t *testing.T) {
	db := agentStateTestDB(t)
	user, agent := testAgentStateOwner(t, db)

	if _, err := GetAgentState(context.Background(), user.ID, agent.ID); !errors.Is(err, repository.ErrStateNotFound) {
		t.Fatalf("expected ErrStateNotFound, got %v", err)
	}
	if _, err := AgentStateOrNil(context.Background(), user.ID, agent.ID); err != nil {
		t.Fatalf("AgentStateOrNil reported %v, want nil", err)
	}

	if _, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "board"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := DeleteAgentState(context.Background(), user.ID, agent.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := DeleteAgentState(context.Background(), user.ID, agent.ID); !errors.Is(err, repository.ErrStateNotFound) {
		t.Fatalf("second delete: expected ErrStateNotFound, got %v", err)
	}

	if next, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "fresh"}); err != nil {
		t.Fatalf("write after delete: %v", err)
	} else if next.Version != 1 {
		t.Fatalf("version after delete = %d, want 1", next.Version)
	}
}

// TestAgentStateFollowsTheAgent covers the cascading foreign key: an agent that
// goes takes its state with it.
func TestAgentStateFollowsTheAgent(t *testing.T) {
	db := agentStateTestDB(t)
	user, agent := testAgentStateOwner(t, db)

	if _, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "board"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := db.Delete(&repository.Agent{}, "id = ?", agent.ID).Error; err != nil {
		t.Fatalf("delete agent: %v", err)
	}

	var count int64
	if err := db.Model(&repository.AgentState{}).Where("agent_id = ?", agent.ID).Count(&count).Error; err != nil {
		t.Fatalf("count state: %v", err)
	}
	if count != 0 {
		t.Fatalf("state survived its agent: %d rows", count)
	}
}

// TestAgentStateRejectsAnUnknownVersion covers the guard on a version that
// cannot name any write.
func TestAgentStateRejectsAnUnknownVersion(t *testing.T) {
	db := agentStateTestDB(t)
	user, agent := testAgentStateOwner(t, db)

	zero := 0
	_, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "board", Version: &zero})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for version 0, got %v", err)
	}
}

// TestAgentStatePromptRead covers the read the prompt makes: the board comes
// back with the facts, and a pair without one reports nil rather than an error.
func TestAgentStatePromptRead(t *testing.T) {
	db := agentStateTestDB(t)
	user, agent := testAgentStateOwner(t, db)

	state, err := AgentStateOrNil(context.Background(), user.ID, agent.ID)
	if err != nil || state != nil {
		t.Fatalf("empty pair read %+v, %v; want nil, nil", state, err)
	}

	if _, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "board", Source: "TUI"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	state, err = AgentStateOrNil(context.Background(), user.ID, agent.ID)
	if err != nil || state == nil {
		t.Fatalf("read %+v, %v; want a row", state, err)
	}
	if state.Content != "board" || state.Version != 1 {
		t.Fatalf("read %+v", state)
	}
}

// TestAgentStateContentLimitIsInTheSchema covers the CHECK constraint, which
// guards the column even when a write skips the service.
func TestAgentStateContentLimitIsInTheSchema(t *testing.T) {
	db := agentStateTestDB(t)
	user, agent := testAgentStateOwner(t, db)

	row := &repository.AgentState{UserID: user.ID, AgentID: agent.ID, Content: strings.Repeat("字", repository.MaxStateContentLen+1)}
	if err := db.Create(row).Error; err == nil {
		t.Fatal("expected the CHECK constraint to reject an over-long row")
	}
}

// TestAgentStateAutoMigrateIsRepeatable covers the migration running again on a
// database that already holds the table.
func TestAgentStateAutoMigrateIsRepeatable(t *testing.T) {
	db := agentStateTestDB(t)
	if err := db.AutoMigrate(&repository.AgentState{}); err != nil {
		t.Fatalf("second automigrate: %v", err)
	}
}

// TestMigrateAgentStateContentLimitWidensAnOldTable covers the upgrade path: a
// table created under the old 2000-character ceiling is narrowed there by its
// own constraint, and AutoMigrate alone never touches a constraint it finds by
// name. The migration has to replace it, or every board over 2000 characters
// stays rejected on a database that has been running.
func TestMigrateAgentStateContentLimitWidensAnOldTable(t *testing.T) {
	db := agentStateTestDB(t)
	user, agent := testAgentStateOwner(t, db)

	// Put the table back the way the previous release left it.
	if err := db.Exec("ALTER TABLE agent_state DROP CONSTRAINT chk_agent_state_content_length").Error; err != nil {
		t.Fatalf("drop the current constraint: %v", err)
	}
	if err := db.Exec("ALTER TABLE agent_state ADD CONSTRAINT chk_agent_state_content_length CHECK (char_length(content) BETWEEN 1 AND 2000)").Error; err != nil {
		t.Fatalf("restore the old constraint: %v", err)
	}

	old := &repository.AgentState{UserID: user.ID, AgentID: agent.ID, Content: strings.Repeat("字", 2001)}
	if err := db.Create(old).Error; err == nil {
		t.Fatal("expected the old constraint to reject a 2001 character board")
	}

	if err := repository.MigrateAgentStateContentLimit(context.Background(), db); err != nil {
		t.Fatalf("migrate the content limit: %v", err)
	}

	widened, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: strings.Repeat("字", repository.MaxStateContentLen)})
	if err != nil {
		t.Fatalf("write a board at the new ceiling: %v", err)
	}
	if utf8.RuneCountInString(widened.Content) != repository.MaxStateContentLen {
		t.Fatalf("stored %d characters, want %d", utf8.RuneCountInString(widened.Content), repository.MaxStateContentLen)
	}

	// Running it again has to leave the same constraint, not fail on a name that
	// is already taken.
	if err := repository.MigrateAgentStateContentLimit(context.Background(), db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

// TestAgentStateSaveTimeIsServerOwned covers the timestamps: created_at stays put
// on an overwrite while updated_at moves.
func TestAgentStateSaveTimeIsServerOwned(t *testing.T) {
	db := agentStateTestDB(t)
	user, agent := testAgentStateOwner(t, db)

	first, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "first"})
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	second, err := SetAgentState(context.Background(), user.ID, SetAgentStateInput{AgentID: agent.ID, Content: "second"})
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("created_at moved from %s to %s", first.CreatedAt, second.CreatedAt)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("updated_at did not move: %s then %s", first.UpdatedAt, second.UpdatedAt)
	}
}
