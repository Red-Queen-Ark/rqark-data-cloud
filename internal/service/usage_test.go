package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestUsageRecordRowKeepsTheNumbers covers the mapping from what the proxy
// observed to the row that is stored: nothing is derived and nothing is added.
func TestUsageRecordRowKeepsTheNumbers(t *testing.T) {
	sessionID, agentID, keyID, providerID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	record := UsageRecord{
		UserID:           uuid.New(),
		APIKeyID:         &keyID,
		SessionID:        &sessionID,
		AgentID:          &agentID,
		ProviderID:       providerID,
		Source:           "TUI",
		Model:            "deepseek-flash",
		PromptTokens:     1200,
		CachedTokens:     800,
		CompletionTokens: 300,
		ReasoningTokens:  0,
		TotalTokens:      1500,
		IsCompaction:     true,
	}

	row := record.row()

	if row.ID == uuid.Nil {
		t.Error("ID = uuid.Nil, want a minted id")
	}
	if row.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
	if row.TotalTokens != 1500 {
		t.Errorf("TotalTokens = %d, want 1500 (never prompt+cached+completion)", row.TotalTokens)
	}
	if row.PromptTokens != 1200 || row.CachedTokens != 800 || row.CompletionTokens != 300 {
		t.Errorf("counters = %d/%d/%d, want 1200/800/300", row.PromptTokens, row.CachedTokens, row.CompletionTokens)
	}
	if row.APIKeyID == nil || *row.APIKeyID != keyID {
		t.Errorf("APIKeyID = %v, want %v", row.APIKeyID, keyID)
	}
	if row.SessionID == nil || *row.SessionID != sessionID {
		t.Errorf("SessionID = %v, want %v", row.SessionID, sessionID)
	}
	if row.Source != "TUI" || row.Model != "deepseek-flash" || !row.IsCompaction {
		t.Errorf("row = %+v, want the tags carried through", row)
	}
}

// TestUsageRecordRowLeavesAbsentReferencesNil covers a call that named no
// session, no agent and used no key: the columns stay NULL rather than zero.
func TestUsageRecordRowLeavesAbsentReferencesNil(t *testing.T) {
	row := UsageRecord{UserID: uuid.New(), ProviderID: uuid.New()}.row()

	if row.SessionID != nil {
		t.Errorf("SessionID = %v, want nil", row.SessionID)
	}
	if row.AgentID != nil {
		t.Errorf("AgentID = %v, want nil", row.AgentID)
	}
	if row.APIKeyID != nil {
		t.Errorf("APIKeyID = %v, want nil", row.APIKeyID)
	}
	if row.IsCompaction {
		t.Error("IsCompaction = true, want the default false")
	}
}

// TestUsageIDsAreDistinctAndOrdered covers the id choice: every row gets its own
// id, and consecutive ids sort in creation order.
func TestUsageIDsAreDistinctAndOrdered(t *testing.T) {
	first := UsageRecord{}.row().ID
	second := UsageRecord{}.row().ID

	if first == second {
		t.Fatal("two rows got the same id")
	}
	if first.String() >= second.String() {
		t.Errorf("ids are not time-ordered: %s then %s", first, second)
	}
}

// TestClampTokens covers the column range: a negative or oversized count cannot
// reach an int32 column badly.
func TestClampTokens(t *testing.T) {
	cases := map[int]int32{
		-1:      0,
		0:       0,
		42:      42,
		1 << 40: 1<<31 - 1,
	}
	for value, want := range cases {
		if got := clampTokens(value); got != want {
			t.Errorf("clampTokens(%d) = %d, want %d", value, got, want)
		}
	}
}

// TestRecordUsageWithoutARecorderDrops covers the safe default: a process that
// never started the writers loses the record instead of blocking.
func TestRecordUsageWithoutARecorderDrops(t *testing.T) {
	before := UsageDropped()
	RecordUsage(context.Background(), UsageRecord{UserID: uuid.New(), ProviderID: uuid.New()})
	if got := UsageDropped(); got != before+1 {
		t.Errorf("dropped = %d, want %d", got, before+1)
	}
}

// TestTruncate caps an over-long label instead of failing the insert.
func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate = %q, want the value unchanged", got)
	}
	if got := truncate("abcdefghij", 4); got != "abcd" {
		t.Errorf("truncate = %q, want abcd", got)
	}
}
