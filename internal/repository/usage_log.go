package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UsageLog is the usage_logs table: one row per model call that this service
// proxied to a provider and got a usage report back for.
//
// The table has no foreign keys. It is a fact record, not a child of a session:
// a session may be deleted long before its usage stops mattering, and the row
// has to outlive it. SessionID, AgentID and ProviderID are references only, and
// any of them may point at a row that no longer exists.
//
// The primary key is (ID, CreatedAt) rather than ID alone so that the table can
// be range-partitioned on created_at later without rewriting the key: Postgres
// requires a partition key to be part of every unique constraint.
type UsageLog struct {
	// ID is minted by the service with uuid.NewV7, never by the database. A
	// time-ordered key keeps inserts at the end of the index, which matters on
	// a table that only ever grows.
	ID uuid.UUID `gorm:"type:uuid;primaryKey"`
	// CreatedAt is when the upstream call finished, stored in UTC.
	CreatedAt time.Time `gorm:"not null;primaryKey;index:idx_usage_user_time,priority:2;index:idx_usage_user_model,priority:3;index:idx_usage_user_key,priority:3"`

	UserID uuid.UUID `gorm:"type:uuid;not null;index:idx_usage_user_time,priority:1;index:idx_usage_user_model,priority:1;index:idx_usage_user_key,priority:1"`

	// APIKeyID is set only when the caller authenticated with a rqark API key.
	// It is NULL for access-token callers and is never guessed: a request made
	// with a token has no key behind it.
	APIKeyID *uuid.UUID `gorm:"type:uuid;index:idx_usage_user_key,priority:2"`

	// SessionID is the session the caller named, or NULL when it named none.
	// A value that no longer matches a session row means the session was
	// deleted, which is not the same as never having been named.
	SessionID *uuid.UUID `gorm:"type:uuid;index"`
	// AgentID is the agent the caller named, or NULL.
	AgentID *uuid.UUID `gorm:"type:uuid"`
	// ProviderID is the provider the call went through.
	ProviderID uuid.UUID `gorm:"type:uuid;not null;index"`

	// Source is the surface the caller reported: TUI, AGENT_CLOUD, TASK, and so
	// on. It is stored as it arrives and is not reconciled against
	// sessions.source, which describes a session rather than a call.
	Source string `gorm:"type:varchar(32);not null;default:''"`
	Model  string `gorm:"type:varchar(120);not null;index:idx_usage_user_model,priority:2"`

	// The counters are int32 because one call cannot spend more than 2^31
	// tokens, and the table is kept forever. SUM() widens to bigint at query
	// time, so no precision is lost by the narrow column.
	PromptTokens     int32 `gorm:"not null"`
	CachedTokens     int32 `gorm:"not null;default:0"`
	CompletionTokens int32 `gorm:"not null;default:0"`
	ReasoningTokens  int32 `gorm:"not null;default:0"`
	// TotalTokens is the provider's own total_tokens, or prompt+completion when
	// the provider omitted it. It is never computed as
	// prompt+cached+completion+reasoning: cached tokens are a subset of the
	// prompt and reasoning tokens a subset of the completion, so adding them
	// would count twice.
	TotalTokens int32 `gorm:"not null"`

	// IsCompaction marks a call the caller reported as summarising the
	// conversation to shrink the context. It defaults to false and is never
	// inferred.
	IsCompaction bool `gorm:"not null;default:false"`
}

// TableName pins the table name.
func (UsageLog) TableName() string { return "usage_logs" }

// CreateUsageLog inserts one row. It takes the caller's context, so a caller
// that wants the write to survive its own cancellation passes a detached one.
func CreateUsageLog(ctx context.Context, db *gorm.DB, row *UsageLog) error {
	return db.WithContext(ctx).Create(row).Error
}

// UsageTotal is the aggregate of a column set over a filtered window.
type UsageTotal struct {
	PromptTokens     int64
	CachedTokens     int64
	CompletionTokens int64
	ReasoningTokens  int64
	TotalTokens      int64
	Requests         int64
}

// SumUsageByUser sums one user's usage over [from, to). It exists so a write
// path test can assert that a delete of the session leaves the numbers alone.
func SumUsageByUser(ctx context.Context, db *gorm.DB, userID uuid.UUID, from, to time.Time) (UsageTotal, error) {
	var row struct {
		PromptTokens     int64
		CachedTokens     int64
		CompletionTokens int64
		ReasoningTokens  int64
		TotalTokens      int64
		Requests         int64
	}
	err := db.WithContext(ctx).
		Model(&UsageLog{}).
		Select("COALESCE(sum(prompt_tokens), 0) AS prompt_tokens, "+
			"COALESCE(sum(cached_tokens), 0) AS cached_tokens, "+
			"COALESCE(sum(completion_tokens), 0) AS completion_tokens, "+
			"COALESCE(sum(reasoning_tokens), 0) AS reasoning_tokens, "+
			"COALESCE(sum(total_tokens), 0) AS total_tokens, "+
			"count(*) AS requests").
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, from, to).
		Scan(&row).Error
	if err != nil {
		return UsageTotal{}, err
	}
	return UsageTotal{
		PromptTokens:     row.PromptTokens,
		CachedTokens:     row.CachedTokens,
		CompletionTokens: row.CompletionTokens,
		ReasoningTokens:  row.ReasoningTokens,
		TotalTokens:      row.TotalTokens,
		Requests:         row.Requests,
	}, nil
}
