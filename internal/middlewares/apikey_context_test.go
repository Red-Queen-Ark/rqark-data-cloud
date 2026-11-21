package middlewares

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rqark-data-cloud/internal/repository"
)

// keyRouter serves a route that reports the key id the middleware stored, so a
// test can see what CurrentAPIKeyID would give a handler.
func keyRouter(user *repository.User, authenticator APIKeyAuthenticator) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/protected", AuthOrAPIKey(testSecret, activeLoader(user), authenticator, nopLastSeen), func(c *gin.Context) {
		id, ok := CurrentAPIKeyID(c)
		if !ok {
			c.String(http.StatusOK, "none")
			return
		}
		c.String(http.StatusOK, id.String())
	})
	return r
}

// TestAuthOrAPIKeyStoresTheKeyID covers the API key path: the key the caller
// presented is named in the context, which is what attributes usage to a key.
func TestAuthOrAPIKeyStoresTheKeyID(t *testing.T) {
	keyID := uuid.New()
	user := testUser()
	r := keyRouter(user, func(context.Context, string) (*repository.User, uuid.UUID, error) {
		return user, keyID, nil
	})

	recorder := do(r, "Bearer rqark-9Zk2Xf1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1p")

	if recorder.Body.String() != keyID.String() {
		t.Errorf("key id = %q, want %q", recorder.Body.String(), keyID)
	}
}

// TestAuthOrAPIKeyLeavesNoKeyIDForAToken covers the access token path: a token
// has no key behind it, so the context is left without one.
func TestAuthOrAPIKeyLeavesNoKeyIDForAToken(t *testing.T) {
	user := testUser()
	r := keyRouter(user, func(context.Context, string) (*repository.User, uuid.UUID, error) {
		t.Error("the API key authenticator was called for an access token")
		return nil, uuid.Nil, ErrAPIKeyRejected
	})

	recorder := do(r, "Bearer "+token(t, user, time.Minute))

	if recorder.Body.String() != "none" {
		t.Errorf("key id = %q, want none for an access token", recorder.Body.String())
	}
}

// TestCurrentAPIKeyIDWithoutMiddleware covers a route that never ran
// AuthOrAPIKey: there is no key, and the second result says so.
func TestCurrentAPIKeyIDWithoutMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)

	if _, ok := CurrentAPIKeyID(context); ok {
		t.Error("CurrentAPIKeyID reported a key for a bare context")
	}
}
