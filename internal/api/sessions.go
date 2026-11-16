package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/middlewares"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// SessionDTO is the session metadata returned by every session endpoint. The
// owner is left out, and so is the history: a session is only ever visible to
// the user it belongs to, and its messages are read from their own endpoint so
// that a listing does not carry every message of every session.
type SessionDTO struct {
	ID        uuid.UUID                    `json:"id" example:"6b1c1f3a-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Title     string                       `json:"title" example:"Trip planning"`
	Source    string                       `json:"source" example:"TUI"`
	ProjectID *uuid.UUID                   `json:"project_id" example:"0f0b7f9a-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	AgentID   *uuid.UUID                   `json:"agent_id" example:"7c2d4e5f-2b2b-4b2b-9b2b-2b2b2b2b2b2b"`
	PlanItems []repository.SessionPlanItem `json:"plan_items"`
	CreatedAt time.Time                    `json:"created_at"`
	UpdatedAt time.Time                    `json:"updated_at"`
}

// SessionMessageDTO is one history entry. Its data is handed back exactly as it
// was stored; role and created_at repeat the two values the cloud copies out of
// it. created_at is null for a message that carries none, which is the summary
// case.
type SessionMessageDTO struct {
	Seq       int            `json:"seq" example:"0"`
	Role      string         `json:"role" example:"user"`
	Data      map[string]any `json:"data"`
	CreatedAt *time.Time     `json:"created_at"`
}

// CreateSessionRequest is the body of POST /api/sessions. The app may bring its
// own uuid, the one it names the session's local file after; when it does not
// the cloud mints one. Source names the application the session was uploaded
// from and defaults to the empty string. project_id names one of the caller's
// projects; omitted or null is a session that works at the volume root.
// agent_id names the agent the session is talked to with; omitted or null is a
// session that names no agent.
type CreateSessionRequest struct {
	UUID      *string                       `json:"uuid" example:"6b1c1f3a-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Title     *string                       `json:"title" example:"Trip planning"`
	Source    *string                       `json:"source" example:"TUI"`
	ProjectID *string                       `json:"project_id" example:"0f0b7f9a-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	AgentID   *string                       `json:"agent_id" example:"7c2d4e5f-2b2b-4b2b-9b2b-2b2b2b2b2b2b"`
	PlanItems *[]repository.SessionPlanItem `json:"plan_items"`
}

// UpdateSessionRequest is the body of PUT /api/sessions/{uuid}. Every field is
// optional and a field that is absent is left untouched. A history replaces the
// session's messages wholesale. An explicit null for project_id or agent_id
// detaches the session from its project or its agent.
type UpdateSessionRequest struct {
	Title     *string                       `json:"title"`
	Source    *string                       `json:"source"`
	ProjectID *string                       `json:"project_id"`
	AgentID   *string                       `json:"agent_id"`
	PlanItems *[]repository.SessionPlanItem `json:"plan_items"`
	History   *[]map[string]any             `json:"history"`

	// present holds the keys the body carried. encoding/json decodes an absent
	// key and an explicit null to the same nil pointer, and the two mean
	// different things for project_id, so the keys are collected on the side.
	present map[string]bool
}

// UnmarshalJSON decodes the body and records which keys it carried.
func (r *UpdateSessionRequest) UnmarshalJSON(data []byte) error {
	// plain has no methods, so decoding into it cannot recurse into this one.
	type plain UpdateSessionRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}

	*r = UpdateSessionRequest(decoded)
	r.present = make(map[string]bool, len(keys))
	for key := range keys {
		r.present[key] = true
	}
	return nil
}

// provided reports whether the body carried key.
func (r UpdateSessionRequest) provided(key string) bool { return r.present[key] }

// toSessionDTO converts a stored session into its wire form. A nil plan_items
// is reported as an empty list, mirroring the column default. A nil project_id
// or agent_id is reported as null, never as the zero uuid.
func toSessionDTO(session *repository.Session) SessionDTO {
	planItems := session.PlanItems
	if planItems == nil {
		planItems = []repository.SessionPlanItem{}
	}
	return SessionDTO{
		ID:        session.ID,
		Title:     session.Title,
		Source:    session.Source,
		ProjectID: session.ProjectID,
		AgentID:   session.AgentID,
		PlanItems: planItems,
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
	}
}

// toSessionMessageDTO converts a stored message into its wire form.
func toSessionMessageDTO(message *repository.SessionMessage) SessionMessageDTO {
	return SessionMessageDTO{
		Seq:       message.Seq,
		Role:      message.Role,
		Data:      message.Data,
		CreatedAt: message.CreatedAt,
	}
}

// ListSessions returns every session of the caller.
//
//	@Summary		List sessions
//	@Description	Returns all of the caller's sessions, most recently updated first, without their messages. Not paginated: the app syncs its whole session set in one go. A source query keeps only sessions uploaded from that application; without one every source is returned. A project_id query keeps only sessions that work in that project.
//	@Tags			sessions
//	@Security		BearerAuth
//	@Produce		json
//	@Param			source		query		string	false	"Keep only sessions uploaded from this application"
//	@Param			project_id	query		string	false	"Keep only sessions that work in this project"	format(uuid)
//	@Success		200			{array}		SessionDTO
//	@Failure		400			{object}	utils.ErrorResponse
//	@Failure		401			{object}	utils.ErrorResponse
//	@Failure		500			{object}	utils.ErrorResponse
//	@Router			/sessions [get]
func ListSessions(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var source *string
	if value, present := c.GetQuery("source"); present {
		source = &value
	}

	var projectID *uuid.UUID
	if value, present := c.GetQuery("project_id"); present {
		id, err := uuid.Parse(value)
		if err != nil {
			abortBadRequest(c, "project_id must be a UUID.")
			return
		}
		projectID = &id
	}

	sessions, err := service.ListSessions(c.Request.Context(), actor.ID, source, projectID)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]SessionDTO, 0, len(sessions))
	for i := range sessions {
		items = append(items, toSessionDTO(&sessions[i]))
	}
	c.JSON(http.StatusOK, items)
}

// CreateSession creates a session.
//
//	@Summary		Create a session
//	@Description	The session belongs to the caller. A uuid the caller brings is kept; one it does not is minted here. Answers 201 with a Location header pointing at the new session.
//	@Tags			sessions
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateSessionRequest	true	"Session to create"
//	@Success		201		{object}	SessionDTO
//	@Header			201		{string}	Location	"URL of the created session"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/sessions [post]
func CreateSession(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request CreateSessionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	in := service.CreateSessionInput{}
	if request.UUID != nil {
		id, err := uuid.Parse(*request.UUID)
		if err != nil {
			abortBadRequest(c, "uuid must be a UUID.")
			return
		}
		in.ID = &id
	}
	if request.Title != nil {
		in.Title = *request.Title
	}
	if request.Source != nil {
		in.Source = *request.Source
	}
	if request.ProjectID != nil {
		id, err := uuid.Parse(*request.ProjectID)
		if err != nil {
			abortBadRequest(c, "project_id must be a UUID.")
			return
		}
		in.ProjectID = &id
	}
	if request.AgentID != nil {
		id, err := uuid.Parse(*request.AgentID)
		if err != nil {
			abortBadRequest(c, "agent_id must be a UUID.")
			return
		}
		in.AgentID = &id
	}
	if request.PlanItems != nil {
		in.PlanItems = *request.PlanItems
	}

	session, err := service.CreateSession(c.Request.Context(), actor.ID, in)
	if err != nil {
		abortError(c, err)
		return
	}

	c.Header("Location", "/api/sessions/"+session.ID.String())
	c.JSON(http.StatusCreated, toSessionDTO(session))
}

// GetSession returns one session's metadata.
//
//	@Summary		Get a session
//	@Description	Returns one of the caller's sessions without its messages. Another user's session is reported as 404.
//	@Tags			sessions
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Session ID"	format(uuid)
//	@Success		200		{object}	SessionDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/sessions/{uuid} [get]
func GetSession(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	session, err := service.GetSession(c.Request.Context(), actor.ID, id)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSessionDTO(session))
}

// UpdateSession updates one session.
//
//	@Summary		Update a session
//	@Description	Every field is optional and a field that is absent is left untouched. A history replaces the session's messages wholesale. Another user's session is reported as 404.
//	@Tags			sessions
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string					true	"Session ID"	format(uuid)
//	@Param			request	body		UpdateSessionRequest	true	"Fields to change"
//	@Success		200		{object}	SessionDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/sessions/{uuid} [put]
func UpdateSession(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var request UpdateSessionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	projectID := utils.Optional[uuid.UUID]{Set: request.provided("project_id")}
	if request.ProjectID != nil {
		parsed, err := uuid.Parse(*request.ProjectID)
		if err != nil {
			abortBadRequest(c, "project_id must be a UUID.")
			return
		}
		projectID.Value = &parsed
	}

	agentID := utils.Optional[uuid.UUID]{Set: request.provided("agent_id")}
	if request.AgentID != nil {
		parsed, err := uuid.Parse(*request.AgentID)
		if err != nil {
			abortBadRequest(c, "agent_id must be a UUID.")
			return
		}
		agentID.Value = &parsed
	}

	session, err := service.UpdateSession(c.Request.Context(), actor.ID, id, service.UpdateSessionInput{
		Title:     request.Title,
		Source:    request.Source,
		ProjectID: projectID,
		AgentID:   agentID,
		PlanItems: request.PlanItems,
		History:   request.History,
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSessionDTO(session))
}

// DeleteSession deletes one session.
//
//	@Summary		Delete a session
//	@Description	Hard delete of one of the caller's sessions, its messages included. Another user's session is reported as 404.
//	@Tags			sessions
//	@Security		BearerAuth
//	@Param			uuid	path	string	true	"Session ID"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		404	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/sessions/{uuid} [delete]
func DeleteSession(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	if err := service.DeleteSession(c.Request.Context(), actor.ID, id); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListSessionMessages returns every message of one session.
//
//	@Summary		List session messages
//	@Description	Returns all messages of one of the caller's sessions in the order they were sent. Not paginated: the app loads a session's whole history, which is also what its own file storage holds. Another user's session is reported as 404.
//	@Tags			sessions
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Session ID"	format(uuid)
//	@Success		200		{array}		SessionMessageDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/sessions/{uuid}/messages [get]
func ListSessionMessages(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	messages, err := service.ListSessionMessages(c.Request.Context(), actor.ID, id)
	if err != nil {
		abortError(c, err)
		return
	}

	items := make([]SessionMessageDTO, 0, len(messages))
	for i := range messages {
		items = append(items, toSessionMessageDTO(&messages[i]))
	}
	c.JSON(http.StatusOK, items)
}

// SessionsRouter registers the session routes. Every session belongs to the
// caller, and every query is scoped to the user behind the access token or the
// API key. An API key is accepted because a session is what the app syncs.
func SessionsRouter(r *gin.Engine) {
	sessions := r.Group("/api/sessions", authOrAPIKey())
	sessions.GET("", ListSessions)
	sessions.POST("", CreateSession)
	sessions.GET("/:uuid", GetSession)
	sessions.PUT("/:uuid", UpdateSession)
	sessions.DELETE("/:uuid", DeleteSession)
	sessions.GET("/:uuid/messages", ListSessionMessages)
}
