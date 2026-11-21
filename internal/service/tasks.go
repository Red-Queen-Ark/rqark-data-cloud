package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/repository"
	"rqark-data-cloud/internal/utils"
)

// maxTaskNameLen is the width of the tasks.name column.
const maxTaskNameLen = 255

// minTaskEverySeconds is the shortest gap an "every" task may run at. A minute
// is the granularity a minute tick can honour, so anything shorter would either
// be rounded up silently or fire late forever.
const minTaskEverySeconds = 60

// TaskScheduleInput is the schedule half of a task write: the kind plus the one
// column that kind uses. The other two are carried so that a body setting more
// than one of them can be refused rather than silently trimmed.
type TaskScheduleInput struct {
	Kind         string
	RunAt        *time.Time
	EverySeconds *int
	CronExpr     *string
}

// CreateTaskInput is the body accepted by POST /api/tasks.
type CreateTaskInput struct {
	Name         string
	Prompt       string
	AgentName    string
	ProviderName string
	// Timezone is the IANA zone a cron expression is evaluated in. Empty means
	// UTC.
	Timezone string
	Enabled  bool
	Schedule TaskScheduleInput
}

// UpdateTaskInput is a partial update: a nil field is left untouched. A non-nil
// Schedule replaces the whole schedule, since the three shapes are not
// independently editable.
type UpdateTaskInput struct {
	Name         *string
	Prompt       *string
	AgentName    *string
	ProviderName *string
	Timezone     *string
	Enabled      *bool
	Schedule     *TaskScheduleInput
}

// scheduleColumns is the stored form of one validated schedule.
type scheduleColumns struct {
	kind         string
	runAt        *time.Time
	everySeconds *int
	cronExpr     *string
}

// CreateTask validates the task, works out when it next runs and stores it under
// userID.
func CreateTask(ctx context.Context, userID uuid.UUID, in CreateTaskInput) (*repository.Task, error) {
	if err := validateTaskName(in.Name); err != nil {
		return nil, err
	}
	if in.Prompt == "" {
		return nil, &ValidationError{Message: "prompt is required."}
	}
	agentName, providerName, err := validateTaskTargets(ctx, userID, in.AgentName, in.ProviderName)
	if err != nil {
		return nil, err
	}

	location, err := LoadTaskLocation(in.Timezone)
	if err != nil {
		return nil, err
	}
	schedule, err := validateTaskSchedule(in.Schedule, location)
	if err != nil {
		return nil, err
	}

	taken, err := repository.TaskNameTaken(ctx, config.DB, userID, in.Name, uuid.Nil)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrTaskNameTaken
	}

	task := &repository.Task{
		ID:           uuid.New(),
		UserID:       userID,
		Name:         in.Name,
		Prompt:       in.Prompt,
		AgentName:    agentName,
		ProviderName: providerName,
		Timezone:     timezoneName(in.Timezone),
		Enabled:      in.Enabled,
	}
	schedule.apply(task)

	if in.Enabled {
		next, err := nextRunAt(schedule, location, time.Now())
		if err != nil {
			return nil, err
		}
		task.NextRunAt = &next
	}

	if err := repository.CreateTask(ctx, config.DB, task); err != nil {
		return nil, mapWriteError(err)
	}
	return task, nil
}

// UpdateTask applies in to one of userID's tasks. next_run_at is recomputed when
// the schedule or the timezone changes, and when a disabled task is enabled
// again; a task that is or becomes disabled carries no next_run_at at all.
func UpdateTask(ctx context.Context, userID, id uuid.UUID, in UpdateTaskInput) (*repository.Task, error) {
	task, err := repository.GetTaskByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}

	wasEnabled := task.Enabled

	if in.Name != nil {
		if err := validateTaskName(*in.Name); err != nil {
			return nil, err
		}
		taken, err := repository.TaskNameTaken(ctx, config.DB, userID, *in.Name, id)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, ErrTaskNameTaken
		}
		task.Name = *in.Name
	}
	if in.Prompt != nil {
		if *in.Prompt == "" {
			return nil, &ValidationError{Message: "prompt is required."}
		}
		task.Prompt = *in.Prompt
	}
	if in.AgentName != nil || in.ProviderName != nil {
		agentName, providerName := task.AgentName, task.ProviderName
		if in.AgentName != nil {
			agentName = *in.AgentName
		}
		if in.ProviderName != nil {
			providerName = *in.ProviderName
		}
		resolvedAgent, resolvedProvider, err := validateTaskTargets(ctx, userID, agentName, providerName)
		if err != nil {
			return nil, err
		}
		task.AgentName, task.ProviderName = resolvedAgent, resolvedProvider
	}

	timezoneChanged := false
	if in.Timezone != nil {
		zone := timezoneName(*in.Timezone)
		// Rewriting the zone a task already carries would re-phase an every task
		// and move a cron task's next fire, which is not what a client that sends
		// the whole object means.
		timezoneChanged = zone != task.Timezone
		task.Timezone = zone
	}
	location, err := LoadTaskLocation(task.Timezone)
	if err != nil {
		return nil, err
	}

	schedule := storedSchedule(task)
	scheduleChanged := in.Schedule != nil
	if scheduleChanged {
		if schedule, err = validateTaskSchedule(*in.Schedule, location); err != nil {
			return nil, err
		}
		schedule.apply(task)
	}

	if in.Enabled != nil {
		task.Enabled = *in.Enabled
	}

	switch {
	case !task.Enabled:
		task.NextRunAt = nil
	case scheduleChanged || timezoneChanged || !wasEnabled:
		next, err := nextRunAt(schedule, location, time.Now())
		if err != nil {
			return nil, err
		}
		task.NextRunAt = &next
	}

	fields := map[string]any{
		"name":          task.Name,
		"prompt":        task.Prompt,
		"agent_name":    task.AgentName,
		"provider_name": task.ProviderName,
		"timezone":      task.Timezone,
		"enabled":       task.Enabled,
		"next_run_at":   task.NextRunAt,
		"schedule_kind": task.ScheduleKind,
		"run_at":        task.RunAt,
		"every_seconds": task.EverySeconds,
		"cron_expr":     task.CronExpr,
	}
	if !task.Enabled {
		// A paused task has no schedule and no pending manual run: both would be
		// honoured the moment it was enabled again, which is not what pausing it
		// asked for.
		fields["run_now_requested_at"] = nil
	}

	if err := repository.UpdateTaskFields(ctx, config.DB, userID, id, fields); err != nil {
		return nil, mapWriteError(err)
	}
	return repository.GetTaskByID(ctx, config.DB, userID, id)
}

// RunTaskNow asks for one extra run of a task, at the next tick.
//
// The request is recorded on the task rather than dispatched here: the agent
// cloud is the only thing that runs a task, and it picks the request up on its
// next claim. next_run_at is deliberately untouched, because a manual run must
// not shift the phase of an every task or skip a cron task's next fire.
//
// A disabled task is refused: its schedule is off, and running it once would be
// a surprise rather than a shortcut. A task that is already running is refused
// too, which is the same one-run-at-a-time rule the dispatcher enforces.
//
// The request is written with the guards in the statement itself, so a claim
// committing between the read and the write cannot revive a task paused in the
// meantime or queue a second request behind a run that just started. The checks
// above are only there to answer with the reason; the write is what decides.
func RunTaskNow(ctx context.Context, userID, id uuid.UUID) (*repository.Task, error) {
	task, err := repository.GetTaskByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}
	if !task.Enabled {
		return nil, ErrTaskDisabled
	}
	if task.RunNowRequestedAt == nil {
		if _, err := repository.RequestRunNow(ctx, config.DB, userID, id, time.Now().UTC()); err != nil {
			return nil, err
		}
	}

	// Re-read so the answer is what was stored, and so a refusal the write hit
	// (a run that started meanwhile) is reported rather than acknowledged.
	stored, err := repository.GetTaskByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}
	if !stored.Enabled {
		return nil, ErrTaskDisabled
	}
	if stored.RunNowRequestedAt == nil {
		running, err := repository.HasRunningRun(ctx, config.DB, id)
		if err != nil {
			return nil, err
		}
		if running {
			return nil, ErrTaskRunInFlight
		}
		return nil, &ValidationError{Message: "the run request was not recorded."}
	}
	return stored, nil
}

// DeleteTask removes one of userID's tasks, its runs included.
func DeleteTask(ctx context.Context, userID, id uuid.UUID) error {
	return repository.DeleteTask(ctx, config.DB, userID, id)
}

// ListTasks returns every task of userID ordered by name.
func ListTasks(ctx context.Context, userID uuid.UUID) ([]repository.Task, error) {
	return repository.ListTasks(ctx, config.DB, userID)
}

// GetTask loads one of userID's tasks.
func GetTask(ctx context.Context, userID, id uuid.UUID) (*repository.Task, error) {
	return repository.GetTaskByID(ctx, config.DB, userID, id)
}

// ListTaskRuns returns one page of a task's runs, newest first. The task is
// looked up first so that another user's id is a 404 rather than an empty page.
func ListTaskRuns(ctx context.Context, userID, taskID uuid.UUID, page utils.Pagination) ([]repository.TaskRun, int64, error) {
	if _, err := repository.GetTaskByID(ctx, config.DB, userID, taskID); err != nil {
		return nil, 0, err
	}

	total, err := repository.CountTaskRuns(ctx, config.DB, taskID)
	if err != nil {
		return nil, 0, err
	}
	runs, err := repository.ListTaskRuns(ctx, config.DB, taskID, page.Limit, page.Offset)
	if err != nil {
		return nil, 0, err
	}
	return runs, total, nil
}

// validateTaskSchedule checks one schedule shape and normalises it into the three
// columns. Only the column the kind names may be set, mirroring the CHECK
// constraint so a client hears about the mistake as a 400 rather than as a
// failed write.
func validateTaskSchedule(in TaskScheduleInput, location *time.Location) (scheduleColumns, error) {
	shape := scheduleColumns{kind: in.Kind}
	set := 0
	if in.RunAt != nil {
		set++
	}
	if in.EverySeconds != nil {
		set++
	}
	if in.CronExpr != nil {
		set++
	}

	switch in.Kind {
	case repository.TaskScheduleAt:
		if in.RunAt == nil || set != 1 {
			return scheduleColumns{}, &ValidationError{Message: "an at task sets run_at and nothing else."}
		}
		at := in.RunAt.UTC()
		shape.runAt = &at
	case repository.TaskScheduleEvery:
		if in.EverySeconds == nil || set != 1 {
			return scheduleColumns{}, &ValidationError{Message: "an every task sets every_seconds and nothing else."}
		}
		if *in.EverySeconds < minTaskEverySeconds {
			return scheduleColumns{}, &ValidationError{Message: "every_seconds must be at least 60."}
		}
		every := *in.EverySeconds
		shape.everySeconds = &every
	case repository.TaskScheduleCron:
		if in.CronExpr == nil || set != 1 {
			return scheduleColumns{}, &ValidationError{Message: "a cron task sets cron_expr and nothing else."}
		}
		expression := *in.CronExpr
		if _, err := ParseTaskCron(expression, location); err != nil {
			return scheduleColumns{}, err
		}
		shape.cronExpr = &expression
	default:
		return scheduleColumns{}, &ValidationError{Message: "schedule_kind must be one of at, every or cron."}
	}
	return shape, nil
}

// nextRunAt works out the first instant a schedule fires at, counting from now.
// An "every" task takes the first slot of its grid after now, so a backlog is
// never replayed and the task keeps its place on the grid rather than drifting
// by whatever the tick happened to be.
//
// An "at" instant further in the past than TASK_AT_GRACE is refused rather than
// stored: the dispatcher would only record it as missed and disable the task
// again, which is not what a caller writing a run time means. An instant inside
// the grace window is kept, so a task created a moment late still fires.
func nextRunAt(schedule scheduleColumns, location *time.Location, now time.Time) (time.Time, error) {
	switch schedule.kind {
	case repository.TaskScheduleAt:
		if schedule.runAt.Before(now.Add(-config.TaskAtGrace)) {
			return time.Time{}, &ValidationError{Message: "run_at is in the past; pick a future time."}
		}
		return *schedule.runAt, nil
	case repository.TaskScheduleEvery:
		return NextEverySlot(now, time.Duration(*schedule.everySeconds)*time.Second), nil
	case repository.TaskScheduleCron:
		compiled, err := ParseTaskCron(*schedule.cronExpr, location)
		if err != nil {
			return time.Time{}, err
		}
		return NextCronRun(compiled, now)
	}
	return time.Time{}, &ValidationError{Message: "schedule_kind must be one of at, every or cron."}
}

// apply writes a validated schedule onto task, clearing the two columns the kind
// does not use.
func (s scheduleColumns) apply(task *repository.Task) {
	task.ScheduleKind = s.kind
	task.RunAt = s.runAt
	task.EverySeconds = s.everySeconds
	task.CronExpr = s.cronExpr
}

// storedSchedule reads the schedule a stored task carries, in the form the
// validation works on.
func storedSchedule(task *repository.Task) scheduleColumns {
	return scheduleColumns{
		kind:         task.ScheduleKind,
		runAt:        task.RunAt,
		everySeconds: task.EverySeconds,
		cronExpr:     task.CronExpr,
	}
}

// validateTaskTargets checks that the names a task runs against resolve to one
// agent and one provider the caller may use, and returns the names to store.
//
// A provider may carry no name of its own, in which case it is addressed by its
// model_name; the resolved name is written back rather than the one the caller
// sent, so a task always holds the label the listing shows.
func validateTaskTargets(ctx context.Context, userID uuid.UUID, agentName, providerName string) (string, string, error) {
	if agentName == "" {
		return "", "", &ValidationError{Message: "agent_name is required."}
	}
	if providerName == "" {
		return "", "", &ValidationError{Message: "provider_name is required."}
	}

	agent, err := repository.FindAgentNamed(ctx, config.DB, userID, agentName)
	if err != nil {
		if errors.Is(err, repository.ErrAgentAmbiguous) {
			return "", "", ErrTaskAgentAmbiguous
		}
		return "", "", &ValidationError{Message: "agent_name does not name an agent you can use."}
	}
	provider, err := repository.FindProviderNamed(ctx, config.DB, userID, providerName)
	if err != nil {
		if errors.Is(err, repository.ErrProviderAmbiguous) {
			return "", "", ErrTaskProviderAmbiguous
		}
		return "", "", &ValidationError{Message: "provider_name does not name a provider you can use."}
	}

	storedProviderName := provider.Name
	if storedProviderName == "" {
		storedProviderName = provider.ModelName
	}
	return agent.Name, storedProviderName, nil
}

// validateTaskName checks that name is present and fits its column.
func validateTaskName(name string) error {
	if name == "" {
		return &ValidationError{Message: "name is required."}
	}
	if len(name) > maxTaskNameLen {
		return &ValidationError{Message: "name is too long."}
	}
	return nil
}

// timezoneName keeps the stored zone name, defaulting an empty one to UTC which
// is what the column defaults to.
func timezoneName(name string) string {
	if name == "" {
		return "UTC"
	}
	return name
}
