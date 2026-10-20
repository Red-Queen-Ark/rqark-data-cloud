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

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/middlewares"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
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
//	@Description	Forwards an OpenAI chat completion to the provider named by provider_id and relays the provider's response as it arrives, including a server-sent event stream when stream is true. The provider's api_key is added server-side. The provider's status, headers and body are returned unchanged, so an upstream 401 or 429 reaches the caller as it was sent.
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
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication is required.")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		abortBadRequest(c, "The request body could not be read.")
		return
	}

	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &fields); err != nil {
		abortBadRequest(c, "The request body must be a JSON object.")
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
	if _, ok := fields["messages"]; !ok {
		abortBadRequest(c, "messages is required.")
		return
	}

	view, err := service.GetProvider(c.Request.Context(), actor.ID, providerID)
	if err != nil {
		abortError(c, err)
		return
	}

	payload, err := json.Marshal(withModel(fields, view.ModelName))
	if err != nil {
		abortInternal(c, err)
		return
	}

	proxyChat(c, view.APIBase, view.APIKey, payload)
}

// proxyChat sends payload to the provider at apiBase, carrying the provider's
// credential, and relays what comes back. The provider's status, headers and
// body are passed on unchanged, so its own error codes reach the caller as they
// were sent rather than being translated.
func proxyChat(c *gin.Context, apiBase, apiKey string, payload []byte) {
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, chatUpstreamURL(apiBase), bytes.NewReader(payload))
	if err != nil {
		utils.Abort(c, utils.BadGateway, "The provider's api_base is not a usable URL.")
		return
	}
	copyRequestHeaders(request.Header, c.Request.Header)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)

	response, err := chatClient.Do(request)
	if err != nil {
		utils.Abort(c, utils.BadGateway, "The provider could not be reached.")
		return
	}
	defer response.Body.Close()

	copyResponseHeaders(c.Writer.Header(), response.Header)
	c.Writer.WriteHeader(response.StatusCode)
	relay(c.Writer, response.Body)
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
}
