package middlewares

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// seenRecorder counts the calls a recorder receives and answers with err.
func seenRecorder(err error) (LastSeenRecorder, *[]uuid.UUID) {
	seen := &[]uuid.UUID{}
	return func(_ context.Context, id uuid.UUID) error {
		*seen = append(*seen, id)
		return err
	}, seen
}

func TestAccessTokenRecordsLastSeen(t *testing.T) {
	user := testUser()
	record, seen := seenRecorder(nil)
	r := newTestRouter(Auth(testSecret, activeLoader(user), record))

	if recorder := do(r, "Bearer "+token(t, user, time.Minute)); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	if len(*seen) != 1 || (*seen)[0] != user.ID {
		t.Errorf("recorded %v, want [%s]", *seen, user.ID)
	}
}

// TestAPIKeyDoesNotRecordLastSeen pins the rule that a key is not presence: a
// key can be held by a script, and the owner is not necessarily anywhere near
// the request it makes.
func TestAPIKeyDoesNotRecordLastSeen(t *testing.T) {
	user := testUser()
	record, seen := seenRecorder(nil)
	r := newTestRouter(AuthOrAPIKey(testSecret, activeLoader(user), func(context.Context, string) (*repository.User, uuid.UUID, error) {
		return user, uuid.Nil, nil
	}, record))

	if recorder := do(r, "Bearer rqark-9Zk2Xf1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1p"); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	if len(*seen) != 0 {
		t.Errorf("recorded %v, want nothing", *seen)
	}
}

// TestRunTokenDoesNotRecordLastSeen pins the other half of the rule: a run
// token is an agent working on the user's behalf, not the user being online.
func TestRunTokenDoesNotRecordLastSeen(t *testing.T) {
	user := testUser()
	sessionID := uuid.New()
	record, seen := seenRecorder(nil)
	r := newScopedRouter(Auth(testSecret, activeLoader(user), record))

	if recorder := call(r, http.MethodGet, "/api/sessions/"+sessionID.String(), runToken(t, user, sessionID)); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	if len(*seen) != 0 {
		t.Errorf("recorded %v, want nothing", *seen)
	}
}

// TestFailedRecordDoesNotFailTheRequest covers the failure path: presence is a
// side record, so a database error behind it leaves the response alone.
func TestFailedRecordDoesNotFailTheRequest(t *testing.T) {
	user := testUser()
	record, _ := seenRecorder(errors.New("database is down"))
	r := newTestRouter(Auth(testSecret, activeLoader(user), record))

	if recorder := do(r, "Bearer "+token(t, user, time.Minute)); recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}
