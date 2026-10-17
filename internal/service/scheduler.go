package service

import (
	"strings"
	"time"
	// The IANA database is embedded rather than read from the host. The server
	// image is alpine, which ships no /usr/share/zoneinfo, so without this every
	// zone name is rejected — Asia/Hong_Kong included — and only UTC resolves.
	// Embedding it also means the answer cannot differ between a developer
	// machine and a container.
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

// taskCronParser accepts a plain five-field crontab line: minute, hour, day of
// month, month and day of week. Descriptors are left out on purpose, so `@daily`
// and `@every 1s` are refused rather than silently expanded: the first says
// nothing a five-field line cannot, and the second is a second-granularity
// schedule this scheduler cannot honour.
var taskCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// LoadTaskLocation resolves the IANA zone a task's cron expression is evaluated
// in. An empty name means UTC, which is what the column defaults to.
func LoadTaskLocation(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, &ValidationError{Message: "timezone must be an IANA zone name, such as Asia/Hong_Kong."}
	}
	return location, nil
}

// ParseTaskCron compiles a cron expression and pins it to location.
//
// The expression may not carry a timezone of its own: robfig's parser reads a
// `TZ=` or `CRON_TZ=` prefix itself, and would then be evaluated in a zone the
// task's timezone column never mentions. A task names its zone in that column
// alone, so an expression that tries to set one is refused.
//
// Parse does not apply a zone either: it leaves the schedule on time.Local, so
// the location has to be assigned afterwards. Doing it here keeps the one place
// that knows about cron from forgetting it.
func ParseTaskCron(expr string, location *time.Location) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, &ValidationError{Message: "cron_expr is required for a cron task."}
	}
	if strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=") {
		return nil, &ValidationError{Message: "cron_expr must not set a timezone; use timezone instead."}
	}

	schedule, err := taskCronParser.Parse(expr)
	if err != nil {
		return nil, &ValidationError{Message: "cron_expr is not a valid five-field cron expression."}
	}
	spec, ok := schedule.(*cron.SpecSchedule)
	if !ok {
		// The parser above cannot produce anything else, but a future change to
		// it should fail loudly rather than schedule in the wrong zone.
		return nil, &ValidationError{Message: "cron_expr is not a valid five-field cron expression."}
	}
	spec.Location = location

	return spec, nil
}

// NextCronRun returns the first instant after now that schedule fires at, or a
// validation error when it never fires again. robfig reports "never" as the zero
// time once it has looked five years ahead, which is what catches an expression
// like `0 0 30 2 *`.
func NextCronRun(schedule cron.Schedule, now time.Time) (time.Time, error) {
	next := schedule.Next(now)
	if next.IsZero() {
		return time.Time{}, &ValidationError{Message: "cron_expr never fires; check the day-of-month and month fields."}
	}
	return next, nil
}

// NextEverySlot returns the first instant strictly after now on the grid a gap
// of every seconds defines.
//
// The grid is anchored at the Unix epoch rather than at the moment the task was
// written, so two tasks with the same gap fire at the same instants and a gap
// that divides an hour or a day lands on a whole minute, hour or day: a task
// that repeats every 60 seconds fires exactly on each minute, and one that
// repeats every hour fires on the hour.
//
// Counting from the moment a task happened to be claimed would let its phase
// drift by a second or two on every tick, so a task meant to run at nine
// o'clock would wander further from nine o'clock all day.
func NextEverySlot(now time.Time, every time.Duration) time.Time {
	step := int64(every / time.Second)
	if step <= 0 {
		step = 1
	}

	seconds := now.UTC().Truncate(time.Second).Unix()
	remainder := seconds % step
	if remainder < 0 {
		remainder += step
	}
	return time.Unix(seconds-remainder+step, 0).UTC()
}
