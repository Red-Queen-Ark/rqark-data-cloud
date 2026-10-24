package service

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestValidateConfigKey covers the key grammar: a key has to fit its
// column, stay URL-safe so it can be a path segment, and be present.
func TestValidateConfigKey(t *testing.T) {
	valid := []string{
		"theme",
		"editor.font-size",
		"client_a-1",
		"a",
		strings.Repeat("k", maxConfigKeyLen),
	}
	for _, key := range valid {
		if err := validateConfigKey(key); err != nil {
			t.Errorf("validateConfigKey(%q) = %v, want nil", key, err)
		}
	}

	invalid := []string{
		"",
		strings.Repeat("k", maxConfigKeyLen+1),
		"has space",
		"has/slash",
		"has?query",
	}
	for _, key := range invalid {
		if err := validateConfigKey(key); err == nil {
			t.Errorf("validateConfigKey(%q) = nil, want an error", key)
		}
	}
}

// TestSanitizeConfigValue covers the value rules: it has to be JSON, it has
// to be present and non-null, NULs are dropped because jsonb cannot hold them,
// and the encoded form is size-capped.
func TestSanitizeConfigValue(t *testing.T) {
	stored, err := sanitizeConfigValue(json.RawMessage(`{"text":"a\u0000b","count":2}`))
	if err != nil {
		t.Fatalf("sanitizeConfigValue: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(stored, &decoded); err != nil {
		t.Fatalf("the stored value is not JSON: %v", err)
	}
	if decoded["text"] != "ab" {
		t.Errorf("text = %q, want the NULs dropped", decoded["text"])
	}
	if decoded["count"] != float64(2) {
		t.Errorf("count = %v, want the number kept", decoded["count"])
	}

	for name, value := range map[string]json.RawMessage{
		"missing": nil,
		"null":    json.RawMessage(`null`),
		"invalid": json.RawMessage(`{`),
		"too big": json.RawMessage(`"` + strings.Repeat("x", maxConfigValueLen) + `"`),
	} {
		if _, err := sanitizeConfigValue(value); err == nil {
			t.Errorf("sanitizeConfigValue(%s) = nil error, want one", name)
		}
	}
}
