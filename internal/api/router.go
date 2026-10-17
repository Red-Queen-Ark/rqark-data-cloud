package api

import (
	"context"
	"errors"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	internalapi "gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/api/internal"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/middlewares"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// APIRouter registers every route of the service.
func APIRouter(r *gin.Engine) {
	PingRouter(r)
	VersionRouter(r)
	SwaggerRouter(r)
	AuthRouter(r)
	UsersRouter(r)
	GroupsRouter(r)
	AgentsRouter(r)
	ProvidersRouter(r)
	SessionsRouter(r)
	APIKeysRouter(r)
	UserConfigsRouter(r)
	AgentDefaultsRouter(r)
	TasksRouter(r)
	// The internal routes live in their own package so that swag, which walks
	// this one, never pulls them into the public specification.
	internalapi.Router(r, config.InternalToken)
}

// LoadUser loads the user an access token names. It is the database hook the
// auth middleware calls on every authenticated request.
func LoadUser(ctx context.Context, id uuid.UUID) (*repository.User, error) {
	return repository.GetUserByID(ctx, config.DB, id)
}

// AuthenticateAPIKey resolves the user behind an API key. It is the hook the
// auth middleware calls when a request carries a key instead of an access
// token. A key that may not be used is reported as a rejected credential;
// anything else is a server-side failure.
func AuthenticateAPIKey(ctx context.Context, raw string) (*repository.User, error) {
	user, err := service.AuthenticateAPIKey(ctx, raw)
	if errors.Is(err, service.ErrInvalidAPIKey) {
		return nil, middlewares.ErrAPIKeyRejected
	}
	return user, err
}

// authRequired guards a route with bearer authentication.
func authRequired() gin.HandlerFunc {
	return middlewares.Auth([]byte(config.SecretKey), LoadUser)
}

// authOrAPIKey guards a route with bearer authentication that accepts either an
// access token or an API key.
func authOrAPIKey() gin.HandlerFunc {
	return middlewares.AuthOrAPIKey([]byte(config.SecretKey), LoadUser, AuthenticateAPIKey)
}

// superuserRequired guards a route with bearer authentication followed by the
// superuser check.
func superuserRequired() []gin.HandlerFunc {
	return []gin.HandlerFunc{authRequired(), middlewares.RequireSuperuser()}
}

// parseUUIDParam reads a UUID path parameter. It answers 400 and reports false
// when the value is not a UUID.
func parseUUIDParam(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		abortBadRequest(c, name+" must be a UUID.")
		return uuid.Nil, false
	}
	return id, true
}

// abortInternal logs err and answers 500.
func abortInternal(c *gin.Context, err error) {
	log.Printf("api: internal error: %v", err)
	utils.Abort(c, utils.Internal, "Internal server error.")
}

// abortBadRequest answers 400 for a body that cannot be decoded or a path
// parameter that is not a UUID.
func abortBadRequest(c *gin.Context, message string) {
	utils.Abort(c, utils.BadRequest, message)
}

// abortError maps the errors the service and repository layers return onto the
// error envelope. An unrecognised error is logged and reported as INTERNAL.
func abortError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrUsernameTaken):
		utils.Abort(c, utils.Conflict, "Username is already taken.")
	case errors.Is(err, service.ErrEmailTaken):
		utils.Abort(c, utils.Conflict, "Email is already taken.")
	case errors.Is(err, service.ErrGroupNameTaken):
		utils.Abort(c, utils.Conflict, "Group name is already taken.")
	case errors.Is(err, service.ErrAgentNameTaken):
		utils.Abort(c, utils.Conflict, "Agent name is already taken.")
	case errors.Is(err, service.ErrTaskNameTaken):
		utils.Abort(c, utils.Conflict, "Task name is already taken.")
	case errors.Is(err, service.ErrTaskAgentAmbiguous):
		utils.Abort(c, utils.AgentAmbiguous, "More than one agent you can use carries that name.")
	case errors.Is(err, service.ErrTaskProviderAmbiguous):
		utils.Abort(c, utils.ProviderAmbiguous, "More than one provider you can use carries that name.")
	case errors.Is(err, service.ErrAgentPublicForbidden):
		utils.Abort(c, utils.Forbidden, "Only a superuser can share an agent.")
	case errors.Is(err, service.ErrSessionConflict):
		utils.Abort(c, utils.Conflict, "Session already exists.")
	case errors.Is(err, repository.ErrAgentNotFound):
		utils.Abort(c, utils.NotFound, "Agent not found.")
	case errors.Is(err, repository.ErrTaskNotFound):
		utils.Abort(c, utils.NotFound, "Task not found.")
	case errors.Is(err, repository.ErrSessionNotFound):
		utils.Abort(c, utils.NotFound, "Session not found.")
	case errors.Is(err, repository.ErrUserNotFound):
		utils.Abort(c, utils.BadRequest, "User not found.")
	case errors.Is(err, repository.ErrGroupNotFound):
		utils.Abort(c, utils.BadRequest, "Group not found.")
	case errors.Is(err, repository.ErrProviderNotFound):
		utils.Abort(c, utils.NotFound, "Provider not found.")
	case errors.Is(err, service.ErrProviderPublicForbidden):
		utils.Abort(c, utils.Forbidden, "Only a superuser can share a provider.")
	case errors.Is(err, repository.ErrAPIKeyNotFound):
		utils.Abort(c, utils.NotFound, "API key not found.")
	case errors.Is(err, repository.ErrUserConfigNotFound):
		utils.Abort(c, utils.NotFound, "User config not found.")
	case errors.Is(err, repository.ErrAgentDefaultNotFound):
		utils.Abort(c, utils.NotFound, "Agent default not found.")
	case errors.Is(err, service.ErrAPIKeyRevoked):
		utils.Abort(c, utils.Conflict, "This API key was deleted and cannot be brought back.")
	case errors.Is(err, service.ErrAPIKeyLapsed):
		utils.Abort(c, utils.Conflict, "This API key has expired. Set expires_at to a future time to renew it.")
	case errors.Is(err, service.ErrOldPassword):
		utils.Abort(c, utils.BadRequest, "Current password is incorrect.")
	case errors.Is(err, service.ErrLastSuperuser):
		utils.Abort(c, utils.LastSuperuser, "The last active superuser cannot be removed.")
	case errors.Is(err, service.ErrSelfDemote):
		utils.Abort(c, utils.Forbidden, "You cannot change your own superuser status.")
	case errors.Is(err, service.ErrSelfDeactivate):
		utils.Abort(c, utils.Conflict, "You cannot deactivate your own account.")

	default:
		var policy *service.PolicyError
		var validation *service.ValidationError
		switch {
		case errors.As(err, &policy):
			utils.Abort(c, utils.BadRequest, policy.Error())
		case errors.As(err, &validation):
			utils.Abort(c, utils.BadRequest, validation.Message)
		default:
			abortInternal(c, err)
		}
	}
}
