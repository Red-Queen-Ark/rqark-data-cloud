package middlewares

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// testSecret is the signing key of the access tokens the tests mint.
var testSecret = []byte("test-secret")

// newTestRouter serves GET /protected behind mw and answers the name of the
// user the middleware stored, so a test can see who was authenticated.
func newTestRouter(mw gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/protected", mw, func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok {
			c.String(http.StatusInternalServerError, "no user in context")
			return
		}
		c.String(http.StatusOK, user.Username)
	})
	return r
}

// do calls the guarded route with an Authorization header, or without one when
// header is empty.
func do(r *gin.Engine, header string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if header != "" {
		request.Header.Set("Authorization", header)
	}

	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	return recorder
}

// nopLastSeen is the recorder the tests that do not look at presence pass. It
// keeps the middleware from caring which user was recorded.
func nopLastSeen(context.Context, uuid.UUID) error { return nil }

// activeLoader is a UserLoader that answers one active user and nothing else.
func activeLoader(user *repository.User) UserLoader {
	return func(_ context.Context, id uuid.UUID) (*repository.User, error) {
		if id != user.ID {
			return nil, repository.ErrUserNotFound
		}
		return user, nil
	}
}

func testUser() *repository.User {
	return &repository.User{ID: uuid.New(), Username: "diana", IsActive: true}
}

func token(t *testing.T, user *repository.User, ttl time.Duration) string {
	t.Helper()

	raw, err := utils.SignAccessToken(user.ID, testSecret, ttl)
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	return raw
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) utils.Code {
	t.Helper()

	var body utils.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", recorder.Body.String(), err)
	}
	return body.Error.Code
}

func TestAuthOrAPIKeyWithoutCredentials(t *testing.T) {
	user := testUser()
	r := newTestRouter(AuthOrAPIKey(testSecret, activeLoader(user), func(context.Context, string) (*repository.User, error) {
		t.Error("the API key authenticator was called without a key")
		return nil, ErrAPIKeyRejected
	}, nopLastSeen))

	recorder := do(r, "")

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if code := errorCode(t, recorder); code != utils.Unauthorized {
		t.Errorf("code = %q, want %q", code, utils.Unauthorized)
	}
}

func TestAuthOrAPIKeyWithAccessToken(t *testing.T) {
	user := testUser()
	r := newTestRouter(AuthOrAPIKey(testSecret, activeLoader(user), func(context.Context, string) (*repository.User, error) {
		t.Error("the API key authenticator was called for an access token")
		return nil, ErrAPIKeyRejected
	}, nopLastSeen))

	recorder := do(r, "Bearer "+token(t, user, time.Minute))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Body.String() != user.Username {
		t.Errorf("body = %q, want %q", recorder.Body.String(), user.Username)
	}
}

func TestAuthOrAPIKeyWithExpiredAccessToken(t *testing.T) {
	user := testUser()
	r := newTestRouter(AuthOrAPIKey(testSecret, activeLoader(user), func(context.Context, string) (*repository.User, error) {
		return nil, ErrAPIKeyRejected
	}, nopLastSeen))

	recorder := do(r, "Bearer "+token(t, user, -time.Minute))

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestAuthOrAPIKeyWithDisabledUser(t *testing.T) {
	user := testUser()
	user.IsActive = false
	r := newTestRouter(AuthOrAPIKey(testSecret, activeLoader(user), func(context.Context, string) (*repository.User, error) {
		return nil, ErrAPIKeyRejected
	}, nopLastSeen))

	recorder := do(r, "Bearer "+token(t, user, time.Minute))

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestAuthOrAPIKeyWithAPIKey(t *testing.T) {
	user := testUser()
	raw := "rqark-9Zk2Xf1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1p"

	var seen string
	r := newTestRouter(AuthOrAPIKey(testSecret, func(context.Context, uuid.UUID) (*repository.User, error) {
		t.Error("the access token path was taken for an API key")
		return nil, repository.ErrUserNotFound
	}, func(_ context.Context, presented string) (*repository.User, error) {
		seen = presented
		return user, nil
	}, nopLastSeen))

	recorder := do(r, "Bearer "+raw)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if seen != raw {
		t.Errorf("authenticator saw %q, want %q", seen, raw)
	}
	if recorder.Body.String() != user.Username {
		t.Errorf("body = %q, want %q", recorder.Body.String(), user.Username)
	}
}

func TestAuthOrAPIKeyWithRejectedAPIKey(t *testing.T) {
	r := newTestRouter(AuthOrAPIKey(testSecret, activeLoader(testUser()), func(context.Context, string) (*repository.User, error) {
		return nil, ErrAPIKeyRejected
	}, nopLastSeen))

	recorder := do(r, "Bearer rqark-9Zk2Xf1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1p")

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if code := errorCode(t, recorder); code != utils.Unauthorized {
		t.Errorf("code = %q, want %q", code, utils.Unauthorized)
	}
}

func TestAuthOrAPIKeyWithFailingLookup(t *testing.T) {
	r := newTestRouter(AuthOrAPIKey(testSecret, activeLoader(testUser()), func(context.Context, string) (*repository.User, error) {
		return nil, errors.New("database is down")
	}, nopLastSeen))

	recorder := do(r, "Bearer rqark-9Zk2Xf1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1p")

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}

func TestAuthRejectsAPIKey(t *testing.T) {
	r := newTestRouter(Auth(testSecret, activeLoader(testUser()), nopLastSeen))

	recorder := do(r, "Bearer rqark-9Zk2Xf1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1p")

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestBearerTokenSchemeIsCaseInsensitive(t *testing.T) {
	user := testUser()
	r := newTestRouter(AuthOrAPIKey(testSecret, activeLoader(user), func(context.Context, string) (*repository.User, error) {
		return nil, ErrAPIKeyRejected
	}, nopLastSeen))

	recorder := do(r, "bearer "+token(t, user, time.Minute))

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}
