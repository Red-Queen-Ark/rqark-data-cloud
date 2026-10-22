package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// createMemoryOfKind writes one row of the named kind, occurred at now.
func createMemoryOfKind(t *testing.T, userID, agentID uuid.UUID, kind, content string, occurredAt time.Time) *repository.Memory {
	t.Helper()

	memory, err := CreateMemory(context.Background(), userID, CreateMemoryInput{
		AgentID:    agentID,
		Content:    content,
		Kind:       kind,
		OccurredAt: occurredAt,
		Source:     "TUI",
	})
	if err != nil {
		t.Fatalf("create %s memory: %v", kind, err)
	}
	return memory
}

// TestCreateMemoryDefaultsToFact covers the backwards-compatible default: a
// caller that does not name a kind writes a fact, which is what memory_save does.
func TestCreateMemoryDefaultsToFact(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	memory, err := CreateMemory(context.Background(), user.ID, CreateMemoryInput{AgentID: agent.ID, Content: "a fact", OccurredAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	if memory.Kind != repository.MemoryKindFact {
		t.Fatalf("kind = %q, want fact", memory.Kind)
	}

	_, err = CreateMemory(context.Background(), user.ID, CreateMemoryInput{AgentID: agent.ID, Content: "a rumour", Kind: "rumour", OccurredAt: time.Now().UTC()})
	var invalid *InvalidContentError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected InvalidContentError for an unknown kind, got %v", err)
	}
}

// TestMemoryKindIsInTheSchema covers the CHECK constraint, which guards the
// column even when a write skips the service.
func TestMemoryKindIsInTheSchema(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	row := &repository.Memory{ID: uuid.New(), UserID: user.ID, AgentID: agent.ID, Content: "row", Kind: "rumour", OccurredAt: time.Now().UTC()}
	if err := db.Create(row).Error; err == nil {
		t.Fatal("expected the CHECK constraint to reject an unknown kind")
	}
}

// TestPromptMemoriesCarriesFactsOnly covers the window: however many events the
// turns leave behind, the injected rows stay facts.
func TestPromptMemoriesCarriesFactsOnly(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	now := time.Now().UTC()
	fact := createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindFact, "Boss works in UTC+8", now)
	for i := 0; i < 100; i++ {
		createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindEvent, "Boss asked for a turn", now.Add(time.Duration(i)*time.Minute))
	}

	results, err := PromptMemories(context.Background(), user.ID, agent.ID)
	if err != nil {
		t.Fatalf("prompt memory: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("prompt returned %d rows, want 1", len(results))
	}
	if results[0].ID != fact.ID {
		t.Fatalf("prompt returned %s, want the fact", results[0].ID)
	}
}

// TestSearchMemoriesFindsEvents covers the other half of the split: an event is
// not injected, but search still reaches it and says which kind it is.
func TestSearchMemoriesFindsEvents(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	event := createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindEvent, "Boss 叫 agent 修好 memory writer", time.Now().UTC())
	createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindFact, "Boss prefers short replies", time.Now().UTC())

	results, err := SearchMemories(context.Background(), user.ID, agent.ID, "writer", nil, nil, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].ID != event.ID {
		t.Fatalf("search returned %d rows, want the event", len(results))
	}
	if results[0].Kind != repository.MemoryKindEvent {
		t.Fatalf("kind = %q, want event", results[0].Kind)
	}
}

// TestListMemoriesFiltersByKind covers the listing filter: the default holds
// both kinds, and ?kind= narrows the page.
func TestListMemoriesFiltersByKind(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	now := time.Now().UTC()
	createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindFact, "a fact", now)
	createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindEvent, "an event", now.Add(time.Second))

	all, err := ListMemories(context.Background(), user.ID, &agent.ID, "", nil, 10)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("list all returned %d rows, want 2", len(all))
	}

	events, err := ListMemories(context.Background(), user.ID, &agent.ID, repository.MemoryKindEvent, nil, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != repository.MemoryKindEvent {
		t.Fatalf("list events returned %+v", events)
	}

	if _, err := ListMemories(context.Background(), user.ID, &agent.ID, "rumour", nil, 10); err == nil {
		t.Fatal("expected an unknown kind to be refused")
	}
}

// TestEventCannotBePinned covers the pin rule: pinned orders the injection
// prompt, and events never reach it.
func TestEventCannotBePinned(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	event := createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindEvent, "an event", time.Now().UTC())

	_, err := SetMemoryPinned(context.Background(), user.ID, event.ID, true)
	var invalid *InvalidContentError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected InvalidContentError, got %v", err)
	}

	var stored repository.Memory
	if err := db.First(&stored, "id = ?", event.ID).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Pinned {
		t.Fatal("the refused pin still pinned the row")
	}
}

// TestPruneExpiredEvents covers retention: events past 90 days go, newer events
// and every fact stay.
func TestPruneExpiredEvents(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	now := time.Now().UTC()
	oldEvent := createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindEvent, "an old event", now.Add(-repository.EventRetention-time.Hour))
	newEvent := createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindEvent, "a new event", now.Add(-repository.EventRetention+time.Hour))
	oldFact := createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindFact, "an old fact", now.Add(-365*24*time.Hour))

	deleted, err := PruneExpiredEvents(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted < 1 {
		t.Fatalf("pruned %d rows, want at least the old event", deleted)
	}

	var gone int64
	if err := db.Model(&repository.Memory{}).Where("id = ?", oldEvent.ID).Count(&gone).Error; err != nil {
		t.Fatalf("count old event: %v", err)
	}
	if gone != 0 {
		t.Fatal("the old event survived the prune")
	}
	for _, kept := range []*repository.Memory{newEvent, oldFact} {
		var count int64
		if err := db.Model(&repository.Memory{}).Where("id = ?", kept.ID).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", kept.ID, err)
		}
		if count != 1 {
			t.Fatalf("row %s (%s) was pruned, want it kept", kept.ID, kept.Kind)
		}
	}
}
