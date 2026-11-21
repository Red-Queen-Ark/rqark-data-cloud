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
	"rqark-data-cloud/internal/utils"
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

	chat := r.Group("/api/chat", mw)
	chat.POST("", ok)
	chat.POST("/:uuid/chat/completions", ok)

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
	r := newScopedRouter(Auth(testSecret, activeLoader(user), nopLastSeen))
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
	r := newScopedRouter(Auth(testSecret, activeLoader(user), nopLastSeen))
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

// TestRunTokenReachesTheChatProxy covers the one route a run token may reach
// that names no session of its own: the model call. The provider in the path is
// not the run's session, so the whitelist has to answer for the route alone.
func TestRunTokenReachesTheChatProxy(t *testing.T) {
	user := testUser()
	sessionID := uuid.New()
	r := newScopedRouter(Auth(testSecret, activeLoader(user), nopLastSeen))

	path := "/api/chat/" + uuid.New().String() + "/chat/completions"
	if recorder := call(r, http.MethodPost, path, runToken(t, user, sessionID)); recorder.Code != http.StatusOK {
		t.Errorf("chat = %d, want %d (body %q)", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

// TestRunTokenIsRefusedTheOtherChatRoute covers the form that names the provider
// in the body: a run token reaches one route, not the whole proxy.
func TestRunTokenIsRefusedTheOtherChatRoute(t *testing.T) {
	user := testUser()
	sessionID := uuid.New()
	r := newScopedRouter(Auth(testSecret, activeLoader(user), nopLastSeen))

	if recorder := call(r, http.MethodPost, "/api/chat", runToken(t, user, sessionID)); recorder.Code != http.StatusForbidden {
		t.Errorf("chat = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

// sessionOf runs one request through a router that carries the real route
// patterns and reports what the middleware left for a handler: the run's
// session, and whether there was one.
func sessionOf(t *testing.T, mw gin.HandlerFunc, method, path, raw string) (uuid.UUID, bool) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	var got uuid.UUID
	var found bool
	read := func(c *gin.Context) {
		got, found = CurrentRunSession(c)
		c.String(http.StatusOK, "ok")
	}
	r := gin.New()
	r.POST("/api/chat/:uuid/chat/completions", mw, read)
	r.GET("/api/agents", mw, read)

	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	r.ServeHTTP(httptest.NewRecorder(), request)
	return got, found
}

// TestTheRunSessionIsAvailableToTheHandler covers the attribution a run cannot
// state itself: the handler reads the session off the token, not the body.
func TestTheRunSessionIsAvailableToTheHandler(t *testing.T) {
	user := testUser()
	sessionID := uuid.New()

	got, found := sessionOf(t, Auth(testSecret, activeLoader(user), nopLastSeen), http.MethodPost, "/api/chat/"+uuid.New().String()+"/chat/completions", runToken(t, user, sessionID))

	if !found || got != sessionID {
		t.Errorf("run session = %v, found = %v, want %v", got, found, sessionID)
	}
}

// TestAPersonsTokenNamesNoRunSession covers the other side: an ordinary token
// leaves the run session unset, so nothing is attributed to a run.
func TestAPersonsTokenNamesNoRunSession(t *testing.T) {
	user := testUser()

	raw, err := utils.SignAccessToken(user.ID, testSecret, time.Minute)
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}

	if got, found := sessionOf(t, Auth(testSecret, activeLoader(user), nopLastSeen), http.MethodGet, "/api/agents", raw); found {
		t.Errorf("run session = %v, found = %v, want none", got, found)
	}
}

// TestRunTokenOfADisabledUserIsRefused covers the account check running before
// the scope check: a run issued for an account disabled since is still refused.
func TestRunTokenOfADisabledUserIsRefused(t *testing.T) {
	user := testUser()
	user.IsActive = false
	sessionID := uuid.New()
	r := newScopedRouter(Auth(testSecret, activeLoader(user), nopLastSeen))

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
	r := newScopedRouter(AuthOrAPIKey(testSecret, activeLoader(user), func(context.Context, string) (*repository.User, uuid.UUID, error) {
		return nil, uuid.Nil, ErrAPIKeyRejected
	}, nopLastSeen))
	raw := runToken(t, user, sessionID)

	if recorder := call(r, http.MethodGet, "/api/sessions/"+sessionID.String()+"/messages", raw); recorder.Code != http.StatusOK {
		t.Errorf("own session = %d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder := call(r, http.MethodGet, "/api/agents", raw); recorder.Code != http.StatusForbidden {
		t.Errorf("agents = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}
