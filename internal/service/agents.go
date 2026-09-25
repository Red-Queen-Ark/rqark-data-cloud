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
}

// UpdateAgentInput is a partial update: a nil field is left untouched.
type UpdateAgentInput struct {
	Name        *string
	Description *string
	Prompts     *[]repository.AgentPrompt
	Tools       *[]repository.AgentTool
}

// CreateAgent validates the name, checks the owner's uniqueness rule and stores
// the agent under userID.
func CreateAgent(ctx context.Context, userID uuid.UUID, in CreateAgentInput) (*repository.Agent, error) {
	if err := validateAgentName(in.Name); err != nil {
		return nil, err
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
	}
	if err := repository.CreateAgent(ctx, config.DB, agent); err != nil {
		return nil, mapWriteError(err)
	}
	return agent, nil
}

// UpdateAgent applies in to one of userID's agents.
func UpdateAgent(ctx context.Context, userID, id uuid.UUID, in UpdateAgentInput) (*repository.Agent, error) {
	agent, err := repository.GetAgentByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
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

	if err := repository.SaveAgent(ctx, config.DB, agent); err != nil {
		return nil, mapWriteError(err)
	}
	return agent, nil
}

// DeleteAgent removes one of userID's agents.
func DeleteAgent(ctx context.Context, userID, id uuid.UUID) error {
	return repository.DeleteAgent(ctx, config.DB, userID, id)
}

// ListAgents returns all of the agents owned by userID.
func ListAgents(ctx context.Context, userID uuid.UUID) ([]repository.Agent, error) {
	return repository.ListAgents(ctx, config.DB, userID)
}

// GetAgent loads a single agent owned by userID.
func GetAgent(ctx context.Context, userID, id uuid.UUID) (*repository.Agent, error) {
	return repository.GetAgentByID(ctx, config.DB, userID, id)
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
