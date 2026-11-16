package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/middlewares"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

const attributionSecret = "run-attribution-secret"

// activeUserLoader answers any lookup with the same active user, so an
// attribution test needs no database.
func activeUserLoader(user *repository.User) middlewares.UserLoader {
	return func(context.Context, uuid.UUID) (*repository.User, error) {
		return user, nil
	}
}

// attributionTags sends body to the chat route with raw and answers the tags the
// proxy would record for the call.
func attributionTags(t *testing.T, body, raw string) usageTags {
	t.Helper()

	gin.SetMode(gin.TestMode)
	user := &repository.User{ID: uuid.New(), IsActive: true}

	var tags usageTags
	r := gin.New()
	r.POST("/api/chat/:uuid/chat/completions", middlewares.Auth([]byte(attributionSecret), activeUserLoader(user), nil), func(c *gin.Context) {
		tags = withRunAttribution(c, readUsageTags(decodeFields(t, body)))
		c.String(http.StatusOK, "ok")
	})

	request := httptest.NewRequest(http.MethodPost, "/api/chat/"+uuid.NewString()+"/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	return tags
}

// TestARunTokenAttributesTheCallToItsSession covers what a run cannot say for
// itself: the session comes off the token, so the usage row lands on the run's
// transcript even though the body names none.
func TestARunTokenAttributesTheCallToItsSession(t *testing.T) {
	sessionID := uuid.New()
	raw, err := utils.SignRunToken(uuid.New(), uuid.New(), sessionID, []byte(attributionSecret), time.Minute)
	if err != nil {
		t.Fatalf("SignRunToken: %v", err)
	}

	tags := attributionTags(t, `{"messages":[]}`, raw)

	if tags.SessionID == nil || *tags.SessionID != sessionID {
		t.Errorf("SessionID = %v, want %v", tags.SessionID, sessionID)
	}
	if tags.Source != runSource {
		t.Errorf("Source = %q, want %q", tags.Source, runSource)
	}
}

// TestARunTokenCannotClaimAnotherSession covers the session the body names: the
// token's own session wins.
func TestARunTokenCannotClaimAnotherSession(t *testing.T) {
	sessionID := uuid.New()
	other := uuid.New()
	raw, err := utils.SignRunToken(uuid.New(), uuid.New(), sessionID, []byte(attributionSecret), time.Minute)
	if err != nil {
		t.Fatalf("SignRunToken: %v", err)
	}

	tags := attributionTags(t, `{"messages":[],"session_id":"`+other.String()+`"}`, raw)

	if tags.SessionID == nil || *tags.SessionID != sessionID {
		t.Errorf("SessionID = %v, want %v", tags.SessionID, sessionID)
	}
}

// TestAPersonsTokenKeepsTheTagsItSent covers the other side: a person's call is
// attributed to whatever the client said, and nothing is invented.
func TestAPersonsTokenKeepsTheTagsItSent(t *testing.T) {
	raw, err := utils.SignAccessToken(uuid.New(), []byte(attributionSecret), time.Minute)
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}

	tags := attributionTags(t, `{"messages":[]}`, raw)

	if tags.SessionID != nil {
		t.Errorf("SessionID = %v, want none", tags.SessionID)
	}
	if tags.Source != "" {
		t.Errorf("Source = %q, want empty", tags.Source)
	}
}
