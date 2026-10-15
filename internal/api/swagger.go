package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

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

// dropEmptyAddressFields removes the parts of the document that would pin the
// address the service is reached at.
//
// The generator writes a `host` key even when no @host annotation is set, and it
// writes `schemes` unconditionally, so a spec with neither annotation reaches a
// caller carrying `"host": ""` and `"schemes": []`. An empty host is not a valid
// host, and both keys are better absent than empty: Swagger 2.0 reads a missing
// host and a missing scheme as "the one serving the documentation", which is what
// this service wants. Every deployment picks its own address, its own port and
// whether it terminates TLS — the port is `PORT`, 7302 by default, and the public
// name belongs to the proxy in front — so anything written here would be wrong
// everywhere but on the machine it was generated on.
func dropEmptyAddressFields(doc map[string]any) {
	if host, ok := doc["host"].(string); ok && host == "" {
		delete(doc, "host")
	}
	if schemes, ok := doc["schemes"].([]any); ok && len(schemes) == 0 {
		delete(doc, "schemes")
	}
}

func SwaggerRouter(r *gin.Engine) {
	doc, err := swag.ReadDoc()
	if err != nil {
		panic("api: swagger doc not registered, run `go generate ./...`: " + err.Error())
	}

	var generic map[string]any
	if err := json.Unmarshal([]byte(doc), &generic); err != nil {
		panic("api: swagger doc is not valid JSON: " + err.Error())
	}

	dropEmptyAddressFields(generic)

	// Both documents are rendered from the one parsed spec, so the JSON and the
	// YAML can never come to disagree about it. HTML escaping is off to keep a
	// description such as "Bearer <access_token>" readable instead of written as
	// \u003c, which is what the generated document looks like on disk.
	var jsonDoc bytes.Buffer
	encoder := json.NewEncoder(&jsonDoc)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "    ")
	if err := encoder.Encode(generic); err != nil {
		panic("api: cannot marshal swagger json: " + err.Error())
	}

	swaggerJSON = strings.TrimSuffix(jsonDoc.String(), "\n")

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
