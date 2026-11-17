package service

import (
	"testing"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// TestMissingUserConfigDefaults covers the selection the fill is built from: a
// default already stored is skipped, every other default is returned with the
// owner attached.
func TestMissingUserConfigDefaults(t *testing.T) {
	userID := uuid.New()

	missing := missingUserConfigDefaults([]repository.UserConfig{
		{ID: uuid.New(), UserID: userID, Key: eventLogConfigKey},
	}, userID)

	if len(missing) != len(defaultUserConfigs)-1 {
		t.Fatalf("got %d missing defaults, want %d", len(missing), len(defaultUserConfigs)-1)
	}
	for i := range missing {
		if missing[i].Key == eventLogConfigKey {
			t.Errorf("%s is stored and should not be filled again", eventLogConfigKey)
		}
		if missing[i].UserID != userID {
			t.Errorf("default %s has owner %s, want %s", missing[i].Key, missing[i].UserID, userID)
		}
		if missing[i].ID == uuid.Nil {
			t.Errorf("default %s has no id", missing[i].Key)
		}
	}
}

// TestMissingUserConfigDefaultsAllPresent covers an account that already stored
// every default: the fill is empty, so a read writes nothing.
func TestMissingUserConfigDefaultsAllPresent(t *testing.T) {
	userID := uuid.New()

	stored := make([]repository.UserConfig, 0, len(defaultUserConfigs))
	for _, entry := range defaultUserConfigs {
		stored = append(stored, repository.UserConfig{ID: uuid.New(), UserID: userID, Key: entry.key})
	}

	if missing := missingUserConfigDefaults(stored, userID); len(missing) != 0 {
		t.Fatalf("got %d missing defaults, want 0", len(missing))
	}
}
