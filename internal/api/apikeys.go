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

// APIKeyDTO is the key representation returned by every api-keys endpoint. The
// key itself is never part of it, only the prefix it was created with, which is
// enough to tell one key from another in a listing.
type APIKeyDTO struct {
	ID             uuid.UUID  `json:"id" example:"3f9a1c7e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Name           string     `json:"name" example:"laptop"`
	KeyPrefix      string     `json:"key_prefix" example:"rqark-9Zk2Xf1p"`
	IsActive       bool       `json:"is_active" example:"true"`
	ExpiresAt      *time.Time `json:"expires_at"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	RevokedAt      *time.Time `json:"revoked_at"`
	RevokedMessage *string    `json:"revoked_message" example:"user_deleted"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`

	// Key is the plaintext key. Only the create response carries it: that is
	// the single moment the key exists outside its owner's hands.
	Key string `json:"key,omitempty" example:"rqark-9Zk2Xf1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1p"`
}

// CreateAPIKeyRequest is the body of POST /api/api-keys.
type CreateAPIKeyRequest struct {
	Name      string     `json:"name" example:"laptop"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// UpdateAPIKeyRequest is the body of PATCH /api/api-keys/{uuid}. Every field is
// optional: a field that is absent leaves the stored value alone, while an
// explicit null for expires_at makes the key never expire again.
type UpdateAPIKeyRequest struct {
	Name      *string    `json:"name"`
	IsActive  *bool      `json:"is_active"`
	ExpiresAt *time.Time `json:"expires_at"`

	// present holds the keys the body carried. encoding/json decodes an absent
	// key and an explicit null to the same nil pointer, and the two mean
	// different things here, so the keys are collected on the side.
	present map[string]bool
}

// UnmarshalJSON decodes the body and records which keys it carried.
func (r *UpdateAPIKeyRequest) UnmarshalJSON(data []byte) error {
	// plain has no methods, so decoding into it cannot recurse into this one.
	type plain UpdateAPIKeyRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}

	*r = UpdateAPIKeyRequest(decoded)
	r.present = make(map[string]bool, len(keys))
	for key := range keys {
		r.present[key] = true
	}
	return nil
}

// provided reports whether the body carried key.
func (r UpdateAPIKeyRequest) provided(key string) bool { return r.present[key] }

// toAPIKeyDTO converts a stored key into its wire form, nullable columns and
// all: a NULL column is reported as null, never as a zero time.
func toAPIKeyDTO(key *repository.APIKey) APIKeyDTO {
	return APIKeyDTO{
		ID:             key.ID,
		Name:           key.Name,
		KeyPrefix:      key.KeyPrefix,
		IsActive:       key.IsActive,
		ExpiresAt:      key.ExpiresAt,
		LastUsedAt:     key.LastUsedAt,
		RevokedAt:      key.RevokedAt,
		RevokedMessage: key.RevokedMessage,
		CreatedAt:      key.CreatedAt,
		UpdatedAt:      key.UpdatedAt,
	}
}

// ListAPIKeys returns a page of the caller's API keys.
//
//	@Summary		List API keys
//	@Description	Returns the caller's keys, newest first. The keys themselves are not part of the response: only the prefix each was created with.
//	@Tags			api-keys
//	@Security		BearerAuth
//	@Produce		json
//	@Param			limit	query		int	false	"Page size (default 20, max 100)"
//	@Param			offset	query		int	false	"Number of keys to skip"
//	@Success		200		{object}	utils.Page[APIKeyDTO]
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/api-keys [get]
func ListAPIKeys(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	page := utils.ParsePagination(c)

	keys, total, err := service.ListAPIKeys(c.Request.Context(), actor.ID, page.Limit, page.Offset)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]APIKeyDTO, 0, len(keys))
	for i := range keys {
		items = append(items, toAPIKeyDTO(&keys[i]))
	}
	c.JSON(http.StatusOK, utils.NewPage(items, total, page))
}

// CreateAPIKey creates an API key.
//
//	@Summary		Create an API key
//	@Description	The key belongs to the caller. The response is the only place the key itself is ever returned; only its hash is stored, so a lost key cannot be recovered, only replaced.
//	@Tags			api-keys
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateAPIKeyRequest	true	"Key to create"
//	@Success		201		{object}	APIKeyDTO
//	@Header			201		{string}	Location	"URL of the created key"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/api-keys [post]
func CreateAPIKey(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request CreateAPIKeyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	key, plaintext, err := service.CreateAPIKey(c.Request.Context(), actor.ID, service.CreateAPIKeyInput{
		Name:      request.Name,
		ExpiresAt: request.ExpiresAt,
	})
	if err != nil {
		abortError(c, err)
		return
	}

	dto := toAPIKeyDTO(key)
	dto.Key = plaintext
	c.Header("Location", "/api/api-keys/"+key.ID.String())
	c.JSON(http.StatusCreated, dto)
}

// UpdateAPIKey updates one API key.
//
//	@Summary		Update an API key
//	@Description	A field that is absent keeps its stored value. Setting expires_at is also how a lapsed key is renewed: the key is then active and usable again, and null makes it never expire. Switching a lapsed key on without moving its expiry is refused. A key that was deleted is hidden and cannot be updated. Another user's key is reported as 404.
//	@Tags			api-keys
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string					true	"API key ID"	format(uuid)
//	@Param			request	body		UpdateAPIKeyRequest	true	"Fields to change"
//	@Success		200		{object}	APIKeyDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/api-keys/{uuid} [patch]
func UpdateAPIKey(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var request UpdateAPIKeyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	key, err := service.UpdateAPIKey(c.Request.Context(), actor.ID, id, service.UpdateAPIKeyInput{
		Name:      request.Name,
		IsActive:  request.IsActive,
		ExpiresAt: optional(request.ExpiresAt, request.provided("expires_at")),
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAPIKeyDTO(key))
}

// DeleteAPIKey deletes one API key.
//
//	@Summary		Delete an API key
//	@Description	Soft delete: the key stops authenticating at once, is left out of the listing, and the row stays behind for audit with the time and the reason. Another user's key is reported as 404, and deleting a deleted key is a no-op.
//	@Tags			api-keys
//	@Security		BearerAuth
//	@Param			uuid	path	string	true	"API key ID"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		404	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/api-keys/{uuid} [delete]
func DeleteAPIKey(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	if err := service.DeleteAPIKey(c.Request.Context(), actor.ID, id); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// APIKeysRouter registers the API key routes. They all require an access token
// and none of them accepts a key: an API key must not be able to create or
// revoke keys, so a leaked key can neither mint a replacement for itself nor
// destroy the other keys of its owner.
func APIKeysRouter(r *gin.Engine) {
	keys := r.Group("/api/api-keys", authRequired())
	keys.GET("", ListAPIKeys)
	keys.POST("", CreateAPIKey)
	keys.PATCH("/:uuid", UpdateAPIKey)
	keys.DELETE("/:uuid", DeleteAPIKey)
}
