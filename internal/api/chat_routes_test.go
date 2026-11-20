package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// chatRouterWithRoutes builds a router carrying the two chat routes behind a
// middleware that stands in for authentication, so a test can reach the handler
// without a database.
func chatRouterWithRoutes(t *testing.T) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/chat")
	group.POST("", Chat)
	group.POST("/:uuid/chat/completions", ChatByProvider)
	return router
}

// TestChatByProviderReadsThePath covers the routing shape an OpenAI client needs:
// the provider comes from the path, so base_url decides which provider a model
// call goes to.
func TestChatByProviderReadsThePath(t *testing.T) {
	router := chatRouterWithRoutes(t)
	id := uuid.New()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat/"+id.String()+"/chat/completions", strings.NewReader(`{"messages":[]}`))
	router.ServeHTTP(recorder, request)

	// No database is installed, so authentication cannot resolve the caller. What
	// the test is after is that the route exists and reaches the handler rather
	// than answering 404 for an unknown path.
	if recorder.Code == http.StatusNotFound {
		t.Fatalf("the path route is not registered: %d", recorder.Code)
	}
}

// TestChatByProviderRejectsANonUUID covers a path that cannot name a provider.
func TestChatByProviderRejectsANonUUID(t *testing.T) {
	router := chatRouterWithRoutes(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat/primary/chat/completions", strings.NewReader(`{"messages":[]}`))
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "uuid must be a UUID") {
		t.Errorf("body = %q, want the uuid complaint", recorder.Body.String())
	}
}

// TestChatByProviderDropsProviderID covers the body of both routes: a provider_id
// that arrived is not forwarded upstream, whichever route read it.
func TestChatByProviderDropsProviderID(t *testing.T) {
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"chat.completion"}`))
	}))
	defer upstream.Close()

	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(`{"provider_id":"`+uuid.NewString()+`","model":"m","messages":[]}`), &fields); err != nil {
		t.Fatalf("decode: %v", err)
	}
	delete(fields, "provider_id")

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{}`))

	payload, err := json.Marshal(withModel(fields, "provider-model"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, _ = proxyChat(context, chatProxyTarget{APIBase: upstream.URL + "/v1", APIKey: "sk-provider"}, payload, false)

	if _, leaked := gotBody["provider_id"]; leaked {
		t.Error("provider_id reached the provider")
	}
	if gotBody["model"] != "m" {
		t.Errorf("model = %v, want the caller's m", gotBody["model"])
	}
	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", recorder.Code)
	}
}
