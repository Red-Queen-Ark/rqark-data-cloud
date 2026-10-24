package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
)

// SystemConfigResponse is what a client needs to know about the deployment
// before anybody has logged in.
type SystemConfigResponse struct {
	// AllowSelfRegistration mirrors ALLOW_SELF_REGISTRATION. When it is false the
	// client hides its "create one" link, because the endpoint behind it would
	// answer 403.
	AllowSelfRegistration bool `json:"allow_self_registration" example:"false"`
}

// SystemConfig reports the deployment-wide settings a client has to respect
// before it can authenticate.
//
//	@Summary		System configuration
//	@Description	Returns the settings that apply to the whole deployment. Unauthenticated, so the login page can read it. It holds nothing user specific and nothing secret.
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	SystemConfigResponse
//	@Router			/system/config [get]
func SystemConfig(c *gin.Context) {
	c.JSON(http.StatusOK, SystemConfigResponse{
		AllowSelfRegistration: config.AllowSelfRegistration,
	})
}

// SystemRouter registers the endpoints that describe the running deployment.
func SystemRouter(r *gin.Engine) {
	r.GET("/api/system/config", SystemConfig)
}
