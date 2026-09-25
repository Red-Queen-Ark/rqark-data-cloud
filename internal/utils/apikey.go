package utils

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

const (
	// APIKeyPrefix marks a credential as one of our API keys. It is what tells
	// an API key apart from an access token on the wire, and it is the only
	// part of a key that survives in the database.
	APIKeyPrefix = "rqark-"
	// APIKeyDisplayLength is how much of a key is kept in clear, so its owner
	// can tell one key from another without being shown them again.
	APIKeyDisplayLength = 12
	// apiKeyEntropyBytes is the size of the random part of a key. 32 bytes is
	// far beyond guessing range and encodes to 43 characters.
	apiKeyEntropyBytes = 32
	// apiKeyHashDomain separates key hashes from every other use of
	// SECRET_KEY, so a signature made for one purpose is never one for another.
	apiKeyHashDomain = "rqark-apikey-v1:"
)

// NewAPIKey returns a fresh API key together with the hash to store for it. The
// key itself is returned to its owner once, at creation, and is never stored in
// clear: only the hash reaches the database.
func NewAPIKey(secret []byte) (key, hash string, err error) {
	random := make([]byte, apiKeyEntropyBytes)
	if _, err := rand.Read(random); err != nil {
		return "", "", err
	}

	key = APIKeyPrefix + base64.RawURLEncoding.EncodeToString(random)
	return key, HashAPIKey(secret, key), nil
}

// HashAPIKey returns the hex encoded HMAC-SHA256 of key under secret. The hash
// is what the key lookup compares, so a leaked database alone does not let
// anyone forge a key: SECRET_KEY is needed as well.
func HashAPIKey(secret []byte, key string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(apiKeyHashDomain + key))
	return hex.EncodeToString(mac.Sum(nil))
}

// IsAPIKey reports whether raw carries the API key prefix. It is the check that
// routes a credential to the API key path instead of the access token path.
func IsAPIKey(raw string) bool {
	return strings.HasPrefix(raw, APIKeyPrefix) && len(raw) > len(APIKeyPrefix)
}

// APIKeyDisplayPrefix returns the leading characters of key, the part stored as
// key_prefix so a listing can show which key is which.
func APIKeyDisplayPrefix(key string) string {
	if len(key) <= APIKeyDisplayLength {
		return key
	}
	return key[:APIKeyDisplayLength]
}
