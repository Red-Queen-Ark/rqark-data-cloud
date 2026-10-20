package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// serveChat runs proxyChat against a recorder carrying one request, and returns
// the recorder so a test can read what the caller would see.
func serveChat(t *testing.T, apiBase, apiKey, payload string) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(payload))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Request.Header.Set("Authorization", "Bearer data-cloud-token")

	proxyChat(context, apiBase, apiKey, []byte(payload))
	return recorder
}

// TestProxyChatForwardsTheBody covers what the provider receives: the caller's
// payload with the provider credential in place of the data cloud token, and
// nothing the proxy invented.
func TestProxyChatForwardsTheBody(t *testing.T) {
	var (
		gotPath          string
		gotAuth          string
		gotContentType   string
		gotBody          map[string]any
		gotCallerHeaders string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotCallerHeaders = r.Header.Get("X-Request-Id")

		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Ratelimit-Remaining", "9")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion"}`))
	}))
	defer upstream.Close()

	recorder := serveChat(t, upstream.URL+"/v1", "sk-provider", `{"messages":[{"role":"user","content":"hi"}],"temperature":0.2}`)

	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-provider" {
		t.Errorf("Authorization = %q, want the provider key", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotCallerHeaders != "" {
		t.Errorf("the data cloud credential path leaked a caller header: %q", gotCallerHeaders)
	}
	if gotBody["temperature"] != 0.2 {
		t.Errorf("temperature = %v, want the caller's 0.2", gotBody["temperature"])
	}
	if _, leaked := gotBody["provider_id"]; leaked {
		t.Error("provider_id reached the provider")
	}

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("X-Ratelimit-Remaining"); got != "9" {
		t.Errorf("X-Ratelimit-Remaining = %q, want the provider's 9", got)
	}
	if got := recorder.Body.String(); got != `{"id":"chatcmpl-1","object":"chat.completion"}` {
		t.Errorf("body = %q, want the provider's body unchanged", got)
	}
}

// TestProxyChatRelaysUpstreamErrors covers the rule that the provider's error is
// the caller's error: a 429 is not translated into a data cloud code.
func TestProxyChatRelaysUpstreamErrors(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	}))
	defer upstream.Close()

	recorder := serveChat(t, upstream.URL, "sk-provider", `{"messages":[]}`)

	if recorder.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want the provider's 429", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "rate limited") {
		t.Errorf("body = %q, want the provider's error body", recorder.Body.String())
	}
}

// TestProxyChatReportsAnUnreachableProvider covers the one failure the proxy
// owns: a provider that cannot be dialled is a gateway failure, not the
// provider's answer.
func TestProxyChatReportsAnUnreachableProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead := upstream.URL
	upstream.Close()

	recorder := serveChat(t, dead, "sk-provider", `{"messages":[]}`)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "BAD_GATEWAY") {
		t.Errorf("body = %q, want the BAD_GATEWAY envelope", recorder.Body.String())
	}
}

// TestProxyChatStreamsWithoutBuffering covers the streaming path: what the
// provider writes and flushes reaches the caller before the provider is done.
func TestProxyChatStreamsWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n"))
		w.(http.Flusher).Flush()

		// The first event is read before this handler is let go, so a proxy that
		// held the body until the provider finished would never see it.
		<-release
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"messages":[],"stream":true}`))

	first := make(chan struct{})
	context.Writer = &streamWriter{ResponseWriter: context.Writer, first: first}

	done := make(chan struct{})
	go func() {
		proxyChat(context, upstream.URL, "sk-provider", []byte(`{"messages":[],"stream":true}`))
		close(done)
	}()

	select {
	case <-first:
	case <-time.After(5 * time.Second):
		close(release)
		<-done
		t.Fatal("the first event did not reach the caller before the provider finished")
	}
	close(release)
	<-done

	if got := recorder.Body.String(); !strings.Contains(got, "[DONE]") {
		t.Errorf("body = %q, want the whole stream", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
}

// streamWriter reports the first write, which is what tells a flushed stream
// from a buffered one.
type streamWriter struct {
	gin.ResponseWriter
	first chan struct{}
	once  sync.Once
}

func (w *streamWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.once.Do(func() { close(w.first) })
	return n, err
}
