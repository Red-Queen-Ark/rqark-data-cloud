package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrTaskNotFound is returned when no task matches the lookup. It also covers a
// task owned by another user, so task ids cannot be enumerated.
var ErrTaskNotFound = errors.New("repository: task not found")

// ScheduleKind values a task may carry. A task names exactly one of them, and
// the CHECK constraint on tasks requires the matching column to be the only one
// set, so the three shapes cannot be mixed.
const (
	TaskScheduleAt    = "at"
	TaskScheduleEvery = "every"
	TaskScheduleCron  = "cron"
)

// Task is the tasks table: one scheduled job of one user. The job runs a prompt
// against an agent and a provider the user may use, both of which travel as
// names rather than ids because the app addresses them by name and the row has
// to survive an agent being re-created.
//
// (user_id, name) is unique because the name is what the user types in the task
// listing and what a run titles its session with, so two tasks of one user
// sharing a name would be indistinguishable.
//
// next_run_at is the only scheduling column the dispatcher reads: it is set when
// a task is written or re-enabled and advanced only inside the claim
// transaction. A disabled task carries no next_run_at.
type Task struct {
	ID     uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:idx_tasks_user_id_name,priority:1"`
	Name   string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_tasks_user_id_name,priority:2"`
	// Prompt is the text handed to the agent at every run.
	Prompt string `gorm:"type:text;not null;default:''"`
	// AgentName and ProviderName are soft references: they are checked against
	// what the user may use when the task is written, and resolved again at
	// claim time, so neither an id nor a foreign key is kept here.
	AgentName    string `gorm:"type:varchar(255);not null;default:''"`
	ProviderName string `gorm:"type:varchar(255);not null;default:''"`
	// Timezone is the IANA name a cron expression is evaluated in. It is stored
	// with the task because the server otherwise runs in UTC and would silently
	// ignore it.
	Timezone string `gorm:"type:varchar(64);not null;default:'UTC'"`
	// Enabled gates the dispatcher. The composite index serves the scan for due
	// tasks, which filters on both columns.
	//
	// It carries no `default` tag on purpose: GORM substitutes a default for the
	// zero value on create and writes that back into the struct, so `default:true`
	// would silently turn a task the caller asked to be paused into an enabled one
	// with no next_run_at — a task nothing would ever pick up.
	Enabled bool `gorm:"not null;index:idx_tasks_enabled_next_run_at,priority:1"`
	// ScheduleKind selects which of the three schedule columns is set. The CHECK
	// constraint pins the shapes, so a row cannot carry two schedules at once.
	ScheduleKind string `gorm:"type:varchar(16);not null;check:chk_tasks_schedule_kind,(schedule_kind = 'at' AND run_at IS NOT NULL AND every_seconds IS NULL AND cron_expr IS NULL) OR (schedule_kind = 'every' AND run_at IS NULL AND every_seconds >= 60 AND cron_expr IS NULL) OR (schedule_kind = 'cron' AND run_at IS NULL AND every_seconds IS NULL AND cron_expr IS NOT NULL)"`
	// RunAt is the single instant an "at" task fires at.
	RunAt *time.Time `gorm:"type:timestamptz"`
	// EverySeconds is the gap between two runs of an "every" task. The CHECK
	// constraint refuses a gap below a minute, which is the precision a minute
	// tick can honour.
	EverySeconds *int `gorm:"type:integer"`
	// CronExpr is the five-field expression of a "cron" task, evaluated in
	// Timezone.
	CronExpr *string `gorm:"type:varchar(255)"`
	// NextRunAt is the instant the dispatcher may claim the task at next. It is
	// null exactly when the task is disabled.
	NextRunAt *time.Time `gorm:"type:timestamptz;index:idx_tasks_enabled_next_run_at,priority:2"`
	// RunNowRequestedAt asks for one extra run at the given instant without
	// moving next_run_at, so a manual run never shifts an every/cron cadence.
	RunNowRequestedAt *time.Time `gorm:"type:timestamptz"`
	CreatedAt         time.Time  `gorm:"not null"`
	UpdatedAt         time.Time  `gorm:"not null"`

	// User only exists to tie user_id to users.id with a foreign key. It is
	// never loaded or written through this struct.
	User User `gorm:"foreignKey:UserID;references:ID"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (Task) TableName() string { return "tasks" }

// GetTaskByID loads one of userID's tasks by primary key. Another user's task is
// reported as ErrTaskNotFound.
func GetTaskByID(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) (*Task, error) {
	var task Task
	if err := db.WithContext(ctx).First(&task, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, taskLookupError(err)
	}
	return &task, nil
}

// ListTasks returns every task of userID ordered by name. Tasks are not
// paginated: a user has few of them, and the app wants the whole set at once,
// the same way it reads its agents.
func ListTasks(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]Task, error) {
	tasks := []Task{}
	err := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("name").Order("id").
		Find(&tasks).Error
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

// CreateTask inserts a task.
func CreateTask(ctx context.Context, db *gorm.DB, task *Task) error {
	return db.WithContext(ctx).Create(task).Error
}

// SaveTask writes every column of task.
func SaveTask(ctx context.Context, db *gorm.DB, task *Task) error {
	return db.WithContext(ctx).Save(task).Error
}

// UpdateTaskFields writes only the named columns of one task.
//
// The dispatcher writes next_run_at and run_now_requested_at from the claim
// transaction, so a request path that saved the whole row would write back the
// values it read before that transaction committed: a slot the claim had already
// used, a manual request the claim had already honoured, or an enabled flag set
// after the user paused the task. A targeted update leaves every column the
// caller did not name alone.
func UpdateTaskFields(ctx context.Context, db *gorm.DB, userID, id uuid.UUID, fields map[string]any) error {
	result := db.WithContext(ctx).
		Model(&Task{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// RequestRunNow records a manual run request on one task, in a single statement.
//
// The guards are part of the update rather than a read followed by a write, so a
// claim that commits in between cannot be undone: a task paused meanwhile is not
// revived, and a task that started running meanwhile does not get a second
// request queued behind the one already running.
func RequestRunNow(ctx context.Context, db *gorm.DB, userID, id uuid.UUID, at time.Time) (bool, error) {
	result := db.WithContext(ctx).
		Model(&Task{}).
		Where("id = ? AND user_id = ? AND enabled = ?", id, userID, true).
		Where("NOT EXISTS (SELECT 1 FROM task_runs r WHERE r.task_id = tasks.id AND r.status = ?)", TaskRunRunning).
		Update("run_now_requested_at", at)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// DeleteTask removes one of userID's tasks and, through the foreign key, the
// runs recorded for it.
func DeleteTask(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) error {
	result := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&Task{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// TaskNameTaken reports whether another task of userID already owns name.
func TaskNameTaken(ctx context.Context, db *gorm.DB, userID uuid.UUID, name string, excludeID uuid.UUID) (bool, error) {
	query := db.WithContext(ctx).Model(&Task{}).Where("user_id = ? AND name = ?", userID, name)
	if excludeID != uuid.Nil {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// taskLookupError maps GORM's not-found error onto the repository's own.
func taskLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrTaskNotFound
	}
	return err
}
