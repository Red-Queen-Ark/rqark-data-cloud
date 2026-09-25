package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrAgentNotFound is returned when no agent matches the lookup. It also covers
// a lookup that names another user's agent, so agent ids cannot be enumerated.
var ErrAgentNotFound = errors.New("repository: agent not found")

// AgentPrompt is one entry of the agents.prompts column. The cloud keeps it
// opaque: name is not checked against any prompt list, and params holds whatever
// shape the prompt class in rqark-core defines. params is omitted when it is
// empty, matching the app's _without_empty_params.
type AgentPrompt struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

// AgentTool is one entry of the agents.tools column, stored opaquely like
// AgentPrompt. SubAgent carries further prompts and tools inside its params.
type AgentTool struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

// Agent is the agents table. The pair (user_id, name) is unique because the app
// keys both its local config file names and its sessions by agent name, so two
// agents of one user sharing a name would overwrite each other.
type Agent struct {
	ID          uuid.UUID     `gorm:"type:uuid;primaryKey"`
	UserID      uuid.UUID     `gorm:"type:uuid;not null;index;uniqueIndex:idx_agents_user_id_name,priority:1"`
	Name        string        `gorm:"type:varchar(255);not null;uniqueIndex:idx_agents_user_id_name,priority:2"`
	Description string        `gorm:"type:text;not null;default:''"`
	Prompts     []AgentPrompt `gorm:"serializer:json;type:jsonb;not null;default:'[]'"`
	Tools       []AgentTool   `gorm:"serializer:json;type:jsonb;not null;default:'[]'"`
	CreatedAt   time.Time     `gorm:"not null"`
	UpdatedAt   time.Time     `gorm:"not null"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (Agent) TableName() string { return "agents" }

// GetAgentByID loads one of userID's agents by primary key. Another user's
// agent is reported as ErrAgentNotFound.
func GetAgentByID(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) (*Agent, error) {
	var agent Agent
	if err := db.WithContext(ctx).First(&agent, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, agentLookupError(err)
	}
	return &agent, nil
}

// CreateAgent inserts an agent.
func CreateAgent(ctx context.Context, db *gorm.DB, agent *Agent) error {
	return db.WithContext(ctx).Create(agent).Error
}

// SaveAgent writes every column of agent.
func SaveAgent(ctx context.Context, db *gorm.DB, agent *Agent) error {
	return db.WithContext(ctx).Save(agent).Error
}

// ListAgents returns all of userID's agents ordered by name. Agents are not
// paginated: a user has few of them, and the app wants the whole set at once,
// which is the same call the provider list makes.
func ListAgents(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]Agent, error) {
	var agents []Agent
	err := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("name").Order("id").
		Find(&agents).Error
	if err != nil {
		return nil, err
	}
	return agents, nil
}

// DeleteAgent removes one of userID's agents.
func DeleteAgent(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) error {
	result := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&Agent{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAgentNotFound
	}
	return nil
}

// AgentNameTaken reports whether another agent of userID already owns name.
func AgentNameTaken(ctx context.Context, db *gorm.DB, userID uuid.UUID, name string, excludeID uuid.UUID) (bool, error) {
	query := db.WithContext(ctx).Model(&Agent{}).Where("user_id = ? AND name = ?", userID, name)
	if excludeID != uuid.Nil {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// agentLookupError maps GORM's not-found error onto the repository's own.
func agentLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrAgentNotFound
	}
	return err
}
