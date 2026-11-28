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
	"github.com/google/uuid"
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

	_, _ = proxyChat(context, chatProxyTarget{APIBase: apiBase, APIKey: apiKey}, []byte(payload), false)
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
		gotUserAgent     string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotCallerHeaders = r.Header.Get("X-Request-Id")
		gotUserAgent = r.Header.Get("User-Agent")

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
	if gotUserAgent != upstreamUserAgent() {
		t.Errorf("User-Agent = %q, want %q", gotUserAgent, upstreamUserAgent())
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
		_, _ = proxyChat(context, chatProxyTarget{APIBase: upstream.URL, APIKey: "sk-provider"}, []byte(`{"messages":[],"stream":true}`), true)
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

// TestProxyChatKeepsASilentStreamAlive covers the slow model: while the
// provider says nothing, the relay writes keepalives of its own, so a reverse
// proxy timing the response out on the gap between bytes has something to read.
// The keepalives are comments, so the events around them are untouched.
func TestProxyChatKeepsASilentStreamAlive(t *testing.T) {
	previous := sseHeartbeatInterval
	sseHeartbeatInterval = 20 * time.Millisecond
	t.Cleanup(func() { sseHeartbeatInterval = previous })

	silent := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n"))
		w.(http.Flusher).Flush()

		<-silent

		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"messages":[],"stream":true}`))

	done := make(chan struct{})
	go func() {
		_, _ = proxyChat(context, chatProxyTarget{APIBase: upstream.URL, APIKey: "sk-provider"}, []byte(`{"messages":[],"stream":true}`), true)
		close(done)
	}()

	// Long enough for several ticks, so the assertion is about a stream that
	// stayed open rather than about one lucky write.
	time.Sleep(150 * time.Millisecond)
	close(silent)
	<-done

	body := recorder.Body.String()
	if !strings.Contains(body, sseHeartbeat) {
		t.Fatalf("body = %q, want a keepalive while the provider was silent", body)
	}
	if strings.Index(body, sseHeartbeat) > strings.Index(body, "[DONE]") {
		t.Errorf("body = %q, want the keepalive before the provider resumed", body)
	}
	// The events still parse: the keepalives sit between them, not inside one.
	if !strings.Contains(body, "{\"delta\":{\"content\":\"he\"}}") {
		t.Errorf("body = %q, want the first event intact", body)
	}
}

// TestProxyChatNeverSplitsALineWithAKeepalive covers the one place a keepalive
// would do harm: a provider that stalls halfway through a line. A comment
// written there would split the provider's bytes in two, so the relay waits for
// the line to finish instead.
func TestProxyChatNeverSplitsALineWithAKeepalive(t *testing.T) {
	previous := sseHeartbeatInterval
	sseHeartbeatInterval = 20 * time.Millisecond
	t.Cleanup(func() { sseHeartbeatInterval = previous })

	finish := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Half a line, then silence.
		_, _ = w.Write([]byte("data: {\"choices\""))
		w.(http.Flusher).Flush()

		<-finish

		_, _ = w.Write([]byte(":[]}\n\ndata: [DONE]\n\n"))
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"messages":[],"stream":true}`))

	done := make(chan struct{})
	go func() {
		_, _ = proxyChat(context, chatProxyTarget{APIBase: upstream.URL, APIKey: "sk-provider"}, []byte(`{"messages":[],"stream":true}`), true)
		close(done)
	}()

	time.Sleep(150 * time.Millisecond)
	close(finish)
	<-done

	if !strings.Contains(recorder.Body.String(), "data: {\"choices\":[]}") {
		t.Fatalf("body = %q, want the provider's line to arrive whole", recorder.Body.String())
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

// TestProxyChatRendersStoredHeaders covers the stored headers a provider owner
// sets: they reach the upstream, and a ${name} placeholder is filled from the
// call rather than sent as written. The OpenCode Go session header is only one
// use of that: the proxy does not know the header by name.
func TestProxyChatRendersStoredHeaders(t *testing.T) {
	sessionID := uuid.New()
	var (
		gotSession string
		gotVersion string
		gotStatic  string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession = r.Header.Get("x-opencode-session")
		gotVersion = r.Header.Get("X-Api-Version")
		gotStatic = r.Header.Get("X-Static")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"messages":[]}`))

	_, _ = proxyChat(context, chatProxyTarget{
		APIBase: upstream.URL,
		APIKey:  "sk-provider",
		Headers: map[string]string{
			"x-opencode-session": "${session_id}",
			"X-Api-Version":      "2024-06-01",
			"X-Static":           "plain",
		},
		Vars: map[string]string{"session_id": sessionID.String()},
	}, []byte(`{"messages":[]}`), false)

	if gotSession != sessionID.String() {
		t.Errorf("x-opencode-session = %q, want %q", gotSession, sessionID)
	}
	if gotVersion != "2024-06-01" {
		t.Errorf("X-Api-Version = %q, want the stored value", gotVersion)
	}
	if gotStatic != "plain" {
		t.Errorf("X-Static = %q, want the stored value", gotStatic)
	}
}

// TestProxyChatDropsAHeaderWhoseValueTheCallDoesNotHave covers a call that
// belongs to no session: the header is left off rather than sent empty or with
// the placeholder still in it.
func TestProxyChatDropsAHeaderWhoseValueTheCallDoesNotHave(t *testing.T) {
	var gotSession string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession = r.Header.Get("x-opencode-session")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"messages":[]}`))

	_, _ = proxyChat(context, chatProxyTarget{
		APIBase: upstream.URL,
		APIKey:  "sk-provider",
		Headers: map[string]string{"x-opencode-session": "${session_id}"},
		Vars:    map[string]string{},
	}, []byte(`{"messages":[]}`), false)

	if gotSession != "" {
		t.Errorf("x-opencode-session = %q, want it absent", gotSession)
	}
}

// TestProxyChatKeepsItsOwnHeadersOverTheStoredOnes covers the boundary: a stored
// header cannot replace the provider credential or the identity the proxy sends
// as, and a connection-scoped header never reaches the upstream.
func TestProxyChatKeepsItsOwnHeadersOverTheStoredOnes(t *testing.T) {
	var (
		gotAuth       string
		gotUserAgent  string
		gotConnection string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUserAgent = r.Header.Get("User-Agent")
		gotConnection = r.Header.Get("Connection")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"messages":[]}`))

	_, _ = proxyChat(context, chatProxyTarget{
		APIBase: upstream.URL,
		APIKey:  "sk-provider",
		Headers: map[string]string{
			"Authorization": "Bearer from-a-stored-header",
			"User-Agent":    "stored-agent",
			"Connection":    "close",
		},
	}, []byte(`{"messages":[]}`), false)

	if gotAuth != "Bearer sk-provider" {
		t.Errorf("Authorization = %q, want the provider key", gotAuth)
	}
	if gotUserAgent != upstreamUserAgent() {
		t.Errorf("User-Agent = %q, want %q", gotUserAgent, upstreamUserAgent())
	}
	if gotConnection != "" {
		t.Errorf("Connection = %q, want it dropped", gotConnection)
	}
}

// TestUserAgentVersionTurnsABuildIntoAToken covers the product version the
// upstream sees: the release tag loses its leading `v`, a short commit stays a
// valid token, and a build with no version names itself dev rather than leaving
// the product version empty.
func TestUserAgentVersionTurnsABuildIntoAToken(t *testing.T) {
	cases := map[string]string{
		"v0.9.1":         "0.9.1",
		"0.9.1":          "0.9.1",
		" v0.9.1 ":       "0.9.1",
		"v0.9.1-rc1":     "0.9.1-rc1",
		"574ada6":        "574ada6",
		"feat/provider":  "featprovider",
		"v0.9.1+dirty":   "0.9.1+dirty",
		"dev":            "dev",
		"":               "dev",
		"   ":            "dev",
		"v":              "dev",
		"v/rqark (beta)": "rqarkbeta",
	}
	for raw, want := range cases {
		if got := userAgentVersion(raw); got != want {
			t.Errorf("userAgentVersion(%q) = %q, want %q", raw, got, want)
		}
	}

	if got := upstreamUserAgent(); !strings.HasPrefix(got, "rqark/") {
		t.Errorf("upstreamUserAgent() = %q, want it to start with rqark/", got)
	}
}
