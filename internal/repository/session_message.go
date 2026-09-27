package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SessionMessage is one row of the session_messages table: a single history
// entry of a session. A message has no id of its own, so it is identified by
// where it sits in the session and the primary key is (session_id, seq).
//
// The whole message lives in Data and is handed back untouched. Role is copied
// out of it into its own column, so a listing can count or filter by role
// without parsing JSON, and CreatedAt is copied from the message's own
// created_at for the same reason. A summary carries no created_at, so its
// CreatedAt is null.
type SessionMessage struct {
	SessionID uuid.UUID      `gorm:"type:uuid;primaryKey"`
	Seq       int            `gorm:"primaryKey"`
	Role      string         `gorm:"type:varchar(16);not null"`
	Data      map[string]any `gorm:"serializer:json;type:jsonb;not null"`
	// CreatedAt is the message's own created_at, copied into a column so a
	// query can read the time without parsing Data. Auto-populating it is off,
	// because a message that carries no created_at, such as a summary, must
	// stay null rather than be given the time it happened to be stored.
	CreatedAt *time.Time `gorm:"type:timestamptz;autoCreateTime:false"`

	// Session only exists to tie session_id to sessions.id with a foreign key
	// that cascades, so deleting a session takes its messages with it. It is
	// never loaded or written through this struct.
	Session Session `gorm:"foreignKey:SessionID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (SessionMessage) TableName() string { return "session_messages" }

// ListSessionMessages returns every message of a session in the order they were
// sent. Like the session list, it is not paginated: the app loads a session's
// whole history, which is also what its own file storage holds.
func ListSessionMessages(ctx context.Context, db *gorm.DB, sessionID uuid.UUID) ([]SessionMessage, error) {
	var messages []SessionMessage
	err := db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("seq").
		Find(&messages).Error
	if err != nil {
		return nil, err
	}
	return messages, nil
}

// DeleteSessionMessages removes every message of a session. It is the first
// half of the wholesale rewrite a save performs.
func DeleteSessionMessages(ctx context.Context, db *gorm.DB, sessionID uuid.UUID) error {
	return db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Delete(&SessionMessage{}).Error
}

// CreateSessionMessages inserts a session's messages in one statement. An empty
// slice is a no-op.
func CreateSessionMessages(ctx context.Context, db *gorm.DB, messages []SessionMessage) error {
	if len(messages) == 0 {
		return nil
	}
	return db.WithContext(ctx).Create(&messages).Error
}
