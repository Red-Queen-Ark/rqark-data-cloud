package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type PingResponse struct {
	Message string `json:"message" example:"pong"`
}

// Ping is the health check endpoint.
//
//	@Summary		Health check
//	@Description	Returns pong. Does not touch PostgreSQL or Redis.
//	@Tags			meta
//	@Produce		json
//	@Success		200	{object}	PingResponse
//	@Router			/ping [get]
func Ping(c *gin.Context) {
	c.JSON(http.StatusOK, PingResponse{Message: "pong"})
}

func PingRouter(r *gin.Engine) {
	r.GET("/api/ping", Ping)
}
