package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
)

// SystemConfigDTO is one deployment-wide config entry in its wire form. It has
// no owner: every row applies to the whole deployment. value is handed back
// exactly as it was stored.
type SystemConfigDTO struct {
	ID        uuid.UUID       `json:"id" example:"3f9a1c7e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Key       string          `json:"key" example:"login_page_style"`
	Value     json.RawMessage `json:"value" swaggertype:"object"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// PutSystemConfigRequest is the body of PUT /api/system-config/{key}. value is
// any JSON value and is required; an absent key is rejected rather than stored
// as null.
type PutSystemConfigRequest struct {
	Value json.RawMessage `json:"value" swaggertype:"object"`
}

// toSystemConfigDTO converts a stored config into its wire form.
func toSystemConfigDTO(config *repository.SystemConfig) SystemConfigDTO {
	return SystemConfigDTO{
		ID:        config.ID,
		Key:       config.Key,
		Value:     config.Value,
		CreatedAt: config.CreatedAt,
		UpdatedAt: config.UpdatedAt,
	}
}

// ListSystemConfigs returns every deployment-wide config entry.
//
//	@Summary		List system config
//	@Description	Returns every deployment-wide config entry, ordered by key. Readable by any signed-in user because a client needs settings such as a theme; not paginated, since the client treats the set as one config file. Never put secrets here.
//	@Tags			system-config
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		SystemConfigDTO
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/system-config [get]
func ListSystemConfigs(c *gin.Context) {
	configs, err := service.ListSystemConfigs(c.Request.Context())
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]SystemConfigDTO, 0, len(configs))
	for i := range configs {
		items = append(items, toSystemConfigDTO(&configs[i]))
	}
	c.JSON(http.StatusOK, items)
}

// PutSystemConfig creates or overwrites one deployment-wide config entry.
//
//	@Summary		Set a system config entry
//	@Description	Upserts one deployment-wide config entry: the value of an existing key is overwritten, a key that does not exist is created. Superuser only, because one write applies to every user.
//	@Tags			system-config
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			key		path		string					true	"Config key"
//	@Param			request	body		PutSystemConfigRequest	true	"Config value"
//	@Success		200		{object}	SystemConfigDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/system-config/{key} [put]
func PutSystemConfig(c *gin.Context) {
	key := c.Param("key")

	var request PutSystemConfigRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	config, err := service.SetSystemConfig(c.Request.Context(), key, request.Value)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSystemConfigDTO(config))
}

// DeleteSystemConfig deletes one deployment-wide config entry.
//
//	@Summary		Delete a system config entry
//	@Description	Hard delete of one deployment-wide config entry.
//	@Tags			system-config
//	@Security		BearerAuth
//	@Param			key	path	string	true	"Config key"
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		403	{object}	utils.ErrorResponse
//	@Failure		404	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/system-config/{key} [delete]
func DeleteSystemConfig(c *gin.Context) {
	key := c.Param("key")

	if err := service.DeleteSystemConfig(c.Request.Context(), key); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SystemConfigsRouter registers the system config routes. Reading accepts an
// access token or an API key and is open to any signed-in user, since a
// deployment-wide setting is what a client asks for after it logs in; writing
// is a superuser action that takes an access token only, because one row is
// shared by everybody. Nothing secret belongs in this table — a credential goes
// in the environment.
func SystemConfigsRouter(r *gin.Engine) {
	readers := r.Group("/api/system-config", authOrAPIKey())
	readers.GET("", ListSystemConfigs)

	admin := r.Group("/api/system-config", superuserRequired()...)
	admin.PUT("/:key", PutSystemConfig)
	admin.DELETE("/:key", DeleteSystemConfig)
}
