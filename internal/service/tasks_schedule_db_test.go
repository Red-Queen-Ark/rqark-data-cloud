package service

import (
	"context"
	"testing"
	"time"

	"rqark-data-cloud/internal/repository"
)

// TestClaimSkipsADeactivatedUsersTasks covers the dispatcher's only user check: an
// account that was switched off keeps its tasks, so that reactivating it resumes
// them, but nothing of its runs while it is off.
func TestClaimSkipsADeactivatedUsersTasks(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	newDueTask(t, db, owner, "while-away", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))

	if err := db.Model(&repository.User{}).Where("id = ?", owner.ID).Update("is_active", false).Error; err != nil {
		t.Fatalf("deactivate the user: %v", err)
	}

	claimed, err := ClaimTaskRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("ClaimTaskRuns: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d runs for a deactivated account, want 0", len(claimed))
	}

	// The task itself is untouched: reactivating the account brings it back
	// rather than leaving it switched off.
	stored, err := repository.ListTasks(context.Background(), db, owner.ID)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(stored) != 1 || !stored[0].Enabled {
		t.Fatalf("tasks = %+v, want the one task still enabled", stored)
	}

	if err := db.Model(&repository.User{}).Where("id = ?", owner.ID).Update("is_active", true).Error; err != nil {
		t.Fatalf("reactivate the user: %v", err)
	}

	claimed, err = ClaimTaskRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("ClaimTaskRuns after reactivation: %v", err)
	}
	if len(claimed) != 1 {
		t.Errorf("claimed %d runs after reactivation, want 1", len(claimed))
	}
}

// TestEveryTaskLandsOnTheMinute covers what the grid is for, end to end: a task
// created at an arbitrary instant, and every slot after it, falls on a whole
// minute.
func TestEveryTaskLandsOnTheMinute(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	every := 60
	in := newTaskInput("on-the-minute")
	in.Schedule = TaskScheduleInput{Kind: repository.TaskScheduleEvery, EverySeconds: &every}

	task, err := CreateTask(context.Background(), owner.ID, in)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.NextRunAt == nil {
		t.Fatal("next_run_at = nil for an enabled every task")
	}
	if second, nanos := task.NextRunAt.Second(), task.NextRunAt.Nanosecond(); second != 0 || nanos != 0 {
		t.Errorf("next_run_at = %s, want a whole minute", task.NextRunAt)
	}

	// Make it due, claim it, and read the slot the claim moved it to.
	due := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&repository.Task{}).Where("id = ?", task.ID).Update("next_run_at", due).Error; err != nil {
		t.Fatalf("make the task due: %v", err)
	}
	if claimed, err := ClaimTaskRuns(context.Background(), 1); err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %d runs, %v; want 1, nil", len(claimed), err)
	}

	stored, err := repository.GetTaskByID(context.Background(), db, owner.ID, task.ID)
	if err != nil {
		t.Fatalf("re-read the task: %v", err)
	}
	if stored.NextRunAt == nil {
		t.Fatal("next_run_at = nil after the claim")
	}
	if second, nanos := stored.NextRunAt.Second(), stored.NextRunAt.Nanosecond(); second != 0 || nanos != 0 {
		t.Errorf("next_run_at = %s after the claim, want a whole minute", stored.NextRunAt)
	}
	if !stored.NextRunAt.After(time.Now()) {
		t.Errorf("next_run_at = %s, want an instant still ahead", stored.NextRunAt)
	}
}

// TestEveryTaskDoesNotRunABacklog covers the other half of the grid: a run that
// outlived several of its slots is followed by one run, not a burst of them.
func TestEveryTaskDoesNotRunABacklog(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	// A task whose slot is ten minutes stale, written straight into the table so
	// the claim sees it as one that was missed.
	task := newDueTask(t, db, owner, "overran", repository.TaskScheduleEvery, time.Now().Add(-10*time.Minute))
	if task.EverySeconds == nil || *task.EverySeconds != 600 {
		t.Fatalf("every_seconds = %v, want 600", task.EverySeconds)
	}

	if claimed, err := ClaimTaskRuns(context.Background(), 1); err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %d runs, %v; want 1, nil", len(claimed), err)
	}

	stored, err := repository.GetTaskByID(context.Background(), db, owner.ID, task.ID)
	if err != nil {
		t.Fatalf("re-read the task: %v", err)
	}
	if stored.NextRunAt == nil || !stored.NextRunAt.After(time.Now()) {
		t.Fatalf("next_run_at = %v, want an instant still ahead", stored.NextRunAt)
	}
	if second := stored.NextRunAt.Second(); second != 0 {
		t.Errorf("next_run_at = %s, want a whole minute", stored.NextRunAt)
	}
}
