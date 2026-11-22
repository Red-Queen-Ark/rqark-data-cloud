package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrProjectNotFound is returned when no project matches the lookup. It also
// covers a lookup that names another user's project, so project ids cannot be
// enumerated.
var ErrProjectNotFound = errors.New("repository: project not found")

// Project is the projects table: one working directory of one user, named. A
// session may point at a project to say which directory the agent works in;
// project_path is relative to the sandbox volume root, never a host path.
//
// Both (user_id, project_name) and (user_id, project_path) are unique: two
// projects of one user that share a name are indistinguishable in the picker,
// and two that share a path are the same directory twice.
type Project struct {
	ID uuid.UUID `gorm:"type:uuid;primaryKey"`
	// UserID leads both unique indexes, which also serve the lookup by owner.
	UserID      uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_projects_user_id_project_name,priority:1;uniqueIndex:idx_projects_user_id_project_path,priority:1"`
	ProjectName string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_projects_user_id_project_name,priority:2"`
	ProjectPath string    `gorm:"type:varchar(512);not null;uniqueIndex:idx_projects_user_id_project_path,priority:2"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`

	// User only exists to tie user_id to users.id with a foreign key. It is
	// never loaded or written through this struct.
	User User `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (Project) TableName() string { return "projects" }

// GetProjectByID loads one of userID's projects by primary key. Another user's
// project is reported as ErrProjectNotFound.
func GetProjectByID(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) (*Project, error) {
	var project Project
	if err := db.WithContext(ctx).First(&project, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, projectLookupError(err)
	}
	return &project, nil
}

// CreateProject inserts a project.
func CreateProject(ctx context.Context, db *gorm.DB, project *Project) error {
	return db.WithContext(ctx).Create(project).Error
}

// SaveProject writes every column of project, bumping its updated_at.
func SaveProject(ctx context.Context, db *gorm.DB, project *Project) error {
	return db.WithContext(ctx).Save(project).Error
}

// ListProjects returns every project of userID, most recently updated first.
// The list is not paginated: it is one user's own set, which the picker wants
// whole.
func ListProjects(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]Project, error) {
	projects := []Project{}
	err := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("updated_at DESC").Order("id").
		Find(&projects).Error
	if err != nil {
		return nil, err
	}
	return projects, nil
}

// DeleteProject removes one of userID's projects. The sessions that point at it
// keep their rows: the project_id foreign key clears itself, so those sessions
// fall back to the volume root.
func DeleteProject(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) error {
	result := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&Project{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrProjectNotFound
	}
	return nil
}

// projectLookupError maps GORM's not-found error onto the repository's own.
func projectLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrProjectNotFound
	}
	return err
}
