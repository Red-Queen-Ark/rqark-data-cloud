package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rqark-data-cloud/internal/middlewares"
	"rqark-data-cloud/internal/repository"
	"rqark-data-cloud/internal/service"
	"rqark-data-cloud/internal/utils"
)

// UserConfigDTO is one config entry in its wire form. The owner is left out: a
// config is only ever visible to the user it belongs to. value is handed back
// exactly as it was stored.
type UserConfigDTO struct {
	ID        uuid.UUID       `json:"id" example:"3f9a1c7e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Key       string          `json:"key" example:"theme"`
	Value     json.RawMessage `json:"value" swaggertype:"object"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// PutUserConfigRequest is the body of PUT /api/user-config/{key}. value is any
// JSON value and is required; an absent key is rejected rather than stored as
// null.
type PutUserConfigRequest struct {
	Value json.RawMessage `json:"value" swaggertype:"object"`
}

// toUserConfigDTO converts a stored config into its wire form.
func toUserConfigDTO(config *repository.UserConfig) UserConfigDTO {
	return UserConfigDTO{
		ID:        config.ID,
		Key:       config.Key,
		Value:     config.Value,
		CreatedAt: config.CreatedAt,
		UpdatedAt: config.UpdatedAt,
	}
}

// ListUserConfigs returns every config entry of the caller.
//
//	@Summary		List user config
//	@Description	Returns all of the caller's config entries, ordered by key. Not paginated: the app treats them as one local config file and syncs the whole set at once. Entries the account has never set are stored with their default before the list is returned, so the memory switches come back explicitly off instead of missing.
//	@Tags			user-config
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		UserConfigDTO
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/user-config [get]
func ListUserConfigs(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	configs, err := service.ListUserConfigs(c.Request.Context(), actor.ID)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]UserConfigDTO, 0, len(configs))
	for i := range configs {
		items = append(items, toUserConfigDTO(&configs[i]))
	}
	c.JSON(http.StatusOK, items)
}

// PutUserConfig creates or overwrites one config entry.
//
//	@Summary		Set a user config entry
//	@Description	Upserts one config entry of the caller: the value of an existing key is overwritten, a key that does not exist is created.
//	@Tags			user-config
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			key		path		string					true	"Config key"
//	@Param			request	body		PutUserConfigRequest	true	"Config value"
//	@Success		200		{object}	UserConfigDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/user-config/{key} [put]
func PutUserConfig(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	key := c.Param("key")

	var request PutUserConfigRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	config, err := service.SetUserConfig(c.Request.Context(), actor.ID, key, request.Value)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserConfigDTO(config))
}

// DeleteUserConfig deletes one config entry.
//
//	@Summary		Delete a user config entry
//	@Description	Hard delete of one of the caller's config entries.
//	@Tags			user-config
//	@Security		BearerAuth
//	@Param			key	path	string	true	"Config key"
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		404	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/user-config/{key} [delete]
func DeleteUserConfig(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	key := c.Param("key")

	if err := service.DeleteUserConfig(c.Request.Context(), actor.ID, key); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// UserConfigsRouter registers the user config routes. There is no superuser path
// here: every entry belongs to the caller, and every query is scoped to the user
// behind the access token or the API key.
func UserConfigsRouter(r *gin.Engine) {
	configs := r.Group("/api/user-config", authOrAPIKey())
	configs.GET("", ListUserConfigs)
	configs.PUT("/:key", PutUserConfig)
	configs.DELETE("/:key", DeleteUserConfig)
}
