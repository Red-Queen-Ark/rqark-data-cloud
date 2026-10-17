package service

import (
	"strings"
	"testing"
	"time"
)

// TestParseTaskCronRefusesDescriptorsAndTimezones covers the expressions the
// parser must not accept: descriptors, second-granularity gaps, and an
// expression that tries to carry its own timezone instead of using the task's.
func TestParseTaskCronRefusesDescriptorsAndTimezones(t *testing.T) {
	for _, expr := range []string{
		"@daily",
		"@every 1h30m",
		"@every 1s",
		"TZ=Asia/Hong_Kong 0 9 * * *",
		"CRON_TZ=Asia/Hong_Kong 0 9 * * *",
		"0 9 * *",
		"0 9 * * * *",
		"",
	} {
		t.Run(expr, func(t *testing.T) {
			if _, err := ParseTaskCron(expr, time.UTC); err == nil {
				t.Fatalf("ParseTaskCron(%q) was accepted", expr)
			}
		})
	}
}

// TestParseTaskCronUsesTheGivenLocation covers the zone: the expression says
// nine o'clock, and the task's zone decides which instant that is.
func TestParseTaskCronUsesTheGivenLocation(t *testing.T) {
	hongKong, err := time.LoadLocation("Asia/Hong_Kong")
	if err != nil {
		t.Fatalf("load Asia/Hong_Kong: %v", err)
	}

	schedule, err := ParseTaskCron("0 9 * * *", hongKong)
	if err != nil {
		t.Fatalf("ParseTaskCron: %v", err)
	}

	from := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	next, err := NextCronRun(schedule, from)
	if err != nil {
		t.Fatalf("NextCronRun: %v", err)
	}

	inHongKong := next.In(hongKong)
	if inHongKong.Hour() != 9 || inHongKong.Minute() != 0 {
		t.Errorf("next = %s, want 09:00 in Asia/Hong_Kong", inHongKong)
	}
	if got := next.UTC(); got.Hour() != 1 || got.Day() != 18 {
		t.Errorf("next = %s UTC, want 01:00 on the 18th", got)
	}
}

// TestParseTaskCronReportsAnExpressionThatNeverFires covers the guard robfig
// leaves to its caller: a day-of-month that no month has yields the zero time
// rather than an error.
func TestParseTaskCronReportsAnExpressionThatNeverFires(t *testing.T) {
	schedule, err := ParseTaskCron("0 0 30 2 *", time.UTC)
	if err != nil {
		t.Fatalf("ParseTaskCron: %v", err)
	}

	if _, err := NextCronRun(schedule, time.Now()); err == nil {
		t.Fatal("NextCronRun accepted an expression that never fires")
	} else if !strings.Contains(err.Error(), "never fires") {
		t.Errorf("error = %v, want the never-fires message", err)
	}
}

// TestLoadTaskLocation covers the zone names a task may carry: the empty name is
// UTC, the column default, and an unknown name is a validation error rather than
// a silent fallback.
func TestLoadTaskLocation(t *testing.T) {
	location, err := LoadTaskLocation("")
	if err != nil {
		t.Fatalf("LoadTaskLocation of the empty name: %v", err)
	}
	if location != time.UTC {
		t.Errorf("location = %v, want UTC", location)
	}

	if _, err := LoadTaskLocation("Mars/Olympus_Mons"); err == nil {
		t.Error("LoadTaskLocation accepted an unknown zone")
	}
}
