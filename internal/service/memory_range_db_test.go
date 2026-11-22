package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"rqark-data-cloud/internal/repository"
)

// membership names the rows a search returned, so a failure says which ones
// came back instead of only how many.
func membership(rows []repository.Memory) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Content)
	}
	return names
}

// TestSearchMemoriesBoundsTheWindow covers the range a caller reads a stretch of
// the log with: from and to are both inclusive, and rows outside them are left
// out rather than merely ranked lower.
func TestSearchMemoriesBoundsTheWindow(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	now := time.Now().UTC().Truncate(time.Second)
	oldest := createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindEvent, "ten days ago", now.Add(-10*24*time.Hour))
	middle := createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindFact, "five days ago", now.Add(-5*24*time.Hour))
	newest := createMemoryOfKind(t, user.ID, agent.ID, repository.MemoryKindEvent, "today", now)

	from := now.Add(-7 * 24 * time.Hour)
	to := now.Add(-1 * 24 * time.Hour)

	// Every search records a hit, and a hit lifts a row's decayed score. Reset
	// the counters before each call so one assertion's ranking is not decided by
	// the calls above it.
	resetHits := func() {
		t.Helper()
		if err := db.Model(&repository.Memory{}).
			Where("id IN ?", []uuid.UUID{oldest.ID, middle.ID, newest.ID}).
			Updates(map[string]any{"hits": 0, "last_used_at": nil}).Error; err != nil {
			t.Fatalf("reset hits: %v", err)
		}
	}

	resetHits()
	rows, err := SearchMemories(context.Background(), user.ID, agent.ID, "", nil, &from, &to)
	if err != nil {
		t.Fatalf("search window: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != middle.ID {
		t.Fatalf("search window = %v, want only the row five days old", membership(rows))
	}

	// An upper bound on its own is the older question: what was true before a
	// moment. Both rows older than it come back, newest first.
	resetHits()
	before, err := SearchMemories(context.Background(), user.ID, agent.ID, "", nil, nil, &to)
	if err != nil {
		t.Fatalf("search to: %v", err)
	}
	if len(before) != 2 || before[0].ID != middle.ID || before[1].ID != oldest.ID {
		t.Fatalf("search to = %v, want the two rows at or before it", membership(before))
	}

	// A lower bound alone is the other half of the same question.
	resetHits()
	after, err := SearchMemories(context.Background(), user.ID, agent.ID, "", nil, &from, nil)
	if err != nil {
		t.Fatalf("search from: %v", err)
	}
	if len(after) != 2 || after[0].ID != newest.ID || after[1].ID != middle.ID {
		t.Fatalf("search from = %v, want the two rows at or after it", membership(after))
	}
}

// TestSearchMemoriesRefusesABackwardsWindow covers the one window that is a
// mistake in the request rather than a fact about the log: it is refused, so a
// caller cannot read an empty page as "nothing was recorded then".
func TestSearchMemoriesRefusesABackwardsWindow(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	to := time.Now().UTC()
	from := to.Add(time.Hour)

	_, err := SearchMemories(context.Background(), user.ID, agent.ID, "", nil, &from, &to)
	if err == nil {
		t.Fatal("a window ending before it starts was accepted")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("backwards window = %v, want a ValidationError", err)
	}
}
