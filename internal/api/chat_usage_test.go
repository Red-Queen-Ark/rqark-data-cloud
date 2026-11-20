package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// decodeFields turns a JSON object into the untyped body the proxy works with.
func decodeFields(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()

	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return fields
}

// recordingWriter returns a gin response writer backed by a recorder, so a test
// can read exactly the bytes the caller would receive.
func recordingWriter(t *testing.T) (gin.ResponseWriter, *httptest.ResponseRecorder) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	return context.Writer, recorder
}

// TestReadUsageTagsRemovesTheFields covers the rule that the four description
// fields, and a stray api_key_id, never reach the provider.
func TestReadUsageTagsRemovesTheFields(t *testing.T) {
	sessionID := uuid.New()
	agentID := uuid.New()
	fields := decodeFields(t, `{
		"model":"m",
		"session_id":"`+sessionID.String()+`",
		"agent_id":"`+agentID.String()+`",
		"source":"TUI",
		"is_compaction":true,
		"api_key_id":"`+uuid.NewString()+`"
	}`)

	tags := readUsageTags(fields)

	if tags.SessionID == nil || *tags.SessionID != sessionID {
		t.Errorf("SessionID = %v, want %v", tags.SessionID, sessionID)
	}
	if tags.AgentID == nil || *tags.AgentID != agentID {
		t.Errorf("AgentID = %v, want %v", tags.AgentID, agentID)
	}
	if tags.Source != "TUI" {
		t.Errorf("Source = %q, want TUI", tags.Source)
	}
	if !tags.IsCompaction {
		t.Error("IsCompaction = false, want true")
	}

	for _, name := range []string{"session_id", "agent_id", "source", "is_compaction", "api_key_id"} {
		if _, still := fields[name]; still {
			t.Errorf("%s was left in the body", name)
		}
	}
	if _, kept := fields["model"]; !kept {
		t.Error("model was dropped from the body")
	}
}

// TestReadUsageTagsTreatsBadValuesAsAbsent covers the lenient rule: a client's
// malformed tag must not fail a chat.
func TestReadUsageTagsTreatsBadValuesAsAbsent(t *testing.T) {
	fields := decodeFields(t, `{"session_id":"primary","agent_id":7,"source":{},"is_compaction":"yes"}`)

	tags := readUsageTags(fields)

	if tags.SessionID != nil {
		t.Errorf("SessionID = %v, want nil for a non-UUID", tags.SessionID)
	}
	if tags.AgentID != nil {
		t.Errorf("AgentID = %v, want nil for a non-string", tags.AgentID)
	}
	if tags.Source != "" {
		t.Errorf("Source = %q, want empty for a non-string", tags.Source)
	}
	if tags.IsCompaction {
		t.Error("IsCompaction = true, want false for a non-boolean")
	}
	if len(fields) != 0 {
		t.Errorf("fields = %v, want all of them removed", fields)
	}
}

// TestWithStreamUsageInjectsForStreams covers the server-side include_usage:
// it is added for a stream, and only for a stream.
func TestWithStreamUsageInjectsForStreams(t *testing.T) {
	fields := decodeFields(t, `{"stream":true}`)
	withStreamUsage(fields)

	var options map[string]any
	if err := json.Unmarshal(fields["stream_options"], &options); err != nil {
		t.Fatalf("decode stream_options: %v", err)
	}
	if options["include_usage"] != true {
		t.Errorf("include_usage = %v, want true", options["include_usage"])
	}

	plain := decodeFields(t, `{"stream":false}`)
	withStreamUsage(plain)
	if _, set := plain["stream_options"]; set {
		t.Error("stream_options was added to a non-streaming body")
	}
}

// TestWithStreamUsageMergesTheCallersOptions covers the rule that the caller's
// own stream_options survive: the object is merged into, never replaced.
func TestWithStreamUsageMergesTheCallersOptions(t *testing.T) {
	fields := decodeFields(t, `{"stream":true,"stream_options":{"include_obfuscation":false}}`)
	withStreamUsage(fields)

	var options map[string]any
	if err := json.Unmarshal(fields["stream_options"], &options); err != nil {
		t.Fatalf("decode stream_options: %v", err)
	}
	if options["include_usage"] != true {
		t.Errorf("include_usage = %v, want true", options["include_usage"])
	}
	if options["include_obfuscation"] != false {
		t.Errorf("include_obfuscation = %v, want the caller's false", options["include_obfuscation"])
	}
}

// TestWithStreamUsageForcesUsageOn covers the forced rule: a caller cannot turn
// usage off, because the record is the server's, not the caller's.
func TestWithStreamUsageForcesUsageOn(t *testing.T) {
	fields := decodeFields(t, `{"stream":true,"stream_options":{"include_usage":false}}`)
	withStreamUsage(fields)

	var options map[string]any
	if err := json.Unmarshal(fields["stream_options"], &options); err != nil {
		t.Fatalf("decode stream_options: %v", err)
	}
	if options["include_usage"] != true {
		t.Errorf("include_usage = %v, want true", options["include_usage"])
	}
}

// TestChatUsageTotalsAreNotDoubleCounted covers the arithmetic: cached and
// reasoning are detail counters, and the total is the provider's own number.
func TestChatUsageTotalsAreNotDoubleCounted(t *testing.T) {
	usage := &chatUsage{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
	}
	if got := usage.total(); got != 120 {
		t.Errorf("total = %d, want the provider's 120", got)
	}

	fallback := &chatUsage{PromptTokens: 100, CompletionTokens: 20}
	if got := fallback.total(); got != 120 {
		t.Errorf("total = %d, want prompt+completion 120", got)
	}
}

// TestChatUsageReadsTheDetailObjects covers the nested cached and reasoning
// counts, and their zero values when the provider omits them.
func TestChatUsageReadsTheDetailObjects(t *testing.T) {
	usage := &chatUsage{}
	if err := json.Unmarshal([]byte(`{
		"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,
		"prompt_tokens_details":{"cached_tokens":80},
		"completion_tokens_details":{"reasoning_tokens":5}
	}`), usage); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if usage.cached() != 80 {
		t.Errorf("cached = %d, want 80", usage.cached())
	}
	if usage.reasoning() != 5 {
		t.Errorf("reasoning = %d, want 5", usage.reasoning())
	}

	bare := &chatUsage{}
	if bare.cached() != 0 || bare.reasoning() != 0 {
		t.Errorf("absent details = %d, %d, want 0, 0", bare.cached(), bare.reasoning())
	}
}

// TestSSECollectorKeepsTheLastUsage covers the streaming parse: only the last
// usage object is kept, and a stream split across reads is reassembled.
func TestSSECollectorKeepsTheLastUsage(t *testing.T) {
	stream := "" +
		": keepalive\n\n" +
		"event: message\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n" +
		"data: [DONE]\n\n"

	collector := &sseUsageCollector{}
	// Feed it in awkward slices so that lines and JSON objects are split.
	for i := 0; i < len(stream); i += 7 {
		end := i + 7
		if end > len(stream) {
			end = len(stream)
		}
		collector.feed([]byte(stream[i:end]))
	}
	collector.endOfStream()

	if collector.last == nil {
		t.Fatal("no usage was collected")
	}
	if collector.last.PromptTokens != 10 || collector.last.CompletionTokens != 2 {
		t.Errorf("usage = %+v, want prompt 10 completion 2", collector.last)
	}
}

// TestSSECollectorHandlesCRLFAndTrailingEvent covers CRLF line endings and a
// stream whose last event is not followed by a blank line.
func TestSSECollectorHandlesCRLFAndTrailingEvent(t *testing.T) {
	collector := &sseUsageCollector{}
	collector.feed([]byte("data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\r\n"))
	collector.endOfStream()

	if collector.last == nil || collector.last.PromptTokens != 3 {
		t.Fatalf("usage = %+v, want prompt 3", collector.last)
	}
}

// TestSSECollectorReadsMultiLineData covers an event whose data field is spread
// over several lines, which SSE joins with a newline.
func TestSSECollectorReadsMultiLineData(t *testing.T) {
	collector := &sseUsageCollector{}
	collector.feed([]byte("data: {\"usage\":{\"prompt_tokens\":5,\n"))
	collector.feed([]byte("data: \"completion_tokens\":1,\"total_tokens\":6}}\n\n"))
	collector.endOfStream()

	if collector.last == nil || collector.last.PromptTokens != 5 {
		t.Fatalf("usage = %+v, want prompt 5", collector.last)
	}
}

// TestUsageRecordCarriesTheKeyOnlyWhenOneAuthenticated covers the attribution
// rule: a key id is recorded only for a key-authenticated call.
func TestUsageRecordCarriesTheKeyOnlyWhenOneAuthenticated(t *testing.T) {
	sessionID := uuid.New()
	tags := usageTags{SessionID: &sessionID, Source: "TUI", IsCompaction: true}
	usage := &chatUsage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}
	userID, providerID, keyID := uuid.New(), uuid.New(), uuid.New()

	withKey := usageRecord(userID, providerID, tags, "m", usage, keyID, true)
	if withKey.APIKeyID == nil || *withKey.APIKeyID != keyID {
		t.Errorf("APIKeyID = %v, want %v", withKey.APIKeyID, keyID)
	}
	if !withKey.IsCompaction || withKey.Source != "TUI" || withKey.TotalTokens != 12 {
		t.Errorf("record = %+v, want the tags and the total carried through", withKey)
	}

	withoutKey := usageRecord(userID, providerID, tags, "m", usage, keyID, false)
	if withoutKey.APIKeyID != nil {
		t.Errorf("APIKeyID = %v, want nil for a token-authenticated call", withoutKey.APIKeyID)
	}
}

// TestRelayCollectingBodyKeepsTheBytesAndTheUsage covers the non-streaming
// path: the caller receives the provider's body unchanged and the usage is
// returned.
func TestRelayCollectingBodyKeepsTheBytesAndTheUsage(t *testing.T) {
	body := `{"id":"c1","usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`
	writer, recorder := recordingWriter(t)

	usage := relayCollectingBody(writer, strings.NewReader(body))

	if recorder.Body.String() != body {
		t.Errorf("relayed %q, want the body unchanged %q", recorder.Body.String(), body)
	}
	if usage == nil || usage.PromptTokens != 7 || usage.total() != 10 {
		t.Fatalf("usage = %+v, want prompt 7 total 10", usage)
	}
}

// TestRelayCollectingBodyWithoutUsage covers a provider that returns a
// completion with no usage object: nothing is returned, and the body still
// reaches the caller.
func TestRelayCollectingBodyWithoutUsage(t *testing.T) {
	body := `{"id":"c1"}`
	writer, recorder := recordingWriter(t)

	usage := relayCollectingBody(writer, strings.NewReader(body))

	if usage != nil {
		t.Errorf("usage = %+v, want nil", usage)
	}
	if recorder.Body.String() != body {
		t.Errorf("relayed %q, want %q", recorder.Body.String(), body)
	}
}

// TestRelayCollectingStreamKeepsTheBytesAndTheUsage covers the streaming path:
// the bytes are relayed exactly and only the usage object is extracted.
func TestRelayCollectingStreamKeepsTheBytesAndTheUsage(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1,\"total_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	writer, recorder := recordingWriter(t)

	usage := relayCollectingStream(writer, strings.NewReader(stream))

	if recorder.Body.String() != stream {
		t.Errorf("relayed %q, want the stream byte for byte", recorder.Body.String())
	}
	if usage == nil || usage.PromptTokens != 4 {
		t.Fatalf("usage = %+v, want prompt 4", usage)
	}
}

// TestProxyChatReportsUsageOnlyOnSuccess covers the rule that an error response
// is relayed but never reported as usage.
func TestProxyChatReportsUsageOnlyOnSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"},"usage":{"prompt_tokens":9,"completion_tokens":9,"total_tokens":18}}`))
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"messages":[]}`))

	usage, recorded := proxyChat(context, chatProxyTarget{APIBase: upstream.URL, APIKey: "sk-provider"}, []byte(`{"messages":[]}`), false)

	if recorded {
		t.Error("recorded = true, want false for a provider error")
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil", usage)
	}
	if !strings.Contains(recorder.Body.String(), "rate limited") {
		t.Errorf("body = %q, want the provider's error relayed", recorder.Body.String())
	}
}
