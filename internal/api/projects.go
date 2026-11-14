package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/middlewares"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// ProjectDTO is the project representation returned by every project endpoint.
// The owner is left out: a project is only ever visible to the user it belongs
// to. project_path is relative to the sandbox volume root.
type ProjectDTO struct {
	ID          uuid.UUID `json:"id" example:"0f0b7f9a-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	ProjectName string    `json:"project_name" example:"acme-api"`
	ProjectPath string    `json:"project_path" example:"acme/api"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateProjectRequest is the body of POST /api/projects.
type CreateProjectRequest struct {
	UUID        *string `json:"id" example:"0f0b7f9a-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	ProjectName string  `json:"project_name" example:"acme-api"`
	ProjectPath string  `json:"project_path" example:"acme/api"`
}

// UpdateProjectRequest is the body of PUT /api/projects/{uuid}. Every field is
// optional: a field that is absent leaves the stored value alone.
type UpdateProjectRequest struct {
	ProjectName *string `json:"project_name"`
	ProjectPath *string `json:"project_path"`
}

// toProjectDTO converts a stored project into its wire form.
func toProjectDTO(project *repository.Project) ProjectDTO {
	return ProjectDTO{
		ID:          project.ID,
		ProjectName: project.ProjectName,
		ProjectPath: project.ProjectPath,
		CreatedAt:   project.CreatedAt,
		UpdatedAt:   project.UpdatedAt,
	}
}

// ListProjects returns the caller's projects.
//
//	@Summary		List projects
//	@Description	Returns every project of the caller, most recently updated first. Not paginated: it is one user's own set, which the picker wants whole.
//	@Tags			projects
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		ProjectDTO
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/projects [get]
func ListProjects(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	projects, err := service.ListProjects(c.Request.Context(), actor.ID)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]ProjectDTO, 0, len(projects))
	for i := range projects {
		items = append(items, toProjectDTO(&projects[i]))
	}
	c.JSON(http.StatusOK, items)
}

// CreateProject creates a project.
//
//	@Summary		Create a project
//	@Description	The project belongs to the caller. project_path is relative to the workspace root and must not escape it. Answers 201 with a Location header pointing at the new project.
//	@Tags			projects
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateProjectRequest	true	"Project to create"
//	@Success		201		{object}	ProjectDTO
//	@Header			201		{string}	Location	"URL of the created project"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/projects [post]
func CreateProject(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}

	var request CreateProjectRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	in := service.CreateProjectInput{
		ProjectName: request.ProjectName,
		ProjectPath: request.ProjectPath,
	}
	if request.UUID != nil {
		id, err := uuid.Parse(*request.UUID)
		if err != nil {
			abortBadRequest(c, "id must be a UUID.")
			return
		}
		in.ID = &id
	}

	project, err := service.CreateProject(c.Request.Context(), actor.ID, in)
	if err != nil {
		abortError(c, err)
		return
	}

	c.Header("Location", "/api/projects/"+project.ID.String())
	c.JSON(http.StatusCreated, toProjectDTO(project))
}

// GetProject returns one project.
//
//	@Summary		Get a project
//	@Description	Returns one of the caller's projects. Another user's project is reported as 404.
//	@Tags			projects
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Project ID"	format(uuid)
//	@Success		200		{object}	ProjectDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/projects/{uuid} [get]
func GetProject(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	project, err := service.GetProject(c.Request.Context(), actor.ID, id)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toProjectDTO(project))
}

// UpdateProject updates one project.
//
//	@Summary		Update a project
//	@Description	Partial update of one of the caller's projects. Changing project_path does not move files: it re-points the project, and its sessions pick up the new directory on their next turn. Another user's project is reported as 404.
//	@Tags			projects
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string					true	"Project ID"	format(uuid)
//	@Param			request	body		UpdateProjectRequest	true	"Fields to change"
//	@Success		200		{object}	ProjectDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		404		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/projects/{uuid} [put]
func UpdateProject(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var request UpdateProjectRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	project, err := service.UpdateProject(c.Request.Context(), actor.ID, id, service.UpdateProjectInput{
		ProjectName: request.ProjectName,
		ProjectPath: request.ProjectPath,
	})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toProjectDTO(project))
}

// DeleteProject deletes one project.
//
//	@Summary		Delete a project
//	@Description	Hard delete of one of the caller's projects. Its files stay where they are and its sessions keep their rows, with project_id cleared. Another user's project is reported as 404.
//	@Tags			projects
//	@Security		BearerAuth
//	@Param			uuid	path	string	true	"Project ID"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		404	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/projects/{uuid} [delete]
func DeleteProject(c *gin.Context) {
	actor, ok := middlewares.CurrentUser(c)
	if !ok {
		utils.Abort(c, utils.Unauthorized, "Authentication credentials were not provided.")
		return
	}
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	if err := service.DeleteProject(c.Request.Context(), actor.ID, id); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ProjectsRouter registers the project routes. There is no superuser path here:
// every project belongs to the caller, and every query is scoped to the user
// behind the access token or the API key.
func ProjectsRouter(r *gin.Engine) {
	projects := r.Group("/api/projects", authOrAPIKey())
	projects.GET("", ListProjects)
	projects.POST("", CreateProject)
	projects.GET("/:uuid", GetProject)
	projects.PUT("/:uuid", UpdateProject)
	projects.DELETE("/:uuid", DeleteProject)
}
