package utils

import (
	"strings"
	"testing"
)

const testSecret = "test-secret"

func TestNewAPIKeyCarriesPrefixAndMatchingHash(t *testing.T) {
	secret := []byte(testSecret)

	key, hash, err := NewAPIKey(secret)
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}

	if !strings.HasPrefix(key, APIKeyPrefix) {
		t.Errorf("key %q does not carry the %q prefix", key, APIKeyPrefix)
	}
	if len(key) != len(APIKeyPrefix)+43 {
		t.Errorf("key %q has length %d, want %d", key, len(key), len(APIKeyPrefix)+43)
	}
	if got := HashAPIKey(secret, key); got != hash {
		t.Errorf("NewAPIKey hash = %q, HashAPIKey = %q", hash, got)
	}
	if len(hash) != 64 {
		t.Errorf("hash %q has length %d, want 64 hex characters", hash, len(hash))
	}
}

func TestNewAPIKeyDoesNotRepeat(t *testing.T) {
	secret := []byte(testSecret)

	seen := make(map[string]bool, 64)
	for range 64 {
		key, _, err := NewAPIKey(secret)
		if err != nil {
			t.Fatalf("NewAPIKey: %v", err)
		}
		if seen[key] {
			t.Fatalf("NewAPIKey repeated the key %q", key)
		}
		seen[key] = true
	}
}

func TestHashAPIKeyDependsOnSecretAndKey(t *testing.T) {
	key, _, err := NewAPIKey([]byte(testSecret))
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}

	if HashAPIKey([]byte(testSecret), key) != HashAPIKey([]byte(testSecret), key) {
		t.Error("HashAPIKey is not deterministic")
	}
	if HashAPIKey([]byte(testSecret), key) == HashAPIKey([]byte("another-secret"), key) {
		t.Error("HashAPIKey ignores the secret")
	}
	if HashAPIKey([]byte(testSecret), key) == HashAPIKey([]byte(testSecret), key+"x") {
		t.Error("HashAPIKey ignores the key")
	}
}

func TestIsAPIKey(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "key", raw: "rqark-9Zk2Xf1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1pZ1p", want: true},
		{name: "prefix only", raw: "rqark-", want: false},
		{name: "empty", raw: "", want: false},
		{name: "access token", raw: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.x", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAPIKey(tc.raw); got != tc.want {
				t.Errorf("IsAPIKey(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestAPIKeyDisplayPrefix(t *testing.T) {
	long := strings.Repeat("a", 48)
	if got := APIKeyDisplayPrefix(long); got != long[:APIKeyDisplayLength] {
		t.Errorf("APIKeyDisplayPrefix(long) = %q, want %q", got, long[:APIKeyDisplayLength])
	}
	if got := APIKeyDisplayPrefix("rqark-abc"); got != "rqark-abc" {
		t.Errorf("APIKeyDisplayPrefix(short) = %q, want the whole key", got)
	}
}
