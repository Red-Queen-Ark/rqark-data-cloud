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

// TaskDTO is the task representation returned by every task endpoint. The
// schedule is flat: schedule_kind says which of run_at, every_seconds and
// cron_expr is set, exactly as the table stores it. next_run_at is null for a
// disabled task.
type TaskDTO struct {
	ID           uuid.UUID  `json:"id" example:"8f0c2b7a-8a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Name         string     `json:"name" example:"Morning brief"`
	Prompt       string     `json:"prompt" example:"Summarise my inbox."`
	AgentName    string     `json:"agent_name" example:"Angel Holiday"`
	ProviderName string     `json:"provider_name" example:"openai"`
	Timezone     string     `json:"timezone" example:"Asia/Hong_Kong"`
	Enabled      bool       `json:"enabled" example:"true"`
	ScheduleKind string     `json:"schedule_kind" example:"cron"`
	RunAt        *time.Time `json:"run_at"`
	EverySeconds *int       `json:"every_seconds"`
	CronExpr     *string    `json:"cron_expr" example:"0 9 * * *"`
	NextRunAt    *time.Time `json:"next_run_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// TaskRunDTO is one attempt of a task. The owner is left out, and so is the
// task, which the listing already names.
type TaskRunDTO struct {
	ID          uuid.UUID  `json:"id"`
	TaskID      uuid.UUID  `json:"task_id"`
	Status      string     `json:"status" example:"ok"`
	Trigger     string     `json:"trigger" example:"schedule"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	Error       string     `json:"error"`
	SessionID   *uuid.UUID `json:"session_id"`
}

// CreateTaskRequest is the body of POST /api/tasks. schedule_kind picks the one
// schedule field the body may set. enabled defaults to true.
type CreateTaskRequest struct {
	Name         string     `json:"name" example:"Morning brief"`
	Prompt       string     `json:"prompt" example:"Summarise my inbox."`
	AgentName    string     `json:"agent_name" example:"Angel Holiday"`
	ProviderName string     `json:"provider_name" example:"openai"`
	Timezone     *string    `json:"timezone" example:"Asia/Hong_Kong"`
	Enabled      *bool      `json:"enabled" example:"true"`
	ScheduleKind string     `json:"schedule_kind" example:"cron"`
	RunAt        *time.Time `json:"run_at"`
	EverySeconds *int       `json:"every_seconds"`
	CronExpr     *string    `json:"cron_expr" example:"0 9 * * *"`
}

// UpdateTaskRequest is the body of PUT /api/tasks/{uuid}. Every field is
// optional and a field that is absent is left untouched. A schedule_kind
// replaces the whole schedule: the three shapes are not independently editable.
type UpdateTaskRequest struct {
	Name         *string    `json:"name"`
	Prompt       *string    `json:"prompt"`
	AgentName    *string    `json:"agent_name"`
	ProviderName *string    `json:"provider_name"`
	Timezone     *string    `json:"timezone"`
	Enabled      *bool      `json:"enabled"`
	ScheduleKind *string    `json:"schedule_kind"`
	RunAt        *time.Time `json:"run_at"`
	EverySeconds *int       `json:"every_seconds"`
	CronExpr     *string    `json:"cron_expr"`
}

// toTaskDTO converts a stored task into its wire form.
func toTaskDTO(task *repository.Task) TaskDTO {
	return TaskDTO{
		ID:           task.ID,
		Name:         task.Name,
		Prompt:       task.Prompt,
		AgentName:    task.AgentName,
		ProviderName: task.ProviderName,
		Timezone:     task.Timezone,
		Enabled:      task.Enabled,
		ScheduleKind: task.ScheduleKind,
		RunAt:        task.RunAt,
		EverySeconds: task.EverySeconds,
		CronExpr:     task.CronExpr,
		NextRunAt:    task.NextRunAt,
		CreatedAt:    task.CreatedAt,
		UpdatedAt:    task.UpdatedAt,
	}
}

// toTaskRunDTO converts a stored run into its wire form.
func toTaskRunDTO(run *repository.TaskRun) TaskRunDTO {
	return TaskRunDTO{
		ID:          run.ID,
		TaskID:      run.TaskID,
		Status:      run.Status,
		Trigger:     run.Trigger,
		ScheduledAt: run.ScheduledAt,
		StartedAt:   run.StartedAt,
		FinishedAt:  run.FinishedAt,
		Error:       run.Error,
		SessionID:   run.SessionID,
	}
}

// ListTasks returns every task of the caller.
//
//	@Summary		List tasks
//	@Description	Returns all of the caller's tasks ordered by name. Not paginated: a user has few of them.
//	@Tags			tasks
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		TaskDTO
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/tasks [get]
func ListTasks(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	tasks, err := service.ListTasks(c.Request.Context(), actor.ID)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]TaskDTO, 0, len(tasks))
	for i := range tasks {
		items = append(items, toTaskDTO(&tasks[i]))
	}
	c.JSON(http.StatusOK, items)
}

// CreateTask creates a task.
//
//	@Summary		Create a task
//	@Description	The task belongs to the caller. schedule_kind selects the one schedule field the body may set: run_at for at, every_seconds (at least 60) for every, or cron_expr for cron, which is evaluated in timezone. agent_name and provider_name must each resolve to exactly one agent or provider the caller may use; a provider is addressed by its name, or by its model_name when it carries none. Answers 201 with a Location header pointing at the new task.
//	@Tags			tasks
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateTaskRequest	true	"Task to create"
//	@Success		201		{object}	TaskDTO
//	@Header			201		{string}	Location	"URL of the created task"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/tasks [post]
func CreateTask(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request CreateTaskRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	in := service.CreateTaskInput{
		Name:         request.Name,
		Prompt:       request.Prompt,
		AgentName:    request.AgentName,
		ProviderName: request.ProviderName,
		Enabled:      true,
		Schedule: service.TaskScheduleInput{
			Kind:         request.ScheduleKind,
			RunAt:        request.RunAt,
			EverySeconds: request.EverySeconds,
			CronExpr:     request.CronExpr,
		},
	}
	if request.Timezone != nil {
		in.Timezone = *request.Timezone
	}
	if request.Enabled != nil {
		in.Enabled = *request.Enabled
	}

	task, err := service.CreateTask(c.Request.Context(), actor.ID, in)
	if err != nil {
		abortError(c, err)
		return
	}

	c.Header("Location", "/api/tasks/"+task.ID.String())
	c.JSON(http.StatusCreated, toTaskDTO(task))
}

// GetTask returns one task.
//
//	@Summary		Get a task
//	@Description	Returns one of the caller's tasks. Another user's task is reported as 404.
//	@Tags			tasks
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Task ID"	format(uuid)
//	@Success		200		{object}	TaskDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/tasks/{uuid} [get]
func GetTask(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	task, err := service.GetTask(c.Request.Context(), actor.ID, id)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTaskDTO(task))
}

// UpdateTask updates one task.
//
//	@Summary		Update a task
//	@Description	Every field is optional and a field that is absent is left untouched. A schedule_kind replaces the whole schedule, so send the one field that kind uses. next_run_at is recomputed when the schedule or timezone changes and when a disabled task is enabled again; a disabled task carries no next_run_at. Another user's task is reported as 404.
//	@Tags			tasks
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string				true	"Task ID"	format(uuid)
//	@Param			request	body		UpdateTaskRequest	true	"Fields to change"
//	@Success		200		{object}	TaskDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/tasks/{uuid} [put]
func UpdateTask(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var request UpdateTaskRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	in := service.UpdateTaskInput{
		Name:         request.Name,
		Prompt:       request.Prompt,
		AgentName:    request.AgentName,
		ProviderName: request.ProviderName,
		Timezone:     request.Timezone,
		Enabled:      request.Enabled,
	}
	if request.ScheduleKind != nil {
		in.Schedule = &service.TaskScheduleInput{
			Kind:         *request.ScheduleKind,
			RunAt:        request.RunAt,
			EverySeconds: request.EverySeconds,
			CronExpr:     request.CronExpr,
		}
	}

	task, err := service.UpdateTask(c.Request.Context(), actor.ID, id, in)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTaskDTO(task))
}

// DeleteTask deletes one task.
//
//	@Summary		Delete a task
//	@Description	Hard delete of one of the caller's tasks, its runs included. Another user's task is reported as 404.
//	@Tags			tasks
//	@Security		BearerAuth
//	@Param			uuid	path	string	true	"Task ID"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		404	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/tasks/{uuid} [delete]
func DeleteTask(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	if err := service.DeleteTask(c.Request.Context(), actor.ID, id); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListTaskRuns returns one page of a task's runs.
//
//	@Summary		List task runs
//	@Description	Returns one page of the runs recorded for one of the caller's tasks, newest scheduled instant first. Paginated because runs grow without bound. Another user's task is reported as 404.
//	@Tags			tasks
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Task ID"	format(uuid)
//	@Param			limit	query		int		false	"Page size (default 20, max 100)"
//	@Param			offset	query		int		false	"Rows to skip"
//	@Success		200		{object}	utils.Page[TaskRunDTO]
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/tasks/{uuid}/runs [get]
func ListTaskRuns(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	page := utils.ParsePagination(c)
	runs, total, err := service.ListTaskRuns(c.Request.Context(), actor.ID, id, page)
	if err != nil {
		abortError(c, err)
		return
	}

	items := make([]TaskRunDTO, 0, len(runs))
	for i := range runs {
		items = append(items, toTaskRunDTO(&runs[i]))
	}
	c.JSON(http.StatusOK, utils.NewPage(items, total, page))
}

// TasksRouter registers the task endpoints.
func TasksRouter(r *gin.Engine) {
	tasks := r.Group("/api/tasks", authOrAPIKey())
	tasks.GET("", ListTasks)
	tasks.POST("", CreateTask)
	tasks.GET("/:uuid", GetTask)
	tasks.PUT("/:uuid", UpdateTask)
	tasks.DELETE("/:uuid", DeleteTask)
	tasks.GET("/:uuid/runs", ListTaskRuns)
}
