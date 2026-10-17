package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// taskWriteDB opens the shared test database and installs the tables the write
// path needs: tasks hang on an agent and a provider by name, so those two come
// with them.
func taskWriteDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := taskTestDB(t)
	if err := db.AutoMigrate(&repository.Agent{}, &repository.Provider{}); err != nil {
		t.Fatalf("automigrate agents and providers: %v", err)
	}
	return db
}

// newTestNamedAgent stores one agent of owner under name.
func newTestNamedAgent(t *testing.T, db *gorm.DB, owner *repository.User, name string, isPublic bool) *repository.Agent {
	t.Helper()

	agent := &repository.Agent{
		ID:       uuid.New(),
		UserID:   owner.ID,
		Name:     name,
		IsPublic: isPublic,
	}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent %q: %v", name, err)
	}
	t.Cleanup(func() { db.Where("user_id = ?", owner.ID).Delete(&repository.Agent{}) })
	return agent
}

// newTestNamedProvider stores one provider of owner under name.
func newTestNamedProvider(t *testing.T, db *gorm.DB, owner *repository.User, name string, isPublic bool) *repository.Provider {
	t.Helper()

	provider := &repository.Provider{
		ID:        uuid.New(),
		UserID:    owner.ID,
		Name:      name,
		ModelName: "gpt-test",
		APIKey:    "sk-test",
		APIBase:   "https://example.com",
		IsPublic:  isPublic,
	}
	if err := db.Create(provider).Error; err != nil {
		t.Fatalf("create provider %q: %v", name, err)
	}
	t.Cleanup(func() { db.Where("user_id = ?", owner.ID).Delete(&repository.Provider{}) })
	return provider
}

// newTaskInput is a valid cron task for owner, pointed at the agent and provider
// named "assistant" and "openai" which newTaskTargets installs.
func newTaskInput(name string) CreateTaskInput {
	expr := "0 9 * * *"
	return CreateTaskInput{
		Name:         name,
		Prompt:       "summarise the inbox",
		AgentName:    "assistant",
		ProviderName: "openai",
		Timezone:     "Asia/Hong_Kong",
		Enabled:      true,
		Schedule: TaskScheduleInput{
			Kind:     repository.TaskScheduleCron,
			CronExpr: &expr,
		},
	}
}

// newTaskTargets installs the agent and provider a task names.
func newTaskTargets(t *testing.T, db *gorm.DB, owner *repository.User) {
	t.Helper()

	newTestNamedAgent(t, db, owner, "assistant", false)
	newTestNamedProvider(t, db, owner, "openai", false)
}

// TestCreateTaskStoresCronInTheTaskTimezone covers the timezone actually taking
// effect: nine o'clock in Hong Kong is stored as 01:00 UTC. Without pinning the
// location the container's UTC clock would quietly run the task at nine UTC.
func TestCreateTaskStoresCronInTheTaskTimezone(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	task, err := CreateTask(context.Background(), owner.ID, newTaskInput("morning"))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.NextRunAt == nil {
		t.Fatal("next_run_at = nil, want the next nine o'clock")
	}

	hongKong, err := time.LoadLocation("Asia/Hong_Kong")
	if err != nil {
		t.Fatalf("load Asia/Hong_Kong: %v", err)
	}
	local := task.NextRunAt.In(hongKong)
	if local.Hour() != 9 || local.Minute() != 0 {
		t.Errorf("next_run_at = %s, want 09:00 in Asia/Hong_Kong", local)
	}
	if utc := task.NextRunAt.UTC(); utc.Hour() != 1 {
		t.Errorf("next_run_at = %s UTC, want 01:00", utc)
	}
	if task.Timezone != "Asia/Hong_Kong" {
		t.Errorf("timezone = %q, want Asia/Hong_Kong", task.Timezone)
	}
}

// TestCreateTaskCountsEveryGapFromNow covers the no-backlog rule for "every":
// the next run is one gap after the write, whatever instant the client asks for.
func TestCreateTaskCountsEveryGapFromNow(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	every := 3600
	in := newTaskInput("hourly")
	in.Timezone = ""
	in.Schedule = TaskScheduleInput{Kind: repository.TaskScheduleEvery, EverySeconds: &every}

	before := time.Now()
	task, err := CreateTask(context.Background(), owner.ID, in)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	after := time.Now()

	if task.NextRunAt == nil {
		t.Fatal("next_run_at = nil for an enabled every task")
	}
	low, high := before.Add(time.Hour), after.Add(time.Hour)
	if task.NextRunAt.Before(low.Add(-time.Second)) || task.NextRunAt.After(high.Add(time.Second)) {
		t.Errorf("next_run_at = %s, want within a second of now + 1h (%s to %s)", task.NextRunAt, low, high)
	}
	if task.RunAt != nil || task.CronExpr != nil {
		t.Errorf("every task kept run_at %v or cron_expr %v", task.RunAt, task.CronExpr)
	}
}

// TestCreateTaskRefusesABadSchedule covers the validation the write path owes the
// client: each shape is refused when its column is missing, when another shape's
// column comes with it, and when its own column is out of range.
func TestCreateTaskRefusesABadSchedule(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	short := 59
	expr := "0 9 * * *"
	bad := "@daily"

	cases := map[string]TaskScheduleInput{
		"unknown kind":      {Kind: "later", RunAt: &at},
		"at without run":    {Kind: repository.TaskScheduleAt},
		"at with every":     {Kind: repository.TaskScheduleAt, RunAt: &at, EverySeconds: &short},
		"every too short":   {Kind: repository.TaskScheduleEvery, EverySeconds: &short},
		"every with run":    {Kind: repository.TaskScheduleEvery, RunAt: &at, EverySeconds: &short},
		"cron without expr": {Kind: repository.TaskScheduleCron},
		"cron descriptor":   {Kind: repository.TaskScheduleCron, CronExpr: &bad},
		"cron never fires":  {Kind: repository.TaskScheduleCron, CronExpr: stringPtr("0 0 30 2 *")},
		"cron with every":   {Kind: repository.TaskScheduleCron, EverySeconds: &short, CronExpr: &expr},
	}

	for name, schedule := range cases {
		t.Run(name, func(t *testing.T) {
			in := newTaskInput("bad-" + name)
			in.Schedule = schedule

			_, err := CreateTask(context.Background(), owner.ID, in)
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("CreateTask = %v, want a ValidationError", err)
			}
		})
	}
}

// TestCreateTaskRefusesAnUnknownTimezone covers the zone name: a task whose
// expression has no zone to run in cannot be stored as if it did.
func TestCreateTaskRefusesAnUnknownTimezone(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	in := newTaskInput("bad-zone")
	in.Timezone = "Mars/Olympus_Mons"

	_, err := CreateTask(context.Background(), owner.ID, in)
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("CreateTask = %v, want a ValidationError", err)
	}
}

// TestCreateTaskRefusesNamesItCannotResolve covers the soft references: an agent
// or provider the caller cannot use is a bad request, not a stored task that
// fails later, and two shared rows carrying the name are reported as ambiguous
// rather than picked arbitrarily.
func TestCreateTaskRefusesNamesItCannotResolve(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	stranger := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	// A private agent of another user is not one the owner may use.
	newTestNamedAgent(t, db, stranger, "secret", false)
	in := newTaskInput("unknown-agent")
	in.AgentName = "secret"
	var validation *ValidationError
	if _, err := CreateTask(context.Background(), owner.ID, in); !errors.As(err, &validation) {
		t.Errorf("CreateTask with another user's private agent = %v, want a ValidationError", err)
	}

	// Two shared providers carry the same name, so the name does not pick one.
	second := testTaskOwner(t, db)
	newTestNamedProvider(t, db, stranger, "shared", true)
	newTestNamedProvider(t, db, second, "shared", true)
	in = newTaskInput("ambiguous-provider")
	in.ProviderName = "shared"
	if _, err := CreateTask(context.Background(), owner.ID, in); !errors.Is(err, ErrTaskProviderAmbiguous) {
		t.Errorf("CreateTask with two shared providers of one name = %v, want %v", err, ErrTaskProviderAmbiguous)
	}
}

// TestCreateTaskResolvesAProviderByItsModelName covers the provider that carries
// no name of its own: rqark-core falls back to model_name, so the cloud resolves
// the same value, and the task stores the name the listing shows.
func TestCreateTaskResolvesAProviderByItsModelName(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTestNamedAgent(t, db, owner, "assistant", false)
	newTestNamedProvider(t, db, owner, "", false)

	in := newTaskInput("nameless-provider")
	in.ProviderName = "gpt-test"

	task, err := CreateTask(context.Background(), owner.ID, in)
	if err != nil {
		t.Fatalf("CreateTask with a provider named by its model: %v", err)
	}
	if task.ProviderName != "gpt-test" {
		t.Errorf("provider_name = %q, want gpt-test", task.ProviderName)
	}
}

// TestCreateTaskPrefersTheOwnersOwnName covers the other half of the resolution:
// the caller's own row wins over a shared one carrying the same name, so a task
// keeps pointing at the copy its owner controls.
func TestCreateTaskPrefersTheOwnersOwnName(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	stranger := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	newTestNamedProvider(t, db, owner, "shared", false)
	newTestNamedProvider(t, db, stranger, "shared", true)

	in := newTaskInput("own-provider")
	in.ProviderName = "shared"
	if _, err := CreateTask(context.Background(), owner.ID, in); err != nil {
		t.Fatalf("CreateTask with the owner's own provider: %v", err)
	}
}

// TestCreateTaskRefusesADuplicateName covers the unique name per user at the
// service boundary: the check reports the sentinel rather than a raw write error.
func TestCreateTaskRefusesADuplicateName(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	if _, err := CreateTask(context.Background(), owner.ID, newTaskInput("morning")); err != nil {
		t.Fatalf("first CreateTask: %v", err)
	}
	if _, err := CreateTask(context.Background(), owner.ID, newTaskInput("morning")); !errors.Is(err, ErrTaskNameTaken) {
		t.Errorf("second CreateTask = %v, want %v", err, ErrTaskNameTaken)
	}
}

// TestCreateDisabledTaskCarriesNoNextRun covers the disabled shape: the schedule
// is stored so it can be enabled later, but nothing is due.
func TestCreateDisabledTaskCarriesNoNextRun(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	in := newTaskInput("paused")
	in.Enabled = false

	task, err := CreateTask(context.Background(), owner.ID, in)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.NextRunAt != nil {
		t.Errorf("next_run_at = %s, want nil for a disabled task", task.NextRunAt)
	}
	if task.CronExpr == nil {
		t.Error("cron_expr was dropped from a disabled task")
	}
}

// TestUpdateTaskClearsAndRecomputesNextRun covers the enable switch: disabling
// drops next_run_at, and enabling recomputes it from the moment of the write.
func TestUpdateTaskClearsAndRecomputesNextRun(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	task, err := CreateTask(context.Background(), owner.ID, newTaskInput("morning"))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	first := *task.NextRunAt

	off := false
	disabled, err := UpdateTask(context.Background(), owner.ID, task.ID, UpdateTaskInput{Enabled: &off})
	if err != nil {
		t.Fatalf("UpdateTask disabling: %v", err)
	}
	if disabled.NextRunAt != nil {
		t.Errorf("next_run_at = %s after disabling, want nil", disabled.NextRunAt)
	}

	on := true
	enabled, err := UpdateTask(context.Background(), owner.ID, task.ID, UpdateTaskInput{Enabled: &on})
	if err != nil {
		t.Fatalf("UpdateTask enabling: %v", err)
	}
	if enabled.NextRunAt == nil {
		t.Fatal("next_run_at = nil after enabling, want a recomputed instant")
	}
	if enabled.NextRunAt.Before(first.Add(-time.Second)) {
		t.Errorf("next_run_at = %s, want not earlier than the first %s", enabled.NextRunAt, first)
	}
}

// TestUpdateTaskRecomputesAnEveryTaskFromNow covers the reschedule rule: a new
// gap is counted from the write, not from the slot the old schedule had reached.
func TestUpdateTaskRecomputesAnEveryTaskFromNow(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	old := 600
	in := newTaskInput("hourly")
	in.Schedule = TaskScheduleInput{Kind: repository.TaskScheduleEvery, EverySeconds: &old}
	task, err := CreateTask(context.Background(), owner.ID, in)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Pretend a long time has passed without the task firing.
	longAgo := time.Now().Add(-24 * time.Hour).UTC()
	if err := db.Model(&repository.Task{}).Where("id = ?", task.ID).Update("next_run_at", longAgo).Error; err != nil {
		t.Fatalf("age next_run_at: %v", err)
	}

	every := 3600
	before := time.Now()
	updated, err := UpdateTask(context.Background(), owner.ID, task.ID, UpdateTaskInput{
		Schedule: &TaskScheduleInput{Kind: repository.TaskScheduleEvery, EverySeconds: &every},
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	low, high := before.Add(time.Hour), time.Now().Add(time.Hour)
	if updated.NextRunAt.Before(low.Add(-time.Second)) || updated.NextRunAt.After(high.Add(time.Second)) {
		t.Errorf("next_run_at = %s, want within a second of now + 1h (%s to %s)", updated.NextRunAt, low, high)
	}
}

// TestUpdateTaskHidesAnotherUsersTask covers the user scoping on the write path.
func TestUpdateTaskHidesAnotherUsersTask(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	viewer := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	task, err := CreateTask(context.Background(), owner.ID, newTaskInput("morning"))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	renamed := "stolen"
	if _, err := UpdateTask(context.Background(), viewer.ID, task.ID, UpdateTaskInput{Name: &renamed}); !errors.Is(err, repository.ErrTaskNotFound) {
		t.Errorf("UpdateTask of another user's task = %v, want %v", err, repository.ErrTaskNotFound)
	}
	if _, err := GetTask(context.Background(), viewer.ID, task.ID); !errors.Is(err, repository.ErrTaskNotFound) {
		t.Errorf("GetTask of another user's task = %v, want %v", err, repository.ErrTaskNotFound)
	}
}

// TestListTaskRunsPagesNewestFirst covers the run listing: newest scheduled
// instant first, one page at a time, with the total of the whole set.
func TestListTaskRunsPagesNewestFirst(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	task, err := CreateTask(context.Background(), owner.ID, newTaskInput("morning"))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Hour)
	for i := 0; i < 3; i++ {
		slot := base.Add(time.Duration(i) * time.Hour)
		run := &repository.TaskRun{
			ID:          uuid.New(),
			TaskID:      task.ID,
			UserID:      owner.ID,
			Status:      repository.TaskRunOK,
			Trigger:     repository.TaskRunTriggerSchedule,
			ScheduledAt: slot,
			StartedAt:   &slot,
		}
		if err := repository.CreateTaskRun(context.Background(), db, run); err != nil {
			t.Fatalf("create run %d: %v", i, err)
		}
	}

	first, total, err := ListTaskRuns(context.Background(), owner.ID, task.ID, utils.Pagination{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListTaskRuns: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(first) != 2 {
		t.Fatalf("first page = %d runs, want 2", len(first))
	}
	if !first[0].ScheduledAt.After(first[1].ScheduledAt) {
		t.Errorf("first page is not newest first: %s then %s", first[0].ScheduledAt, first[1].ScheduledAt)
	}

	last, total, err := ListTaskRuns(context.Background(), owner.ID, task.ID, utils.Pagination{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListTaskRuns second page: %v", err)
	}
	if total != 3 || len(last) != 1 {
		t.Fatalf("second page = %d runs (total %d), want 1 of 3", len(last), total)
	}
	if !last[0].ScheduledAt.Equal(base) {
		t.Errorf("second page = %s, want the oldest %s", last[0].ScheduledAt, base)
	}

	if _, _, err := ListTaskRuns(context.Background(), owner.ID, uuid.New(), utils.Pagination{Limit: 2}); !errors.Is(err, repository.ErrTaskNotFound) {
		t.Errorf("ListTaskRuns of an unknown task = %v, want %v", err, repository.ErrTaskNotFound)
	}
}

// TestUpdateTaskRefusesToEnableAnAtTaskThatAlreadyPassed covers the re-enable
// case: an "at" task that has already fired has no future instant to offer, so
// turning it back on is refused rather than stored as a run the dispatcher would
// immediately mark missed.
func TestUpdateTaskRefusesToEnableAnAtTaskThatAlreadyPassed(t *testing.T) {
	db := taskWriteDB(t)
	owner := testTaskOwner(t, db)
	newTaskTargets(t, db, owner)

	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	in := newTaskInput("once")
	in.Schedule = TaskScheduleInput{Kind: repository.TaskScheduleAt, RunAt: &at}
	task, err := CreateTask(context.Background(), owner.ID, in)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Pretend the task fired an hour ago: the dispatcher disables an at task
	// once it runs, and by now its instant is behind the grace window.
	passed := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	if err := db.Model(&repository.Task{}).Where("id = ?", task.ID).
		Updates(map[string]any{"enabled": false, "next_run_at": nil, "run_at": passed}).Error; err != nil {
		t.Fatalf("simulate the fired task: %v", err)
	}

	on := true
	_, err = UpdateTask(context.Background(), owner.ID, task.ID, UpdateTaskInput{Enabled: &on})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("re-enabling a passed at task = %v, want a ValidationError", err)
	}

	// Moving the instant forward is what makes it enableable again.
	later := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	task, err = UpdateTask(context.Background(), owner.ID, task.ID, UpdateTaskInput{
		Enabled:  &on,
		Schedule: &TaskScheduleInput{Kind: repository.TaskScheduleAt, RunAt: &later},
	})
	if err != nil {
		t.Fatalf("UpdateTask with a new instant: %v", err)
	}
	if task.NextRunAt == nil || !task.NextRunAt.Equal(later) {
		t.Errorf("next_run_at = %v, want %s", task.NextRunAt, later)
	}
}

// stringPtr is the string literal pointer the schedule cases need.
func stringPtr(value string) *string { return &value }
