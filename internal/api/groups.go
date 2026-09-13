package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/service"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// GroupDTO is the group representation returned by every group endpoint.
type GroupDTO struct {
	ID        uuid.UUID `json:"id" example:"9b2f5c1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a"`
	Name      string    `json:"name" example:"engineering"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateGroupRequest is the body of POST /api/groups.
type CreateGroupRequest struct {
	Name string `json:"name" example:"engineering"`
}

// UpdateGroupRequest is the body of PATCH /api/groups/{uuid}.
type UpdateGroupRequest struct {
	Name *string `json:"name"`
}

// toGroupDTO converts a stored group into its wire form.
func toGroupDTO(group *repository.Group) GroupDTO {
	return GroupDTO{
		ID:        group.ID,
		Name:      group.Name,
		CreatedAt: group.CreatedAt,
		UpdatedAt: group.UpdatedAt,
	}
}

// ListGroups returns a page of groups.
//
//	@Summary		List groups
//	@Description	Requires a bearer token. Returns groups only, never their members.
//	@Tags			groups
//	@Security		BearerAuth
//	@Produce		json
//	@Param			limit	query		int	false	"Page size (default 20, max 100)"
//	@Param			offset	query		int	false	"Number of groups to skip"
//	@Success		200		{object}	utils.Page[GroupDTO]
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/groups [get]
func ListGroups(c *gin.Context) {
	page := utils.ParsePagination(c)

	groups, total, err := service.ListGroups(c.Request.Context(), page.Limit, page.Offset)
	if err != nil {
		abortInternal(c, err)
		return
	}

	items := make([]GroupDTO, 0, len(groups))
	for i := range groups {
		items = append(items, toGroupDTO(&groups[i]))
	}
	c.JSON(http.StatusOK, utils.NewPage(items, total, page))
}

// CreateGroup creates a group.
//
//	@Summary		Create a group
//	@Description	Superuser only. Answers 201 with a Location header pointing at the new group.
//	@Tags			groups
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateGroupRequest	true	"Group to create"
//	@Success		201		{object}	GroupDTO
//	@Header			201		{string}	Location	"URL of the created group"
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/groups [post]
func CreateGroup(c *gin.Context) {
	var request CreateGroupRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	group, err := service.CreateGroup(c.Request.Context(), request.Name)
	if err != nil {
		abortError(c, err)
		return
	}

	c.Header("Location", "/api/groups/"+group.ID.String())
	c.JSON(http.StatusCreated, toGroupDTO(group))
}

// GetGroup returns one group.
//
//	@Summary		Get a group
//	@Description	Requires a bearer token.
//	@Tags			groups
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Group ID"	format(uuid)
//	@Success		200		{object}	GroupDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/groups/{uuid} [get]
func GetGroup(c *gin.Context) {
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	group, err := service.GetGroup(c.Request.Context(), id)
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toGroupDTO(group))
}

// UpdateGroup renames one group.
//
//	@Summary		Rename a group
//	@Description	Superuser only.
//	@Tags			groups
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			uuid	path		string				true	"Group ID"	format(uuid)
//	@Param			request	body		UpdateGroupRequest	true	"New name"
//	@Success		200		{object}	GroupDTO
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		409		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/groups/{uuid} [patch]
func UpdateGroup(c *gin.Context) {
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	var request UpdateGroupRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		abortBadRequest(c, "Malformed request body.")
		return
	}

	group, err := service.UpdateGroup(c.Request.Context(), id, service.UpdateGroupInput{Name: request.Name})
	if err != nil {
		abortError(c, err)
		return
	}
	c.JSON(http.StatusOK, toGroupDTO(group))
}

// DeleteGroup deletes one group.
//
//	@Summary		Delete a group
//	@Description	Superuser only. Hard delete: the group and its memberships are removed.
//	@Tags			groups
//	@Security		BearerAuth
//	@Param			uuid	path	string	true	"Group ID"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		403	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/groups/{uuid} [delete]
func DeleteGroup(c *gin.Context) {
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}

	if err := service.DeleteGroup(c.Request.Context(), id); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListGroupUsers returns a page of the members of one group.
//
//	@Summary		List the members of a group
//	@Description	Superuser only: the response carries user DTOs, so a plain token must not reach it.
//	@Tags			groups
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uuid	path		string	true	"Group ID"	format(uuid)
//	@Param			limit	query		int		false	"Page size (default 20, max 100)"
//	@Param			offset	query		int		false	"Number of members to skip"
//	@Success		200		{object}	utils.Page[UserDTO]
//	@Failure		400		{object}	utils.ErrorResponse
//	@Failure		401		{object}	utils.ErrorResponse
//	@Failure		403		{object}	utils.ErrorResponse
//	@Failure		500		{object}	utils.ErrorResponse
//	@Router			/groups/{uuid}/users [get]
func ListGroupUsers(c *gin.Context) {
	id, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}
	page := utils.ParsePagination(c)

	users, total, err := service.ListGroupUsers(c.Request.Context(), id, page.Limit, page.Offset)
	if err != nil {
		abortError(c, err)
		return
	}

	items := make([]UserDTO, 0, len(users))
	for i := range users {
		items = append(items, toUserDTO(&users[i]))
	}
	c.JSON(http.StatusOK, utils.NewPage(items, total, page))
}

// AddGroupMember makes a user a member of a group.
//
//	@Summary		Add a group member
//	@Description	Superuser only. Idempotent: adding an existing member answers 204 as well.
//	@Tags			groups
//	@Security		BearerAuth
//	@Param			uuid		path	string	true	"Group ID"		format(uuid)
//	@Param			user_uuid	path	string	true	"User ID"		format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		403	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/groups/{uuid}/users/{user_uuid} [put]
func AddGroupMember(c *gin.Context) {
	groupID, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}
	userID, ok := parseUUIDParam(c, "user_uuid")
	if !ok {
		return
	}

	if err := service.AddGroupMember(c.Request.Context(), groupID, userID); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// RemoveGroupMember drops a membership.
//
//	@Summary		Remove a group member
//	@Description	Superuser only. Removing a membership that does not exist answers 204 as well.
//	@Tags			groups
//	@Security		BearerAuth
//	@Param			uuid		path	string	true	"Group ID"	format(uuid)
//	@Param			user_uuid	path	string	true	"User ID"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	utils.ErrorResponse
//	@Failure		401	{object}	utils.ErrorResponse
//	@Failure		403	{object}	utils.ErrorResponse
//	@Failure		500	{object}	utils.ErrorResponse
//	@Router			/groups/{uuid}/users/{user_uuid} [delete]
func RemoveGroupMember(c *gin.Context) {
	groupID, ok := parseUUIDParam(c, "uuid")
	if !ok {
		return
	}
	userID, ok := parseUUIDParam(c, "user_uuid")
	if !ok {
		return
	}

	if err := service.RemoveGroupMember(c.Request.Context(), groupID, userID); err != nil {
		abortError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GroupsRouter registers the group routes. Reading a group only needs a bearer
// token; everything that writes, and the member listing, needs a superuser.
func GroupsRouter(r *gin.Engine) {
	read := r.Group("/api/groups", authRequired())
	read.GET("", ListGroups)
	read.GET("/:uuid", GetGroup)

	admin := r.Group("/api/groups", superuserRequired()...)
	admin.POST("", CreateGroup)
	admin.PATCH("/:uuid", UpdateGroup)
	admin.DELETE("/:uuid", DeleteGroup)
	admin.GET("/:uuid/users", ListGroupUsers)
	admin.PUT("/:uuid/users/:user_uuid", AddGroupMember)
	admin.DELETE("/:uuid/users/:user_uuid", RemoveGroupMember)
}
