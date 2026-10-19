package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrStateNotFound is returned when the (user, agent) pair has no state row.
var ErrStateNotFound = errors.New("repository: agent state not found")

// ErrStateConflict is returned when a write carries a version the row no longer
// holds: another writer got there first.
var ErrStateConflict = errors.New("repository: agent state version conflict")

// MaxStateContentLen is the limit the content CHECK constraint enforces.
const MaxStateContentLen = 2000

// AgentState is the state board: one overwritable row per (user, agent) pair.
//
// It is deliberately not a memory row. Memory is an append-only log whose rows
// are capped at 200 characters and counted against a 20-row prompt window; this
// table holds the current situation instead, is overwritten in place, and is
// written by the agent as well as the user.
type AgentState struct {
	UserID  uuid.UUID `gorm:"type:uuid;primaryKey"`
	AgentID uuid.UUID `gorm:"type:uuid;primaryKey"`
	// Content is capped at 2000 characters by a CHECK constraint.
	Content string `gorm:"type:text;not null;check:chk_agent_state_content_length,char_length(content) BETWEEN 1 AND 2000"`
	// Version counts writes: the first row is 1 and every upsert adds one. A
	// writer that sends an expected version is refused when it falls behind.
	Version int `gorm:"type:integer;not null;default:1"`
	// Source is the platform the row was last written from: TUI or AGENT_CLOUD.
	Source    string    `gorm:"type:varchar(32);not null;default:''"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	// User and Agent only exist to tie the two ids to their tables with
	// cascading foreign keys. They are never loaded or written through this
	// struct.
	User  User  `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
	Agent Agent `gorm:"foreignKey:AgentID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName pins the table name to the singular used by the design.
func (AgentState) TableName() string { return "agent_state" }

// GetAgentState loads the state of one (user, agent) pair. A pair that has never
// been written reports ErrStateNotFound.
func GetAgentState(ctx context.Context, db *gorm.DB, userID, agentID uuid.UUID) (*AgentState, error) {
	var state AgentState
	err := db.WithContext(ctx).First(&state, "user_id = ? AND agent_id = ?", userID, agentID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrStateNotFound
		}
		return nil, err
	}
	return &state, nil
}

// UpsertAgentState writes the state of one pair in a single statement, so two
// writers cannot interleave a read and a write.
//
// expectedVersion is the version the caller read; nil means "overwrite whatever
// is there". When the expectation is not met the update does nothing and
// ErrStateConflict is returned, and the row keeps its content and version.
func UpsertAgentState(ctx context.Context, db *gorm.DB, state *AgentState, expectedVersion *int) (*AgentState, error) {
	sql := `INSERT INTO agent_state (user_id, agent_id, content, version, source, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, now(), now())
		ON CONFLICT (user_id, agent_id) DO UPDATE
		SET content = EXCLUDED.content,
			version = agent_state.version + 1,
			source = EXCLUDED.source,
			updated_at = now()`
	args := []any{state.UserID, state.AgentID, state.Content, state.Source}
	if expectedVersion != nil {
		sql += ` WHERE agent_state.version = ?`
		args = append(args, *expectedVersion)
	}
	sql += ` RETURNING user_id, agent_id, content, version, source, created_at, updated_at`

	var written AgentState
	result := db.WithContext(ctx).Raw(sql, args...).Scan(&written)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		// Only the guarded update can affect no row: the insert always writes
		// one, so an empty result means the version did not match.
		return nil, ErrStateConflict
	}
	return &written, nil
}

// DeleteAgentState removes the state of one pair. A pair without a row reports
// ErrStateNotFound so the caller can answer 404 rather than pretend it deleted
// something.
func DeleteAgentState(ctx context.Context, db *gorm.DB, userID, agentID uuid.UUID) error {
	result := db.WithContext(ctx).Where("user_id = ? AND agent_id = ?", userID, agentID).Delete(&AgentState{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrStateNotFound
	}
	return nil
}
