package service

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

const (
	// maxProjectNameLen is the width of the projects.project_name column.
	maxProjectNameLen = 64
	// maxProjectPathLen is the width of the projects.project_path column.
	maxProjectPathLen = 512
)

// CreateProjectInput is the body accepted by POST /api/projects. ID is the uuid
// the caller wants for the project; when it is nil the cloud mints one.
type CreateProjectInput struct {
	ID          *uuid.UUID
	ProjectName string
	ProjectPath string
}

// UpdateProjectInput is a partial update: a nil field is left untouched.
type UpdateProjectInput struct {
	ProjectName *string
	ProjectPath *string
}

// ListProjects returns every project of userID.
func ListProjects(ctx context.Context, userID uuid.UUID) ([]repository.Project, error) {
	return repository.ListProjects(ctx, config.DB, userID)
}

// GetProject loads one of userID's projects.
func GetProject(ctx context.Context, userID, id uuid.UUID) (*repository.Project, error) {
	return repository.GetProjectByID(ctx, config.DB, userID, id)
}

// CreateProject validates the name and path and stores the project under
// userID.
func CreateProject(ctx context.Context, userID uuid.UUID, in CreateProjectInput) (*repository.Project, error) {
	name, err := normalizeProjectName(in.ProjectName)
	if err != nil {
		return nil, err
	}
	projectPath, err := normalizeProjectPath(in.ProjectPath)
	if err != nil {
		return nil, err
	}

	project := &repository.Project{
		ID:          uuid.New(),
		UserID:      userID,
		ProjectName: name,
		ProjectPath: projectPath,
	}
	if in.ID != nil {
		project.ID = *in.ID
	}
	if err := repository.CreateProject(ctx, config.DB, project); err != nil {
		return nil, mapWriteError(err)
	}
	return project, nil
}

// UpdateProject applies in to one of userID's projects. Moving project_path does
// not move any files: it re-points the project, and every session that uses it
// picks up the new directory on its next turn.
func UpdateProject(ctx context.Context, userID, id uuid.UUID, in UpdateProjectInput) (*repository.Project, error) {
	project, err := repository.GetProjectByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}

	if in.ProjectName != nil {
		name, err := normalizeProjectName(*in.ProjectName)
		if err != nil {
			return nil, err
		}
		project.ProjectName = name
	}
	if in.ProjectPath != nil {
		projectPath, err := normalizeProjectPath(*in.ProjectPath)
		if err != nil {
			return nil, err
		}
		project.ProjectPath = projectPath
	}

	if err := repository.SaveProject(ctx, config.DB, project); err != nil {
		return nil, mapWriteError(err)
	}
	return project, nil
}

// DeleteProject removes one of userID's projects. Its sessions stay; their
// project_id is cleared by the foreign key.
func DeleteProject(ctx context.Context, userID, id uuid.UUID) error {
	return repository.DeleteProject(ctx, config.DB, userID, id)
}

// checkProjectOwner reports whether projectID names a project of userID. A nil
// id is accepted: it is a session that names no project. Another user's project
// is a validation error rather than a not-found one, because the caller named
// the id explicitly.
func checkProjectOwner(ctx context.Context, userID uuid.UUID, projectID *uuid.UUID) error {
	if projectID == nil {
		return nil
	}
	if _, err := repository.GetProjectByID(ctx, config.DB, userID, *projectID); err != nil {
		if errors.Is(err, repository.ErrProjectNotFound) {
			return &ValidationError{Message: "project_id must name one of your projects."}
		}
		return err
	}
	return nil
}

// normalizeProjectName trims the name, rejects an empty one and enforces the
// column width. A NUL is rejected rather than dropped: for a name the caller
// typed, silently shortening it would be worse than refusing it.
func normalizeProjectName(name string) (string, error) {
	if strings.ContainsRune(name, 0) {
		return "", &ValidationError{Message: "project_name must not contain a NUL character."}
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", &ValidationError{Message: "project_name is required."}
	}
	if len([]rune(trimmed)) > maxProjectNameLen {
		return "", &ValidationError{Message: "project_name is too long."}
	}
	for _, char := range trimmed {
		if char < 0x20 || char == 0x7f {
			return "", &ValidationError{Message: "project_name must not contain control characters."}
		}
	}
	return trimmed, nil
}

// normalizeProjectPath checks that path is a relative POSIX path inside the
// sandbox volume and returns it normalised: doubled and trailing slashes
// collapsed, no "." segments. An absolute path, a backslash, a NUL or a ".."
// segment is rejected, so the stored value can only ever name a directory at or
// below the volume root.
func normalizeProjectPath(raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", &ValidationError{Message: "project_path must not contain a NUL character."}
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", &ValidationError{Message: "project_path is required."}
	}
	if len(trimmed) > maxProjectPathLen {
		return "", &ValidationError{Message: "project_path is too long."}
	}
	if strings.HasPrefix(trimmed, "/") {
		return "", &ValidationError{Message: "project_path must be relative to the workspace root."}
	}
	if strings.Contains(trimmed, `\`) {
		return "", &ValidationError{Message: "project_path must use forward slashes."}
	}

	for _, segment := range strings.Split(trimmed, "/") {
		if segment == ".." {
			return "", &ValidationError{Message: "project_path must not contain \"..\"."}
		}
	}

	normalized := path.Clean(trimmed)
	if normalized == "." || normalized == "/" || strings.HasPrefix(normalized, "../") {
		return "", &ValidationError{Message: "project_path must name a directory inside the workspace root."}
	}
	return normalized, nil
}
