package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gorm.io/gorm"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/repository"
	"rqark-data-cloud/internal/utils"
)

// claimTestDB opens a database the claim tests may empty.
//
// The claim is global by design — it walks every user's due tasks — so a test
// that wants to see exactly its own task has to start from an empty table. That
// makes it destructive, and it is only ever pointed at a database of its own:
// the DSN has to name a database ending in _test, otherwise these tests skip
// rather than wipe whatever the developer is working with.
//
// Every other task test is safe on a shared database, because it only ever
// writes rows belonging to the throwaway users it creates. This one cannot be,
// so it refuses to run where it could do damage.
func claimTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	if !strings.HasSuffix(testDatabaseName(t), "_test") {
		t.Skip("the claim tests empty the task tables, so they only run against a database whose name ends in _test")
	}

	db := taskWriteDB(t)
	if err := db.AutoMigrate(&repository.Session{}); err != nil {
		t.Fatalf("automigrate sessions: %v", err)
	}
	for _, statement := range []string{
		"DELETE FROM task_runs",
		"DELETE FROM tasks WHERE user_id IN (SELECT id FROM users WHERE username LIKE 'tasktest-%')",
		"DELETE FROM sessions WHERE source = 'TASK'",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return db
}

// testDatabaseName reads the database name out of TEST_POSTGRES_DSN, so a test
// can decide whether it is allowed to empty that database.
func testDatabaseName(t *testing.T) string {
	t.Helper()

	config_, err := pgx.ParseConfig(os.Getenv("TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatalf("cannot parse TEST_POSTGRES_DSN: %v", err)
	}
	return config_.Database
}

// claimTestConfig sets the run clock the claim reads, and restores it afterwards
// so one test cannot change what another sees.
func claimTestConfig(t *testing.T, atGrace, staleAfter time.Duration) {
	t.Helper()

	previousGrace, previousStale := config.TaskAtGrace, config.TaskRunStaleAfter
	config.TaskAtGrace, config.TaskRunStaleAfter = atGrace, staleAfter
	t.Cleanup(func() {
		config.TaskAtGrace, config.TaskRunStaleAfter = previousGrace, previousStale
	})
}

// newDueTask stores a task of owner that is already due, with the given kind.
func newDueTask(t *testing.T, db *gorm.DB, owner *repository.User, name, kind string, dueAt time.Time) *repository.Task {
	t.Helper()

	at := dueAt.UTC().Truncate(time.Second)
	every := 600
	expr := "0 9 * * *"

	task := &repository.Task{
		ID:           uuid.New(),
		UserID:       owner.ID,
		Name:         name,
		Prompt:       "summarise the inbox",
		AgentName:    "assistant",
		ProviderName: "openai",
		Timezone:     "UTC",
		Enabled:      true,
		ScheduleKind: kind,
		NextRunAt:    &at,
	}
	switch kind {
	case repository.TaskScheduleAt:
		task.RunAt = &at
	case repository.TaskScheduleEvery:
		task.EverySeconds = &every
	case repository.TaskScheduleCron:
		task.CronExpr = &expr
	}
	if err := repository.CreateTask(context.Background(), db, task); err != nil {
		t.Fatalf("create due task: %v", err)
	}
	return task
}

// runsOf returns every run recorded for a task, oldest slot first.
func runsOf(t *testing.T, db *gorm.DB, taskID uuid.UUID) []repository.TaskRun {
	t.Helper()

	runs := []repository.TaskRun{}
	if err := db.Where("task_id = ?", taskID).Order("scheduled_at").Find(&runs).Error; err != nil {
		t.Fatalf("read runs: %v", err)
	}
	return runs
}

// TestClaimDispatchesOneDueTask covers the happy path: a run row that is already
// running, the session it writes to, the advanced schedule, and a token bound to
// that session.
func TestClaimDispatchesOneDueTask(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "morning", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))

	claimed, err := ClaimTaskRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("ClaimTaskRuns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d runs, want 1", len(claimed))
	}

	run := claimed[0]
	if run.Task.ID != task.ID {
		t.Errorf("claimed task %s, want %s", run.Task.ID, task.ID)
	}
	if run.Agent.Name != "assistant" || run.Provider.Name != "openai" {
		t.Errorf("resolved %q / %q, want assistant / openai", run.Agent.Name, run.Provider.Name)
	}

	claims, err := utils.ParseTokenClaims(run.ActorToken, []byte(config.SecretKey))
	if err != nil {
		t.Fatalf("ParseTokenClaims: %v", err)
	}
	if claims.Scope != utils.ScopeRun || claims.SessionID != run.SessionID || claims.TaskID != task.ID || claims.UserID != owner.ID {
		t.Errorf("token = scope %q task %s session %s user %s, want run/%s/%s/%s", claims.Scope, claims.TaskID, claims.SessionID, claims.UserID, task.ID, run.SessionID, owner.ID)
	}

	session, err := repository.GetSessionByID(context.Background(), db, owner.ID, run.SessionID)
	if err != nil {
		t.Fatalf("the run's session: %v", err)
	}
	if session.Source != TaskSessionSource || session.Title != task.Name {
		t.Errorf("session = source %q title %q, want %q / %q", session.Source, session.Title, TaskSessionSource, task.Name)
	}

	runs := runsOf(t, db, task.ID)
	if len(runs) != 1 || runs[0].Status != repository.TaskRunRunning || runs[0].StartedAt == nil {
		t.Fatalf("runs = %+v, want one running row with a start", runs)
	}
	if runs[0].Trigger != repository.TaskRunTriggerSchedule {
		t.Errorf("trigger = %q, want %q", runs[0].Trigger, repository.TaskRunTriggerSchedule)
	}

	stored, err := repository.GetTaskByID(context.Background(), db, owner.ID, task.ID)
	if err != nil {
		t.Fatalf("re-read task: %v", err)
	}
	if stored.NextRunAt == nil || !stored.NextRunAt.After(*task.NextRunAt) {
		t.Errorf("next_run_at = %v, want an instant after %v", stored.NextRunAt, *task.NextRunAt)
	}
}

// TestClaimTwiceTakesOneSlot covers the unique index under concurrency: two
// workers ticking at the same moment produce one run, not two.
func TestClaimTwiceTakesOneSlot(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "morning", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))

	var wg sync.WaitGroup
	total := 0
	var mutex sync.Mutex
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := ClaimTaskRuns(context.Background(), 1)
			if err != nil {
				t.Errorf("ClaimTaskRuns: %v", err)
				return
			}
			mutex.Lock()
			total += len(claimed)
			mutex.Unlock()
		}()
	}
	wg.Wait()

	if total != 1 {
		t.Errorf("two workers claimed %d runs, want 1", total)
	}
	if runs := runsOf(t, db, task.ID); len(runs) != 1 {
		t.Errorf("runs = %d rows, want 1", len(runs))
	}
}

// TestClaimDisablesAnAtTask covers the fire-once shape: after the claim the task
// is off and carries no next_run_at, so the next tick does not see it again.
func TestClaimDisablesAnAtTask(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "once", repository.TaskScheduleAt, time.Now().Add(-time.Second))

	if claimed, err := ClaimTaskRuns(context.Background(), 1); err != nil || len(claimed) != 1 {
		t.Fatalf("first claim = %d runs, %v; want 1, nil", len(claimed), err)
	}

	stored, err := repository.GetTaskByID(context.Background(), db, owner.ID, task.ID)
	if err != nil {
		t.Fatalf("re-read task: %v", err)
	}
	if stored.Enabled {
		t.Error("the at task is still enabled after its run was claimed")
	}
	if stored.NextRunAt != nil {
		t.Errorf("next_run_at = %s, want nil", stored.NextRunAt)
	}

	claimed, err := ClaimTaskRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("second claim answered %d runs, want 0", len(claimed))
	}
}

// TestClaimSkipsAnAtTaskItIsTooLateFor covers the grace window: an at task that
// came due longer ago than the window is recorded as missed and switched off
// rather than run.
func TestClaimSkipsAnAtTaskItIsTooLateFor(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "stale-once", repository.TaskScheduleAt, time.Now().Add(-10*time.Minute))

	claimed, err := ClaimTaskRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("ClaimTaskRuns: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d runs, want none for a missed at task", len(claimed))
	}

	runs := runsOf(t, db, task.ID)
	if len(runs) != 1 {
		t.Fatalf("runs = %d rows, want 1", len(runs))
	}
	if runs[0].Status != repository.TaskRunSkipped || runs[0].Error != TaskRunErrorMissed {
		t.Errorf("run = %s/%s, want %s/%s", runs[0].Status, runs[0].Error, repository.TaskRunSkipped, TaskRunErrorMissed)
	}

	stored, err := repository.GetTaskByID(context.Background(), db, owner.ID, task.ID)
	if err != nil {
		t.Fatalf("re-read task: %v", err)
	}
	if stored.Enabled {
		t.Error("a missed at task is still enabled")
	}
}

// TestClaimAdvancesPastASlotThatAlreadyHasARun covers the conflict rule: when the
// slot is already recorded, the claim adds nothing and still moves the schedule,
// so the task cannot stay pinned to a slot it can never claim.
func TestClaimAdvancesPastASlotThatAlreadyHasARun(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "hourly", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))

	taken := &repository.TaskRun{
		ID:          uuid.New(),
		TaskID:      task.ID,
		UserID:      owner.ID,
		Status:      repository.TaskRunOK,
		Trigger:     repository.TaskRunTriggerSchedule,
		ScheduledAt: *task.NextRunAt,
	}
	if err := repository.CreateTaskRun(context.Background(), db, taken); err != nil {
		t.Fatalf("seed the taken slot: %v", err)
	}

	claimed, err := ClaimTaskRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("ClaimTaskRuns: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d runs, want none for a taken slot", len(claimed))
	}

	if runs := runsOf(t, db, task.ID); len(runs) != 1 {
		t.Errorf("runs = %d rows, want the seeded one only", len(runs))
	}

	stored, err := repository.GetTaskByID(context.Background(), db, owner.ID, task.ID)
	if err != nil {
		t.Fatalf("re-read task: %v", err)
	}
	if stored.NextRunAt == nil || !stored.NextRunAt.After(*task.NextRunAt) {
		t.Errorf("next_run_at = %v, want an advance past %v", stored.NextRunAt, *task.NextRunAt)
	}
}

// TestClaimRunsOneUsersTasksTogether covers the rule the other way round: two
// due tasks of one user are both claimed, so a morning brief does not wait for an
// hourly sweep. They run in the same sandbox, which is the user's own, and the
// older one is claimed first.
func TestClaimRunsOneUsersTasksTogether(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	newDueTask(t, db, owner, "first", repository.TaskScheduleEvery, time.Now().Add(-2*time.Minute))
	newDueTask(t, db, owner, "second", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))

	claimed, err := ClaimTaskRuns(context.Background(), 2)
	if err != nil {
		t.Fatalf("ClaimTaskRuns: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d runs, want both due tasks of one user", len(claimed))
	}
	if claimed[0].Task.Name != "first" {
		t.Errorf("claimed %q first, want the older due task", claimed[0].Task.Name)
	}
}

// TestClaimNeverDoublesOneTask covers the rule that survived the per-user change:
// a task with a run in flight is not claimed again, however many workers tick.
func TestClaimNeverDoublesOneTask(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "hourly", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))

	if claimed, err := ClaimTaskRuns(context.Background(), 1); err != nil || len(claimed) != 1 {
		t.Fatalf("first claim = %d runs, %v; want 1, nil", len(claimed), err)
	}

	// Make it due again while the run is still running.
	soon := time.Now().Add(-time.Minute).UTC()
	if err := db.Model(&repository.Task{}).Where("id = ?", task.ID).Update("next_run_at", soon).Error; err != nil {
		t.Fatalf("make the task due again: %v", err)
	}

	claimed, err := ClaimTaskRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d runs while one was still running, want 0", len(claimed))
	}
}

// TestClaimRefusesAPausedTaskWithAPendingRun covers a paused task that still
// carries a manual request: it is not dispatched, and nothing dereferences the
// next_run_at it no longer has.
func TestClaimRefusesAPausedTaskWithAPendingRun(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "paused", repository.TaskScheduleEvery, time.Now().Add(time.Hour))
	now := time.Now().UTC()
	if err := db.Model(&repository.Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"enabled":              false,
		"next_run_at":          nil,
		"run_now_requested_at": now,
	}).Error; err != nil {
		t.Fatalf("pause the task: %v", err)
	}

	claimed, err := ClaimTaskRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("ClaimTaskRuns: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d runs for a paused task, want 0", len(claimed))
	}
}

// TestClaimSweepsOnlyStaleRuns covers the sweep: a run still inside the window is
// left alone, one past it is marked lost, and a finalize that arrives afterwards
// is refused because the run it names is no longer running.
func TestClaimSweepsOnlyStaleRuns(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 10*time.Minute)

	fresh := newDueTask(t, db, owner, "fresh", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))
	stale := newDueTask(t, db, owner, "stale", repository.TaskScheduleEvery, time.Now().Add(-2*time.Minute))

	now := time.Now().UTC()
	recent := now.Add(-2 * time.Minute)
	old := now.Add(-30 * time.Minute)
	freshRun := &repository.TaskRun{
		ID: uuid.New(), TaskID: fresh.ID, UserID: owner.ID,
		Status: repository.TaskRunRunning, Trigger: repository.TaskRunTriggerSchedule,
		ScheduledAt: *fresh.NextRunAt, StartedAt: &recent,
	}
	oldRun := &repository.TaskRun{
		ID: uuid.New(), TaskID: stale.ID, UserID: owner.ID,
		Status: repository.TaskRunRunning, Trigger: repository.TaskRunTriggerSchedule,
		ScheduledAt: *stale.NextRunAt, StartedAt: &old,
	}
	if err := repository.CreateTaskRun(context.Background(), db, freshRun); err != nil {
		t.Fatalf("seed the fresh run: %v", err)
	}
	if err := repository.CreateTaskRun(context.Background(), db, oldRun); err != nil {
		t.Fatalf("seed the stale run: %v", err)
	}

	if _, err := ClaimTaskRuns(context.Background(), 1); err != nil {
		t.Fatalf("ClaimTaskRuns: %v", err)
	}

	freshStored := &repository.TaskRun{}
	if err := db.First(freshStored, "id = ?", freshRun.ID).Error; err != nil {
		t.Fatalf("re-read the fresh run: %v", err)
	}
	if freshStored.Status != repository.TaskRunRunning {
		t.Errorf("a run inside the window was swept to %q", freshStored.Status)
	}

	staleStored := &repository.TaskRun{}
	if err := db.First(staleStored, "id = ?", oldRun.ID).Error; err != nil {
		t.Fatalf("re-read the stale run: %v", err)
	}
	if staleStored.Status != repository.TaskRunLost || staleStored.Error != TaskRunErrorLost || staleStored.FinishedAt == nil {
		t.Errorf("stale run = %s/%s, want %s/%s with a finish", staleStored.Status, staleStored.Error, repository.TaskRunLost, TaskRunErrorLost)
	}

	if _, err := FinalizeTaskRun(context.Background(), stale.ID, oldRun.ID, FinalizeTaskRunInput{Status: repository.TaskRunOK}); !errors.Is(err, ErrTaskRunAlreadyFinal) {
		t.Errorf("finalizing a swept run = %v, want %v", err, ErrTaskRunAlreadyFinal)
	}
}

// TestClaimWithNoSlotsTouchesNothing covers the busy worker: a limit of zero is a
// no-op, not an error and not an empty transaction that advances anything.
func TestClaimWithNoSlotsTouchesNothing(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "morning", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))

	claimed, err := ClaimTaskRuns(context.Background(), 0)
	if err != nil {
		t.Fatalf("ClaimTaskRuns: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d runs, want 0", len(claimed))
	}
	if runs := runsOf(t, db, task.ID); len(runs) != 0 {
		t.Errorf("runs = %d rows, want 0", len(runs))
	}

	stored, err := repository.GetTaskByID(context.Background(), db, owner.ID, task.ID)
	if err != nil {
		t.Fatalf("re-read task: %v", err)
	}
	if !stored.NextRunAt.Equal(*task.NextRunAt) {
		t.Errorf("next_run_at moved to %v, want %v", stored.NextRunAt, *task.NextRunAt)
	}
}

// TestFinalizeRecordsOneTerminalState covers the guard: the first finalize wins,
// and a repeat is a conflict rather than an overwrite.
func TestFinalizeRecordsOneTerminalState(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	task := newDueTask(t, db, owner, "morning", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))
	claimed, err := ClaimTaskRuns(context.Background(), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %d runs, %v; want 1, nil", len(claimed), err)
	}
	run := claimed[0]

	finalized, err := FinalizeTaskRun(context.Background(), task.ID, run.RunID, FinalizeTaskRunInput{Status: repository.TaskRunOK})
	if err != nil {
		t.Fatalf("FinalizeTaskRun: %v", err)
	}
	if finalized.Status != repository.TaskRunOK || finalized.FinishedAt == nil {
		t.Errorf("run = %s finished %v, want ok with a finish", finalized.Status, finalized.FinishedAt)
	}

	if _, err := FinalizeTaskRun(context.Background(), task.ID, run.RunID, FinalizeTaskRunInput{Status: repository.TaskRunError, Error: "late"}); !errors.Is(err, ErrTaskRunAlreadyFinal) {
		t.Errorf("second finalize = %v, want %v", err, ErrTaskRunAlreadyFinal)
	}

	stored := &repository.TaskRun{}
	if err := db.First(stored, "id = ?", run.RunID).Error; err != nil {
		t.Fatalf("re-read run: %v", err)
	}
	if stored.Status != repository.TaskRunOK || stored.Error != "" {
		t.Errorf("run = %s/%q, want the first result kept", stored.Status, stored.Error)
	}
}

// TestFinalizeRefusesAnotherTasksRun covers the id pairing: a run cannot be
// finalized through a task it does not belong to.
func TestFinalizeRefusesAnotherTasksRun(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)
	claimTestConfig(t, 5*time.Minute, 12*time.Minute)

	first := newDueTask(t, db, owner, "first", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))
	claimed, err := ClaimTaskRuns(context.Background(), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %d runs, %v; want 1, nil", len(claimed), err)
	}

	other := newDueTask(t, db, owner, "second", repository.TaskScheduleEvery, time.Now().Add(-time.Minute))

	if _, err := FinalizeTaskRun(context.Background(), other.ID, claimed[0].RunID, FinalizeTaskRunInput{Status: repository.TaskRunOK}); !errors.Is(err, ErrTaskRunAlreadyFinal) {
		t.Errorf("finalize through another task = %v, want %v", err, ErrTaskRunAlreadyFinal)
	}

	if _, err := FinalizeTaskRun(context.Background(), first.ID, claimed[0].RunID, FinalizeTaskRunInput{Status: repository.TaskRunOK}); err != nil {
		t.Errorf("finalize through its own task: %v", err)
	}
}

// TestFinalizeRefusesRunningAsATerminalState covers the status set: a caller
// cannot finalize a run back into running, which would leave it stuck.
func TestFinalizeRefusesRunningAsATerminalState(t *testing.T) {
	db := claimTestDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	if _, err := FinalizeTaskRun(context.Background(), uuid.New(), uuid.New(), FinalizeTaskRunInput{Status: repository.TaskRunRunning}); err == nil {
		t.Fatal("finalizing into running was accepted")
	} else {
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Errorf("error = %v, want a ValidationError", err)
		}
	}
}
