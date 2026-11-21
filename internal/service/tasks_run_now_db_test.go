package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"rqark-data-cloud/internal/repository"
)

// TestRunTaskNowLeavesTheScheduleAlone covers the rule that makes a manual run
// safe: the schedule keeps the instant it already had, so an every task's phase
// does not drift and a cron task's next fire is not skipped.
func TestRunTaskNowLeavesTheScheduleAlone(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "hourly", repository.TaskScheduleEvery, time.Now().Add(time.Hour))
	before := *task.NextRunAt

	updated, err := RunTaskNow(context.Background(), owner.ID, task.ID)
	if err != nil {
		t.Fatalf("RunTaskNow: %v", err)
	}
	if updated.RunNowRequestedAt == nil {
		t.Fatal("run_now_requested_at is unset, want the request recorded")
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.Equal(before) {
		t.Errorf("next_run_at = %v, want the untouched %v", updated.NextRunAt, before)
	}
}

// TestRunTaskClaimsAManualRun covers the other half: the request turns into one
// run with trigger manual, and the field is cleared so the same request cannot
// fire twice.
func TestRunTaskClaimsAManualRun(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "hourly", repository.TaskScheduleEvery, time.Now().Add(time.Hour))
	before := *task.NextRunAt

	if _, err := RunTaskNow(context.Background(), owner.ID, task.ID); err != nil {
		t.Fatalf("RunTaskNow: %v", err)
	}

	claimed, err := ClaimTaskRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("ClaimTaskRuns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d runs, want the manual one", len(claimed))
	}

	runs := runsOf(t, db, task.ID)
	if len(runs) != 1 || runs[0].Trigger != repository.TaskRunTriggerManual {
		t.Fatalf("runs = %+v, want one manual run", runs)
	}

	stored, err := repository.GetTaskByID(context.Background(), db, owner.ID, task.ID)
	if err != nil {
		t.Fatalf("re-read task: %v", err)
	}
	if stored.RunNowRequestedAt != nil {
		t.Error("run_now_requested_at survived the claim, so the run would fire again")
	}
	if stored.NextRunAt == nil || !stored.NextRunAt.Equal(before) {
		t.Errorf("next_run_at = %v, want the untouched %v", stored.NextRunAt, before)
	}
}

// TestRunTaskNowIsRefusedWhilePaused covers the paused task: its schedule is off,
// so a one-off run would be a surprise rather than a shortcut.
func TestRunTaskNowIsRefusedWhilePaused(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	task := newDueTask(t, db, owner, "paused", repository.TaskScheduleEvery, time.Now().Add(time.Hour))
	off := false
	if _, err := UpdateTask(context.Background(), owner.ID, task.ID, UpdateTaskInput{Enabled: &off}); err != nil {
		t.Fatalf("pause the task: %v", err)
	}

	if _, err := RunTaskNow(context.Background(), owner.ID, task.ID); !errors.Is(err, ErrTaskDisabled) {
		t.Errorf("RunTaskNow on a paused task = %v, want %v", err, ErrTaskDisabled)
	}
}

// TestRunTaskNowIsRefusedWhileRunning covers the concurrency rule at the user
// boundary: asking for a run of a task that is already running is a conflict
// rather than a request that would sit behind the current one.
func TestRunTaskNowIsRefusedWhileRunning(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "hourly", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))
	if claimed, err := ClaimTaskRuns(context.Background(), 1); err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %d runs, %v; want 1, nil", len(claimed), err)
	}

	if _, err := RunTaskNow(context.Background(), owner.ID, task.ID); !errors.Is(err, ErrTaskRunInFlight) {
		t.Errorf("RunTaskNow on a running task = %v, want %v", err, ErrTaskRunInFlight)
	}
}

// TestRunTaskNowHidesAnotherUsersTask covers the user scope: a task id alone does
// not let anyone ask for a run.
func TestRunTaskNowHidesAnotherUsersTask(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	viewer := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	task := newDueTask(t, db, owner, "hourly", repository.TaskScheduleEvery, time.Now().Add(time.Hour))

	if _, err := RunTaskNow(context.Background(), viewer.ID, task.ID); !errors.Is(err, repository.ErrTaskNotFound) {
		t.Errorf("RunTaskNow of another user's task = %v, want %v", err, repository.ErrTaskNotFound)
	}
	if _, err := RunTaskNow(context.Background(), owner.ID, uuid.New()); !errors.Is(err, repository.ErrTaskNotFound) {
		t.Errorf("RunTaskNow of an unknown task = %v, want %v", err, repository.ErrTaskNotFound)
	}
}
