package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// shareAgent marks an agent public, the way a superuser does, and undoes it
// afterwards.
func shareAgent(t *testing.T, db *gorm.DB, agent *repository.Agent) {
	t.Helper()

	if err := db.Model(&repository.Agent{}).Where("id = ?", agent.ID).Update("is_public", true).Error; err != nil {
		t.Fatalf("share agent: %v", err)
	}
	t.Cleanup(func() {
		db.Model(&repository.Agent{}).Where("id = ?", agent.ID).Update("is_public", false)
	})
}

// TestMemorySharedAgentKeepsPairsApart covers the case a shared agent creates:
// another user may read and write through it, but every row still belongs to
// its own (user, agent) pair, so neither side sees the other's rows.
func TestMemorySharedAgentKeepsPairsApart(t *testing.T) {
	db := memoryTestDB(t)
	owner, shared := testMemoryOwner(t, db)
	shareAgent(t, db, shared)
	stranger, _ := testMemoryOwner(t, db)

	ownerRow, err := CreateMemory(context.Background(), owner.ID, CreateMemoryInput{
		AgentID:    shared.ID,
		Content:    "owner fact",
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("owner write through the shared agent: %v", err)
	}
	strangerRow, err := CreateMemory(context.Background(), stranger.ID, CreateMemoryInput{
		AgentID:    shared.ID,
		Content:    "stranger fact",
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("stranger write through the shared agent: %v", err)
	}

	strangerRows, err := ListMemories(context.Background(), stranger.ID, &shared.ID, "", nil, 10)
	if err != nil {
		t.Fatalf("stranger list: %v", err)
	}
	if len(strangerRows) != 1 || strangerRows[0].ID != strangerRow.ID {
		t.Fatalf("stranger list = %+v, want only its own row", strangerRows)
	}

	ownerRows, err := ListMemories(context.Background(), owner.ID, &shared.ID, "", nil, 10)
	if err != nil {
		t.Fatalf("owner list: %v", err)
	}
	if len(ownerRows) != 1 || ownerRows[0].ID != ownerRow.ID {
		t.Fatalf("owner list = %+v, want only its own row", ownerRows)
	}

	found, err := SearchMemories(context.Background(), stranger.ID, shared.ID, "fact", nil, nil)
	if err != nil {
		t.Fatalf("stranger search: %v", err)
	}
	if len(found) != 1 || found[0].ID != strangerRow.ID {
		t.Fatalf("stranger search = %+v, want only its own row", found)
	}

	injected, err := PromptMemories(context.Background(), stranger.ID, shared.ID)
	if err != nil {
		t.Fatalf("stranger prompt: %v", err)
	}
	if len(injected) != 1 || injected[0].ID != strangerRow.ID {
		t.Fatalf("stranger prompt = %+v, want only its own row", injected)
	}

	if _, err := GetMemory(context.Background(), stranger.ID, ownerRow.ID); !errors.Is(err, repository.ErrMemoryNotFound) {
		t.Fatalf("stranger read of the owner's row: expected ErrMemoryNotFound, got %v", err)
	}
	if err := DeleteMemory(context.Background(), stranger.ID, ownerRow.ID); !errors.Is(err, repository.ErrMemoryNotFound) {
		t.Fatalf("stranger delete of the owner's row: expected ErrMemoryNotFound, got %v", err)
	}
}

// TestMemoryPrivateAgentStaysOwnerOnly covers the boundary that survives the
// change: an agent nobody shared is still reported as not found to another
// user, on read and on write alike.
func TestMemoryPrivateAgentStaysOwnerOnly(t *testing.T) {
	db := memoryTestDB(t)
	owner, agent := testMemoryOwner(t, db)
	stranger, _ := testMemoryOwner(t, db)

	if _, err := CreateMemory(context.Background(), stranger.ID, CreateMemoryInput{
		AgentID:    agent.ID,
		Content:    "hijack",
		OccurredAt: time.Now().UTC(),
	}); !errors.Is(err, repository.ErrAgentNotFound) {
		t.Fatalf("stranger write: expected ErrAgentNotFound, got %v", err)
	}
	if _, err := ListMemories(context.Background(), stranger.ID, &agent.ID, "", nil, 10); !errors.Is(err, repository.ErrAgentNotFound) {
		t.Fatalf("stranger list: expected ErrAgentNotFound, got %v", err)
	}
	if _, err := SearchMemories(context.Background(), stranger.ID, agent.ID, "hijack", nil, nil); !errors.Is(err, repository.ErrAgentNotFound) {
		t.Fatalf("stranger search: expected ErrAgentNotFound, got %v", err)
	}
	if _, err := PromptMemories(context.Background(), stranger.ID, agent.ID); !errors.Is(err, repository.ErrAgentNotFound) {
		t.Fatalf("stranger prompt: expected ErrAgentNotFound, got %v", err)
	}
	if _, err := PromptMemories(context.Background(), owner.ID, agent.ID); err != nil {
		t.Fatalf("owner prompt: %v", err)
	}
}

// TestAgentStateSharedAgentKeepsPairsApart covers the board on a shared agent:
// each user writes and reads its own board, and the owner's board is untouched
// by a stranger's write.
func TestAgentStateSharedAgentKeepsPairsApart(t *testing.T) {
	db := agentStateTestDB(t)
	owner, shared := testAgentStateOwner(t, db)
	shareAgent(t, db, shared)
	stranger, _ := testAgentStateOwner(t, db)

	if _, err := SetAgentState(context.Background(), owner.ID, SetAgentStateInput{AgentID: shared.ID, Content: "owner board"}); err != nil {
		t.Fatalf("owner write: %v", err)
	}
	if _, err := SetAgentState(context.Background(), stranger.ID, SetAgentStateInput{AgentID: shared.ID, Content: "stranger board"}); err != nil {
		t.Fatalf("stranger write: %v", err)
	}

	strangerState, err := GetAgentState(context.Background(), stranger.ID, shared.ID)
	if err != nil {
		t.Fatalf("stranger read: %v", err)
	}
	if strangerState.Content != "stranger board" {
		t.Fatalf("stranger board = %q, want its own", strangerState.Content)
	}

	ownerState, err := GetAgentState(context.Background(), owner.ID, shared.ID)
	if err != nil {
		t.Fatalf("owner read: %v", err)
	}
	if ownerState.Content != "owner board" {
		t.Fatalf("owner board = %q, want its own", ownerState.Content)
	}
}
