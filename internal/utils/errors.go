// Package utils holds the small helpers shared across the API: the error
// envelope, password hashing and policy validation, and access token signing.
package utils

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Code is the machine-readable error identifier. Only the codes declared in
// this file may appear in a response body.
type Code string

const (
	// Unauthorized is used when authentication fails.
	Unauthorized Code = "UNAUTHORIZED"
	// Forbidden is used when the caller lacks the required permission.
	Forbidden Code = "FORBIDDEN"
	// BadRequest is used for malformed or invalid input.
	BadRequest Code = "BAD_REQUEST"
	// NotFound is used when the addressed resource does not exist or is not
	// visible to the caller.
	NotFound Code = "NOT_FOUND"
	// Conflict is used when the request conflicts with the current state.
	Conflict Code = "CONFLICT"
	// InvalidContent is used when a memory record breaks a column limit: empty
	// or over-long content, too many keywords, a missing occurrence time.
	InvalidContent Code = "INVALID_CONTENT"
	// ImmutableField is used when a request names a field that cannot be
	// changed after it is written.
	ImmutableField Code = "IMMUTABLE_FIELD"
	// PinnedLimit is used when a pin would take a (user, agent) pair past its
	// pinned rows limit.
	PinnedLimit Code = "PINNED_LIMIT"
	// AgentAmbiguous is used when a name matches more than one agent the caller
	// may use.
	AgentAmbiguous Code = "AGENT_AMBIGUOUS"
	// ProviderAmbiguous is used when a name matches more than one provider the
	// caller may use.
	ProviderAmbiguous Code = "PROVIDER_AMBIGUOUS"
	// LastSuperuser is used when an operation would leave no active superuser.
	LastSuperuser Code = "LAST_SUPERUSER"
	// Internal is used for unexpected server-side failures.
	Internal Code = "INTERNAL"
)

// statuses maps every allowed code to its HTTP status.
var statuses = map[Code]int{
	Unauthorized:      http.StatusUnauthorized,
	Forbidden:         http.StatusForbidden,
	BadRequest:        http.StatusBadRequest,
	NotFound:          http.StatusNotFound,
	Conflict:          http.StatusConflict,
	InvalidContent:    http.StatusBadRequest,
	ImmutableField:    http.StatusBadRequest,
	PinnedLimit:       http.StatusConflict,
	AgentAmbiguous:    http.StatusConflict,
	ProviderAmbiguous: http.StatusConflict,
	LastSuperuser:     http.StatusConflict,
	Internal:          http.StatusInternalServerError,
}

// Error is a single error object.
type Error struct {
	Code    Code   `json:"code" example:"UNAUTHORIZED"`
	Message string `json:"message" example:"Invalid username or password"`
}

// ErrorResponse is the error envelope returned by every failing endpoint.
type ErrorResponse struct {
	Error Error `json:"error"`
}

// Status returns the HTTP status paired with code, or 500 for an unknown code.
func (c Code) Status() int {
	if s, ok := statuses[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Abort stops the handler chain and writes the error envelope. An unknown code
// is reported as Internal so that only allowed codes reach the client.
func Abort(c *gin.Context, code Code, message string) {
	status, ok := statuses[code]
	if !ok {
		code = Internal
		status = http.StatusInternalServerError
	}
	c.AbortWithStatusJSON(status, ErrorResponse{Error: Error{Code: code, Message: message}})
}
