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

// ErrAgentAmbiguous is returned when a name resolves to more than one agent the
// caller may use, so the name alone does not identify an agent.
var ErrAgentAmbiguous = errors.New("repository: agent name is ambiguous")

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

// AgentName is one entry of the names listing: the id a caller needs to address
// the agent plus the name it shows. Prompts and tools are left out, so a caller
// that only fills a picker never reads them.
type AgentName struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
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
	// IsPublic marks an agent its owner shares with every other user. Only a
	// superuser may set it, and only the owner may change the row afterwards.
	IsPublic  bool      `gorm:"not null;default:false;index"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
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

// GetVisibleAgentByID loads an agent the viewer may use by primary key: one it
// owns, or a public one another user shares. Any other agent is reported as
// ErrAgentNotFound, so agent ids cannot be enumerated.
func GetVisibleAgentByID(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) (*Agent, error) {
	var agent Agent
	err := db.WithContext(ctx).
		First(&agent, "id = ? AND (user_id = ? OR is_public = ?)", id, userID, true).Error
	if err != nil {
		return nil, agentLookupError(err)
	}
	return &agent, nil
}

// CreateAgent inserts an agent.
func CreateAgent(ctx context.Context, db *gorm.DB, agent *Agent) error {
	return db.WithContext(ctx).Create(agent).Error
}

// FindAgentNamed resolves a name to the one agent userID may use that carries
// it. The caller's own agent wins over a shared one; two candidates at the same
// level are reported as ErrAgentAmbiguous rather than picked arbitrarily, and no
// candidate at all as ErrAgentNotFound.
func FindAgentNamed(ctx context.Context, db *gorm.DB, userID uuid.UUID, name string) (*Agent, error) {
	owned, err := findAgentsNamed(ctx, db, "user_id = ? AND name = ?", userID, name)
	if err != nil {
		return nil, err
	}
	switch len(owned) {
	case 1:
		return &owned[0], nil
	case 0:
	default:
		return nil, ErrAgentAmbiguous
	}

	shared, err := findAgentsNamed(ctx, db, "user_id <> ? AND is_public = ? AND name = ?", userID, true, name)
	if err != nil {
		return nil, err
	}
	switch len(shared) {
	case 1:
		return &shared[0], nil
	case 0:
		return nil, ErrAgentNotFound
	default:
		return nil, ErrAgentAmbiguous
	}
}

// findAgentsNamed runs one half of FindAgentNamed. The limit of two is enough to
// tell "exactly one" from "more than one" without reading the rest.
func findAgentsNamed(ctx context.Context, db *gorm.DB, where string, args ...any) ([]Agent, error) {
	agents := []Agent{}
	err := db.WithContext(ctx).
		Where(where, args...).
		Order("id").
		Limit(2).
		Find(&agents).Error
	if err != nil {
		return nil, err
	}
	return agents, nil
}

// SaveAgent writes every column of agent.
func SaveAgent(ctx context.Context, db *gorm.DB, agent *Agent) error {
	return db.WithContext(ctx).Save(agent).Error
}

// ListAgents returns every agent userID may use: the ones it owns plus the
// public agents other users share, ordered by name. Agents are not paginated: a
// user has few of them, and the app wants the whole set at once, which is the
// same call the provider list makes.
func ListAgents(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]Agent, error) {
	var agents []Agent
	err := db.WithContext(ctx).
		Where("user_id = ? OR is_public = ?", userID, true).
		Order("name").Order("id").
		Find(&agents).Error
	if err != nil {
		return nil, err
	}
	return agents, nil
}

// ListAgentNames returns the id and name of every agent userID may use: the ones
// it owns plus the public agents other users share, ordered by name then id. It
// selects only those two columns, so a listing that only needs names the user can
// pick never reads prompts or tools. The order matches ListAgents, which is what
// makes a name that two agents share resolve to the same row on both sides.
func ListAgentNames(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]AgentName, error) {
	names := []AgentName{}
	err := db.WithContext(ctx).
		Model(&Agent{}).
		Where("user_id = ? OR is_public = ?", userID, true).
		Order("name").Order("id").
		Select("id", "name").
		Scan(&names).Error
	if err != nil {
		return nil, err
	}
	return names, nil
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
