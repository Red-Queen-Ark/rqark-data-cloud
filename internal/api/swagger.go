package api

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/swaggo/swag"
	"gopkg.in/yaml.v2"

	_ "gitea.carhyme.top/RedQueenArk/rqark-data-cloud/docs"
)

var (
	swaggerJSON string
	swaggerYAML []byte
)

func SwaggerRouter(r *gin.Engine) {
	doc, err := swag.ReadDoc()
	if err != nil {
		panic("api: swagger doc not registered, run `go generate ./...`: " + err.Error())
	}

	swaggerJSON = doc

	var generic map[string]any
	if err := json.Unmarshal([]byte(doc), &generic); err != nil {
		panic("api: swagger doc is not valid JSON: " + err.Error())
	}

	yml, err := yaml.Marshal(generic)
	if err != nil {
		panic("api: cannot marshal swagger yaml: " + err.Error())
	}
	swaggerYAML = yml

	r.GET("/api/swagger.json", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(swaggerJSON))
	})

	r.GET("/api/swagger.yml", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/yaml; charset=utf-8", swaggerYAML)
	})
}
