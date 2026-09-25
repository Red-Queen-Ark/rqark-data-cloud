// Package middlewares holds the Gin middleware shared by the authenticated
// routes.
package middlewares

import (
	"context"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// userContextKey is the gin context key holding the authenticated user.
const userContextKey = "middlewares.user"

// ErrAPIKeyRejected is what an APIKeyAuthenticator reports for a key that may
// not be used. Any other error it returns is a server-side failure, not a
// rejected credential.
var ErrAPIKeyRejected = errors.New("middlewares: api key rejected")

// UserLoader loads the user with the given primary key. It reports
// repository.ErrUserNotFound when no such user exists.
type UserLoader func(ctx context.Context, id uuid.UUID) (*repository.User, error)

// APIKeyAuthenticator resolves the user an API key belongs to. It reports
// ErrAPIKeyRejected whenever the key may not be used, whatever the reason.
type APIKeyAuthenticator func(ctx context.Context, raw string) (*repository.User, error)

// Auth authenticates the bearer access token, loads the user it names and
// stores that user in the request context. A request whose token is missing,
// invalid, expired, names an unknown user or names a disabled user is rejected
// with 401.
func Auth(secret []byte, load UserLoader) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
			return
		}

		user, ok := authenticateAccessToken(c, raw, secret, load)
		if !ok {
			return
		}

		c.Set(userContextKey, user)
		c.Next()
	}
}

// AuthOrAPIKey authenticates either a bearer access token or an API key. The
// two are told apart by the prefix a key carries, and both end in the same
// request context user, so one route serves a signed-in browser and a
// key-carrying client alike. A key that cannot be used, an unknown one
// included, is rejected with 401; an access token is checked exactly as Auth
// checks it.
func AuthOrAPIKey(secret []byte, load UserLoader, apiKey APIKeyAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
			return
		}

		var user *repository.User
		if utils.IsAPIKey(raw) {
			user, ok = authenticateAPIKey(c, raw, apiKey)
		} else {
			user, ok = authenticateAccessToken(c, raw, secret, load)
		}
		if !ok {
			return
		}

		c.Set(userContextKey, user)
		c.Next()
	}
}

// authenticateAccessToken verifies one access token and loads its user, writing
// the matching rejection when either step fails.
func authenticateAccessToken(c *gin.Context, raw string, secret []byte, load UserLoader) (*repository.User, bool) {
	userID, err := utils.ParseAccessToken(raw, secret)
	if err != nil {
		utils.Abort(c, utils.Unauthorized, "Invalid or expired access token.")
		return nil, false
	}

	user, err := load(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			utils.Abort(c, utils.Unauthorized, "Invalid or expired access token.")
			return nil, false
		}
		utils.Abort(c, utils.Internal, "Internal server error.")
		return nil, false
	}

	if !user.IsActive {
		utils.Abort(c, utils.Unauthorized, "Invalid or expired access token.")
		return nil, false
	}
	return user, true
}

// authenticateAPIKey verifies one API key, writing the matching rejection when
// it fails. A key that is merely unusable is told apart from a failure behind
// the lookup, so a database that is down is not reported as a bad key.
func authenticateAPIKey(c *gin.Context, raw string, apiKey APIKeyAuthenticator) (*repository.User, bool) {
	user, err := apiKey(c.Request.Context(), raw)
	if err != nil {
		if errors.Is(err, ErrAPIKeyRejected) {
			utils.Abort(c, utils.Unauthorized, "Invalid or revoked API key.")
			return nil, false
		}
		utils.Abort(c, utils.Internal, "Internal server error.")
		return nil, false
	}
	return user, true
}

// RequireSuperuser rejects callers whose user is not a superuser. It must be
// mounted after Auth and only reads the user from the request context.
func RequireSuperuser() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok || !user.IsSuperuser {
			utils.Abort(c, utils.Forbidden, "You do not have permission to perform this action.")
			return
		}
		c.Next()
	}
}

// CurrentUser returns the user stored in the request context by Auth.
func CurrentUser(c *gin.Context) (*repository.User, bool) {
	value, ok := c.Get(userContextKey)
	if !ok {
		return nil, false
	}
	user, ok := value.(*repository.User)
	return user, ok
}

// bearerToken extracts the credentials from an "Authorization: Bearer <token>"
// header. The scheme is compared case-insensitively, the token itself is not.
func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return header[len(prefix):], true
}
