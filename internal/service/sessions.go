package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/repository"
	"rqark-data-cloud/internal/utils"
)

// maxSessionTitleLen is the width of the sessions.title column.
const maxSessionTitleLen = 255

// maxSessionSourceLen is the width of the sessions.source column.
const maxSessionSourceLen = 32

// sessionRoles are the roles a history entry may carry. The whole message is
// stored opaquely, so role is the one part of it the cloud does read: it is
// copied out into a column of its own.
var sessionRoles = map[string]bool{
	"user":      true,
	"assistant": true,
	"tool":      true,
	"system":    true,
	"summary":   true,
}

// CreateSessionInput is the body accepted by POST /api/sessions. ID is the uuid
// the app generated for the session; when it is nil the cloud mints one. Source
// names the application the session was uploaded from; an empty string is kept
// as the column's default. ProjectID names the project the session works in; a
// nil one is a session at the volume root. AgentID names the agent the session
// is talked to with; a nil one is a session that names no agent.
type CreateSessionInput struct {
	ID        *uuid.UUID
	Title     string
	Source    string
	ProjectID *uuid.UUID
	AgentID   *uuid.UUID
	PlanItems []repository.SessionPlanItem
}

// UpdateSessionInput is a partial update: a nil field is left untouched. A
// non-nil History replaces the session's messages wholesale. ProjectID and
// AgentID are utils.Optional so an explicit null detaches the session from its
// project or its agent.
type UpdateSessionInput struct {
	Title     *string
	Source    *string
	ProjectID utils.Optional[uuid.UUID]
	AgentID   utils.Optional[uuid.UUID]
	PlanItems *[]repository.SessionPlanItem
	History   *[]map[string]any
}

// CreateSession validates the title and source and stores a session under
// userID. The session keeps the caller's uuid when one is given and the cloud
// mints one otherwise. A project_id must name one of the caller's projects, and
// an agent_id one of the agents the caller may use.
func CreateSession(ctx context.Context, userID uuid.UUID, in CreateSessionInput) (*repository.Session, error) {
	if err := validateSessionTitle(in.Title); err != nil {
		return nil, err
	}
	if err := validateSessionSource(in.Source); err != nil {
		return nil, err
	}
	if err := checkProjectOwner(ctx, userID, in.ProjectID); err != nil {
		return nil, err
	}
	if err := checkAgentVisible(ctx, userID, in.AgentID); err != nil {
		return nil, err
	}

	session := &repository.Session{
		ID:        uuid.New(),
		UserID:    userID,
		Title:     in.Title,
		Source:    in.Source,
		ProjectID: in.ProjectID,
		AgentID:   in.AgentID,
		PlanItems: in.PlanItems,
	}
	if in.ID != nil {
		session.ID = *in.ID
	}
	if err := repository.CreateSession(ctx, config.DB, session); err != nil {
		return nil, mapWriteError(err)
	}
	return session, nil
}

// UpdateSession applies in to one of userID's sessions. When in carries a
// history the session's messages are replaced wholesale, in one transaction:
// the app rewrites its whole session on every save, so a save is a full
// overwrite rather than a diff.
func UpdateSession(ctx context.Context, userID, id uuid.UUID, in UpdateSessionInput) (*repository.Session, error) {
	if in.Title != nil {
		if err := validateSessionTitle(*in.Title); err != nil {
			return nil, err
		}
	}
	if in.Source != nil {
		if err := validateSessionSource(*in.Source); err != nil {
			return nil, err
		}
	}
	if in.ProjectID.Set {
		if err := checkProjectOwner(ctx, userID, in.ProjectID.Value); err != nil {
			return nil, err
		}
	}
	if in.AgentID.Set {
		if err := checkAgentVisible(ctx, userID, in.AgentID.Value); err != nil {
			return nil, err
		}
	}

	var messages []repository.SessionMessage
	if in.History != nil {
		built, err := buildSessionMessages(id, *in.History)
		if err != nil {
			return nil, err
		}
		messages = built
	}

	var session *repository.Session
	err := config.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stored, err := repository.GetSessionByID(ctx, tx, userID, id)
		if err != nil {
			return err
		}

		if in.Title != nil {
			stored.Title = *in.Title
		}
		if in.Source != nil {
			stored.Source = *in.Source
		}
		if in.ProjectID.Set {
			stored.ProjectID = in.ProjectID.Value
		}
		if in.AgentID.Set {
			stored.AgentID = in.AgentID.Value
		}
		if in.PlanItems != nil {
			stored.PlanItems = *in.PlanItems
		}
		if in.History != nil {
			if err := repository.DeleteSessionMessages(ctx, tx, id); err != nil {
				return err
			}
			if err := repository.CreateSessionMessages(ctx, tx, messages); err != nil {
				return err
			}
		}
		if err := repository.SaveSession(ctx, tx, stored); err != nil {
			return err
		}

		session = stored
		return nil
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

// DeleteSession removes one of userID's sessions and its messages.
func DeleteSession(ctx context.Context, userID, id uuid.UUID) error {
	return repository.DeleteSession(ctx, config.DB, userID, id)
}

// ListSessions returns all of userID's session metadata. The list is not
// paginated, matching the agent and provider listings. A non-nil source keeps
// only sessions uploaded from that application; a nil one keeps every source. A
// non-nil projectID keeps only sessions that work in that project.
func ListSessions(ctx context.Context, userID uuid.UUID, source *string, projectID *uuid.UUID) ([]repository.Session, error) {
	return repository.ListSessions(ctx, config.DB, userID, source, projectID)
}

// GetSession loads a single session owned by userID.
func GetSession(ctx context.Context, userID, id uuid.UUID) (*repository.Session, error) {
	return repository.GetSessionByID(ctx, config.DB, userID, id)
}

// ListSessionMessages returns every message of one of userID's sessions. The
// session is loaded first so that another user's session is reported as not
// found rather than as an empty history.
func ListSessionMessages(ctx context.Context, userID, sessionID uuid.UUID) ([]repository.SessionMessage, error) {
	if _, err := repository.GetSessionByID(ctx, config.DB, userID, sessionID); err != nil {
		return nil, err
	}
	return repository.ListSessionMessages(ctx, config.DB, sessionID)
}

// buildSessionMessages turns a history body into rows. Every entry must be an
// object carrying a known role; seq is the position in the array, so the app's
// order is what the cloud stores. created_at is copied out of the message when
// it has one and left null when it does not, which is the summary case.
func buildSessionMessages(sessionID uuid.UUID, history []map[string]any) ([]repository.SessionMessage, error) {
	messages := make([]repository.SessionMessage, 0, len(history))
	for i, data := range history {
		role, ok := data["role"].(string)
		if !ok || !sessionRoles[role] {
			return nil, &ValidationError{
				Message: fmt.Sprintf("history[%d].role must be one of user, assistant, tool, system, summary.", i),
			}
		}

		createdAt, err := sessionMessageCreatedAt(data)
		if err != nil {
			return nil, &ValidationError{
				Message: fmt.Sprintf("history[%d].created_at must be an RFC 3339 timestamp.", i),
			}
		}

		// A message body is stored opaquely, but jsonb still rejects U+0000,
		// which a Windows tool output such as "wsl --status" carries: it is
		// UTF-16LE and puts a NUL after every character. Strip the NULs so one
		// such message cannot fail the whole session save.
		utils.StripNullChars(data)

		messages = append(messages, repository.SessionMessage{
			SessionID: sessionID,
			Seq:       i,
			Role:      role,
			Data:      data,
			CreatedAt: createdAt,
		})
	}
	return messages, nil
}

// sessionMessageCreatedAt reads the created_at a message carries. A message
// without one, such as a summary, yields nil.
func sessionMessageCreatedAt(data map[string]any) (*time.Time, error) {
	raw, ok := data["created_at"]
	if !ok || raw == nil {
		return nil, nil
	}
	value, ok := raw.(string)
	if !ok {
		return nil, errors.New("created_at is not a string")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

// validateSessionTitle checks that title fits its column. An empty title is
// allowed: the app fills it in later with a generated one.
func validateSessionTitle(title string) error {
	if len(title) > maxSessionTitleLen {
		return &ValidationError{Message: "title is too long."}
	}
	return nil
}

// validateSessionSource checks that source fits its column. An empty source is
// allowed: it is the column default for a session that names no source.
func validateSessionSource(source string) error {
	if len(source) > maxSessionSourceLen {
		return &ValidationError{Message: "source is too long."}
	}
	return nil
}

// checkAgentVisible reports whether agentID names an agent userID may use: its
// own, or one another user shares. A nil id is accepted: it is a session that
// names no agent. An agent the caller may not use is a validation error rather
// than a not-found one, because the caller named the id explicitly.
func checkAgentVisible(ctx context.Context, userID uuid.UUID, agentID *uuid.UUID) error {
	if agentID == nil {
		return nil
	}
	if _, err := repository.GetVisibleAgentByID(ctx, config.DB, userID, *agentID); err != nil {
		if errors.Is(err, repository.ErrAgentNotFound) {
			return &ValidationError{Message: "agent_id must name one of your agents."}
		}
		return err
	}
	return nil
}
