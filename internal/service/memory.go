package service

import (
	"context"
	"encoding/json"
	"log"
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
	// Kind is 'fact' or 'event'. An empty kind means 'fact': a caller that does
	// not name one is appending to the log the agent reads, which is what
	// memory_save does.
	Kind string
}

// ListMemories returns one page of the caller's memory, newest occurrence
// first. A non-nil agentID is checked against the agents the caller may use
// first, so an agent it may not use is reported as not found.
func ListMemories(ctx context.Context, userID uuid.UUID, agentID *uuid.UUID, kind string, cursor *repository.MemoryCursor, limit int) ([]repository.Memory, error) {
	if kind != "" && !repository.ValidMemoryKind(kind) {
		return nil, &ValidationError{Message: "kind must be 'fact' or 'event'."}
	}
	if agentID != nil {
		if err := requireUsableAgent(ctx, userID, *agentID); err != nil {
			return nil, err
		}
	}
	return repository.ListMemories(ctx, config.DB, userID, agentID, kind, cursor, limit)
}

// GetMemory returns one of the caller's rows, including its source message.
func GetMemory(ctx context.Context, userID, id uuid.UUID) (*repository.Memory, error) {
	return repository.GetMemory(ctx, config.DB, userID, id)
}

// CreateMemory validates and stores one memory record. The agent must be one
// the caller may use: memory is never written into another user's pair, even
// when the agent is shared, so a shared agent stores the caller's own row.
func CreateMemory(ctx context.Context, userID uuid.UUID, in CreateMemoryInput) (*repository.Memory, error) {
	kind := in.Kind
	if kind == "" {
		kind = repository.MemoryKindFact
	}
	if !repository.ValidMemoryKind(kind) {
		return nil, &InvalidContentError{Message: "kind must be 'fact' or 'event'."}
	}
	content, keywords, err := validateMemoryRecord(in.Content, in.Keywords, in.OccurredAt, in.Source)
	if err != nil {
		return nil, err
	}
	if err := requireUsableAgent(ctx, userID, in.AgentID); err != nil {
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
		Kind:          kind,
	}
	if err := repository.CreateMemory(ctx, config.DB, memory); err != nil {
		return nil, err
	}
	return memory, nil
}

// SetMemoryPinned pins or unpins one of the caller's rows. An event cannot be
// pinned: a pin orders the injection prompt, and events never reach it.
func SetMemoryPinned(ctx context.Context, userID, id uuid.UUID, pinned bool) (*repository.Memory, error) {
	memory, err := repository.GetMemory(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}
	if memory.Kind == repository.MemoryKindEvent {
		return nil, &InvalidContentError{Message: "kind 'event' rows cannot be pinned."}
	}
	return repository.SetMemoryPinned(ctx, config.DB, userID, id, pinned)
}

// DeleteMemory removes one of the caller's rows.
func DeleteMemory(ctx context.Context, userID, id uuid.UUID) error {
	return repository.DeleteMemory(ctx, config.DB, userID, id)
}

// PromptMemories returns the rows the injection prompt carries for one agent:
// at most repository.MemoryPromptLimit fact rows, pinned first, then the highest
// decayed score, then the most recent. Events are never injected. Every returned row counts as injected, so
// its hit counter is bumped once per UTC day before the rows are handed back.
func PromptMemories(ctx context.Context, userID, agentID uuid.UUID) ([]repository.Memory, error) {
	if err := requireUsableAgent(ctx, userID, agentID); err != nil {
		return nil, err
	}

	memories, err := repository.PromptMemories(ctx, config.DB, userID, agentID)
	if err != nil {
		return nil, err
	}
	return touchAndReread(ctx, memories)
}

// SearchMemories returns up to repository.MemorySearchLimit rows of one agent,
// facts and events alike. Every returned row counts as injected, so its hit
// counter is bumped once per UTC day before the rows are handed back. from and
// to bound occurred_at, both inclusive; a window that ends before it starts is
// refused rather than answered with an empty page, because it is a mistake in
// the request rather than a fact about the log.
func SearchMemories(ctx context.Context, userID, agentID uuid.UUID, query string, keywords []string, from, to *time.Time) ([]repository.Memory, error) {
	if from != nil && to != nil && to.Before(*from) {
		return nil, &ValidationError{Message: "to must not be before from."}
	}
	if err := requireUsableAgent(ctx, userID, agentID); err != nil {
		return nil, err
	}

	memories, err := repository.SearchMemories(ctx, config.DB, userID, agentID, strings.TrimSpace(query), keywords, from, to)
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

// PruneExpiredEvents deletes the event rows older than repository.EventRetention
// and reports how many went. Facts are never pruned.
func PruneExpiredEvents(ctx context.Context) (int64, error) {
	return repository.DeleteExpiredEvents(ctx, config.DB, time.Now().UTC().Add(-repository.EventRetention))
}

// PruneExpiredEventsEvery prunes once at startup and then every interval, so an
// event's retention does not depend on somebody writing a new one. A failure is
// logged rather than fatal: a stale row costs disk, not correctness.
func PruneExpiredEventsEvery(interval time.Duration) {
	for {
		deleted, err := PruneExpiredEvents(context.Background())
		if err != nil {
			log.Printf("service: prune expired memory events: %v", err)
		} else if deleted > 0 {
			log.Printf("service: pruned %d expired memory events", deleted)
		}
		time.Sleep(interval)
	}
}

// requireUsableAgent reports repository.ErrAgentNotFound unless agentID is one
// userID may use: its own agent or a public one another user shares. A shared
// agent qualifies because memory and state belong to the (user, agent) pair, so
// every user writing through a shared agent writes its own row.
func requireUsableAgent(ctx context.Context, userID, agentID uuid.UUID) error {
	if agentID == uuid.Nil {
		return &ValidationError{Message: "agent_id is required."}
	}
	_, err := repository.GetVisibleAgentByID(ctx, config.DB, userID, agentID)
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
