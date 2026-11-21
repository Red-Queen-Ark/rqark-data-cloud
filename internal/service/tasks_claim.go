package service

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/repository"
	"rqark-data-cloud/internal/utils"
)

// TaskSessionSource is the sessions.source a task run files its transcript
// under. It keeps a run out of the chat sidebar, which reads only the source its
// own application writes.
const TaskSessionSource = "TASK"

// Reasons a run carries in its error column. `lost` is written by the sweep,
// `missed` by a claim that found an at task too late to dispatch, and the two
// `*_NOT_FOUND` values by a claim whose soft reference no longer resolves.
const (
	TaskRunErrorLost             = "lost"
	TaskRunErrorMissed           = "missed"
	TaskRunErrorAgentNotFound    = "AGENT_NOT_FOUND"
	TaskRunErrorProviderNotFound = "PROVIDER_NOT_FOUND"
)

// ClaimedRun is one run handed to the dispatcher: everything it needs to execute
// without calling back into any user endpoint. The agent and provider are
// resolved rows rather than names, so a run cannot be redirected by a rename
// between the claim and the execution.
type ClaimedRun struct {
	RunID uuid.UUID
	// UserID names the account the run acts for. It is what the runner names the
	// user's sandbox by, so a run sees the same files as that user's chat.
	UserID     uuid.UUID
	Task       repository.Task
	Agent      repository.Agent
	Provider   repository.Provider
	SessionID  uuid.UUID
	ActorToken string
}

// dueTasksSQL selects the tasks a tick may claim, oldest due first.
//
// The task row is locked with SKIP LOCKED, so two workers ticking at once take
// different tasks instead of contending for the same one. The NOT EXISTS clause
// is the one concurrency rule: a task never has two running runs, so a slow run
// is never doubled up on. Two tasks of one user may run together — a user who
// wants a morning brief and an hourly sweep does not want one to wait for the
// other.
//
// A task that was switched off is never selected, whatever is pending on it: a
// run_now request on a paused task is dropped rather than dispatched. Without
// that, a paused task carries a NULL next_run_at and the claim would have
// nothing to name the slot by.
//
// An account that is not active never runs anything. Deactivating an account
// leaves its tasks alone rather than switching them off, so that reactivating it
// resumes them; the check is here because the dispatcher is the only thing that
// would otherwise not care.
const dueTasksSQL = `
SELECT t.* FROM tasks t
WHERE t.enabled = true
  AND (
        (t.next_run_at IS NOT NULL AND t.next_run_at <= ?)
     OR t.run_now_requested_at IS NOT NULL
  )
  AND EXISTS (SELECT 1 FROM users u WHERE u.id = t.user_id AND u.is_active = true)
  AND NOT EXISTS (SELECT 1 FROM task_runs r WHERE r.task_id = t.id AND r.status = 'running')
ORDER BY COALESCE(t.run_now_requested_at, t.next_run_at), t.id
LIMIT ?
FOR UPDATE SKIP LOCKED`

// ClaimTaskRuns claims up to limit due tasks and returns the runs to dispatch.
//
// The whole claim is one transaction, which is what makes next_run_at the
// dispatcher's only source of truth: a slot is claimed, recorded and skipped in
// the same breath, so a crashed worker cannot leave a task due twice. The
// schedule is advanced whether or not the run row was inserted, so a run that
// loses the unique (task_id, scheduled_at) race cannot pin a task to a slot it
// will never be able to claim.
//
// A limit of zero claims nothing and touches nothing, which is what a worker
// with every slot busy sends.
func ClaimTaskRuns(ctx context.Context, limit int) ([]ClaimedRun, error) {
	claimed := []ClaimedRun{}
	if limit <= 0 {
		return claimed, nil
	}

	err := config.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()

		if err := sweepStaleRuns(tx, now); err != nil {
			return err
		}

		due := []repository.Task{}
		if err := tx.Raw(dueTasksSQL, now, limit).Scan(&due).Error; err != nil {
			return err
		}

		for i := range due {
			run, ok, err := claimOne(ctx, tx, &due[i], now)
			if err != nil {
				return err
			}
			if ok {
				claimed = append(claimed, *run)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// sweepStaleRuns marks every run that has been running for longer than the stale
// window as lost. A claimed run is never retried: the schedule is fire-once, and
// a run that died leaves a hole in the schedule rather than a duplicate.
func sweepStaleRuns(tx *gorm.DB, now time.Time) error {
	cutoff := now.Add(-config.TaskRunStaleAfter)
	return tx.Model(&repository.TaskRun{}).
		Where("status = ? AND started_at IS NOT NULL AND started_at < ?", repository.TaskRunRunning, cutoff).
		Updates(map[string]any{
			"status":      repository.TaskRunLost,
			"error":       TaskRunErrorLost,
			"finished_at": now,
		}).Error
}

// claimOne records and prepares one due task. It reports false when the attempt
// produced no run to dispatch, which happens for an at task that came due too
// late and for a run that lost the slot to an existing row.
func claimOne(ctx context.Context, tx *gorm.DB, task *repository.Task, now time.Time) (*ClaimedRun, bool, error) {
	// A run requested by hand carries its own instant and never moves
	// next_run_at: a manual run is one extra run, not a replacement for the slot
	// the schedule already has. Moving it would drift an every task's phase and
	// skip a cron task's next fire.
	manual := task.RunNowRequestedAt != nil
	trigger := repository.TaskRunTriggerSchedule
	slot := now.Truncate(time.Second)
	switch {
	case manual:
		slot = *task.RunNowRequestedAt
		trigger = repository.TaskRunTriggerManual
	case task.NextRunAt != nil:
		slot = *task.NextRunAt
	default:
		// An enabled task always carries a next_run_at, so this is a row that broke
		// that rule. Dereferencing nil would panic every claim and stop the whole
		// dispatcher, so it is switched off instead and left to the user: a task
		// with no schedule can never become due, and leaving it selected would
		// spend one of the claim's slots on it every tick.
		log.Printf("service: task %s is enabled without a next_run_at, disabling it", task.ID)
		if err := tx.Model(&repository.Task{}).Where("id = ?", task.ID).
			Updates(map[string]any{"enabled": false, "run_now_requested_at": nil}).Error; err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	// An at task whose instant is older than the grace window is not dispatched:
	// running it now would be firing a reminder that already stopped being true.
	// The miss is recorded and the task switched off, so it does not stay due.
	// A manual run is asked for now, so it is never too late.
	if !manual && task.ScheduleKind == repository.TaskScheduleAt && task.RunAt != nil && task.RunAt.Before(now.Add(-config.TaskAtGrace)) {
		if err := recordFinishedRun(tx, task, slot, trigger, repository.TaskRunSkipped, TaskRunErrorMissed, now); err != nil {
			return nil, false, err
		}
		return nil, false, advanceNextRunAt(tx, task, now)
	}

	agent, err := repository.FindAgentNamed(ctx, tx, task.UserID, task.AgentName)
	if err != nil {
		if err := recordFinishedRun(tx, task, slot, trigger, repository.TaskRunError, TaskRunErrorAgentNotFound, now); err != nil {
			return nil, false, err
		}
		return nil, false, settleTask(tx, task, manual, now)
	}

	provider, err := repository.FindProviderNamed(ctx, tx, task.UserID, task.ProviderName)
	if err != nil {
		if err := recordFinishedRun(tx, task, slot, trigger, repository.TaskRunError, TaskRunErrorProviderNotFound, now); err != nil {
			return nil, false, err
		}
		return nil, false, settleTask(tx, task, manual, now)
	}

	// The session is created here rather than through POST /api/sessions: the
	// run has no user credential to create one with, and the run is what owns it.
	session := &repository.Session{
		ID:      uuid.New(),
		UserID:  task.UserID,
		Title:   task.Name,
		Source:  TaskSessionSource,
		AgentID: &agent.ID,
	}
	if err := tx.Create(session).Error; err != nil {
		return nil, false, err
	}

	run := &repository.TaskRun{
		ID:          uuid.New(),
		TaskID:      task.ID,
		UserID:      task.UserID,
		Status:      repository.TaskRunRunning,
		Trigger:     trigger,
		ScheduledAt: slot,
		StartedAt:   &now,
		SessionID:   &session.ID,
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "task_id"}, {Name: "scheduled_at"}},
		DoNothing: true,
	}).Create(run)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		// The slot was taken by an earlier run. Nothing will ever write into the
		// session this attempt opened, so it is removed rather than left behind.
		if err := tx.Delete(&repository.Session{}, "id = ?", session.ID).Error; err != nil {
			return nil, false, err
		}
		return nil, false, settleTask(tx, task, manual, now)
	}

	if err := settleTask(tx, task, manual, now); err != nil {
		return nil, false, err
	}

	token, err := utils.SignRunToken(task.UserID, task.ID, session.ID, []byte(config.SecretKey), config.TaskRunTimeout+config.TaskRunTokenBuffer)
	if err != nil {
		return nil, false, err
	}

	return &ClaimedRun{
		RunID:      run.ID,
		UserID:     task.UserID,
		Task:       *task,
		Agent:      *agent,
		Provider:   *provider,
		SessionID:  session.ID,
		ActorToken: token,
	}, true, nil
}

// settleTask closes a claimed task's side of the transaction: a manual run only
// clears the request that asked for it, while a scheduled run advances the
// schedule. Both happen in the claim transaction, so neither can be lost and
// neither can be repeated.
func settleTask(tx *gorm.DB, task *repository.Task, manual bool, now time.Time) error {
	if !manual {
		return advanceNextRunAt(tx, task, now)
	}
	return tx.Model(&repository.Task{}).Where("id = ?", task.ID).
		Update("run_now_requested_at", nil).Error
}

// recordFinishedRun writes a run row that starts and ends at the same instant,
// which is what a claim that refuses to dispatch leaves behind. A slot that
// already carries a run is left alone.
func recordFinishedRun(tx *gorm.DB, task *repository.Task, slot time.Time, trigger, status, reason string, now time.Time) error {
	run := &repository.TaskRun{
		ID:          uuid.New(),
		TaskID:      task.ID,
		UserID:      task.UserID,
		Status:      status,
		Trigger:     trigger,
		ScheduledAt: slot,
		StartedAt:   &now,
		FinishedAt:  &now,
		Error:       reason,
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "task_id"}, {Name: "scheduled_at"}},
		DoNothing: true,
	}).Create(run).Error
}

// advanceNextRunAt moves a task past the slot it was just claimed at. It is the
// only writer of next_run_at on a claimed task, and it runs in the claim
// transaction, so a task is never due twice for one slot.
//
// An at task has no second slot, so it is switched off. An every task takes the
// first slot of its grid after now, which is what keeps a backlog from
// accumulating and keeps the task on the minute rather than drifting. A cron expression that never fires again also switches the
// task off rather than leaving it due forever.
func advanceNextRunAt(tx *gorm.DB, task *repository.Task, now time.Time) error {
	updates := map[string]any{}

	switch task.ScheduleKind {
	case repository.TaskScheduleAt:
		updates["enabled"] = false
		updates["next_run_at"] = nil

	case repository.TaskScheduleEvery:
		if task.EverySeconds == nil {
			updates["enabled"] = false
			updates["next_run_at"] = nil
			break
		}
		updates["next_run_at"] = NextEverySlot(now, time.Duration(*task.EverySeconds)*time.Second)

	case repository.TaskScheduleCron:
		next, err := nextCronForTask(task, now)
		if err != nil {
			log.Printf("service: task %s has no next run, disabling it: %v", task.ID, err)
			updates["enabled"] = false
			updates["next_run_at"] = nil
			break
		}
		updates["next_run_at"] = next

	default:
		updates["enabled"] = false
		updates["next_run_at"] = nil
	}

	return tx.Model(&repository.Task{}).Where("id = ?", task.ID).Updates(updates).Error
}

// nextCronForTask evaluates a cron task's expression in the task's own zone.
func nextCronForTask(task *repository.Task, now time.Time) (time.Time, error) {
	if task.CronExpr == nil {
		return time.Time{}, &ValidationError{Message: "cron_expr is required for a cron task."}
	}
	location, err := LoadTaskLocation(task.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	schedule, err := ParseTaskCron(*task.CronExpr, location)
	if err != nil {
		return time.Time{}, err
	}
	return NextCronRun(schedule, now)
}

// FinalizeTaskRunInput is the body of the internal finalize call.
type FinalizeTaskRunInput struct {
	Status     string
	Error      string
	SessionID  *uuid.UUID
	FinishedAt *time.Time
}

// taskRunTerminalStatuses are the states a run may be finalized into. `running`
// is deliberately absent: it is what a run is finalized from, never to.
var taskRunTerminalStatuses = map[string]bool{
	repository.TaskRunOK:        true,
	repository.TaskRunError:     true,
	repository.TaskRunCancelled: true,
	repository.TaskRunSkipped:   true,
	repository.TaskRunLost:      true,
}

// FinalizeTaskRun moves one running run to a terminal state.
//
// The update is guarded on status = 'running', so a run can only ever leave
// running once: a late or repeated finalize matches no row and is reported as a
// conflict rather than overwriting the result the first call recorded. The
// task_id is part of the guard, so a run id cannot be finalized through another
// task's path.
func FinalizeTaskRun(ctx context.Context, taskID, runID uuid.UUID, in FinalizeTaskRunInput) (*repository.TaskRun, error) {
	if !taskRunTerminalStatuses[in.Status] {
		return nil, &ValidationError{Message: "status must be one of ok, error, cancelled, skipped or lost."}
	}

	finishedAt := time.Now().UTC()
	if in.FinishedAt != nil {
		finishedAt = in.FinishedAt.UTC()
	}

	updates := map[string]any{
		"status":      in.Status,
		"error":       in.Error,
		"finished_at": finishedAt,
	}
	if in.SessionID != nil {
		updates["session_id"] = *in.SessionID
	}

	result := config.DB.WithContext(ctx).
		Model(&repository.TaskRun{}).
		Where("id = ? AND task_id = ? AND status = ?", runID, taskID, repository.TaskRunRunning).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrTaskRunAlreadyFinal
	}

	run := &repository.TaskRun{}
	if err := config.DB.WithContext(ctx).First(run, "id = ? AND task_id = ?", runID, taskID).Error; err != nil {
		return nil, err
	}
	return run, nil
}
