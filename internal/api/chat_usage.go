package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
)

// chatUsage is the upstream usage object of an OpenAI-compatible completion.
// Only the fields the table stores are read; a provider that sends more is not
// a problem.
type chatUsage struct {
	PromptTokens           int                        `json:"prompt_tokens"`
	CompletionTokens       int                        `json:"completion_tokens"`
	TotalTokens            int                        `json:"total_tokens"`
	PromptTokensDetails    *chatUsagePromptDetail     `json:"prompt_tokens_details"`
	CompletionTokensDetail *chatUsageCompletionDetail `json:"completion_tokens_details"`
}

// chatUsagePromptDetail is the prompt detail object, whose cached count is a
// subset of prompt_tokens.
type chatUsagePromptDetail struct {
	CachedTokens int `json:"cached_tokens"`
}

// chatUsageCompletionDetail is the completion detail object, whose reasoning
// count is a subset of completion_tokens.
type chatUsageCompletionDetail struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// cached is the reported cached-prompt count. It is a subset of prompt_tokens
// and is never added to the total.
func (u *chatUsage) cached() int {
	if u.PromptTokensDetails == nil {
		return 0
	}
	return u.PromptTokensDetails.CachedTokens
}

// reasoning is the reported reasoning count. It is a subset of
// completion_tokens and is never added to the total.
func (u *chatUsage) reasoning() int {
	if u.CompletionTokensDetail == nil {
		return 0
	}
	return u.CompletionTokensDetail.ReasoningTokens
}

// total is the provider's own total, falling back to prompt+completion when the
// provider omits it.
func (u *chatUsage) total() int {
	if u.TotalTokens != 0 {
		return u.TotalTokens
	}
	return u.PromptTokens + u.CompletionTokens
}

// usageTags are the four body fields that describe a call rather than ask it.
// They are read and removed before the body reaches the provider.
type usageTags struct {
	SessionID    *uuid.UUID
	AgentID      *uuid.UUID
	Source       string
	IsCompaction bool
}

// readUsageTags pulls the usage fields out of the body and removes them, so
// that none of them is forwarded upstream. A field of the wrong type is treated
// as absent: a malformed tag is the client's mistake and must not fail a chat.
//
// api_key_id is removed too but never read: key attribution comes from the
// middleware, and a body value must not be able to claim another key.
func readUsageTags(fields map[string]json.RawMessage) usageTags {
	var tags usageTags

	tags.SessionID = takeUUIDField(fields, "session_id")
	tags.AgentID = takeUUIDField(fields, "agent_id")
	tags.Source = takeStringField(fields, "source")
	tags.IsCompaction = takeBoolField(fields, "is_compaction")
	delete(fields, "api_key_id")

	return tags
}

// takeUUIDField reads and removes a uuid field.
func takeUUIDField(fields map[string]json.RawMessage, name string) *uuid.UUID {
	raw, ok := fields[name]
	if !ok {
		return nil
	}
	delete(fields, name)

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		log.Printf("api: %s is not a string, ignoring it", name)
		return nil
	}
	id, err := uuid.Parse(text)
	if err != nil {
		log.Printf("api: %s is not a UUID, ignoring it", name)
		return nil
	}
	return &id
}

// takeStringField reads and removes a string field.
func takeStringField(fields map[string]json.RawMessage, name string) string {
	raw, ok := fields[name]
	if !ok {
		return ""
	}
	delete(fields, name)

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		log.Printf("api: %s is not a string, ignoring it", name)
		return ""
	}
	return text
}

// takeBoolField reads and removes a boolean field.
func takeBoolField(fields map[string]json.RawMessage, name string) bool {
	raw, ok := fields[name]
	if !ok {
		return false
	}
	delete(fields, name)

	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		log.Printf("api: %s is not a boolean, ignoring it", name)
		return false
	}
	return value
}

// bodyStream reports whether the body asks for a server-sent event stream.
func bodyStream(fields map[string]json.RawMessage) bool {
	raw, ok := fields["stream"]
	if !ok {
		return false
	}
	var stream bool
	if err := json.Unmarshal(raw, &stream); err != nil {
		return false
	}
	return stream
}

// bodyModel reads the model after withModel has filled it in.
func bodyModel(fields map[string]json.RawMessage) string {
	var model string
	if raw, ok := fields["model"]; ok {
		_ = json.Unmarshal(raw, &model)
	}
	return model
}

// withStreamUsage asks the provider to include a usage object on a streamed
// response. An OpenAI-compatible stream omits usage unless the request carries
// stream_options.include_usage, and the server adds it so that no client has to.
//
// It merges into whatever stream_options the caller already sent rather than
// replacing the object, and does nothing when the call is not a stream or when
// the deployment has turned injection off for an upstream that rejects the
// parameter.
func withStreamUsage(fields map[string]json.RawMessage) {
	if !config.UsageIncludeStreamUsage {
		return
	}
	if !bodyStream(fields) {
		return
	}

	options := map[string]any{}
	if raw, ok := fields["stream_options"]; ok {
		// A non-object is replaced rather than merged: the caller sent something
		// that cannot be merged with.
		if err := json.Unmarshal(raw, &options); err != nil || options == nil {
			options = map[string]any{}
		}
	}
	if enabled, ok := options["include_usage"].(bool); ok && enabled {
		return
	}
	options["include_usage"] = true

	encoded, err := json.Marshal(options)
	if err != nil {
		return
	}
	fields["stream_options"] = encoded
}

// usagePollSize is how much of a non-streaming response body is kept for
// parsing. A body past this size is relayed but not recorded.
const usagePollSize = 4 << 20

// relayCollectingBody relays a whole (non-streaming) response and returns the
// usage it carried. The bytes the caller receives are untouched: the body is
// copied as it is written, not re-encoded.
func relayCollectingBody(w gin.ResponseWriter, body io.Reader) *chatUsage {
	buffer := make([]byte, 32*1024)
	var polled bytes.Buffer
	overflow := false

	for {
		n, err := body.Read(buffer)
		if n > 0 {
			if !overflow {
				if polled.Len()+n > usagePollSize {
					overflow = true
					polled.Reset()
				} else {
					_, _ = polled.Write(buffer[:n])
				}
			}
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return nil
			}
			w.Flush()
		}
		if err != nil {
			break
		}
	}

	if overflow {
		log.Printf("api: response body exceeded %d bytes, usage not recorded", usagePollSize)
		return nil
	}

	var envelope struct {
		Usage *chatUsage `json:"usage"`
	}
	if err := json.Unmarshal(polled.Bytes(), &envelope); err != nil {
		log.Printf("api: response body is not JSON, usage not recorded: %v", err)
		return nil
	}
	return envelope.Usage
}

// relayCollectingStream relays a server-sent event stream and returns the last
// usage object it carried. The caller's bytes are relayed exactly as read. Only
// the event being assembled is held in memory, so the footprint does not grow
// with the length of the stream.
func relayCollectingStream(w gin.ResponseWriter, body io.Reader) *chatUsage {
	collector := &sseUsageCollector{}
	buffer := make([]byte, 32*1024)

	for {
		n, err := body.Read(buffer)
		if n > 0 {
			collector.feed(buffer[:n])
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return nil
			}
			w.Flush()
		}
		if err != nil {
			break
		}
	}
	collector.endOfStream()
	return collector.last
}

// sseEventLimit bounds one event's data. A provider that sends a single event
// larger than this is not one whose usage is worth unbounded memory.
const sseEventLimit = 1 << 20

// sseUsageCollector reassembles an SSE stream line by line and keeps the last
// usage object. It handles comments, event names, multi-line data fields, CRLF
// and a JSON object split across reads.
type sseUsageCollector struct {
	line     []byte
	data     []byte
	last     *chatUsage
	skipping bool
	dropLine bool
}

// feed consumes one read's worth of bytes.
func (s *sseUsageCollector) feed(chunk []byte) {
	start := 0
	for i, b := range chunk {
		if b != '\n' {
			continue
		}
		s.appendLine(chunk[start:i])
		s.endLine()
		start = i + 1
	}
	if start < len(chunk) {
		s.appendLine(chunk[start:])
	}
}

// appendLine adds bytes to the line being assembled, bounded.
func (s *sseUsageCollector) appendLine(part []byte) {
	if s.dropLine || len(part) == 0 {
		return
	}
	if len(s.line)+len(part) > sseEventLimit {
		s.dropLine = true
		s.line = s.line[:0]
		return
	}
	s.line = append(s.line, part...)
}

// endLine processes one completed line.
func (s *sseUsageCollector) endLine() {
	line := s.line
	s.line = s.line[:0]

	if s.dropLine {
		s.dropLine = false
		s.skipping = true
		s.data = s.data[:0]
		return
	}
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}

	// A blank line ends the event.
	if len(line) == 0 {
		s.endEvent()
		return
	}
	// A comment, or a field this collector does not read (event:, id:, retry:).
	if line[0] == ':' || !bytes.HasPrefix(line, []byte("data:")) {
		return
	}

	value := line[len("data:"):]
	value = bytes.TrimPrefix(value, []byte(" "))

	if len(s.data)+len(value)+1 > sseEventLimit {
		s.skipping = true
		s.data = s.data[:0]
		return
	}
	if len(s.data) > 0 {
		s.data = append(s.data, '\n')
	}
	s.data = append(s.data, value...)
}

// endEvent parses the event that just finished, keeping its usage if it has
// one.
func (s *sseUsageCollector) endEvent() {
	data := s.data
	s.data = s.data[:0]
	skipping := s.skipping
	s.skipping = false

	if skipping || len(data) == 0 {
		return
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		return
	}

	var envelope struct {
		Usage *chatUsage `json:"usage"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		// A chunk that is not JSON is not fatal: the stream is relayed either
		// way, and a provider may send keepalives this collector ignores.
		return
	}
	if envelope.Usage != nil {
		s.last = envelope.Usage
	}
}

// endOfStream flushes an event that was not terminated by a blank line.
func (s *sseUsageCollector) endOfStream() {
	if len(s.line) > 0 {
		s.endLine()
	}
	s.endEvent()
}
