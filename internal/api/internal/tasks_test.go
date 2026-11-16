package internalapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/middlewares"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

const testInternalToken = "test-internal-token"

// newTestRouter serves the internal routes behind the given secret.
func newTestRouter(secret string) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	Router(r, secret)
	return r
}

// post sends one claim request with the given headers and body.
func post(r *gin.Engine, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	return recorder
}

// TestClaimRequiresTheInternalToken covers the three ways a caller can arrive
// without the shared secret: no header, a wrong one, and a bearer token, which
// this endpoint deliberately does not accept.
func TestClaimRequiresTheInternalToken(t *testing.T) {
	r := newTestRouter(testInternalToken)

	cases := map[string]map[string]string{
		"no header":    {},
		"wrong token":  {middlewares.InternalTokenHeader: "nope"},
		"bearer token": {"Authorization": "Bearer " + testInternalToken},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := post(r, "/api/internal/tasks/claim", `{"limit":0}`, headers)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d (body %q)", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
			}
			var body utils.ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error.Code != utils.Unauthorized {
				t.Errorf("code = %q, want %q", body.Error.Code, utils.Unauthorized)
			}
		})
	}
}

// TestClaimIsClosedWhenNoSecretIsConfigured covers the deployment that forgot the
// setting: an empty secret must not mean "any header, including none, is fine".
func TestClaimIsClosedWhenNoSecretIsConfigured(t *testing.T) {
	r := newTestRouter("")

	for name, headers := range map[string]map[string]string{
		"no header":   {},
		"empty value": {middlewares.InternalTokenHeader: ""},
		"any value":   {middlewares.InternalTokenHeader: "anything"},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := post(r, "/api/internal/tasks/claim", `{"limit":0}`, headers)
			if recorder.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
		})
	}
}

// TestClaimWithNoSlotsAnswersAnEmptyBatch covers the worker whose slots are all
// busy: it still sends a claim, and gets an empty batch rather than an error.
func TestClaimWithNoSlotsAnswersAnEmptyBatch(t *testing.T) {
	r := newTestRouter(testInternalToken)

	recorder := post(r, "/api/internal/tasks/claim", `{"limit":0}`, map[string]string{
		middlewares.InternalTokenHeader: testInternalToken,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body ClaimResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Runs) != 0 {
		t.Errorf("runs = %d, want 0", len(body.Runs))
	}
}

// TestClaimRefusesAnOutOfRangeLimit covers the bound: a worker cannot claim a
// whole backlog in one call, and a negative limit is a malformed request rather
// than an empty batch.
func TestClaimRefusesAnOutOfRangeLimit(t *testing.T) {
	r := newTestRouter(testInternalToken)
	headers := map[string]string{middlewares.InternalTokenHeader: testInternalToken}

	for _, body := range []string{`{"limit":-1}`, `{"limit":33}`, `{"limit":"one"}`} {
		recorder := post(r, "/api/internal/tasks/claim", body, headers)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("body %s = %d, want %d", body, recorder.Code, http.StatusBadRequest)
		}
	}
}

// TestFinalizeRefusesAMalformedPath covers the two uuid parameters, which are
// checked before the body is read so a typo in the path is reported as such.
func TestFinalizeRefusesAMalformedPath(t *testing.T) {
	r := newTestRouter(testInternalToken)

	request := httptest.NewRequest(http.MethodPatch, "/api/internal/tasks/not-a-uuid/runs/also-not", strings.NewReader(`{"status":"ok"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(middlewares.InternalTokenHeader, testInternalToken)
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

// TestClaimNeverHandsOutTheProviderKey covers what a claim response must not
// contain. The run reaches the model through the chat proxy, so the data cloud
// is the only place the key lives.
func TestClaimNeverHandsOutTheProviderKey(t *testing.T) {
	payload := toClaimedRun(&service.ClaimedRun{
		Provider: repository.Provider{Name: "openai", ModelName: "gpt-5", APIKey: "sk-secret", APIBase: "https://example.com"},
	})

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "sk-secret") {
		t.Errorf("the claim payload carries the provider key: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"api_base":"https://example.com"`) {
		t.Errorf("the claim payload lost the api_base: %s", encoded)
	}
}
