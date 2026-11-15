package service

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// UsageRecord is the token usage of one chat completion the proxy relayed, as
// observed at the proxy. It is the payload of the write path: the proxy builds
// one of these and hands it over, and everything after that is this file's
// problem.
type UsageRecord struct {
	UserID     uuid.UUID
	APIKeyID   *uuid.UUID
	SessionID  *uuid.UUID
	AgentID    *uuid.UUID
	ProviderID uuid.UUID
	Source     string
	Model      string

	PromptTokens     int
	CachedTokens     int
	CompletionTokens int
	ReasoningTokens  int
	TotalTokens      int

	IsCompaction bool
}

// UsageQueueDepth is how many records may wait to be written. A record is
// dropped once the queue is full: a chat must never block on its own
// bookkeeping.
const UsageQueueDepth = 1024

// usageWorkerCount is how many writers drain the queue. Writes are single-row
// inserts, so two is already more than a normal deployment needs.
const usageWorkerCount = 2

// usageWriteTimeout bounds one insert. The record is dropped if the database
// does not answer in this window.
const usageWriteTimeout = 5 * time.Second

// usageItem carries a record together with the context it was observed under.
// The context has its cancellation detached, so the insert still runs when the
// request that produced it has already finished.
type usageItem struct {
	ctx context.Context
	row repository.UsageLog
}

var (
	usageQueue     chan usageItem
	usageStartOnce sync.Once
	usageDropped   atomic.Int64
)

// StartUsageRecorder starts the background writers. Calling it more than once
// is harmless. Until it is called every record is dropped, which is what keeps
// tests and tools that never start it from needing a database.
func StartUsageRecorder() {
	usageStartOnce.Do(func() {
		usageQueue = make(chan usageItem, UsageQueueDepth)
		for i := 0; i < usageWorkerCount; i++ {
			go usageWorker()
		}
	})
}

// UsageDropped reports how many records have been dropped because the queue was
// full or the recorder was never started. It exists so the loss is visible
// rather than silent.
func UsageDropped() int64 { return usageDropped.Load() }

// RecordUsage queues one record for writing. It never blocks and never returns
// an error: usage is a side record of a chat, not part of what the chat asked
// for, so a failure to store it must not surface to the caller.
func RecordUsage(ctx context.Context, rec UsageRecord) {
	if usageQueue == nil {
		usageDropped.Add(1)
		return
	}

	item := usageItem{ctx: context.WithoutCancel(ctx), row: rec.row()}
	select {
	case usageQueue <- item:
	default:
		usageDropped.Add(1)
	}
}

// usageWorker drains the queue, writing one record at a time.
func usageWorker() {
	for item := range usageQueue {
		ctx, cancel := context.WithTimeout(item.ctx, usageWriteTimeout)
		row := item.row
		err := repository.CreateUsageLog(ctx, config.DB, &row)
		cancel()
		if err != nil {
			log.Printf("service: write usage log %s: %v", item.row.ID, err)
		}
	}
}

// row converts a record into the row that is stored. The id is minted here, and
// the timestamp is the moment the observation was handed over: the provider has
// already answered, so this is when the tokens were spent.
func (rec UsageRecord) row() repository.UsageLog {
	return repository.UsageLog{
		ID:               newUsageID(),
		CreatedAt:        time.Now().UTC(),
		UserID:           rec.UserID,
		APIKeyID:         rec.APIKeyID,
		SessionID:        rec.SessionID,
		AgentID:          rec.AgentID,
		ProviderID:       rec.ProviderID,
		Source:           truncate(rec.Source, 32),
		Model:            truncate(rec.Model, 120),
		PromptTokens:     clampTokens(rec.PromptTokens),
		CachedTokens:     clampTokens(rec.CachedTokens),
		CompletionTokens: clampTokens(rec.CompletionTokens),
		ReasoningTokens:  clampTokens(rec.ReasoningTokens),
		TotalTokens:      clampTokens(rec.TotalTokens),
		IsCompaction:     rec.IsCompaction,
	}
}

// newUsageID mints a time-ordered uuid. The random source failing is not worth
// ending a process over, so a v4 is the fallback.
func newUsageID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return id
}

// clampTokens keeps a counter inside the column's range. A provider reporting a
// negative number is reporting nonsense; zero is the closest honest value.
func clampTokens(value int) int32 {
	if value < 0 {
		return 0
	}
	if value > 1<<31-1 {
		return 1<<31 - 1
	}
	return int32(value)
}

// truncate caps a string at the column's width. A value past the limit is cut
// rather than allowed to fail the insert: losing the tail of a label is better
// than losing the row that carries the tokens.
func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
