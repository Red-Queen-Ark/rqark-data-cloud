package service

import (
	"context"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// maxAgentNameLen is the width of the agents.name column.
const maxAgentNameLen = 255

// CreateAgentInput is the body accepted by POST /api/agents. Prompts and tools
// are stored as they arrive: the service does not read their contents.
type CreateAgentInput struct {
	Name        string
	Description string
	Prompts     []repository.AgentPrompt
	Tools       []repository.AgentTool
	// IsPublic shares the agent with every other user. Only a superuser may ask
	// for it.
	IsPublic bool
}

// UpdateAgentInput is a partial update: a nil field is left untouched.
type UpdateAgentInput struct {
	Name        *string
	Description *string
	Prompts     *[]repository.AgentPrompt
	Tools       *[]repository.AgentTool
	// IsPublic clears or sets the share flag. Only a superuser may set it.
	IsPublic *bool
}

// CreateAgent validates the name, checks the owner's uniqueness rule and stores
// the agent under userID. Sharing the agent (`IsPublic`) is reserved to
// superusers.
func CreateAgent(ctx context.Context, userID uuid.UUID, isSuperuser bool, in CreateAgentInput) (*repository.Agent, error) {
	if err := validateAgentName(in.Name); err != nil {
		return nil, err
	}
	if in.IsPublic && !isSuperuser {
		return nil, ErrAgentPublicForbidden
	}

	taken, err := repository.AgentNameTaken(ctx, config.DB, userID, in.Name, uuid.Nil)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrAgentNameTaken
	}

	agent := &repository.Agent{
		ID:          uuid.New(),
		UserID:      userID,
		Name:        in.Name,
		Description: in.Description,
		Prompts:     in.Prompts,
		Tools:       in.Tools,
		IsPublic:    in.IsPublic,
	}
	if err := repository.CreateAgent(ctx, config.DB, agent); err != nil {
		return nil, mapWriteError(err)
	}
	return agent, nil
}

// UpdateAgent applies in to one of userID's agents. Setting the share flag is
// reserved to superusers; a non-superuser owner leaves it untouched or turns it
// off.
func UpdateAgent(ctx context.Context, userID uuid.UUID, isSuperuser bool, id uuid.UUID, in UpdateAgentInput) (*repository.Agent, error) {
	agent, err := repository.GetAgentByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}

	if in.IsPublic != nil && *in.IsPublic && !isSuperuser {
		return nil, ErrAgentPublicForbidden
	}

	if in.Name != nil {
		if err := validateAgentName(*in.Name); err != nil {
			return nil, err
		}
		taken, err := repository.AgentNameTaken(ctx, config.DB, userID, *in.Name, id)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, ErrAgentNameTaken
		}
		agent.Name = *in.Name
	}
	if in.Description != nil {
		agent.Description = *in.Description
	}
	if in.Prompts != nil {
		agent.Prompts = *in.Prompts
	}
	if in.Tools != nil {
		agent.Tools = *in.Tools
	}
	// Withdrawing the share takes the agent away from every other user, so the
	// defaults they set on it go too. The owner keeps the agent, and any default
	// of its own, since un-sharing does not take it away from the owner.
	unshared := in.IsPublic != nil && agent.IsPublic && !*in.IsPublic
	if in.IsPublic != nil {
		agent.IsPublic = *in.IsPublic
	}

	if err := repository.SaveAgent(ctx, config.DB, agent); err != nil {
		return nil, mapWriteError(err)
	}
	if unshared {
		if err := repository.DeleteAgentDefaultsOfOthers(ctx, config.DB, id, userID); err != nil {
			return nil, err
		}
	}
	return agent, nil
}

// DeleteAgent removes one of userID's agents.
func DeleteAgent(ctx context.Context, userID, id uuid.UUID) error {
	return repository.DeleteAgent(ctx, config.DB, userID, id)
}

// ListAgents returns all of the agents userID may use: the ones it owns plus the
// public agents other users share.
func ListAgents(ctx context.Context, userID uuid.UUID) ([]repository.Agent, error) {
	return repository.ListAgents(ctx, config.DB, userID)
}

// ListAgentNames returns the names of all agents userID may use: the ones it
// owns plus the public agents other users share.
func ListAgentNames(ctx context.Context, userID uuid.UUID) ([]string, error) {
	return repository.ListAgentNames(ctx, config.DB, userID)
}

// GetAgent loads a single agent the caller may use: one it owns, or one another
// user shares.
func GetAgent(ctx context.Context, userID, id uuid.UUID) (*repository.Agent, error) {
	return repository.GetVisibleAgentByID(ctx, config.DB, userID, id)
}

// validateAgentName checks that name is present and fits its column.
func validateAgentName(name string) error {
	if name == "" {
		return &ValidationError{Message: "name is required."}
	}
	if len(name) > maxAgentNameLen {
		return &ValidationError{Message: "name is too long."}
	}
	return nil
}
