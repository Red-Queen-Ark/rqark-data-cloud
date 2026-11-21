package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"rqark-data-cloud/internal/repository"
)

// checkViolation is the PostgreSQL SQLSTATE for a CHECK constraint violation.
// The unique violation code lives in errors.go, next to the mapping it feeds.
const checkViolation = "23514"

// taskTestDB opens the shared test database and installs the task tables on top
// of it. AutoMigrate only writes the CHECK constraint while it creates the
// table, so a tasks table left behind by an earlier shape is rebuilt first: the
// tests below would otherwise pass a table that never had the constraint.
func taskTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := testDatabase(t)
	if db.Migrator().HasTable(&repository.Task{}) && !db.Migrator().HasConstraint(&repository.Task{}, "chk_tasks_schedule_kind") {
		if err := db.Migrator().DropTable(&repository.TaskRun{}, &repository.Task{}); err != nil {
			t.Fatalf("drop stale task tables: %v", err)
		}
	}
	if err := db.AutoMigrate(&repository.Task{}, &repository.TaskRun{}); err != nil {
		t.Fatalf("automigrate tasks: %v", err)
	}
	return db
}

// testTaskOwner inserts a user the task tests can hang tasks on and removes the
// user, its tasks and its runs afterwards.
func testTaskOwner(t *testing.T, db *gorm.DB) *repository.User {
	t.Helper()

	id := uuid.New()
	user := &repository.User{
		ID:             id,
		Username:       "tasktest-" + id.String(),
		Email:          id.String() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Cleanup(func() {
		db.Where("user_id = ?", id).Delete(&repository.TaskRun{})
		db.Where("user_id = ?", id).Delete(&repository.Task{})
		db.Delete(&repository.User{}, "id = ?", id)
	})
	return user
}

// newTestTask stores one valid "at" task for owner, firing an hour from now.
func newTestTask(t *testing.T, db *gorm.DB, owner *repository.User) *repository.Task {
	t.Helper()

	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	task := &repository.Task{
		ID:           uuid.New(),
		UserID:       owner.ID,
		Name:         "task-" + uuid.New().String(),
		Prompt:       "summarise the inbox",
		AgentName:    "assistant",
		ProviderName: "openai",
		Timezone:     "UTC",
		Enabled:      true,
		ScheduleKind: repository.TaskScheduleAt,
		RunAt:        &at,
		NextRunAt:    &at,
	}
	if err := repository.CreateTask(context.Background(), db, task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// pgError fails the test unless err carries a PostgreSQL error, and returns it.
func pgError(t *testing.T, err error) *pgconn.PgError {
	t.Helper()

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %v, want a PostgreSQL error", err)
	}
	return pgErr
}

// TestTaskAutoMigrateIsRepeatable covers the migration running again on a
// database that already holds both tables: the indexes and the CHECK constraint
// must not be recreated or rejected.
func TestTaskAutoMigrateIsRepeatable(t *testing.T) {
	db := taskTestDB(t)

	if err := db.AutoMigrate(&repository.Task{}, &repository.TaskRun{}); err != nil {
		t.Fatalf("second automigrate: %v", err)
	}
}

// TestTaskSchemaCarriesItsIndexesAndCheck covers the storage the dispatcher
// relies on: the unique name per user, the due scan on (enabled, next_run_at),
// the three task_runs indexes, and the schedule CHECK.
func TestTaskSchemaCarriesItsIndexesAndCheck(t *testing.T) {
	db := taskTestDB(t)

	for _, name := range []string{
		"idx_tasks_user_id_name",
		"idx_tasks_enabled_next_run_at",
	} {
		if !db.Migrator().HasIndex(&repository.Task{}, name) {
			t.Errorf("tasks is missing index %s", name)
		}
	}
	for _, name := range []string{
		"idx_task_runs_task_id_scheduled_at",
		"idx_task_runs_task_id_status",
		"idx_task_runs_status_started_at",
	} {
		if !db.Migrator().HasIndex(&repository.TaskRun{}, name) {
			t.Errorf("task_runs is missing index %s", name)
		}
	}
	if !db.Migrator().HasConstraint(&repository.Task{}, "chk_tasks_schedule_kind") {
		t.Error("tasks is missing check constraint chk_tasks_schedule_kind")
	}
}

// TestTaskAcceptsEachScheduleShape covers the three shapes the CHECK admits, so
// that the constraint is not read as stricter than it is.
func TestTaskAcceptsEachScheduleShape(t *testing.T) {
	db := taskTestDB(t)
	owner := testTaskOwner(t, db)

	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	every := 3600
	expr := "0 9 * * *"
	shapes := map[string]repository.Task{
		"at": {
			ScheduleKind: repository.TaskScheduleAt,
			RunAt:        &at,
			NextRunAt:    &at,
		},
		"every": {
			ScheduleKind: repository.TaskScheduleEvery,
			EverySeconds: &every,
		},
		"cron": {
			ScheduleKind: repository.TaskScheduleCron,
			CronExpr:     &expr,
		},
	}

	for name, shape := range shapes {
		t.Run(name, func(t *testing.T) {
			shape.ID = uuid.New()
			shape.UserID = owner.ID
			shape.Name = "shape-" + name
			shape.Prompt = "say hello"
			shape.Timezone = "UTC"
			shape.Enabled = true
			if err := repository.CreateTask(context.Background(), db, &shape); err != nil {
				t.Fatalf("create %s task: %v", name, err)
			}
		})
	}
}

// TestTaskRefusesAMixedSchedule covers the CHECK: a row may not name a kind
// whose column is absent, or carry a column belonging to another kind.
func TestTaskRefusesAMixedSchedule(t *testing.T) {
	db := taskTestDB(t)
	owner := testTaskOwner(t, db)

	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	everyTooShort := 59
	everyAllowed := 600
	expr := "0 9 * * *"

	cases := map[string]repository.Task{
		"at without run_at": {
			ScheduleKind: repository.TaskScheduleAt,
		},
		"at with every_seconds": {
			ScheduleKind: repository.TaskScheduleAt,
			RunAt:        &at,
			EverySeconds: &everyAllowed,
		},
		"every below a minute": {
			ScheduleKind: repository.TaskScheduleEvery,
			EverySeconds: &everyTooShort,
		},
		"cron without cron_expr": {
			ScheduleKind: repository.TaskScheduleCron,
		},
		"cron with every_seconds": {
			ScheduleKind: repository.TaskScheduleCron,
			EverySeconds: &everyAllowed,
			CronExpr:     &expr,
		},
		"unknown kind": {
			ScheduleKind: "later",
			RunAt:        &at,
		},
	}

	for name, shape := range cases {
		t.Run(name, func(t *testing.T) {
			shape.ID = uuid.New()
			shape.UserID = owner.ID
			shape.Name = "bad-" + name
			shape.Enabled = true

			err := repository.CreateTask(context.Background(), db, &shape)
			if err == nil {
				t.Fatal("create with a mixed schedule was accepted")
			}
			pgErr := pgError(t, err)
			if pgErr.Code != checkViolation {
				t.Fatalf("sqlstate = %s, want %s", pgErr.Code, checkViolation)
			}
			if pgErr.ConstraintName != "chk_tasks_schedule_kind" {
				t.Errorf("constraint = %s, want chk_tasks_schedule_kind", pgErr.ConstraintName)
			}
		})
	}
}

// TestTaskRefusesADuplicateName covers the unique name per user, and that the
// service recognises the violation rather than reporting an internal error.
func TestTaskRefusesADuplicateName(t *testing.T) {
	db := taskTestDB(t)
	owner := testTaskOwner(t, db)
	first := newTestTask(t, db, owner)

	second := *first
	second.ID = uuid.New()

	err := repository.CreateTask(context.Background(), db, &second)
	if err == nil {
		t.Fatal("a second task with the same name was accepted")
	}
	pgErr := pgError(t, err)
	if pgErr.ConstraintName != "idx_tasks_user_id_name" {
		t.Errorf("constraint = %s, want idx_tasks_user_id_name", pgErr.ConstraintName)
	}
	if mapped := mapWriteError(err); !errors.Is(mapped, ErrTaskNameTaken) {
		t.Errorf("mapWriteError = %v, want %v", mapped, ErrTaskNameTaken)
	}
}

// TestTaskRunKeepsOneRunPerSlot covers the dedupe key the dispatcher relies on:
// a second row for the same task and scheduled instant conflicts instead of
// being added.
func TestTaskRunKeepsOneRunPerSlot(t *testing.T) {
	db := taskTestDB(t)
	owner := testTaskOwner(t, db)
	task := newTestTask(t, db, owner)

	slot := time.Now().UTC().Truncate(time.Second)
	first := &repository.TaskRun{
		ID:          uuid.New(),
		TaskID:      task.ID,
		UserID:      owner.ID,
		Status:      repository.TaskRunRunning,
		Trigger:     repository.TaskRunTriggerSchedule,
		ScheduledAt: slot,
		StartedAt:   &slot,
	}
	if err := repository.CreateTaskRun(context.Background(), db, first); err != nil {
		t.Fatalf("create run: %v", err)
	}

	second := *first
	second.ID = uuid.New()
	err := repository.CreateTaskRun(context.Background(), db, &second)
	if err == nil {
		t.Fatal("a second run for the same slot was accepted")
	}
	pgErr := pgError(t, err)
	if pgErr.ConstraintName != "idx_task_runs_task_id_scheduled_at" {
		t.Errorf("constraint = %s, want idx_task_runs_task_id_scheduled_at", pgErr.ConstraintName)
	}
}

// TestGetTaskHidesAnotherUsersTask covers the user scoping: one user's id reads
// nothing of another's task.
func TestGetTaskHidesAnotherUsersTask(t *testing.T) {
	db := taskTestDB(t)
	owner := testTaskOwner(t, db)
	viewer := testTaskOwner(t, db)
	task := newTestTask(t, db, owner)

	stored, err := repository.GetTaskByID(context.Background(), db, owner.ID, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID of the owner: %v", err)
	}
	if stored.ID != task.ID || stored.Name != task.Name {
		t.Errorf("task = %s %q, want %s %q", stored.ID, stored.Name, task.ID, task.Name)
	}

	if _, err := repository.GetTaskByID(context.Background(), db, viewer.ID, task.ID); !errors.Is(err, repository.ErrTaskNotFound) {
		t.Errorf("GetTaskByID of another user = %v, want %v", err, repository.ErrTaskNotFound)
	}
}

// TestDeleteTaskTakesItsRunsWithIt covers the cascade: a deleted task leaves no
// run rows behind.
func TestDeleteTaskTakesItsRunsWithIt(t *testing.T) {
	db := taskTestDB(t)
	owner := testTaskOwner(t, db)
	task := newTestTask(t, db, owner)

	slot := time.Now().UTC().Truncate(time.Second)
	run := &repository.TaskRun{
		ID:          uuid.New(),
		TaskID:      task.ID,
		UserID:      owner.ID,
		Status:      repository.TaskRunOK,
		Trigger:     repository.TaskRunTriggerSchedule,
		ScheduledAt: slot,
		StartedAt:   &slot,
		FinishedAt:  &slot,
	}
	if err := repository.CreateTaskRun(context.Background(), db, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := repository.DeleteTask(context.Background(), db, owner.ID, task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	var runs int64
	if err := db.Model(&repository.TaskRun{}).Where("task_id = ?", task.ID).Count(&runs).Error; err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 0 {
		t.Errorf("runs = %d, want 0 after the task was deleted", runs)
	}

	if err := repository.DeleteTask(context.Background(), db, owner.ID, task.ID); !errors.Is(err, repository.ErrTaskNotFound) {
		t.Errorf("second DeleteTask = %v, want %v", err, repository.ErrTaskNotFound)
	}
}
