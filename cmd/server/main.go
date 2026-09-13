package main

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/api"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
)

//go:generate go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g main.go -d .,../../internal/api -o ../../docs --parseInternal

// @title						rqark-data-cloud
// @version					1.0
// @description				Data cloud API.
//
// @host						localhost:8080
// @BasePath					/api
//
// @tag.name					ping
// @tag.description			Health check endpoint.
// @tag.name					swagger
// @tag.description			OpenAPI specification endpoints.
// @tag.name					users
// @tag.description			User accounts.
// @tag.name					groups
// @tag.description			Groups and their memberships.
// @tag.name					auth
// @tag.description			Authentication, registration and password reset.
//
// @securityDefinitions.apikey	BearerAuth
// @in							header
// @name						Authorization
// @description				Bearer <access_token>
func main() {
	config.InitDatabase()
	config.InitRedis()

	r := gin.Default()
	api.PingRouter(r)
	api.SwaggerRouter(r)

	r.GET("/swagger/*any", ginSwagger.WrapHandler(
		swaggerFiles.Handler,
		ginSwagger.URL("/api/swagger.json"),
	))

	r.Run()
}
