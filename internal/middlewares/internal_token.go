package middlewares

import (
	"crypto/subtle"

	"github.com/gin-gonic/gin"

	"rqark-data-cloud/internal/utils"
)

// InternalTokenHeader is the header the agent cloud carries on the internal
// endpoints. It is a header of its own rather than an Authorization scheme: the
// routes behind it are not user-scoped, and accepting a bearer token there would
// invite a user credential to stand in for the service one.
const InternalTokenHeader = "X-Internal-Token"

// InternalToken guards the internal endpoints with the shared secret the agent
// cloud presents.
//
// An unset secret rejects every request rather than accepting an empty header:
// a deployment that forgot to configure it would otherwise leave the endpoints
// open to anyone who sends nothing at all. The comparison is constant time, so a
// wrong token cannot be recovered byte by byte from the response timing.
func InternalToken(secret string) gin.HandlerFunc {
	expected := []byte(secret)

	return func(c *gin.Context) {
		if len(expected) == 0 {
			utils.Abort(c, utils.Unauthorized, "Internal endpoints are not configured.")
			return
		}

		presented := c.GetHeader(InternalTokenHeader)
		if subtle.ConstantTimeCompare([]byte(presented), expected) != 1 {
			utils.Abort(c, utils.Unauthorized, "Invalid internal token.")
			return
		}
		c.Next()
	}
}
