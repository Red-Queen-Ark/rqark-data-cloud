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

// AgentDefaultDTO is one default-agent entry in its wire form. The owner is left
// out: a default is only ever visible to the user it belongs to. Source carries
// the same value as a session's source.
type AgentDefaultDTO struct {
	ID        uuid.UUID `json:"id" example:"3f9a1c7e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Source    string    `json:"source" example:"TUI"`
	AgentID   uuid.UUID `json:"agent_id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PutAgentDefaultRequest is the body of PUT /api/agent-defaults/{source}.
// agent_id is required and must name one of the caller's agents.
type PutAgentDefaultRequest struct {
	AgentID uuid.UUID `json:"agent_id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
}

// toAgentDefaultDTO converts a stored default into its wire form.
func toAgentDefaultDTO(default_ *repository.AgentDefault) AgentDefaultDTO {
	return AgentDefaultDTO{
		ID:        default_.ID,
		Source:    default_.Source,
		AgentID:   default_.AgentID,
		CreatedAt: default_.CreatedAt,
		UpdatedAt: default_.UpdatedAt,
	}
}

// ListAgentDefaults returns every default agent of the caller.
//
//	@Summary		List agent defaults
//	@Description	Returns all of the caller's default agents, ordered by source. Not paginated: a user names one source per client it runs and the app wants the whole set at once.
//	@Tags			agent-defaults
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		AgentDefaultDTO
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/agent-defaults [get]
func ListAgentDefaults(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	defaults, err := service.ListAgentDefaults(c.Request.Context(), actor.ID)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]AgentDefaultDTO, 0, len(defaults))
	for i := range defaults {
		items = append(items, toAgentDefaultDTO(&defaults[i]))
	}
	c.JSON(http.StatusOK, items)
}

// GetAgentDefault returns the caller's default agent for a source.
//
//	@Summary		Get a default agent
//	@Description	Returns the caller's default agent for one source. A source that holds no default is reported as 404.
//	@Tags			agent-defaults
//	@Security		BearerAuth
//	@Produce		json
//	@Param			source	path		string	true	"Session source"	example(TUI)
//	@Success		200		{object}	AgentDefaultDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/agent-defaults/{source} [get]
func GetAgentDefault(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	default_, err := service.GetAgentDefault(c.Request.Context(), actor.ID, c.Param("source"))
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAgentDefaultDTO(default_))
}

// PutAgentDefault creates or overwrites the caller's default agent for a source.
//
//	@Summary		Set a default agent
//	@Description	Upserts the caller's default agent for one source: the agent of an existing source is overwritten, a source that does not exist is created. The agent has to belong to the caller.
//	@Tags			agent-defaults
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			source	path		string					true	"Session source"	example(TUI)
//	@Param			request	body		PutAgentDefaultRequest	true	"Default agent to set"
//	@Success		200		{object}	AgentDefaultDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/agent-defaults/{source} [put]
func PutAgentDefault(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	source := c.Param("source")

	var request PutAgentDefaultRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	default_, err := service.SetAgentDefault(c.Request.Context(), actor.ID, source, request.AgentID)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAgentDefaultDTO(default_))
}

// DeleteAgentDefault deletes the caller's default agent for a source.
//
//	@Summary		Delete a default agent
//	@Description	Hard delete of the caller's default agent for one source. A source that has no default is reported as 404.
//	@Tags			agent-defaults
//	@Security		BearerAuth
//	@Param			source	path	string	true	"Session source"	example(TUI)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		404	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/agent-defaults/{source} [delete]
func DeleteAgentDefault(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	source := c.Param("source")

	if err := service.DeleteAgentDefault(c.Request.Context(), actor.ID, source); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AgentDefaultsRouter registers the agent default routes. There is no superuser
// path here: every default belongs to the caller, and every query is scoped to
// the user behind the access token or the API key.
func AgentDefaultsRouter(r *gin.Engine) {
	defaults := r.Group("/api/agent-defaults", authOrAPIKey())
	defaults.GET("", ListAgentDefaults)
	defaults.GET("/:source", GetAgentDefault)
	defaults.PUT("/:source", PutAgentDefault)
	defaults.DELETE("/:source", DeleteAgentDefault)
}
