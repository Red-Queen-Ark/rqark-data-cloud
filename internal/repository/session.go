package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrSessionNotFound is returned when no session matches the lookup. It also
// covers a lookup that names another user's session, so session ids cannot be
// enumerated.
var ErrSessionNotFound = errors.New("repository: session not found")

// SessionPlanItem is one entry of the sessions.plan_items column. The cloud
// stores it as it arrives: the status enum belongs to the app, and a value the
// app adds later round-trips without a schema change.
type SessionPlanItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// Session is the sessions table. Its primary key is the uuid the app generated
// for the session locally rather than one minted here: the app names its
// per-session file and its per-directory cache after it, so both sides have to
// agree on the value. Source names the application the session was uploaded
// from; it defaults to the empty string for a session that names no source.
type Session struct {
	ID        uuid.UUID         `gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID         `gorm:"type:uuid;not null;index"`
	Title     string            `gorm:"type:varchar(255);not null;default:''"`
	Source    string            `gorm:"type:varchar(32);not null;default:''"`
	PlanItems []SessionPlanItem `gorm:"serializer:json;type:jsonb;not null;default:'[]'"`
	CreatedAt time.Time         `gorm:"not null"`
	UpdatedAt time.Time         `gorm:"not null"`

	// User only exists to tie user_id to users.id with a foreign key. It is
	// never loaded or written through this struct.
	User User `gorm:"foreignKey:UserID;references:ID"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (Session) TableName() string { return "sessions" }

// GetSessionByID loads one of userID's sessions by primary key. Another user's
// session is reported as ErrSessionNotFound.
func GetSessionByID(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) (*Session, error) {
	var session Session
	if err := db.WithContext(ctx).First(&session, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, sessionLookupError(err)
	}
	return &session, nil
}

// CreateSession inserts a session.
func CreateSession(ctx context.Context, db *gorm.DB, session *Session) error {
	return db.WithContext(ctx).Create(session).Error
}

// SaveSession writes every column of session, bumping its updated_at.
func SaveSession(ctx context.Context, db *gorm.DB, session *Session) error {
	return db.WithContext(ctx).Save(session).Error
}

// ListSessions returns all of userID's sessions, most recently updated first.
// The list is not paginated: a session is the cloud copy of one local file, and
// the app wants the whole set at once, the same way it reads its agents. A
// non-nil source keeps only sessions uploaded from that application; a nil one
// keeps every source, the empty string included.
func ListSessions(ctx context.Context, db *gorm.DB, userID uuid.UUID, source *string) ([]Session, error) {
	query := db.WithContext(ctx).Where("user_id = ?", userID)
	if source != nil {
		query = query.Where("source = ?", *source)
	}
	var sessions []Session
	err := query.Order("updated_at DESC").Order("id").Find(&sessions).Error
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

// DeleteSession removes one of userID's sessions. Its messages go with it
// through the session_messages foreign key.
func DeleteSession(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) error {
	result := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&Session{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// sessionLookupError maps GORM's not-found error onto the repository's own.
func sessionLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrSessionNotFound
	}
	return err
}
