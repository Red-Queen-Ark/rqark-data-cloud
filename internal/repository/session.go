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
//
// ProjectID says which project the session works in. A nil one is a session
// that works at the volume root, which is what every session created before
// projects existed does. Deleting the project clears the column rather than the
// session: the conversation survives its directory.
//
// AgentID names the agent the session was last talked to with. A nil one is a
// session whose client never said, which is every session created before the
// column existed. Deleting the agent clears the column rather than the session:
// the conversation survives the agent it was held with.
type Session struct {
	ID        uuid.UUID         `gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID         `gorm:"type:uuid;not null;index"`
	Title     string            `gorm:"type:varchar(255);not null;default:''"`
	Source    string            `gorm:"type:varchar(32);not null;default:''"`
	ProjectID *uuid.UUID        `gorm:"type:uuid;index"`
	AgentID   *uuid.UUID        `gorm:"type:uuid;index"`
	PlanItems []SessionPlanItem `gorm:"serializer:json;type:jsonb;not null;default:'[]'"`
	CreatedAt time.Time         `gorm:"not null"`
	UpdatedAt time.Time         `gorm:"not null"`

	// User only exists to tie user_id to users.id with a foreign key. It is
	// never loaded or written through this struct.
	User User `gorm:"foreignKey:UserID;references:ID"`

	// Project does the same for project_id. OnDelete SET NULL is what makes a
	// deleted project detach its sessions instead of being refused.
	Project *Project `gorm:"foreignKey:ProjectID;references:ID;constraint:OnDelete:SET NULL"`

	// Agent does the same for agent_id, so a deleted agent leaves its sessions
	// behind rather than being refused.
	Agent *Agent `gorm:"foreignKey:AgentID;references:ID;constraint:OnDelete:SET NULL"`
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
// keeps every source, the empty string included. A non-nil projectID keeps only
// sessions that work in that project.
func ListSessions(ctx context.Context, db *gorm.DB, userID uuid.UUID, source *string, projectID *uuid.UUID) ([]Session, error) {
	query := db.WithContext(ctx).Where("user_id = ?", userID)
	if source != nil {
		query = query.Where("source = ?", *source)
	}
	if projectID != nil {
		query = query.Where("project_id = ?", *projectID)
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
