package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Run status and trigger values. They are stored as plain text: the status set
// is the one the dispatcher writes, and a value a later release adds has to
// round-trip without a schema change.
const (
	TaskRunRunning   = "running"
	TaskRunOK        = "ok"
	TaskRunError     = "error"
	TaskRunCancelled = "cancelled"
	TaskRunSkipped   = "skipped"
	TaskRunLost      = "lost"

	TaskRunTriggerSchedule = "schedule"
	TaskRunTriggerManual   = "manual"
)

// TaskRun is the task_runs table: one attempt of one task at one scheduled
// instant. (task_id, scheduled_at) is unique, which is how the dispatcher keeps
// a slot to a single run however many workers tick at once: the insert that
// loses the race conflicts instead of adding a second row.
//
// UserID repeats the task's owner so the dispatcher can hold a user to one
// running run without joining tasks on every candidate.
//
// StartedAt is written by the claim that inserts the row, so a running row means
// the run really started. A run that is claimed and then never finalized is
// swept to lost rather than retried: the schedule is fire-once.
type TaskRun struct {
	ID     uuid.UUID `gorm:"type:uuid;primaryKey"`
	TaskID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_task_runs_task_id_scheduled_at,priority:1;index:idx_task_runs_task_id_status,priority:1"`
	UserID uuid.UUID `gorm:"type:uuid;not null;index"`
	Status string    `gorm:"type:varchar(16);not null;index:idx_task_runs_task_id_status,priority:2;index:idx_task_runs_status_started_at,priority:1"`
	// Trigger says what asked for the run: the schedule, or a user's run_now.
	Trigger string `gorm:"type:varchar(16);not null;default:'schedule'"`
	// ScheduledAt is the instant the run belongs to. It is the dedupe key, so a
	// manual run uses the instant it was requested at rather than "now".
	ScheduledAt time.Time  `gorm:"type:timestamptz;not null;uniqueIndex:idx_task_runs_task_id_scheduled_at,priority:2"`
	StartedAt   *time.Time `gorm:"type:timestamptz;index:idx_task_runs_status_started_at,priority:2"`
	FinishedAt  *time.Time `gorm:"type:timestamptz"`
	// Error carries the reason a run did not end ok, empty otherwise.
	Error string `gorm:"type:text;not null;default:''"`
	// SessionID names the session the run writes its transcript to. It is null
	// for a run that was skipped before a session was opened.
	SessionID *uuid.UUID `gorm:"type:uuid"`
	// Task only exists to tie task_id to tasks.id with a foreign key that drops
	// the runs together with their task. It is never loaded or written through
	// this struct.
	Task Task `gorm:"foreignKey:TaskID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (TaskRun) TableName() string { return "task_runs" }

// CreateTaskRun inserts a run row.
func CreateTaskRun(ctx context.Context, db *gorm.DB, run *TaskRun) error {
	return db.WithContext(ctx).Create(run).Error
}

// SaveTaskRun writes every column of run.
func SaveTaskRun(ctx context.Context, db *gorm.DB, run *TaskRun) error {
	return db.WithContext(ctx).Save(run).Error
}

// ListTaskRuns returns one page of a task's runs, newest scheduled instant
// first. Runs are paginated because they grow without bound: every fire of every
// task adds a row.
func ListTaskRuns(ctx context.Context, db *gorm.DB, taskID uuid.UUID, limit, offset int) ([]TaskRun, error) {
	runs := []TaskRun{}
	err := db.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order("scheduled_at DESC").Order("id").
		Limit(limit).Offset(offset).
		Find(&runs).Error
	if err != nil {
		return nil, err
	}
	return runs, nil
}

// CountTaskRuns returns how many runs a task has, whatever the page asked for.
func CountTaskRuns(ctx context.Context, db *gorm.DB, taskID uuid.UUID) (int64, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&TaskRun{}).Where("task_id = ?", taskID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
