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

// UserLoader loads the user with the given primary key. It reports
// repository.ErrUserNotFound when no such user exists.
type UserLoader func(ctx context.Context, id uuid.UUID) (*repository.User, error)

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

		userID, err := utils.ParseAccessToken(raw, secret)
		if err != nil {
			utils.Abort(c, utils.Unauthorized, "Invalid or expired access token.")
			return
		}

		user, err := load(c.Request.Context(), userID)
		if err != nil {
			if errors.Is(err, repository.ErrUserNotFound) {
				utils.Abort(c, utils.Unauthorized, "Invalid or expired access token.")
				return
			}
			utils.Abort(c, utils.Internal, "Internal server error.")
			return
		}

		if !user.IsActive {
			utils.Abort(c, utils.Unauthorized, "Invalid or expired access token.")
			return
		}

		c.Set(userContextKey, user)
		c.Next()
	}
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
