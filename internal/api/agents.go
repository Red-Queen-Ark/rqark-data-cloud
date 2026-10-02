package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/middlewares"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// AgentDTO is the agent representation returned by every agent endpoint. The
// owner is left out: an agent is only ever visible to the user it belongs to.
type AgentDTO struct {
	ID          uuid.UUID                `json:"id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Name        string                   `json:"name" example:"Angel Holiday"`
	Description string                   `json:"description" example:""`
	Prompts     []repository.AgentPrompt `json:"prompts"`
	Tools       []repository.AgentTool   `json:"tools"`
	CreatedAt   time.Time                `json:"created_at"`
	UpdatedAt   time.Time                `json:"updated_at"`
}

// CreateAgentRequest is the body of POST /api/agents.
type CreateAgentRequest struct {
	Name        string                   `json:"name" example:"Angel Holiday"`
	Description string                   `json:"description"`
	Prompts     []repository.AgentPrompt `json:"prompts"`
	Tools       []repository.AgentTool   `json:"tools"`
}

// UpdateAgentRequest is the body of PUT /api/agents/{uuid}. Every field is
// optional and a field that is absent is left untouched.
type UpdateAgentRequest struct {
	Name        *string                   `json:"name"`
	Description *string                   `json:"description"`
	Prompts     *[]repository.AgentPrompt `json:"prompts"`
	Tools       *[]repository.AgentTool   `json:"tools"`
}

// toAgentDTO converts a stored agent into its wire form. The prompts and tools
// are passed through untouched; nil is reported as an empty list so that the
// two columns always mirror the table defaults.
func toAgentDTO(agent *repository.Agent) AgentDTO {
	prompts := agent.Prompts
	if prompts == nil {
		prompts = []repository.AgentPrompt{}
	}
	tools := agent.Tools
	if tools == nil {
		tools = []repository.AgentTool{}
	}
	return AgentDTO{
		ID:          agent.ID,
		Name:        agent.Name,
		Description: agent.Description,
		Prompts:     prompts,
		Tools:       tools,
		CreatedAt:   agent.CreatedAt,
		UpdatedAt:   agent.UpdatedAt,
	}
}

// ListAgents returns every agent of the caller.
//
//	@Summary		List agents
//	@Description	Returns all of the caller's agents, ordered by name. Not paginated: a user has few of them.
//	@Tags			agents
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		AgentDTO
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/agents [get]
func ListAgents(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	agents, err := service.ListAgents(c.Request.Context(), actor.ID)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]AgentDTO, 0, len(agents))
	for i := range agents {
		items = append(items, toAgentDTO(&agents[i]))
	}
	c.JSON(http.StatusOK, items)
}

// ListAgentNames returns the name of every agent of the caller.
//
//	@Summary		List agent names
//	@Description	Returns the name of all of the caller's agents, ordered by name. The rest of each agent is left out. Not paginated: a user has few of them.
//	@Tags			agents
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		string
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/agents/names [get]
func ListAgentNames(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	names, err := service.ListAgentNames(c.Request.Context(), actor.ID)
	if err != nil {
		abortInternal(c, err)
		return
	}
	c.JSON(http.StatusOK, names)
}

// CreateAgent creates an agent.
//
//	@Summary		Create an agent
//	@Description	The agent belongs to the caller. Answers 201 with a Location header pointing at the new agent.
//	@Tags			agents
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateAgentRequest	true	"Agent to create"
//	@Success		201		{object}	AgentDTO
//	@Header			201		{string}	Location	"URL of the created agent"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/agents [post]
func CreateAgent(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request CreateAgentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	agent, err := service.CreateAgent(c.Request.Context(), actor.ID, service.CreateAgentInput{
		Name:        request.Name,
		Description: request.Description,
		Prompts:     request.Prompts,
		Tools:       request.Tools,
	})
	if err != nil {
		abortError(c, err)
		return
	}

	c.Header("Location", "/api/agents/"+agent.ID.String())
	c.JSON(http.StatusCreated, toAgentDTO(agent))
}

// GetAgent returns one agent.
//
//	@Summary		Get an agent
//	@Description	Returns one of the caller's agents. Another user's agent is reported as 404.
//	@Tags			agents
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Agent ID"	format(uuid)
//	@Success		200		{object}	AgentDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/agents/{uuid} [get]
func GetAgent(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	agent, err := service.GetAgent(c.Request.Context(), actor.ID, id)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAgentDTO(agent))
}

// UpdateAgent updates one agent.
//
//	@Summary		Update an agent
//	@Description	Every field is optional and a field that is absent is left untouched. Another user's agent is reported as 404.
//	@Tags			agents
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string				true	"Agent ID"	format(uuid)
//	@Param			request	body		UpdateAgentRequest	true	"Fields to change"
//	@Success		200		{object}	AgentDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/agents/{uuid} [put]
func UpdateAgent(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var request UpdateAgentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	agent, err := service.UpdateAgent(c.Request.Context(), actor.ID, id, service.UpdateAgentInput{
		Name:        request.Name,
		Description: request.Description,
		Prompts:     request.Prompts,
		Tools:       request.Tools,
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAgentDTO(agent))
}

// DeleteAgent deletes one agent.
//
//	@Summary		Delete an agent
//	@Description	Hard delete of one of the caller's agents. Another user's agent is reported as 404.
//	@Tags			agents
//	@Security		BearerAuth
//	@Param			uuid	path	string	true	"Agent ID"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		404	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/agents/{uuid} [delete]
func DeleteAgent(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	if err := service.DeleteAgent(c.Request.Context(), actor.ID, id); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AgentsRouter registers the agent routes. There is no superuser path here:
// every agent belongs to the caller, and every query is scoped to the user
// behind the access token or the API key.
func AgentsRouter(r *gin.Engine) {
	agents := r.Group("/api/agents", authOrAPIKey())
	agents.GET("", ListAgents)
	agents.POST("", CreateAgent)
	agents.GET("/names", ListAgentNames)
	agents.GET("/:uuid", GetAgent)
	agents.PUT("/:uuid", UpdateAgent)
	agents.DELETE("/:uuid", DeleteAgent)
}
