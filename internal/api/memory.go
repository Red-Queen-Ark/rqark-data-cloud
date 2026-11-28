package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rqark-data-cloud/internal/middlewares"
	"rqark-data-cloud/internal/repository"
	"rqark-data-cloud/internal/service"
	"rqark-data-cloud/internal/utils"
)

const (
	// memoryDefaultLimit is the page size of GET /api/memory when limit is
	// absent, invalid or above the ceiling.
	memoryDefaultLimit = 100
	// memoryMaxLimit is the largest page a caller may ask for.
	memoryMaxLimit = 100
)

// MemoryDTO is one memory record in its wire form. The owner is left out: a row
// is only ever visible to the user it belongs to. source_message is left out
// too; the single-record endpoint carries it.
type MemoryDTO struct {
	ID         uuid.UUID  `json:"id" example:"7c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	AgentID    uuid.UUID  `json:"agent_id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Content    string     `json:"content" example:"Boss prefers short replies."`
	Keywords   []string   `json:"keywords" example:"reply,style"`
	Kind       string     `json:"kind" example:"fact" enums:"fact,event"`
	Hits       int        `json:"hits" example:"2"`
	Source     string     `json:"source" example:"TUI"`
	OccurredAt time.Time  `json:"occurred_at"`
	Pinned     bool       `json:"pinned" example:"false"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// MemoryDetailDTO is one memory record with the message it was extracted from.
type MemoryDetailDTO struct {
	ID            uuid.UUID       `json:"id" example:"7c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	AgentID       uuid.UUID       `json:"agent_id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Content       string          `json:"content" example:"Boss prefers short replies."`
	Keywords      []string        `json:"keywords" example:"reply,style"`
	Kind          string          `json:"kind" example:"fact" enums:"fact,event"`
	Hits          int             `json:"hits" example:"2"`
	Source        string          `json:"source" example:"TUI"`
	SourceMessage json.RawMessage `json:"source_message" swaggertype:"object"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Pinned        bool            `json:"pinned" example:"false"`
	LastUsedAt    *time.Time      `json:"last_used_at"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// PatchMemoryRequest is the body of PATCH /api/memory/{uuid}. pinned is the
// only field a caller may change.
type PatchMemoryRequest struct {
	Pinned bool `json:"pinned" example:"true"`
}

// MemoryPage is one keyset page of memory records. next_cursor is empty on the
// last page and is otherwise passed back as ?cursor= unchanged.
type MemoryPage struct {
	Items      []MemoryDTO `json:"items"`
	NextCursor string      `json:"next_cursor" example:""`
}

// CreateMemoryRequest is the body of POST /api/memory. id, user_id, hits,
// last_used_at and the timestamps are not accepted: the server owns them.
type CreateMemoryRequest struct {
	AgentID       uuid.UUID       `json:"agent_id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Content       string          `json:"content" example:"Boss prefers short replies."`
	Keywords      []string        `json:"keywords" example:"reply,style"`
	Kind          string          `json:"kind" example:"fact" enums:"fact,event"`
	Source        string          `json:"source" example:"TUI"`
	SourceMessage json.RawMessage `json:"source_message" swaggertype:"object"`
	OccurredAt    *time.Time      `json:"occurred_at"`
	Pinned        bool            `json:"pinned" example:"false"`
}

// PromptMemoryRequest is the body of POST /api/memory/prompt.
type PromptMemoryRequest struct {
	AgentID uuid.UUID `json:"agent_id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
}

// SearchMemoryRequest is the body of POST /api/memory/search. Every field but
// agent_id is optional. from and to bound occurred_at, both inclusive, so a
// caller can ask for one stretch of the log; at is the older spelling of to and
// is kept so a client that has not been updated keeps working.
type SearchMemoryRequest struct {
	AgentID  uuid.UUID  `json:"agent_id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Query    string     `json:"query" example:"reply"`
	Keywords []string   `json:"keywords" example:"reply,style"`
	From     *time.Time `json:"from" example:"2026-09-01T00:00:00Z"`
	To       *time.Time `json:"to" example:"2026-09-30T23:59:59Z"`
	At       *time.Time `json:"at"`
}

// upperBound is the end of the window the caller asked for: to when it sent one,
// otherwise at. Sending both is not an error; to is the newer name and wins.
func (r SearchMemoryRequest) upperBound() *time.Time {
	if r.To != nil {
		return r.To
	}
	return r.At
}

// AgentStateDTO is the state board of one (user, agent) pair in its wire form.
// The pair itself is left out: a caller only ever sees its own.
type AgentStateDTO struct {
	AgentID   uuid.UUID `json:"agent_id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Content   string    `json:"content" example:"Working on the memory rework."`
	Version   int       `json:"version" example:"7"`
	Source    string    `json:"source" example:"TUI"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`
}

// PutAgentStateRequest is the body of PUT /api/memory/state. version is the
// version the caller read; leaving it out overwrites whatever is stored.
type PutAgentStateRequest struct {
	AgentID uuid.UUID `json:"agent_id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Content string    `json:"content" example:"Working on the memory rework."`
	Version *int      `json:"version" example:"7"`
	Source  string    `json:"source" example:"TUI"`
}

// MemoryPromptResponse is what the injection prompt asks for in one round trip:
// the current state board plus the fact rows.
type MemoryPromptResponse struct {
	State    *AgentStateDTO `json:"state"`
	Memories []MemoryDTO    `json:"memories"`
}

// toAgentStateDTO converts a stored state row into its wire form.
func toAgentStateDTO(state *repository.AgentState) AgentStateDTO {
	return AgentStateDTO{
		AgentID:   state.AgentID,
		Content:   state.Content,
		Version:   state.Version,
		Source:    state.Source,
		UpdatedAt: state.UpdatedAt,
		CreatedAt: state.CreatedAt,
	}
}

// toMemoryDTO converts a stored row into its list form. keywords is reported as
// an empty list rather than null so a client never has to branch on it.
func toMemoryDTO(memory *repository.Memory) MemoryDTO {
	keywords := []string(memory.Keywords)
	if keywords == nil {
		keywords = []string{}
	}
	return MemoryDTO{
		ID:         memory.ID,
		AgentID:    memory.AgentID,
		Content:    memory.Content,
		Keywords:   keywords,
		Kind:       memory.Kind,
		Hits:       memory.Hits,
		Source:     memory.Source,
		OccurredAt: memory.OccurredAt,
		Pinned:     memory.Pinned,
		LastUsedAt: memory.LastUsedAt,
		CreatedAt:  memory.CreatedAt,
		UpdatedAt:  memory.UpdatedAt,
	}
}

// toMemoryDetailDTO converts a stored row into its single-record form.
func toMemoryDetailDTO(memory *repository.Memory) MemoryDetailDTO {
	keywords := []string(memory.Keywords)
	if keywords == nil {
		keywords = []string{}
	}
	return MemoryDetailDTO{
		ID:            memory.ID,
		AgentID:       memory.AgentID,
		Content:       memory.Content,
		Keywords:      keywords,
		Kind:          memory.Kind,
		Hits:          memory.Hits,
		Source:        memory.Source,
		SourceMessage: memory.SourceMessage,
		OccurredAt:    memory.OccurredAt,
		Pinned:        memory.Pinned,
		LastUsedAt:    memory.LastUsedAt,
		CreatedAt:     memory.CreatedAt,
		UpdatedAt:     memory.UpdatedAt,
	}
}

// ListMemory returns one keyset page of the caller's memory.
//
//	@Summary		List memory
//	@Description	Returns the caller's memory records ordered by occurred_at descending, then id descending. agent_id narrows the page to one agent the caller may use, its own or a public one; leaving it out covers every agent. The page continues from cursor, which is the next_cursor of the previous response. Rows are not ordered by score: score depends on now(), so a cursor built from it would drift and repeat or skip rows between pages.
//	@Tags			memory
//	@Security		BearerAuth
//	@Produce		json
//	@Param			agent_id	query		string	false	"Agent ID"	format(uuid)
//	@Param			kind		query		string	false	"Restrict to one kind: fact or event"	Enums(fact, event)
//	@Param			cursor		query		string	false	"next_cursor of the previous page"
//	@Param			limit		query		int		false	"Page size, default 100, at most 100"
//	@Success		200			{object}	MemoryPage
//	@Failure		400			{object}	utils.ErrorResponse
//	@Failure		401			{object}	utils.ErrorResponse
//	@Failure		404			{object}	utils.ErrorResponse
//	@Failure		500			{object}	utils.ErrorResponse
//	@Router			/memory [get]
func ListMemory(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var agentID *uuid.UUID
	if raw := c.Query("agent_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			abortBadRequest(c, "agent_id must be a UUID.")
			return
		}
		agentID = &id
	}

	var cursor *repository.MemoryCursor
	if raw := c.Query("cursor"); raw != "" {
		parsed, err := decodeMemoryCursor(raw)
		if err != nil {
			abortBadRequest(c, "cursor is not valid.")
			return
		}
		cursor = parsed
	}

	limit := memoryPageLimit(c.Query("limit"))
	memories, err := service.ListMemories(c.Request.Context(), actor.ID, agentID, c.Query("kind"), cursor, limit)
	if err != nil {
		abortError(c, err)
		return
	}

	items := make([]MemoryDTO, 0, len(memories))
	for i := range memories {
		items = append(items, toMemoryDTO(&memories[i]))
	}
	page := MemoryPage{Items: items}
	if len(memories) == limit {
		page.NextCursor = encodeMemoryCursor(&memories[len(memories)-1])
	}
	c.JSON(http.StatusOK, page)
}

// GetMemory returns one memory record.
//
//	@Summary		Get a memory record
//	@Description	Returns one of the caller's records, source_message included. A record that does not exist or belongs to another pair is reported as 404.
//	@Tags			memory
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Memory ID"	format(uuid)
//	@Success		200		{object}	MemoryDetailDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/memory/{uuid} [get]
func GetMemory(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	memory, err := service.GetMemory(c.Request.Context(), actor.ID, id)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toMemoryDetailDTO(memory))
}

// CreateMemory appends one memory record.
//
//	@Summary		Create a memory record
//	@Description	Appends one record to the caller's memory log. The agent must belong to the caller. content is limited to 200 characters and keywords to 8 entries; a longer value is rejected rather than truncated. occurred_at is required because it drives the score decay. Pinning at creation counts against the same limit of 5 pinned rows per (user, agent) pair.
//	@Tags			memory
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateMemoryRequest	true	"Record to append"
//	@Success		201		{object}	MemoryDetailDTO
//	@Header			201		{string}	Location	"URL of the created record"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/memory [post]
func CreateMemory(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request CreateMemoryRequest
	if err := decodeStrictJSON(c, &request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	var occurredAt time.Time
	if request.OccurredAt != nil {
		occurredAt = *request.OccurredAt
	}

	memory, err := service.CreateMemory(c.Request.Context(), actor.ID, service.CreateMemoryInput{
		AgentID:       request.AgentID,
		Content:       request.Content,
		Keywords:      request.Keywords,
		Kind:          request.Kind,
		Source:        request.Source,
		SourceMessage: request.SourceMessage,
		OccurredAt:    occurredAt,
		Pinned:        request.Pinned,
	})
	if err != nil {
		abortError(c, err)
		return
	}

	c.Header("Location", "/api/memory/"+memory.ID.String())
	c.JSON(http.StatusCreated, toMemoryDetailDTO(memory))
}

// PatchMemory pins or unpins one memory record.
//
//	@Summary		Pin or unpin a memory record
//	@Description	The only field a caller may change is pinned; content, occurred_at and every other field are immutable, and naming one answers 400 IMMUTABLE_FIELD. A corrected record is written as a new row, not an edit. At most 5 rows of one (user, agent) pair may be pinned.
//	@Tags			memory
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string				true	"Memory ID"	format(uuid)
//	@Param			request	body		PatchMemoryRequest	true	"Pin state"
//	@Success		200		{object}	MemoryDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/memory/{uuid} [patch]
func PatchMemory(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var body map[string]json.RawMessage
	if err := decodeStrictJSON(c, &body); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}
	if code, message, ok := checkPatchFields(body); !ok {
		utils.Abort(c, code, message)
		return
	}

	var pinned bool
	if err := json.Unmarshal(body["pinned"], &pinned); err != nil {
		abortBadRequest(c, "pinned must be a boolean.")
		return
	}

	memory, err := service.SetMemoryPinned(c.Request.Context(), actor.ID, id, pinned)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toMemoryDTO(memory))
}

// DeleteMemory deletes one memory record.
//
//	@Summary		Delete a memory record
//	@Description	Hard delete of one of the caller's records. This is how a wrong record is corrected: the agent cannot edit or delete, so only the user removes it.
//	@Tags			memory
//	@Security		BearerAuth
//	@Param			uuid	path	string	true	"Memory ID"	format(uuid)
//	@Success		204
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/memory/{uuid} [delete]
func DeleteMemory(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	if err := service.DeleteMemory(c.Request.Context(), actor.ID, id); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SearchMemory returns the most relevant memory records of one agent.//
//
//	@Summary		Search memory
//	@Description	Returns at most 20 records of one agent the caller may use. Rows are ranked by how many of the requested keywords they carry, then by the decayed score, then by recency; pinned is ignored here and only orders the injection prompt. query matches content or a keyword as a substring. from and to bound occurred_at, both inclusive, so one call can ask for a stretch of the log (a month, a week) instead of everything; at is the older name for to and still works. Every returned row counts as one hit, recorded at most once per UTC day.
//	@Tags			memory
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		SearchMemoryRequest	true	"Search terms"
//	@Success		200		{array}		MemoryDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/memory/search [post]
func SearchMemory(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request SearchMemoryRequest
	if err := decodeStrictJSON(c, &request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	memories, err := service.SearchMemories(c.Request.Context(), actor.ID, request.AgentID, request.Query, request.Keywords, request.From, request.upperBound())
	if err != nil {
		abortError(c, err)
		return
	}

	items := make([]MemoryDTO, 0, len(memories))
	for i := range memories {
		items = append(items, toMemoryDTO(&memories[i]))
	}
	c.JSON(http.StatusOK, items)
}

// PromptMemory returns the state board and the records the injection prompt
// carries for one agent.
//
//	@Summary		Memory injection prompt
//	@Description	One round trip for a turn's head: state is the current state board (null when the pair has none yet) and memories are at most 20 fact rows of one agent the caller may use, ordered pinned first, then the decayed score, then recency. This is the one ordering the injection prompt uses and search does not: pinned rows here are exempt from decay. Events are never injected. Every returned memory row counts as injected, recorded at most once per UTC day.
//	@Tags			memory
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		PromptMemoryRequest	true	"Agent to read"
//	@Success		200		{object}	MemoryPromptResponse
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/memory/prompt [post]
func PromptMemory(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request PromptMemoryRequest
	if err := decodeStrictJSON(c, &request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	memories, err := service.PromptMemories(c.Request.Context(), actor.ID, request.AgentID)
	if err != nil {
		abortError(c, err)
		return
	}
	state, err := service.AgentStateOrNil(c.Request.Context(), actor.ID, request.AgentID)
	if err != nil {
		abortError(c, err)
		return
	}

	items := make([]MemoryDTO, 0, len(memories))
	for i := range memories {
		items = append(items, toMemoryDTO(&memories[i]))
	}
	response := MemoryPromptResponse{Memories: items}
	if state != nil {
		dto := toAgentStateDTO(state)
		response.State = &dto
	}
	c.JSON(http.StatusOK, response)
}

// GetAgentState returns the state board of one agent.
//
//	@Summary		Get the state board
//	@Description	Returns the current state board of one agent the caller may use. A pair that has never been written answers 404 NOT_FOUND: an agent without a state is not an error, it simply has nothing to show.
//	@Tags			memory
//	@Security		BearerAuth
//	@Produce		json
//	@Param			agent_id	query		string	true	"Agent ID"	format(uuid)
//	@Success		200			{object}	AgentStateDTO
//	@Failure		400			{object}	utils.ErrorResponse
//	@Failure		401			{object}	utils.ErrorResponse
//	@Failure		404			{object}	utils.ErrorResponse
//	@Failure		500			{object}	utils.ErrorResponse
//	@Router			/memory/state [get]
func GetAgentState(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	agentID, ok := stateAgentID(c)
	if !ok {
		return
	}

	state, err := service.GetAgentState(c.Request.Context(), actor.ID, agentID)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAgentStateDTO(state))
}

// PutAgentState overwrites the state board of one agent.
//
//	@Summary		Overwrite the state board
//	@Description	Upserts the state board of one agent the caller may use. The agent may write here, unlike memory, which only the user deletes. version, when sent, is the version the caller read: a write carrying a version the row no longer holds answers 409 STATE_CONFLICT and changes nothing, so a stale writer cannot silently undo a newer board. Omitting version overwrites whatever is stored. content is markdown, at most 8000 characters, and blank is refused: DELETE removes the board.
//	@Tags			memory
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		PutAgentStateRequest	true	"State to store"
//	@Success		200		{object}	AgentStateDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/memory/state [put]
func PutAgentState(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request PutAgentStateRequest
	if err := decodeStrictJSON(c, &request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	state, err := service.SetAgentState(c.Request.Context(), actor.ID, service.SetAgentStateInput{
		AgentID: request.AgentID,
		Content: request.Content,
		Source:  request.Source,
		Version: request.Version,
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAgentStateDTO(state))
}

// DeleteAgentState removes the state board of one agent.
//
//	@Summary		Delete the state board
//	@Description	Hard deletes the state board of one agent the caller may use. The next write starts again at version 1.
//	@Tags			memory
//	@Security		BearerAuth
//	@Param			agent_id	query	string	true	"Agent ID"	format(uuid)
//	@Success		204
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/memory/state [delete]
func DeleteAgentState(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	agentID, ok := stateAgentID(c)
	if !ok {
		return
	}

	if err := service.DeleteAgentState(c.Request.Context(), actor.ID, agentID); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// stateAgentID reads the required agent_id query parameter of the state
// endpoints. A missing or malformed one is a bad request rather than a lookup of
// the zero uuid.
func stateAgentID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.Query("agent_id")
	if raw == "" {
		abortBadRequest(c, "agent_id is required.")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		abortBadRequest(c, "agent_id must be a UUID.")
		return uuid.Nil, false
	}
	return id, true
}

// MemoryRouter registers the memory routes.
func MemoryRouter(r *gin.Engine) {
	memory := r.Group("/api/memory", authOrAPIKey())
	memory.GET("", ListMemory)
	memory.POST("", CreateMemory)
	memory.POST("/search", SearchMemory)
	memory.POST("/prompt", PromptMemory)
	memory.GET("/state", GetAgentState)
	memory.PUT("/state", PutAgentState)
	memory.DELETE("/state", DeleteAgentState)
	memory.GET("/:uuid", GetMemory)
	memory.PATCH("/:uuid", PatchMemory)
	memory.DELETE("/:uuid", DeleteMemory)
}

// memoryPageLimit reads the limit query parameter. A missing, non-numeric,
// non-positive or oversized value falls back to the default page size.
func memoryPageLimit(raw string) int {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > memoryMaxLimit {
		return memoryDefaultLimit
	}
	return limit
}

// encodeMemoryCursor turns a row position into the opaque cursor a client passes
// back. It carries the (occurred_at, id) pair the keyset comparison needs.
func encodeMemoryCursor(memory *repository.Memory) string {
	raw := memory.OccurredAt.UTC().Format(time.RFC3339Nano) + "|" + memory.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeMemoryCursor parses a cursor produced by encodeMemoryCursor.
func decodeMemoryCursor(value string) (*repository.MemoryCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	occurredAt, id, found := strings.Cut(string(decoded), "|")
	if !found {
		return nil, errors.New("api: cursor has no separator")
	}
	at, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return nil, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return &repository.MemoryCursor{OccurredAt: at, ID: parsed}, nil
}

// decodeStrictJSON decodes one JSON document into dst and refuses a field dst
// does not name.
func decodeStrictJSON(c *gin.Context, dst any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

// checkPatchFields reports whether a PATCH body may be applied. pinned is the
// only writable field; any other field of a memory record is immutable and
// answers 400 IMMUTABLE_FIELD, while an unknown field is a bad request. A body
// without pinned is a bad request too.
func checkPatchFields(body map[string]json.RawMessage) (utils.Code, string, bool) {
	readonly := map[string]bool{
		"id":             true,
		"user_id":        true,
		"agent_id":       true,
		"content":        true,
		"keywords":       true,
		"kind":           true,
		"hits":           true,
		"source":         true,
		"source_message": true,
		"occurred_at":    true,
		"last_used_at":   true,
		"created_at":     true,
		"updated_at":     true,
	}
	for field := range body {
		if field == "pinned" {
			continue
		}
		if readonly[field] {
			return utils.ImmutableField, field + " cannot be changed.", false
		}
		return utils.BadRequest, "Unknown field " + field + ".", false
	}
	if _, ok := body["pinned"]; !ok {
		return utils.BadRequest, "pinned is required.", false
	}
	return "", "", true
}
