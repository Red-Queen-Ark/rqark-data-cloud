package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestChatUpstreamURL covers the one join between a provider's api_base and the
// chat path: a trailing slash cannot double the separator, and a version prefix
// already on api_base is left alone.
func TestChatUpstreamURL(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com/v1":  "https://api.deepseek.com/v1/chat/completions",
		"https://api.deepseek.com/v1/": "https://api.deepseek.com/v1/chat/completions",
		"https://api.openai.com":       "https://api.openai.com/chat/completions",
		"http://127.0.0.1:11434/v1":    "http://127.0.0.1:11434/v1/chat/completions",
	}
	for apiBase, want := range cases {
		if got := chatUpstreamURL(apiBase); got != want {
			t.Errorf("chatUpstreamURL(%q) = %q, want %q", apiBase, got, want)
		}
	}
}

// TestReadProviderID covers what the proxy takes out of the body: the field
// never reaches the provider, and a field that is missing or not a UUID is
// reported as absent.
func TestReadProviderID(t *testing.T) {
	id := uuid.New()
	t.Run("removes the field", func(t *testing.T) {
		fields := map[string]json.RawMessage{"provider_id": json.RawMessage(`"` + id.String() + `"`), "model": json.RawMessage(`"m"`)}
		got, ok := readProviderID(fields)
		if !ok || got != id {
			t.Fatalf("readProviderID = %v, %v, want %v, true", got, ok, id)
		}
		if _, still := fields["provider_id"]; still {
			t.Error("provider_id was left in the body")
		}
		if _, kept := fields["model"]; !kept {
			t.Error("model was dropped from the body")
		}
	})
	t.Run("missing", func(t *testing.T) {
		if _, ok := readProviderID(map[string]json.RawMessage{}); ok {
			t.Error("a missing provider_id was accepted")
		}
	})
	t.Run("not a uuid", func(t *testing.T) {
		fields := map[string]json.RawMessage{"provider_id": json.RawMessage(`"primary"`)}
		if _, ok := readProviderID(fields); ok {
			t.Error("a provider_id that is not a UUID was accepted")
		}
	})
}

// TestWithModel covers the model fallback: the provider's model_name is only a
// default, never an override.
func TestWithModel(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "absent is filled", body: `{"messages":[]}`, want: "provider-model"},
		{name: "empty is filled", body: `{"model":""}`, want: "provider-model"},
		{name: "given is kept", body: `{"model":"caller-model"}`, want: "caller-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]json.RawMessage{}
			if err := json.Unmarshal([]byte(tc.body), &fields); err != nil {
				t.Fatalf("decode %s: %v", tc.body, err)
			}
			fields = withModel(fields, "provider-model")

			var got string
			if err := json.Unmarshal(fields["model"], &got); err != nil {
				t.Fatalf("model is not a string: %v", err)
			}
			if got != tc.want {
				t.Errorf("model = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCopyRequestHeaders covers what the provider is told: the caller's
// credentials and the connection-scoped headers stay behind, and everything
// else is carried over.
func TestCopyRequestHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Authorization", "Bearer data-cloud-token")
	src.Set("Connection", "keep-alive")
	src.Set("Proxy-Authorization", "Basic abc")
	src.Set("Accept-Encoding", "gzip")
	src.Set("User-Agent", "curl/8.7.1")
	src.Set("Content-Type", "application/json")
	src.Set("OpenAI-Beta", "assistants=v2")

	dst := http.Header{}
	copyRequestHeaders(dst, src)

	for _, name := range []string{"Authorization", "Connection", "Proxy-Authorization", "Accept-Encoding", "User-Agent"} {
		if got := dst.Get(name); got != "" {
			t.Errorf("%s reached the provider as %q", name, got)
		}
	}
	for name, want := range map[string]string{"Content-Type": "application/json", "OpenAI-Beta": "assistants=v2"} {
		if got := dst.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestCopyResponseHeaders covers what the caller is told: the body crosses a
// fresh connection, so the provider's connection-scoped framing is dropped and
// what it says about the body is kept.
func TestCopyResponseHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Transfer-Encoding", "chunked")
	src.Set("Content-Type", "text/event-stream")
	src.Set("X-Ratelimit-Remaining", "9")

	dst := http.Header{}
	copyResponseHeaders(dst, src)

	if got := dst.Get("Transfer-Encoding"); got != "" {
		t.Errorf("Transfer-Encoding reached the caller as %q", got)
	}
	for name, want := range map[string]string{"Content-Type": "text/event-stream", "X-Ratelimit-Remaining": "9"} {
		if got := dst.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
