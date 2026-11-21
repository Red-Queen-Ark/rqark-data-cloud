package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rqark-data-cloud/internal/middlewares"
	"rqark-data-cloud/internal/repository"
	"rqark-data-cloud/internal/service"
	"rqark-data-cloud/internal/utils"
)

// TodoDTO is the todo representation returned by every todo endpoint. The owner
// is left out: a todo is only ever visible to the user it belongs to. done_at is
// derived from done by the server, never written from a request.
type TodoDTO struct {
	ID          uuid.UUID  `json:"id" example:"5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Title       string     `json:"title" example:"寫 issue"`
	Description string     `json:"description" example:""`
	Done        bool       `json:"done" example:"false"`
	DoneAt      *time.Time `json:"done_at"`
	DueAt       *time.Time `json:"due_at"`
	SortOrder   int        `json:"sort_order" example:"0"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// CreateTodoRequest is the body of POST /api/todos. done starts false and
// done_at is derived, so neither is accepted here.
type CreateTodoRequest struct {
	Title       string     `json:"title" example:"寫 issue"`
	Description string     `json:"description"`
	DueAt       *time.Time `json:"due_at"`
	SortOrder   int        `json:"sort_order" example:"0"`
}

// UpdateTodoRequest is the body of PUT /api/todos/{uuid}. Every field is
// optional: a field that is absent leaves the stored value alone, while an
// explicit null for due_at clears the due date. done_at follows done and is not
// accepted here.
type UpdateTodoRequest struct {
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Done        *bool      `json:"done"`
	DueAt       *time.Time `json:"due_at"`
	SortOrder   *int       `json:"sort_order"`

	// present holds the keys the body carried. encoding/json decodes an absent
	// key and an explicit null to the same nil pointer, and the two mean
	// different things here, so the keys are collected on the side.
	present map[string]bool
}

// UnmarshalJSON decodes the body and records which keys it carried.
func (r *UpdateTodoRequest) UnmarshalJSON(data []byte) error {
	// plain has no methods, so decoding into it cannot recurse into this one.
	type plain UpdateTodoRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}

	*r = UpdateTodoRequest(decoded)
	r.present = make(map[string]bool, len(keys))
	for key := range keys {
		r.present[key] = true
	}
	return nil
}

// provided reports whether the body carried key.
func (r UpdateTodoRequest) provided(key string) bool { return r.present[key] }

// toTodoDTO converts a stored todo into its wire form, nullable columns and all:
// a NULL due_at or done_at is reported as null, never as a zero time.
func toTodoDTO(todo *repository.Todo) TodoDTO {
	return TodoDTO{
		ID:          todo.ID,
		Title:       todo.Title,
		Description: todo.Description,
		Done:        todo.Done,
		DoneAt:      todo.DoneAt,
		DueAt:       todo.DueAt,
		SortOrder:   todo.SortOrder,
		CreatedAt:   todo.CreatedAt,
		UpdatedAt:   todo.UpdatedAt,
	}
}

// ListTodos returns the caller's todo list.
//
//	@Summary		List todos
//	@Description	Returns every todo of the caller, open ones first, then by sort_order and creation time. An optional done filter keeps one half of the list. Not paginated: it is one user's own list, which the client syncs whole.
//	@Tags			todos
//	@Security		BearerAuth
//	@Produce		json
//	@Param			done	query		bool	false	"Keep only open (false) or done (true) items"
//	@Success		200		{array}		TodoDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/todos [get]
func ListTodos(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var done *bool
	if raw := c.Query("done"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			abortBadRequest(c, "done must be true or false.")
			return
		}
		done = &value
	}

	todos, err := service.ListTodos(c.Request.Context(), actor.ID, done)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]TodoDTO, 0, len(todos))
	for i := range todos {
		items = append(items, toTodoDTO(&todos[i]))
	}
	c.JSON(http.StatusOK, items)
}

// CreateTodo creates a todo.
//
//	@Summary		Create a todo
//	@Description	The todo belongs to the caller and starts open. Answers 201 with a Location header pointing at the new todo.
//	@Tags			todos
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateTodoRequest	true	"Todo to create"
//	@Success		201		{object}	TodoDTO
//	@Header			201		{string}	Location	"URL of the created todo"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/todos [post]
func CreateTodo(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request CreateTodoRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	todo, err := service.CreateTodo(c.Request.Context(), actor.ID, service.CreateTodoInput{
		Title:       request.Title,
		Description: request.Description,
		DueAt:       request.DueAt,
		SortOrder:   request.SortOrder,
	})
	if err != nil {
		abortError(c, err)
		return
	}

	c.Header("Location", "/api/todos/"+todo.ID.String())
	c.JSON(http.StatusCreated, toTodoDTO(todo))
}

// GetTodo returns one todo.
//
//	@Summary		Get a todo
//	@Description	Returns one of the caller's todos. Another user's todo is reported as 404.
//	@Tags			todos
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Todo ID"	format(uuid)
//	@Success		200		{object}	TodoDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/todos/{uuid} [get]
func GetTodo(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	todo, err := service.GetTodo(c.Request.Context(), actor.ID, id)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTodoDTO(todo))
}

// UpdateTodo updates one todo.
//
//	@Summary		Update a todo
//	@Description	Partial update of one of the caller's todos: an absent field is left untouched, an explicit null for due_at clears the due date. Ticking done stamps done_at, unticking it clears the stamp. Another user's todo is reported as 404.
//	@Tags			todos
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string				true	"Todo ID"	format(uuid)
//	@Param			request	body		UpdateTodoRequest	true	"Fields to change"
//	@Success		200		{object}	TodoDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/todos/{uuid} [put]
func UpdateTodo(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var request UpdateTodoRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	todo, err := service.UpdateTodo(c.Request.Context(), actor.ID, id, service.UpdateTodoInput{
		Title:       request.Title,
		Description: request.Description,
		Done:        request.Done,
		DueAt:       optional(request.DueAt, request.provided("due_at")),
		SortOrder:   request.SortOrder,
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTodoDTO(todo))
}

// DeleteTodo deletes one todo.
//
//	@Summary		Delete a todo
//	@Description	Hard delete of one of the caller's todos. Another user's todo is reported as 404.
//	@Tags			todos
//	@Security		BearerAuth
//	@Param			uuid	path	string	true	"Todo ID"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		404	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/todos/{uuid} [delete]
func DeleteTodo(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	if err := service.DeleteTodo(c.Request.Context(), actor.ID, id); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// TodosRouter registers the todo routes. There is no superuser path here: every
// item belongs to the caller, and every query is scoped to the user behind the
// access token or the API key.
func TodosRouter(r *gin.Engine) {
	todos := r.Group("/api/todos", authOrAPIKey())
	todos.GET("", ListTodos)
	todos.POST("", CreateTodo)
	todos.GET("/:uuid", GetTodo)
	todos.PUT("/:uuid", UpdateTodo)
	todos.DELETE("/:uuid", DeleteTodo)
}
