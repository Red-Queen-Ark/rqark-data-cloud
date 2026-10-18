package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/middlewares"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

const (
	// sopDefaultLimit is the page size of GET /api/sops when limit is absent,
	// invalid or above the ceiling.
	sopDefaultLimit = 100
	// sopMaxLimit is the largest page a caller may ask for.
	sopMaxLimit = 100
)

// SopDTO is one sop in its wire form. The owner is left out: a row is only ever
// visible to the user it belongs to.
type SopDTO struct {
	Slug      string    `json:"slug" example:"trip-itinerary"`
	Title     string    `json:"title" example:"Trip itinerary"`
	Content   string    `json:"content" example:"# Trip itinerary\n\nPlan the trip day by day."`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SopPage is one keyset page of sops. next_cursor is empty on the last page and
// is otherwise passed back as ?cursor= unchanged.
type SopPage struct {
	Items      []SopDTO `json:"items"`
	NextCursor string   `json:"next_cursor" example:""`
}

// CreateSopRequest is the body of POST /api/sops. user_id and the timestamps
// are not accepted: the server owns them. A title left out is derived from the
// content's first heading.
type CreateSopRequest struct {
	Slug    string `json:"slug" example:"trip-itinerary"`
	Title   string `json:"title" example:"Trip itinerary"`
	Content string `json:"content" example:"# Trip itinerary\n\nPlan the trip day by day."`
}

// PatchSopRequest is the body of PATCH /api/sops/{slug}. A field left out keeps
// what is stored; the slug itself is immutable.
type PatchSopRequest struct {
	Title   *string `json:"title" example:"Trip itinerary"`
	Content *string `json:"content" example:"# Trip itinerary\n\nPlan the trip day by day."`
}

// toSopDTO converts a stored row into its wire form.
func toSopDTO(sop *repository.Sop) SopDTO {
	return SopDTO{
		Slug:      sop.Slug,
		Title:     sop.Title,
		Content:   sop.Content,
		CreatedAt: sop.CreatedAt,
		UpdatedAt: sop.UpdatedAt,
	}
}

// ListSops returns one keyset page of the caller's sops.
//
//	@Summary		List sops
//	@Description	Returns the caller's sops ordered by slug ascending. The page continues from cursor, which is the next_cursor of the previous response. sops are per-user: no agent is involved, so one procedure is available to every agent the caller talks to.
//	@Tags			sops
//	@Security		BearerAuth
//	@Produce		json
//	@Param			cursor	query		string	false	"next_cursor of the previous page"
//	@Param			limit	query		int		false	"Page size, default 100, at most 100"
//	@Success		200		{object}	SopPage
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/sops [get]
func ListSops(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var cursor string
	if raw := c.Query("cursor"); raw != "" {
		decoded, err := decodeSopCursor(raw)
		if err != nil {
			abortBadRequest(c, "cursor is not valid.")
			return
		}
		cursor = decoded
	}

	limit := sopPageLimit(c.Query("limit"))
	sops, err := service.ListSops(c.Request.Context(), actor.ID, cursor, limit)
	if err != nil {
		abortError(c, err)
		return
	}

	items := make([]SopDTO, 0, len(sops))
	for i := range sops {
		items = append(items, toSopDTO(&sops[i]))
	}
	page := SopPage{Items: items}
	if len(sops) == limit {
		page.NextCursor = encodeSopCursor(sops[len(sops)-1].Slug)
	}
	c.JSON(http.StatusOK, page)
}

// GetSop returns one sop.
//
//	@Summary		Get a sop
//	@Description	Returns one of the caller's sops, content included. A slug that does not exist or belongs to another user is reported as 404.
//	@Tags			sops
//	@Security		BearerAuth
//	@Produce		json
//	@Param			slug	path		string	true	"Sop slug"
//	@Success		200		{object}	SopDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/sops/{slug} [get]
func GetSop(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	sop, err := service.GetSop(c.Request.Context(), actor.ID, c.Param("slug"))
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSopDTO(sop))
}

// CreateSop stores one sop.
//
//	@Summary		Create a sop
//	@Description	Stores one procedure under a slug the caller picks. The slug is what a message calls the procedure by, so it is limited to lowercase letters, digits and hyphens; a slug the caller already uses answers 409 rather than overwriting the stored procedure. content is limited to 2000 characters and a longer one is rejected rather than truncated. title is optional and defaults to the content's first heading, then to the slug.
//	@Tags			sops
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateSopRequest	true	"Sop to store"
//	@Success		201		{object}	SopDTO
//	@Header			201		{string}	Location	"URL of the created sop"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/sops [post]
func CreateSop(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request CreateSopRequest
	if err := decodeStrictJSON(c, &request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	sop, err := service.CreateSop(c.Request.Context(), actor.ID, service.CreateSopInput{
		Slug:    request.Slug,
		Title:   request.Title,
		Content: request.Content,
	})
	if err != nil {
		abortError(c, err)
		return
	}

	c.Header("Location", "/api/sops/"+sop.Slug)
	c.JSON(http.StatusCreated, toSopDTO(sop))
}

// PatchSop rewrites the title or content of one sop.
//
//	@Summary		Update a sop
//	@Description	Rewrites the title or content of one of the caller's sops. A field left out keeps what is stored. The slug is part of the identity and cannot be changed; a body that names it answers 400 IMMUTABLE_FIELD. content is limited to 2000 characters and a longer one is rejected rather than truncated.
//	@Tags			sops
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			slug	path		string				true	"Sop slug"
//	@Param			request	body		PatchSopRequest	true	"Fields to change"
//	@Success		200		{object}	SopDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/sops/{slug} [patch]
func PatchSop(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var body map[string]any
	if err := decodeStrictJSON(c, &body); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}
	if code, message, ok := checkSopPatchFields(body); !ok {
		utils.Abort(c, code, message)
		return
	}

	var request PatchSopRequest
	if raw, ok := body["title"]; ok {
		title, isString := raw.(string)
		if !isString {
			abortBadRequest(c, "title must be a string.")
			return
		}
		request.Title = &title
	}
	if raw, ok := body["content"]; ok {
		content, isString := raw.(string)
		if !isString {
			abortBadRequest(c, "content must be a string.")
			return
		}
		request.Content = &content
	}

	sop, err := service.UpdateSop(c.Request.Context(), actor.ID, c.Param("slug"), service.UpdateSopInput{
		Title:   request.Title,
		Content: request.Content,
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSopDTO(sop))
}

// DeleteSop deletes one sop.
//
//	@Summary		Delete a sop
//	@Description	Hard delete of one of the caller's sop. A message that named the slug afterwards is an ordinary message again.
//	@Tags			sops
//	@Security		BearerAuth
//	@Param			slug	path	string	true	"Sop slug"
//	@Success		204
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/sops/{slug} [delete]
func DeleteSop(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	if err := service.DeleteSop(c.Request.Context(), actor.ID, c.Param("slug")); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SopsRouter registers the sop routes.
func SopsRouter(r *gin.Engine) {
	sops := r.Group("/api/sops", authOrAPIKey())
	sops.GET("", ListSops)
	sops.POST("", CreateSop)
	sops.GET("/:slug", GetSop)
	sops.PATCH("/:slug", PatchSop)
	sops.DELETE("/:slug", DeleteSop)
}

// sopPageLimit reads the limit query parameter. A missing, non-numeric,
// non-positive or oversized value falls back to the default page size.
func sopPageLimit(raw string) int {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > sopMaxLimit {
		return sopDefaultLimit
	}
	return limit
}

// encodeSopCursor turns a slug into the opaque cursor a client passes back.
func encodeSopCursor(slug string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(slug))
}

// decodeSopCursor parses a cursor produced by encodeSopCursor.
func decodeSopCursor(value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// checkSopPatchFields reports whether a PATCH body may be applied. title and
// content are the only writable fields; the slug is immutable and answers 400
// IMMUTABLE_FIELD, while an unknown field is a bad request. A body with neither
// writable field is a bad request too.
func checkSopPatchFields(body map[string]any) (utils.Code, string, bool) {
	for field := range body {
		switch field {
		case "title", "content":
			continue
		case "slug", "id", "user_id", "created_at", "updated_at":
			return utils.ImmutableField, field + " cannot be changed.", false
		default:
			return utils.BadRequest, "Unknown field " + field + ".", false
		}
	}
	if _, ok := body["title"]; ok {
		return "", "", true
	}
	if _, ok := body["content"]; ok {
		return "", "", true
	}
	return utils.BadRequest, "title or content is required.", false
}
