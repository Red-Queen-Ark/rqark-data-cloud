package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rqark-data-cloud/internal/repository"
)

// usageScopeContext builds a gin context for the scope check. The recorder
// captures the body of a rejection.
func usageScopeContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/usage", nil)
	return c, recorder
}

// TestUsageScopeRestrictsAPlainCaller covers the boundary: a plain caller is
// always scoped to itself, and a body that names a user is rejected rather than
// honoured.
func TestUsageScopeRestrictsAPlainCaller(t *testing.T) {
	actor := &repository.User{ID: uuid.New()}
	other := uuid.New()

	c, recorder := usageScopeContext()
	scope, ok := usageScope(c, actor, UsageQuery{})
	if !ok {
		t.Fatalf("a plain caller with no user_id was rejected: %s", recorder.Body.String())
	}
	if scope == nil || *scope != actor.ID {
		t.Fatalf("scope = %v, want the caller's own id", scope)
	}

	c, recorder = usageScopeContext()
	if _, ok := usageScope(c, actor, UsageQuery{UserID: &other}); ok {
		t.Fatal("a plain caller naming another user was accepted")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

// TestUsageScopeLetsASuperuserChoose covers the admin half: a superuser may name
// one user or none, and none means every user.
func TestUsageScopeLetsASuperuserChoose(t *testing.T) {
	actor := &repository.User{ID: uuid.New(), IsSuperuser: true}
	target := uuid.New()

	c, recorder := usageScopeContext()
	scope, ok := usageScope(c, actor, UsageQuery{UserID: &target})
	if !ok {
		t.Fatalf("a superuser naming a user was rejected: %s", recorder.Body.String())
	}
	if scope == nil || *scope != target {
		t.Fatalf("scope = %v, want %s", scope, target)
	}

	c, _ = usageScopeContext()
	scope, ok = usageScope(c, actor, UsageQuery{})
	if !ok {
		t.Fatal("a superuser with no user_id was rejected")
	}
	if scope != nil {
		t.Fatalf("scope = %v, want nil for every user", scope)
	}
}
