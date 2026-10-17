// Package internalapi holds the endpoints the agent cloud calls to claim and
// finalize task runs. It is a package of its own because it must not appear in
// the public specification: these routes are guarded by a shared secret rather
// than by a user, and publishing their shapes would invite a caller to try them.
// The swag invocation in cmd/server excludes this directory.
package internalapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/middlewares"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// maxClaimLimit caps one claim. A worker that asks for more than it has slots
// would otherwise be able to claim a whole user's backlog in one call.
const maxClaimLimit = 32

// ClaimRequest is the body of POST /api/internal/tasks/claim. limit is the
// worker's free slot count, so a busy worker claims nothing at all.
type ClaimRequest struct {
	Limit int `json:"limit" example:"1"`
}

// TaskDefinition is the half of a task a run needs: what to run and what to run
// it against.
type TaskDefinition struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Prompt       string    `json:"prompt"`
	AgentName    string    `json:"agent_name"`
	ProviderName string    `json:"provider_name"`
}

// AgentDefinition is a resolved agent, shaped like the agent DTO the user API
// serves so the engine builds it with the same code path.
type AgentDefinition struct {
	ID          uuid.UUID                `json:"id"`
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Prompts     []repository.AgentPrompt `json:"prompts"`
	Tools       []repository.AgentTool   `json:"tools"`
}

// ProviderDefinition is a resolved provider, including the credential the run
// needs to reach the model. It exists only in this response: the endpoint is
// behind the internal token, and the caller is the service that runs the prompt.
type ProviderDefinition struct {
	ID               uuid.UUID `json:"id"`
	Name             string    `json:"name"`
	ModelName        string    `json:"model_name"`
	APIKey           string    `json:"api_key"`
	APIBase          string    `json:"api_base"`
	Priority         int       `json:"priority"`
	ReasoningEffort  *string   `json:"reasoning_effort"`
	Thinking         *string   `json:"thinking"`
	Temperature      *float64  `json:"temperature"`
	TopP             *float64  `json:"top_p"`
	Seed             *int      `json:"seed"`
	FrequencyPenalty *float64  `json:"frequency_penalty"`
	PresencePenalty  *float64  `json:"presence_penalty"`
	Combine          bool      `json:"combine"`
}

// ClaimedRun is one run to dispatch. actor_token is a run-scoped token: it
// reaches the one session in this entry and nothing else.
type ClaimedRun struct {
	RunID       uuid.UUID          `json:"run_id"`
	UserID      uuid.UUID          `json:"user_id"`
	Task        TaskDefinition     `json:"task"`
	AgentDef    AgentDefinition    `json:"agent_def"`
	ProviderDef ProviderDefinition `json:"provider_def"`
	SessionID   uuid.UUID          `json:"session_id"`
	ActorToken  string             `json:"actor_token"`
}

// ClaimResponse is the answer to a claim. It is an envelope rather than a bare
// array so a later field can be added without breaking the caller.
type ClaimResponse struct {
	Runs []ClaimedRun `json:"runs"`
}

// FinalizeRequest is the body of the finalize call. finished_at is optional; the
// cloud stamps the instant of the call when it is absent.
type FinalizeRequest struct {
	Status     string     `json:"status" example:"ok"`
	Error      string     `json:"error" example:""`
	SessionID  *uuid.UUID `json:"session_id"`
	FinishedAt *time.Time `json:"finished_at"`
}

// TaskRunDTO is the run as the caller sees it back after finalizing.
type TaskRunDTO struct {
	ID          uuid.UUID  `json:"id"`
	TaskID      uuid.UUID  `json:"task_id"`
	Status      string     `json:"status"`
	Trigger     string     `json:"trigger"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	Error       string     `json:"error"`
	SessionID   *uuid.UUID `json:"session_id"`
}

func toClaimedRun(run *service.ClaimedRun) ClaimedRun {
	prompts := run.Agent.Prompts
	if prompts == nil {
		prompts = []repository.AgentPrompt{}
	}
	tools := run.Agent.Tools
	if tools == nil {
		tools = []repository.AgentTool{}
	}

	return ClaimedRun{
		RunID:  run.RunID,
		UserID: run.UserID,
		Task: TaskDefinition{
			ID:           run.Task.ID,
			Name:         run.Task.Name,
			Prompt:       run.Task.Prompt,
			AgentName:    run.Task.AgentName,
			ProviderName: run.Task.ProviderName,
		},
		AgentDef: AgentDefinition{
			ID:          run.Agent.ID,
			Name:        run.Agent.Name,
			Description: run.Agent.Description,
			Prompts:     prompts,
			Tools:       tools,
		},
		ProviderDef: ProviderDefinition{
			ID:               run.Provider.ID,
			Name:             run.Provider.Name,
			ModelName:        run.Provider.ModelName,
			APIKey:           run.Provider.APIKey,
			APIBase:          run.Provider.APIBase,
			Priority:         run.Provider.Priority,
			ReasoningEffort:  run.Provider.ReasoningEffort,
			Thinking:         run.Provider.Thinking,
			Temperature:      run.Provider.Temperature,
			TopP:             run.Provider.TopP,
			Seed:             run.Provider.Seed,
			FrequencyPenalty: run.Provider.FrequencyPenalty,
			PresencePenalty:  run.Provider.PresencePenalty,
			Combine:          run.Provider.Combine,
		},
		SessionID:  run.SessionID,
		ActorToken: run.ActorToken,
	}
}

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

// abortError maps the errors this package can raise onto the error envelope.
func abortError(c *gin.Context, err error) {
	var validation *service.ValidationError
	switch {
	case errors.Is(err, service.ErrTaskRunAlreadyFinal):
		utils.Abort(c, utils.Conflict, "This run is no longer running.")
	case errors.As(err, &validation):
		utils.Abort(c, utils.BadRequest, validation.Message)
	default:
		utils.Abort(c, utils.Internal, "Internal server error.")
	}
}

// Claim marks up to limit due tasks as running and answers the runs to execute.
// It is the only writer of next_run_at, and it does so in the same transaction as
// the run insert, so a task cannot become due twice for one slot.
func Claim(c *gin.Context) {
	var request ClaimRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		utils.Abort(c, utils.BadRequest, "Malformed request body.")
		return
	}
	if request.Limit < 0 || request.Limit > maxClaimLimit {
		utils.Abort(c, utils.BadRequest, "limit must be between 0 and 32.")
		return
	}

	runs, err := service.ClaimTaskRuns(c.Request.Context(), request.Limit)
	if err != nil {
		abortError(c, err)
		return
	}

	items := make([]ClaimedRun, 0, len(runs))
	for i := range runs {
		items = append(items, toClaimedRun(&runs[i]))
	}
	c.JSON(http.StatusOK, ClaimResponse{Runs: items})
}

// Finalize records the outcome of one run. A run that is no longer running —
// because another call already finalized it, or because the sweep marked it lost
// — answers 409 rather than being overwritten.
func Finalize(c *gin.Context) {
	taskID, ok := parseUUIDParam(c, "task_uuid")
	if !ok {
		return
	}
	runID, ok := parseUUIDParam(c, "run_uuid")
	if !ok {
		return
	}

	var request FinalizeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		utils.Abort(c, utils.BadRequest, "Malformed request body.")
		return
	}

	run, err := service.FinalizeTaskRun(c.Request.Context(), taskID, runID, service.FinalizeTaskRunInput{
		Status:     request.Status,
		Error:      request.Error,
		SessionID:  request.SessionID,
		FinishedAt: request.FinishedAt,
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTaskRunDTO(run))
}

// parseUUIDParam reads a UUID path parameter, answering 400 when it is not one.
func parseUUIDParam(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		utils.Abort(c, utils.BadRequest, name+" must be a UUID.")
		return uuid.Nil, false
	}
	return id, true
}

// Router registers the internal endpoints behind the shared secret.
func Router(r *gin.Engine, internalToken string) {
	routes := r.Group("/api/internal", middlewares.InternalToken(internalToken))
	routes.POST("/tasks/claim", Claim)
	routes.PATCH("/tasks/:task_uuid/runs/:run_uuid", Finalize)
}
