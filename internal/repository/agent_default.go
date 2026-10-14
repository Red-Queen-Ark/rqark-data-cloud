package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrAgentDefaultNotFound is returned when no default row matches the lookup. It
// also covers a default that belongs to another user, so the defaults of one
// user cannot be read through another.
var ErrAgentDefaultNotFound = errors.New("repository: agent default not found")

// AgentDefault is the agent_defaults table: the agent one user starts a session
// with on one source. Source carries the same value as sessions.source, so a
// client picks its default agent by the application it runs in. The pair
// (user_id, source) is unique, so a user holds at most one default per source.
type AgentDefault struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:idx_agent_defaults_user_id_source,priority:1"`
	Source    string    `gorm:"type:varchar(32);not null;uniqueIndex:idx_agent_defaults_user_id_source,priority:2"`
	AgentID   uuid.UUID `gorm:"type:uuid;not null;index"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	// User only exists to tie user_id to users.id with a foreign key. It is
	// never loaded or written through this struct.
	User User `gorm:"foreignKey:UserID;references:ID"`
	// Agent only exists to tie agent_id to agents.id with a foreign key that
	// drops the default together with the agent. It is never loaded or written
	// through this struct.
	Agent Agent `gorm:"foreignKey:AgentID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (AgentDefault) TableName() string { return "agent_defaults" }

// ListAgentDefaults returns every default of userID ordered by source. Defaults
// are not paginated: a user names one source per client it runs, and the app
// wants the whole set at once.
func ListAgentDefaults(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]AgentDefault, error) {
	defaults := []AgentDefault{}
	err := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("source").Order("id").
		Find(&defaults).Error
	if err != nil {
		return nil, err
	}
	return defaults, nil
}

// GetAgentDefault loads one user's default for a source. A default that belongs
// to another user is reported as ErrAgentDefaultNotFound.
func GetAgentDefault(ctx context.Context, db *gorm.DB, userID uuid.UUID, source string) (*AgentDefault, error) {
	var default_ AgentDefault
	if err := db.WithContext(ctx).First(&default_, "user_id = ? AND source = ?", userID, source).Error; err != nil {
		return nil, agentDefaultLookupError(err)
	}
	return &default_, nil
}

// CreateAgentDefault inserts a default row.
func CreateAgentDefault(ctx context.Context, db *gorm.DB, default_ *AgentDefault) error {
	return db.WithContext(ctx).Create(default_).Error
}

// SaveAgentDefault writes every column of default_.
func SaveAgentDefault(ctx context.Context, db *gorm.DB, default_ *AgentDefault) error {
	return db.WithContext(ctx).Save(default_).Error
}

// DeleteAgentDefault removes one of userID's defaults. A source that does not
// exist is reported as ErrAgentDefaultNotFound.
func DeleteAgentDefault(ctx context.Context, db *gorm.DB, userID uuid.UUID, source string) error {
	result := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&AgentDefault{}, "source = ?", source)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAgentDefaultNotFound
	}
	return nil
}

// agentDefaultLookupError maps GORM's not-found error onto the repository's own.
func agentDefaultLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrAgentDefaultNotFound
	}
	return err
}
