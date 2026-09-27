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

// specHeaders marks a specification response as readable from anywhere and
// never cacheable.
//
// The document is public, so any origin may read it: that is what lets a
// browser-based viewer such as Scalar, served from a different origin, load the
// API it is pointed at. No credentials are involved, which is why the wildcard
// is safe here and is not used on the rest of the API.
//
// no-store is the part that keeps the viewer current. Without it a viewer, a
// proxy or the browser is free to hand back the copy it fetched earlier, so an
// endpoint that was added since stays invisible however often the page is
// reloaded.
func specHeaders(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Methods", "GET, OPTIONS")
	c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")
}

// SwaggerJSON serves the generated OpenAPI 2.0 document as JSON.
//
//	@Summary		OpenAPI specification (JSON)
//	@Description	Returns the generated Swagger 2.0 document describing this API. Served without caching, so a viewer always sees the current API.
//	@Tags			swagger
//	@Produce		application/json
//	@Success		200	{string}	string	"Swagger 2.0 document as JSON"
//	@Router			/swagger.json [get]
func SwaggerJSON(c *gin.Context) {
	specHeaders(c)
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(swaggerJSON))
}

// SwaggerYAML serves the generated OpenAPI 2.0 document as YAML.
//
//	@Summary		OpenAPI specification (YAML)
//	@Description	Returns the generated Swagger 2.0 document describing this API. Served without caching, so a viewer always sees the current API.
//	@Tags			swagger
//	@Produce		application/yaml
//	@Success		200	{string}	string	"Swagger 2.0 document as YAML"
//	@Router			/swagger.yml [get]
func SwaggerYAML(c *gin.Context) {
	specHeaders(c)
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", swaggerYAML)
}

// SwaggerPreflight answers the CORS preflight a browser sends before it reads
// the specification from another origin.
//
//	@Summary		Specification preflight
//	@Description	Answers the CORS preflight for the specification endpoints. Any origin may read the document, and GET is the only method offered.
//	@Tags			swagger
//	@Success		204
//	@Router			/swagger.json [options]
func SwaggerPreflight(c *gin.Context) {
	specHeaders(c)
	c.Status(http.StatusNoContent)
}

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

	r.GET("/api/swagger.json", SwaggerJSON)
	r.OPTIONS("/api/swagger.json", SwaggerPreflight)
	r.GET("/api/swagger.yml", SwaggerYAML)
	r.OPTIONS("/api/swagger.yml", SwaggerPreflight)
}
