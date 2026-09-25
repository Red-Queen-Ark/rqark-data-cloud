package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/middlewares"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// UserDTO is the single user representation, shared by /users/me, /users/{uuid}
// and the group member listing.
type UserDTO struct {
	ID          uuid.UUID `json:"id" example:"3f1a5c1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Username    string    `json:"username" example:"boss"`
	Email       string    `json:"email" example:"boss@example.com"`
	DisplayName string    `json:"display_name" example:"Boss"`
	IsActive    bool      `json:"is_active" example:"true"`
	IsStaff     bool      `json:"is_staff" example:"false"`
	IsSuperuser bool      `json:"is_superuser" example:"true"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// MessageResponse acknowledges an operation that returns no resource.
type MessageResponse struct {
	Message string `json:"message" example:"Password updated."`
}

// toUserDTO converts a stored user into its wire form. The hashed password and
// every other column stay behind.
func toUserDTO(user *repository.User) UserDTO {
	return UserDTO{
		ID:          user.ID,
		Username:    user.Username,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		IsActive:    user.IsActive,
		IsStaff:     user.IsStaff,
		IsSuperuser: user.IsSuperuser,
		CreatedAt:   user.CreatedAt,
		UpdatedAt:   user.UpdatedAt,
	}
}

// CreateUserRequest is the body of POST /api/users.
type CreateUserRequest struct {
	Username    string `json:"username" example:"boss"`
	Email       string `json:"email" example:"boss@example.com"`
	Password    string `json:"password" example:"correct-horse-battery"`
	DisplayName string `json:"display_name" example:"Boss"`
	IsSuperuser bool   `json:"is_superuser" example:"false"`
}

// UpdateMeRequest is the body of PATCH /api/users/me. Only these three fields
// are accepted; is_superuser is present so that sending it can be rejected
// instead of ignored.
type UpdateMeRequest struct {
	Username    *string `json:"username"`
	Email       *string `json:"email"`
	DisplayName *string `json:"display_name"`
	IsSuperuser *bool   `json:"is_superuser"`
}

// UpdateUserRequest is the body of PATCH /api/users/{uuid}. Every field is
// optional and a field that is absent is left untouched.
type UpdateUserRequest struct {
	Username    *string `json:"username"`
	Email       *string `json:"email"`
	DisplayName *string `json:"display_name"`
	IsSuperuser *bool   `json:"is_superuser"`
	IsStaff     *bool   `json:"is_staff"`
	IsActive    *bool   `json:"is_active"`
}

// ChangePasswordRequest is the body of PUT /api/users/me/password.
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" example:"correct-horse-battery"`
	NewPassword string `json:"new_password" example:"staple-correct-battery"`
}

// SetPasswordRequest is the body of PUT /api/users/{uuid}/password.
type SetPasswordRequest struct {
	NewPassword string `json:"new_password" example:"staple-correct-battery"`
}

// GetMe returns the authenticated user.
//
//	@Summary		Get the current user
//	@Description	Returns the account behind the access token.
//	@Tags			users
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	UserDTO
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/users/me [get]
func GetMe(c *gin.Context) {
	user, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	c.JSON(http.StatusOK, toUserDTO(user))
}

// UpdateMe updates the caller's own profile.
//
//	@Summary		Update the current user
//	@Description	Changes username, email and display name. Sending is_superuser is rejected with 403 rather than ignored.
//	@Tags			users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		UpdateMeRequest	true	"Fields to change"
//	@Success		200		{object}	UserDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/users/me [patch]
func UpdateMe(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request UpdateMeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}
	if request.IsSuperuser != nil {
		utils.Abort(c, utils.Forbidden, "You cannot change your own superuser status.")
		return
	}

	updated, err := service.UpdateUser(c.Request.Context(), actor.ID, actor.ID, service.UpdateUserInput{
		Username:    request.Username,
		Email:       request.Email,
		DisplayName: request.DisplayName,
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(updated))
}

// ChangeMyPassword replaces the caller's password.
//
//	@Summary		Change the current user's password
//	@Description	Requires the current password. No refresh token is revoked, so other devices stay logged in.
//	@Tags			users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ChangePasswordRequest	true	"Current and new password"
//	@Success		200		{object}	MessageResponse
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/users/me/password [put]
func ChangeMyPassword(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request ChangePasswordRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}
	if request.OldPassword == "" || request.NewPassword == "" {
		abortBadRequest(c, "old_password and new_password are required.")
		return
	}

	err := service.ChangeOwnPassword(c.Request.Context(), actor.ID, request.OldPassword, request.NewPassword)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, MessageResponse{Message: "Password updated."})
}

// ListUsers returns a page of users.
//
//	@Summary		List users
//	@Description	Superuser only. Filters by is_active and searches username, email and display name with q.
//	@Tags			users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			limit		query		int		false	"Page size (default 20, max 100)"
//	@Param			offset		query		int		false	"Number of users to skip"
//	@Param			is_active	query		bool	false	"Keep only active or only inactive users"
//	@Param			q			query		string	false	"Search term for username, email or display name"
//	@Success		200			{object}	utils.Page[UserDTO]
//	@Failure		400			{object}	utils.ErrorResponse
//	@Failure		401			{object}	utils.ErrorResponse
//	@Failure		403			{object}	utils.ErrorResponse
//	@Failure		500			{object}	utils.ErrorResponse
//	@Router			/users [get]
func ListUsers(c *gin.Context) {
	page := utils.ParsePagination(c)

	var filter repository.UserFilter
	if raw := c.Query("is_active"); raw != "" {
		isActive, err := strconv.ParseBool(raw)
		if err != nil {
			abortBadRequest(c, "is_active must be true or false.")
			return
		}
		filter.IsActive = &isActive
	}
	filter.Query = c.Query("q")

	users, total, err := service.ListUsers(c.Request.Context(), filter, page.Limit, page.Offset)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]UserDTO, 0, len(users))
	for i := range users {
		items = append(items, toUserDTO(&users[i]))
	}
	c.JSON(http.StatusOK, utils.NewPage(items, total, page))
}

// CreateUser creates a user.
//
//	@Summary		Create a user
//	@Description	Superuser only. Answers 201 with a Location header pointing at the new user.
//	@Tags			users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateUserRequest	true	"User to create"
//	@Success		201		{object}	UserDTO
//	@Header			201		{string}	Location	"URL of the created user"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/users [post]
func CreateUser(c *gin.Context) {
	var request CreateUserRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	user, err := service.CreateUser(c.Request.Context(), service.CreateUserInput{
		Username:    request.Username,
		Email:       request.Email,
		Password:    request.Password,
		DisplayName: request.DisplayName,
		IsSuperuser: request.IsSuperuser,
	})
	if err != nil {
		abortError(c, err)
		return
	}

	c.Header("Location", "/api/users/"+user.ID.String())
	c.JSON(http.StatusCreated, toUserDTO(user))
}

// GetUser returns one user.
//
//	@Summary		Get a user
//	@Description	Superuser only.
//	@Tags			users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"User ID"	format(uuid)
//	@Success		200		{object}	UserDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/users/{uuid} [get]
func GetUser(c *gin.Context) {
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	user, err := service.GetUser(c.Request.Context(), id)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(user))
}

// UpdateUser updates one user.
//
//	@Summary		Update a user
//	@Description	Superuser only. Every field is optional; this is the only path that changes is_superuser. Is_active false deactivates the account and true restores it.
//	@Tags			users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string				true	"User ID"	format(uuid)
//	@Param			request	body		UpdateUserRequest	true	"Fields to change"
//	@Success		200		{object}	UserDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/users/{uuid} [patch]
func UpdateUser(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var request UpdateUserRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	updated, err := service.UpdateUser(c.Request.Context(), actor.ID, id, service.UpdateUserInput{
		Username:    request.Username,
		Email:       request.Email,
		DisplayName: request.DisplayName,
		IsSuperuser: request.IsSuperuser,
		IsStaff:     request.IsStaff,
		IsActive:    request.IsActive,
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(updated))
}

// SetUserPassword replaces the password of one user.
//
//	@Summary		Set a user's password
//	@Description	Superuser only. The current password is not required and no refresh token is revoked.
//	@Tags			users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string				true	"User ID"	format(uuid)
//	@Param			request	body		SetPasswordRequest	true	"New password"
//	@Success		200		{object}	MessageResponse
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/users/{uuid}/password [put]
func SetUserPassword(c *gin.Context) {
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var request SetPasswordRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	if err := service.SetPassword(c.Request.Context(), id, request.NewPassword); err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, MessageResponse{Message: "Password updated."})
}

// DeleteUser deactivates one user.
//
//	@Summary		Deactivate a user
//	@Description	Superuser only. Soft delete: the row stays and is_active becomes false. Use PATCH to restore. A caller cannot deactivate itself, and the last active superuser cannot be deactivated.
//	@Tags			users
//	@Security		BearerAuth
//	@Param			uuid	path	string	true	"User ID"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		403	{object}	utils.ErrorResponse
//	@Failure		409	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/users/{uuid} [delete]
func DeleteUser(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	if err := service.DeleteUser(c.Request.Context(), actor.ID, id); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// UsersRouter registers the user routes. Everything below /users requires a
// bearer credential, an API key included for the caller's own account; the
// collection and the uuid routes additionally require a superuser, and those
// take an access token only.
func UsersRouter(r *gin.Engine) {
	me := r.Group("/api/users", authOrAPIKey())
	me.GET("/me", GetMe)
	me.PATCH("/me", UpdateMe)
	me.PUT("/me/password", ChangeMyPassword)

	admin := r.Group("/api/users", superuserRequired()...)
	admin.GET("", ListUsers)
	admin.POST("", CreateUser)
	admin.GET("/:uuid", GetUser)
	admin.PATCH("/:uuid", UpdateUser)
	admin.PUT("/:uuid/password", SetUserPassword)
	admin.DELETE("/:uuid", DeleteUser)
}
