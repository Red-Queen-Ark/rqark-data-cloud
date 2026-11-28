package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rqark-data-cloud/internal/middlewares"
	"rqark-data-cloud/internal/service"
	"rqark-data-cloud/internal/utils"
	"rqark-data-cloud/internal/version"
)

// ChatRequest is the body of POST /api/chat: the body of an OpenAI
// POST /v1/chat/completions request, plus the provider the call goes through.
// Every field other than provider_id is forwarded to the provider as it
// arrives, so this type documents the two fields the proxy reads rather than
// the whole schema.
type ChatRequest struct {
	// ProviderID names the provider whose credential and api_base the call uses.
	ProviderID uuid.UUID `json:"provider_id" example:"7d1e4c2a-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	// Model is forwarded as it arrives. When it is absent or empty, the
	// provider's own model_name is sent instead.
	Model string `json:"model" example:"deepseek-v4-pro"`
	// Messages is required and forwarded unchanged.
	Messages []map[string]any `json:"messages"`
	// Stream asks the provider for a server-sent event stream. The events are
	// relayed as they arrive.
	Stream bool `json:"stream" example:"true"`
	// Tools, StreamOptions and ReasoningEffort are forwarded unchanged.
	Tools           []map[string]any `json:"tools,omitempty"`
	StreamOptions   map[string]any   `json:"stream_options,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty" example:"high"`

	// The four fields below describe the call rather than ask it. They are read
	// here and never forwarded to the provider; all are optional and a malformed
	// value is ignored rather than rejected.
	//
	// SessionID is the session the call belongs to, for usage attribution.
	SessionID *uuid.UUID `json:"session_id,omitempty" example:"7d1e4c2a-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	// AgentID is the agent that issued the call, for usage attribution.
	AgentID *uuid.UUID `json:"agent_id,omitempty" example:"8e2f5d3b-2b2b-4b2b-9b2b-2b2b2b2b2b2b"`
	// Source is the caller's own label for itself, such as TUI or AGENT_CLOUD.
	// It is stored as reported and never reconciled against the session.
	Source string `json:"source,omitempty" example:"TUI"`
	// IsCompaction marks a call that summarised the conversation to shrink the
	// context. It is reported by the caller and defaults to false.
	IsCompaction bool `json:"is_compaction,omitempty" example:"false"`
}

// chatUpstreamPath is the path a chat completion is requested on. It follows
// the provider's api_base, which carries whatever version prefix that provider
// needs.
const chatUpstreamPath = "/chat/completions"

// chatUpstreamTimeout bounds the wait for the provider's response headers. It is
// not an overall deadline: a response has to stay open while it streams, so the
// body is bounded by the connection alone, and by the caller going away.
const chatUpstreamTimeout = 10 * time.Minute

// chatClient talks to the providers. The transport is a clone of the default
// one so that HTTP_PROXY and the connection pool settings still apply, with the
// response header timeout raised for models that think before they answer.
var chatClient = &http.Client{
	Transport: func() *http.Transport {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		transport.TLSHandshakeTimeout = 10 * time.Second
		transport.ResponseHeaderTimeout = chatUpstreamTimeout
		return transport
	}(),
}

// hopByHopHeaders are the connection-scoped headers RFC 9110 forbids a proxy
// from forwarding in either direction.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// Chat proxies one chat completion to the provider the body names. The
// provider's credential is added here and never leaves the service: the caller
// holds a data cloud token, not the provider key.
//
//	@Summary		Chat through a stored provider
//	@Description	Forwards an OpenAI chat completion to the provider named by provider_id and relays the provider's response as it arrives, including a server-sent event stream when stream is true. The provider's api_key is added server-side, the call carries `User-Agent: rqark/<version>` with the build's version, and the provider's stored headers are sent with their `${name}` placeholders filled from the call. The provider's status, headers and body are returned unchanged, so an upstream 401 or 429 reaches the caller as it was sent. While a streaming provider is silent the response carries SSE comment lines (`: keepalive`), which a parser ignores: they exist so a reverse proxy in front of this service does not time a slow model out on the gap between bytes.
//	@Tags			chat
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		ChatRequest	true	"OpenAI chat completion body, plus provider_id"
//	@Success		200		{object}	map[string]any
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		502		{object}	utils.ErrorResponse
//	@Router			/chat [post]
func Chat(c *gin.Context) {
	fields, ok := chatBody(c)
	if !ok {
		return
	}

	if _, ok := fields["provider_id"]; !ok {
		abortBadRequest(c, "provider_id is required.")
		return
	}
	providerID, ok := readProviderID(fields)
	if !ok {
		abortBadRequest(c, "provider_id must be a UUID.")
		return
	}

	forwardChatCompletion(c, providerID, fields)
}

// ChatByProvider proxies one chat completion to the provider the path names. The
// body is a plain OpenAI request: the provider is where an OpenAI client would
// put its base_url, so a caller can point the SDK at /api/chat/{uuid} and change
// nothing else.
//
//	@Summary		Chat through a stored provider, addressed by path
//	@Description	Same as POST /chat, except the provider is named by the path and the body carries no provider_id. It exists so an OpenAI client can reach the proxy by setting base_url to /api/chat/{uuid} and its api_key to a data cloud token. A streamed answer carries the same SSE comment keepalives as POST /chat while the provider is silent.
//	@Tags			chat
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			uuid	path		string			true	"Provider ID"
//	@Param			request	body		map[string]any	true	"OpenAI chat completion body"
//	@Success		200		{object}	map[string]any
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		502		{object}	utils.ErrorResponse
//	@Router			/chat/{uuid}/chat/completions [post]
func ChatByProvider(c *gin.Context) {
	providerID, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	fields, ok := chatBody(c)
	if !ok {
		return
	}
	// A body sent to this path carries no provider_id, but one that does is not
	// an error: it is dropped rather than forwarded, like any other unknown key.
	delete(fields, "provider_id")

	forwardChatCompletion(c, providerID, fields)
}

// chatBody reads the request body as a JSON object of unparsed fields, so that
// everything the proxy does not itself read reaches the provider as it arrived.
func chatBody(c *gin.Context) (map[string]json.RawMessage, bool) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		abortBadRequest(c, "The request body could not be read.")
		return nil, false
	}

	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &fields); err != nil {
		abortBadRequest(c, "The request body must be a JSON object.")
		return nil, false
	}
	return fields, true
}

// storeUsage records one call's token usage. It is a variable so that a test
// can observe what the handler decided without a database, and so that the
// write path stays entirely in the service layer.
var storeUsage = service.RecordUsage

// forwardChatCompletion resolves the provider, fills in the model the caller left
// out and hands the call to the proxy. It is the part the two chat routes share.
func forwardChatCompletion(c *gin.Context, providerID uuid.UUID, fields map[string]json.RawMessage) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication is required.")
		return
	}
	// The usage tags are read and removed before anything else touches the body:
	// they describe the call, and the provider must never see them.
	tags := readUsageTags(fields)
	tags = withRunAttribution(c, tags)

	if _, ok := fields["messages"]; !ok {
		abortBadRequest(c, "messages is required.")
		return
	}

	view, err := service.GetProvider(c.Request.Context(), actor.ID, providerID)
	if err != nil {
		abortError(c, err)
		return
	}

	fields = withModel(fields, view.ModelName)
	withStreamUsage(fields)

	stream := bodyStream(fields)
	model := bodyModel(fields)

	payload, err := json.Marshal(fields)
	if err != nil {
		abortInternal(c, err)
		return
	}

	usage, recorded := proxyChat(c, chatProxyTarget{
		APIBase: view.APIBase,
		APIKey:  view.APIKey,
		Headers: view.Headers,
		Vars:    headerVariables(actor.ID, providerID, model, tags),
	}, payload, stream)
	if !recorded || usage == nil {
		return
	}

	keyID, hasKey := middlewares.CurrentAPIKeyID(c)
	record := usageRecord(actor.ID, providerID, tags, model, usage, keyID, hasKey)

	// The context is the request's only for what it carries. The service detaches
	// its cancellation, so a stream that ended after the client hung up is still
	// written.
	storeUsage(c.Request.Context(), record)
}

// withRunAttribution fills in what a run-scoped caller cannot say itself. A run
// token is bound to one session, so the usage row belongs to that session
// whatever the body claims, and the run labels itself TASK. A person's token
// leaves the tags as the client sent them.
func withRunAttribution(c *gin.Context, tags usageTags) usageTags {
	sessionID, ok := middlewares.CurrentRunSession(c)
	if !ok {
		return tags
	}
	tags.SessionID = &sessionID
	if tags.Source == "" {
		tags.Source = runSource
	}
	return tags
}

// runSource is the source label a task run's model calls carry.
const runSource = "TASK"

// usageRecord builds the row the write path stores from what the handler knows.
// It is separate from the handler so the mapping can be tested without a
// database: keyID is used only when hasKey is true, which is what keeps an
// access-token call from claiming a key.
func usageRecord(userID, providerID uuid.UUID, tags usageTags, model string, usage *chatUsage, keyID uuid.UUID, hasKey bool) service.UsageRecord {
	record := service.UsageRecord{
		UserID:           userID,
		SessionID:        tags.SessionID,
		AgentID:          tags.AgentID,
		ProviderID:       providerID,
		Source:           tags.Source,
		Model:            model,
		PromptTokens:     usage.PromptTokens,
		CachedTokens:     usage.cached(),
		CompletionTokens: usage.CompletionTokens,
		ReasoningTokens:  usage.reasoning(),
		TotalTokens:      usage.total(),
		IsCompaction:     tags.IsCompaction,
	}
	if hasKey {
		record.APIKeyID = &keyID
	}
	return record
}

// upstreamUserAgent is the User-Agent every proxied call carries, so an upstream
// sees the service rather than whichever client happened to call it. The product
// token carries the build's version, so an upstream can tell one release from
// the next, and a caller cannot disguise itself by choosing a user agent.
func upstreamUserAgent() string {
	return "rqark/" + userAgentVersion(version.Version)
}

// userAgentVersion turns the build's version string into the token a User-Agent
// product version may carry: the release tag without its leading `v`, and only
// the characters RFC 9110 allows there. A tag with a branch or a short commit
// in it still produces a valid token, and a build with no version at all
// reports `dev` rather than an empty product.
func userAgentVersion(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
			return r
		case r == '.', r == '-', r == '_', r == '+':
			return r
		}
		return -1
	}, strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	if cleaned == "" {
		return "dev"
	}
	return cleaned
}

// chatProxyTarget is everything about a call that comes from the provider row
// and the request tags rather than from the body: where the call goes, the
// credential it carries, the extra headers the owner stored, and the values
// those headers may interpolate.
type chatProxyTarget struct {
	APIBase string
	APIKey  string
	// Headers are the provider's stored extra headers, with ${name} placeholders
	// still in place. They are rendered against Vars in proxyChat.
	Headers map[string]string
	// Vars are the values a stored header may interpolate. A variable that is
	// absent from the map has no value for this call, and a header that names it
	// is left off.
	Vars map[string]string
}

// headerVariables collects the values a stored header may interpolate for one
// call. Everything here is something the call already knows; nothing is
// invented, so a variable with no value is simply absent and the header that
// names it is dropped upstream.
func headerVariables(userID, providerID uuid.UUID, model string, tags usageTags) map[string]string {
	vars := map[string]string{
		"user_id":     userID.String(),
		"provider_id": providerID.String(),
		"model":       model,
	}
	if tags.SessionID != nil {
		vars["session_id"] = tags.SessionID.String()
	}
	if tags.AgentID != nil {
		vars["agent_id"] = tags.AgentID.String()
	}
	return vars
}

// applyProviderHeaders puts the owner's stored headers on the upstream request,
// interpolating the ${name} placeholders against vars. A header naming a value
// this call does not have is skipped: half a value is worse than none. The
// headers that carry the proxy's own identity are set afterwards, so a stored
// header cannot replace the provider credential.
func applyProviderHeaders(header http.Header, stored map[string]string, vars map[string]string) {
	skip := headerSet(hopByHopHeaders...)
	skip["Host"] = true
	skip["Content-Length"] = true
	skip["Authorization"] = true
	skip["User-Agent"] = true

	for name, value := range stored {
		canonical := http.CanonicalHeaderKey(name)
		if skip[canonical] {
			continue
		}
		rendered, ok := service.RenderProviderHeaderValue(value, vars)
		if !ok {
			continue
		}
		header.Set(canonical, rendered)
	}
}

// proxyChat sends payload to the provider at apiBase, carrying the provider's
// credential, and relays what comes back. The provider's status, headers and
// body are passed on unchanged, so its own error codes reach the caller as they
// were sent rather than being translated.
//
// When the provider answered with a success, the usage object it carried is
// returned so the caller can record it. The bytes relayed to the caller are the
// same either way.
func proxyChat(c *gin.Context, target chatProxyTarget, payload []byte, stream bool) (*chatUsage, bool) {
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, chatUpstreamURL(target.APIBase), bytes.NewReader(payload))
	if err != nil {
		utils.Abort(c, utils.BadGateway, "The provider's api_base is not a usable URL.")
		return nil, false
	}
	copyRequestHeaders(request.Header, c.Request.Header)
	applyProviderHeaders(request.Header, target.Headers, target.Vars)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+target.APIKey)
	request.Header.Set("User-Agent", upstreamUserAgent())

	response, err := chatClient.Do(request)
	if err != nil {
		utils.Abort(c, utils.BadGateway, "The provider could not be reached.")
		return nil, false
	}
	defer response.Body.Close()

	copyResponseHeaders(c.Writer.Header(), response.Header)
	// A reverse proxy in front of this service buffers a response by default,
	// which would hold a streamed answer until the model finished. This header
	// is the one nginx reads to forward the response as it arrives.
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(response.StatusCode)

	// Only a success is recorded: a 4xx or 5xx spent nothing the provider would
	// bill for, and its body is an error rather than a completion.
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		relay(c.Writer, response.Body)
		return nil, false
	}

	if stream {
		return relayCollectingStream(c.Writer, response.Body), true
	}
	return relayCollectingBody(c.Writer, response.Body), true
}

// readProviderID pulls provider_id out of the body and removes it, so that it
// is not forwarded to the provider.
func readProviderID(fields map[string]json.RawMessage) (uuid.UUID, bool) {
	raw, ok := fields["provider_id"]
	if !ok {
		return uuid.Nil, false
	}
	delete(fields, "provider_id")

	var providerID uuid.UUID
	if err := json.Unmarshal(raw, &providerID); err != nil {
		return uuid.Nil, false
	}
	return providerID, true
}

// withModel returns the body with model filled from the provider when the
// caller left it out or sent it empty. It is the caller's model whenever there
// is one: the provider only supplies the default.
func withModel(fields map[string]json.RawMessage, defaultModel string) map[string]json.RawMessage {
	current := ""
	if raw, ok := fields["model"]; ok {
		_ = json.Unmarshal(raw, &current)
	}
	if current != "" {
		return fields
	}
	model, err := json.Marshal(defaultModel)
	if err != nil {
		return fields
	}
	fields["model"] = model
	return fields
}

// chatUpstreamURL joins the provider's api_base and the chat path. A trailing
// slash on api_base is dropped so that the path cannot be doubled; the version
// prefix the provider expects is already part of api_base.
func chatUpstreamURL(apiBase string) string {
	return strings.TrimSuffix(apiBase, "/") + chatUpstreamPath
}

// copyRequestHeaders copies the caller's headers to the upstream request,
// leaving out the hop-by-hop ones, the caller's own credentials and the
// encoding the client negotiates on its own.
func copyRequestHeaders(dst, src http.Header) {
	skip := headerSet(hopByHopHeaders...)
	skip["Authorization"] = true
	skip["Host"] = true
	skip["Content-Length"] = true
	skip["Accept-Encoding"] = true
	// The proxy identifies itself to the upstream rather than passing the
	// caller's user agent through, see proxyChat.
	skip["User-Agent"] = true

	for key, values := range src {
		if skip[http.CanonicalHeaderKey(key)] {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// copyResponseHeaders copies the provider's headers to the caller, leaving out
// the hop-by-hop ones.
func copyResponseHeaders(dst, src http.Header) {
	skip := headerSet(hopByHopHeaders...)

	for key, values := range src {
		if skip[http.CanonicalHeaderKey(key)] {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// headerSet builds the canonical-name lookup used to skip headers.
func headerSet(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[http.CanonicalHeaderKey(name)] = true
	}
	return set
}

// relay writes the provider's body to the caller and flushes after every read,
// so that a server-sent event stream reaches the caller token by token instead
// of being held until the provider is done.
func relay(w gin.ResponseWriter, body io.Reader) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			w.Flush()
		}
		if err != nil {
			return
		}
	}
}

// ChatRouter registers the provider proxy route. It accepts an access token or
// an API key, like the provider routes it builds on.
func ChatRouter(r *gin.Engine) {
	chat := r.Group("/api/chat", authOrAPIKey())
	chat.POST("", Chat)
	chat.POST("/:uuid/chat/completions", ChatByProvider)
}
