package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

const (
	// maxMemoryContentLen is the limit the content CHECK constraint enforces.
	maxMemoryContentLen = 200
	// maxMemoryKeywords is the limit the keywords CHECK constraint enforces.
	maxMemoryKeywords = 8
	// maxMemorySourceLen is the width of the source column.
	maxMemorySourceLen = 32
)

// InvalidContentError is a memory record that cannot be stored: an empty or
// over-long content, too many keywords, a missing occurrence time or an
// over-long source. It is reported with its own code so the caller can tell a
// badly shaped record from a malformed request.
type InvalidContentError struct {
	Message string
}

// Error returns the message shown to the client.
func (e *InvalidContentError) Error() string { return e.Message }

// CreateMemoryInput is the body accepted by POST /api/memory.
type CreateMemoryInput struct {
	AgentID       uuid.UUID
	Content       string
	Keywords      []string
	Source        string
	SourceMessage json.RawMessage
	OccurredAt    time.Time
	Pinned        bool
}

// ListMemories returns one page of the caller's memory, newest occurrence
// first. A non-nil agentID is checked against the caller's own agents first, so
// another user's agent is reported as not found.
func ListMemories(ctx context.Context, userID uuid.UUID, agentID *uuid.UUID, cursor *repository.MemoryCursor, limit int) ([]repository.Memory, error) {
	if agentID != nil {
		if err := requireOwnedAgent(ctx, userID, *agentID); err != nil {
			return nil, err
		}
	}
	return repository.ListMemories(ctx, config.DB, userID, agentID, cursor, limit)
}

// GetMemory returns one of the caller's rows, including its source message.
func GetMemory(ctx context.Context, userID, id uuid.UUID) (*repository.Memory, error) {
	return repository.GetMemory(ctx, config.DB, userID, id)
}

// CreateMemory validates and stores one memory record. The agent must belong to
// the caller: memory is never written into another user's pair, even when the
// agent is shared.
func CreateMemory(ctx context.Context, userID uuid.UUID, in CreateMemoryInput) (*repository.Memory, error) {
	content, keywords, err := validateMemoryRecord(in.Content, in.Keywords, in.OccurredAt, in.Source)
	if err != nil {
		return nil, err
	}
	if err := requireOwnedAgent(ctx, userID, in.AgentID); err != nil {
		return nil, err
	}

	memory := &repository.Memory{
		ID:            uuid.New(),
		UserID:        userID,
		AgentID:       in.AgentID,
		Content:       content,
		Keywords:      keywords,
		Source:        in.Source,
		SourceMessage: in.SourceMessage,
		OccurredAt:    in.OccurredAt.UTC(),
		Pinned:        in.Pinned,
	}
	if err := repository.CreateMemory(ctx, config.DB, memory); err != nil {
		return nil, err
	}
	return memory, nil
}

// SetMemoryPinned pins or unpins one of the caller's rows.
func SetMemoryPinned(ctx context.Context, userID, id uuid.UUID, pinned bool) (*repository.Memory, error) {
	return repository.SetMemoryPinned(ctx, config.DB, userID, id, pinned)
}

// DeleteMemory removes one of the caller's rows.
func DeleteMemory(ctx context.Context, userID, id uuid.UUID) error {
	return repository.DeleteMemory(ctx, config.DB, userID, id)
}

// PromptMemories returns the rows the injection prompt carries for one agent:
// at most repository.MemoryPromptLimit rows, pinned first, then the highest
// decayed score, then the most recent. Every returned row counts as injected, so
// its hit counter is bumped once per UTC day before the rows are handed back.
func PromptMemories(ctx context.Context, userID, agentID uuid.UUID) ([]repository.Memory, error) {
	if err := requireOwnedAgent(ctx, userID, agentID); err != nil {
		return nil, err
	}

	memories, err := repository.PromptMemories(ctx, config.DB, userID, agentID)
	if err != nil {
		return nil, err
	}
	return touchAndReread(ctx, memories)
}

// SearchMemories returns up to repository.MemorySearchLimit rows of one agent.
// Every returned row counts as injected, so its hit counter is bumped once per
// UTC day before the rows are handed back.
func SearchMemories(ctx context.Context, userID, agentID uuid.UUID, query string, keywords []string, at *time.Time) ([]repository.Memory, error) {
	if err := requireOwnedAgent(ctx, userID, agentID); err != nil {
		return nil, err
	}

	memories, err := repository.SearchMemories(ctx, config.DB, userID, agentID, strings.TrimSpace(query), keywords, at)
	if err != nil {
		return nil, err
	}
	return touchAndReread(ctx, memories)
}

// touchAndReread records one hit for each row and returns the rows in the order
// they were ranked, carrying the counters the increment just wrote.
func touchAndReread(ctx context.Context, memories []repository.Memory) ([]repository.Memory, error) {
	if len(memories) == 0 {
		return memories, nil
	}

	ids := make([]uuid.UUID, 0, len(memories))
	for i := range memories {
		ids = append(ids, memories[i].ID)
	}
	if err := repository.TouchMemoryHits(ctx, config.DB, ids); err != nil {
		return nil, err
	}
	return repository.GetMemoriesInOrder(ctx, config.DB, ids)
}

// requireOwnedAgent reports repository.ErrAgentNotFound unless agentID is one of
// userID's own agents. A shared agent does not qualify: memory belongs to the
// pair, and the writer of the pair is the owner.
func requireOwnedAgent(ctx context.Context, userID, agentID uuid.UUID) error {
	if agentID == uuid.Nil {
		return &ValidationError{Message: "agent_id is required."}
	}
	_, err := repository.GetAgentByID(ctx, config.DB, userID, agentID)
	return err
}

// validateMemoryRecord checks a record against the column limits and returns the
// normalized content and keywords. Content is stored as sent; keywords are
// trimmed and empty entries dropped, since an empty keyword can never match.
func validateMemoryRecord(content string, keywords []string, occurredAt time.Time, source string) (string, repository.StringArray, error) {
	if strings.TrimSpace(content) == "" {
		return "", nil, &InvalidContentError{Message: "content is required."}
	}
	if utf8.RuneCountInString(content) > maxMemoryContentLen {
		return "", nil, &InvalidContentError{Message: "content must be at most 200 characters."}
	}
	if occurredAt.IsZero() {
		return "", nil, &InvalidContentError{Message: "occurred_at is required."}
	}
	if len(source) > maxMemorySourceLen {
		return "", nil, &InvalidContentError{Message: "source must be at most 32 characters."}
	}

	normalized := make(repository.StringArray, 0, len(keywords))
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword != "" {
			normalized = append(normalized, keyword)
		}
	}
	if len(normalized) > maxMemoryKeywords {
		return "", nil, &InvalidContentError{Message: "keywords must hold at most 8 entries."}
	}
	return content, normalized, nil
}
