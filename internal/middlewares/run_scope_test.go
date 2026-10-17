package middlewares

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// newScopedRouter serves the three session routes a run token may reach, plus
// the routes it may not, all behind one middleware. Each handler answers "ok" so
// a test only has to look at the status code.
func newScopedRouter(mw gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)

	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	r := gin.New()

	sessions := r.Group("/api/sessions", mw)
	sessions.GET("/:uuid", ok)
	sessions.PUT("/:uuid", ok)
	sessions.DELETE("/:uuid", ok)
	sessions.GET("/:uuid/messages", ok)
	sessions.POST("", ok)

	r.GET("/api/agents", mw, ok)

	return r
}

// call sends method to path with the given bearer token.
func call(r *gin.Engine, method, path, raw string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	return recorder
}

// runToken mints a run token for the given session under testSecret.
func runToken(t *testing.T, user *repository.User, sessionID uuid.UUID) string {
	t.Helper()

	raw, err := utils.SignRunToken(user.ID, uuid.New(), sessionID, testSecret, time.Minute)
	if err != nil {
		t.Fatalf("SignRunToken: %v", err)
	}
	return raw
}

// TestRunTokenReachesOnlyItsOwnSession covers the whitelist: the three routes a
// run needs, and only with the uuid the token names.
func TestRunTokenReachesOnlyItsOwnSession(t *testing.T) {
	user := testUser()
	sessionID := uuid.New()
	r := newScopedRouter(Auth(testSecret, activeLoader(user)))
	raw := runToken(t, user, sessionID)

	allowed := []struct{ method, path string }{
		{http.MethodGet, "/api/sessions/" + sessionID.String()},
		{http.MethodPut, "/api/sessions/" + sessionID.String()},
		{http.MethodGet, "/api/sessions/" + sessionID.String() + "/messages"},
	}
	for _, route := range allowed {
		recorder := call(r, route.method, route.path, raw)
		if recorder.Code != http.StatusOK {
			t.Errorf("%s %s = %d, want %d (body %q)", route.method, route.path, recorder.Code, http.StatusOK, recorder.Body.String())
		}
	}
}

// TestRunTokenIsRefusedEverywhereElse covers the other half: another session's
// uuid, a delete, creating a session, and a route outside sessions at all.
func TestRunTokenIsRefusedEverywhereElse(t *testing.T) {
	user := testUser()
	sessionID := uuid.New()
	otherID := uuid.New()
	r := newScopedRouter(Auth(testSecret, activeLoader(user)))
	raw := runToken(t, user, sessionID)

	refused := []struct{ method, path string }{
		{http.MethodGet, "/api/sessions/" + otherID.String()},
		{http.MethodPut, "/api/sessions/" + otherID.String()},
		{http.MethodGet, "/api/sessions/" + otherID.String() + "/messages"},
		{http.MethodDelete, "/api/sessions/" + sessionID.String()},
		{http.MethodPost, "/api/sessions"},
		{http.MethodGet, "/api/agents"},
	}
	for _, route := range refused {
		recorder := call(r, route.method, route.path, raw)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want %d (body %q)", route.method, route.path, recorder.Code, http.StatusForbidden, recorder.Body.String())
		}
		if code := errorCode(t, recorder); code != utils.Forbidden {
			t.Errorf("%s %s code = %q, want %q", route.method, route.path, code, utils.Forbidden)
		}
	}
}

// TestRunTokenOfADisabledUserIsRefused covers the account check running before
// the scope check: a run issued for an account disabled since is still refused.
func TestRunTokenOfADisabledUserIsRefused(t *testing.T) {
	user := testUser()
	user.IsActive = false
	sessionID := uuid.New()
	r := newScopedRouter(Auth(testSecret, activeLoader(user)))

	recorder := call(r, http.MethodGet, "/api/sessions/"+sessionID.String(), runToken(t, user, sessionID))

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

// TestRunTokenIsHeldToTheWhitelistUnderAuthOrAPIKey covers the other guard: a
// route mounted with AuthOrAPIKey applies the same rule, so the whitelist is not
// bypassed by picking a different middleware.
func TestRunTokenIsHeldToTheWhitelistUnderAuthOrAPIKey(t *testing.T) {
	user := testUser()
	sessionID := uuid.New()
	r := newScopedRouter(AuthOrAPIKey(testSecret, activeLoader(user), func(context.Context, string) (*repository.User, error) {
		return nil, ErrAPIKeyRejected
	}))
	raw := runToken(t, user, sessionID)

	if recorder := call(r, http.MethodGet, "/api/sessions/"+sessionID.String()+"/messages", raw); recorder.Code != http.StatusOK {
		t.Errorf("own session = %d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder := call(r, http.MethodGet, "/api/agents", raw); recorder.Code != http.StatusForbidden {
		t.Errorf("agents = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}
