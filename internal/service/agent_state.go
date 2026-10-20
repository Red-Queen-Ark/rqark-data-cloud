package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// SetAgentStateInput is the body of PUT /api/memory/state. Version is the
// version the caller read; nil overwrites whatever is stored.
type SetAgentStateInput struct {
	AgentID uuid.UUID
	Content string
	Source  string
	Version *int
}

// GetAgentState returns the current state of one agent the caller may use.
func GetAgentState(ctx context.Context, userID, agentID uuid.UUID) (*repository.AgentState, error) {
	if err := requireUsableAgent(ctx, userID, agentID); err != nil {
		return nil, err
	}
	return repository.GetAgentState(ctx, config.DB, userID, agentID)
}

// SetAgentState overwrites the state of one agent the caller may use and bumps its
// version. An empty content is refused: removing the state is DELETE, so a
// blank overwrite cannot empty the board by accident.
func SetAgentState(ctx context.Context, userID uuid.UUID, in SetAgentStateInput) (*repository.AgentState, error) {
	if err := requireUsableAgent(ctx, userID, in.AgentID); err != nil {
		return nil, err
	}
	content, err := validateStateContent(in.Content, in.Source)
	if err != nil {
		return nil, err
	}
	if in.Version != nil && *in.Version < 1 {
		return nil, &ValidationError{Message: "version must be at least 1."}
	}

	state := &repository.AgentState{
		UserID:  userID,
		AgentID: in.AgentID,
		Content: content,
		Source:  in.Source,
	}
	return repository.UpsertAgentState(ctx, config.DB, state, in.Version)
}

// DeleteAgentState removes the state of one agent the caller may use.
func DeleteAgentState(ctx context.Context, userID, agentID uuid.UUID) error {
	if err := requireUsableAgent(ctx, userID, agentID); err != nil {
		return err
	}
	return repository.DeleteAgentState(ctx, config.DB, userID, agentID)
}

// AgentStateOrNil returns the state of one pair, with a pair that has never been
// written reported as nil rather than as an error. It is what the injection
// prompt uses: an agent without a state simply renders no state block.
func AgentStateOrNil(ctx context.Context, userID, agentID uuid.UUID) (*repository.AgentState, error) {
	state, err := repository.GetAgentState(ctx, config.DB, userID, agentID)
	if err != nil {
		if errors.Is(err, repository.ErrStateNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return state, nil
}

// validateStateContent checks a state against the column limits and returns the
// content as sent. The board is markdown, so it is not trimmed: leading
// whitespace may be a list indent.
func validateStateContent(content, source string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", &InvalidContentError{Message: "content is required; delete the state to remove it."}
	}
	if utf8.RuneCountInString(content) > repository.MaxStateContentLen {
		return "", &InvalidContentError{Message: "content must be at most 2000 characters."}
	}
	if len(source) > maxMemorySourceLen {
		return "", &InvalidContentError{Message: "source must be at most 32 characters."}
	}
	return content, nil
}
