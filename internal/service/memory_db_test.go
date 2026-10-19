package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// memoryTestDB opens the shared test database and installs the memory tables on
// top of it. AutoMigrate only writes the CHECK constraint while it creates the
// table, so a memory table left behind by an earlier shape (before kind existed)
// is rebuilt first: the tests below would otherwise run against a table without
// the constraint they assert.
func memoryTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := testDatabase(t)
	if db.Migrator().HasTable(&repository.Memory{}) && !db.Migrator().HasConstraint(&repository.Memory{}, "chk_memory_kind") {
		if err := db.Migrator().DropTable(&repository.Memory{}); err != nil {
			t.Fatalf("drop stale memory table: %v", err)
		}
	}
	if err := db.AutoMigrate(&repository.User{}, &repository.Agent{}, &repository.Memory{}); err != nil {
		t.Fatalf("automigrate memory: %v", err)
	}
	return db
}

// testMemoryOwner inserts a user and one agent it owns, and removes both, their
// memory included, afterwards.
func testMemoryOwner(t *testing.T, db *gorm.DB) (*repository.User, *repository.Agent) {
	t.Helper()

	id := uuid.New()
	user := &repository.User{
		ID:             id,
		Username:       "memtest-" + id.String(),
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
		db.Where("user_id = ?", id).Delete(&repository.Memory{})
		db.Delete(&repository.Agent{}, "id = ?", agent.ID)
		db.Delete(&repository.User{}, "id = ?", id)
	})
	return user, agent
}

// TestMemoryKeywordsRoundTrip covers the text[] column: Postgres prints the
// array literal itself, so every escaping shape has to survive the read back.
func TestMemoryKeywordsRoundTrip(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	keywords := []string{"plain", "comma,inside", `quote"inside`, `back\slash`, "{}"}
	memory, err := CreateMemory(context.Background(), user.ID, CreateMemoryInput{
		AgentID:    agent.ID,
		Content:    "keyword round trip",
		Keywords:   keywords,
		Source:     "TUI",
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	if len(memory.Keywords) != len(keywords) {
		t.Fatalf("got %d keywords, want %d", len(memory.Keywords), len(keywords))
	}
	for i, want := range keywords {
		if memory.Keywords[i] != want {
			t.Fatalf("keyword %d = %q, want %q", i, memory.Keywords[i], want)
		}
	}
}

// TestMemoryContentLimits covers the service-side validation the CHECK
// constraints also enforce.
func TestMemoryContentLimits(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	base := CreateMemoryInput{
		AgentID:    agent.ID,
		Source:     "TUI",
		OccurredAt: time.Now().UTC(),
	}

	tooLong := base
	tooLong.Content = strings.Repeat("字", 201)
	if _, err := CreateMemory(context.Background(), user.ID, tooLong); err == nil {
		t.Fatal("expected over-long content to be rejected")
	}

	empty := base
	empty.Content = "   "
	if _, err := CreateMemory(context.Background(), user.ID, empty); err == nil {
		t.Fatal("expected empty content to be rejected")
	}

	tooMany := base
	tooMany.Content = "too many keywords"
	tooMany.Keywords = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
	_, err := CreateMemory(context.Background(), user.ID, tooMany)
	var invalid *InvalidContentError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected InvalidContentError, got %v", err)
	}
}

// TestMemoryPinnedLimit covers the transaction that counts pinned rows under an
// advisory lock: the sixth pin is refused and the first five stay pinned.
func TestMemoryPinnedLimit(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	ids := make([]uuid.UUID, 0, 6)
	for i := 0; i < 6; i++ {
		memory, err := CreateMemory(context.Background(), user.ID, CreateMemoryInput{
			AgentID:    agent.ID,
			Content:    "pin me",
			OccurredAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("create memory: %v", err)
		}
		ids = append(ids, memory.ID)
	}
	for i := 0; i < 5; i++ {
		if _, err := SetMemoryPinned(context.Background(), user.ID, ids[i], true); err != nil {
			t.Fatalf("pin %d: %v", i, err)
		}
	}
	if _, err := SetMemoryPinned(context.Background(), user.ID, ids[5], true); !errors.Is(err, repository.ErrPinnedLimit) {
		t.Fatalf("expected ErrPinnedLimit, got %v", err)
	}
	if _, err := SetMemoryPinned(context.Background(), user.ID, ids[0], false); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if _, err := SetMemoryPinned(context.Background(), user.ID, ids[5], true); err != nil {
		t.Fatalf("pin after unpin: %v", err)
	}
}

// TestMemoryListCursor covers the keyset page: a cursor resumes strictly after
// the row it names, and two pages together hold every row once.
func TestMemoryListCursor(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	base := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 5; i++ {
		if _, err := CreateMemory(context.Background(), user.ID, CreateMemoryInput{
			AgentID:    agent.ID,
			Content:    "page row",
			OccurredAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("create memory: %v", err)
		}
	}

	first, err := ListMemories(context.Background(), user.ID, &agent.ID, "", nil, 3)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("first page has %d rows, want 3", len(first))
	}

	cursor := &repository.MemoryCursor{OccurredAt: first[len(first)-1].OccurredAt, ID: first[len(first)-1].ID}
	second, err := ListMemories(context.Background(), user.ID, &agent.ID, "", cursor, 3)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second) != 2 {
		t.Fatalf("second page has %d rows, want 2", len(second))
	}

	seen := map[uuid.UUID]bool{}
	for _, row := range append(first, second...) {
		if seen[row.ID] {
			t.Fatalf("row %s served twice", row.ID)
		}
		seen[row.ID] = true
	}
	if len(seen) != 5 {
		t.Fatalf("saw %d rows, want 5", len(seen))
	}
}

// TestMemoryPromptOrdering covers the injection window: pinned rows come first
// whatever their age, and the rest follow the decayed score, so an old row that
// was injected often still beats a fresh row that never was.
func TestMemoryPromptOrdering(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	now := time.Now().UTC()
	oldPinned, err := CreateMemory(context.Background(), user.ID, CreateMemoryInput{
		AgentID:    agent.ID,
		Content:    "old but pinned",
		OccurredAt: now.Add(-365 * 24 * time.Hour),
		Pinned:     true,
	})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	oldHot, err := CreateMemory(context.Background(), user.ID, CreateMemoryInput{AgentID: agent.ID, Content: "old and hot", OccurredAt: now.Add(-10 * 24 * time.Hour)})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	fresh, err := CreateMemory(context.Background(), user.ID, CreateMemoryInput{AgentID: agent.ID, Content: "fresh and cold", OccurredAt: now})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := repository.TouchMemoryHits(context.Background(), db, []uuid.UUID{oldHot.ID}); err != nil {
			t.Fatalf("touch hits: %v", err)
		}
		if err := db.Model(&repository.Memory{}).Where("id = ?", oldHot.ID).Update("last_used_at", now.Add(-time.Duration(i+1)*24*time.Hour)).Error; err != nil {
			t.Fatalf("age last_used_at: %v", err)
		}
	}

	results, err := PromptMemories(context.Background(), user.ID, agent.ID)
	if err != nil {
		t.Fatalf("prompt memory: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("prompt returned %d rows, want 3", len(results))
	}
	if results[0].ID != oldPinned.ID {
		t.Fatalf("first row is %s, want the pinned one", results[0].ID)
	}
	if results[1].ID != oldHot.ID || results[2].ID != fresh.ID {
		t.Fatalf("order was %s, %s, want the hot old row before the fresh one", results[1].ID, results[2].ID)
	}
}

// TestMemoryPromptCountsHitsOncePerDay covers the daily guard on the prompt
// path: the rows are injected again on every turn, but only one hit a day lands.
func TestMemoryPromptCountsHitsOncePerDay(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	if _, err := CreateMemory(context.Background(), user.ID, CreateMemoryInput{AgentID: agent.ID, Content: "a fact", OccurredAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create memory: %v", err)
	}

	first, err := PromptMemories(context.Background(), user.ID, agent.ID)
	if err != nil {
		t.Fatalf("first prompt: %v", err)
	}
	if first[0].Hits != 1 {
		t.Fatalf("hits after first prompt = %d, want 1", first[0].Hits)
	}
	second, err := PromptMemories(context.Background(), user.ID, agent.ID)
	if err != nil {
		t.Fatalf("second prompt: %v", err)
	}
	if second[0].Hits != 1 {
		t.Fatalf("hits after second prompt = %d, want 1", second[0].Hits)
	}
}

// TestMemorySearchRanksAndCountsHits covers the ranking (keyword hits first) and
// the daily hit guard: a second injection on the same UTC day does not add.
func TestMemorySearchRanksAndCountsHits(t *testing.T) {
	db := memoryTestDB(t)
	user, agent := testMemoryOwner(t, db)

	now := time.Now().UTC()
	both, err := CreateMemory(context.Background(), user.ID, CreateMemoryInput{
		AgentID:    agent.ID,
		Content:    "both keywords",
		Keywords:   []string{"alpha", "beta"},
		OccurredAt: now.Add(-48 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	one, err := CreateMemory(context.Background(), user.ID, CreateMemoryInput{
		AgentID:    agent.ID,
		Content:    "one keyword",
		Keywords:   []string{"alpha"},
		OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}

	results, err := SearchMemories(context.Background(), user.ID, agent.ID, "", []string{"alpha", "beta"}, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("search returned %d rows, want 2", len(results))
	}
	if results[0].ID != both.ID || results[1].ID != one.ID {
		t.Fatalf("ranking was %s then %s, want the two-keyword row first", results[0].ID, results[1].ID)
	}
	if results[0].Hits != 1 || results[1].Hits != 1 {
		t.Fatalf("hits after first search = %d,%d, want 1,1", results[0].Hits, results[1].Hits)
	}

	again, err := SearchMemories(context.Background(), user.ID, agent.ID, "", []string{"alpha", "beta"}, nil)
	if err != nil {
		t.Fatalf("second search: %v", err)
	}
	for _, row := range again {
		if row.Hits != 1 {
			t.Fatalf("hit counted twice in one day: %s has %d", row.ID, row.Hits)
		}
	}
}
