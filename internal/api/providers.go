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

// ProviderDTO is the provider representation returned by every provider
// endpoint. The owner is left out: a provider is only ever visible to the user
// it belongs to.
type ProviderDTO struct {
	ID               uuid.UUID `json:"id" example:"7d1e4c2a-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Name             string    `json:"name" example:"deepseek-v4-pro"`
	ModelName        string    `json:"model_name" example:"deepseek-v4-pro"`
	APIKey           string    `json:"api_key" example:"sk-rqark-0000"`
	APIBase          string    `json:"api_base" example:"https://api.deepseek.com/v1"`
	Priority         int       `json:"priority" example:"1"`
	ReasoningEffort  *string   `json:"reasoning_effort" example:"high"`
	Thinking         *string   `json:"thinking" example:"enabled"`
	Temperature      *float64  `json:"temperature" example:"0.7"`
	TopP             *float64  `json:"top_p" example:"0.9"`
	Seed             *int      `json:"seed" example:"42"`
	FrequencyPenalty *float64  `json:"frequency_penalty" example:"0.5"`
	PresencePenalty  *float64  `json:"presence_penalty" example:"0.5"`
	Combine          bool      `json:"combine" example:"false"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ProviderSummaryDTO is the lightweight provider representation returned by
// GET /api/providers/models: enough to label a provider without exposing its
// credentials.
type ProviderSummaryDTO struct {
	Name      string `json:"name" example:"primary"`
	ModelName string `json:"model_name" example:"kimi-k3"`
}

// CreateProviderRequest is the body of POST /api/providers.
type CreateProviderRequest struct {
	Name             string   `json:"name" example:"deepseek-v4-pro"`
	ModelName        string   `json:"model_name" example:"deepseek-v4-pro"`
	APIKey           string   `json:"api_key" example:"sk-rqark-0000"`
	APIBase          string   `json:"api_base" example:"https://api.deepseek.com/v1"`
	Priority         *int     `json:"priority" example:"1"`
	ReasoningEffort  *string  `json:"reasoning_effort" example:"high"`
	Thinking         *string  `json:"thinking" example:"enabled"`
	Temperature      *float64 `json:"temperature" example:"0.7"`
	TopP             *float64 `json:"top_p" example:"0.9"`
	Seed             *int     `json:"seed" example:"42"`
	FrequencyPenalty *float64 `json:"frequency_penalty" example:"0.5"`
	PresencePenalty  *float64 `json:"presence_penalty" example:"0.5"`
	Combine          bool     `json:"combine" example:"false"`
}

// UpdateProviderRequest is the body of PUT /api/providers/{uuid}. Every field is
// optional: a field that is absent leaves the stored value alone, while an
// explicit null clears one of the nullable columns.
type UpdateProviderRequest struct {
	Name             *string  `json:"name"`
	ModelName        *string  `json:"model_name"`
	APIKey           *string  `json:"api_key"`
	APIBase          *string  `json:"api_base"`
	Priority         *int     `json:"priority"`
	ReasoningEffort  *string  `json:"reasoning_effort"`
	Thinking         *string  `json:"thinking"`
	Temperature      *float64 `json:"temperature"`
	TopP             *float64 `json:"top_p"`
	Seed             *int     `json:"seed"`
	FrequencyPenalty *float64 `json:"frequency_penalty"`
	PresencePenalty  *float64 `json:"presence_penalty"`
	Combine          *bool    `json:"combine"`

	// present holds the keys the body carried. encoding/json decodes an absent
	// key and an explicit null to the same nil pointer, and the two mean
	// different things here, so the keys are collected on the side.
	present map[string]bool
}

// UnmarshalJSON decodes the body and records which keys it carried.
func (r *UpdateProviderRequest) UnmarshalJSON(data []byte) error {
	// plain has no methods, so decoding into it cannot recurse into this one.
	type plain UpdateProviderRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}

	*r = UpdateProviderRequest(decoded)
	r.present = make(map[string]bool, len(keys))
	for key := range keys {
		r.present[key] = true
	}
	return nil
}

// provided reports whether the body carried key.
func (r UpdateProviderRequest) provided(key string) bool { return r.present[key] }

// optional pairs a decoded nullable field with the fact that its key was
// present in the body.
func optional[T any](value *T, provided bool) utils.Optional[T] {
	return utils.Optional[T]{Set: provided, Value: value}
}

// toProviderDTO converts a stored provider into its wire form, nullable columns
// and all: a NULL column is reported as null, never as 0 or false.
func toProviderDTO(provider *repository.Provider) ProviderDTO {
	return ProviderDTO{
		ID:               provider.ID,
		Name:             provider.Name,
		ModelName:        provider.ModelName,
		APIKey:           provider.APIKey,
		APIBase:          provider.APIBase,
		Priority:         provider.Priority,
		ReasoningEffort:  provider.ReasoningEffort,
		Thinking:         provider.Thinking,
		Temperature:      provider.Temperature,
		TopP:             provider.TopP,
		Seed:             provider.Seed,
		FrequencyPenalty: provider.FrequencyPenalty,
		PresencePenalty:  provider.PresencePenalty,
		Combine:          provider.Combine,
		CreatedAt:        provider.CreatedAt,
		UpdatedAt:        provider.UpdatedAt,
	}
}

// ListProviders returns every provider of the caller.
//
//	@Summary		List providers
//	@Description	Returns all of the caller's providers, lowest priority first. Not paginated: a user has few of them.
//	@Tags			providers
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		ProviderDTO
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/providers [get]
func ListProviders(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	providers, err := service.ListProviders(c.Request.Context(), actor.ID)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]ProviderDTO, 0, len(providers))
	for i := range providers {
		items = append(items, toProviderDTO(&providers[i]))
	}
	c.JSON(http.StatusOK, items)
}

// ListProviderModels returns the name and model_name of every provider of the caller.
//
//	@Summary		List provider models
//	@Description	Returns name and model_name for all of the caller's providers, lowest priority first. The remaining provider fields, credentials included, are left out. Not paginated: a user has few of them.
//	@Tags			providers
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		ProviderSummaryDTO
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/providers/models [get]
func ListProviderModels(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	summaries, err := service.ListProviderSummaries(c.Request.Context(), actor.ID)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]ProviderSummaryDTO, 0, len(summaries))
	for i := range summaries {
		items = append(items, ProviderSummaryDTO{
			Name:      summaries[i].Name,
			ModelName: summaries[i].ModelName,
		})
	}
	c.JSON(http.StatusOK, items)
}

// CreateProvider creates a provider.
//
//	@Summary		Create a provider
//	@Description	The provider belongs to the caller. Answers 201 with a Location header pointing at the new provider.
//	@Tags			providers
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateProviderRequest	true	"Provider to create"
//	@Success		201		{object}	ProviderDTO
//	@Header			201		{string}	Location	"URL of the created provider"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/providers [post]
func CreateProvider(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request CreateProviderRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	provider, err := service.CreateProvider(c.Request.Context(), actor.ID, service.CreateProviderInput{
		Name:             request.Name,
		ModelName:        request.ModelName,
		APIKey:           request.APIKey,
		APIBase:          request.APIBase,
		Priority:         request.Priority,
		ReasoningEffort:  request.ReasoningEffort,
		Thinking:         request.Thinking,
		Temperature:      request.Temperature,
		TopP:             request.TopP,
		Seed:             request.Seed,
		FrequencyPenalty: request.FrequencyPenalty,
		PresencePenalty:  request.PresencePenalty,
		Combine:          request.Combine,
	})
	if err != nil {
		abortError(c, err)
		return
	}

	c.Header("Location", "/api/providers/"+provider.ID.String())
	c.JSON(http.StatusCreated, toProviderDTO(provider))
}

// GetProvider returns one provider.
//
//	@Summary		Get a provider
//	@Description	Returns one of the caller's providers, api_key included. Another user's provider is reported as 404.
//	@Tags			providers
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Provider ID"	format(uuid)
//	@Success		200		{object}	ProviderDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/providers/{uuid} [get]
func GetProvider(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	provider, err := service.GetProvider(c.Request.Context(), actor.ID, id)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toProviderDTO(provider))
}

// UpdateProvider updates one provider.
//
//	@Summary		Update a provider
//	@Description	A field that is absent keeps its stored value; sending null clears a nullable field. Another user's provider is reported as 404.
//	@Tags			providers
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string					true	"Provider ID"	format(uuid)
//	@Param			request	body		UpdateProviderRequest	true	"Fields to change"
//	@Success		200		{object}	ProviderDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/providers/{uuid} [put]
func UpdateProvider(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var request UpdateProviderRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	provider, err := service.UpdateProvider(c.Request.Context(), actor.ID, id, service.UpdateProviderInput{
		Name:             request.Name,
		ModelName:        request.ModelName,
		APIKey:           request.APIKey,
		APIBase:          request.APIBase,
		Priority:         request.Priority,
		ReasoningEffort:  optional(request.ReasoningEffort, request.provided("reasoning_effort")),
		Thinking:         optional(request.Thinking, request.provided("thinking")),
		Temperature:      optional(request.Temperature, request.provided("temperature")),
		TopP:             optional(request.TopP, request.provided("top_p")),
		Seed:             optional(request.Seed, request.provided("seed")),
		FrequencyPenalty: optional(request.FrequencyPenalty, request.provided("frequency_penalty")),
		PresencePenalty:  optional(request.PresencePenalty, request.provided("presence_penalty")),
		Combine:          request.Combine,
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toProviderDTO(provider))
}

// DeleteProvider deletes one provider.
//
//	@Summary		Delete a provider
//	@Description	Hard delete of one of the caller's providers. Another user's provider is reported as 404.
//	@Tags			providers
//	@Security		BearerAuth
//	@Param			uuid	path	string	true	"Provider ID"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		404	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/providers/{uuid} [delete]
func DeleteProvider(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	if err := service.DeleteProvider(c.Request.Context(), actor.ID, id); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ProvidersRouter registers the provider routes. Providers hold credentials, so
// every route is scoped to the caller and none of them has a superuser path. An
// access token or an API key is accepted: a provider is what a client needs
// before it can talk to a model.
func ProvidersRouter(r *gin.Engine) {
	providers := r.Group("/api/providers", authOrAPIKey())
	providers.GET("", ListProviders)
	providers.POST("", CreateProvider)
	providers.GET("/models", ListProviderModels)
	providers.GET("/:uuid", GetProvider)
	providers.PUT("/:uuid", UpdateProvider)
	providers.DELETE("/:uuid", DeleteProvider)
}
