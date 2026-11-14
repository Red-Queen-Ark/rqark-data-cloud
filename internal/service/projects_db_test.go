package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// testProjectDatabase opens the test database and adds the tables the project
// tests need.
func testProjectDatabase(t *testing.T) *gorm.DB {
	t.Helper()

	db := testDatabase(t)
	if err := db.AutoMigrate(&repository.Project{}, &repository.Session{}); err != nil {
		t.Fatalf("automigrate projects: %v", err)
	}
	return db
}

// testProjectOwner inserts a user the tests can hang projects on and removes the
// user, its projects and its sessions afterwards.
func testProjectOwner(t *testing.T, db *gorm.DB) *repository.User {
	t.Helper()

	id := uuid.New()
	user := &repository.User{
		ID:             id,
		Username:       "projecttest-" + id.String(),
		Email:          id.String() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Cleanup(func() {
		db.Where("user_id = ?", id).Delete(&repository.Session{})
		db.Where("user_id = ?", id).Delete(&repository.Project{})
		db.Delete(&repository.User{}, "id = ?", id)
	})
	return user
}

func TestCreateProjectStoresTheNameAndNormalisedPath(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)

	project, err := CreateProject(context.Background(), owner.ID, CreateProjectInput{
		ProjectName: "  Acme API ",
		ProjectPath: "acme//api/",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if project.ProjectName != "Acme API" {
		t.Errorf("project_name = %q, want %q", project.ProjectName, "Acme API")
	}
	if project.ProjectPath != "acme/api" {
		t.Errorf("project_path = %q, want %q", project.ProjectPath, "acme/api")
	}

	stored, err := repository.GetProjectByID(context.Background(), db, owner.ID, project.ID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	if stored.ProjectPath != "acme/api" {
		t.Errorf("stored project_path = %q, want %q", stored.ProjectPath, "acme/api")
	}
}

func TestCreateProjectRejectsAPathOutsideTheVolume(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)

	_, err := CreateProject(context.Background(), owner.ID, CreateProjectInput{
		ProjectName: "escape",
		ProjectPath: "../../etc",
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want a ValidationError", err)
	}
}

func TestCreateProjectRejectsADuplicateName(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)
	ctx := context.Background()

	if _, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	_, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme-2"})
	if !errors.Is(err, ErrProjectNameTaken) {
		t.Fatalf("error = %v, want ErrProjectNameTaken", err)
	}
}

func TestCreateProjectRejectsADuplicatePath(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)
	ctx := context.Background()

	if _, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	_, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "acme-2", ProjectPath: "acme"})
	if !errors.Is(err, ErrProjectPathTaken) {
		t.Fatalf("error = %v, want ErrProjectPathTaken", err)
	}
}

func TestTwoUsersMayUseTheSameProjectName(t *testing.T) {
	db := testProjectDatabase(t)
	first := testProjectOwner(t, db)
	second := testProjectOwner(t, db)
	ctx := context.Background()

	if _, err := CreateProject(ctx, first.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme"}); err != nil {
		t.Fatalf("CreateProject for the first user: %v", err)
	}
	if _, err := CreateProject(ctx, second.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme"}); err != nil {
		t.Fatalf("CreateProject for the second user: %v", err)
	}
}

func TestGetProjectHidesAnotherUsersProject(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)
	other := testProjectOwner(t, db)
	ctx := context.Background()

	project, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := GetProject(ctx, other.ID, project.ID); !errors.Is(err, repository.ErrProjectNotFound) {
		t.Fatalf("error = %v, want ErrProjectNotFound", err)
	}
}

func TestListProjectsReturnsOnlyTheCallersProjects(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)
	other := testProjectOwner(t, db)
	ctx := context.Background()

	mine, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "mine", ProjectPath: "mine"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := CreateProject(ctx, other.ID, CreateProjectInput{ProjectName: "theirs", ProjectPath: "theirs"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	projects, err := ListProjects(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != mine.ID {
		t.Fatalf("ListProjects returned %d projects, want only %s", len(projects), mine.ID)
	}
}

func TestUpdateProjectMovesThePath(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)
	ctx := context.Background()

	project, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	moved := "acme/v2"
	updated, err := UpdateProject(ctx, owner.ID, project.ID, UpdateProjectInput{ProjectPath: &moved})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if updated.ProjectPath != "acme/v2" {
		t.Errorf("project_path = %q, want %q", updated.ProjectPath, "acme/v2")
	}
	if updated.ProjectName != "acme" {
		t.Errorf("project_name = %q, want it left alone", updated.ProjectName)
	}
}

func TestDeleteProjectDetachesItsSessions(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)
	ctx := context.Background()

	project, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	session, err := CreateSession(ctx, owner.ID, CreateSessionInput{Title: "chat", ProjectID: &project.ID})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.ProjectID == nil {
		t.Fatal("a session created with a project has no project_id")
	}

	if err := DeleteProject(ctx, owner.ID, project.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	stored, err := GetSession(ctx, owner.ID, session.ID)
	if err != nil {
		t.Fatalf("GetSession after deleting the project: %v", err)
	}
	if stored.ProjectID != nil {
		t.Errorf("project_id = %v after deleting the project, want nil", stored.ProjectID)
	}
}

func TestCreateSessionRejectsAnotherUsersProject(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)
	other := testProjectOwner(t, db)
	ctx := context.Background()

	project, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	_, err = CreateSession(ctx, other.ID, CreateSessionInput{Title: "chat", ProjectID: &project.ID})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want a ValidationError", err)
	}

	if sessions, err := ListSessions(ctx, other.ID, nil, nil); err != nil {
		t.Fatalf("ListSessions: %v", err)
	} else if len(sessions) != 0 {
		t.Fatalf("%d sessions were stored despite the rejected project", len(sessions))
	}
}

func TestUpdateSessionDetachesWithAnExplicitNull(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)
	ctx := context.Background()

	project, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	session, err := CreateSession(ctx, owner.ID, CreateSessionInput{Title: "chat", ProjectID: &project.ID})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	title := "renamed"
	untouched, err := UpdateSession(ctx, owner.ID, session.ID, UpdateSessionInput{Title: &title})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if untouched.ProjectID == nil {
		t.Fatal("a title-only update cleared project_id")
	}

	detached, err := UpdateSession(ctx, owner.ID, session.ID, UpdateSessionInput{ProjectID: utils.Optional[uuid.UUID]{Set: true, Value: nil}})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if detached.ProjectID != nil {
		t.Errorf("project_id = %v after an explicit null, want nil", detached.ProjectID)
	}
}

func TestListSessionsFiltersByProject(t *testing.T) {
	db := testProjectDatabase(t)
	owner := testProjectOwner(t, db)
	ctx := context.Background()

	project, err := CreateProject(ctx, owner.ID, CreateProjectInput{ProjectName: "acme", ProjectPath: "acme"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	inProject, err := CreateSession(ctx, owner.ID, CreateSessionInput{Title: "in", ProjectID: &project.ID})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := CreateSession(ctx, owner.ID, CreateSessionInput{Title: "out"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sessions, err := ListSessions(ctx, owner.ID, nil, &project.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != inProject.ID {
		t.Fatalf("ListSessions returned %d sessions, want only %s", len(sessions), inProject.ID)
	}
}
