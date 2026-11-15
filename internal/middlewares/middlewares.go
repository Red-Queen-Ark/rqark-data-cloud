// Package middlewares holds the Gin middleware shared by the authenticated
// routes.
package middlewares

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// userContextKey is the gin context key holding the authenticated user.
const userContextKey = "middlewares.user"

// apiKeyContextKey is the gin context key holding the id of the API key a
// request authenticated with. It is set only on the API key path: an access
// token request carries no key, and nothing guesses one.
const apiKeyContextKey = "middlewares.apiKeyID"

// ErrAPIKeyRejected is what an APIKeyAuthenticator reports for a key that may
// not be used. Any other error it returns is a server-side failure, not a
// rejected credential.
var ErrAPIKeyRejected = errors.New("middlewares: api key rejected")

// UserLoader loads the user with the given primary key. It reports
// repository.ErrUserNotFound when no such user exists.
type UserLoader func(ctx context.Context, id uuid.UUID) (*repository.User, error)

// APIKeyAuthenticator resolves the user an API key belongs to, and the id of
// the key that was presented. It reports ErrAPIKeyRejected whenever the key may
// not be used, whatever the reason. The id is uuid.Nil when the implementation
// has no key to name, and a nil id never reaches the context.
type APIKeyAuthenticator func(ctx context.Context, raw string) (*repository.User, uuid.UUID, error)

// LastSeenRecorder records that a user was active at this moment. It is called
// on the access token paths only: a request made with an API key never counts
// as the user being online, because a key outlives the session it was made for
// and may belong to a script. It is also skipped for a run-scoped token, which
// is an agent working in the background rather than a person at a keyboard.
type LastSeenRecorder func(ctx context.Context, id uuid.UUID) error

// Auth authenticates the bearer access token, loads the user it names and
// stores that user in the request context. A request whose token is missing,
// invalid, expired, names an unknown user or names a disabled user is rejected
// with 401.
func Auth(secret []byte, load UserLoader, record LastSeenRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
			return
		}

		user, ok := authenticateAccessToken(c, raw, secret, load, record)
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
func AuthOrAPIKey(secret []byte, load UserLoader, apiKey APIKeyAuthenticator, record LastSeenRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
			return
		}

		var user *repository.User
		var keyID uuid.UUID
		if utils.IsAPIKey(raw) {
			user, keyID, ok = authenticateAPIKey(c, raw, apiKey)
		} else {
			user, ok = authenticateAccessToken(c, raw, secret, load, record)
		}
		if !ok {
			return
		}

		c.Set(userContextKey, user)
		if keyID != uuid.Nil {
			c.Set(apiKeyContextKey, keyID)
		}
		c.Next()
	}
}

// runScopeRoutes is the whole surface a run-scoped token may reach. The key is
// the method plus the route pattern, so the uuid is compared as a path parameter
// rather than as text, and a route added later is unreachable until it is named
// here.
var runScopeRoutes = map[string]bool{
	"GET /api/sessions/:uuid":          true,
	"PUT /api/sessions/:uuid":          true,
	"GET /api/sessions/:uuid/messages": true,
}

// runScopeAllows reports whether a run token bound to sessionID may make this
// call. The only session it may touch is its own: a run writes the transcript of
// the run it belongs to and reads nothing else.
func runScopeAllows(c *gin.Context, sessionID uuid.UUID) bool {
	if !runScopeRoutes[c.Request.Method+" "+c.FullPath()] {
		return false
	}
	return c.Param("uuid") == sessionID.String()
}

// authenticateAccessToken verifies one access token and loads its user, writing
// the matching rejection when either step fails. A run-scoped token is held to
// its own session here, so a handler behind Auth never sees one. A token that
// gets this far has authenticated a person, so the user's last_seen_at is
// advanced before the request runs.
func authenticateAccessToken(c *gin.Context, raw string, secret []byte, load UserLoader, record LastSeenRecorder) (*repository.User, bool) {
	claims, err := utils.ParseTokenClaims(raw, secret)
	if err != nil {
		utils.Abort(c, utils.Unauthorized, "Invalid or expired access token.")
		return nil, false
	}

	user, err := load(c.Request.Context(), claims.UserID)
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

	if claims.Scope == utils.ScopeRun {
		if !runScopeAllows(c, claims.SessionID) {
			utils.Abort(c, utils.Forbidden, "This token cannot be used here.")
			return nil, false
		}
		return user, true
	}

	recordLastSeen(c, record, user.ID)
	return user, true
}

// recordLastSeen advances the user's last_seen_at and drops whatever goes
// wrong. Presence is a side record of the request, not part of what the request
// asked for, so a failure to write it must not turn a request that
// authenticated correctly into an error. A nil recorder is a no-op.
func recordLastSeen(c *gin.Context, record LastSeenRecorder, id uuid.UUID) {
	if record == nil {
		return
	}
	if err := record(c.Request.Context(), id); err != nil {
		log.Printf("middlewares: record last seen for user %s: %v", id, err)
	}
}

// authenticateAPIKey verifies one API key, writing the matching rejection when
// it fails. A key that is merely unusable is told apart from a failure behind
// the lookup, so a database that is down is not reported as a bad key. The key
// id is returned alongside the user so the context can name the key, and is
// uuid.Nil when the request was rejected.
func authenticateAPIKey(c *gin.Context, raw string, apiKey APIKeyAuthenticator) (*repository.User, uuid.UUID, bool) {
	user, keyID, err := apiKey(c.Request.Context(), raw)
	if err != nil {
		if errors.Is(err, ErrAPIKeyRejected) {
			utils.Abort(c, utils.Unauthorized, "Invalid or revoked API key.")
			return nil, uuid.Nil, false
		}
		utils.Abort(c, utils.Internal, "Internal server error.")
		return nil, uuid.Nil, false
	}
	return user, keyID, true
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

// CurrentAPIKeyID returns the id of the API key the request authenticated with.
// The second result is false for a request made with an access token, which has
// no key behind it, and for a route that never ran AuthOrAPIKey.
func CurrentAPIKeyID(c *gin.Context) (uuid.UUID, bool) {
	value, ok := c.Get(apiKeyContextKey)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := value.(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
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
