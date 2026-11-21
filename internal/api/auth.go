package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/service"
	"rqark-data-cloud/internal/utils"
)

// TokenPair is the response of login and refresh.
type TokenPair struct {
	AccessToken  string `json:"access_token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
	RefreshToken string `json:"refresh_token" example:"9f2c1a1b1c1d1e1f1a1b1c1d1e1f1a1b"`
}

// LoginRequest is the body of POST /api/auth/login.
type LoginRequest struct {
	Login    string `json:"login" example:"boss@example.com"`
	Password string `json:"password" example:"correct-horse-battery"`
}

// RefreshRequest is the body of POST /api/auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" example:"9f2c1a1b1c1d1e1f1a1b1c1d1e1f1a1b"`
}

// RegisterRequest is the body of POST /api/auth/register. It carries no
// is_superuser field: self-registration cannot grant superuser rights.
type RegisterRequest struct {
	Username    string `json:"username" example:"newcomer"`
	Email       string `json:"email" example:"newcomer@example.com"`
	Password    string `json:"password" example:"correct-horse-battery"`
	DisplayName string `json:"display_name" example:"New Comer"`
}

// ForgotPasswordRequest is the body of POST /api/auth/forgot-password.
type ForgotPasswordRequest struct {
	Email string `json:"email" example:"boss@example.com"`
}

// ResetPasswordRequest is the body of POST /api/auth/reset-password.
type ResetPasswordRequest struct {
	Token       string `json:"token" example:"9f2c1a1b1c1d1e1f1a1b1c1d1e1f1a1b"`
	NewPassword string `json:"new_password" example:"staple-correct-battery"`
}

// Login exchanges credentials for a token pair.
//
//	@Summary		Log in
//	@Description	Accepts a username or an email as login. A wrong password, an unknown account and a deactivated account all answer 401 with the same body.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		LoginRequest	true	"Credentials"
//	@Success		200		{object}	TokenPair
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/auth/login [post]
func Login(c *gin.Context) {
	var request LoginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	access, refresh, err := service.Login(c.Request.Context(), request.Login, request.Password)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			utils.Abort(c, utils.Unauthorized, "Invalid username or password.")
			return
		}
		abortError(c, err)
		return
	}

	c.JSON(http.StatusOK, TokenPair{AccessToken: access, RefreshToken: refresh})
}

// Refresh rotates a refresh token.
//
//	@Summary		Refresh a token pair
//	@Description	Consumes the refresh token and returns a new pair. The old token cannot be used again.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		RefreshRequest	true	"Refresh token"
//	@Success		200		{object}	TokenPair
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/auth/refresh [post]
func Refresh(c *gin.Context) {
	var request RefreshRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	access, refresh, err := service.Refresh(c.Request.Context(), request.RefreshToken)
	if err != nil {
		if errors.Is(err, service.ErrInvalidToken) {
			utils.Abort(c, utils.Unauthorized, "Invalid or expired refresh token.")
			return
		}
		abortError(c, err)
		return
	}

	c.JSON(http.StatusOK, TokenPair{AccessToken: access, RefreshToken: refresh})
}

// Register creates an account.
//
//	@Summary		Register
//	@Description	Creates an ordinary user and returns it. No token is issued; the caller logs in afterwards.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		RegisterRequest	true	"Account to create"
//	@Success		201		{object}	UserDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/auth/register [post]
func Register(c *gin.Context) {
	if !config.AllowSelfRegistration {
		utils.Abort(c, utils.Forbidden, "Self-registration is disabled.")
		return
	}

	var request RegisterRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	user, err := service.Register(c.Request.Context(), service.CreateUserInput{
		Username:    request.Username,
		Email:       request.Email,
		Password:    request.Password,
		DisplayName: request.DisplayName,
	})
	if err != nil {
		abortError(c, err)
		return
	}

	c.JSON(http.StatusCreated, toUserDTO(user))
}

// ForgotPassword starts a password reset.
//
//	@Summary		Request a password reset
//	@Description	Answers 204 whether or not the email exists, so the endpoint cannot be used to enumerate accounts. A reset link is generated for an existing active account only.
//	@Tags			auth
//	@Accept			json
//	@Param			request	body	ForgotPasswordRequest	true	"Email address"
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/auth/forgot-password [post]
func ForgotPassword(c *gin.Context) {
	var request ForgotPasswordRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	if err := service.ForgotPassword(c.Request.Context(), request.Email); err != nil {
		abortInternal(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ResetPassword finishes a password reset.
//
//	@Summary		Reset a password
//	@Description	Consumes the reset token and stores the new password. An unknown, expired or already used token answers 400 with the same body. No refresh token is revoked.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ResetPasswordRequest	true	"Reset token and new password"
//	@Success		200		{object}	MessageResponse
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/auth/reset-password [post]
func ResetPassword(c *gin.Context) {
	var request ResetPasswordRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	err := service.ResetPassword(c.Request.Context(), request.Token, request.NewPassword)
	if err != nil {
		if errors.Is(err, service.ErrInvalidToken) {
			utils.Abort(c, utils.BadRequest, "Invalid or expired reset token.")
			return
		}
		abortError(c, err)
		return
	}

	c.JSON(http.StatusOK, MessageResponse{Message: "Password updated."})
}

// AuthRouter registers the endpoints that do not need a token.
func AuthRouter(r *gin.Engine) {
	auth := r.Group("/api/auth")
	auth.POST("/login", Login)
	auth.POST("/refresh", Refresh)
	auth.POST("/register", Register)
	auth.POST("/forgot-password", ForgotPassword)
	auth.POST("/reset-password", ResetPassword)
}
