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
