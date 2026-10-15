package main

import (
	"log"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/api"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

//go:generate go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g main.go -d .,../../internal/api,../../internal/utils,../../internal/repository -o ../../docs --parseInternal

// @title						rqark-data-cloud
// @version					1.0
// @description				Data cloud API.
//
// The spec carries no @host on purpose. Every deployment picks its own address
// and port (`PORT`, 7302 by default) and the service sits behind a proxy it does
// not know the public name of, so any host written here would be wrong for every
// deployment but one. Swagger 2.0 reads a missing host as "the host serving the
// documentation", which is exactly right: the spec is served by the service it
// describes, at /api/swagger.json and /api/swagger.yml.
//
// @BasePath					/api
//
// @tag.name					ping
// @tag.description			Health check endpoint.
// @tag.name					version
// @tag.description			Build version of the running service.
// @tag.name					swagger
// @tag.description			OpenAPI specification endpoints.
// @tag.name					users
// @tag.description			User accounts.
// @tag.name					groups
// @tag.description			Groups and their memberships.
// @tag.name					auth
// @tag.description			Authentication, registration and password reset.
// @tag.name					agents
// @tag.description			Agents owned by the authenticated user.
// @tag.name					providers
// @tag.description			Model providers owned by the authenticated user.
// @tag.name					sessions
// @tag.description			Sessions and their messages owned by the authenticated user.
// @tag.name					api-keys
// @tag.description			API keys owned by the authenticated user.
// @tag.name					user-config
// @tag.description			Per-user settings owned by the authenticated user.
// @tag.name					agent-defaults
// @tag.description			Default agent per session source owned by the authenticated user.

// @securityDefinitions.apikey	BearerAuth
// @in							header
// @name						Authorization
// @description				Bearer <access_token>
func main() {
	config.InitDatabase()
	if err := config.DB.AutoMigrate(&repository.User{}, &repository.Group{}, &repository.UserGroup{}, &repository.Agent{}, &repository.Provider{}, &repository.Session{}, &repository.SessionMessage{}, &repository.APIKey{}, &repository.UserConfig{}, &repository.ProviderPriority{}, &repository.AgentDefault{}); err != nil {
		log.Fatalf("main: automigrate: %v", err)
	}
	config.InitRedis()

	r := gin.Default()
	api.APIRouter(r)

	r.GET("/swagger/*any", ginSwagger.WrapHandler(
		swaggerFiles.Handler,
		ginSwagger.URL("/api/swagger.json"),
	))

	r.Run()
}
