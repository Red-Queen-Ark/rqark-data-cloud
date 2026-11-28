package api

import (
	"errors"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/middlewares"
	"rqark-data-cloud/internal/repository"
	"rqark-data-cloud/internal/utils"
)

// UsageQuery is the body of POST /api/usage. Every field is optional and the
// ones that are set are ANDed together; an empty body returns every row the
// caller may read.
type UsageQuery struct {
	// UserID restricts the listing to one user's rows. It is accepted only
	// from a superuser: a plain caller is always scoped to itself, and a body
	// that names another user is rejected rather than silently narrowed.
	UserID *uuid.UUID `json:"user_id" example:"4c2a3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`

	// From and To bound created_at, half-open: from is included, to excluded.
	From *time.Time `json:"from" example:"2026-11-01T00:00:00Z"`
	To   *time.Time `json:"to" example:"2026-12-01T00:00:00Z"`

	// Source, AgentID, ProviderID and IsCompaction select the rows the proxy
	// recorded, not the rows themselves: they narrow the listing by what the
	// caller reported at call time.
	Source       *string    `json:"source" example:"TUI"`
	AgentID      *uuid.UUID `json:"agent_id" example:"8e2f5d3b-2b2b-4b2b-9b2b-2b2b2b2b2b2b"`
	ProviderID   *uuid.UUID `json:"provider_id" example:"1b2c3d4e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	IsCompaction *bool      `json:"is_compaction" example:"false"`

	// APIKeyName selects rows by the name of the rqark API key they were made
	// with. It is a name rather than an id because that is what the caller
	// knows; two keys may share a name, in which case both match.
	APIKeyName *string `json:"api_key_name" example:"laptop"`

	// Model selects rows by the model the proxy reported. It is an exact match:
	// the model string is stored as it arrived, so a caller filters by the same
	// label the listing returns.
	Model *string `json:"model" example:"deepseek-flash"`
}

// UsageLogDTO is one usage row as the listing returns it. It is the stored row
// with the key's name resolved. The counters are the provider's own report;
// cached and reasoning tokens are subsets of the prompt and completion counts
// and are never added to the total.
//
// UserID is returned so a superuser listing every user can tell the rows apart.
// For a plain caller it is always its own id.
type UsageLogDTO struct {
	ID         uuid.UUID `json:"id" example:"0192f3a4-1a1a-7a1a-8a1a-1a1a1a1a1a1a"`
	CreatedAt  time.Time `json:"created_at" example:"2026-11-01T09:30:00Z"`
	UserID     uuid.UUID `json:"user_id" example:"4c2a3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	APIKeyName *string   `json:"api_key_name" example:"laptop"`

	Source       string     `json:"source" example:"TUI"`
	Model        string     `json:"model" example:"deepseek-flash"`
	SessionID    *uuid.UUID `json:"session_id"`
	AgentID      *uuid.UUID `json:"agent_id"`
	ProviderID   uuid.UUID  `json:"provider_id" example:"1b2c3d4e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	IsCompaction bool       `json:"is_compaction" example:"false"`

	PromptTokens     int32 `json:"prompt_tokens" example:"1200"`
	CachedTokens     int32 `json:"cached_tokens" example:"800"`
	CompletionTokens int32 `json:"completion_tokens" example:"300"`
	ReasoningTokens  int32 `json:"reasoning_tokens" example:"0"`
	TotalTokens      int32 `json:"total_tokens" example:"1500"`
}

// toUsageLogDTO converts a stored row into its wire form.
func toUsageLogDTO(entry repository.UsageLogEntry) UsageLogDTO {
	return UsageLogDTO{
		ID:               entry.ID,
		CreatedAt:        entry.CreatedAt,
		UserID:           entry.UserID,
		APIKeyName:       entry.APIKeyName,
		Source:           entry.Source,
		Model:            entry.Model,
		SessionID:        entry.SessionID,
		AgentID:          entry.AgentID,
		ProviderID:       entry.ProviderID,
		IsCompaction:     entry.IsCompaction,
		PromptTokens:     entry.PromptTokens,
		CachedTokens:     entry.CachedTokens,
		CompletionTokens: entry.CompletionTokens,
		ReasoningTokens:  entry.ReasoningTokens,
		TotalTokens:      entry.TotalTokens,
	}
}

// toUsageLogFilter maps the request onto the query the repository runs. scope is
// the user the listing is restricted to, or nil for a superuser listing every
// user; the caller has already decided which one applies.
func toUsageLogFilter(query UsageQuery, scope *uuid.UUID) repository.UsageLogFilter {
	return repository.UsageLogFilter{
		UserID:       scope,
		From:         query.From,
		To:           query.To,
		Source:       query.Source,
		AgentID:      query.AgentID,
		ProviderID:   query.ProviderID,
		IsCompaction: query.IsCompaction,
		APIKeyName:   query.APIKeyName,
		Model:        query.Model,
	}
}

// usageScope decides which rows the caller may read. A superuser may name any
// user or name none and see every user; a plain caller is always restricted to
// itself, and a body that carries user_id is rejected instead of ignored so that
// the caller is not left believing it was honoured.
func usageScope(c *gin.Context, actor *repository.User, query UsageQuery) (*uuid.UUID, bool) {
	if actor.IsSuperuser {
		return query.UserID, true
	}
	if query.UserID != nil {
		abortBadRequest(c, "user_id is only accepted from a superuser.")
		return nil, false
	}
	return &actor.ID, true
}

// ListUsage returns the caller's usage rows.
//
//	@Summary		List usage records
//	@Description	Returns every usage row that matches the body, newest first. It is not paginated and not aggregated: the client receives the rows and charts them itself. An empty body returns every row the caller may read. A plain caller reads its own rows only and may not send user_id; a superuser reads every user's rows, and may narrow them with user_id. api_key_name selects rows by the name of the key they were made with, and each row reports that name; a row made with an access token has none. model selects rows by the exact model string the proxy reported.
//	@Tags			usage
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			query	body		UsageQuery	false	"Filters; every field is optional"
//	@Success		200		{array}		UsageLogDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/usage [post]
func ListUsage(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var query UsageQuery
	// An absent body and an empty object mean the same thing here: no filter.
	if err := c.ShouldBindJSON(&query); err != nil && !errors.Is(err, io.EOF) {
		abortBadRequest(c, "The body is not valid JSON.")
		return
	}

	if query.From != nil && query.To != nil && !query.From.Before(*query.To) {
		abortBadRequest(c, "from must be earlier than to.")
		return
	}

	scope, ok := usageScope(c, actor, query)
	if !ok {
		return
	}

	entries, err := repository.ListUsageLogs(c.Request.Context(), config.DB, toUsageLogFilter(query, scope))
	if err != nil {
		abortInternal(c, err)
		return
	}

	body := make([]UsageLogDTO, 0, len(entries))
	for _, entry := range entries {
		body = append(body, toUsageLogDTO(entry))
	}
	c.JSON(200, body)
}

// UsageRouter registers the usage listing. It accepts an access token or an API
// key. A plain caller reads its own rows; a superuser reads every user's rows
// through the same route and the same body, which is why there is no separate
// admin path.
func UsageRouter(r *gin.Engine) {
	usage := r.Group("/api/usage", authOrAPIKey())
	usage.POST("", ListUsage)
}
