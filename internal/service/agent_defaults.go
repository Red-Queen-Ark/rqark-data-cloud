package service

import (
	"context"
	"errors"
	"regexp"

	"github.com/google/uuid"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/repository"
)

// maxAgentDefaultSourceLen is the width of the agent_defaults.source column. It
// matches the width of sessions.source, since a default is looked up by the
// source a session carries.
const maxAgentDefaultSourceLen = 32

// agentDefaultSourcePattern is the source grammar: URL-safe, so a source can be
// a path segment.
var agentDefaultSourcePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ListAgentDefaults returns every default row of userID.
func ListAgentDefaults(ctx context.Context, userID uuid.UUID) ([]repository.AgentDefault, error) {
	return repository.ListAgentDefaults(ctx, config.DB, userID)
}

// GetAgentDefault returns the default agent userID uses on source. A source that
// holds no default is reported as ErrAgentDefaultNotFound.
func GetAgentDefault(ctx context.Context, userID uuid.UUID, source string) (*repository.AgentDefault, error) {
	if err := validateAgentDefaultSource(source); err != nil {
		return nil, err
	}
	return repository.GetAgentDefault(ctx, config.DB, userID, source)
}

// SetAgentDefault upserts the default agent userID uses on source. The agent has
// to be one userID may use: its own, or one another user shares. A default can
// therefore point at an agent owned by someone else, but only while that agent
// stays shared.
func SetAgentDefault(ctx context.Context, userID uuid.UUID, source string, agentID uuid.UUID) (*repository.AgentDefault, error) {
	if err := validateAgentDefaultSource(source); err != nil {
		return nil, err
	}
	if agentID == uuid.Nil {
		return nil, &ValidationError{Message: "agent_id is required."}
	}
	if _, err := repository.GetVisibleAgentByID(ctx, config.DB, userID, agentID); err != nil {
		return nil, err
	}

	existing, err := repository.GetAgentDefault(ctx, config.DB, userID, source)
	switch {
	case err == nil:
		existing.AgentID = agentID
		if err := repository.SaveAgentDefault(ctx, config.DB, existing); err != nil {
			return nil, err
		}
		return existing, nil
	case errors.Is(err, repository.ErrAgentDefaultNotFound):
		created := &repository.AgentDefault{
			ID:      uuid.New(),
			UserID:  userID,
			Source:  source,
			AgentID: agentID,
		}
		if err := repository.CreateAgentDefault(ctx, config.DB, created); err != nil {
			return nil, err
		}
		return created, nil
	default:
		return nil, err
	}
}

// DeleteAgentDefault removes one default row of userID.
func DeleteAgentDefault(ctx context.Context, userID uuid.UUID, source string) error {
	if err := validateAgentDefaultSource(source); err != nil {
		return err
	}
	return repository.DeleteAgentDefault(ctx, config.DB, userID, source)
}

// validateAgentDefaultSource checks that source is present, fits its column and
// is made of characters that are safe in a path segment.
func validateAgentDefaultSource(source string) error {
	if source == "" {
		return &ValidationError{Message: "source is required."}
	}
	if len(source) > maxAgentDefaultSourceLen {
		return &ValidationError{Message: "source is too long."}
	}
	if !agentDefaultSourcePattern.MatchString(source) {
		return &ValidationError{Message: "source may only contain letters, digits, '_', '.' and '-'."}
	}
	return nil
}
