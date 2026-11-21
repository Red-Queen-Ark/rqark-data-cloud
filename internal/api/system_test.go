package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"rqark-data-cloud/internal/config"
)

// systemRouterWithRoutes builds a router carrying the system and auth routes a
// client reads before it can log in.
func systemRouterWithRoutes(t *testing.T) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	SystemRouter(router)
	AuthRouter(router)
	return router
}

// TestSystemConfigReportsTheRegistrationSwitch covers the endpoint the login
// pages read: the answer has to follow ALLOW_SELF_REGISTRATION, not be a
// constant, otherwise hiding the link is guesswork.
func TestSystemConfigReportsTheRegistrationSwitch(t *testing.T) {
	for _, allow := range []bool{true, false} {
		previous := config.AllowSelfRegistration
		config.AllowSelfRegistration = allow

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/system/config", nil)
		systemRouterWithRoutes(t).ServeHTTP(recorder, request)

		config.AllowSelfRegistration = previous

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}

		var body SystemConfigResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.AllowSelfRegistration != allow {
			t.Errorf("allow_self_registration = %v, want %v", body.AllowSelfRegistration, allow)
		}
	}
}

// TestRegisterIsForbiddenWhenSelfRegistrationIsOff covers the guard: with the
// switch off the handler answers 403 before it reads the body, so a deployment
// cannot be talked into creating an account.
func TestRegisterIsForbiddenWhenSelfRegistrationIsOff(t *testing.T) {
	previous := config.AllowSelfRegistration
	config.AllowSelfRegistration = false
	defer func() { config.AllowSelfRegistration = previous }()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"username":"newcomer","email":"newcomer@example.com","password":"correct-horse-battery"}`))
	request.Header.Set("Content-Type", "application/json")
	systemRouterWithRoutes(t).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}
