package service

import (
	"encoding/json"
	"regexp"

	"rqark-data-cloud/internal/utils"
)

// The rules below are shared by every key/value config table: user_configs and
// system_configs store the same shape, so they accept the same keys and the
// same values. Only the owner differs, and that lives in the service that
// writes the row.

const (
	// maxConfigKeyLen is the width of the key column.
	maxConfigKeyLen = 128
	// maxConfigValueLen caps one stored config value. It is measured on the
	// encoded JSON text and kept well below the request body limit so a config
	// sync cannot be turned into a bulk upload.
	maxConfigValueLen = 64 * 1024
)

// configKeyPattern is the key grammar: URL-safe, so a key can be a path
// segment, and namespaced by dots or dashes when a setting belongs to one
// client.
var configKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// validateConfigKey checks that key is present, fits its column and is made of
// characters that are safe in a path segment.
func validateConfigKey(key string) error {
	if key == "" {
		return &ValidationError{Message: "key is required."}
	}
	if len(key) > maxConfigKeyLen {
		return &ValidationError{Message: "key is too long."}
	}
	if !configKeyPattern.MatchString(key) {
		return &ValidationError{Message: "key may only contain letters, digits, '_', '.' and '-'."}
	}
	return nil
}

// sanitizeConfigValue decodes value, drops every U+0000 and re-encodes it, then
// enforces the size cap. A missing value is rejected so a key is never created
// without one.
func sanitizeConfigValue(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 {
		return nil, &ValidationError{Message: "value is required."}
	}

	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, &ValidationError{Message: "value must be valid JSON."}
	}
	if decoded == nil {
		return nil, &ValidationError{Message: "value must not be null."}
	}
	encoded, err := json.Marshal(utils.StripNullChars(decoded))
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxConfigValueLen {
		return nil, &ValidationError{Message: "value is too large."}
	}
	return json.RawMessage(encoded), nil
}
