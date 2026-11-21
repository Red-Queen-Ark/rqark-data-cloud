package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"rqark-data-cloud/internal/version"
)

type VersionResponse struct {
	Version string `json:"version" example:"v0.1.6"`
}

// Version reports the version of the running build.
//
//	@Summary		Build version
//	@Description	Returns the version of the running binary. It is injected at build time from the release tag, so a development build reports "dev". Does not touch PostgreSQL or Redis.
//	@Tags			version
//	@Produce		json
//	@Success		200	{object}	VersionResponse
//	@Router			/version [get]
func Version(c *gin.Context) {
	c.JSON(http.StatusOK, VersionResponse{Version: version.Version})
}

func VersionRouter(r *gin.Engine) {
	r.GET("/api/version", Version)
}
